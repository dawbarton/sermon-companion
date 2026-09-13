package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSiteAssumesHTTPSAndTrimsTrailingParts(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
	}{
		{"example.org", "https://example.org"},
		{"https://example.org/", "https://example.org"},
		{"http://example.org/church/", "http://example.org/church"},
		{"https://example.org/?a=b#c", "https://example.org"},
	} {
		parsed, err := parseSite(test.raw)
		if err != nil {
			t.Fatalf("parseSite(%q): %v", test.raw, err)
		}
		if parsed.String() != test.want {
			t.Errorf("parseSite(%q) = %q, want %q", test.raw, parsed.String(), test.want)
		}
	}
	for _, raw := range []string{"", "   ", "ftp://example.org"} {
		if _, err := parseSite(raw); err == nil {
			t.Errorf("parseSite(%q) accepted an address it should have refused", raw)
		}
	}
}

func TestRESTURLCoversBothPermalinkForms(t *testing.T) {
	base, err := parseSite("https://example.org/church")
	if err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Base: base, pretty: true}
	got := probe.restURL("/wp/v2/media", url.Values{"per_page": {"1"}})
	if got != "https://example.org/church/wp-json/wp/v2/media?per_page=1" {
		t.Errorf("pretty form = %q", got)
	}
	probe.pretty = false
	got = probe.restURL("/wp/v2/media", url.Values{"per_page": {"1"}})
	if got != "https://example.org/church/?per_page=1&rest_route=%2Fwp%2Fv2%2Fmedia" {
		t.Errorf("plain form = %q", got)
	}
}

func TestLooksLikeSermonMatchesPluginNamesNotOrdinaryContent(t *testing.T) {
	for _, name := range []string{"wpfc_sermon", "Sermon Series", "sermon-manager/v1", "Preacher", "podcast"} {
		if !looksLikeSermon(name) {
			t.Errorf("looksLikeSermon(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"page", "category", "wp/v2", "Events"} {
		if looksLikeSermon(name) {
			t.Errorf("looksLikeSermon(%q) = true, want false", name)
		}
	}
}

func TestIsCandidateKeepsUnfamiliarTypesAndSkipsUnexposedOnes(t *testing.T) {
	if !isCandidate("resource", postType{RestBase: "resource", Name: "Resource"}) {
		t.Error("an unfamiliar custom type should still be a candidate; sermons need not be called sermons")
	}
	if isCandidate("wpfc_sermon", postType{Name: "Sermons"}) {
		t.Error("a type with no REST base cannot be written to and is not a candidate")
	}
	if isCandidate("page", postType{RestBase: "pages", Name: "Pages"}) {
		t.Error("builtin pages should not be reported as a candidate talk type")
	}
}

func TestDescribeArgsNamesTheFieldsOfThePOSTEndpoint(t *testing.T) {
	body := []byte(`{"endpoints":[
		{"methods":["GET"],"args":{"per_page":{}}},
		{"methods":["POST"],"args":{"title":{},"date":{},"wpfc_preacher":{}}}]}`)
	got := describeArgs(body)
	for _, want := range []string{"title", "date", "wpfc_preacher"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeArgs() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "per_page") {
		t.Errorf("describeArgs() = %q, should describe the POST endpoint, not the listing", got)
	}
	if !strings.Contains(describeArgs([]byte(`{"endpoints":[{"methods":["GET"],"args":{}}]}`)), "no POST route") {
		t.Error("a type the credentials cannot create should be reported as such")
	}
}

// filterBySlug mimics the `slug` query parameter of a WordPress collection
// route, which is what resolving a permalink relies on.
func filterBySlug(body any, slug string) any {
	entries, ok := body.([]any)
	if !ok {
		return body
	}
	kept := []any{}
	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if fields["slug"] == slug {
			kept = append(kept, entry)
		}
	}
	return kept
}

// fakeSite is the smallest WordPress-shaped site that exercises the whole walk:
// an index, a custom talk type with a taxonomy, media, and an authenticated
// account.
func fakeSite(t *testing.T) *httptest.Server {
	t.Helper()
	routes := map[string]any{
		"/": map[string]any{
			"name":            "St Mary's",
			"description":     "A parish church",
			"namespaces":      []string{"wp/v2", "sermon-manager/v1"},
			"authentication":  map[string]any{"application-passwords": map[string]any{}},
			"timezone_string": "Europe/London",
			"routes":          map[string]any{"/wp/v2/posts": map[string]any{}, "/sermon-manager/v1/series": map[string]any{}},
		},
		"/wp/v2/types": map[string]any{
			"post":        map[string]any{"name": "Posts", "slug": "post", "rest_base": "posts", "taxonomies": []string{"category"}},
			"page":        map[string]any{"name": "Pages", "slug": "page", "rest_base": "pages"},
			"wpfc_sermon": map[string]any{"name": "Sermons", "slug": "wpfc_sermon", "rest_base": "sermons", "taxonomies": []string{"wpfc_preacher", "congregation"}},
		},
		"/wp/v2/taxonomies": map[string]any{
			"wpfc_preacher": map[string]any{"name": "Preachers", "slug": "wpfc_preacher", "rest_base": "preachers", "types": []string{"wpfc_sermon"}},
			"congregation":  map[string]any{"name": "Congregations", "slug": "congregation", "rest_base": "congregations", "types": []string{"wpfc_sermon"}},
			"category":      map[string]any{"name": "Categories", "slug": "category", "rest_base": "categories", "types": []string{"post"}},
			"bible_book":    map[string]any{"name": "Bible Books", "slug": "bible_book", "rest_base": "bible_books", "types": []string{"wpfc_sermon"}},
		},
		// The edit context names each taxonomy's capabilities and leaves out any
		// the account may not assign, here the Bible books.
		"/wp/v2/taxonomies?context=edit": map[string]any{
			"wpfc_preacher": map[string]any{"slug": "wpfc_preacher", "hierarchical": false,
				"capabilities": map[string]string{"assign_terms": "edit_posts", "edit_terms": "manage_categories"}},
			"congregation": map[string]any{"slug": "congregation", "hierarchical": true,
				"capabilities": map[string]string{"assign_terms": "edit_posts", "edit_terms": "manage_categories"}},
		},
		"/wp/v2/preachers":     []any{map[string]any{"id": 7, "name": "David Barton", "slug": "david-barton", "count": 3}},
		"/wp/v2/congregations": []any{map[string]any{"id": 11, "name": "Emmanuel Bishopston", "slug": "emmanuel-bishopston", "count": 40}},
		"/wp/v2/search": []any{map[string]any{
			"id": 42, "title": "The Prodigal Son", "type": "post", "subtype": "wpfc_sermon",
			"url": "https://example.org/sermons/the-prodigal-son/",
		}},
		"/wp/v2/sermons": []any{map[string]any{
			"id": 42, "slug": "the-prodigal-son", "date": "2026-09-06T10:30:00", "title": map[string]any{"rendered": "The Prodigal Son"},
			"meta": map[string]any{"sermon_date": "1757116800", "bible_passage": "Luke 15"},
		}},
		"/wp/v2/posts": []any{},
		"/wp/v2/media": []any{map[string]any{"id": 9, "source_url": "https://example.org/2026/08/service.mp3", "mime_type": "audio/mpeg"}},
		"/wp/v2/users/me": map[string]any{
			"id": 2, "name": "David", "slug": "david", "roles": []string{"editor"},
			"email":        "private@example.org",
			"capabilities": map[string]bool{"upload_files": true, "edit_posts": true, "publish_posts": false},
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := strings.TrimPrefix(r.URL.Path, "/wp-json")
		if route == "" {
			route = "/"
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"endpoints": []any{
				map[string]any{"methods": []string{"POST"}, "args": map[string]any{"title": map[string]any{}, "wpfc_preacher": map[string]any{}}},
			}})
			return
		}
		edit := route == "/wp/v2/taxonomies" && r.URL.Query().Get("context") == "edit"
		if (route == "/wp/v2/users/me" || edit) && r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"code":"rest_not_logged_in"}`))
			return
		}
		if edit {
			route += "?context=edit"
		}
		body, ok := routes[route]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"code":"rest_no_route"}`))
			return
		}
		if slug := r.URL.Query().Get("slug"); slug != "" {
			body = filterBySlug(body, slug)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body)
	}))
}

