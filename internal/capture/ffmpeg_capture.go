package capture

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

type ffmpegCapture struct {
	config   config.Config
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	started  time.Time
	partPath string
	stopOnce sync.Once
	done     chan captureResult
}

func startFFmpegCapture(c config.Config, partPath string, logFile *os.File) (activeCapture, error) {
	inputArgs, err := InputArgs(c.Capture)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-y"}
	args = append(args, inputArgs...)
	args = append(args, "-map", "0:a:0", "-vn", "-ac", strconv.Itoa(c.Capture.Channels), "-ar", strconv.Itoa(c.Capture.SampleRate), "-c:a", "flac", "-compression_level", "5", "-f", "flac", partPath)
	command := exec.Command(c.FFmpeg, args...)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	command.Stdout, command.Stderr = logFile, logFile
	fmt.Fprintf(logFile, "\n[%s] starting fallback FFmpeg device capture: %s\n", time.Now().Format(time.RFC3339), printableCommand(c.FFmpeg, args))
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start FFmpeg device capture: %w", err)
	}
	capture := &ffmpegCapture{config: c, cmd: command, stdin: stdin, started: time.Now(), partPath: partPath, done: make(chan captureResult, 1)}
	go capture.wait()
	return capture, nil
}

func (c *ffmpegCapture) wait() {
	err := c.cmd.Wait()
	wall := time.Since(c.started).Seconds()
	duration := wall
	if measured, probeErr := probeDuration(c.config.FFprobe, c.partPath); probeErr == nil {
		duration = measured
	}
	frames := uint64(duration*float64(c.config.Capture.SampleRate) + 0.5)
	info := c.Info()
	info.TotalFrames, info.WrittenFrames = frames, frames
	info.WallDuration, info.AudioDuration = wall, duration
	if wall > 0 {
		info.ClockDriftPPM = (duration - wall) / wall * 1_000_000
	}
	c.done <- captureResult{Info: info, PartPath: c.partPath, Error: err}
	close(c.done)
}

func (c *ffmpegCapture) PositionAt(at time.Time) Position {
	seconds := at.Sub(c.started).Seconds()
	if seconds < 0 {
		seconds = 0
	}
	return Position{Frames: uint64(seconds*float64(c.config.Capture.SampleRate) + 0.5), Seconds: seconds, Estimated: true}
}

func (c *ffmpegCapture) Latest() Position { return c.PositionAt(time.Now()) }
func (c *ffmpegCapture) Stop() {
	c.stopOnce.Do(func() {
		_, _ = io.WriteString(c.stdin, "q\n")
		_ = c.stdin.Close()
	})
}
func (c *ffmpegCapture) Done() <-chan captureResult { return c.done }
func (c *ffmpegCapture) Info() store.CaptureInfo {
	return store.CaptureInfo{Backend: "ffmpeg", DeviceName: c.config.Capture.Device, SampleRate: c.config.Capture.SampleRate, Channels: c.config.Capture.Channels, SampleFormat: "s16le"}
}
