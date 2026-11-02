package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func TestStopRecordingStopsWhateverIsRecording(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	c := config.DefaultConfig()
	c.FFmpeg = ffmpeg
	c.Capture.Backend, c.Capture.Driver, c.Capture.Device = "ffmpeg", "lavfi", "sine=frequency=440:sample_rate=48000"
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings := config.NewSettings("", c)
	server := NewServer(settings, sessions, capture.New(settings, sessions), master.New(c, sessions), StaticFiles)
	t.Cleanup(func() {
		// Stopping starts the waveform, which must finish before the
		// temporary folder is removed.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	})
	handler := server.Handler()
	post := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		return response
	}

	// Nothing is recording yet.
	if response := post("/api/stop-recording"); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "nothing is being recorded") {
		t.Fatalf("stop with nothing recording: %d %s", response.Code, response.Body.String())
	}
	if response := post("/api/sessions"); response.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", response.Code, response.Body.String())
	}
	// A second start, as when OBS's recording starts during the stream, leaves
	// the recording alone.
	if response := post("/api/sessions"); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "already in progress") {
		t.Fatalf("second start: %d %s", response.Code, response.Body.String())
	}
	time.Sleep(500 * time.Millisecond)
	response := post("/api/stop-recording")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"stopped"`) {
		t.Fatalf("stop: %d %s", response.Code, response.Body.String())
	}
	if _, _, _, active := server.capture.Active(); active {
		t.Fatal("still recording after the stop")
	}
}
