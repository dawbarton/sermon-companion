// Making the MP3: starting the background export, serving the finished file,
// and opening its folder, with the rules for when an edit makes it stale.

package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/dawbarton/sermon-companion/internal/store"
)

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithCancel(context.Background())
	s.jobsMu.Lock()
	if s.closing {
		s.jobsMu.Unlock()
		cancel()
		writeError(w, http.StatusServiceUnavailable, errors.New("Sermon Companion is closing"))
		return
	}
	if _, exists := s.jobs[id]; exists {
		s.jobsMu.Unlock()
		cancel()
		writeError(w, http.StatusConflict, errors.New("an export is already running"))
		return
	}
	if _, uploading := s.jobs[publishJob(id)]; uploading {
		// The upload is reading the MP3 that a new export would replace.
		s.jobsMu.Unlock()
		cancel()
		writeError(w, http.StatusConflict, errors.New("wait for the upload to finish before creating the MP3 again"))
		return
	}
	s.jobs[id] = cancel
	s.jobsWG.Add(1)
	s.jobsMu.Unlock()
	job, err := s.master.Prepare(id)
	if err != nil {
		s.finishExportJob(id)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	go func() {
		defer s.finishExportJob(id)
		if err := s.master.Run(ctx, job); err != nil {
			log.Printf("export %s: %v", id, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
}

func (s *Server) finishExportJob(id string) {
	s.jobsMu.Lock()
	if cancel, exists := s.jobs[id]; exists {
		cancel()
		delete(s.jobs, id)
	}
	s.jobsMu.Unlock()
	s.jobsWG.Done()
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

func markExportStale(session *store.Session) {
	if session.Export != nil && session.Export.Status == "completed" {
		session.Export.Status = "stale"
		session.Export.Error = "Service details or segments changed after this MP3 was created."
	}
}

func rejectExportMutation(session *store.Session) error {
	if session.Export != nil && session.Export.Status == "running" {
		return errors.New("wait for the MP3 to finish before changing this service")
	}
	return nil
}
