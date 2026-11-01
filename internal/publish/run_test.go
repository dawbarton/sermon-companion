// Tests for carrying out a plan: the requests go in the order that leaves
// nothing public until the end, each carries exactly the fields the site
// expects, IDs flow from one request to the next, and an upload that fails part
// way resumes without creating anything twice.
package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type call struct {
	kind    string // "term", "json", or "upload"
	route   string
	query   url.Values
	payload map[string]any
	body    string
}

// recorder is a Sender that remembers every request and answers with the IDs
// the site gave the Lord's Prayer talk. failUpload makes the upload fail once.
type recorder struct {
	calls      []call
	failUpload bool
}

func (r *recorder) CreateTerm(ctx context.Context, restBase, name string) (Term, error) {
	r.calls = append(r.calls, call{kind: "term", route: restBase, body: name})
	return Term{ID: 740, Name: name}, nil
}

func (r *recorder) SendJSON(ctx context.Context, op, method, route string, query url.Values, payload, out any) error {
	encoded, _ := json.Marshal(payload)
	var decoded map[string]any
	json.Unmarshal(encoded, &decoded)
	r.calls = append(r.calls, call{kind: "json", route: method + " " + route, query: query, payload: decoded})
	return json.Unmarshal([]byte(`{"id":15944,"link":"https://example.org/resources/the-lords-prayer/"}`), out)
}

func (r *recorder) UploadFile(ctx context.Context, op, route string, query url.Values, filename, contentType string, content io.Reader, size int64, out any) error {
	if r.failUpload {
		r.failUpload = false
		return &Error{Op: op, Kind: KindTimeout}
	}
	body, _ := io.ReadAll(content)
	r.calls = append(r.calls, call{kind: "upload", route: route, query: query, body: filename + " " + contentType + " " + string(body)})
	return json.Unmarshal([]byte(`{"id":15945}`), out)
}