func TestRunReportsTheTalkTypeItsTaxonomyAndTheAccount(t *testing.T) {
	server := fakeSite(t)
	defer server.Close()
	base, err := parseSite(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	probe := &Probe{Base: base, User: "david", Pass: "secret", OutDir: dir, HTTP: server.Client()}
	summary := probe.Run()

	for _, want := range []string{
		"wpfc_sermon",            // the talk type was found
		"wpfc_preacher",          // and the taxonomy a speaker would come from
		"david-barton",           // whose terms were listed
		"The Prodigal Son",       // the previous talk defaults would be copied from
		"bible_passage",          // including its custom meta fields
		"sermon-manager/v1",      // a plugin namespace worth reading
		"`upload_files`: true",   // the account can attach an MP3
		"`publish_posts`: false", // but cannot publish, which changes the design
		"Europe/London",          // dates without a zone are read in this one
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not mention %q\n---\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "private@example.org") {
		t.Error("the account's email address must not reach the summary")
	}
	saved, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) < 4 {
		t.Errorf("expected the raw responses to be saved for later reading, got %v", saved)
	}
	for _, path := range saved {
		if strings.Contains(filepath.Base(path), "me") {
			content, err := readFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(content, "private@example.org") {
				t.Errorf("%s holds the account's email address", path)
			}
		}
	}
}

