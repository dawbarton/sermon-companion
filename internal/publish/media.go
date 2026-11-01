// The MP3's duration, which the site shows beside the player. WordPress works
// it out itself when a file is chosen in the admin, but whether it does so for
// a talk saved over REST is unknown, so the publisher sends it. FFprobe is
// already a dependency of the application, and like every child process it is
// started through internal/proc.
package publish

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dawbarton/sermon-companion/internal/proc"
)

// ProbeDuration asks FFprobe how long the audio file at path lasts.
func ProbeDuration(ctx context.Context, ffprobe, path string) (time.Duration, error) {
	command := proc.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path)
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("FFprobe could not read the MP3's duration: %w", err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("FFprobe reported the MP3's duration as %q", strings.TrimSpace(string(output)))
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// formatDuration writes a duration as the site's Audio info field holds it,
// such as 00:31:12, rounded to the nearest second.
func formatDuration(d time.Duration) string {
	total := int64(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, total/60%60, total%60)
}
