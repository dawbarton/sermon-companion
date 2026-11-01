// The retry record for one session's upload: which site and MP3 it was for,
// and every ID the site has given so far. A failed upload is resumed from it,
// a finished one is not repeated, and -delete removes exactly what it lists.
// It lives outside the session folder, under work/publish-state by default,
// because this standalone tool does not write into the application's sessions.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dawbarton/sermon-companion/internal/atomicfile"
	"github.com/dawbarton/sermon-companion/internal/publish"
)

type record struct {
	Session  string           `json:"session"`
	Site     string           `json:"site"`
	MP3      string           `json:"mp3"`
	Size     int64            `json:"size"`
	Progress publish.Progress `json:"progress"`
	Updated  time.Time        `json:"updated"`
}

func recordPath(dir, session string) string {
	return filepath.Join(dir, session+".json")
}

// loadRecord reads the record at path, reporting whether there was one.
func loadRecord(path string) (*record, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &record{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, false, fmt.Errorf("could not read the upload record %s: %w", path, err)
	}
	return &r, true, nil
}

// save writes the record so that it is either the old version or the new one,
// never half of each, whatever happens part way.
func (r *record) save(path string) error {
	r.Updated = time.Now().UTC()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := atomicfile.Write(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("could not save the upload record %s: %w", path, err)
	}
	return nil
}
