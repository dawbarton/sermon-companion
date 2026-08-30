package capture

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func recordedSession(t *testing.T, sessions *store.Store, title string, ended time.Time) *store.Session {
	t.Helper()
	session, err := sessions.Create(title, "Test Church", ended.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	for _, name := range []string{"audio.flac", "waveform-20pps.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "exports", "sermon.mp3"), []byte("mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status, s.AudioFile, s.EndedAt = "stopped", "audio.flac", &ended
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestApplyRetentionRemovesOnlyExpiredRecordings(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := recordedSession(t, sessions, "Old service", now.AddDate(0, 0, -90))
	recent := recordedSession(t, sessions, "Recent service", now.AddDate(0, 0, -5))

	c := config.DefaultConfig()
	removed, err := New(c, sessions).ApplyRetention(now)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d recordings, want 1", removed)
	}

	oldDir, _ := sessions.SessionDir(old.ID)
	if _, err := os.Stat(filepath.Join(oldDir, "audio.flac")); !os.IsNotExist(err) {
		t.Fatal("the expired recording was kept")
	}
	if _, err := os.Stat(filepath.Join(oldDir, "waveform-20pps.json")); !os.IsNotExist(err) {
		t.Fatal("the cached waveform of an expired recording was kept")
	}
	if _, err := os.Stat(filepath.Join(oldDir, "exports", "sermon.mp3")); err != nil {
		t.Fatalf("the published MP3 was removed: %v", err)
	}
	reloaded, err := sessions.Get(old.ID)
	if err != nil || reloaded.AudioRemovedAt == nil {
		t.Fatalf("removal was not recorded: %#v, %v", reloaded, err)
	}

	recentDir, _ := sessions.SessionDir(recent.ID)
	if _, err := os.Stat(filepath.Join(recentDir, "audio.flac")); err != nil {
		t.Fatalf("a recording within the retention period was removed: %v", err)
	}

	// A second pass must not repeat the work or the journal entry.
	again, err := New(c, sessions).ApplyRetention(now)
	if err != nil || again != 0 {
		t.Fatalf("second pass removed %d recordings: %v", again, err)
	}
}

func TestApplyRetentionKeepsEverythingWhenDisabled(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := recordedSession(t, sessions, "Ancient service", now.AddDate(-2, 0, 0))
	c := config.DefaultConfig()
	c.RetentionDays = nil
	removed, err := New(c, sessions).ApplyRetention(now)
	if err != nil || removed != 0 {
		t.Fatalf("removed %d recordings with retention disabled: %v", removed, err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	if _, err := os.Stat(filepath.Join(dir, "audio.flac")); err != nil {
		t.Fatalf("recording removed with retention disabled: %v", err)
	}
}
