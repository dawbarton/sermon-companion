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

func TestOpenReviewPageUsesTheLoopbackAddress(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	c.Listen = ":8765"
	settings := config.NewSettings("", c)
	server := NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles)
	opened := ""
	server.openLink = func(url string) error { opened = url; return nil }

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/open-review-page", strings.NewReader("{}")))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if opened != "http://127.0.0.1:8765/" {
		t.Fatalf("opened %q", opened)
	}
}

func TestLocalURLKeepsAnExplicitHost(t *testing.T) {
	for listen, want := range map[string]string{
		"127.0.0.1:8765": "http://127.0.0.1:8765/",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/",
		"localhost:8765": "http://localhost:8765/",
	} {
		if got := LocalURL(listen); got != want {
			t.Errorf("LocalURL(%q) = %q, want %q", listen, got, want)
		}
	}
}
