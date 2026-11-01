// Reading a finished session for upload, without changing it. The standalone
// publisher must not write into the application's session folders, but the
// store's own Get may repair an interrupted save or migrate an old schema, both
// of which write. This file therefore reads session.json directly and refuses
// any session that would need either, asking for it to be opened in Sermon
// Companion first.
package publish

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dawbarton/sermon-companion/internal/store"
)

// These mirror the unexported names in internal/store/recovery.go.
const (
	snapshotName       = "session.json"
	stagedSnapshotName = "session.json.tmp"
)

// Export is a session's finished MP3, ready to be uploaded.
type Export struct {
	Session store.Session
	Dir     string // the session folder
	Path    string // the MP3
	Name    string // the MP3's file name
	Size    int64
}

// LoadExport reads the session in dir and finds its current MP3, refusing one
// that is missing, still being made, or out of date.
func LoadExport(dir string) (*Export, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, snapshotName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s is not a Sermon Companion session folder: it has no %s", dir, snapshotName)
	}
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, stagedSnapshotName)); err == nil {
		return nil, errors.New("this session has a save that was interrupted; open it in Sermon Companion once so that it can finish, then try again")
	}
	var session store.Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("could not read %s: %w", snapshotName, err)
	}
	if session.ID != filepath.Base(dir) {
		return nil, fmt.Errorf("%s belongs to session %q, not to the folder it is in", snapshotName, session.ID)
	}
	if session.SchemaVersion != store.SchemaVersion {
		return nil, fmt.Errorf("this session was saved in an older format (version %d; this publisher reads %d); open it in Sermon Companion once to bring it up to date",
			session.SchemaVersion, store.SchemaVersion)
	}
	if strings.TrimSpace(session.Church) == "" {
		return nil, errors.New("this session has no church, so there is no congregation to file the talk under")
	}

	export := session.Export
	switch {
	case export == nil:
		return nil, errors.New("no MP3 has been created for this session yet")
	case export.Status == "stale":
		return nil, errors.New("the MP3 is out of date: the service details or segments changed after it was created. Create a new MP3 first")
	case export.Status == "running":
		return nil, errors.New("the MP3 is still being created")
	case export.Status == "failed":
		return nil, fmt.Errorf("creating the MP3 failed: %s", export.Error)
	case export.Status != "completed":
		return nil, fmt.Errorf("the MP3's state is %q, which this publisher does not recognise", export.Status)
	}

	relative := filepath.Clean(filepath.FromSlash(export.Output))
	if export.Output == "" || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("the session names its MP3 as %q, which is not inside the session folder", export.Output)
	}
	path := filepath.Join(dir, relative)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("the MP3 the session names cannot be read: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("%s is not an MP3 file with anything in it", path)
	}
	return &Export{Session: session, Dir: dir, Path: path, Name: filepath.Base(path), Size: info.Size()}, nil
}
