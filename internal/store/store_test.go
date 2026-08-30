package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreKeepsSnapshotAndAppendOnlyEvents(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	session, err := sessions.Create("Test service", "St Mary's", now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sessions.Update(session.ID, "test.changed", map[string]string{"field": "status"}, func(s *Session) error { s.Status = "stopped"; return nil })
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "stopped" || loaded.Church != "St Mary's" || loaded.Revision != 2 {
		t.Fatalf("unexpected snapshot: %#v", loaded)
	}
	events, err := sessions.Events(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "session.created" || events[1].Type != "test.changed" {
		t.Fatalf("unexpected events: %#v", events)
	}
	dir, _ := sessions.SessionDir(session.ID)
	if _, err := os.Stat(filepath.Join(dir, "session.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temporary snapshot remains: %v", err)
	}
}

func TestSessionDirRejectsTraversal(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../outside", "a/b", `a\\b`, ""} {
		if _, err := sessions.SessionDir(id); err == nil {
			t.Errorf("accepted invalid ID %q", id)
		}
	}
}
