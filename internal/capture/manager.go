package capture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dawbarton/sermon-companion/internal/atomicfile"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/proc"
	"github.com/dawbarton/sermon-companion/internal/store"
)

type Manager struct {
	settings    *config.Settings
	store       *store.Store
	mu          sync.Mutex
	run         *running
	lastError   string
	startNative func(config.Config, string, *os.File) (activeCapture, error)
	startFFmpeg func(config.Config, string, *os.File) (activeCapture, error)
}

type running struct {
	id       string
	capture  activeCapture
	log      *os.File
	partPath string
	path     string
	finished chan struct{}
}

func New(settings *config.Settings, sessions *store.Store) *Manager {
	return &Manager{settings: settings, store: sessions, startNative: startMiniaudioCapture, startFFmpeg: startFFmpegCapture}
}

// Settings exposes the live configuration so that a caller can read the chosen
// capture device without keeping a stale copy of its own.
func (m *Manager) Settings() *config.Settings { return m.settings }

func (m *Manager) RecoverInterrupted() error {
	sessions, err := m.store.List()
	if err != nil {
		return err
	}
	c := m.settings.Get()
	for index := range sessions {
		session := &sessions[index]
		switch {
		case session.Status == "recording" || session.Status == "starting":
			if err := m.closeInterruptedRecording(c, session); err != nil {
				return err
			}
		case needsLengthRepair(session):
			if err := m.repairUnmeasuredLength(c, session); err != nil {
				return err
			}
		}
		if session.Export != nil && session.Export.Status == "running" {
			ended := time.Now().UTC()
			_, updateErr := m.store.Update(session.ID, "export.recovered_after_interruption", nil, func(s *store.Session) error {
				s.Export.Status, s.Export.EndedAt, s.Export.Error = "failed", &ended, "The application stopped before the export finished. Create the MP3 again."
				return nil
			})
			if updateErr != nil {
				return updateErr
			}
		}
		if session.Publish != nil && session.Publish.Status == "running" {
			ended := time.Now().UTC()
			_, updateErr := m.store.Update(session.ID, "publish.recovered_after_interruption", nil, func(s *store.Session) error {
				s.Publish.Status, s.Publish.EndedAt = "failed", &ended
				s.Publish.Error = "Sermon Companion stopped before the upload finished. Publish again to carry on from where it stopped."
				return nil
			})
			if updateErr != nil {
				return updateErr
			}
		}
	}
	return nil
}

// closeInterruptedRecording finishes a service whose recording was still in
// progress when the application stopped. The frame counts it carries were
// written when the recording began, so they are replaced by the measured ones
// whatever the measurement found: a service left claiming the first audio
// callback as its whole length rejects every segment the operator marked.
func (m *Manager) closeInterruptedRecording(c config.Config, session *store.Session) error {
	rate := sampleRate(session, c.Capture.SampleRate)
	audio := measureSessionAudio(c, m.store, session, rate)
	ended := time.Now().UTC()
	_, err := m.store.Update(session.ID, "capture.recovered_after_interruption", audio.payload(), func(s *store.Session) error {
		s.Status, s.EndedAt, s.Duration = "interrupted", &ended, audio.duration
		s.Capture.TotalFrames, s.Capture.WrittenFrames = audio.frames, audio.frames
		s.Capture.AudioDuration = audio.duration
		if audio.file != "" {
			s.AudioFile = audio.file
		}
		s.Error = audio.message
		closeOpenSegments(s, Position{Frames: audio.frames, Seconds: audio.duration, Estimated: true}, ended)
		trimSegmentsToRecording(s, audio.frames, rate, ended)
		return nil
	})
	return err
}

// needsLengthRepair reports a service recovered from an interruption by an
// earlier version, before the length of an unclosed FLAC could be measured. It
// kept the frame count written when the recording began, which describes a
// fraction of a second of audio and makes every real segment look as though it
// ends beyond the recording. Measuring it again repairs the service in place;
// the test cannot match twice, because the count is replaced either way.
func needsLengthRepair(session *store.Session) bool {
	return session.Status == "interrupted" && session.Duration <= 0 &&
		session.Capture.AudioDuration <= 0 && session.Capture.TotalFrames > 0
}

func (m *Manager) repairUnmeasuredLength(c config.Config, session *store.Session) error {
	rate := sampleRate(session, c.Capture.SampleRate)
	audio := measureSessionAudio(c, m.store, session, rate)
	now := time.Now().UTC()
	_, err := m.store.Update(session.ID, "capture.length_remeasured", audio.payload(), func(s *store.Session) error {
		s.Duration = audio.duration
		s.Capture.TotalFrames, s.Capture.WrittenFrames = audio.frames, audio.frames
		s.Capture.AudioDuration = audio.duration
		if audio.file != "" {
			s.AudioFile = audio.file
		}
		s.Error = audio.message
		closeOpenSegments(s, Position{Frames: audio.frames, Seconds: audio.duration, Estimated: true}, now)
		trimSegmentsToRecording(s, audio.frames, rate, now)
		return nil
	})
	return err
}