func TestAuthenticatedRunReportsWhichTermsTheAccountMayCreate(t *testing.T) {
	server := fakeSite(t)
	defer server.Close()
	base, err := parseSite(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Base: base, User: "david", Pass: "secret", OutDir: t.TempDir(), HTTP: server.Client()}
	summary := probe.Run()

	for _, want := range []string{
		// A flat taxonomy: assigning is enough to create, as for a new speaker.
		"| `wpfc_preacher` | no | yes (`edit_posts`) | yes (`assign_terms`: `edit_posts`) |",
		// A hierarchical one needs edit_terms, which this account lacks.
		"| `congregation` | yes | yes (`edit_posts`) | no (`edit_terms`: `manage_categories`) |",
		// Left out of the edit listing, so the account may not even assign it.
		"| `bible_book` | ? | no | no |",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not contain %q\n---\n%s", want, summary)
		}
	}
}

func TestAnonymousRunDoesNotReportTermRights(t *testing.T) {
	server := fakeSite(t)
	defer server.Close()
	base, err := parseSite(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Base: base, OutDir: t.TempDir(), HTTP: server.Client()}
	if strings.Contains(probe.Run(), "Terms this account may assign and create") {
		t.Error("term rights belong to an account and should not be reported without one")
	}
}

func TestEmailAddressGivenAsUserStaysOutOfTheSummary(t *testing.T) {
	server := fakeSite(t)
	defer server.Close()
	base, err := parseSite(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Base: base, User: "uploads@example.org", Pass: "secret", OutDir: t.TempDir(), HTTP: server.Client()}
	if summary := probe.Run(); strings.Contains(summary, "uploads@example.org") {
		t.Errorf("an email address given as the user must not reach the summary\n---\n%s", summary)
	}
}

func TestRunSaysSoWhenThereIsNoRESTAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	base, err := parseSite(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Base: base, OutDir: t.TempDir(), HTTP: server.Client()}
	if !strings.Contains(probe.Run(), "No REST API was found") {
		t.Error("a site without a REST API should be reported plainly rather than as an empty result")
	}
}

func TestSuppliedURLNamesThePostTypeAndTheTaxonomyBehindAFacet(t *testing.T) {
	server := fakeSite(t)
	defer server.Close()
	base, err := parseSite(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// A facet parameter named after a Bible book but carrying a congregation
	// slug is exactly the case the lookup exists for: the parameter's name is
	// no guide at all to the taxonomy behind it.
	talk, err := url.Parse(server.URL + "/sermons/the-prodigal-son/?_resource_bible_book_2_2=emmanuel-bishopston")
	if err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Base: base, OutDir: t.TempDir(), HTTP: server.Client(), TalkURL: talk}
	summary := probe.Run()

	for _, want := range []string{
		"found in taxonomy `congregation` as term 11",
		"Found in post type `wpfc_sermon`",
		"15-page-wpfc_sermon.json",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not mention %q\n---\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "found in taxonomy `wpfc_preacher`") {
		t.Error("the slug was reported against a taxonomy that does not hold it")
	}
}

func TestSuppliedArchiveURLSaysItIsNotOneTalk(t *testing.T) {
	server := fakeSite(t)
	defer server.Close()
	base, err := parseSite(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	talk, err := url.Parse(server.URL + "/emmanuel-sermons/?_resource_bible_book_2_2=emmanuel-bishopston")
	if err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Base: base, OutDir: t.TempDir(), HTTP: server.Client(), TalkURL: talk}
	summary := probe.Run()

	if !strings.Contains(summary, "supply the address of a single talk") {
		t.Errorf("a filtered listing should be reported as not being one talk\n---\n%s", summary)
	}
	// The facet is still worth tracing even when the page is an archive: that
	// is where the church term is learnt from.
	if !strings.Contains(summary, "found in taxonomy `congregation` as term 11") {
		t.Errorf("the filter value should still be traced on an archive page\n---\n%s", summary)
	}
}

func TestHiddenRESTIndexIsNotMistakenForNoRESTAPI(t *testing.T) {
	inner := fakeSite(t)
	defer inner.Close()
	// Several security plugins refuse the index while leaving the routes open.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, "/wp-json") == "" || r.URL.Query().Get("rest_route") == "/" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"code":"rest_disabled"}`))
			return
		}
		http.Redirect(w, r, inner.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	base, err := parseSite(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Base: base, OutDir: t.TempDir(), HTTP: server.Client()}
	summary := probe.Run()

	if strings.Contains(summary, "No REST API was found") {
		t.Errorf("a hidden index should not be reported as a missing REST API\n---\n%s", summary)
	}
	if !strings.Contains(summary, "wpfc_sermon") {
		t.Errorf("the talk type should still be found through the fallback\n---\n%s", summary)
	}
}

func TestRefusalBodyIsReportedSoTheRefuserCanBeIdentified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`<html><body>Blocked by Wordfence</body></html>`))
	}))
	defer server.Close()
	base, err := parseSite(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Base: base, OutDir: t.TempDir(), HTTP: server.Client()}
	summary := probe.Run()

	if !strings.Contains(summary, "Blocked by Wordfence") {
		t.Errorf("the refusal body distinguishes a firewall from WordPress and must be shown\n---\n%s", summary)
	}
}
