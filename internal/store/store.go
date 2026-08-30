package store

import (
	"bufio"
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
)

type Store struct {
	root string
	mu   sync.Mutex
}

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
	return &Store{root: root}, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) SessionDir(id string) (string, error) {
	if !validID(id) {
		return "", errors.New("invalid session ID")
	}
	return filepath.Join(s.root, "sessions", id), nil
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
		if err == nil {
			out = append(out, *session)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

func (s *Store) Update(id, eventType string, payload interface{}, mutate func(*Session) error) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.getLocked(id)
	if err != nil {
		return nil, err
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
	dir := filepath.Join(s.root, "sessions", session.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	session.Revision++
	event := Event{Sequence: session.Revision, At: time.Now().UTC(), Type: eventType, SessionID: session.ID, Payload: payload}
	eventBytes, err := json.Marshal(event)
	if err != nil {
		return err
	}
	eventsPath := filepath.Join(dir, "events.jsonl")
	events, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err = events.Write(append(eventBytes, '\n')); err == nil {
		err = events.Sync()
	}
	closeErr := events.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}

	snapshot, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "session.json.tmp")
	if err := os.WriteFile(tmp, append(snapshot, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "session.json"))
}

func (s *Store) getLocked(id string) (*Session, error) {
	if !validID(id) {
		return nil, errors.New("invalid session ID")
	}
	data, err := os.ReadFile(filepath.Join(s.root, "sessions", id, "session.json"))
	if err != nil {
		return nil, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *Store) Events(id string) ([]Event, error) {
	dir, err := s.SessionDir(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event Event
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			events = append(events, event)
		}
	}
	return events, scanner.Err()
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
