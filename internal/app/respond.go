// Reading requests and writing answers: strict JSON decoding, the revision
// precondition, JSON and error replies, and serving a recorded file.

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dawbarton/sermon-companion/internal/store"
)

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

func decodeJSON(r *http.Request, destination interface{}) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return fmt.Errorf("invalid request: trailing content: %w", err)
	}
	return nil
}

func requireRevision(r *http.Request) (int64, error) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	if value == "" {
		return 0, errors.New("If-Match session revision is required")
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, "W/"))
	value = strings.Trim(value, `"`)
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 0 {
		return 0, errors.New("If-Match must contain a valid session revision")
	}
	return revision, nil
}

func writeSessionMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrRevisionConflict) {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeError(w, http.StatusBadRequest, err)
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
