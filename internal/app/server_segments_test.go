package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	session, err := sessions.Create("Segment API test", time.Now())
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

	created := requestSession(t, handler, http.MethodPost, "/api/sessions/"+session.ID+"/segments/manual", `{"kind":"notices","label":"Notices","startSeconds":30,"endSeconds":45}`)
	if len(created.Segments) != 1 || created.Segments[0].Label != "Notices" || !created.Segments[0].Include {
		t.Fatalf("unexpected created segment: %#v", created.Segments)
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

func TestManualSegmentValidatesRecordingBounds(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Segment API test", time.Now())
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
