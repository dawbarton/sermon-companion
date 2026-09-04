package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func TestDeletingAServiceRemovesItsFolderAndRefusesWhileRecording(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Old service", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	settings := config.NewSettings("", c)
	handler := NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles).Handler()
	dir, err := sessions.SessionDir(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	// A service is created in the "starting" state, and the recording it holds
	// is the only copy, so it cannot be deleted out from under the capture.
	requestError(t, handler, http.MethodDelete, "/api/sessions/"+session.ID, "", http.StatusConflict, "stop the recording")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("refused deletion still removed the session folder: %v", err)
	}

	if _, err := sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error { s.Status = "stopped"; return nil }); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/sessions/"+session.ID, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session folder still exists: %v", err)
	}

	requestError(t, handler, http.MethodDelete, "/api/sessions/"+session.ID, "", http.StatusNotFound, "not found")
}
