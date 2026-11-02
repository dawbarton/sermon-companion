// Tests of the OBS script's requests to Sermon Companion, run with LuaJIT
// against a local server standing in for the application. OBS's own events
// cannot be raised here; they were tried in OBS itself.
package obs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/proc"
)

type received struct {
	method, path, body, contentType string
}

// run runs one action of the script against address and returns whether it
// succeeded and its message.
func run(t *testing.T, address, action string, timeout int) (bool, string) {
	t.Helper()
	luajit, err := exec.LookPath("luajit")
	if err != nil {
		t.Skip("LuaJIT is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := proc.CommandContext(ctx, luajit, "testdata/harness.lua", "sermon-companion.lua", address, action, strconv.Itoa(timeout)).CombinedOutput()
	if err != nil {
		t.Fatalf("luajit: %v\n%s", err, output)
	}
	ok, message, found := strings.Cut(strings.TrimSpace(string(output)), "\t")
	if !found {
		t.Fatalf("unexpected output %q", output)
	}
	return ok == "true", message
}

func fakeApplication(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) (string, *[]received) {
	t.Helper()
	var mu sync.Mutex
	var requests []received
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, received{r.Method, r.URL.Path, string(body), r.Header.Get("Content-Type")})
		mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL, &requests
}

func TestStartAndStopMakeTheDocksRequests(t *testing.T) {
	address, requests := fakeApplication(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sessions":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"2026-11-02_103000_abcdef","status":"recording"}`))
		case "/api/stop-recording":
			// A finished service is several kilobytes, more than one read
			// and sent in chunks.
			w.Write([]byte(`{"id":"2026-11-02_103000_abcdef","status":"stopped","notes":"` + strings.Repeat("x", 20000) + `"}`))
		}
	})
	if ok, message := run(t, address, "start", 5); !ok || message != "Started Sermon Companion's recording." {
		t.Fatalf("start: %v %q", ok, message)
	}
	if ok, message := run(t, address, "stop", 5); !ok || message != "Stopped Sermon Companion's recording." {
		t.Fatalf("stop: %v %q", ok, message)
	}
	want := []received{
		{"POST", "/api/sessions", "{}", "application/json"},
		{"POST", "/api/stop-recording", "{}", "application/json"},
	}
	if len(*requests) != 2 || (*requests)[0] != want[0] || (*requests)[1] != want[1] {
		t.Fatalf("requests = %#v", *requests)
	}
}

func TestAnswersBesidesSuccessAreReported(t *testing.T) {
	address, _ := fakeApplication(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		switch r.URL.Path {
		case "/api/sessions":
			w.Write([]byte(`{"error":"a recording is already in progress"}`))
		case "/api/stop-recording":
			w.Write([]byte(`{"error":"nothing is being recorded"}`))
		}
	})
	if ok, message := run(t, address, "start", 5); !ok || !strings.Contains(message, "already recording, so that recording carries on") {
		t.Fatalf("start while recording: %v %q", ok, message)
	}
	if ok, message := run(t, address, "stop", 5); !ok || !strings.Contains(message, "was not recording") {
		t.Fatalf("stop with nothing recording: %v %q", ok, message)
	}

	refused, _ := fakeApplication(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"the capture device \"HDMI & USB\" is not connected"}`))
	})
	if ok, message := run(t, refused, "start", 5); ok || message != `Sermon Companion refused to start recording: the capture device "HDMI & USB" is not connected.` {
		t.Fatalf("refused start: %v %q", ok, message)
	}
}

func TestAnAbsentOrSilentApplicationIsReported(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	closed := server.URL
	server.Close()
	if ok, message := run(t, closed, "start", 5); ok || !strings.Contains(message, "is not running") {
		t.Fatalf("closed port: %v %q", ok, message)
	}

	release := make(chan struct{})
	silent, _ := fakeApplication(t, func(w http.ResponseWriter, r *http.Request) { <-release })
	defer close(release)
	started := time.Now()
	if ok, message := run(t, silent, "stop", 1); ok || !strings.Contains(message, "did not answer") {
		t.Fatalf("silent application: %v %q", ok, message)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("the timeout was not kept: %v", elapsed)
	}
}
