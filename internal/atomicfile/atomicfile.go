// Package atomicfile durably replaces small application metadata files.
package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Write writes data to a sibling temporary file, flushes it, and atomically
// replaces path. Callers never observe a partially written metadata file.
func Write(path string, data []byte, permission os.FileMode) error {
	temporary := path + ".tmp"
	if err := WriteStaged(temporary, data, permission); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := Replace(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// WriteStaged writes and flushes a file without publishing it. It supports the
// store's journal-first transaction, which publishes the already durable file
// only after its matching event has been flushed.
func WriteStaged(path string, data []byte, permission os.FileMode) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, permission)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		} else if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	written, err := file.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return file.Sync()
}

// Replace publishes a staged sibling file and asks the platform to make the
// directory entry durable where it provides such an operation.
func Replace(staged, destination string) error {
	if filepath.Dir(staged) != filepath.Dir(destination) {
		return errors.New("atomic replacement requires files in the same directory")
	}
	return replace(staged, destination)
}
