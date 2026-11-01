package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dawbarton/sermon-companion/internal/config"
)

// withProgramme gives the fixture a programme and a YouTube channel.
func (f *publishFixture) withProgramme(content string) {
	f.t.Helper()
	filename := filepath.Join(f.root, "Programme.csv")
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.server.settings.Update(func(c *config.Config) error {
		c.Publish.Programme = filename
		c.Publish.YouTubeChannel = "UCReU3dVxdLKPuSs59dBgG6Q"
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *publishFixture) check(query string) talkCheck {
	f.t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/sessions/"+f.id+"/talk/check"+query, nil)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		f.t.Fatalf("check: %d %s", response.Code, response.Body.String())
	}
	var result talkCheck
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		f.t.Fatal(err)
	}
	return result
}

func TestPrefillTakesTheProgrammeTheSiteAndYouTube(t *testing.T) {
	f := newPublishFixture(t)
	f.withProgramme("Date,Service,Title,Speakers,Series,Passage\r\n" +
		"01/11/2026,10:30,What's wrong with us?,jim walford,,Romans 3:9-20\r\n" +
		"01/11/2026,18:30,Evening,Ann Other,,\r\n" +
		"08/11/2026,10:30,Next week,James Maurice,,\r\n")
	path := "/api/sessions/" + f.id + "/talk/prefill"

	session := requestSession(t, f.handler, http.MethodPost, path, `{}`)
	talk := session.Talk
	if talk == nil {
		t.Fatal("nothing was filled in")
	}
	if talk.Title != "What's wrong with us?" || len(talk.Speakers) != 1 || talk.Speakers[0] != "Jim Walford" ||
		talk.Passage != "Romans 3:9-20" || talk.Date != "2026-11-01" || talk.Church != "Emmanuel Bishopston" || !talk.Podcast {
		t.Fatalf("talk = %#v", talk)
	}
	// The programme gave no series, so the church's latest one is used.
	if talk.Series != "Life Together" || talk.VideoURL != "https://www.youtube.com/watch?v=FqAJchBCktM" {
		t.Fatalf("series %q, video %q", talk.Series, talk.VideoURL)
	}
	for _, want := range []string{"row 2 of the programme", "10:30 service", "latest talk", "What's Wrong with Us?"} {
		if !strings.Contains(talk.Source, want) {
			t.Errorf("source %q lacks %q", talk.Source, want)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, catalogueName)); err != nil {
		t.Errorf("the website's lists were not kept: %v", err)
	}

	// Opening the page again keeps the operator's changes; asking to fill in
	// again replaces them.
	requestSession(t, f.handler, http.MethodPatch, "/api/sessions/"+f.id+"/talk", `{"title":"Changed"}`)
	if again := requestSession(t, f.handler, http.MethodPost, path, `{}`); again.Talk.Title != "Changed" {
		t.Fatalf("opening the page again replaced the title with %q", again.Talk.Title)
	}
	if replaced := requestSession(t, f.handler, http.MethodPost, path, `{"replace":true}`); replaced.Talk.Title != "What's wrong with us?" {
		t.Fatalf("filling in again left %q", replaced.Talk.Title)
	}
	events, _ := f.sessions.Events(f.id)
	if last := events[len(events)-1]; last.Type != "talk.prefilled" {
		t.Fatalf("last event = %s", last.Type)
	}
}

func TestPrefillReportsAProgrammeItCannotRead(t *testing.T) {
	f := newPublishFixture(t)
	f.withProgramme("Week,Speaker\r\n")
	session := requestSession(t, f.handler, http.MethodPost, "/api/sessions/"+f.id+"/talk/prefill", `{}`)
	if !strings.Contains(session.Talk.Source, "Date column") || session.Talk.Title != "" {
		t.Fatalf("talk = %#v", session.Talk)
	}
}

func TestCheckSaysWhatTheWebsiteWillMakeOfTheTalk(t *testing.T) {
	f := newPublishFixture(t)
	f.fillTalk()
	check := f.check("")
	if check.Review == nil || len(check.Review.Problems) != 0 {
		t.Fatalf("review = %#v", check.Review)
	}
	if strings.Join(check.Churches, "|") != "Emmanuel Bishopston" || check.Speakers[0] != "Jim Walford" || check.Series[0] != "Life Together" {
		t.Fatalf("lists = %q, %q, %q", check.Churches, check.Speakers, check.Series)
	}
	if check.Image == nil || check.Image.ID != 16361 || !check.Image.FromSeries || check.Image.Error != "" {
		t.Fatalf("image = %#v", check.Image)
	}
	if len(check.Review.Books) != 1 || check.Review.Books[0] != "Matthew" || len(check.Duplicates) != 0 {
		t.Fatalf("books %q, duplicates %v", check.Review.Books, check.Duplicates)
	}

	// A near miss is a problem until it is confirmed as new.
	requestSession(t, f.handler, http.MethodPatch, "/api/sessions/"+f.id+"/talk", `{"speakers":["James Morris"]}`)
	// A talk filed by hand meanwhile is seen once the remembered answer has
	// expired, which is forced here.
	f.site.duplicate = true
	f.server.web.talksOn = nil
	check = f.check("")
	if len(check.Review.Problems) != 1 || !strings.Contains(check.Review.Problems[0], `"James Maurice"`) || len(check.Review.Speakers[0].Near) != 1 {
		t.Fatalf("review = %#v", check.Review)
	}
	if len(check.Duplicates) != 1 || check.Duplicates[0].Title != "By hand" {
		t.Fatalf("duplicates = %#v", check.Duplicates)
	}
	requestSession(t, f.handler, http.MethodPatch, "/api/sessions/"+f.id+"/talk", `{"newSpeakers":["James Morris"]}`)
	if check = f.check(""); len(check.Review.Problems) != 0 || !check.Review.Speakers[0].New {
		t.Fatalf("a confirmed new speaker = %#v", check.Review)
	}
}

func TestPublishSettingsTellTheReviewPageWhetherToUpload(t *testing.T) {
	f := newPublishFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/api/publish", nil)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), `"enabled":true`) || strings.Contains(response.Body.String(), "abcd") {
		t.Fatalf("publish settings = %s", response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/upload?session="+f.id, nil)
	response = httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "upload.js") {
		t.Fatalf("upload page: %d", response.Code)
	}
}
