package store

import (
	"encoding/json"
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

func TestSessionFileRejectsPathsOutsideTheSession(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Path test", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path, err := sessions.SessionFile(session.ID, "exports/2026-01-01-Church.mp3")
	if err != nil {
		t.Fatalf("rejected a legitimate export path: %v", err)
	}
	if filepath.Base(path) != "2026-01-01-Church.mp3" {
		t.Fatalf("unexpected export path %q", path)
	}
	for _, stored := range []string{"", "  ", "../../etc/passwd", ".."} {
		if _, err := sessions.SessionFile(session.ID, stored); err == nil {
			t.Fatalf("accepted %q", stored)
		}
	}
}

func TestStoreRejectsSnapshotWhoseIDDoesNotMatchItsDirectory(t *testing.T) {
	root := t.TempDir()
	sessions, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Path test", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	path := filepath.Join(dir, "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, session); err != nil {
		t.Fatal(err)
	}
	session.ID = "../redirected"
	data, err = json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Get(filepath.Base(dir)); err == nil {
		t.Fatal("snapshot with redirected ID was accepted")
	}
	if _, err := sessions.Update(filepath.Base(dir), "test", nil, func(*Session) error { return nil }); err == nil {
		t.Fatal("redirected snapshot was updated")
	}
	if _, err := os.Stat(filepath.Join(root, "redirected")); !os.IsNotExist(err) {
		t.Fatalf("write escaped the sessions directory: %v", err)
	}
}

func TestStoreRejectsFutureSessionSchema(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Future", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	path := filepath.Join(dir, "session.json")
	session.SchemaVersion = SchemaVersion + 1
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Get(session.ID); err == nil {
		t.Fatal("future schema was accepted")
	}
}
