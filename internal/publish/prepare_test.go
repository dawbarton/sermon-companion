// Tests for preparing an upload against a fake site holding the real terms and
// IDs: the Lord's Prayer talk of 13 September 2026 must resolve to the same
// terms it was filed under by hand, and a name the site does not know must stop
// the upload unless the operator has said it is new.
package publish

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeTermSite serves the five term lists a talk is filed under.
func fakeTermSite(t *testing.T) *Client {
	t.Helper()
	lists := map[string][]Term{
		"/wp-json/wp/v2/resource-church":      {{ID: 128, Name: "Emmanuel Bishopston"}, {ID: 127, Name: "Emmanuel City Centre"}, {ID: 129, Name: "Emmanuel Westbury"}},
		"/wp-json/wp/v2/resource-type":        {{ID: 272, Name: "Article"}, {ID: 690, Name: "Blog"}, {ID: 270, Name: "Sermon"}},
		"/wp-json/wp/v2/resource-contributor": siteSpeakers,
		"/wp-json/wp/v2/resource-series":      siteSeries,
		"/wp-json/wp/v2/bible_book":           {{ID: 225, Name: "Ezekiel"}, {ID: 239, Name: "Matthew"}, {ID: 244, Name: "Romans"}},
	}
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("preparing must only read, but sent %s %s", r.Method, r.URL.Path)
		}
		list, ok := lists[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"code":"rest_no_route"}`))
			return
		}
		w.Header().Set("X-WP-TotalPages", "1")
		json.NewEncoder(w).Encode(list)
	})
	return client
}

func lordsPrayerExport() *Export {
	export := &Export{Path: "/tmp/2026-09-13-Emmanuel-Bishopston.mp3", Name: "2026-09-13-Emmanuel-Bishopston.mp3", Size: 16237512}
	export.Session.Church = "Emmanuel Bishopston"
	export.Session.StartedAt = time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)
	return export
}

func lordsPrayerTalk() Talk {
	return Talk{
		Title:   "The Lord’s Prayer: Our Father’s protection",
		Speaker: "Jim Walford",
		Series:  "The Lord's Prayer (EB)",
		Passage: "Matthew 6:13, Ezekiel 36:22-27",
	}
}

func TestPrepareFilesTheTalkAsItWasFiledByHand(t *testing.T) {
	client := fakeTermSite(t)
	plan, warnings, err := Prepare(context.Background(), client, lordsPrayerExport(), lordsPrayerTalk(),
		Options{Status: "publish", Podcast: true}, 31*time.Minute+12*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
	if plan.Church.ID != 128 || plan.Type.ID != 270 || plan.Speaker.ID != 296 || plan.Series == nil || plan.Series.ID != 736 {
		t.Errorf("plan terms = church %d, type %d, speaker %d, series %v", plan.Church.ID, plan.Type.ID, plan.Speaker.ID, plan.Series)
	}
	if len(plan.Books) != 2 || plan.Books[0].ID != 239 || plan.Books[1].ID != 225 {
		t.Errorf("books = %v; want Matthew then Ezekiel", plan.Books)
	}
	if plan.LocalDate != "20260913" || plan.Passage != "Matthew 6:13, Ezekiel 36:22-27" {
		t.Errorf("date %q, passage %q", plan.LocalDate, plan.Passage)
	}
}

func TestPrepareStopsAtAnUnknownSpeakerAndSuggestsTheNearOne(t *testing.T) {
	talk := lordsPrayerTalk()
	talk.Speaker = "James Morris"
	_, _, err := Prepare(context.Background(), fakeTermSite(t), lordsPrayerExport(), talk, Options{Status: "publish", Podcast: true}, time.Minute)
	if err == nil || !strings.Contains(err.Error(), `did you mean "James Maurice"`) || !strings.Contains(err.Error(), `"newSpeaker"`) {
		t.Errorf("error = %v", err)
	}
}

func TestPrepareAcceptsANewSpeakerButWarnsAboutNearOnes(t *testing.T) {
	talk := lordsPrayerTalk()
	talk.Speaker, talk.NewSpeaker = "James Morris", true
	plan, warnings, err := Prepare(context.Background(), fakeTermSite(t), lordsPrayerExport(), talk, Options{Status: "publish", Podcast: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Speaker.ID != 0 || plan.Speaker.Name != "James Morris" {
		t.Errorf("speaker = %+v; it should be marked for creation", plan.Speaker)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"James Maurice"`) {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestPrepareRefusesWhatItCannotFile(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Talk, *Export, *Options)
		says   string
	}{
		{"unknown church", func(_ *Talk, e *Export, _ *Options) { e.Session.Church = "Emmanuel Clifton" }, `no church called "Emmanuel Clifton"`},
		{"no title", func(t *Talk, _ *Export, _ *Options) { t.Title = " " }, "needs a title"},
		{"no speaker", func(t *Talk, _ *Export, _ *Options) { t.Speaker = "" }, "needs a speaker"},
		{"bad status", func(_ *Talk, _ *Export, o *Options) { o.Status = "published" }, "must be publish, private, or draft"},
		{"unknown series", func(t *Talk, _ *Export, _ *Options) { t.Series = "Amazing Grace (2 Kings; EBB)" }, "did you mean"},
		{"book not on site", func(t *Talk, _ *Export, _ *Options) { t.Passage = "Mark 1:1" }, `no "Mark"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			talk, export, opts := lordsPrayerTalk(), lordsPrayerExport(), Options{Status: "publish", Podcast: true}
			test.change(&talk, export, &opts)
			_, _, err := Prepare(context.Background(), fakeTermSite(t), export, talk, opts, time.Minute)
			if err == nil || !strings.Contains(err.Error(), test.says) {
				t.Errorf("error = %v, want one saying %q", err, test.says)
			}
		})
	}
}

func TestPrepareWarnsWhenTheTalkIsKeptOutOfThePodcast(t *testing.T) {
	_, warnings, err := Prepare(context.Background(), fakeTermSite(t), lordsPrayerExport(), lordsPrayerTalk(), Options{Status: "private"}, time.Minute)
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "podcast") {
		t.Errorf("warnings %v, err %v", warnings, err)
	}
}
