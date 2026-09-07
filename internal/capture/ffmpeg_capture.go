package capture

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/proc"
	"github.com/dawbarton/sermon-companion/internal/store"
)

// progressPeriod is how often the fallback FFmpeg process reports the position
// it has encoded. Positions are interpolated between reports, so this bounds
// the wait for a marker rather than its accuracy.
const progressPeriod = "0.2"

type ffmpegCapture struct {
	config       config.Config
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	clock        *frameClock
	started      time.Time
	partPath     string
	stopOnce     sync.Once
	first        chan struct{}
	firstOnce    sync.Once
	progressDone chan error
	done         chan captureResult
}

func startFFmpegCapture(c config.Config, partPath string, logFile *os.File) (activeCapture, error) {
	inputArgs, err := InputArgs(c.Capture)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-y", "-stats_period", progressPeriod, "-progress", "pipe:1"}
	args = append(args, inputArgs...)
	args = append(args, "-map", "0:a:0", "-vn", "-ac", strconv.Itoa(c.Capture.Channels), "-ar", strconv.Itoa(c.Capture.SampleRate), "-c:a", "flac", "-compression_level", "5", "-f", "flac", partPath)
	command := proc.Command(c.FFmpeg, args...)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	progress, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = logFile
	fmt.Fprintf(logFile, "\n[%s] starting fallback FFmpeg device capture: %s\n", time.Now().Format(time.RFC3339), printableCommand(c.FFmpeg, args))
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start FFmpeg device capture: %w", err)
	}
	capture := &ffmpegCapture{
		config: c, cmd: command, stdin: stdin, clock: newFrameClock(c.Capture.SampleRate),
		started: time.Now(), partPath: partPath, first: make(chan struct{}), progressDone: make(chan error, 1), done: make(chan captureResult, 1),
	}
	go capture.readProgress(progress)
	go capture.wait()
	select {
	case <-capture.first:
		return capture, nil
	case result := <-capture.done:
		if result.Error == nil {
			result.Error = errors.New("FFmpeg capture stopped before reporting an audio position")
		}
		return nil, result.Error
	case <-time.After(5 * time.Second):
		timeout := errors.New("FFmpeg capture supplied no audio position within five seconds")
		capture.Stop()
		select {
		case <-capture.done:
		case <-time.After(5 * time.Second):
			_ = capture.Abort()
			select {
			case <-capture.done:
			case <-time.After(5 * time.Second):
			}
		}
		return nil, timeout
	}
}

// readProgress anchors the capture clock to the audio position FFmpeg reports,
// so device start-up latency and encoder pacing cannot leak into the marker
// positions the way an elapsed wall-clock estimate does.
func (c *ffmpegCapture) readProgress(progress io.Reader) {
	scanner := bufio.NewScanner(progress)
	for scanner.Scan() {
		key, value, found := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !found || key != "out_time_us" {
			continue
		}
		microseconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || microseconds <= 0 {
			continue
		}
		frames := uint64(math.Round(float64(microseconds) / 1e6 * float64(c.config.Capture.SampleRate)))
		c.clock.acceptTotal(frames, time.Now())
		c.firstOnce.Do(func() { close(c.first) })
	}
	c.progressDone <- scanner.Err()
	close(c.progressDone)
}

func (c *ffmpegCapture) wait() {
	progressErr := <-c.progressDone
	commandErr := c.cmd.Wait()
	wall := time.Since(c.started).Seconds()
	duration := c.clock.latest().Seconds
	probeErr := error(nil)
	if measured, err := probeDuration(c.config.FFprobe, c.partPath); err == nil {
		duration = measured
	} else {
		probeErr = fmt.Errorf("verify captured FLAC duration: %w", err)
	}
	frames := uint64(duration*float64(c.config.Capture.SampleRate) + 0.5)
	info := c.Info()
	info.TotalFrames, info.WrittenFrames = frames, frames
	info.WallDuration, info.AudioDuration = wall, duration
	if wall > 0 {
		info.ClockDriftPPM = (duration - wall) / wall * 1_000_000
	}
	c.done <- captureResult{Info: info, PartPath: c.partPath, Error: errors.Join(progressErr, commandErr, probeErr)}
	close(c.done)
}

func (c *ffmpegCapture) PositionAt(at time.Time) Position {
	return c.clock.positionAt(at, 500*time.Millisecond)
}

func (c *ffmpegCapture) Latest() Position { return c.clock.latest() }
func (c *ffmpegCapture) Stop() {
	c.stopOnce.Do(func() {
		_, _ = io.WriteString(c.stdin, "q\n")
		_ = c.stdin.Close()
	})
}
func (c *ffmpegCapture) Abort() error {
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	return c.cmd.Process.Kill()
}
func (c *ffmpegCapture) Done() <-chan captureResult { return c.done }
func (c *ffmpegCapture) Info() store.CaptureInfo {
	return store.CaptureInfo{Backend: "ffmpeg", DeviceName: c.config.Capture.Device, SampleRate: c.config.Capture.SampleRate, Channels: c.config.Capture.Channels, SampleFormat: "s16le"}
}
