package capture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

type Manager struct {
	config config.Config
	store  *store.Store
	mu     sync.Mutex
	run    *running
}

type running struct {
	id       string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	started  time.Time
	done     chan error
	log      *os.File
	partPath string
	path     string
}

func New(config config.Config, sessions *store.Store) *Manager {
	return &Manager{config: config, store: sessions}
}

// RecoverInterrupted marks recordings left active by an application or power
// failure as interrupted. The original partial FLAC is retained and any open
// semantic segment is conservatively closed at the measured end of the audio.
func (m *Manager) RecoverInterrupted() error {
	sessions, err := m.store.List()
	if err != nil {
		return err
	}
	for i := range sessions {
		session := &sessions[i]
		if session.Status == "recording" || session.Status == "starting" {
			dir, _ := m.store.SessionDir(session.ID)
			duration := session.Duration
			if measured, probeErr := probeDuration(m.config.FFprobe, filepath.Join(dir, session.AudioFile)); probeErr == nil {
				duration = measured
			}
			ended := time.Now().UTC()
			_, updateErr := m.store.Update(session.ID, "capture.recovered_after_interruption", map[string]any{"durationSeconds": duration}, func(s *store.Session) error {
				s.Status, s.EndedAt, s.Duration = "interrupted", &ended, duration
				s.Error = "The application stopped before the recording was closed normally; the captured audio was retained."
				for index := range s.Segments {
					if s.Segments[index].End == nil && duration > s.Segments[index].Start {
						s.Segments[index].End = &duration
						s.Segments[index].UpdatedAt = ended
					}
				}
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

func (m *Manager) Start(title string) (*store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run != nil {
		return nil, errors.New("a recording is already in progress")
	}
	session, err := m.store.Create(title, m.config.Church, time.Now())
	if err != nil {
		return nil, err
	}
	dir, _ := m.store.SessionDir(session.ID)
	inputArgs, err := InputArgs(m.config.Capture)
	if err != nil {
		_, _ = m.store.Update(session.ID, "capture.failed", map[string]any{"error": err.Error()}, func(s *store.Session) error { s.Status = "failed"; s.Error = err.Error(); return nil })
		return nil, err
	}
	partPath := filepath.Join(dir, "audio.part.flac")
	finalPath := filepath.Join(dir, "audio.flac")
	args := []string{"-hide_banner", "-y"}
	args = append(args, inputArgs...)
	args = append(args, "-map", "0:a:0", "-vn", "-ac", strconv.Itoa(m.config.Capture.Channels), "-ar", strconv.Itoa(m.config.Capture.SampleRate), "-c:a", "flac", "-compression_level", "5", "-f", "flac", partPath)
	cmd := exec.Command(m.config.FFmpeg, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "capture.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if _, err := fmt.Fprintf(logFile, "\n[%s] starting capture: %s\n", time.Now().Format(time.RFC3339), printableCommand(m.config.FFmpeg, args)); err != nil {
		logFile.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		_, _ = m.store.Update(session.ID, "capture.failed", map[string]any{"error": err.Error()}, func(s *store.Session) error { s.Status = "failed"; s.Error = err.Error(); return nil })
		return nil, fmt.Errorf("start FFmpeg: %w", err)
	}
	r := &running{id: session.ID, cmd: cmd, stdin: stdin, started: time.Now(), done: make(chan error, 1), log: logFile, partPath: partPath, path: finalPath}
	m.run = r
	updated, err := m.store.Update(session.ID, "capture.started", map[string]any{"driver": m.config.Capture.Driver, "device": m.config.Capture.Device}, func(s *store.Session) error { s.Status = "recording"; return nil })
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	go m.wait(r)
	return updated, nil
}

func (m *Manager) wait(r *running) {
	err := r.cmd.Wait()
	r.log.Close()

	m.mu.Lock()
	current := m.run == r
	if current {
		m.run = nil
	}
	m.mu.Unlock()
	if !current {
		return
	}

	ended := time.Now().UTC()
	status := "stopped"
	errText := ""
	if err != nil {
		status, errText = "failed", err.Error()
	}
	if _, statErr := os.Stat(r.partPath); statErr == nil {
		if renameErr := os.Rename(r.partPath, r.path); renameErr == nil {
			r.partPath = r.path
		}
	}
	duration := time.Since(r.started).Seconds()
	if probed, probeErr := probeDuration(m.config.FFprobe, r.partPath); probeErr == nil {
		duration = probed
	}
	_, _ = m.store.Update(r.id, "capture.exited", map[string]any{"error": errText}, func(s *store.Session) error {
		s.Status, s.Error, s.EndedAt = status, errText, &ended
		s.Duration = duration
		closeOpenSegments(s, duration, ended)
		if filepath.Base(r.partPath) == "audio.flac" {
			s.AudioFile = "audio.flac"
		}
		return nil
	})
	r.done <- err
	close(r.done)
}

func (m *Manager) Stop() (*store.Session, error) {
	m.mu.Lock()
	r := m.run
	if r == nil {
		m.mu.Unlock()
		return nil, errors.New("no recording is in progress")
	}
	elapsed := time.Since(r.started).Seconds()
	if _, err := m.store.Update(r.id, "segments.closed_on_stop", map[string]any{"atSeconds": elapsed}, func(s *store.Session) error {
		closeOpenSegments(s, elapsed, time.Now().UTC())
		return nil
	}); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	_, _ = io.WriteString(r.stdin, "q\n")
	_ = r.stdin.Close()
	m.mu.Unlock()

	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
		_ = r.cmd.Process.Kill()
		<-r.done
	}
	return m.store.Get(r.id)
}

func closeOpenSegments(session *store.Session, elapsed float64, now time.Time) {
	for i := range session.Segments {
		if session.Segments[i].End == nil && elapsed > session.Segments[i].Start {
			session.Segments[i].End = &elapsed
			session.Segments[i].UpdatedAt = now
		}
	}
}

func (m *Manager) Active() (id string, elapsed float64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run == nil {
		return "", 0, false
	}
	return m.run.id, time.Since(m.run.started).Seconds(), true
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
	cmd := exec.Command(program, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(output.String()), 64)
}
