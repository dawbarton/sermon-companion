package publish

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// catalogueSite answers as the church's site does for the lists and the
// latest sermons.
func catalogueSite(t *testing.T) *Client {
	terms := map[string]string{
		churchTaxonomy:  `[{"id":127,"name":"Emmanuel City Centre"},{"id":128,"name":"Emmanuel Bishopston"}]`,
		typeTaxonomy:    `[{"id":270,"name":"Sermon"},{"id":271,"name":"Blog"}]`,
		speakerTaxonomy: `[{"id":1,"name":"Ann Other"},{"id":2,"name":"Bob Jones"},{"id":3,"name":"Cy Twombly"},{"id":4,"name":"Di Prince"}]`,
		seriesTaxonomy:  `[{"id":736,"name":"The Lord&#8217;s Prayer (EB)"},{"id":738,"name":"Life Together"},{"id":740,"name":"Acts"}]`,
		bookTaxonomy:    `[{"id":45,"name":"Romans","slug":"045-romans"}]`,
	}
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		base := strings.TrimPrefix(r.URL.Path, "/wp-json/wp/v2/")
		if list, ok := terms[base]; ok {
			w.Write([]byte(list))
			return
		}
		if base != "resource" {
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get(typeTaxonomy) != "270" || !strings.Contains(r.URL.Query().Get("_fields"), "featured_media") {
			t.Errorf("latest talks asked for with %s", r.URL.RawQuery)
		}
		// Newest first, as the site lists them: City Centre's latest, then
		// Bishopston's two series, the older with its own image.
		w.Write([]byte(`[
			{"id":20,"date":"2026-11-01T12:00:00","link":"https://example.org/resources/c/","title":{"rendered":"City"},"featured_media":0,"resource-church":[127],"resource-contributor":[4],"resource-series":[740]},
			{"id":19,"date":"2026-11-01T10:30:00","link":"https://example.org/resources/b/","title":{"rendered":"What&#8217;s wrong"},"featured_media":16361,"resource-church":[128],"resource-contributor":[3],"resource-series":[738]},
			{"id":18,"date":"2026-10-25T10:30:00","link":"https://example.org/resources/a/","title":{"rendered":"Older"},"featured_media":16361,"resource-church":[128],"resource-contributor":[2,3],"resource-series":[738]},
			{"id":10,"date":"2026-09-13T10:30:00","link":"https://example.org/resources/p/","title":{"rendered":"Pardon"},"featured_media":15668,"resource-church":[128],"resource-contributor":[1],"resource-series":[736]}
		]`))
	})
	return client
}

func TestCatalogueOrdersTheChurchsOwnNamesFirst(t *testing.T) {
	catalogue, err := FetchCatalogue(context.Background(), catalogueSite(t), "Sermon")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Recent) != 4 || catalogue.Recent[1].Title != "What’s wrong" || catalogue.Recent[1].Date != "2026-11-01" {
		t.Fatalf("recent talks = %#v", catalogue.Recent)
	}
	speakers := catalogue.SpeakerNames("Emmanuel Bishopston")
	if strings.Join(speakers, "|") != "Cy Twombly|Bob Jones|Ann Other|Di Prince" {
		t.Fatalf("speakers = %q", speakers)
	}
	series := catalogue.SeriesNames("emmanuel bishopston")
	if strings.Join(series, "|") != "Life Together|The Lord’s Prayer (EB)|Acts" {
		t.Fatalf("series = %q", series)
	}
	if latest := catalogue.LatestSeries("Emmanuel Bishopston"); latest != "Life Together" {
		t.Fatalf("latest series = %q", latest)
	}
	if latest := catalogue.LatestSeries("Emmanuel Westbury"); latest != "" {
		t.Fatalf("a church the site does not have has a latest series: %q", latest)
	}
	if image := catalogue.SeriesImage(736); image != 15668 {
		t.Fatalf("series image = %d", image)
	}
	if image := catalogue.SeriesImage(740); image != 0 {
		t.Fatalf("a series without an image has %d", image)
	}

	// The copy kept on disk reads back the same; a missing copy is not an error.
	filename := filepath.Join(t.TempDir(), "website.json")
	if err := catalogue.Save(filename); err != nil {
		t.Fatal(err)
	}
	reread, err := LoadCatalogue(filename)
	if err != nil || reread == nil || len(reread.Speakers) != 4 || !reread.Fetched.Equal(catalogue.Fetched) {
		t.Fatalf("reread catalogue = %#v, %v", reread, err)
	}
	if missing, err := LoadCatalogue(filepath.Join(t.TempDir(), "none.json")); missing != nil || err != nil {
		t.Fatalf("a missing copy gave %v, %v", missing, err)
	}
}

