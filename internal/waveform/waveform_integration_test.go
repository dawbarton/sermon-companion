package waveform

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/store"
)

func TestGenerateAndReuseEnvelope(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Waveform integration test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	audio := filepath.Join(dir, "audio.flac")
	command := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2", "-c:a", "flac", audio)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v\n%s", err, output)
	}
	_, err = sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status, s.AudioFile, s.Duration = "stopped", "audio.flac", 2
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	generator := New(ffmpeg, sessions)
	first, err := generator.Generate(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	peaks, err := base64.StdEncoding.DecodeString(first.PeaksBase64)
	if err != nil {
		t.Fatal(err)
	}
	if len(peaks) != 40 || first.PointsPerSecond != 20 {
		t.Fatalf("unexpected envelope: %d peaks at %d pps", len(peaks), first.PointsPerSecond)
	}
	cache := filepath.Join(dir, "waveform-20pps.json")
	before, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generator.Generate(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	if second.PeaksBase64 != first.PeaksBase64 || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("valid cache was not reused")
	}
}
