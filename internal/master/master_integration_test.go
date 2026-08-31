package master

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func TestTwoPassPerSegmentExport(t *testing.T) {
	ffmpeg, ffprobe := ffmpegTools(t)
	sessions, session := exportFixture(t, ffmpeg)
	dir, _ := sessions.SessionDir(session.ID)
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
	// Two two-second segments with the configured two seconds of silence between
	// them, and no silence added before the first or after the last.
	if got := probeDuration(t, ffprobe, output); math.Abs(got-6) > .3 {
		t.Fatalf("MP3 duration = %g s, want about 6 s", got)
	}
}

// The service's own gap overrides the configured one, and the ceiling is
// applied after the loudness normalisation: an integrated target of -6 LUFS
// puts a sine well above -12 dBFS unless the limiter pulls it back.
func TestExportHonoursTheServiceGapAndPeakCeiling(t *testing.T) {
	ffmpeg, ffprobe := ffmpegTools(t)
	sessions, session := exportFixture(t, ffmpeg)
	dir, _ := sessions.SessionDir(session.ID)
	gap := 3.0
	if _, err := sessions.Update(session.ID, "test.gap", nil, func(s *store.Session) error { s.GapSeconds = &gap; return nil }); err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	c.FFmpeg = ffmpeg
	c.Master.IntegratedLUFS = -6
	ceiling := -12.0
	c.Master.PeakLimitDB = &ceiling
	if err := New(c, sessions).Export(session.ID); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "exports", "2026-08-30-St-Marys-Church.mp3")
	if got := probeDuration(t, ffprobe, output); math.Abs(got-7) > .3 {
		t.Fatalf("MP3 duration = %g s, want about 7 s", got)
	}
	peak := probePeakDBFS(t, ffmpeg, output)
	if peak > -11 || peak < -14 {
		t.Fatalf("MP3 peak = %g dBFS, want about -12 dBFS", peak)
	}
}

func ffmpegTools(t *testing.T) (string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("FFprobe is not installed")
	}
	return ffmpeg, ffprobe
}

// exportFixture is a stopped service holding four seconds of tone marked as two
// consecutive two-second segments.
func exportFixture(t *testing.T, ffmpeg string) (*store.Store, *store.Session) {
	t.Helper()
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
	return sessions, session
}

func probeDuration(t *testing.T, ffprobe, path string) float64 {
	t.Helper()
	output, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("probe %s: %v", path, err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		t.Fatalf("parse duration %q: %v", output, err)
	}
	return seconds
}

func probePeakDBFS(t *testing.T, ffmpeg, path string) float64 {
	t.Helper()
	output, err := exec.Command(ffmpeg, "-hide_banner", "-nostats", "-i", path, "-af", "volumedetect", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("measure %s: %v\n%s", path, err, output)
	}
	match := regexp.MustCompile(`max_volume: (-?[0-9.]+) dB`).FindSubmatch(output)
	if match == nil {
		t.Fatalf("FFmpeg reported no peak level for %s:\n%s", path, output)
	}
	peak, err := strconv.ParseFloat(string(match[1]), 64)
	if err != nil {
		t.Fatalf("parse peak %q: %v", match[1], err)
	}
	return peak
}
