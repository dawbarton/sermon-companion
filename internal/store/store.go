package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dawbarton/sermon-companion/internal/atomicfile"
)

type Store struct {
	root     string
	mu       sync.Mutex
	problems map[string]string
}

var ErrRevisionConflict = errors.New("session revision conflict")

func New(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("data directory is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	root = absoluteRoot
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return &Store{root: root, problems: make(map[string]string)}, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) SessionDir(id string) (string, error) {
	if !validID(id) {
		return "", errors.New("invalid session ID")
	}
	return filepath.Join(s.root, "sessions", id), nil
}

// SessionFile resolves a session-relative path recorded in a snapshot. A
// corrupted or hand-edited session.json must not be able to name a file
// outside its own session directory.
func (s *Store) SessionFile(id, stored string) (string, error) {
	dir, err := s.SessionDir(id)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(stored) == "" {
		return "", errors.New("no file is recorded for this session")
	}
	path := filepath.Join(dir, filepath.FromSlash(stored))
	relative, err := filepath.Rel(dir, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("recorded file is outside the session directory")
	}
	return path, nil
}

func (s *Store) Create(title, church string, now time.Time) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := now.Format("2006-01-02_150405") + "_" + randomID(3)
	dir := filepath.Join(s.root, "sessions", id)
	if err := os.MkdirAll(filepath.Join(dir, "exports"), 0o755); err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		title = now.Format("2 January 2006")
	}
	if strings.TrimSpace(church) == "" {
		church = "Church"
	}
	session := &Session{
		SchemaVersion: SchemaVersion,
		ID:            id,
		Title:         strings.TrimSpace(title),
		Church:        strings.TrimSpace(church),
		Status:        "starting",
		StartedAt:     now.UTC(),
		AudioFile:     "audio.part.flac",
		Segments:      []Segment{},
		Markers:       []Marker{},
	}
	if err := s.saveLocked(session, "session.created", map[string]any{"title": session.Title, "church": session.Church}); err != nil {
		return nil, err
	}
	return clone(session), nil
}

// Delete removes a session directory and everything in it, including its
// journal and its exports. Retention is the only caller: a service is kept on
// this machine only until its MP3 has been published elsewhere.
func (s *Store) Delete(id string) error {
	return s.DeleteAtRevision(id, nil)
}

// DeleteAtRevision removes a session only if the caller saw its current
// revision. Retention passes nil because it makes its decision from the store
// while no operator request is involved.
func (s *Store) DeleteAtRevision(id string, expected *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.SessionDir(id)
	if err != nil {
		return err
	}
	if expected != nil {
		session, err := s.getLocked(id)
		if err != nil {
			return err
		}
		if session.Revision != *expected {
			return fmt.Errorf("%w: expected %d, current %d", ErrRevisionConflict, *expected, session.Revision)
		}
	}
	return os.RemoveAll(dir)
}

func (s *Store) Get(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}

func (s *Store) List() ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(filepath.Join(s.root, "sessions"))
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		session, err := s.getLocked(entry.Name())
		if err != nil {
			s.problems[entry.Name()] = err.Error()
			continue
		}
		delete(s.problems, entry.Name())
		out = append(out, *session)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// Problems reports session directories that could not be recovered or read.
// Healthy sessions remain available, but damaged data must not disappear from
// the interface without leaving an actionable message in the application log.
func (s *Store) Problems() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.problems))
	for id := range s.problems {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	problems := make([]string, 0, len(ids))
	for _, id := range ids {
		problems = append(problems, fmt.Sprintf("session %s: %s", id, s.problems[id]))
	}
	return problems
}

func (s *Store) Update(id, eventType string, payload interface{}, mutate func(*Session) error) (*Session, error) {
	return s.UpdateAtRevision(id, nil, eventType, payload, mutate)
}

// UpdateAtRevision applies a mutation only if expected is nil or matches the
// current snapshot. HTTP clients use it to prevent a late save from silently
// overwriting a newer edit; internal lifecycle operations pass nil via Update.
func (s *Store) UpdateAtRevision(id string, expected *int64, eventType string, payload interface{}, mutate func(*Session) error) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.getLocked(id)
	if err != nil {
		return nil, err
	}
	if expected != nil && session.Revision != *expected {
		return nil, fmt.Errorf("%w: expected %d, current %d", ErrRevisionConflict, *expected, session.Revision)
	}
	if err := mutate(session); err != nil {
		return nil, err
	}
	if err := s.saveLocked(session, eventType, payload); err != nil {
		return nil, err
	}
	return clone(session), nil
}

func (s *Store) saveLocked(session *Session, eventType string, payload interface{}) error {
	if session == nil {
		return errors.New("session is required")
	}
	if session.SchemaVersion < 1 || session.SchemaVersion > SchemaVersion {
		return fmt.Errorf("unsupported session schema version %d", session.SchemaVersion)
	}
	dir, err := s.SessionDir(session.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	session.Revision++
	event := Event{Sequence: session.Revision, At: time.Now().UTC(), Type: eventType, SessionID: session.ID, Payload: payload}
	eventBytes, err := json.Marshal(event)
	if err != nil {
		return err
	}
	snapshot, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	staged := filepath.Join(dir, stagedSnapshotName)
	if err := atomicfile.WriteStaged(staged, append(snapshot, '\n'), 0o644); err != nil {
		return err
	}
	if err := appendJournalEvent(filepath.Join(dir, journalName), append(eventBytes, '\n')); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return atomicfile.Replace(staged, filepath.Join(dir, snapshotName))
}

func (s *Store) getLocked(id string) (*Session, error) {
	if !validID(id) {
		return nil, errors.New("invalid session ID")
	}
	dir, _ := s.SessionDir(id)
	if err := recoverSessionFiles(dir, id); err != nil {
		return nil, err
	}
	return readSnapshot(filepath.Join(dir, snapshotName), id)
}

func (s *Store) Events(id string) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.SessionDir(id)
	if err != nil {
		return nil, err
	}
	if err := recoverSessionFiles(dir, id); err != nil {
		return nil, err
	}
	return readJournal(filepath.Join(dir, journalName), id, true)
}

func clone(session *Session) *Session {
	data, _ := json.Marshal(session)
	var out Session
	_ = json.Unmarshal(data, &out)
	return &out
}

func randomID(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%06d", time.Now().UnixNano()%1_000_000)
	}
	return hex.EncodeToString(b)
}

func NewObjectID(prefix string) string { return prefix + "_" + randomID(6) }

func validID(id string) bool {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) {
		return false
	}
	for _, r := range id {
		if !(r == '_' || r == '-' || r == '.' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return false
		}
	}
	return true
}