func testPlan(t *testing.T) *Plan {
	t.Helper()
	file := filepath.Join(t.TempDir(), "2026-09-13-Emmanuel-Bishopston.mp3")
	if err := os.WriteFile(file, []byte("ID3 audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Plan{
		Title:     "The Lord's Prayer: Our Father's protection",
		Passage:   "Matthew 6:13, Ezekiel 36:22-27",
		Started:   time.Date(2026, 9, 13, 9, 30, 0, 0, time.UTC),
		LocalDate: "20260913",
		Church:    Term{ID: 128},
		Type:      Term{ID: 270},
		Speaker:   Term{Name: "Ruth Visiting"}, // new
		Series:    &Term{ID: 736},
		Books:     []Term{{ID: 239}, {ID: 225}},
		File:      file,
		FileName:  "2026-09-13-Emmanuel-Bishopston.mp3",
		Size:      9,
		Duration:  31*time.Minute + 12*time.Second + 400*time.Millisecond,
		Status:    "publish",
		Podcast:   true,
	}
}

func TestRunMakesTheRequestsInOrderWithTheSitesFields(t *testing.T) {
	plan := testPlan(t)
	sender := &recorder{}
	var saved []Progress
	progress := &Progress{}
	if err := Run(context.Background(), sender, plan, progress, func(p Progress) error { saved = append(saved, p); return nil }); err != nil {
		t.Fatal(err)
	}

	kinds := make([]string, len(sender.calls))
	for i, c := range sender.calls {
		kinds[i] = c.kind + " " + c.route
	}
	want := []string{"term resource-contributor", "json POST /wp/v2/resource", "upload /wp/v2/media", "json POST /wp/v2/resource/15944"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("requests = %v, want %v", kinds, want)
	}

	draft := sender.calls[1].payload
	acf := draft["acf"].(map[string]any)
	for field, wantValue := range map[string]any{
		"status":   "draft",
		"date_gmt": "2026-09-13T09:30:00",
		"title":    plan.Title,
	} {
		if draft[field] != wantValue {
			t.Errorf("draft %s = %v, want %v", field, draft[field], wantValue)
		}
	}
	for field, wantValue := range map[string]any{
		"resource-church":      []any{128.0},
		"resource-type":        []any{270.0},
		"resource-contributor": []any{740.0}, // the speaker just created
		"resource-series":      []any{736.0},
		"bible_book":           []any{239.0, 225.0},
	} {
		if !reflect.DeepEqual(draft[field], wantValue) {
			t.Errorf("draft %s = %v, want %v", field, draft[field], wantValue)
		}
	}
	if acf["resource_date"] != "20260913" || acf["resource_type"] != 270.0 ||
		acf["resource_bible_passages"] != plan.Passage || acf["resource_include_in_podcast_feed"] != "Yes" {
		t.Errorf("draft acf = %v", acf)
	}

	upload := sender.calls[2]
	if upload.query.Get("post") != "15944" || upload.body != "2026-09-13-Emmanuel-Bishopston.mp3 audio/mpeg ID3 audio" {
		t.Errorf("upload = %+v", upload)
	}

	finish := sender.calls[3].payload
	finishACF := finish["acf"].(map[string]any)
	info := finishACF["resource_audio_info"].(map[string]any)
	if finish["status"] != "publish" || finishACF["resource_audio"] != 15945.0 || finishACF["resource_type"] != 270.0 ||
		info["resource_audio_file_size"] != 9.0 || info["resource_audio_duration"] != "00:31:12" {
		t.Errorf("finish = %v", finish)
	}

	if len(saved) != 4 {
		t.Fatalf("progress saved %d times, want once per step", len(saved))
	}
	final := saved[len(saved)-1]
	if final.SpeakerID != 740 || final.PostID != 15944 || final.MediaID != 15945 || !final.Finished || final.Link == "" {
		t.Errorf("final progress = %+v", final)
	}
}

func TestRunResumesWithoutCreatingAnythingTwice(t *testing.T) {
	plan := testPlan(t)
	sender := &recorder{failUpload: true}
	progress := &Progress{}
	err := Run(context.Background(), sender, plan, progress, nil)
	if !IsKind(err, KindTimeout) {
		t.Fatalf("error = %v, want the upload's timeout", err)
	}
	if progress.SpeakerID != 740 || progress.PostID != 15944 || progress.MediaID != 0 {
		t.Fatalf("progress after the failure = %+v", progress)
	}

	sender.calls = nil
	if err := Run(context.Background(), sender, plan, progress, nil); err != nil {
		t.Fatal(err)
	}
	if len(sender.calls) != 2 || sender.calls[0].kind != "upload" || sender.calls[1].route != "POST /wp/v2/resource/15944" {
		t.Errorf("the retry should upload and finish only, but made %+v", sender.calls)
	}
}

func TestRunLeavesTheSeriesEmptyWhenThereIsNone(t *testing.T) {
	plan := testPlan(t)
	plan.Series, plan.Speaker = nil, Term{ID: 296}
	sender := &recorder{}
	if err := Run(context.Background(), sender, plan, &Progress{}, nil); err != nil {
		t.Fatal(err)
	}
	if sender.calls[0].kind != "json" {
		t.Errorf("an existing speaker should not be created again: first request %+v", sender.calls[0])
	}
	if series := sender.calls[0].payload["resource-series"]; !reflect.DeepEqual(series, []any{}) {
		t.Errorf("series = %#v, want an empty list rather than null", series)
	}
}

func TestRunStopsIfProgressCannotBeSaved(t *testing.T) {
	sender := &recorder{}
	broken := errors.New("disk full")
	err := Run(context.Background(), sender, testPlan(t), &Progress{}, func(Progress) error { return broken })
	if !errors.Is(err, broken) || len(sender.calls) != 1 {
		t.Errorf("error %v after %d requests; an unrecorded ID must stop the upload at once", err, len(sender.calls))
	}
}

func TestDryRunPrintsEveryRequestAndChangesNothing(t *testing.T) {
	var out bytes.Buffer
	plan := testPlan(t)
	plan.File = filepath.Join(t.TempDir(), "absent.mp3") // a dry run must not need to read it
	if err := Run(context.Background(), &DryRun{Out: &out}, plan, &Progress{}, nil); err == nil {
		// Opening the file is part of Run, so a missing file is still caught
		// in a dry run, which is what is wanted.
		t.Fatal("a missing MP3 should be reported even in a dry run")
	}

	out.Reset()
	if err := Run(context.Background(), &DryRun{Out: &out}, testPlan(t), &Progress{}, nil); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		`1. adding "Ruth Visiting" to the resource-contributor list`,
		"2. creating the talk\n   POST /wp-json/wp/v2/resource\n",
		`"status": "draft"`,
		`"resource-contributor": [`,
		"POST /wp-json/wp/v2/media?post=-2",
		`Content-Disposition: attachment; filename="2026-09-13-Emmanuel-Bishopston.mp3"`,
		"(9 bytes of audio)",
		"POST /wp-json/wp/v2/resource/-2",
		`"resource_audio": -3`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("dry run output lacks %q\n---\n%s", want, text)
		}
	}
}
