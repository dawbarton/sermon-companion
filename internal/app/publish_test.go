package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

// fakeSite is the church website as far as an upload from the app needs it.
type fakeSite struct {
	mu          sync.Mutex
	writes      []string
	failUploads int
	duplicate   bool
	nextMedia   int
	lastFinish  map[string]any
	uploadQuery string
}

func (f *fakeSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if user, pass, ok := r.BasicAuth(); !ok || user != "uploader" || pass != "abcdEFGH1234" {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"incorrect_password","message":"The provided password is an invalid application password."}`))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/wp-json")
	if r.Method != http.MethodGet {
		f.writes = append(f.writes, r.Method+" "+path)
	}
	lists := map[string]string{
		"/wp/v2/resource-church":      `[{"id":128,"name":"Emmanuel Bishopston"}]`,
		"/wp/v2/resource-type":        `[{"id":270,"name":"Sermon"}]`,
		"/wp/v2/resource-contributor": `[{"id":296,"name":"Jim Walford"},{"id":299,"name":"James Maurice"},{"id":1,"name":"Ann Other"}]`,
		"/wp/v2/resource-series":      `[{"id":738,"name":"Life Together"}]`,
		"/wp/v2/bible_book":           `[{"id":239,"name":"Matthew"}]`,
	}
	switch {
	case r.Method == http.MethodGet && path == "/wp/v2/users/me":
		w.Write([]byte(`{"id":8,"slug":"uploads","roles":["author"],"capabilities":{"upload_files":true,"publish_posts":true}}`))
	case r.Method == http.MethodGet && lists[path] != "":
		w.Write([]byte(lists[path]))
	case r.Method == http.MethodGet && path == "/wp/v2/resource" && r.URL.Query().Get("resource-series") != "":
		w.Write([]byte(`[{"id":16415,"featured_media":16361}]`))
	case r.Method == http.MethodGet && path == "/wp/v2/resource" && r.URL.Query().Get("resource-church") == "":
		// The latest sermons, for the lists.
		w.Write([]byte(`[{"id":16415,"date":"2026-10-25T10:30:00","link":"https://example.org/resources/lt/","title":{"rendered":"Life Together"},"featured_media":16361,"resource-church":[128],"resource-contributor":[296],"resource-series":[738]}]`))
	case r.Method == http.MethodGet && path == "/wp/v2/resource":
		if f.duplicate {
			w.Write([]byte(`[{"id":16481,"date":"2026-11-01T12:00:00","link":"https://example.org/resources/by-hand/","title":{"rendered":"By hand"},"acf":{"resource_date":"20261101"}}]`))
			return
		}
		w.Write([]byte(`[]`))
	case r.Method == http.MethodGet && path == "/wp/v2/media/16361":
		w.Write([]byte(`{"id":16361,"title":{"rendered":"Life Together"},"media_type":"image","source_url":"https://example.org/lt.png"}`))
	case r.Method == http.MethodPost && path == "/wp/v2/resource":
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":500}`))
	case r.Method == http.MethodPost && path == "/wp/v2/media":
		if f.failUploads > 0 {
			f.failUploads--
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		f.uploadQuery = r.URL.RawQuery
		f.nextMedia++
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]int{"id": 500 + f.nextMedia})
	case r.Method == http.MethodPost && path == "/wp/v2/resource/500":
		json.NewDecoder(r.Body).Decode(&f.lastFinish)
		w.Write([]byte(`{"id":500,"link":"https://example.org/resources/test/"}`))
	case r.Method == http.MethodGet && path == "/wp/v2/resource/500":
		w.Write([]byte(`{"id":500,"status":"draft"}`))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/wp/v2/media/50"):
		w.Write([]byte(`{"post":500}`))
	case r.Method == http.MethodDelete:
		w.Write([]byte(`{"deleted":true}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"rest_no_route"}`))
	}
}

func (f *fakeSite) takeWrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	writes := f.writes
	f.writes = nil
	return writes
}

type publishFixture struct {
	t        *testing.T
	server   *Server
	handler  http.Handler
	sessions *store.Store
	site     *fakeSite
	id       string
	dir      string
	root     string
}