// ApplyRetention deletes services that finished longer ago than the configured
// retention period, including their MP3s. A service is roughly 500 MB and the
// exports are published to the church website, so nothing here is the copy of
// record. Each deletion is logged, since the session's own journal goes with it.
func (m *Manager) ApplyRetention(now time.Time) ([]string, error) {
	days, limited := m.settings.Get().KeepRecordingsFor()
	if !limited {
		return nil, nil
	}
	sessions, err := m.store.List()
	if err != nil {
		return nil, err
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	var deleted []string
	for index := range sessions {
		session := &sessions[index]
		if session.Status == "recording" || session.Status == "starting" {
			continue
		}
		finished := session.StartedAt
		if session.EndedAt != nil {
			finished = *session.EndedAt
		}
		if !finished.Before(cutoff) {
			continue
		}
		if err := m.store.Delete(session.ID); err != nil {
			return deleted, fmt.Errorf("delete expired service %s: %w", session.ID, err)
		}
		deleted = append(deleted, session.ID)
	}
	return deleted, nil
}

func (m *Manager) Start(title string) (*store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run != nil {
		return nil, errors.New("a recording is already in progress")
	}
	// One snapshot for the whole start-up, so a device chosen in the dock while
	// the recording is being set up cannot be applied halfway through it.
	c := m.settings.Get()
	session, err := m.store.Create(title, c.Church, time.Now())
	if err != nil {
		return nil, err
	}
	dir, _ := m.store.SessionDir(session.ID)
	partPath, finalPath := filepath.Join(dir, "audio.part.flac"), filepath.Join(dir, "audio.flac")
	logFile, err := os.OpenFile(filepath.Join(dir, "capture.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		m.recordStartFailure(session.ID, err)
		return nil, err
	}
	var active activeCapture
	if strings.EqualFold(c.Capture.Backend, "miniaudio") {
		active, err = m.startNative(c, partPath, logFile)
	} else {
		active, err = m.startFFmpeg(c, partPath, logFile)
	}
	if err != nil {
		logFile.Close()
		m.recordStartFailure(session.ID, err)
		return nil, err
	}
	run := &running{id: session.ID, capture: active, log: logFile, partPath: partPath, path: finalPath, finished: make(chan struct{})}
	info := active.Info()
	// The counters describe a finished capture, and this is its first moment.
	// The miniaudio backend returns only once the first audio callback has run,
	// so recording them here would persist a fraction of a second of audio as
	// though it were the whole service; an interruption that then leaves them
	// in place makes every segment the operator marked look out of range.
	info.TotalFrames, info.WrittenFrames, info.DroppedFrames = 0, 0, 0
	info.CallbackCount, info.QueueHighWaterFrames = 0, 0
	updated, err := m.store.Update(session.ID, "capture.started", info, func(s *store.Session) error {
		s.Status, s.Capture = "recording", info
		return nil
	})
	if err != nil {
		active.Stop()
		select {
		case <-active.Done():
		case <-time.After(15 * time.Second):
			_ = active.Abort()
			select {
			case <-active.Done():
			case <-time.After(5 * time.Second):
			}
		}
		_ = logFile.Close()
		m.recordStartFailure(session.ID, fmt.Errorf("save recording start: %w", err))
		return nil, err
	}
	m.run = run
	m.lastError = ""
	go m.wait(run)
	return updated, nil
}

func (m *Manager) wait(run *running) {
	result := <-run.capture.Done()
	run.log.Close()
	ended := time.Now().UTC()
	status, errText := "stopped", ""
	if result.Error != nil {
		status, errText = "failed", result.Error.Error()
	}
	audioPath := result.PartPath
	if result.Error == nil {
		if err := atomicfile.Replace(result.PartPath, run.path); err != nil {
			status, errText = "failed", fmt.Sprintf("publish recording: %v", err)
		} else {
			audioPath = run.path
		}
	}
	duration := result.Info.AudioDuration
	if duration == 0 && result.Info.SampleRate > 0 {
		duration = float64(result.Info.TotalFrames) / float64(result.Info.SampleRate)
	}
	_, updateErr := m.store.Update(run.id, "capture.exited", map[string]any{"error": errText, "capture": result.Info}, func(s *store.Session) error {
		s.Status, s.Error, s.EndedAt, s.Duration, s.Capture = status, errText, &ended, duration, result.Info
		closeOpenSegments(s, Position{Frames: result.Info.TotalFrames, Seconds: duration}, ended)
		if filepath.Base(audioPath) == "audio.flac" {
			s.AudioFile = "audio.flac"
		}
		return nil
	})
	if updateErr != nil {
		log.Printf("save completed capture %s: %v", run.id, updateErr)
	}
	m.mu.Lock()
	if m.run == run {
		m.run = nil
	}
	if updateErr != nil {
		m.lastError = fmt.Sprintf("Recording ended, but its session details could not be saved: %v. Review the session folder and restart Sermon Companion.", updateErr)
	} else if errText != "" {
		m.lastError = "Recording stopped because of a capture problem: " + errText
	}
	m.mu.Unlock()
	close(run.finished)
}

// ErrNotRecording is Stop's answer when there is nothing to stop, including
// when the recording ended between a caller seeing it and asking to stop it.
var ErrNotRecording = errors.New("no recording is in progress")

func (m *Manager) Stop() (*store.Session, error) {
	m.mu.Lock()
	run := m.run
	if run == nil {
		m.mu.Unlock()
		return nil, ErrNotRecording
	}
	position := run.capture.PositionAt(time.Now())
	if _, err := m.store.Update(run.id, "segments.closed_on_stop", position, func(s *store.Session) error {
		closeOpenSegments(s, position, time.Now().UTC())
		return nil
	}); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	run.capture.Stop()
	m.mu.Unlock()
	select {
	case <-run.finished:
	case <-time.After(15 * time.Second):
		abortErr := run.capture.Abort()
		select {
		case <-run.finished:
		case <-time.After(5 * time.Second):
			return nil, errors.Join(errors.New("capture did not stop within twenty seconds"), abortErr)
		}
	}
	return m.store.Get(run.id)
}

// LastError returns a capture failure that still needs the operator's
// attention. A successful recording start clears it.
func (m *Manager) LastError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastError
}

func (m *Manager) recordStartFailure(id string, cause error) {
	message := cause.Error()
	if _, err := m.store.Update(id, "capture.failed", map[string]any{"error": message}, func(s *store.Session) error {
		s.Status, s.Error = "failed", message
		return nil
	}); err != nil {
		log.Printf("save capture start failure for %s: %v", id, err)
		message = fmt.Sprintf("%s (the failure could not be saved: %v)", message, err)
	}
	m.lastError = "Recording could not start: " + message
}

// recoveredAudio is what the source recording of an interrupted service turned
// out to hold. A zero length means the length is unknown rather than nil: the
// rest of the application treats an unknown length as unbounded, so the
// operator can still edit segments against the waveform, which is measured
// from the audio itself.
type recoveredAudio struct {
	file     string
	frames   uint64
	duration float64
	message  string
}

func (a recoveredAudio) payload() map[string]any {
	return map[string]any{"durationSeconds": a.duration, "totalFrames": a.frames, "audioFile": a.file, "error": a.message}
}

func measureSessionAudio(c config.Config, sessions *store.Store, session *store.Session, rate int) recoveredAudio {
	candidates := []string{"audio.flac", session.AudioFile, "audio.part.flac"}
	seen := map[string]bool{}
	var found string
	var measureError error
	for _, candidate := range candidates {
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		path, err := sessions.SessionFile(session.ID, candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		if found == "" {
			found = candidate
		}
		measured, err := measureRecordedDuration(c.FFprobe, c.FFmpeg, path)
		if err == nil {
			return recoveredAudio{
				file: candidate, frames: durationFrames(measured, rate), duration: measured,
				message: "The application stopped before the recording was closed normally; the captured audio was retained.",
			}
		}
		measureError = err
	}
	if found != "" {
		return recoveredAudio{file: found, message: fmt.Sprintf("The application stopped before the recording was closed normally. The audio was retained and can still be played and exported, but its length could not be measured, so this service shows no duration: %v", measureError)}
	}
	return recoveredAudio{message: "The application stopped before the recording was closed normally, and its audio file could not be found. Check the session folder and log."}
}

func closeOpenSegments(session *store.Session, position Position, now time.Time) {
	for index := range session.Segments {
		if session.Segments[index].End == nil && position.Frames > session.Segments[index].StartFrame {
			frames, seconds := position.Frames, position.Seconds
			session.Segments[index].EndFrame, session.Segments[index].End = &frames, &seconds
			session.Segments[index].UpdatedAt = now
		}
	}
}

// trimSegmentsToRecording reconciles what the operator marked with the audio
// that survived. The frame clock runs a little ahead of the encoder, so a
// segment marked in the moments before an interruption can end past the last
// frame the FLAC holds. Those ends are trimmed to it; a segment beginning
// beyond it has no audio at all and is archived, which the interface can undo.
// An unknown length is left alone, because there is then no end to trim to.
func trimSegmentsToRecording(session *store.Session, frames uint64, rate int, now time.Time) {
	if frames == 0 || rate <= 0 {
		return
	}
	end := float64(frames) / float64(rate)
	for index := range session.Segments {
		segment := &session.Segments[index]
		if segment.Archived {
			continue
		}
		if segment.StartFrame >= frames {
			segment.Archived, segment.UpdatedAt = true, now
			continue
		}
		if segment.EndFrame != nil && *segment.EndFrame > frames {
			trimmed, trimmedSeconds := frames, end
			segment.EndFrame, segment.End = &trimmed, &trimmedSeconds
			segment.UpdatedAt = now
		}
	}
}

func (m *Manager) Active() (id string, position Position, info store.CaptureInfo, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run == nil {
		return "", Position{}, store.CaptureInfo{}, false
	}
	return m.run.id, m.run.capture.Latest(), m.run.capture.Info(), true
}

func (m *Manager) MarkPosition(at time.Time) (id string, position Position, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run == nil {
		return "", Position{}, false
	}
	return m.run.id, m.run.capture.PositionAt(at), true
}

func printableCommand(program string, args []string) string {
	parts := []string{program}
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\"") {
			parts = append(parts, strconv.Quote(arg))
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}

// decodeMeasureTimeout bounds the fallback decode. FLAC decodes hundreds of
// times faster than real time, so a service-length recording finishes in
// seconds. The ceiling is there so that a damaged file cannot hold up start-up
// indefinitely; a recording that cannot be measured within it is recovered
// with an unknown length rather than none of it at all.
const decodeMeasureTimeout = 2 * time.Minute

// measureRecordedDuration reports how much audio a recording holds. FFprobe
// answers from the container, which is instant and exact for a file the
// encoder closed itself, but a FLAC left behind by an interrupted encoder has
// no total-sample count in its header and is reported as "N/A". Decoding the
// stream is then the only way to learn how much of it survived, so it is tried
// second.
func measureRecordedDuration(ffprobe, ffmpeg, path string) (float64, error) {
	duration, probeErr := probeDuration(ffprobe, path)
	if probeErr == nil {
		return duration, nil
	}
	duration, decodeErr := decodeDuration(ffmpeg, path)
	if decodeErr != nil {
		return 0, fmt.Errorf("%w; decoding it reported: %v", probeErr, decodeErr)
	}
	return duration, nil
}

// decodeDuration decodes a recording to nothing and reports the position
// FFmpeg reached. The last FLAC frame of an interrupted recording is torn, so
// FFmpeg logs a decoding error for it and may exit unsuccessfully; a position
// it has already reported is audio that can still be read back, so the
// measurement is taken whatever the exit status turns out to be.
func decodeDuration(program, path string) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), decodeMeasureTimeout)
	defer cancel()
	args := []string{"-hide_banner", "-nostats", "-loglevel", "error", "-nostdin", "-progress", "pipe:1", "-i", path, "-map", "0:a:0", "-vn", "-f", "null", "-"}
	command := proc.CommandContext(ctx, program, args...)
	var progress, problems bytes.Buffer
	command.Stdout, command.Stderr = &progress, &problems
	runErr := command.Run()
	if duration, ok := furthestReportedPosition(progress.String()); ok {
		return duration, nil
	}
	if runErr != nil {
		return 0, fmt.Errorf("decode %s: %w: %s", filepath.Base(path), runErr, strings.TrimSpace(problems.String()))
	}
	return 0, fmt.Errorf("decoding %s reported no audio position", filepath.Base(path))
}

