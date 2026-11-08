package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func guardedHandler(t *testing.T, listen string) http.Handler {
	t.Helper()
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	c.Listen = listen
	settings := config.NewSettings("", c)
	return NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles).Handler()
}

func send(handler http.Handler, method, target, origin, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader("{}"))
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// A page on another website can send a form-style POST to the loopback address
// without asking first. It must not be able to stop the recording.
func TestRequestsFromOtherWebsitesAreRefused(t *testing.T) {
	handler := guardedHandler(t, "127.0.0.1:8765")
	foreign := send(handler, http.MethodPost, "http://127.0.0.1:8765/api/stop-recording", "https://example.org", "text/plain")
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("a request from another website: %d %s", foreign.Code, foreign.Body.String())
	}
	for _, origin := range []string{"null", "http://127.0.0.1:9999", "https://127.0.0.1:8765"} {
		if response := send(handler, http.MethodPost, "http://127.0.0.1:8765/api/stop-recording", origin, "application/json"); response.Code != http.StatusForbidden {
			t.Fatalf("origin %s: %d %s", origin, response.Code, response.Body.String())
		}
	}
	// The dock and review pages send their own origin; the OBS script sends none.
	for _, origin := range []string{"http://127.0.0.1:8765", ""} {
		response := send(handler, http.MethodPost, "http://127.0.0.1:8765/api/stop-recording", origin, "application/json")
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "nothing is being recorded") {
			t.Fatalf("origin %q was not let through: %d %s", origin, response.Code, response.Body.String())
		}
	}
	// Reading needs no origin check: without CORS headers, another site's page
	// cannot read the answer.
	if response := send(handler, http.MethodGet, "http://127.0.0.1:8765/api/status", "https://example.org", ""); response.Code != http.StatusOK {
		t.Fatalf("status read: %d %s", response.Code, response.Body.String())
	}
}

// A page whose own name is made to resolve to 127.0.0.1 after loading is the
// same origin as itself, so it is refused by the name it addressed.
func TestRequestsAddressedToAnotherNameAreRefused(t *testing.T) {
	handler := guardedHandler(t, "127.0.0.1:8765")
	if response := send(handler, http.MethodGet, "http://attacker.example:8765/api/sessions", "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("a rebound name: %d %s", response.Code, response.Body.String())
	}
	if response := send(handler, http.MethodPost, "http://attacker.example:8765/api/stop-recording", "http://attacker.example:8765", "text/plain"); response.Code != http.StatusForbidden {
		t.Fatalf("a rebound name posting to itself: %d %s", response.Code, response.Body.String())
	}
	for _, target := range []string{"http://127.0.0.1:8765/api/status", "http://localhost:8765/api/status", "http://LOCALHOST:8765/api/status", "http://[::1]:8765/api/status", "http://192.168.1.20:8765/api/status"} {
		if response := send(handler, http.MethodGet, target, "", ""); response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", target, response.Code, response.Body.String())
		}
	}
	// A name given in the listen setting is the operator's own choice.
	named := guardedHandler(t, "church-pc.local:8765")
	if response := send(named, http.MethodGet, "http://church-pc.local:8765/api/status", "", ""); response.Code != http.StatusOK {
		t.Fatalf("the configured name: %d %s", response.Code, response.Body.String())
	}
}
