package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func configFileServer(t *testing.T, path string) *Server {
	t.Helper()
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	settings := config.NewSettings(path, c)
	return NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles)
}

func TestOpenConfigFileOpensTheFileInUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := configFileServer(t, path)
	opened := ""
	server.openEditor = func(p string) error { opened = p; return nil }

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/open-config-file", strings.NewReader(`{}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if opened != path {
		t.Fatalf("opened %q, want %q", opened, path)
	}
	if !strings.Contains(response.Body.String(), path) {
		t.Fatalf("response does not name the file the operator was sent to: %s", response.Body.String())
	}
}

// An operator who cannot be shown the file has to be told where it is, so a
// missing file is reported rather than an editor being opened on nothing.
func TestOpenConfigFileReportsAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	server := configFileServer(t, path)
	server.openEditor = func(string) error {
		t.Error("an editor was opened for a configuration file that does not exist")
		return nil
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/open-config-file", strings.NewReader(`{}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), path) {
		t.Fatalf("error does not name the file: %s", response.Body.String())
	}
}

// In-memory settings, which the tests and any future embedded use rely on, have
// no file to edit.
func TestOpenConfigFileWithoutAConfigurationFile(t *testing.T) {
	server := configFileServer(t, "")
	server.openEditor = func(string) error {
		t.Error("an editor was opened without a configuration file")
		return nil
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/open-config-file", strings.NewReader(`{}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
