package capture

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
		if session.Status == "recording" || session.Status == "starting" {
			duration, audioFile, recoveryError := recoverRecording(c.FFprobe, m.store, session)
			rate := sampleRate(session, c.Capture.SampleRate)
			frames := uint64(duration*float64(rate) + 0.5)
			ended := time.Now().UTC()
			_, updateErr := m.store.Update(session.ID, "capture.recovered_after_interruption", map[string]any{"durationSeconds": duration, "totalFrames": frames, "audioFile": audioFile, "error": recoveryError}, func(s *store.Session) error {
				s.Status, s.EndedAt, s.Duration = "interrupted", &ended, duration
				if frames > 0 {
					s.Capture.TotalFrames, s.Capture.WrittenFrames = frames, frames
					s.Capture.AudioDuration = duration
				}
				if audioFile != "" {
					s.AudioFile = audioFile
				}
				s.Error = recoveryError
				closeOpenSegments(s, Position{Frames: frames, Seconds: duration, Estimated: true}, ended)
				return nil
			})
			if updateErr != nil {
				return updateErr
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
	}
	return nil
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
		if err := os.Rename(result.PartPath, run.path); err != nil {
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

func (m *Manager) Stop() (*store.Session, error) {
	m.mu.Lock()
	run := m.run
	if run == nil {
		m.mu.Unlock()
		return nil, errors.New("no recording is in progress")
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

func recoverRecording(ffprobe string, sessions *store.Store, session *store.Session) (duration float64, audioFile, message string) {
	candidates := []string{"audio.flac", session.AudioFile, "audio.part.flac"}
	seen := map[string]bool{}
	var probeError error
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
		if audioFile == "" {
			audioFile = candidate
		}
		if measured, err := probeDuration(ffprobe, path); err == nil {
			return measured, candidate, "The application stopped before the recording was closed normally; the captured audio was retained."
		} else {
			probeError = err
		}
	}
	if audioFile != "" {
		return session.Duration, audioFile, fmt.Sprintf("The application stopped before the recording was closed normally. The audio file was retained, but its duration could not be verified: %v", probeError)
	}
	return session.Duration, "", "The application stopped before the recording was closed normally, and its audio file could not be found. Check the session folder and log."
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

func probeDuration(program, path string) (float64, error) {
	command := proc.Command(program, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(output.String()), 64)
}

func sampleRate(session *store.Session, fallback int) int {
	if session.Capture.SampleRate > 0 {
		return session.Capture.SampleRate
	}
	return fallback
}
