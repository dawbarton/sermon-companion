// Package app is the local HTTP API and the browser pages it serves: the OBS
// dock, the review page, the upload page, and the log page. It listens on
// loopback and keeps little of its own: the background jobs it runs and what it
// has last read from the church website. Sessions belong to internal/store and
// recording to internal/capture.
//
// The handlers are grouped by what they act on:
//
//	server.go    the server, its routes, and the pages and assets
//	guard.go     refusing requests from other websites and rebound names
//	respond.go   strict JSON requests, revision preconditions, and replies
//	sessions.go  starting, stopping, changing, and deleting a service
//	segments.go  segments and markers, live and afterwards
//	export.go    making the MP3 and when an edit makes it stale
//	talk.go      the talk's details for the church website
//	upload.go    filling those in, and checking them against the website
//	publish.go   uploading the talk in the background
//	devices.go   listing and choosing the capture device
//	logview.go   the log page
//	launch.go    opening folders, pages, and the settings file
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dawbarton/sermon-companion/internal/applog"
	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/publish"
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
	openEditor func(string) error
	log        *applog.Log
	version    string
	jobsMu     sync.Mutex
	jobs       map[string]context.CancelFunc
	jobsWG     sync.WaitGroup
	background context.Context
	cancelWork context.CancelFunc
	closing    bool
	// publishHTTP makes the requests to the church website and YouTube, and
	// probeDuration measures an MP3 before it is uploaded. The tests replace
	// both.
	publishHTTP   *http.Client
	probeDuration func(ctx context.Context, ffprobe, path string) (time.Duration, error)
	web           website
}

func NewServer(settings *config.Settings, sessions *store.Store, captureManager *capture.Manager, mastering *master.Master, static fs.FS) *Server {
	background, cancelWork := context.WithCancel(context.Background())
	return &Server{settings: settings, store: sessions, capture: captureManager, master: mastering, waveform: waveform.New(settings.Get().FFmpeg, sessions), static: static, openFolder: openFolder, openLink: OpenInBrowser, openEditor: openInEditor, jobs: map[string]context.CancelFunc{}, background: background, cancelWork: cancelWork,
		publishHTTP: &http.Client{Timeout: 10 * time.Minute}, probeDuration: publish.ProbeDuration}
}

// Shutdown cancels active exports and waits for them to record their final
// state. The caller should stop accepting HTTP requests first.
func (s *Server) Shutdown(ctx context.Context) error {
	s.jobsMu.Lock()
	s.closing = true
	if s.cancelWork != nil {
		s.cancelWork()
	}
	for _, cancel := range s.jobs {
		cancel()
	}
	s.jobsMu.Unlock()
	done := make(chan struct{})
	go func() {
		s.jobsWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	mux.HandleFunc("PATCH /api/sessions/{id}/talk", s.patchTalk)
	mux.HandleFunc("POST /api/sessions/{id}/publish", s.startPublish)
	mux.HandleFunc("POST /api/sessions/{id}/talk/prefill", s.prefillTalk)
	mux.HandleFunc("GET /api/sessions/{id}/talk/check", s.checkTalk)
	mux.HandleFunc("GET /api/publish", s.publishSettings)
	mux.HandleFunc("POST /api/sessions/{id}/stop", s.stopSession)
	mux.HandleFunc("POST /api/stop-recording", s.stopRecording)
	mux.HandleFunc("POST /api/sessions/{id}/segments", s.startSegment)
	mux.HandleFunc("POST /api/sessions/{id}/segments/manual", s.addManualSegment)
	mux.HandleFunc("PATCH /api/sessions/{id}/segments/{segmentID}", s.patchSegment)
	mux.HandleFunc("POST /api/sessions/{id}/segments/{segmentID}/split", s.splitSegment)
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
	mux.HandleFunc("POST /api/open-config-file", s.openConfigFile)
	mux.HandleFunc("GET /api/devices", s.listDevices)
	mux.HandleFunc("POST /api/devices", s.selectDevice)
	mux.HandleFunc("GET /api/log", s.readLog)
	mux.HandleFunc("POST /api/open-log-folder", s.openLogFolder)
	mux.HandleFunc("GET /api/sessions/{id}/events", s.events)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", s.assets()))
	mux.HandleFunc("GET /dock", s.page("dock.html"))
	mux.HandleFunc("GET /log", s.page("log.html"))
	mux.HandleFunc("GET /upload", s.page("upload.html"))
	mux.HandleFunc("GET /", s.page("index.html"))
	return refuseOtherSites(s.settings.Get().Listen, s.localOnlyHeaders(mux))
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
		if r.URL.Path != "/" && r.URL.Path != "/dock" && r.URL.Path != "/log" && r.URL.Path != "/upload" {
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
	writeJSON(w, http.StatusOK, map[string]any{"active": active, "elapsedSeconds": position.Seconds, "framePosition": position.Frames, "capture": captureInfo, "captureError": s.capture.LastError(), "session": session, "presets": s.settings.Get().Presets})
}
