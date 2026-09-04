package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func ffmpegBackedHandler(t *testing.T) http.Handler {
	t.Helper()
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	c.Capture.Backend, c.Capture.Driver, c.Capture.Device = "ffmpeg", "lavfi", "sine=frequency=440"
	settings := config.NewSettings("", c)
	return NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles).Handler()
}

// The FFmpeg backends have no list to offer. The dock must be told that plainly
// rather than being handed an empty list it would read as "no devices found".
func TestDeviceListReportsAnUnselectableBackend(t *testing.T) {
	recorder := httptest.NewRecorder()
	ffmpegBackedHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/devices", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var response deviceListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Selectable {
		t.Fatal("the FFmpeg backend was reported as offering a device list")
	}
	if response.SelectedName != "sine=frequency=440" {
		t.Fatalf("selected name = %q, want the configured device", response.SelectedName)
	}
	if response.Error == "" {
		t.Fatal("no reason was given for the missing list")
	}
	if response.SelectedMissing {
		t.Fatal("a device that could not be listed must not be called missing")
	}
}

func TestSelectingADeviceIsRefusedWhenNoneCanBeListed(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader(`{"id":"anything"}`))
	ffmpegBackedHandler(t).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
}

func TestDeviceSelectableMirrorsTheCaptureLookup(t *testing.T) {
	devices := []capture.Device{
		{ID: "{0.0.1.0}", Name: "HDMI capture", IsDefault: false},
		{ID: "{0.0.1.1}", Name: "Microphone", IsDefault: true},
	}
	cases := []struct {
		name, id, device string
		want             bool
	}{
		{"the system default is always present", "", "", true},
		{"the word default means the system default", "", "default", true},
		{"a known identifier is found", "{0.0.1.0}", "HDMI capture", true},
		{"identifiers are compared without regard to capitals", "{0.0.1.A}", "", false},
		{"a name alone is found when no identifier is saved", "", "Microphone", true},
		{"an unplugged device is reported missing", "{0.0.9.9}", "HDMI capture", false},
		{"a name that matches nothing is reported missing", "", "Old mixer", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := deviceSelectable(devices, test.id, test.device); got != test.want {
				t.Fatalf("deviceSelectable(%q, %q) = %v, want %v", test.id, test.device, got, test.want)
			}
		})
	}
}

// An identifier that differs only in capitals is the same device to the capture
// backend, so the dock must agree.
func TestDeviceSelectableIgnoresCapitals(t *testing.T) {
	devices := []capture.Device{{ID: "{0.0.1.0}-ABC", Name: "HDMI capture"}}
	if !deviceSelectable(devices, "{0.0.1.0}-abc", "HDMI capture") {
		t.Fatal("an identifier differing only in capitals was reported missing")
	}
}

// The application cannot answer --version at an interactive prompt on Windows,
// so the log page has to carry the version instead.
func TestLogReportsTheVersion(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	c.Capture.Backend = "ffmpeg"
	settings := config.NewSettings("", c)
	server := NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles)
	server.SetLog(nil, "1.2.3")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/log", nil))
	var response struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", response.Version)
	}
}

func TestLogIsServedWhenNoneIsConfigured(t *testing.T) {
	recorder := httptest.NewRecorder()
	ffmpegBackedHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/log", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var response struct {
		Available bool     `json:"available"`
		Lines     []string `json:"lines"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Available {
		t.Fatal("a log was claimed when the server was given none")
	}
	if response.Lines == nil {
		t.Fatal("lines must be an empty list rather than null, so the page can render it")
	}
}

func TestLogPageIsServed(t *testing.T) {
	recorder := httptest.NewRecorder()
	ffmpegBackedHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/log", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Application log") {
		t.Fatal("the log page was not served")
	}
}

// An embedded file has no modification time, so without an ETag a browser is
// given nothing to revalidate against and can keep running a script from before
// an upgrade against an API that has moved on.
func TestAssetsCanBeRevalidated(t *testing.T) {
	handler := ffmpegBackedHandler(t)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/assets/review.js", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", first.Code)
	}
	tag := first.Header().Get("ETag")
	if tag == "" {
		t.Fatal("no ETag was offered for an embedded asset")
	}
	if cache := first.Header().Get("Cache-Control"); cache != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cache)
	}
	repeat := httptest.NewRequest(http.MethodGet, "/assets/review.js", nil)
	repeat.Header.Set("If-None-Match", tag)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, repeat)
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d for an unchanged asset, want 304", second.Code)
	}
	stale := httptest.NewRequest(http.MethodGet, "/assets/review.js", nil)
	stale.Header.Set("If-None-Match", `"0000000000000000"`)
	third := httptest.NewRecorder()
	handler.ServeHTTP(third, stale)
	if third.Code != http.StatusOK || third.Body.Len() == 0 {
		t.Fatalf("status = %d for a changed asset, want 200 with the new file", third.Code)
	}
}

func TestPagesAreNotCachedWithoutChecking(t *testing.T) {
	handler := ffmpegBackedHandler(t)
	for _, path := range []string{"/", "/dock", "/log"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, recorder.Code)
		}
		if cache := recorder.Header().Get("Cache-Control"); cache != "no-cache" {
			t.Fatalf("%s Cache-Control = %q, want no-cache", path, cache)
		}
	}
}
