// Segments and markers: marking them live from the dock, and adding,
// adjusting, splitting, removing, and restoring them afterwards. Audio frames
// are canonical; the seconds the browser sends and shows are converted at this
// boundary and regenerated from the frames after every change.

package app

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/store"
)

var errRecordingNotAdvanced = errors.New("wait for the recording to advance before starting the next segment")

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
		if err := closeOpenSegmentsForNext(session, position, now); err != nil {
			return err
		}
		session.Segments = append(session.Segments, segment)
		markExportStale(session)
		return nil
	})
	if err != nil {
		if errors.Is(err, errRecordingNotAdvanced) {
			writeError(w, http.StatusConflict, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
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
	if !finite(request.Start) || !finite(request.End) || request.Start < 0 || request.End <= request.Start {
		writeError(w, http.StatusBadRequest, errors.New("segment times must satisfy 0 ≤ start < end"))
		return
	}
	expected, err := requireRevision(r)
	if err != nil {
		writeError(w, http.StatusPreconditionRequired, err)
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
	// The request is copied: snapping below writes through the end-frame
	// pointer, and the journal must keep what was asked for beside what was
	// committed.
	payload := map[string]any{"requested": cloneSegmentValue(segment)}
	session, err := s.store.UpdateAtRevision(id, &expected, "segment.added_manually", payload, func(session *store.Session) error {
		if session.Recording() {
			return errors.New("use the OBS dock to mark segments while recording")
		}
		if err := rejectExportMutation(session); err != nil {
			return err
		}
		session.Segments = append(session.Segments, segment)
		candidate := findSegment(session, segment.ID)
		store.SnapSegmentBoundaries(session.Segments, segment.ID, boundaryToleranceFrames(rate))
		snapToRecordingEnd(session, candidate, rate)
		syncSegmentSeconds(session, s.settings.Get().Capture.SampleRate)
		if recordingFrames(session, rate) > 0 && *candidate.EndFrame > recordingFrames(session, rate) {
			return fmt.Errorf("segment ends beyond the recording (%.1f seconds)", session.Duration)
		}
		if *candidate.EndFrame <= candidate.StartFrame {
			return errors.New("segment times must satisfy 0 ≤ start < end")
		}
		if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
			return err
		}
		payload["after"] = cloneSegmentValue(*candidate)
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeSessionMutationError(w, err)
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
	expected, err := requireRevision(r)
	if err != nil {
		writeError(w, http.StatusPreconditionRequired, err)
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
	payload := map[string]any{"requested": request}
	session, err := s.store.UpdateAtRevision(id, &expected, "segment.adjusted", payload, func(session *store.Session) error {
		if session.Recording() {
			return errors.New("segments cannot be adjusted while recording")
		}
		if err := rejectExportMutation(session); err != nil {
			return err
		}
		segment := findSegment(session, segmentID)
		if segment == nil || segment.Archived {
			return errors.New("segment not found")
		}
		previous := cloneSegmentValue(*segment)
		payload["before"] = previous
		if request.Kind != nil && strings.TrimSpace(*request.Kind) != "" {
			segment.Kind = strings.TrimSpace(*request.Kind)
		}
		if request.Label != nil && strings.TrimSpace(*request.Label) != "" {
			segment.Label = strings.TrimSpace(*request.Label)
		}
		if request.Start != nil {
			if !finite(*request.Start) {
				return errors.New("segment times must be finite")
			}
			segment.Start = *request.Start
			segment.StartFrame = secondsToFrame(*request.Start, sessionSampleRate(session, s.settings.Get().Capture.SampleRate))
		}
		if request.End != nil {
			if !finite(*request.End) {
				return errors.New("segment times must be finite")
			}
			segment.End = floatPointer(*request.End)
			segment.EndFrame = uint64Pointer(secondsToFrame(*request.End, sessionSampleRate(session, s.settings.Get().Capture.SampleRate)))
		}
		if request.Include != nil {
			segment.Include = *request.Include
		}
		if segment.Start < 0 || segment.End == nil || segment.EndFrame == nil || *segment.EndFrame <= segment.StartFrame {
			return errors.New("segment times must satisfy 0 ≤ start < end")
		}
		rate := sessionSampleRate(session, s.settings.Get().Capture.SampleRate)
		store.SnapSegmentBoundaries(session.Segments, segment.ID, boundaryToleranceFrames(rate))
		snapToRecordingEnd(session, segment, rate)
		syncSegmentSeconds(session, s.settings.Get().Capture.SampleRate)
		if recordingFrames(session, rate) > 0 && *segment.EndFrame > recordingFrames(session, rate) {
			return fmt.Errorf("segment ends beyond the recording (%.1f seconds)", session.Duration)
		}
		if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
			return err
		}
		// A click on a segment in the waveform is a drag that went nowhere,
		// and a typed time can round back to the stored one. Neither changes
		// the MP3, so neither may make it stale.
		if sameSegmentContent(previous, *segment) {
			payload["after"] = cloneSegmentValue(*segment)
			return nil
		}
		segment.UpdatedAt = time.Now().UTC()
		payload["after"] = cloneSegmentValue(*segment)
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeSessionMutationError(w, err)
		return
	}
	s.writeSession(w, http.StatusOK, session)
}

func (s *Server) splitSegment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		At *float64 `json:"atSeconds"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.At == nil {
		writeError(w, http.StatusBadRequest, errors.New("atSeconds is required"))
		return
	}
	if !finite(*request.At) || *request.At < 0 {
		writeError(w, http.StatusBadRequest, errors.New("split time must be a finite, non-negative value"))
		return
	}
	expected, err := requireRevision(r)
	if err != nil {
		writeError(w, http.StatusPreconditionRequired, err)
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
	payload := map[string]any{"requestedAtSeconds": *request.At}
	session, err := s.store.UpdateAtRevision(id, &expected, "segment.split", payload, func(session *store.Session) error {
		if session.Recording() {
			return errors.New("segments cannot be split while recording")
		}
		if err := rejectExportMutation(session); err != nil {
			return err
		}
		segment := findSegment(session, segmentID)
		if segment == nil || segment.Archived {
			return errors.New("segment not found")
		}
		if segment.EndFrame == nil || *segment.EndFrame <= segment.StartFrame {
			return errors.New("only a complete segment can be split")
		}
		rate := sessionSampleRate(session, s.settings.Get().Capture.SampleRate)
		splitFrame := secondsToFrame(*request.At, rate)
		endFrame := *segment.EndFrame
		minimumFrames := secondsToFrame(0.1, rate)
		if splitFrame <= segment.StartFrame || splitFrame >= endFrame || splitFrame-segment.StartFrame < minimumFrames || endFrame-splitFrame < minimumFrames {
			return errors.New("split must leave at least 0.1 seconds on each side")
		}
		now := time.Now().UTC()
		payload["before"] = cloneSegmentValue(*segment)
		right := cloneSegmentValue(*segment)
		right.ID = store.NewObjectID("seg")
		right.StartFrame = splitFrame
		right.CreatedAt, right.UpdatedAt = now, now
		segment.EndFrame = uint64Pointer(splitFrame)
		segment.UpdatedAt = now
		session.Segments = append(session.Segments, right)
		syncSegmentSeconds(session, rate)
		if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
			return err
		}
		left, storedRight := findSegment(session, segmentID), findSegment(session, right.ID)
		if left == nil || storedRight == nil {
			return errors.New("split segments could not be recovered")
		}
		payload["splitFrame"] = splitFrame
		payload["left"] = cloneSegmentValue(*left)
		payload["right"] = cloneSegmentValue(*storedRight)
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeSessionMutationError(w, err)
		return
	}
	s.writeSession(w, http.StatusOK, session)
}

func (s *Server) archiveSegment(w http.ResponseWriter, r *http.Request) {
	expected, err := requireRevision(r)
	if err != nil {
		writeError(w, http.StatusPreconditionRequired, err)
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
	payload := map[string]any{}
	session, err := s.store.UpdateAtRevision(id, &expected, "segment.archived", payload, func(session *store.Session) error {
		if session.Recording() {
			return errors.New("segments cannot be removed while recording")
		}
		if err := rejectExportMutation(session); err != nil {
			return err
		}
		segment := findSegment(session, segmentID)
		if segment == nil || segment.Archived {
			return errors.New("segment not found")
		}
		payload["before"] = cloneSegmentValue(*segment)
		segment.Archived, segment.UpdatedAt = true, time.Now().UTC()
		payload["after"] = cloneSegmentValue(*segment)
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeSessionMutationError(w, err)
		return
	}
	s.writeSession(w, http.StatusOK, session)
}

func (s *Server) restoreSegment(w http.ResponseWriter, r *http.Request) {
	expected, err := requireRevision(r)
	if err != nil {
		writeError(w, http.StatusPreconditionRequired, err)
		return
	}
	id, segmentID := r.PathValue("id"), r.PathValue("segmentID")
	payload := map[string]any{"segmentId": segmentID}
	session, err := s.store.UpdateAtRevision(id, &expected, "segment.restored", payload, func(session *store.Session) error {
		if session.Recording() {
			return errors.New("segments cannot be restored while recording")
		}
		if err := rejectExportMutation(session); err != nil {
			return err
		}
		segment := findSegment(session, segmentID)
		if segment == nil || !segment.Archived {
			return errors.New("removed segment not found")
		}
		payload["before"] = cloneSegmentValue(*segment)
		segment.Archived, segment.UpdatedAt = false, time.Now().UTC()
		rate := sessionSampleRate(session, s.settings.Get().Capture.SampleRate)
		store.SnapSegmentBoundaries(session.Segments, segment.ID, boundaryToleranceFrames(rate))
		syncSegmentSeconds(session, rate)
		if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
			return err
		}
		payload["after"] = cloneSegmentValue(*segment)
		markExportStale(session)
		return nil
	})
	if err != nil {
		writeSessionMutationError(w, err)
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
	maximumFrame := uint64(0)
	var expected *int64
	if at != nil && (!finite(*at) || *at < 0) {
		writeError(w, http.StatusBadRequest, errors.New("marker time must be a finite, non-negative value"))
		return
	}
	if at == nil {
		if !active || id != r.PathValue("id") {
			writeError(w, http.StatusBadRequest, errors.New("atSeconds is required for a finished session"))
			return
		}
		at = &position.Seconds
		atFrame = position.Frames
	} else {
		revision, err := requireRevision(r)
		if err != nil {
			writeError(w, http.StatusPreconditionRequired, err)
			return
		}
		expected = &revision
		session, err := s.store.Get(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, errors.New("session not found"))
			return
		}
		rate := sessionSampleRate(session, s.settings.Get().Capture.SampleRate)
		atFrame = secondsToFrame(*at, rate)
		*at = float64(atFrame) / float64(rate)
		maximumFrame = recordingFrames(session, rate)
	}
	if maximumFrame > 0 && atFrame > maximumFrame {
		writeError(w, http.StatusBadRequest, errors.New("marker time must fall within the recording"))
		return
	}
	if strings.TrimSpace(request.Label) == "" {
		request.Label = "Marker"
	}
	if strings.TrimSpace(request.Kind) == "" {
		request.Kind = kindFromLabel(request.Label)
	}
	marker := store.Marker{ID: store.NewObjectID("mark"), Kind: strings.TrimSpace(request.Kind), Label: strings.TrimSpace(request.Label), AtFrame: atFrame, At: *at, CreatedAt: time.Now().UTC()}
	session, err := s.store.UpdateAtRevision(r.PathValue("id"), expected, "marker.added", marker, func(session *store.Session) error {
		if err := rejectExportMutation(session); err != nil {
			return err
		}
		session.Markers = append(session.Markers, marker)
		return nil
	})
	if err != nil {
		writeSessionMutationError(w, err)
		return
	}
	s.writeSession(w, http.StatusCreated, session)
}

func closeOpenSegmentsForNext(session *store.Session, position capture.Position, now time.Time) error {
	for index := range session.Segments {
		open := &session.Segments[index]
		if !open.Archived && open.EndFrame == nil && position.Frames <= open.StartFrame {
			return errRecordingNotAdvanced
		}
	}
	store.CloseOpenSegments(session, position.Frames, position.Seconds, now)
	return nil
}

func findSegment(session *store.Session, id string) *store.Segment {
	for i := range session.Segments {
		if session.Segments[i].ID == id {
			return &session.Segments[i]
		}
	}
	return nil
}

func cloneSegmentValue(segment store.Segment) store.Segment {
	if segment.EndFrame != nil {
		endFrame := *segment.EndFrame
		segment.EndFrame = &endFrame
	}
	if segment.End != nil {
		end := *segment.End
		segment.End = &end
	}
	return segment
}

// sameSegmentContent reports whether two versions of a segment would put the
// same audio, under the same label, into the MP3. The seconds fields are
// derived from the frames and are not compared.
func sameSegmentContent(a, b store.Segment) bool {
	sameEnd := (a.EndFrame == nil) == (b.EndFrame == nil) && (a.EndFrame == nil || *a.EndFrame == *b.EndFrame)
	return a.Kind == b.Kind && a.Label == b.Label && a.Include == b.Include && a.Archived == b.Archived &&
		a.StartFrame == b.StartFrame && sameEnd
}

func floatPointer(v float64) *float64 { return &v }

func uint64Pointer(v uint64) *uint64 { return &v }

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

func syncSegmentSeconds(session *store.Session, fallbackRate int) {
	rate := sessionSampleRate(session, fallbackRate)
	for index := range session.Segments {
		segment := &session.Segments[index]
		segment.Start = float64(segment.StartFrame) / float64(rate)
		if segment.EndFrame == nil {
			segment.End = nil
		} else {
			segment.End = floatPointer(float64(*segment.EndFrame) / float64(rate))
		}
	}
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func boundaryToleranceFrames(rate int) uint64 { return secondsToFrame(0.051, rate) }

func recordingFrames(session *store.Session, rate int) uint64 {
	if session.Capture.TotalFrames > 0 {
		return session.Capture.TotalFrames
	}
	return secondsToFrame(session.Duration, rate)
}

func snapToRecordingEnd(session *store.Session, segment *store.Segment, rate int) {
	end := recordingFrames(session, rate)
	if segment.EndFrame != nil && end > 0 && frameDistance(*segment.EndFrame, end) <= boundaryToleranceFrames(rate) {
		*segment.EndFrame = end
	}
}

func frameDistance(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
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