func newPublishFixture(t *testing.T) *publishFixture {
	t.Helper()
	site := &fakeSite{}
	web := httptest.NewTLSServer(site)
	t.Cleanup(web.Close)

	root := t.TempDir()
	sessions, err := store.New(root)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 1, 10, 30, 0, 0, time.Local)
	session, err := sessions.Create("1 November 2026", "Emmanuel Bishopston", started)
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	if err := os.WriteFile(filepath.Join(dir, "exports", "2026-11-01-Emmanuel-Bishopston.mp3"), []byte("ID3 audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	if _, err := sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status = "stopped"
		s.Export = &store.ExportInfo{Status: "completed", EndedAt: &ended, Output: "exports/2026-11-01-Emmanuel-Bishopston.mp3"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	c := config.DefaultConfig()
	c.Church = "Emmanuel Bishopston"
	c.Publish = &config.PublishConfig{Site: web.URL, User: "uploader"}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(filepath.Join(root, config.PasswordFileName), []byte("abcd EFGH 1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := config.NewSettings(configPath, c)
	server := NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles)
	server.probeDuration = func(context.Context, string, string) (time.Duration, error) { return 31 * time.Minute, nil }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	})
	f := &publishFixture{t: t, server: server, handler: server.Handler(), sessions: sessions, site: site, id: session.ID, dir: dir, root: root}
	server.publishHTTP = &http.Client{Transport: routes{site: web.Client().Transport, youtube: http.HandlerFunc(f.youtube)}}
	return f
}

// fillTalk gives the talk its details as the upload page would.
func (f *publishFixture) fillTalk() {
	f.t.Helper()
	requestSession(f.t, f.handler, http.MethodPatch, "/api/sessions/"+f.id+"/talk",
		`{"title":"What's wrong with us?","speakers":["Jim Walford"],"series":"Life Together","passage":"Matthew 6:13","videoUrl":"https://www.youtube.com/watch?v=FqAJchBCktM"}`)
}

// youtube answers for the channel's feed.
func (f *publishFixture) youtube(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/feeds/videos.xml" || r.URL.Query().Get("channel_id") != "UCReU3dVxdLKPuSs59dBgG6Q" {
		http.NotFound(w, r)
		return
	}
	w.Write([]byte(`<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015" xmlns="http://www.w3.org/2005/Atom">
<entry><yt:videoId>FqAJchBCktM</yt:videoId><title>What's Wrong with Us? Sunday 1 November 2026</title><published>` +
		time.Date(2026, 11, 1, 10, 1, 42, 0, time.Local).UTC().Format(time.RFC3339) + `</published></entry></feed>`))
}

// routes sends YouTube's requests to a handler and the rest to the site.
type routes struct {
	site    http.RoundTripper
	youtube http.Handler
}

func (r routes) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host == "www.youtube.com" {
		recorder := httptest.NewRecorder()
		r.youtube.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	}
	return r.site.RoundTrip(request)
}

