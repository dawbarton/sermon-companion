// A service's life as the API sees it: starting and stopping the recording,
// reading and changing the service's own details, deleting it, and serving its
// audio, waveform, and journal.

package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

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
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, session.Revision))
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
	expected, err := requireRevision(r)
	if err != nil {
		writeError(w, http.StatusPreconditionRequired, err)
		return
	}
	id := r.PathValue("id")
	_, err = s.store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	payload := map[string]any{"requested": request}
	session, err := s.store.UpdateAtRevision(id, &expected, "session.metadata_updated", payload, func(session *store.Session) error {
		if session.Export != nil && session.Export.Status == "running" {
			return errors.New("service details cannot be changed while an MP3 is being created")
		}
		payload["before"] = map[string]any{"title": session.Title, "church": session.Church, "gapSeconds": session.GapSeconds}
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
		payload["after"] = map[string]any{"title": session.Title, "church": session.Church, "gapSeconds": session.GapSeconds}
		return nil
	})
	if err != nil {
		writeSessionMutationError(w, err)
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
	expected, err := requireRevision(r)
	if err != nil {
		writeError(w, http.StatusPreconditionRequired, err)
		return
	}
	activeID, _, _, active := s.capture.Active()
	if (active && activeID == id) || session.Recording() {
		writeError(w, http.StatusConflict, errors.New("stop the recording before deleting this service"))
		return
	}
	exporting, publishing := s.busy(id)
	if exporting {
		writeError(w, http.StatusConflict, errors.New("wait for the MP3 to finish before deleting this service"))
		return
	}
	if publishing {
		writeError(w, http.StatusConflict, errors.New("wait for the upload to finish before deleting this service"))
		return
	}
	if err := s.store.DeleteAtRevision(id, &expected); err != nil {
		if errors.Is(err, store.ErrRevisionConflict) {
			writeError(w, http.StatusConflict, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	log.Printf("deleted service %s (%s) at the operator's request", id, session.Title)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	notRecording := errors.New("this session is not recording")
	id, _, _, active := s.capture.Active()
	if !active || id != r.PathValue("id") {
		writeError(w, http.StatusConflict, notRecording)
		return
	}
	s.stopActive(w, notRecording)
}

// stopRecording stops whatever is being recorded. The OBS script calls it when
// the stream ends, without knowing which service it is; the dock, which does,
// stops a named session instead, so that a stale page cannot stop a newer one.
func (s *Server) stopRecording(w http.ResponseWriter, r *http.Request) {
	if err := decodeJSON(r, &struct{}{}); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// The OBS script recognises this answer as nothing to do.
	notRecording := errors.New("nothing is being recorded")
	if _, _, _, active := s.capture.Active(); !active {
		writeError(w, http.StatusConflict, notRecording)
		return
	}
	s.stopActive(w, notRecording)
}

// stopActive stops the recording. One that ends between the caller's check and
// the stop, as when the dock's Stop and the end of the stream arrive together,
// is answered as though it had already ended, not as a failure.
func (s *Server) stopActive(w http.ResponseWriter, notRecording error) {
	session, err := s.capture.Stop()
	if errors.Is(err, capture.ErrNotRecording) {
		writeError(w, http.StatusConflict, notRecording)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// The review page is opened as soon as the service ends, and its first
	// request would otherwise decode the whole recording while the operator is
	// already trying to listen to it. Build the waveform now, so that nothing is
	// competing with the recording for the disk when a segment is first played.
	s.generateWaveform(session.ID)
	s.writeSession(w, http.StatusOK, session)
}

func (s *Server) generateWaveform(id string) {
	s.jobsMu.Lock()
	if s.closing {
		s.jobsMu.Unlock()
		return
	}
	s.jobsWG.Add(1)
	ctx := s.background
	s.jobsMu.Unlock()
	go func() {
		defer s.jobsWG.Done()
		if _, err := s.waveform.Generate(ctx, id); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("waveform for %s: %v", id, err)
		}
	}()
}

func (s *Server) audio(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// The recording grows under a fixed name, so a partial response must never
	// be reused for the finished file.
	if session.Recording() {
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

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.Events(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}
