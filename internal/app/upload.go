// The upload page's view of the website: filling a talk's details in from the
// preaching programme, the site, and YouTube; checking them as they are typed;
// and keeping a copy of the site's lists so that the page still works when the
// site cannot be reached. Everything here only reads from the site.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/programme"
	"github.com/dawbarton/sermon-companion/internal/publish"
	"github.com/dawbarton/sermon-companion/internal/store"
)

const (
	// catalogueName is the copy of the site's lists, beside the sessions.
	catalogueName = "website-lists.json"
	// catalogueFresh is how long the copy is used before it is read again.
	// Speakers and series are added a few times a term.
	catalogueFresh = time.Hour
	// lookupTimeout bounds each request made while the page is being used,
	// so that a slow site makes the page slow rather than stuck.
	lookupTimeout = 20 * time.Second
	// videosFresh is how long the channel's list of videos is reused.
	videosFresh = 10 * time.Minute
	// talksFresh is how long a check for a talk already on the website is
	// reused. The upload repeats the check against the site regardless.
	talksFresh = 2 * time.Minute
)

// website holds what has been read from the site and YouTube.
type website struct {
	mu        sync.Mutex
	catalogue *publish.Catalogue
	loaded    bool
	lastError string
	images    map[string]imageLookup
	videos    []publish.Video
	videosAt  time.Time
	programme *programme.Programme
	readFrom  string
	modified  time.Time
	talksOn   map[string]talksLookup
}

type talksLookup struct {
	talks []publish.ExistingTalk
	at    time.Time
}

type imageLookup struct {
	image publish.Image
	err   string
	at    time.Time
}

func (s *Server) catalogueFile() string { return filepath.Join(s.store.Root(), catalogueName) }

// client signs in to the site with the configured account.
func (s *Server) client(c config.Config) (*publish.Client, error) {
	if c.Publish == nil {
		return nil, errors.New("uploading to the website is not set up in the settings file")
	}
	password, err := s.settings.Password()
	if err != nil {
		return nil, err
	}
	return publish.NewClient(c.Publish.Site, c.Publish.User, password, s.publishHTTP)
}

// lists returns the copy of the site's lists, reading the site again when the
// copy is older than catalogueFresh or when force is set. If the site cannot
// be read, the old copy is returned with the reason.
func (s *Server) lists(ctx context.Context, force bool) (*publish.Catalogue, string) {
	c := s.settings.Get()
	s.web.mu.Lock()
	defer s.web.mu.Unlock()
	if !s.web.loaded {
		s.web.loaded = true
		catalogue, err := publish.LoadCatalogue(s.catalogueFile())
		if err != nil {
			log.Printf("website lists: %v", err)
		}
		if catalogue != nil && strings.TrimSuffix(catalogue.Site, "/") == strings.TrimSuffix(c.Publish.Site, "/") {
			s.web.catalogue = catalogue
		}
	}
	current := s.web.catalogue
	if !force && current != nil && time.Since(current.Fetched) < catalogueFresh {
		return current, ""
	}
	client, err := s.client(c)
	if err == nil {
		ctx, cancel := context.WithTimeout(ctx, 3*lookupTimeout)
		defer cancel()
		var fresh *publish.Catalogue
		if fresh, err = publish.FetchCatalogue(ctx, client, c.Publish.Type()); err == nil {
			if saveErr := fresh.Save(s.catalogueFile()); saveErr != nil {
				log.Printf("website lists: keeping a copy: %v", saveErr)
			}
			s.web.catalogue, s.web.lastError = fresh, ""
			return fresh, ""
		}
	}
	message := fmt.Sprintf("The website's lists could not be read: %v", err)
	if current != nil {
		message += fmt.Sprintf(" The copy from %s is being used.", current.Fetched.In(time.Local).Format("2 January at 15:04"))
	}
	if message != s.web.lastError {
		log.Print(message)
	}
	s.web.lastError = message
	return current, message
}

