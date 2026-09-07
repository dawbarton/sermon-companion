package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	listed, err := sessions.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatal("unreadable session was listed as healthy")
	}
	problems := sessions.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], session.ID) {
		t.Fatalf("session problems = %v", problems)
	}
}

func TestStoreFinishesCommittedStagedSnapshot(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Recover", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	candidate := clone(session)
	candidate.Revision++
	candidate.Status = "stopped"
	writeSnapshotForTest(t, filepath.Join(dir, stagedSnapshotName), candidate)
	appendEventForTest(t, filepath.Join(dir, journalName), Event{Sequence: candidate.Revision, At: time.Now(), Type: "test.committed", SessionID: session.ID})

	recovered, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Revision != candidate.Revision || recovered.Status != "stopped" {
		t.Fatalf("recovered snapshot = %#v", recovered)
	}
	if _, err := os.Stat(filepath.Join(dir, stagedSnapshotName)); !os.IsNotExist(err) {
		t.Fatalf("staged snapshot remains: %v", err)
	}
}

func TestStoreDiscardsStagedSnapshotWithoutAnEvent(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Recover", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	candidate := clone(session)
	candidate.Revision++
	candidate.Status = "stopped"
	writeSnapshotForTest(t, filepath.Join(dir, stagedSnapshotName), candidate)

	recovered, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Revision != session.Revision || recovered.Status != session.Status {
		t.Fatalf("uncommitted snapshot was published: %#v", recovered)
	}
	if _, err := os.Stat(filepath.Join(dir, stagedSnapshotName)); !os.IsNotExist(err) {
		t.Fatalf("uncommitted snapshot remains: %v", err)
	}
}

func TestStorePreservesAndRemovesTruncatedFinalEvent(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Recover", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	journal := filepath.Join(dir, journalName)
	file, err := os.OpenFile(journal, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"sequence":2`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	events, err := sessions.Events(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want the one complete event", len(events))
	}
	matches, err := filepath.Glob(journal + ".truncated-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("preserved truncated records = %v, %v", matches, err)
	}
	preserved, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(preserved) != `{"sequence":2` {
		t.Fatalf("preserved tail = %q", preserved)
	}
}

func TestStoreRejectsJournalAheadWithoutCandidate(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Recover", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	appendEventForTest(t, filepath.Join(dir, journalName), Event{Sequence: session.Revision + 1, At: time.Now(), Type: "test.orphaned", SessionID: session.ID})
	if _, err := sessions.Get(session.ID); err == nil {
		t.Fatal("journal ahead of its snapshot was accepted")
	}
}

func TestUpdateAtRevisionRejectsStaleWriter(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Original", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	expected := session.Revision
	if _, err := sessions.UpdateAtRevision(session.ID, &expected, "test.first", nil, func(s *Session) error {
		s.Title = "First"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.UpdateAtRevision(session.ID, &expected, "test.stale", nil, func(s *Session) error {
		s.Title = "Stale"
		return nil
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	stored, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != "First" {
		t.Fatalf("stale writer changed title to %q", stored.Title)
	}
}

func TestSchemaOneSessionMigratesTimesToFramesTransactionally(t *testing.T) {
	sessions, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Legacy", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	end := 2.5
	session.SchemaVersion = 1
	session.Capture = CaptureInfo{}
	session.Segments = []Segment{{ID: "one", Label: "Sermon", Start: 1.25, End: &end, Include: true}}
	session.Markers = []Marker{{ID: "mark", Label: "Note", At: 2}}
	dir, _ := sessions.SessionDir(session.ID)
	writeSnapshotForTest(t, filepath.Join(dir, snapshotName), session)

	migrated, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.SchemaVersion != SchemaVersion || migrated.Capture.SampleRate != legacySampleRate {
		t.Fatalf("migration metadata = schema %d, rate %d", migrated.SchemaVersion, migrated.Capture.SampleRate)
	}
	if migrated.Segments[0].StartFrame != 60_000 || migrated.Segments[0].EndFrame == nil || *migrated.Segments[0].EndFrame != 120_000 {
		t.Fatalf("migrated segment = %#v", migrated.Segments[0])
	}
	if migrated.Markers[0].AtFrame != 96_000 {
		t.Fatalf("migrated marker = %#v", migrated.Markers[0])
	}
	events, err := sessions.Events(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].Type != "session.schema_migrated" || events[len(events)-1].Sequence != migrated.Revision {
		t.Fatalf("migration event = %#v", events[len(events)-1])
	}
}

func writeSnapshotForTest(t *testing.T, path string, session *Session) {
	t.Helper()
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendEventForTest(t *testing.T, path string, event Event) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