// publish presses Publish and waits for the upload to end.
func (f *publishFixture) publish(body string) *store.Session {
	f.t.Helper()
	requestSession(f.t, f.handler, http.MethodPost, "/api/sessions/"+f.id+"/publish", body)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, publishing := f.server.busy(f.id); !publishing {
			session, err := f.sessions.Get(f.id)
			if err != nil {
				f.t.Fatal(err)
			}
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatal("the upload did not finish")
	return nil
}

func TestPublishUploadsTheTalkAndRecordsEachStep(t *testing.T) {
	f := newPublishFixture(t)
	f.fillTalk()
	session := f.publish(`{}`)
	if session.Publish == nil || session.Publish.Status != "completed" || !session.Publish.Progress.Finished ||
		session.Publish.Progress.Link != "https://example.org/resources/test/" || session.Publish.Error != "" {
		t.Fatalf("publish = %#v", session.Publish)
	}
	want := "POST /wp/v2/resource\nPOST /wp/v2/media\nPOST /wp/v2/resource/500"
	if got := strings.Join(f.site.takeWrites(), "\n"); got != want {
		t.Fatalf("writes =\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(f.site.uploadQuery, "title=What") {
		t.Errorf("upload query = %s", f.site.uploadQuery)
	}
	acf := f.site.lastFinish["acf"].(map[string]any)
	if f.site.lastFinish["status"] != "publish" || f.site.lastFinish["featured_media"] != 16361.0 || acf["resource_youtube_url"] == nil {
		t.Errorf("final request = %v", f.site.lastFinish)
	}
	events, err := f.sessions.Events(f.id)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range events {
		if strings.HasPrefix(event.Type, "publish.") {
			types = append(types, event.Type)
		}
	}
	if strings.Join(types, " ") != "publish.started publish.progress publish.progress publish.progress publish.completed" {
		t.Errorf("journal = %v", types)
	}

	// It is not uploaded twice, and its details are now corrected on the site.
	requestError(t, f.handler, http.MethodPost, "/api/sessions/"+f.id+"/publish", `{}`, http.StatusBadRequest, "already on the website")
	requestError(t, f.handler, http.MethodPatch, "/api/sessions/"+f.id+"/talk", `{"title":"Late"}`, http.StatusBadRequest, "already on the website")
}

func TestFailedUploadCarriesOnAndReplacesAnMP3MadeAgain(t *testing.T) {
	f := newPublishFixture(t)
	f.fillTalk()
	f.site.failUploads = 1
	session := f.publish(`{}`)
	if session.Publish.Status != "failed" || !strings.Contains(session.Publish.Error, "larger than the website accepts") || session.Publish.Progress.PostID != 500 {
		t.Fatalf("publish after a refused upload = %#v", session.Publish)
	}
	f.site.takeWrites()

	// Pressed again, it carries on without creating the talk again.
	session = f.publish(`{}`)
	if session.Publish.Status != "completed" {
		t.Fatalf("publish after the retry = %#v", session.Publish)
	}
	if got := strings.Join(f.site.takeWrites(), "\n"); got != "POST /wp/v2/media\nPOST /wp/v2/resource/500" {
		t.Fatalf("the retry made\n%s", got)
	}
}

func TestAnMP3MadeAgainReplacesTheOneUploadedBefore(t *testing.T) {
	f := newPublishFixture(t)
	f.fillTalk()
	// The file was uploaded, then the last step failed; the operator then
	// fixed a segment and made the MP3 again.
	uploaded := time.Now().Add(-time.Hour).UTC()
	f.site.nextMedia = 1 // the site has already given 501
	if _, err := f.sessions.Update(f.id, "test.partial", nil, func(s *store.Session) error {
		s.Publish = &store.PublishInfo{Status: "failed", Site: f.server.settings.Get().Publish.Site, MP3: s.Export.Output, MP3Finished: &uploaded,
			Progress: store.PublishProgress{PostID: 500, MediaID: 501}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	session := f.publish(`{}`)
	if session.Publish.Status != "completed" || session.Publish.Progress.MediaID != 502 || !sameTime(session.Publish.MP3Finished, session.Export.EndedAt) {
		t.Fatalf("publish = %#v", session.Publish)
	}
	if got := strings.Join(f.site.takeWrites(), "\n"); got != "DELETE /wp/v2/media/501\nPOST /wp/v2/media\nPOST /wp/v2/resource/500" {
		t.Fatalf("writes =\n%s", got)
	}
}

func TestATalkAlreadyOnTheSiteNeedsConfirming(t *testing.T) {
	f := newPublishFixture(t)
	f.fillTalk()
	f.site.duplicate = true
	session := f.publish(`{}`)
	if session.Publish.Status != "failed" || !strings.Contains(session.Publish.Error, `"By hand"`) || !session.Publish.Progress.Empty() {
		t.Fatalf("publish = %#v", session.Publish)
	}
	if writes := f.site.takeWrites(); len(writes) != 0 {
		t.Fatalf("a refused duplicate changed the site: %v", writes)
	}
	if session = f.publish(`{"allowDuplicate":true}`); session.Publish.Status != "completed" {
		t.Fatalf("a confirmed duplicate = %#v", session.Publish)
	}
}

func TestPublishIsRefusedUntilItCanWork(t *testing.T) {
	f := newPublishFixture(t)
	f.fillTalk()
	path := "/api/sessions/" + f.id + "/publish"

	// A wrong password is reported with where it is kept.
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.server.settings.Path()), config.PasswordFileName), []byte("wrong"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := f.publish(`{}`)
	if session.Publish.Status != "failed" || !strings.Contains(session.Publish.Error, config.PasswordFileName) {
		t.Fatalf("a wrong password gave %#v", session.Publish)
	}
	os.Remove(filepath.Join(filepath.Dir(f.server.settings.Path()), config.PasswordFileName))
	requestError(t, f.handler, http.MethodPost, path, `{}`, http.StatusConflict, "has not been set up")

	if _, err := f.sessions.Update(f.id, "test.stale", nil, func(s *store.Session) error {
		s.Export.Status = "stale"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(filepath.Dir(f.server.settings.Path()), config.PasswordFileName), []byte("abcdEFGH1234"), 0o600)
	requestError(t, f.handler, http.MethodPost, path, `{}`, http.StatusBadRequest, "out of date")

	// Neither can the MP3 be made again, nor the service deleted, while it
	// is being uploaded.
	f.server.jobsMu.Lock()
	f.server.jobs[publishJob(f.id)] = func() {}
	f.server.jobsMu.Unlock()
	requestError(t, f.handler, http.MethodPost, "/api/sessions/"+f.id+"/export", `{}`, http.StatusConflict, "wait for the upload")
	requestError(t, f.handler, http.MethodDelete, "/api/sessions/"+f.id, ``, http.StatusConflict, "wait for the upload")
	f.server.jobsMu.Lock()
	delete(f.server.jobs, publishJob(f.id))
	f.server.jobsMu.Unlock()

	c := config.DefaultConfig()
	settings := config.NewSettings("", c)
	unconfigured := NewServer(settings, f.sessions, capture.New(settings, f.sessions), master.New(c, f.sessions), StaticFiles).Handler()
	requestError(t, unconfigured, http.MethodPost, path, `{}`, http.StatusConflict, "not set up")
}