// readProgramme reads the preaching programme, again only if the file has
// changed since it was last read.
func (s *Server) readProgramme(p config.PublishConfig) (*programme.Programme, error) {
	if strings.TrimSpace(p.Programme) == "" {
		return nil, nil
	}
	info, err := os.Stat(p.Programme)
	if err != nil {
		return nil, fmt.Errorf("the programme %s cannot be found: %w", p.Programme, err)
	}
	s.web.mu.Lock()
	defer s.web.mu.Unlock()
	key := p.Programme + "\x00" + p.ProgrammeSheet
	if s.web.programme != nil && s.web.readFrom == key && s.web.modified.Equal(info.ModTime()) {
		return s.web.programme, nil
	}
	read, err := programme.Read(p.Programme, p.ProgrammeSheet)
	if err != nil {
		return nil, fmt.Errorf("the programme %s cannot be read: %w", filepath.Base(p.Programme), err)
	}
	s.web.programme, s.web.readFrom, s.web.modified = read, key, info.ModTime()
	return read, nil
}

// channelVideos lists the YouTube channel's latest videos.
func (s *Server) channelVideos(ctx context.Context, channel string) ([]publish.Video, error) {
	s.web.mu.Lock()
	if s.web.videos != nil && time.Since(s.web.videosAt) < videosFresh {
		videos := s.web.videos
		s.web.mu.Unlock()
		return videos, nil
	}
	s.web.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	videos, err := publish.ChannelVideos(ctx, s.publishHTTP, channel)
	if err != nil {
		return nil, err
	}
	s.web.mu.Lock()
	s.web.videos, s.web.videosAt = videos, time.Now()
	s.web.mu.Unlock()
	return videos, nil
}

// prefillTalk fills a session's talk in for the first time, or again when
// replace is set, from the programme, the website, and YouTube.
func (s *Server) prefillTalk(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Replace bool `json:"replace"`
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
	c := s.settings.Get()
	if c.Publish == nil {
		writeError(w, http.StatusConflict, errors.New("uploading to the website is not set up in the settings file"))
		return
	}
	id := r.PathValue("id")
	session, err := s.store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	if session.Talk != nil && !request.Replace {
		s.writeSession(w, http.StatusOK, session)
		return
	}
	// The lookups are made before the session is changed, so that a slow
	// site does not hold the store.
	talk := s.suggestTalk(r.Context(), c, session)
	payload := map[string]any{"talk": talk}
	updated, err := s.store.UpdateAtRevision(id, &expected, "talk.prefilled", payload, func(session *store.Session) error {
		if err := rejectTalkMutation(session); err != nil {
			return err
		}
		if session.Talk != nil && !request.Replace {
			return nil
		}
		if session.Talk != nil {
			payload["before"] = *session.Talk
		}
		session.Talk = talk
		return nil
	})
	if err != nil {
		writeSessionMutationError(w, err)
		return
	}
	s.writeSession(w, http.StatusOK, updated)
}

// suggestTalk is the talk as far as the programme, the site, and YouTube can
// say. Source records where each part came from, for the operator to read.
func (s *Server) suggestTalk(ctx context.Context, c config.Config, session *store.Session) *store.Talk {
	talk := s.newTalk(session)
	var notes []string
	catalogue, _ := s.lists(ctx, false)

	if c.Publish.Programme != "" {
		read, err := s.readProgramme(*c.Publish)
		switch {
		case err != nil:
			notes = append(notes, publish.Capitalise(err.Error())+".")
		default:
			match := read.Find(talk.Church, session.StartedAt)
			notes = append(notes, match.Note)
			if row := match.Row; row != nil {
				talk.Title, talk.Series, talk.Passage, talk.Image = row.Title, row.Series, row.Passage, row.SeriesImage
				if row.Podcast != nil {
					talk.Podcast = *row.Podcast
				}
				talk.Speakers = append([]string{}, row.Speakers...)
			}
		}
	}
	if catalogue != nil {
		// Names are given as the site spells them, so that "jim walford" in
		// the programme is filed under the site's "Jim Walford".
		for i, name := range talk.Speakers {
			if match := publish.MatchTerm(catalogue.Speakers, name); match.Exact != nil {
				talk.Speakers[i] = match.Exact.Name
			}
		}
		if talk.Series != "" {
			if match := publish.MatchTerm(catalogue.Series, talk.Series); match.Exact != nil {
				talk.Series = match.Exact.Name
			}
		} else if latest := catalogue.LatestSeries(talk.Church); latest != "" {
			talk.Series = latest
			notes = append(notes, "The series is the one the church's latest talk on the website is in.")
		}
	}
	if channel := strings.TrimSpace(c.Publish.YouTubeChannel); channel != "" {
		videos, err := s.channelVideos(ctx, channel)
		switch {
		case err != nil:
			notes = append(notes, publish.Capitalise(err.Error())+".")
		default:
			if video, ok := publish.ChooseVideo(videos, session.StartedAt); ok {
				talk.VideoURL = video.URL()
				notes = append(notes, fmt.Sprintf("The video is %q from YouTube.", video.Title))
			} else {
				notes = append(notes, "YouTube has no video for this service yet.")
			}
		}
	}
	talk.Source = strings.Join(notes, " ")
	return talk
}

