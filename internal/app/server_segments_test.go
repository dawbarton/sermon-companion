package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func TestManualSegmentArchiveAndRestore(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Segment API test", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status, s.Duration = "stopped", 120
		s.Export = &store.ExportInfo{Status: "completed", StartedAt: time.Now(), Output: "exports/sermon.mp3"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	handler := NewServer(c, sessions, capture.New(c, sessions), master.New(c, sessions), StaticFiles).Handler()

	created := requestSession(t, handler, http.MethodPost, "/api/sessions/"+session.ID+"/segments/manual", `{"label":"Notices","startSeconds":30,"endSeconds":45}`)
	if len(created.Segments) != 1 || created.Segments[0].Label != "Notices" || created.Segments[0].Kind != "notices" || !created.Segments[0].Include {
		t.Fatalf("unexpected created segment: %#v", created.Segments)
	}
	if created.Segments[0].StartFrame != 1_440_000 || created.Segments[0].EndFrame == nil || *created.Segments[0].EndFrame != 2_160_000 {
		t.Fatalf("manual segment was not mapped to audio frames: %#v", created.Segments[0])
	}
	if created.Export == nil || created.Export.Status != "stale" {
		t.Fatalf("completed export was not invalidated: %#v", created.Export)
	}
	segmentID := created.Segments[0].ID

	archived := requestSession(t, handler, http.MethodDelete, "/api/sessions/"+session.ID+"/segments/"+segmentID, "")
	if !archived.Segments[0].Archived {
		t.Fatal("segment was not archived")
	}

	restored := requestSession(t, handler, http.MethodPost, "/api/sessions/"+session.ID+"/segments/"+segmentID+"/restore", `{}`)
	if restored.Segments[0].Archived {
		t.Fatal("segment was not restored")
	}
	events, err := sessions.Events(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-3].Type != "segment.added_manually" || events[len(events)-2].Type != "segment.archived" || events[len(events)-1].Type != "segment.restored" {
		t.Fatalf("unexpected event history: %#v", events)
	}
}

func TestSessionMetadataAndOpenExportFolder(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Old title", "Old Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status = "stopped"
		s.Export = &store.ExportInfo{Status: "completed", StartedAt: time.Now(), Output: "exports/old.mp3"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	server := NewServer(c, sessions, capture.New(c, sessions), master.New(c, sessions), StaticFiles)
	opened := ""
	server.openFolder = func(path string) error { opened = path; return nil }
	handler := server.Handler()

	updated := requestSession(t, handler, http.MethodPatch, "/api/sessions/"+session.ID, `{"title":"Sunday Eucharist","church":"St Mary's Church"}`)
	if updated.Title != "Sunday Eucharist" || updated.Church != "St Mary's Church" {
		t.Fatalf("unexpected metadata: %#v", updated)
	}
	if updated.Export == nil || updated.Export.Status != "stale" {
		t.Fatalf("metadata change did not invalidate export: %#v", updated.Export)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/sessions/"+session.ID+"/open-export-folder", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", response.Code, response.Body.String())
	}
	dir, _ := sessions.SessionDir(session.ID)
	if opened != filepath.Join(dir, "exports") {
		t.Fatalf("opened %q", opened)
	}
}

func TestKindFromLabel(t *testing.T) {
	for label, want := range map[string]string{"Notices": "notices", "Bible Reading": "bible-reading", "Q&A": "q-a", "St Mary's": "st-marys", "!!!": "custom"} {
		if got := kindFromLabel(label); got != want {
			t.Errorf("kindFromLabel(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestManualSegmentValidatesRecordingBounds(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Segment API test", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error { s.Status, s.Duration = "stopped", 20; return nil })
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	handler := NewServer(c, sessions, capture.New(c, sessions), master.New(c, sessions), StaticFiles).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/sessions/"+session.ID+"/segments/manual", strings.NewReader(`{"label":"Invalid","startSeconds":10,"endSeconds":30}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, body=%s", response.Code, response.Body.String())
	}
}

func TestSegmentAPIRejectsOverlapsAndAllowsTouchingBoundaries(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Overlap test", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error { s.Status, s.Duration = "stopped", 100; return nil })
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	handler := NewServer(c, sessions, capture.New(c, sessions), master.New(c, sessions), StaticFiles).Handler()
	requestSession(t, handler, http.MethodPost, "/api/sessions/"+session.ID+"/segments/manual", `{"label":"First","startSeconds":10,"endSeconds":20}`)
	second := requestSession(t, handler, http.MethodPost, "/api/sessions/"+session.ID+"/segments/manual", `{"label":"Second","startSeconds":30,"endSeconds":40}`)
	secondID := second.Segments[1].ID

	requestError(t, handler, http.MethodPost, "/api/sessions/"+session.ID+"/segments/manual", `{"label":"Overlap","startSeconds":15,"endSeconds":25}`, http.StatusBadRequest, "overlaps")
	requestError(t, handler, http.MethodPatch, "/api/sessions/"+session.ID+"/segments/"+secondID, `{"startSeconds":19,"endSeconds":40}`, http.StatusBadRequest, "overlaps")
	touching := requestSession(t, handler, http.MethodPatch, "/api/sessions/"+session.ID+"/segments/"+secondID, `{"startSeconds":20,"endSeconds":40}`)
	if touching.Segments[1].Start != 20 {
		t.Fatalf("touching boundary was not accepted: %#v", touching.Segments)
	}

	_, err = sessions.Update(session.ID, "test.force_overlap", nil, func(s *store.Session) error {
		s.Segments[1].Start = 19
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	requestError(t, handler, http.MethodPost, "/api/sessions/"+session.ID+"/export", `{}`, http.StatusBadRequest, "overlaps")
}

func requestError(t *testing.T, handler http.Handler, method, path, body string, wantStatus int, wantText string) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus || !strings.Contains(response.Body.String(), wantText) {
		t.Fatalf("%s %s: status=%d, body=%s", method, path, response.Code, response.Body.String())
	}
}

func requestSession(t *testing.T, handler http.Handler, method, path, body string) store.Session {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code < 200 || response.Code >= 300 {
		t.Fatalf("%s %s: status=%d, body=%s", method, path, response.Code, response.Body.String())
	}
	var session store.Session
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return session
}
