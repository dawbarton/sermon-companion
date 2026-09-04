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

func TestSilenceBetweenSegmentsFallsBackToTheConfiguredDefault(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Gap test", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	completed := "completed"
	if _, err := sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status = "stopped"
		s.Export = &store.ExportInfo{Status: completed}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	gap := 4.0
	c.Master.GapSeconds = &gap
	settings := config.NewSettings("", c)
	handler := NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles).Handler()
	path := "/api/sessions/" + session.ID

	// A service recorded before the setting existed has no gap of its own, so
	// the review page is told the one an export would actually use.
	fetched := requestSession(t, handler, http.MethodGet, path, "")
	if fetched.GapSeconds == nil || *fetched.GapSeconds != 4 {
		t.Fatalf("inherited gap = %v", fetched.GapSeconds)
	}
	if stored, err := sessions.Get(session.ID); err != nil || stored.GapSeconds != nil {
		t.Fatalf("the inherited gap was written to the session: %v, %v", stored.GapSeconds, err)
	}

	patched := requestSession(t, handler, http.MethodPatch, path, `{"gapSeconds":3.25}`)
	if patched.GapSeconds == nil || *patched.GapSeconds != 3.3 {
		t.Fatalf("patched gap = %v", patched.GapSeconds)
	}
	// The MP3 would now sound different, so the one already made is not current.
	if patched.Export == nil || patched.Export.Status != "stale" {
		t.Fatalf("export after a gap change = %#v", patched.Export)
	}
	stored, err := sessions.Get(session.ID)
	if err != nil || stored.GapSeconds == nil || *stored.GapSeconds != 3.3 {
		t.Fatalf("stored gap = %v, %v", stored.GapSeconds, err)
	}

	requestError(t, handler, http.MethodPatch, path, `{"gapSeconds":45}`, http.StatusBadRequest, "between 0 and 30 seconds")
	requestError(t, handler, http.MethodPatch, path, `{"gapSeconds":-1}`, http.StatusBadRequest, "between 0 and 30 seconds")
	if unchanged, err := sessions.Get(session.ID); err != nil || *unchanged.GapSeconds != 3.3 {
		t.Fatalf("rejected gap was stored: %v, %v", unchanged.GapSeconds, err)
	}
}
