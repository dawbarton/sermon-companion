package capture

import (
	"bytes"
	"errors"
	"fmt"
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
	settings *config.Settings
	store    *store.Store
	mu       sync.Mutex
	run      *running
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
	return &Manager{settings: settings, store: sessions}
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
			dir, _ := m.store.SessionDir(session.ID)
			duration := session.Duration
			if measured, probeErr := probeDuration(c.FFprobe, filepath.Join(dir, session.AudioFile)); probeErr == nil {
				duration = measured
			}
			rate := sampleRate(session, c.Capture.SampleRate)
			frames := uint64(duration*float64(rate) + 0.5)
			ended := time.Now().UTC()
			_, updateErr := m.store.Update(session.ID, "capture.recovered_after_interruption", map[string]any{"durationSeconds": duration, "totalFrames": frames}, func(s *store.Session) error {
				s.Status, s.EndedAt, s.Duration = "interrupted", &ended, duration
				s.Capture.TotalFrames = frames
				s.Error = "The application stopped before the recording was closed normally; the captured audio was retained."
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
		return nil, err
	}
	var active activeCapture
	if strings.EqualFold(c.Capture.Backend, "miniaudio") {
		active, err = startMiniaudioCapture(c, partPath, logFile)
	} else {
		active, err = startFFmpegCapture(c, partPath, logFile)
	}
	if err != nil {
		logFile.Close()
		_, _ = m.store.Update(session.ID, "capture.failed", map[string]any{"error": err.Error()}, func(s *store.Session) error { s.Status = "failed"; s.Error = err.Error(); return nil })
		return nil, err
	}
	run := &running{id: session.ID, capture: active, log: logFile, partPath: partPath, path: finalPath, finished: make(chan struct{})}
	m.run = run
	info := active.Info()
	updated, err := m.store.Update(session.ID, "capture.started", info, func(s *store.Session) error {
		s.Status, s.Capture = "recording", info
		return nil
	})
	if err != nil {
		active.Stop()
		return nil, err
	}
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
	_, _ = m.store.Update(run.id, "capture.exited", map[string]any{"error": errText, "capture": result.Info}, func(s *store.Session) error {
		s.Status, s.Error, s.EndedAt, s.Duration, s.Capture = status, errText, &ended, duration, result.Info
		closeOpenSegments(s, Position{Frames: result.Info.TotalFrames, Seconds: duration}, ended)
		if filepath.Base(audioPath) == "audio.flac" {
			s.AudioFile = "audio.flac"
		}
		return nil
	})
	m.mu.Lock()
	if m.run == run {
		m.run = nil
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
		return nil, errors.New("capture did not stop within fifteen seconds")
	}
	return m.store.Get(run.id)
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
