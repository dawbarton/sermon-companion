// Tests for the MP3's duration: the format the site's Audio info field holds,
// and a real FFprobe reading of a short MP3 when FFmpeg is installed.
package publish

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/proc"
)

func TestFormatDurationMatchesTheSitesAudioInfo(t *testing.T) {
	for _, test := range []struct {
		d    time.Duration
		want string
	}{
		{31*time.Minute + 12*time.Second + 400*time.Millisecond, "00:31:12"}, // as the site shows today's talk
		{time.Hour + 2*time.Minute + 3600*time.Millisecond, "01:02:04"},
		{0, "00:00:00"},
	} {
		if got := formatDuration(test.d); got != test.want {
			t.Errorf("formatDuration(%v) = %q, want %q", test.d, got, test.want)
		}
	}
}

func TestProbeDurationReadsARealMP3(t *testing.T) {
	ffmpeg, errFFmpeg := exec.LookPath("ffmpeg")
	ffprobe, errFFprobe := exec.LookPath("ffprobe")
	if errFFmpeg != nil || errFFprobe != nil {
		t.Skip("FFmpeg and FFprobe are needed on PATH for this test")
	}
	file := filepath.Join(t.TempDir(), "tone.mp3")
	encode := proc.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:a", "libmp3lame", "-q:a", "5", file)
	if output, err := encode.CombinedOutput(); err != nil {
		t.Fatalf("making the test MP3: %v\n%s", err, output)
	}
	duration, err := ProbeDuration(context.Background(), ffprobe, file)
	if err != nil {
		t.Fatal(err)
	}
	if duration < 1900*time.Millisecond || duration > 2200*time.Millisecond {
		t.Errorf("duration = %v, want about two seconds", duration)
	}
	if _, err := ProbeDuration(context.Background(), ffprobe, filepath.Join(t.TempDir(), "absent.mp3")); err == nil {
		t.Error("a missing file should be an error")
	}
}
