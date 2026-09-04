package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dawbarton/sermon-companion/internal/applog"
	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
	"github.com/dawbarton/sermon-companion/internal/waveform"
)

type Server struct {
	settings   *config.Settings
	store      *store.Store
	capture    *capture.Manager
	master     *master.Master
	waveform   *waveform.Generator
	static     fs.FS
	openFolder func(string) error
	openLink   func(string) error
	log        *applog.Log
	version    string
	jobsMu     sync.Mutex
	jobs       map[string]bool
}

func NewServer(settings *config.Settings, sessions *store.Store, captureManager *capture.Manager, mastering *master.Master, static fs.FS) *Server {
	return &Server{settings: settings, store: sessions, capture: captureManager, master: mastering, waveform: waveform.New(settings.Get().FFmpeg, sessions), static: static, openFolder: openFolder, openLink: OpenInBrowser, jobs: map[string]bool{}}
}

// SetLog gives the interface the running log to display, and the version to
// show beside it. The application is linked without a console on Windows, so the
// log page is where its version is read from. Both are optional: the tests
// construct a server without them.
func (s *Server) SetLog(l *applog.Log, version string) { s.log, s.version = l, version }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/sessions", s.listSessions)
	mux.HandleFunc("POST /api/sessions", s.startSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	mux.HandleFunc("PATCH /api/sessions/{id}", s.patchSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.deleteSession)
	mux.HandleFunc("POST /api/sessions/{id}/stop", s.stopSession)
	mux.HandleFunc("POST /api/sessions/{id}/segments", s.startSegment)
	mux.HandleFunc("POST /api/sessions/{id}/segments/manual", s.addManualSegment)
	mux.HandleFunc("PATCH /api/sessions/{id}/segments/{segmentID}", s.patchSegment)
	mux.HandleFunc("DELETE /api/sessions/{id}/segments/{segmentID}", s.archiveSegment)
	mux.HandleFunc("POST /api/sessions/{id}/segments/{segmentID}/stop", s.stopSegment)
	mux.HandleFunc("POST /api/sessions/{id}/segments/{segmentID}/restore", s.restoreSegment)
	mux.HandleFunc("POST /api/sessions/{id}/markers", s.addMarker)
	mux.HandleFunc("POST /api/sessions/{id}/export", s.export)
	mux.HandleFunc("GET /api/sessions/{id}/audio", s.audio)
	mux.HandleFunc("GET /api/sessions/{id}/waveform", s.waveformEnvelope)
	mux.HandleFunc("GET /api/sessions/{id}/export-file", s.exportFile)
	mux.HandleFunc("POST /api/sessions/{id}/open-export-folder", s.openExportFolder)
	mux.HandleFunc("POST /api/open-review-page", s.openReviewPage)
	mux.HandleFunc("GET /api/devices", s.listDevices)
	mux.HandleFunc("POST /api/devices", s.selectDevice)
	mux.HandleFunc("GET /api/log", s.readLog)
	mux.HandleFunc("POST /api/open-log-folder", s.openLogFolder)
	mux.HandleFunc("GET /api/sessions/{id}/events", s.events)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", s.assets()))
	mux.HandleFunc("GET /dock", s.page("dock.html"))
	mux.HandleFunc("GET /log", s.page("log.html"))
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

// assets serves the embedded browser files. An embedded file reports no
// modification time, so http.FileServer sends nothing a browser can revalidate
// against, and a cached script can outlive an upgrade and run against an API it
// no longer matches. Each file is given an ETag from its own contents and must
// be checked before it is reused.
func (s *Server) assets() http.Handler {
	assets, _ := fs.Sub(s.static, "static")
	fileServer := http.FileServer(http.FS(assets))
	tags := map[string]string{}
	_ = fs.WalkDir(assets, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		data, readErr := fs.ReadFile(assets, path)
		if readErr != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		tags[path] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The prefix strip leaves the path without its leading slash, which
		// http.FileServer puts back for itself.
		if tag, ok := tags[strings.TrimPrefix(r.URL.Path, "/")]; ok {
			w.Header().Set("ETag", tag)
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/dock" && r.URL.Path != "/log" {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(s.static, "static/"+name)
		if err != nil {
			http.Error(w, "page unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The page names the scripts it needs, so it must not be reused from a
		// cache after an upgrade without being checked.
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	}
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	id, position, captureInfo, active := s.capture.Active()
	var session *store.Session
	if active {
		session, _ = s.store.Get(id)
		if session != nil {
			session.Capture = captureInfo
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": active, "elapsedSeconds": position.Seconds, "framePosition": position.Frames, "capture": captureInfo, "session": session, "presets": s.settings.Get().Presets})
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
	s.writeSession(w, http.StatusCreated, session)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	s.writeSession(w, http.StatusOK, session)
}

// writeSession answers with a service, filling in the settings it inherits from
// config.json. The review page then shows the values an export would actually
// use rather than empty boxes.
func (s *Server) writeSession(w http.ResponseWriter, status int, session *store.Session) {
	c := s.settings.Get()
	if strings.TrimSpace(session.Church) == "" {
		session.Church = c.Church
	}
	if session.GapSeconds == nil {
		gap := c.Master.GapBetweenSegments()
		session.GapSeconds = &gap
	}
	writeJSON(w, status, session)
}

func (s *Server) patchSession(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Title      *string  `json:"title"`
		Church     *string  `json:"church"`
		GapSeconds *float64 `json:"gapSeconds"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	before, err := s.store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	session, err := s.store.Update(id, "session.metadata_updated", map[string]any{"before": map[string]string{"title": before.Title, "church": before.Church}, "requested": request}, func(session *store.Session) error {
		if session.Export != nil && session.Export.Status == "running" {
			return errors.New("service details cannot be changed while an MP3 is being created")
		}
		changed := false
		if request.Title != nil {
			title := strings.TrimSpace(*request.Title)
			if title == "" {
				return errors.New("service title is required")
			}
			changed = changed || title != session.Title
			session.Title = title
		}
		if request.Church != nil {
			church := strings.TrimSpace(*request.Church)
			if church == "" {
				return errors.New("church is required")
			}
			changed = changed || church != session.Church
			session.Church = church
		}
		if request.GapSeconds != nil {
			gap := *request.GapSeconds
			if math.IsNaN(gap) || gap < 0 || gap > config.MaximumGapSeconds {
				return fmt.Errorf("silence between segments must be between 0 and %g seconds", config.MaximumGapSeconds)
			}
			gap = math.Round(gap*10) / 10
			// A service with nothing set of its own is currently exported with
			// the configured gap, so that is what the change is measured against.
			previous := s.settings.Get().Master.GapBetweenSegments()
			if session.GapSeconds != nil {
				previous = *session.GapSeconds
			}
			changed = changed || math.Abs(gap-previous) > 1e-9
			session.GapSeconds = &gap
		}
		if changed {
			markExportStale(session)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeSession(w, http.StatusOK, session)
}

// deleteSession removes a finished service and everything recorded with it:
// the audio, the journal, and every MP3 made from it. Retention does the same
// thing on a timer, and the operator clearing out services already published to
// the church website is the same act done sooner.
func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	activeID, _, _, active := s.capture.Active()
	if (active && activeID == id) || session.Status == "recording" || session.Status == "starting" {
		writeError(w, http.StatusConflict, errors.New("stop the recording before deleting this service"))
		return
	}
	s.jobsMu.Lock()
	exporting := s.jobs[id]
	s.jobsMu.Unlock()
	if exporting {
		writeError(w, http.StatusConflict, errors.New("wait for the MP3 to finish before deleting this service"))
		return
	}
	if err := s.store.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	log.Printf("deleted service %s (%s) at the operator's request", id, session.Title)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	id, _, _, active := s.capture.Active()
	if !active || id != r.PathValue("id") {
		writeError(w, http.StatusConflict, errors.New("this session is not recording"))
		return
	}
	session, err := s.capture.Stop()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// The review page is opened as soon as the service ends, and its first
	// request would otherwise decode the whole recording while the operator is
	// already trying to listen to it. Build the waveform now, so that nothing is
	// competing with the recording for the disk when a segment is first played.
	go func(id string) {
		if _, err := s.waveform.Generate(context.Background(), id); err != nil {
			log.Printf("waveform for %s: %v", id, err)
		}
	}(session.ID)
	s.writeSession(w, http.StatusOK, session)
}

func (s *Server) startSegment(w http.ResponseWriter, r *http.Request) {
	id, position, active := s.capture.MarkPosition(time.Now())
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
		request.Kind = kindFromLabel(request.Label)
	}
	if request.Label == "" {
		request.Label = request.Kind
	}
	now := time.Now().UTC()
	segment := store.Segment{ID: store.NewObjectID("seg"), Kind: request.Kind, Label: request.Label, StartFrame: position.Frames, Start: position.Seconds, Include: true, CreatedAt: now, UpdatedAt: now}
	session, err := s.store.Update(id, "segment.started", segment, func(session *store.Session) error {
		closeOpenSegments(session, position, now)
		session.Segments = append(session.Segments, segment)
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeSession(w, http.StatusCreated, session)
}

func (s *Server) addManualSegment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Kind    string  `json:"kind"`
		Label   string  `json:"label"`
		Start   float64 `json:"startSeconds"`
		End     float64 `json:"endSeconds"`
		Include *bool   `json:"include"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.Kind, request.Label = strings.TrimSpace(request.Kind), strings.TrimSpace(request.Label)
	if request.Kind == "" {
		request.Kind = kindFromLabel(request.Label)
	}
	if request.Label == "" {
		writeError(w, http.StatusBadRequest, errors.New("segment label is required"))
		return
	}
	include := true
	if request.Include != nil {
		include = *request.Include
	}
	id := r.PathValue("id")
	existing, err := s.store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	rate := sessionSampleRate(existing, s.settings.Get().Capture.SampleRate)
	now := time.Now().UTC()
	end := request.End
	startFrame, endFrame := secondsToFrame(request.Start, rate), secondsToFrame(request.End, rate)
	segment := store.Segment{ID: store.NewObjectID("seg"), Kind: request.Kind, Label: request.Label, StartFrame: startFrame, EndFrame: &endFrame, Start: request.Start, End: &end, Include: include, CreatedAt: now, UpdatedAt: now}
	session, err := s.store.Update(id, "segment.added_manually", segment, func(session *store.Session) error {
		if session.Status == "recording" || session.Status == "starting" {
			return errors.New("use the OBS dock to mark segments while recording")
		}
		if segment.Start < 0 || *segment.End <= segment.Start {
			return errors.New("segment times must satisfy 0 ≤ start < end")
		}
		if session.Duration > 0 && *segment.End > session.Duration+1 {
			return fmt.Errorf("segment ends beyond the recording (%.1f seconds)", session.Duration)
		}
		session.Segments = append(session.Segments, segment)
		store.SnapSegmentBoundaries(session.Segments, segment.ID, 0.051)
		syncSegmentFrames(session, s.settings.Get().Capture.SampleRate)
		if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
			return err
		}
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeSession(w, http.StatusCreated, session)
}

func (s *Server) stopSegment(w http.ResponseWriter, r *http.Request) {
	id, position, active := s.capture.MarkPosition(time.Now())
	if !active || id != r.PathValue("id") {
		writeError(w, http.StatusConflict, errors.New("this session is not recording"))
		return
	}
	segmentID := r.PathValue("segmentID")
	session, err := s.store.Update(id, "segment.stopped", map[string]any{"segmentId": segmentID, "position": position}, func(session *store.Session) error {
		segment := findSegment(session, segmentID)
		if segment == nil {
			return errors.New("segment not found")
		}
		if segment.Archived {
			return errors.New("segment has been removed")
		}
		if segment.End != nil {
			return errors.New("segment is already closed")
		}
		if position.Frames <= segment.StartFrame {
			return errors.New("segment end must be after its start")
		}
		segment.EndFrame, segment.End, segment.UpdatedAt = uint64Pointer(position.Frames), floatPointer(position.Seconds), time.Now().UTC()
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeSession(w, http.StatusOK, session)
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
	if before == nil || before.Archived {
		writeError(w, http.StatusNotFound, errors.New("segment not found"))
		return
	}
	beforeCopy := *before
	session, err := s.store.Update(id, "segment.adjusted", map[string]any{"before": beforeCopy, "requested": request}, func(session *store.Session) error {
		if session.Status == "recording" || session.Status == "starting" {
			return errors.New("segments cannot be adjusted while recording")
		}
		segment := findSegment(session, segmentID)
		if request.Kind != nil && strings.TrimSpace(*request.Kind) != "" {
			segment.Kind = strings.TrimSpace(*request.Kind)
		}
		if request.Label != nil && strings.TrimSpace(*request.Label) != "" {
			segment.Label = strings.TrimSpace(*request.Label)
		}
		if request.Start != nil {
			segment.Start = *request.Start
			segment.StartFrame = secondsToFrame(*request.Start, sessionSampleRate(session, s.settings.Get().Capture.SampleRate))
		}
		if request.End != nil {
			segment.End = floatPointer(*request.End)
			segment.EndFrame = uint64Pointer(secondsToFrame(*request.End, sessionSampleRate(session, s.settings.Get().Capture.SampleRate)))
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
		store.SnapSegmentBoundaries(session.Segments, segment.ID, 0.051)
		syncSegmentFrames(session, s.settings.Get().Capture.SampleRate)
		if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
			return err
		}
		segment.UpdatedAt = time.Now().UTC()
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeSession(w, http.StatusOK, session)
}

func (s *Server) archiveSegment(w http.ResponseWriter, r *http.Request) {
	id, segmentID := r.PathValue("id"), r.PathValue("segmentID")
	beforeSession, err := s.store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	before := findSegment(beforeSession, segmentID)
	if before == nil || before.Archived {
		writeError(w, http.StatusNotFound, errors.New("segment not found"))
		return
	}
	beforeCopy := *before
	session, err := s.store.Update(id, "segment.archived", beforeCopy, func(session *store.Session) error {
		if session.Status == "recording" || session.Status == "starting" {
			return errors.New("segments cannot be removed while recording")
		}
		segment := findSegment(session, segmentID)
		if segment == nil || segment.Archived {
			return errors.New("segment not found")
		}
		segment.Archived, segment.UpdatedAt = true, time.Now().UTC()
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeSession(w, http.StatusOK, session)
}

func (s *Server) restoreSegment(w http.ResponseWriter, r *http.Request) {
	id, segmentID := r.PathValue("id"), r.PathValue("segmentID")
	session, err := s.store.Update(id, "segment.restored", map[string]string{"segmentId": segmentID}, func(session *store.Session) error {
		if session.Status == "recording" || session.Status == "starting" {
			return errors.New("segments cannot be restored while recording")
		}
		segment := findSegment(session, segmentID)
		if segment == nil || !segment.Archived {
			return errors.New("removed segment not found")
		}
		segment.Archived, segment.UpdatedAt = false, time.Now().UTC()
		store.SnapSegmentBoundaries(session.Segments, segment.ID, 0.051)
		syncSegmentFrames(session, s.settings.Get().Capture.SampleRate)
		if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
			return err
		}
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeSession(w, http.StatusOK, session)
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
	id, position, active := s.capture.MarkPosition(time.Now())
	at := request.At
	atFrame := uint64(0)
	if at == nil {
		if !active || id != r.PathValue("id") {
			writeError(w, http.StatusBadRequest, errors.New("atSeconds is required for a finished session"))
			return
		}
		at = &position.Seconds
		atFrame = position.Frames
	} else {
		session, err := s.store.Get(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, errors.New("session not found"))
			return
		}
		atFrame = secondsToFrame(*at, sessionSampleRate(session, s.settings.Get().Capture.SampleRate))
	}
	if *at < 0 {
		writeError(w, http.StatusBadRequest, errors.New("marker time cannot be negative"))
		return
	}
	if strings.TrimSpace(request.Label) == "" {
		request.Label = "Marker"
	}
	if strings.TrimSpace(request.Kind) == "" {
		request.Kind = kindFromLabel(request.Label)
	}
	marker := store.Marker{ID: store.NewObjectID("mark"), Kind: strings.TrimSpace(request.Kind), Label: strings.TrimSpace(request.Label), AtFrame: atFrame, At: *at, CreatedAt: time.Now().UTC()}
	session, err := s.store.Update(r.PathValue("id"), "marker.added", marker, func(session *store.Session) error { session.Markers = append(session.Markers, marker); return nil })
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeSession(w, http.StatusCreated, session)
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
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
	// The recording grows under a fixed name, so a partial response must never
	// be reused for the finished file.
	if session.Status == "recording" || session.Status == "starting" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	path, err := s.store.SessionFile(session.ID, session.AudioFile)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, path, "audio/flac", false)
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
	path, err := s.store.SessionFile(session.ID, session.Export.Output)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, path, "audio/mpeg", true)
}

func (s *Server) openExportFolder(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.Get(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	dir, _ := s.store.SessionDir(r.PathValue("id"))
	exportDir := filepath.Join(dir, "exports")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.openFolder(exportDir); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("open MP3 folder: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "opened"})
}

// openReviewPage exists because the dock runs inside OBS's embedded browser,
// where an ordinary link would open in the dock panel itself.
func (s *Server) openReviewPage(w http.ResponseWriter, _ *http.Request) {
	url := LocalURL(s.settings.Get().Listen)
	if err := s.openLink(url); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("open the review page: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "opened", "url": url})
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

func closeOpenSegments(session *store.Session, position capture.Position, now time.Time) {
	for index := range session.Segments {
		if session.Segments[index].End == nil && position.Frames > session.Segments[index].StartFrame {
			session.Segments[index].EndFrame = uint64Pointer(position.Frames)
			session.Segments[index].End, session.Segments[index].UpdatedAt = floatPointer(position.Seconds), now
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
func uint64Pointer(v uint64) *uint64  { return &v }

func sessionSampleRate(session *store.Session, fallback int) int {
	if session != nil && session.Capture.SampleRate > 0 {
		return session.Capture.SampleRate
	}
	if fallback > 0 {
		return fallback
	}
	return 48_000
}

func secondsToFrame(seconds float64, sampleRate int) uint64 {
	if seconds <= 0 || sampleRate <= 0 {
		return 0
	}
	return uint64(math.Round(seconds * float64(sampleRate)))
}

func syncSegmentFrames(session *store.Session, fallbackRate int) {
	rate := sessionSampleRate(session, fallbackRate)
	for index := range session.Segments {
		segment := &session.Segments[index]
		segment.StartFrame = secondsToFrame(segment.Start, rate)
		if segment.End == nil {
			segment.EndFrame = nil
		} else {
			segment.EndFrame = uint64Pointer(secondsToFrame(*segment.End, rate))
		}
	}
}

func markExportStale(session *store.Session) {
	if session.Export != nil && session.Export.Status == "completed" {
		session.Export.Status = "stale"
		session.Export.Error = "Service details or segments changed after this MP3 was created."
	}
}

func kindFromLabel(label string) string {
	var out strings.Builder
	separator := false
	for _, r := range strings.ToLower(strings.TrimSpace(label)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if separator && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(r)
			separator = false
		case r == '\'' || r == '’':
		default:
			separator = out.Len() > 0
		}
	}
	if out.Len() == 0 {
		return "custom"
	}
	return strings.TrimRight(out.String(), "-")
}

func openFolder(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer.exe", path)
	case "darwin":
		command = exec.Command("open", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	return start(command)
}

// OpenInBrowser opens a local page in the operator's default web browser.
func OpenInBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return start(command)
}

// LocalURL turns a listen address into a page address an operator can open,
// substituting the loopback host for a wildcard or omitted one.
func LocalURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

func start(command *exec.Cmd) error {
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

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
