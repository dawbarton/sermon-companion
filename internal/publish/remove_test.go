// Tests for checking and undoing an earlier upload: a talk or file deleted on
// the site, or sent to its bin, must be reported as gone rather than worked
// around, and removal must delete exactly the recorded IDs and nothing else.
package publish

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestVerifyFindsWhatWasCreated(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("context") != "edit" {
			t.Errorf("a draft or private talk is only visible in the edit context: %s", r.URL)
		}
		switch r.URL.Path {
		case "/wp-json/wp/v2/resource/500":
			w.Write([]byte(`{"id":500,"status":"draft"}`))
		case "/wp-json/wp/v2/media/501":
			w.Write([]byte(`{"id":501,"post":500}`))
		default:
			w.WriteHeader(404)
		}
	})
	if err := Verify(context.Background(), client, Progress{PostID: 500, MediaID: 501}); err != nil {
		t.Error(err)
	}
	if err := Verify(context.Background(), client, Progress{}); err != nil {
		t.Errorf("nothing recorded should need no checking: %v", err)
	}
}

func TestVerifyReportsWhatHasGone(t *testing.T) {
	for _, test := range []struct {
		name     string
		post     string
		media    string
		progress Progress
		gone     bool
		says     string
	}{
		{"talk deleted", "", `{"id":501,"post":500}`, Progress{PostID: 500}, true, "talk an earlier attempt created (ID 500)"},
		{"talk in the bin", `{"id":500,"status":"trash"}`, "", Progress{PostID: 500}, true, "talk an earlier attempt created"},
		{"file deleted", `{"id":500,"status":"draft"}`, "", Progress{PostID: 500, MediaID: 501}, true, "MP3 an earlier attempt uploaded (ID 501)"},
		{"file moved", `{"id":500,"status":"draft"}`, `{"id":501,"post":77}`, Progress{PostID: 500, MediaID: 501}, false, "attached to post 77"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				body := map[string]string{"/wp-json/wp/v2/resource/500": test.post, "/wp-json/wp/v2/media/501": test.media}[r.URL.Path]
				if body == "" {
					w.WriteHeader(404)
					w.Write([]byte(`{"code":"rest_post_invalid_id"}`))
					return
				}
				w.Write([]byte(body))
			})
			err := Verify(context.Background(), client, test.progress)
			if err == nil || !strings.Contains(err.Error(), test.says) || errors.Is(err, ErrGone) != test.gone {
				t.Errorf("error = %v; want one saying %q, gone %v", err, test.says, test.gone)
			}
		})
	}
}

func TestRemoveDeletesExactlyTheRecordedIDs(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		if r.URL.Path == "/wp-json/wp/v2/resource/500" {
			// Already in the bin: WordPress answers 410 to a second trashing.
			w.WriteHeader(http.StatusGone)
			w.Write([]byte(`{"code":"rest_already_trashed"}`))
			return
		}
		w.Write([]byte(`{"deleted":true}`))
	})
	if err := Remove(context.Background(), client, Progress{Speakers: map[string]int{"Ruth Visiting": 740}, PostID: 500, MediaID: 501}); err != nil {
		t.Fatal(err)
	}
	want := []string{"DELETE /wp-json/wp/v2/media/501?force=true", "DELETE /wp-json/wp/v2/resource/500?"}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests =\n%s\nwant\n%s", strings.Join(requests, "\n"), strings.Join(want, "\n"))
	}
}
