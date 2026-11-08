package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

// finishedSessionWithMP3 is a stopped two-minute service with one sermon from
// 30 to 60 seconds and a current MP3.
func finishedSessionWithMP3(t *testing.T) (http.Handler, *store.Store, string) {
	t.Helper()
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Edits", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	end, endSeconds := uint64(60*48_000), 60.0
	if _, err := sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status, s.Duration = "stopped", 120
		s.Segments = []store.Segment{{ID: "seg_sermon", Kind: "sermon", Label: "Sermon", StartFrame: 30 * 48_000, Start: 30, EndFrame: &end, End: &endSeconds, Include: true}}
		s.Export = &store.ExportInfo{Status: "completed", StartedAt: time.Now(), Output: "exports/sermon.mp3"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	settings := config.NewSettings("", c)
	return NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles).Handler(), sessions, session.ID
}

// Clicking a segment on the waveform is a drag that goes nowhere, and sends
// the times it already has. That must not make the MP3 out of date.
func TestAnUnchangedSegmentLeavesTheMP3Current(t *testing.T) {
	handler, _, id := finishedSessionWithMP3(t)
	path := "/api/sessions/" + id + "/segments/seg_sermon"
	unchanged := requestSession(t, handler, http.MethodPatch, path, `{"startSeconds":30,"endSeconds":60,"label":"Sermon","include":true}`)
	if unchanged.Export == nil || unchanged.Export.Status != "completed" {
		t.Fatalf("an unchanged segment made the MP3 %#v", unchanged.Export)
	}
	moved := requestSession(t, handler, http.MethodPatch, path, `{"startSeconds":31}`)
	if moved.Export == nil || moved.Export.Status != "stale" {
		t.Fatalf("a moved segment left the MP3 %#v", moved.Export)
	}
}

// The journal keeps what was asked for beside what was committed, so a
// snapped end must not overwrite the requested one.
func TestManualSegmentJournalKeepsTheRequestedEnd(t *testing.T) {
	handler, sessions, id := finishedSessionWithMP3(t)
	requestSession(t, handler, http.MethodPost, "/api/sessions/"+id+"/segments/manual", `{"label":"Notices","startSeconds":100,"endSeconds":119.97}`)
	events, err := sessions.Events(id)
	if err != nil {
		t.Fatal(err)
	}
	payload := events[len(events)-1].Payload.(map[string]any)
	requested := payload["requested"].(map[string]any)
	after := payload["after"].(map[string]any)
	if requested["endFrame"] != float64(5_758_560) || after["endFrame"] != float64(5_760_000) {
		t.Fatalf("requested end frame %v, committed %v", requested["endFrame"], after["endFrame"])
	}
}
