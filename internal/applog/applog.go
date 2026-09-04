// Package applog keeps the messages that used to scroll past in the console.
// The application now runs without a terminal, so the same lines go to a
// rolling file and to a bounded in-memory tail the review interface can show.
package applog

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
)

const (
	// MaxFileBytes is the size at which the current log file is rotated. One
	// previous file is kept, so a fault during last Sunday's service is still
	// readable after a week of ordinary start-up messages.
	MaxFileBytes = 1 << 20
	// TailLines is how much of the log the interface can show without reading
	// the file back.
	TailLines = 500
)

type Log struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	size    int64
	lines   []string
	partial []byte
}

// New opens the rolling log beneath the data directory. A log that cannot be
// opened is not fatal: the returned Log still keeps the in-memory tail, because
// losing the messages entirely is worse than losing the file copy of them.
func New(dataDir string) (*Log, error) {
	l := &Log{path: filepath.Join(dataDir, "logs", "sermon-companion.log"), lines: make([]string, 0, TailLines)}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return l, err
	}
	if err := l.open(); err != nil {
		return l, err
	}
	return l, nil
}

func (l *Log) open() error {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	size := int64(0)
	if info, statErr := file.Stat(); statErr == nil {
		size = info.Size()
	}
	l.file, l.size = file, size
	return nil
}

// Path is the file the log is written to, so the interface can tell an operator
// which file to send to whoever maintains the installation.
func (l *Log) Path() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return ""
	}
	return l.path
}

// Write accepts the output of the standard logger. A write is never reported as
// failed: logging must not be able to stop a recording.
func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.record(p)
	if l.file != nil {
		if l.size+int64(len(p)) > MaxFileBytes {
			l.rotate()
		}
		if written, err := l.file.Write(p); err == nil {
			l.size += int64(written)
		}
	}
	return len(p), nil
}

// record splits the incoming bytes into whole lines for the in-memory tail. The
// standard logger writes one complete record per call, but an io.MultiWriter
// carries whatever its other users produce, so a trailing fragment is held back
// until its line is finished.
func (l *Log) record(p []byte) {
	l.partial = append(l.partial, p...)
	for {
		index := bytes.IndexByte(l.partial, '\n')
		if index < 0 {
			break
		}
		l.append(string(bytes.TrimRight(l.partial[:index], "\r")))
		l.partial = l.partial[index+1:]
	}
	// A fragment that never ends in a newline must not grow without bound.
	if len(l.partial) > 8<<10 {
		l.append(string(l.partial))
		l.partial = l.partial[:0]
	}
}

func (l *Log) append(line string) {
	if len(l.lines) == TailLines {
		copy(l.lines, l.lines[1:])
		l.lines[len(l.lines)-1] = line
		return
	}
	l.lines = append(l.lines, line)
}

// rotate keeps one previous file. The caller holds the lock.
func (l *Log) rotate() {
	_ = l.file.Close()
	l.file = nil
	previous := l.path + ".1"
	_ = os.Remove(previous)
	if err := os.Rename(l.path, previous); err != nil {
		// The current file could not be moved aside, so continue appending to
		// it rather than losing the messages that follow.
		_ = l.open()
		return
	}
	_ = l.open()
}

// Tail returns the most recent lines, oldest first.
func (l *Log) Tail(limit int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 || limit > len(l.lines) {
		limit = len(l.lines)
	}
	return append([]string(nil), l.lines[len(l.lines)-limit:]...)
}

// Close flushes and closes the file copy.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