func mediaSite(t *testing.T) *Client {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case r.URL.Path == "/wp-json/wp/v2/media/16361":
			w.Write([]byte(`{"id":16361,"title":{"rendered":"IN THE BEGINNING 11"},"link":"https://example.org/in-the-beginning-11/","source_url":"https://example.org/wp-content/uploads/2026/10/IN-THE-BEGINNING-11.png","media_type":"image"}`))
		case r.URL.Path == "/wp-json/wp/v2/media/16486":
			w.Write([]byte(`{"id":16486,"media_type":"file","source_url":"https://example.org/a.mp3"}`))
		case r.URL.Path == "/wp-json/wp/v2/media" && query.Get("slug") == "in-the-beginning-11":
			w.Write([]byte(`[{"id":16361,"title":{"rendered":"IN THE BEGINNING 11"},"link":"https://example.org/in-the-beginning-11/","source_url":"https://example.org/wp-content/uploads/2026/10/IN-THE-BEGINNING-11.png","media_type":"image"}]`))
		case r.URL.Path == "/wp-json/wp/v2/media" && query.Get("search") == "IN-THE-BEGINNING-11":
			// A search also finds images whose names merely contain the words.
			w.Write([]byte(`[
				{"id":16360,"title":{"rendered":"IN THE BEGINNING 1"},"source_url":"https://example.org/wp-content/uploads/2026/10/IN-THE-BEGINNING-11-old.png","media_type":"image"},
				{"id":16361,"title":{"rendered":"IN THE BEGINNING 11"},"source_url":"https://cdn.example.net/wp-content/uploads/2026/10/IN-THE-BEGINNING-11.png","media_type":"image"}
			]`))
		case r.URL.Path == "/wp-json/wp/v2/media":
			w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	})
	return client
}

func TestFindImageAcceptsAnIDAPageLinkOrAFileAddress(t *testing.T) {
	client := mediaSite(t)
	ctx := context.Background()
	for _, reference := range []string{
		"16361",
		"https://example.org/in-the-beginning-11/",
		"https://example.org/wp-content/uploads/2026/10/IN-THE-BEGINNING-11.png",
		// A resized copy, as the media library's thumbnails link to.
		"https://example.org/wp-content/uploads/2026/10/IN-THE-BEGINNING-11-300x169.png",
	} {
		image, err := client.FindImage(ctx, reference)
		if err != nil || image.ID != 16361 || image.Title != "IN THE BEGINNING 11" {
			t.Errorf("FindImage(%q) = %#v, %v", reference, image, err)
		}
	}
	for reference, want := range map[string]string{
		"16486":                              "not an image",
		"https://example.org/no-such-image/": "no image at",
		"https://example.org/uploads/unknown.png": "no image called unknown.png",
		"not a link":           "not a link",
		"https://example.org/": "not a link",
	} {
		if _, err := client.FindImage(ctx, reference); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("FindImage(%q) gave %v, want %q", reference, err, want)
		}
	}
}

func TestTalksOnComparesTheServiceDateNotThePostDate(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get(churchTaxonomy) != "128" || query.Get(typeTaxonomy) != "270" || !strings.Contains(query.Get("_fields"), "acf."+fieldDate) {
			t.Errorf("asked with %s", r.URL.RawQuery)
		}
		// "Our Father's pardon" was posted on 13 September for 6 September.
		w.Write([]byte(`[
			{"id":15946,"date":"2026-09-13T15:33:59","link":"https://example.org/pardon/","title":{"rendered":"Our Father&#8217;s pardon"},"acf":{"resource_date":"20260906"}},
			{"id":15944,"date":"2026-09-13T12:29:17","link":"https://example.org/protection/","title":{"rendered":"Our Father&#8217;s protection"},"acf":{"resource_date":"20260913"}},
			{"id":15000,"date":"2026-09-06T10:00:00","link":"https://example.org/old/","title":{"rendered":"Undated"},"acf":[]}
		]`))
	})
	found, err := client.TalksOn(context.Background(), 128, 270, "2026-09-06")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(found)
	// The undated talk has an acf array rather than an object, as ACF sends
	// when no field is set, and is matched by the day it was posted instead.
	if len(found) != 2 || found[0].ID != 15946 || found[0].Title != "Our Father’s pardon" || found[1].ID != 15000 {
		t.Fatalf("talks on 6 September = %s", encoded)
	}
}

func TestChooseVideoTakesTheFirstPublishedAroundTheService(t *testing.T) {
	at := func(text string) time.Time {
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	// The channel's feed as it was on 1 November 2026, newest first.
	videos := []Video{
		{ID: "FqAJchBCktM", Title: "What's Wrong with Us? Sunday 1 November 2026", Published: at("2026-11-01T10:01:42Z")},
		{ID: "LT-yW34lR-8", Title: "Life Together, Sunday 25 October 2026", Published: at("2026-10-26T00:45:10Z")},
		{ID: "rNQ2APCSxB4", Title: "Life with God, Sunday 18 October 2026", Published: at("2026-10-18T23:13:53Z")},
	}
	for started, want := range map[string]string{
		"2026-11-01T10:25:00Z": "FqAJchBCktM",
		"2026-10-25T09:30:00Z": "LT-yW34lR-8", // published the next morning, UTC
		"2026-10-18T09:30:00Z": "rNQ2APCSxB4",
		"2026-10-11T09:30:00Z": "",
	} {
		video, ok := ChooseVideo(videos, at(started))
		if video.ID != want || ok != (want != "") {
			t.Errorf("service at %s chose %q, want %q", started, video.ID, want)
		}
	}
	if url := videos[0].URL(); url != "https://www.youtube.com/watch?v=FqAJchBCktM" {
		t.Fatalf("URL = %q", url)
	}
}
