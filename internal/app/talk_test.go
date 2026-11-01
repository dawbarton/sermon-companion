package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func talkFixture(t *testing.T) (http.Handler, *store.Store, string) {
	t.Helper()
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 1, 10, 30, 0, 0, time.Local)
	session, err := sessions.Create("1 November 2026", "Emmanuel Bishopston", started)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status = "stopped"
		s.Export = &store.ExportInfo{Status: "completed", Output: "exports/2026-11-01-Emmanuel-Bishopston.mp3"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	podcast := false
	c.Publish = &config.PublishConfig{Site: "https://example.org", User: "uploader", Podcast: &podcast}
	settings := config.NewSettings("", c)
	handler := NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles).Handler()
	return handler, sessions, session.ID
}

func TestTalkDetailsStartFromTheSessionAndLeaveTheMP3Current(t *testing.T) {
	handler, sessions, id := talkFixture(t)
	path := "/api/sessions/" + id + "/talk"

	patched := requestSession(t, handler, http.MethodPatch, path, `{"title":"  What's   wrong with us? ","speakers":["Jo Bloggs"," jo bloggs ","","Ann Other"]}`)
	talk := patched.Talk
	if talk == nil {
		t.Fatal("no talk was recorded")
	}
	if talk.Title != "What's wrong with us?" || talk.Date != "2026-11-01" || talk.Church != "Emmanuel Bishopston" || talk.Podcast {
		t.Fatalf("talk = %#v", talk)
	}
	if len(talk.Speakers) != 2 || talk.Speakers[0] != "Jo Bloggs" || talk.Speakers[1] != "Ann Other" {
		t.Fatalf("speakers = %q", talk.Speakers)
	}
	if patched.Export == nil || patched.Export.Status != "completed" {
		t.Fatalf("a talk edit made the MP3 out of date: %#v", patched.Export)
	}

	// Confirming a name as new lasts only while it is still used.
	patched = requestSession(t, handler, http.MethodPatch, path, `{"newSpeakers":["Ann Other"],"series":"In the Beginning","newSeries":true}`)
	if len(patched.Talk.NewSpeakers) != 1 || !patched.Talk.NewSeries {
		t.Fatalf("confirmations = %q, %v", patched.Talk.NewSpeakers, patched.Talk.NewSeries)
	}
	patched = requestSession(t, handler, http.MethodPatch, path, `{"speakers":["Jo Bloggs"],"series":"Life Together"}`)
	if len(patched.Talk.NewSpeakers) != 0 || patched.Talk.NewSeries {
		t.Fatalf("confirmations outlived their names: %q, %v", patched.Talk.NewSpeakers, patched.Talk.NewSeries)
	}

	requestError(t, handler, http.MethodPatch, path, `{"date":"01/11/2026"}`, http.StatusBadRequest, "2026-11-01")
	requestError(t, handler, http.MethodPatch, path, `{"videoUrl":"https://vimeo.com/1"}`, http.StatusBadRequest, "YouTube")
	requestError(t, handler, http.MethodPatch, path, `{"image":"http://example.org/a.png"}`, http.StatusBadRequest, "https://")
	requestError(t, handler, http.MethodPatch, path, `{"speakers":["a","b","c","d","e","f","g"]}`, http.StatusBadRequest, "at most")
	accepted := requestSession(t, handler, http.MethodPatch, path, `{"videoUrl":"https://www.youtube.com/watch?v=FqAJchBCktM","image":"16361"}`)
	if accepted.Talk.VideoURL == "" || accepted.Talk.Image != "16361" {
		t.Fatalf("talk = %#v", accepted.Talk)
	}

	events, err := sessions.Events(id)
	if err != nil {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last.Type != "talk.updated" {
		t.Fatalf("last event = %#v", last)
	}

	// Once the talk is on the website it is corrected there.
	if _, err := sessions.Update(id, "test.published", nil, func(s *store.Session) error {
		s.Publish = &store.PublishInfo{Status: "completed", Progress: store.PublishProgress{PostID: 1, Finished: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	requestError(t, handler, http.MethodPatch, path, `{"title":"Late change"}`, http.StatusBadRequest, "already on the website")
}
