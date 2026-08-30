package capture

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func TestSyntheticCaptureStartsAndStopsCleanly(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("FFprobe is not installed")
	}
	c := config.DefaultConfig()
	c.FFmpeg, c.FFprobe = ffmpeg, ffprobe
	c.Capture.Driver, c.Capture.Device = "lavfi", "sine=frequency=440:sample_rate=48000"
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := New(c, sessions)
	session, err := manager.Start("Integration test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = sessions.Update(session.ID, "test.segment", nil, func(s *store.Session) error {
		s.Segments = append(s.Segments, store.Segment{ID: "test", Kind: "test", Label: "Test", Start: 0, Include: true, CreatedAt: now, UpdatedAt: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(350 * time.Millisecond)
	stopped, err := manager.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != "stopped" || stopped.Duration <= 0 {
		t.Fatalf("unexpected stopped session: %#v", stopped)
	}
	if len(stopped.Segments) != 1 || stopped.Segments[0].End == nil {
		t.Fatalf("open segment was not closed: %#v", stopped.Segments)
	}
	dir, _ := sessions.SessionDir(session.ID)
	if info, err := os.Stat(filepath.Join(dir, "audio.flac")); err != nil || info.Size() == 0 {
		t.Fatalf("lossless recording missing: %v", err)
	}
}
