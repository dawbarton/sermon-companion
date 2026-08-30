package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
	"github.com/dawbarton/sermon-companion/internal/waveform"
)

type Server struct {
	config   config.Config
	store    *store.Store
	capture  *capture.Manager
	master   *master.Master
	waveform *waveform.Generator
	static   fs.FS
	jobsMu   sync.Mutex
	jobs     map[string]bool
}

func NewServer(c config.Config, sessions *store.Store, captureManager *capture.Manager, mastering *master.Master, static fs.FS) *Server {
	return &Server{config: c, store: sessions, capture: captureManager, master: mastering, waveform: waveform.New(c.FFmpeg, sessions), static: static, jobs: map[string]bool{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/sessions", s.listSessions)
	mux.HandleFunc("POST /api/sessions", s.startSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	mux.HandleFunc("POST /api/sessions/{id}/stop", s.stopSession)
	mux.HandleFunc("POST /api/sessions/{id}/segments", s.startSegment)
	mux.HandleFunc("PATCH /api/sessions/{id}/segments/{segmentID}", s.patchSegment)
	mux.HandleFunc("POST /api/sessions/{id}/segments/{segmentID}/stop", s.stopSegment)
	mux.HandleFunc("POST /api/sessions/{id}/markers", s.addMarker)
	mux.HandleFunc("POST /api/sessions/{id}/export", s.export)
	mux.HandleFunc("GET /api/sessions/{id}/audio", s.audio)
	mux.HandleFunc("GET /api/sessions/{id}/waveform", s.waveformEnvelope)
	mux.HandleFunc("GET /api/sessions/{id}/export-file", s.exportFile)
	mux.HandleFunc("GET /api/sessions/{id}/events", s.events)
	assets, _ := fs.Sub(s.static, "static")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /dock", s.page("dock.html"))
	mux.HandleFunc("GET /", s.page("index.html"))
	return s.localOnlyHeaders(mux)
}

func (s *Server) localOnlyHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; media-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/dock" {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(s.static, "static/"+name)
		if err != nil {
			http.Error(w, "page unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	id, elapsed, active := s.capture.Active()
	var session *store.Session
	if active {
		session, _ = s.store.Get(id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": active, "elapsedSeconds": elapsed, "session": session, "presets": s.config.Presets})
}

func (s *Server) listSessions(w http.ResponseWriter, _ *http.Request) {
	sessions, err := s.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	session, err := s.capture.Start(request.Title)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	id, _, active := s.capture.Active()
	if !active || id != r.PathValue("id") {
		writeError(w, http.StatusConflict, errors.New("this session is not recording"))
		return
	}
	session, err := s.capture.Stop()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) startSegment(w http.ResponseWriter, r *http.Request) {
	id, elapsed, active := s.capture.Active()
	if !active || id != r.PathValue("id") {
		writeError(w, http.StatusConflict, errors.New("segments can only be started during the active recording"))
		return
	}
	var request struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.Kind, request.Label = strings.TrimSpace(request.Kind), strings.TrimSpace(request.Label)
	if request.Kind == "" {
		request.Kind = "custom"
	}
	if request.Label == "" {
		request.Label = request.Kind
	}
	now := time.Now().UTC()
	segment := store.Segment{ID: store.NewObjectID("seg"), Kind: request.Kind, Label: request.Label, Start: elapsed, Include: true, CreatedAt: now, UpdatedAt: now}
	session, err := s.store.Update(id, "segment.started", segment, func(session *store.Session) error {
		closeOpenSegments(session, elapsed, now)
		session.Segments = append(session.Segments, segment)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) stopSegment(w http.ResponseWriter, r *http.Request) {
	id, elapsed, active := s.capture.Active()
	if !active || id != r.PathValue("id") {
		writeError(w, http.StatusConflict, errors.New("this session is not recording"))
		return
	}
	segmentID := r.PathValue("segmentID")
	session, err := s.store.Update(id, "segment.stopped", map[string]any{"segmentId": segmentID, "atSeconds": elapsed}, func(session *store.Session) error {
		segment := findSegment(session, segmentID)
		if segment == nil {
			return errors.New("segment not found")
		}
		if segment.End != nil {
			return errors.New("segment is already closed")
		}
		if elapsed <= segment.Start {
			return errors.New("segment end must be after its start")
		}
		segment.End, segment.UpdatedAt = floatPointer(elapsed), time.Now().UTC()
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) patchSegment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Kind    *string  `json:"kind"`
		Label   *string  `json:"label"`
		Start   *float64 `json:"startSeconds"`
		End     *float64 `json:"endSeconds"`
		Include *bool    `json:"include"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id, segmentID := r.PathValue("id"), r.PathValue("segmentID")
	beforeSession, err := s.store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	before := findSegment(beforeSession, segmentID)
	if before == nil {
		writeError(w, http.StatusNotFound, errors.New("segment not found"))
		return
	}
	beforeCopy := *before
	session, err := s.store.Update(id, "segment.adjusted", map[string]any{"before": beforeCopy, "requested": request}, func(session *store.Session) error {
		segment := findSegment(session, segmentID)
		if request.Kind != nil && strings.TrimSpace(*request.Kind) != "" {
			segment.Kind = strings.TrimSpace(*request.Kind)
		}
		if request.Label != nil && strings.TrimSpace(*request.Label) != "" {
			segment.Label = strings.TrimSpace(*request.Label)
		}
		if request.Start != nil {
			segment.Start = *request.Start
		}
		if request.End != nil {
			segment.End = floatPointer(*request.End)
		}
		if request.Include != nil {
			segment.Include = *request.Include
		}
		if segment.Start < 0 || segment.End == nil || *segment.End <= segment.Start {
			return errors.New("segment times must satisfy 0 ≤ start < end")
		}
		if session.Duration > 0 && *segment.End > session.Duration+1 {
			return fmt.Errorf("segment ends beyond the recording (%.1f seconds)", session.Duration)
		}
		segment.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) addMarker(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Kind  string   `json:"kind"`
		Label string   `json:"label"`
		At    *float64 `json:"atSeconds"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id, elapsed, active := s.capture.Active()
	at := request.At
	if at == nil {
		if !active || id != r.PathValue("id") {
			writeError(w, http.StatusBadRequest, errors.New("atSeconds is required for a finished session"))
			return
		}
		at = &elapsed
	}
	if *at < 0 {
		writeError(w, http.StatusBadRequest, errors.New("marker time cannot be negative"))
		return
	}
	if strings.TrimSpace(request.Kind) == "" {
		request.Kind = "note"
	}
	if strings.TrimSpace(request.Label) == "" {
		request.Label = "Marker"
	}
	marker := store.Marker{ID: store.NewObjectID("mark"), Kind: strings.TrimSpace(request.Kind), Label: strings.TrimSpace(request.Label), At: *at, CreatedAt: time.Now().UTC()}
	session, err := s.store.Update(r.PathValue("id"), "marker.added", marker, func(session *store.Session) error { session.Markers = append(session.Markers, marker); return nil })
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.jobsMu.Lock()
	if s.jobs[id] {
		s.jobsMu.Unlock()
		writeError(w, http.StatusConflict, errors.New("an export is already running"))
		return
	}
	s.jobs[id] = true
	s.jobsMu.Unlock()
	go func() {
		if err := s.master.Export(id); err != nil {
			log.Printf("export %s: %v", id, err)
		}
		s.jobsMu.Lock()
		delete(s.jobs, id)
		s.jobsMu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "starting"})
}

func (s *Server) audio(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dir, _ := s.store.SessionDir(session.ID)
	serveFile(w, r, filepath.Join(dir, session.AudioFile), "audio/flac", false)
}

func (s *Server) waveformEnvelope(w http.ResponseWriter, r *http.Request) {
	envelope, err := s.waveform.Generate(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, envelope)
}

func (s *Server) exportFile(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.Get(r.PathValue("id"))
	if err != nil || session.Export == nil || session.Export.Output == "" {
		http.NotFound(w, r)
		return
	}
	dir, _ := s.store.SessionDir(session.ID)
	serveFile(w, r, filepath.Join(dir, filepath.FromSlash(session.Export.Output)), "audio/mpeg", true)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.Events(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func serveFile(w http.ResponseWriter, r *http.Request, path, contentType string, download bool) {
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	if download {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	}
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func closeOpenSegments(session *store.Session, elapsed float64, now time.Time) {
	for i := range session.Segments {
		if session.Segments[i].End == nil && elapsed > session.Segments[i].Start {
			session.Segments[i].End, session.Segments[i].UpdatedAt = floatPointer(elapsed), now
		}
	}
}

func findSegment(session *store.Session, id string) *store.Segment {
	for i := range session.Segments {
		if session.Segments[i].ID == id {
			return &session.Segments[i]
		}
	}
	return nil
}

func floatPointer(v float64) *float64 { return &v }

func decodeJSON(r *http.Request, destination interface{}) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func normalisedSegments(session *store.Session) []store.Segment {
	segments := append([]store.Segment(nil), session.Segments...)
	sort.SliceStable(segments, func(i, j int) bool {
		return segments[i].Start < segments[j].Start || math.Abs(segments[i].Start-segments[j].Start) < 1e-9 && segments[i].CreatedAt.Before(segments[j].CreatedAt)
	})
	return segments
}
