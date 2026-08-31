package master

import (
	"fmt"
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
	if got := probeChannels(t, ffprobe, output); got != 1 {
		t.Fatalf("MP3 channels = %d, want mono", got)
	}
	// The downmix happens before the loudness is measured. Folding two identical
	// channels afterwards would leave the MP3 about 3 LU below the target.
	if got := probeLoudnessLUFS(t, ffmpeg, output); math.Abs(got-c.Master.IntegratedLUFS) > 1.5 {
		t.Fatalf("MP3 loudness = %g LUFS, want about %g LUFS", got, c.Master.IntegratedLUFS)
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
	stereo := false
	c.Master.Mono = &stereo
	if err := New(c, sessions).Export(session.ID); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "exports", "2026-08-30-St-Marys-Church.mp3")
	if got := probeDuration(t, ffprobe, output); math.Abs(got-7) > .3 {
		t.Fatalf("MP3 duration = %g s, want about 7 s", got)
	}
	if got := probeChannels(t, ffprobe, output); got != 2 {
		t.Fatalf("MP3 channels = %d, want the captured stereo", got)
	}
	peak := probePeakDBFS(t, ffmpeg, output)
	if peak > -11 || peak < -14 {
		t.Fatalf("MP3 peak = %g dBFS, want about -12 dBFS", peak)
	}
}

// A sermon split in two, with a reading between the halves and the second half
// eight decibels quieter than the first. Levelling each piece on its own would
// bring the halves to the same loudness and leave a step at the cut; measuring
// them together keeps the difference the microphone recorded.
func TestSegmentsSharingALabelAreLevelledTogether(t *testing.T) {
	ffmpeg, ffprobe := ffmpegTools(t)
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Split sermon", "St Mary's Church", time.Date(2026, 8, 30, 10, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	audio := filepath.Join(dir, "audio.flac")
	// Three four-second sections: the first half of the talk, a quiet reading,
	// then the second half at -8 dB.
	generate := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000:duration=4",
		"-filter_complex", "[1:a]volume=0.1[reading];[2:a]volume=0.398[second];[0:a][reading][second]concat=n=3:v=0:a=1[out]",
		"-map", "[out]", "-ac", "2", "-c:a", "flac", audio)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v\n%s", err, output)
	}
	seconds := []float64{4, 8, 12}
	frames := []uint64{192_000, 384_000, 576_000}
	gap, now := 0.0, time.Now().UTC()
	if _, err := sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status, s.AudioFile, s.Duration, s.GapSeconds = "stopped", "audio.flac", 12, &gap
		s.Capture = store.CaptureInfo{SampleRate: 48_000, Channels: 2, SampleFormat: "s16le", TotalFrames: 576_000}
		s.Segments = []store.Segment{
			{ID: "first", Kind: "sermon", Label: "Sermon", Start: 0, End: &seconds[0], EndFrame: &frames[0], Include: true, CreatedAt: now, UpdatedAt: now},
			{ID: "reading", Kind: "reading", Label: "Reading", StartFrame: frames[0], Start: 4, End: &seconds[1], EndFrame: &frames[1], Include: true, CreatedAt: now, UpdatedAt: now},
			{ID: "second", Kind: "sermon", Label: "Sermon", StartFrame: frames[1], Start: 8, End: &seconds[2], EndFrame: &frames[2], Include: true, CreatedAt: now, UpdatedAt: now},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	c.FFmpeg = ffmpeg
	if err := New(c, sessions).Export(session.ID); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "exports", "2026-08-30-St-Marys-Church.mp3")
	if got := probeDuration(t, ffprobe, output); math.Abs(got-12) > .3 {
		t.Fatalf("MP3 duration = %g s, want the three parts back to back", got)
	}
	// Measured away from the joins, where the encoder blurs the boundary.
	firstHalf := probeLoudnessRange(t, ffmpeg, output, .3, 3.7)
	reading := probeLoudnessRange(t, ffmpeg, output, 4.3, 7.7)
	secondHalf := probeLoudnessRange(t, ffmpeg, output, 8.3, 11.7)
	if step := (firstHalf - secondHalf) - 8; math.Abs(step) > 1.5 {
		t.Fatalf("halves of the sermon are %g LU apart, want the recorded 8 LU (%g, %g LUFS)", firstHalf-secondHalf, firstHalf, secondHalf)
	}
	// The reading carries its own label, so it is still levelled on its own.
	if math.Abs(reading-c.Master.IntegratedLUFS) > 1.5 {
		t.Fatalf("reading = %g LUFS, want about %g LUFS", reading, c.Master.IntegratedLUFS)
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

func probeChannels(t *testing.T, ffprobe, path string) int {
	t.Helper()
	output, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=channels", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("probe %s: %v", path, err)
	}
	channels, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatalf("parse channels %q: %v", output, err)
	}
	return channels
}

func probeLoudnessLUFS(t *testing.T, ffmpeg, path string) float64 {
	t.Helper()
	return measureLoudness(t, ffmpeg, path, "ebur128")
}

// probeLoudnessRange measures one stretch of a finished MP3, so the parts of a
// service can be compared with one another.
func probeLoudnessRange(t *testing.T, ffmpeg, path string, from, to float64) float64 {
	t.Helper()
	return measureLoudness(t, ffmpeg, path, fmt.Sprintf("atrim=start=%g:end=%g,asetpts=PTS-STARTPTS,ebur128", from, to))
}

func measureLoudness(t *testing.T, ffmpeg, path, filter string) float64 {
	t.Helper()
	output, err := exec.Command(ffmpeg, "-hide_banner", "-nostats", "-i", path, "-af", filter, "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("measure %s: %v\n%s", path, err, output)
	}
	// ebur128 reports a running figure as it goes; the last one is the summary
	// for the whole file.
	matches := regexp.MustCompile(`I:\s+(-?[0-9.]+) LUFS`).FindAllSubmatch(output, -1)
	if matches == nil {
		t.Fatalf("FFmpeg reported no integrated loudness for %s:\n%s", path, output)
	}
	last := matches[len(matches)-1][1]
	loudness, err := strconv.ParseFloat(string(last), 64)
	if err != nil {
		t.Fatalf("parse loudness %q: %v", last, err)
	}
	return loudness
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