// talkCheck is everything the upload page shows beside the details.
type talkCheck struct {
	Site       string                 `json:"site"`
	Status     string                 `json:"status"`
	Type       string                 `json:"type"`
	Fetched    *time.Time             `json:"listsFetched,omitempty"`
	ListsError string                 `json:"listsError,omitempty"`
	Churches   []string               `json:"churches"`
	Speakers   []string               `json:"speakers"`
	Series     []string               `json:"series"`
	Review     *publish.Review        `json:"review,omitempty"`
	Image      *checkedImage          `json:"image,omitempty"`
	Video      string                 `json:"video,omitempty"`
	Duplicates []publish.ExistingTalk `json:"duplicates"`
	Notes      []string               `json:"notes"`
	Programme  []string               `json:"programmeProblems,omitempty"`
}

type checkedImage struct {
	publish.Image
	FromSeries bool   `json:"fromSeries"`
	Error      string `json:"error,omitempty"`
}

// checkTalk reports what the website will make of the session's talk.
func (s *Server) checkTalk(w http.ResponseWriter, r *http.Request) {
	c := s.settings.Get()
	if c.Publish == nil {
		writeError(w, http.StatusConflict, errors.New("uploading to the website is not set up in the settings file"))
		return
	}
	session, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	check := talkCheck{
		Site: c.Publish.Site, Status: c.Publish.PublishStatus(), Type: c.Publish.Type(),
		Churches: []string{}, Speakers: []string{}, Series: []string{}, Duplicates: []publish.ExistingTalk{}, Notes: []string{},
	}
	if c.Publish.Programme != "" {
		if read, err := s.readProgramme(*c.Publish); err == nil {
			check.Programme = read.Problems
		}
	}
	catalogue, listsError := s.lists(r.Context(), r.URL.Query().Get("refresh") == "1")
	check.ListsError = listsError
	talk := session.Talk
	if talk == nil {
		talk = s.newTalk(session)
	}
	if catalogue != nil {
		fetched := catalogue.Fetched
		check.Fetched = &fetched
		for _, church := range catalogue.Churches {
			check.Churches = append(check.Churches, church.Name)
		}
		check.Speakers = catalogue.SpeakerNames(talk.Church)
		check.Series = catalogue.SeriesNames(talk.Church)
		review := publish.ReviewTalk(publish.Lists{Churches: catalogue.Churches, Types: catalogue.Types,
			Speakers: catalogue.Speakers, Series: catalogue.Series, Books: catalogue.Books}, *talk, c.Publish.Type())
		check.Review = &review
		if session.Talk == nil {
			review.Problems = append(review.Problems, "The talk's details have not been filled in yet.")
		}
	}
	if session.Publish == nil || !session.Publish.Progress.Finished {
		// What is already on the website no longer matters once this talk is.
		s.checkOnline(r.Context(), c, catalogue, talk, &check)
	}
	if session.Export == nil || session.Export.Status != "completed" {
		check.Notes = append(check.Notes, "The talk can be published once the MP3 has been created.")
	}
	writeJSON(w, http.StatusOK, check)
}

