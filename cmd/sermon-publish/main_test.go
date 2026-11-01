// End-to-end tests of the command against a fake WordPress that remembers what
// has been created: an upload asks first, happens once, resumes after a failure
// without repeating itself, refuses to carry on when its talk has been deleted
// on the site, and -delete removes exactly what was recorded.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/store"
)

// fakeWordPress is the smallest site the whole command can run against.
type fakeWordPress struct {
	mu          sync.Mutex
	writes      []string // every request that would change the site
	failUploads int      // how many uploads to refuse as too large
	deleted     map[string]bool
	lastPost    map[string]any
}

func (f *fakeWordPress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, _, ok := r.BasicAuth(); !ok {
		w.WriteHeader(401)
		w.Write([]byte(`{"code":"rest_not_logged_in"}`))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/wp-json")
	if r.Method != http.MethodGet {
		f.writes = append(f.writes, r.Method+" "+path)
	}
	if f.deleted[path] {
		w.WriteHeader(404)
		w.Write([]byte(`{"code":"rest_post_invalid_id"}`))
		return
	}
	lists := map[string]string{
		"/wp/v2/resource-church":      `[{"id":128,"name":"Emmanuel Bishopston"}]`,
		"/wp/v2/resource-type":        `[{"id":270,"name":"Sermon"}]`,
		"/wp/v2/resource-contributor": `[{"id":703,"name":"David Barton"}]`,
		"/wp/v2/resource-series":      `[]`,
		"/wp/v2/bible_book":           `[{"id":239,"name":"Matthew"}]`,
	}
	switch {
	case r.Method == http.MethodGet && path == "/wp/v2/users/me":
		w.Write([]byte(`{"id":8,"slug":"uploads","roles":["author"],"capabilities":{"upload_files":true,"publish_posts":true}}`))
	case r.Method == http.MethodGet && lists[path] != "":
		w.Header().Set("X-WP-TotalPages", "1")
		w.Write([]byte(lists[path]))
	case r.Method == http.MethodPost && path == "/wp/v2/resource":
		json.NewDecoder(r.Body).Decode(&f.lastPost)
		w.WriteHeader(201)
		w.Write([]byte(`{"id":500}`))
	case r.Method == http.MethodPost && path == "/wp/v2/media":
		if f.failUploads > 0 {
			f.failUploads--
			w.WriteHeader(413)
			return
		}
		w.WriteHeader(201)
		w.Write([]byte(`{"id":501}`))
	case r.Method == http.MethodPost && path == "/wp/v2/resource/500":
		w.Write([]byte(`{"id":500,"link":"https://example.org/resources/test/"}`))
	case r.Method == http.MethodGet && path == "/wp/v2/resource/500":
		w.Write([]byte(`{"id":500,"status":"draft"}`))
	case r.Method == http.MethodGet && path == "/wp/v2/media/501":
		w.Write([]byte(`{"id":501,"post":500}`))
	case r.Method == http.MethodDelete:
		w.Write([]byte(`{"deleted":true}`))
	default:
		w.WriteHeader(404)
		w.Write([]byte(`{"code":"rest_no_route"}`))
	}
}

func (f *fakeWordPress) takeWrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	writes := f.writes
	f.writes = nil
	return writes
}

