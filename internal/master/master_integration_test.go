package master

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func TestTwoPassPerSegmentExport(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	serviceDate := time.Date(2026, 8, 30, 10, 0, 0, 0, time.Local)
	session, err := sessions.Create("Mastering integration test", "St Mary's Church", serviceDate)
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	audio := filepath.Join(dir, "audio.flac")
	generate := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=4", "-ac", "2", "-c:a", "flac", audio)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v\n%s", err, output)
	}
	end2, end4 := 2.0, 4.0
	endFrame2, endFrame4 := uint64(96_000), uint64(192_000)
	now := time.Now().UTC()
	_, err = sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status, s.AudioFile, s.Duration = "stopped", "audio.flac", 4
		s.Capture = store.CaptureInfo{SampleRate: 48_000, Channels: 2, SampleFormat: "s16le", TotalFrames: 192_000}
		s.Segments = []store.Segment{
			{ID: "one", Kind: "reading", Label: "Reading", Start: 0, EndFrame: &endFrame2, End: &end2, Include: true, CreatedAt: now, UpdatedAt: now},
			{ID: "two", Kind: "sermon", Label: "Sermon", StartFrame: 96_000, EndFrame: &endFrame4, Start: 2, End: &end4, Include: true, CreatedAt: now, UpdatedAt: now},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	c.FFmpeg = ffmpeg
	if err := New(c, sessions).Export(session.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Export == nil || completed.Export.Status != "completed" {
		t.Fatalf("unexpected export: %#v", completed.Export)
	}
	if completed.Export.Output != "exports/2026-08-30-St-Marys-Church.mp3" {
		t.Fatalf("unexpected output name: %q", completed.Export.Output)
	}
	output := filepath.Join(dir, filepath.FromSlash(completed.Export.Output))
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		t.Fatalf("MP3 missing: %v", err)
	}
}
