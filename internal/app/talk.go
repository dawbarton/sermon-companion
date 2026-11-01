// The talk's details for the church website: the record kept in the session,
// and the route that edits it. These details are not part of the MP3, so
// editing them never makes an export stale, and they may be edited while the
// MP3 is still being made.
package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dawbarton/sermon-companion/internal/store"
)

// Limits on what the website is sent. They are far beyond any real title or
// name and exist to keep a pasted mistake from reaching the site.
const (
	maximumTalkTitle   = 200
	maximumTalkName    = 100
	maximumTalkPassage = 300
	maximumSpeakers    = 6
)

// newTalk is a talk with only what the session itself says: the service's
// date, its church, and the configured podcast choice.
func (s *Server) newTalk(session *store.Session) *store.Talk {
	c := s.settings.Get()
	church := strings.TrimSpace(session.Church)
	if church == "" {
		church = c.Church
	}
	podcast := true
	if c.Publish != nil {
		podcast = c.Publish.PodcastByDefault()
	}
	return &store.Talk{
		Date:     session.StartedAt.In(time.Local).Format("2006-01-02"),
		Church:   church,
		Speakers: []string{},
		Podcast:  podcast,
	}
}

// talkRequest is a change to some of a talk's details; a field left out is
// left as it is.
type talkRequest struct {
	Title       *string   `json:"title"`
	Date        *string   `json:"date"`
	Church      *string   `json:"church"`
	Speakers    *[]string `json:"speakers"`
	NewSpeakers *[]string `json:"newSpeakers"`
	Series      *string   `json:"series"`
	NewSeries   *bool     `json:"newSeries"`
	Passage     *string   `json:"passage"`
	Podcast     *bool     `json:"podcast"`
	VideoURL    *string   `json:"videoUrl"`
	Image       *string   `json:"image"`
}

func (s *Server) patchTalk(w http.ResponseWriter, r *http.Request) {
	var request talkRequest
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
	if _, err := s.store.Get(id); err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	payload := map[string]any{"requested": request}
	session, err := s.store.UpdateAtRevision(id, &expected, "talk.updated", payload, func(session *store.Session) error {
		if err := rejectTalkMutation(session); err != nil {
			return err
		}
		talk := session.Talk
		if talk == nil {
			talk = s.newTalk(session)
		} else {
			payload["before"] = *talk
			copied := *talk
			talk = &copied
		}
		if err := applyTalkRequest(talk, request); err != nil {
			return err
		}
		session.Talk = talk
		payload["after"] = *talk
		return nil
	})
	if err != nil {
		writeSessionMutationError(w, err)
		return
	}
	s.writeSession(w, http.StatusOK, session)
}

// rejectTalkMutation refuses a change while the talk is being uploaded, and
// once it is on the website, where the details are now corrected by hand.
func rejectTalkMutation(session *store.Session) error {
	if session.Publish == nil {
		return nil
	}
	if session.Publish.Status == "running" {
		return errors.New("wait for the upload to finish before changing the talk's details")
	}
	if session.Publish.Progress.Finished {
		return errors.New("this talk is already on the website; correct its details there")
	}
	return nil
}

func applyTalkRequest(talk *store.Talk, request talkRequest) error {
	text := func(value *string, field string, limit int, target *string) error {
		if value == nil {
			return nil
		}
		trimmed := strings.Join(strings.Fields(*value), " ")
		if utf8.RuneCountInString(trimmed) > limit {
			return fmt.Errorf("the %s is longer than %d characters", field, limit)
		}
		*target = trimmed
		return nil
	}
	if err := text(request.Title, "title", maximumTalkTitle, &talk.Title); err != nil {
		return err
	}
	if err := text(request.Church, "church", maximumTalkName, &talk.Church); err != nil {
		return err
	}
	previousSeries := talk.Series
	if err := text(request.Series, "series", maximumTalkName, &talk.Series); err != nil {
		return err
	}
	if err := text(request.Passage, "Bible passage", maximumTalkPassage, &talk.Passage); err != nil {
		return err
	}
	if request.Date != nil {
		date := strings.TrimSpace(*request.Date)
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return errors.New("the date must be a day such as 2026-11-01")
		}
		talk.Date = date
	}
	if request.Speakers != nil {
		speakers, err := cleanNames(*request.Speakers)
		if err != nil {
			return err
		}
		talk.Speakers = speakers
	}
	if request.NewSpeakers != nil {
		names, err := cleanNames(*request.NewSpeakers)
		if err != nil {
			return err
		}
		talk.NewSpeakers = names
	}
	// A speaker confirmed as new stays confirmed only while they are a speaker.
	talk.NewSpeakers = keepListed(talk.NewSpeakers, talk.Speakers)
	// Likewise a series confirmed as new: choosing another one withdraws it.
	if !strings.EqualFold(previousSeries, talk.Series) {
		talk.NewSeries = false
	}
	if request.NewSeries != nil {
		talk.NewSeries = *request.NewSeries && talk.Series != ""
	}
	if request.Podcast != nil {
		talk.Podcast = *request.Podcast
	}
	if request.VideoURL != nil {
		video := strings.TrimSpace(*request.VideoURL)
		if video != "" && !youTubeAddress(video) {
			return errors.New("the video must be a YouTube address, such as https://www.youtube.com/watch?v=…")
		}
		talk.VideoURL = video
	}
	if request.Image != nil {
		image := strings.TrimSpace(*request.Image)
		if image != "" && !imageReference(image) {
			return errors.New("the image must be a link to an image on the website, beginning https://, or its ID")
		}
		talk.Image = image
	}
	return nil
}

// cleanNames tidies a list of names, dropping blanks and repeats.
func cleanNames(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.Join(strings.Fields(name), " ")
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		if utf8.RuneCountInString(name) > maximumTalkName {
			return nil, fmt.Errorf("the name %q is longer than %d characters", name, maximumTalkName)
		}
		seen[key] = true
		out = append(out, name)
	}
	if len(out) > maximumSpeakers {
		return nil, fmt.Errorf("a talk can have at most %d speakers", maximumSpeakers)
	}
	return out, nil
}

func keepListed(names, listed []string) []string {
	present := map[string]bool{}
	for _, name := range listed {
		present[strings.ToLower(name)] = true
	}
	var kept []string
	for _, name := range names {
		if present[strings.ToLower(name)] {
			kept = append(kept, name)
		}
	}
	return kept
}

func youTubeAddress(raw string) bool {
	address, err := url.Parse(raw)
	if err != nil || address.Scheme != "https" {
		return false
	}
	switch strings.ToLower(address.Hostname()) {
	case "youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be":
		return true
	}
	return false
}

func imageReference(raw string) bool {
	if id, err := strconv.Atoi(raw); err == nil {
		return id > 0
	}
	address, err := url.Parse(raw)
	return err == nil && address.Scheme == "https" && address.Host != ""
}
