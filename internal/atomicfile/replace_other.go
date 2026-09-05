//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
)

func replace(staged, destination string) error {
	if err := os.Rename(staged, destination); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
