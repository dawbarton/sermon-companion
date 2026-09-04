package app

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/dawbarton/sermon-companion/internal/applog"
)

// readLog serves the messages that used to scroll past in the console window.
func (s *Server) readLog(w http.ResponseWriter, _ *http.Request) {
	if s.log == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "path": "", "lines": []string{}, "version": s.version})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "path": s.log.Path(), "lines": s.log.Tail(applog.TailLines), "version": s.version})
}

// openLogFolder reveals the log file, so that an operator can send it to
// whoever maintains the installation without being told where to look.
func (s *Server) openLogFolder(w http.ResponseWriter, _ *http.Request) {
	if s.log == nil || s.log.Path() == "" {
		writeError(w, http.StatusNotFound, errors.New("no log file is being written"))
		return
	}
	if err := s.openFolder(filepath.Dir(s.log.Path())); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("open log folder: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "opened"})
}
