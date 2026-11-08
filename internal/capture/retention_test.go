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

func TestApplyRetentionDeletesOnlyExpiredServices(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := recordedSession(t, sessions, "Old service", now.AddDate(0, 0, -90))
	recent := recordedSession(t, sessions, "Recent service", now.AddDate(0, 0, -5))

	c := config.DefaultConfig()
	deleted, err := New(config.NewSettings("", c), sessions).ApplyRetention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != old.ID {
		t.Fatalf("deleted %v, want [%s]", deleted, old.ID)
	}

	oldDir, _ := sessions.SessionDir(old.ID)
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("the expired service directory was kept")
	}
	if _, err := sessions.Get(old.ID); err == nil {
		t.Fatal("the expired service is still listed")
	}

	recentDir, _ := sessions.SessionDir(recent.ID)
	for _, name := range []string{"audio.flac", "session.json", filepath.Join("exports", "sermon.mp3")} {
		if _, err := os.Stat(filepath.Join(recentDir, name)); err != nil {
			t.Fatalf("a service within the retention period lost %s: %v", name, err)
		}
	}

	again, err := New(config.NewSettings("", c), sessions).ApplyRetention(now)
	if err != nil || len(again) != 0 {
		t.Fatalf("second pass deleted %v: %v", again, err)
	}
}

func TestApplyRetentionKeepsEveryServiceWhenDisabled(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := recordedSession(t, sessions, "Ancient service", now.AddDate(-2, 0, 0))
	c := config.DefaultConfig()
	c.RetentionDays = nil
	deleted, err := New(config.NewSettings("", c), sessions).ApplyRetention(now)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("deleted %v with retention disabled: %v", deleted, err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	if _, err := os.Stat(filepath.Join(dir, "audio.flac")); err != nil {
		t.Fatalf("service deleted with retention disabled: %v", err)
	}
}

// Retention deletes a service's publish record with it. When an upload stopped
// part-way, that record is the only note of what it left on the website, so
// the deletion says what that was.
func TestRetentionSaysWhatAnUnfinishedUploadLeft(t *testing.T) {
	session := &store.Session{Publish: &store.PublishInfo{Site: "https://church.example", Progress: store.PublishProgress{
		PostID: 41, MediaID: 42, Speakers: map[string]int{"Ann Other": 7},
	}}}
	want := `its upload to https://church.example did not finish and left draft talk 41, MP3 42, speaker "Ann Other" (7) there`
	if got := unfinishedUpload(session); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	session.Publish.Progress.Finished = true
	if got := unfinishedUpload(session); got != "" {
		t.Fatalf("a finished upload was reported: %q", got)
	}
	if got := unfinishedUpload(&store.Session{}); got != "" {
		t.Fatalf("a service never uploaded was reported: %q", got)
	}
}