// checkOnline adds what only the site and YouTube can say: the image, the
// video's title, and any talk already filed for the service.
func (s *Server) checkOnline(ctx context.Context, c config.Config, catalogue *publish.Catalogue, talk *store.Talk, check *talkCheck) {
	client, err := s.client(c)
	if err != nil {
		check.Notes = append(check.Notes, publish.Capitalise(err.Error())+".")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	switch reference := strings.TrimSpace(talk.Image); {
	case reference != "":
		image, err := s.findImage(ctx, client, reference)
		check.Image = &checkedImage{Image: image}
		if err != nil {
			check.Image.Error = publish.Capitalise(err.Error()) + "."
		}
	case catalogue != nil && talk.Series != "":
		if match := publish.MatchTerm(catalogue.Series, talk.Series); match.Exact != nil {
			id := catalogue.SeriesImage(match.Exact.ID)
			if id == 0 {
				if kind := publish.MatchTerm(catalogue.Types, c.Publish.Type()).Exact; kind != nil {
					id, _ = client.SeriesImage(ctx, match.Exact.ID, kind.ID)
				}
			}
			if id != 0 {
				image, err := s.findImage(ctx, client, fmt.Sprint(id))
				check.Image = &checkedImage{Image: image, FromSeries: true}
				if err != nil {
					check.Image.Error = publish.Capitalise(err.Error()) + "."
				}
			}
		}
	}

	if talk.VideoURL != "" && c.Publish.YouTubeChannel != "" {
		if videos, err := s.channelVideos(ctx, c.Publish.YouTubeChannel); err == nil {
			if address, err := url.Parse(talk.VideoURL); err == nil {
				id := address.Query().Get("v")
				if id == "" {
					id = strings.Trim(address.Path[strings.LastIndex(address.Path, "/")+1:], "/")
				}
				for _, video := range videos {
					if video.ID == id {
						check.Video = video.Title
					}
				}
			}
		}
	}

	if catalogue != nil {
		church := catalogue.Church(talk.Church)
		kind := publish.MatchTerm(catalogue.Types, c.Publish.Type()).Exact
		if church != nil && kind != nil && talk.Date != "" {
			found, err := s.talksOn(ctx, client, church.ID, kind.ID, talk.Date)
			if err != nil {
				check.Notes = append(check.Notes, "Whether the website already has a talk for this service could not be checked: "+err.Error()+".")
			} else if found != nil {
				check.Duplicates = found
			}
		}
	}
}

// talksOn finds talks already filed for a church and day, remembering the
// answer for a few minutes, since the page asks again after every change.
func (s *Server) talksOn(ctx context.Context, client *publish.Client, churchID, typeID int, day string) ([]publish.ExistingTalk, error) {
	key := fmt.Sprintf("%d/%d/%s", churchID, typeID, day)
	s.web.mu.Lock()
	if found, ok := s.web.talksOn[key]; ok && time.Since(found.at) < talksFresh {
		s.web.mu.Unlock()
		return found.talks, nil
	}
	s.web.mu.Unlock()
	talks, err := client.TalksOn(ctx, churchID, typeID, day)
	if err != nil {
		return nil, err
	}
	s.web.mu.Lock()
	if s.web.talksOn == nil {
		s.web.talksOn = map[string]talksLookup{}
	}
	s.web.talksOn[key] = talksLookup{talks: talks, at: time.Now()}
	s.web.mu.Unlock()
	return talks, nil
}

// findImage looks an image up, remembering the answer for a few minutes so
// that saving each field does not ask the site again.
func (s *Server) findImage(ctx context.Context, client *publish.Client, reference string) (publish.Image, error) {
	s.web.mu.Lock()
	if s.web.images == nil {
		s.web.images = map[string]imageLookup{}
	}
	if found, ok := s.web.images[reference]; ok && time.Since(found.at) < videosFresh {
		s.web.mu.Unlock()
		if found.err != "" {
			return found.image, errors.New(found.err)
		}
		return found.image, nil
	}
	s.web.mu.Unlock()
	image, err := client.FindImage(ctx, reference)
	lookup := imageLookup{image: image, at: time.Now()}
	if err != nil {
		if publish.IsKind(err, publish.KindNetwork) || publish.IsKind(err, publish.KindTimeout) {
			return image, err
		}
		lookup.err = err.Error()
	}
	s.web.mu.Lock()
	s.web.images[reference] = lookup
	s.web.mu.Unlock()
	return image, err
}

// publishSettings tells the review page whether uploading is set up.
func (s *Server) publishSettings(w http.ResponseWriter, _ *http.Request) {
	c := s.settings.Get()
	if c.Publish == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "site": c.Publish.Site, "status": c.Publish.PublishStatus()})
}
