package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/dawbarton/sermon-companion/internal/atomicfile"
)

const (
	snapshotName       = "session.json"
	stagedSnapshotName = "session.json.tmp"
	journalName        = "events.jsonl"
)

func readSnapshot(path, expectedID string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	if session.ID != expectedID {
		return nil, fmt.Errorf("session snapshot ID %q does not match directory %q", session.ID, expectedID)
	}
	if !validID(session.ID) {
		return nil, errors.New("session snapshot contains an invalid ID")
	}
	if session.SchemaVersion < 1 || session.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("unsupported session schema version %d (this application supports up to %d)", session.SchemaVersion, SchemaVersion)
	}
	return &session, nil
}

// recoverSessionFiles resolves an interrupted metadata transaction. The full
// candidate snapshot is durable before its event is appended, so an event and
// matching candidate are sufficient to finish publication after a restart.
func recoverSessionFiles(dir, id string) error {
	current, currentErr := readSnapshot(filepath.Join(dir, snapshotName), id)
	currentExists := currentErr == nil
	if currentErr != nil && !os.IsNotExist(currentErr) {
		return fmt.Errorf("read current snapshot: %w", currentErr)
	}
	currentRevision := int64(0)
	if currentExists {
		currentRevision = current.Revision
	}

	journalPath := filepath.Join(dir, journalName)
	events, err := readJournal(journalPath, id, true)
	if err != nil {
		return err
	}
	journalRevision := int64(0)
	if len(events) > 0 {
		journalRevision = events[len(events)-1].Sequence
	}

	stagedPath := filepath.Join(dir, stagedSnapshotName)
	staged, stagedErr := readSnapshot(stagedPath, id)
	if stagedErr == nil {
		switch {
		case staged.Revision == currentRevision+1 && journalRevision == staged.Revision:
			if err := atomicfile.Replace(stagedPath, filepath.Join(dir, snapshotName)); err != nil {
				return fmt.Errorf("finish interrupted snapshot publication: %w", err)
			}
			currentRevision = staged.Revision
		case staged.Revision == currentRevision+1 && journalRevision == currentRevision:
			if err := os.Remove(stagedPath); err != nil {
				return fmt.Errorf("discard uncommitted snapshot: %w", err)
			}
		default:
			return fmt.Errorf("session metadata is inconsistent: snapshot revision %d, staged revision %d, journal revision %d", currentRevision, staged.Revision, journalRevision)
		}
	} else if !os.IsNotExist(stagedErr) {
		return fmt.Errorf("read staged snapshot: %w", stagedErr)
	}

	if !currentExists && currentRevision == 0 {
		if journalRevision == 0 {
			return os.ErrNotExist
		}
		return fmt.Errorf("session journal is at revision %d but no snapshot can recover it", journalRevision)
	}
	if journalRevision != currentRevision {
		return fmt.Errorf("session metadata is inconsistent: snapshot revision %d, journal revision %d", currentRevision, journalRevision)
	}
	return nil
}

func appendJournalEvent(path string, record []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	start := int64(0)
	if info, statErr := file.Stat(); statErr == nil {
		start = info.Size()
	} else {
		_ = file.Close()
		return statErr
	}
	written, writeErr := file.Write(record)
	if writeErr == nil && written != len(record) {
		writeErr = io.ErrShortWrite
	}
	var syncErr error
	if writeErr == nil {
		syncErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		rollbackErr := truncateAndSync(path, start)
		return errors.Join(err, rollbackErr)
	}
	return nil
}

func truncateAndSync(path string, size int64) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	truncateErr := file.Truncate(size)
	var syncErr error
	if truncateErr == nil {
		syncErr = file.Sync()
	}
	return errors.Join(truncateErr, syncErr, file.Close())
}

func readJournal(path, expectedID string, repairTail bool) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if !repairTail {
			return nil, errors.New("event journal ends with a partial record")
		}
		lastNewline := bytes.LastIndexByte(data, '\n')
		complete, partial := data[:lastNewline+1], data[lastNewline+1:]
		quarantine := fmt.Sprintf("%s.truncated-%d", path, time.Now().UTC().UnixNano())
		if err := atomicfile.Write(quarantine, partial, 0o644); err != nil {
			return nil, fmt.Errorf("preserve truncated event record: %w", err)
		}
		if err := atomicfile.Write(path, complete, 0o644); err != nil {
			return nil, fmt.Errorf("remove truncated event record: %w", err)
		}
		data = complete
	}
	lines := bytes.Split(data, []byte{'\n'})
	events := make([]Event, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("event journal contains malformed record %d: %w", len(events)+1, err)
		}
		expectedSequence := int64(len(events) + 1)
		if event.Sequence != expectedSequence {
			return nil, fmt.Errorf("event journal sequence is %d, want %d", event.Sequence, expectedSequence)
		}
		if event.SessionID != expectedID {
			return nil, fmt.Errorf("event %d belongs to session %q, want %q", event.Sequence, event.SessionID, expectedID)
		}
		events = append(events, event)
	}
	return events, nil
}