// fixture is one site, one session, one talk file, and one state folder.
type fixture struct {
	t      *testing.T
	site   *fakeWordPress
	server *httptest.Server
	args   []string
	state  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	session := filepath.Join(dir, "s_test01")
	os.MkdirAll(filepath.Join(session, "exports"), 0o755)
	data, _ := json.Marshal(store.Session{
		SchemaVersion: store.SchemaVersion, ID: "s_test01", Church: "Emmanuel Bishopston",
		StartedAt: time.Date(2026, 9, 13, 9, 30, 0, 0, time.UTC),
		Export:    &store.ExportInfo{Status: "completed", Output: "exports/2026-09-13-Emmanuel-Bishopston.mp3"},
	})
	os.WriteFile(filepath.Join(session, "session.json"), data, 0o644)
	os.WriteFile(filepath.Join(session, "exports", "2026-09-13-Emmanuel-Bishopston.mp3"), []byte("ID3 audio"), 0o644)
	os.WriteFile(filepath.Join(dir, "password"), []byte("abcd efgh ijkl mnop qrst uvwx\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "talk.json"), []byte(`{"title":"Test talk","speaker":"David Barton","passage":"Matthew 6:13"}`), 0o644)

	site := &fakeWordPress{deleted: map[string]bool{}}
	server := httptest.NewTLSServer(site)
	t.Cleanup(server.Close)
	state := filepath.Join(dir, "state")
	return &fixture{t: t, site: site, server: server, state: state, args: []string{
		"-site", server.URL, "-user", "uploads", "-password-file", filepath.Join(dir, "password"),
		"-session", session, "-talk", filepath.Join(dir, "talk.json"), "-state-dir", state,
	}}
}

// run runs the command with extra flags and typed input, returning its output.
func (f *fixture) run(input string, extra ...string) (string, error) {
	var out strings.Builder
	env := environment{
		in: strings.NewReader(input), out: &out, http: f.server.Client(),
		probe: func(context.Context, string, string) (time.Duration, error) { return 31 * time.Minute, nil },
	}
	err := run(context.Background(), append(append([]string{}, f.args...), extra...), env)
	return out.String(), err
}

func (f *fixture) record() record {
	data, err := os.ReadFile(filepath.Join(f.state, "s_test01.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var r record
	json.Unmarshal(data, &r)
	return r
}

func TestRequiredFlags(t *testing.T) {
	err := run(context.Background(), []string{"-site", "https://example.org"}, environment{out: &strings.Builder{}})
	if err == nil || !strings.Contains(err.Error(), "is required") {
		t.Errorf("error = %v", err)
	}
}

func TestUploadAsksFirstAndChangesNothingWithoutYes(t *testing.T) {
	f := newFixture(t)
	_, err := f.run("no\n", "-status", "private")
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("error = %v", err)
	}
	if writes := f.site.takeWrites(); len(writes) != 0 {
		t.Errorf("declining should change nothing, but made %v", writes)
	}
}

func TestUploadHappensOnceAndIsThenReported(t *testing.T) {
	f := newFixture(t)
	out, err := f.run("yes\n", "-status", "private", "-podcast=false")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := "POST /wp/v2/resource\nPOST /wp/v2/media\nPOST /wp/v2/resource/500"
	if got := strings.Join(f.site.takeWrites(), "\n"); got != want {
		t.Errorf("writes =\n%s\nwant\n%s", got, want)
	}
	if acf := f.site.lastPost["acf"].(map[string]any); acf["resource_include_in_podcast_feed"] != "No" {
		t.Errorf("-podcast=false should reach the site: %v", acf)
	}
	if r := f.record(); !r.Progress.Finished || r.Progress.PostID != 500 || r.Progress.MediaID != 501 {
		t.Errorf("record = %+v", r)
	}

	out, err = f.run("", "-status", "private", "-yes")
	if err != nil || !strings.Contains(out, "already been uploaded: https://example.org/resources/test/") {
		t.Errorf("a second run should report the talk: %v\n%s", err, out)
	}
	if writes := f.site.takeWrites(); len(writes) != 0 {
		t.Errorf("a second run must not upload again, but made %v", writes)
	}
}

func TestFailedUploadResumesWhereItStopped(t *testing.T) {
	f := newFixture(t)
	f.site.failUploads = 1
	_, err := f.run("", "-status", "private", "-yes")
	if err == nil || !strings.Contains(err.Error(), "larger than the website accepts") || !strings.Contains(err.Error(), "run the same command again") {
		t.Fatalf("error = %v", err)
	}
	if r := f.record(); r.Progress.PostID != 500 || r.Progress.MediaID != 0 {
		t.Fatalf("record after the failure = %+v", r)
	}
	f.site.takeWrites()

	out, err := f.run("", "-status", "private", "-yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.Join(f.site.takeWrites(), "\n"); got != "POST /wp/v2/media\nPOST /wp/v2/resource/500" {
		t.Errorf("the resumed run should upload and finish only, but made:\n%s", got)
	}
	if !strings.Contains(out, "Carrying on with the upload begun") {
		t.Errorf("the operator should be told it is resuming:\n%s", out)
	}
}

func TestResumeStopsIfTheTalkWasDeletedOnTheSite(t *testing.T) {
	f := newFixture(t)
	f.site.failUploads = 1
	f.run("", "-status", "private", "-yes")
	f.site.takeWrites()
	f.site.deleted["/wp/v2/resource/500"] = true

	_, err := f.run("", "-status", "private", "-yes")
	if err == nil || !strings.Contains(err.Error(), "is no longer on the site") || !strings.Contains(err.Error(), "not recreated automatically") {
		t.Errorf("error = %v", err)
	}
	if writes := f.site.takeWrites(); len(writes) != 0 {
		t.Errorf("nothing should be created in place of a deleted talk, but made %v", writes)
	}
}

func TestRecordForADifferentMP3IsRefused(t *testing.T) {
	f := newFixture(t)
	os.MkdirAll(f.state, 0o755)
	os.WriteFile(filepath.Join(f.state, "s_test01.json"),
		[]byte(fmt.Sprintf(`{"session":"s_test01","site":%q,"mp3":"2026-09-13-Emmanuel-Bishopston.mp3","size":12345,"progress":{"postId":500}}`, f.server.URL)), 0o644)
	_, err := f.run("", "-status", "private", "-yes")
	if err == nil || !strings.Contains(err.Error(), "not for this MP3 and site") {
		t.Errorf("error = %v", err)
	}
}

func TestDeleteAsksAndRemovesOnlyWhatWasRecorded(t *testing.T) {
	f := newFixture(t)
	if _, err := f.run("", "-status", "private", "-yes"); err != nil {
		t.Fatal(err)
	}
	f.site.takeWrites()

	_, err := f.run("no\n", "-delete", "-yes")
	if err == nil || !strings.Contains(err.Error(), "nothing was deleted") {
		t.Errorf("-delete must ask even with -yes: %v", err)
	}
	if writes := f.site.takeWrites(); len(writes) != 0 {
		t.Errorf("declining should delete nothing, but made %v", writes)
	}

	out, err := f.run("delete\n", "-delete")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "permanently") || !strings.Contains(out, "can be restored") {
		t.Errorf("the operator should be told what is permanent and what is not:\n%s", out)
	}
	if got := strings.Join(f.site.takeWrites(), "\n"); got != "DELETE /wp/v2/media/501\nDELETE /wp/v2/resource/500" {
		t.Errorf("deletes =\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(f.state, "s_test01.json")); !os.IsNotExist(err) {
		t.Error("the record should be set aside so that a fresh upload can start")
	}
	if matches, _ := filepath.Glob(filepath.Join(f.state, "s_test01.deleted-*.json")); len(matches) != 1 {
		t.Errorf("the deleted record should be kept: %v", matches)
	}
}

func TestDryRunChangesNothingAndWritesNoRecord(t *testing.T) {
	f := newFixture(t)
	out, err := f.run("", "-status", "private", "-dry-run")
	if err != nil || !strings.Contains(out, "would be made in this order") {
		t.Fatalf("%v\n%s", err, out)
	}
	if writes := f.site.takeWrites(); len(writes) != 0 {
		t.Errorf("a dry run made %v", writes)
	}
	if _, err := os.Stat(f.state); !os.IsNotExist(err) {
		t.Error("a dry run should not create a record")
	}
}

func TestReadTalkCatchesTypos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "talk.json")
	os.WriteFile(path, []byte(`{"title":"The Lost Sheep","speaker":"Ian Taylor","newSpeeker":true}`), 0o644)
	if _, err := readTalk(path); err == nil || !strings.Contains(err.Error(), "newSpeeker") {
		t.Errorf("an unknown key should be reported: %v", err)
	}
	os.WriteFile(path, []byte(`{"title":"The Lost Sheep","speaker":"Ian Taylor","newSpeaker":true,"passage":"Luke 15:1-7"}`), 0o644)
	talk, err := readTalk(path)
	if err != nil || !talk.NewSpeaker || talk.Passage != "Luke 15:1-7" {
		t.Errorf("talk %+v, err %v", talk, err)
	}
}