// furthestReportedPosition reads the greatest out_time_us in FFmpeg's
// -progress output. The reports are cumulative, but the greatest is taken
// rather than the last, so that a report cut short as the process exits cannot
// be mistaken for a shorter recording.
func furthestReportedPosition(progress string) (float64, bool) {
	var furthest int64
	for _, line := range strings.Split(progress, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || key != "out_time_us" {
			continue
		}
		if microseconds, err := strconv.ParseInt(value, 10, 64); err == nil && microseconds > furthest {
			furthest = microseconds
		}
	}
	return float64(furthest) / 1e6, furthest > 0
}

func probeDuration(program, path string) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := proc.CommandContext(ctx, program, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return 0, err
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(output.String()), 64)
	if err != nil {
		return 0, err
	}
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, fmt.Errorf("FFprobe returned invalid duration %q", strings.TrimSpace(output.String()))
	}
	return duration, nil
}

// durationFrames converts a measured length to the audio-frame count that is
// canonical for segments and markers. An unmeasured length stays zero, which
// the rest of the application reads as unknown.
func durationFrames(duration float64, rate int) uint64 {
	if duration <= 0 || rate <= 0 {
		return 0
	}
	return uint64(duration*float64(rate) + 0.5)
}

func sampleRate(session *store.Session, fallback int) int {
	if session.Capture.SampleRate > 0 {
		return session.Capture.SampleRate
	}
	return fallback
}
