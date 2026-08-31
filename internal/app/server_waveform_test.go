package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

// The review page is opened straight after the service, so the waveform has to
// be built by then rather than decoding the whole recording underneath the
// operator's first attempt to listen to a segment.
func TestStoppingASessionBuildsTheWaveform(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("FFprobe is not installed")
	}
	c := config.DefaultConfig()
	c.FFmpeg, c.FFprobe = ffmpeg, ffprobe
	c.Capture.Backend, c.Capture.Driver, c.Capture.Device = "ffmpeg", "lavfi", "sine=frequency=440:sample_rate=48000"
	dataDir := t.TempDir()
	sessions, err := store.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(c, sessions, capture.New(c, sessions), master.New(c, sessions), StaticFiles).Handler()

	started := requestSession(t, handler, http.MethodPost, "/api/sessions", `{"title":"Waveform test"}`)
	time.Sleep(400 * time.Millisecond)
	stopped := requestSession(t, handler, http.MethodPost, "/api/sessions/"+started.ID+"/stop", `{}`)
	if stopped.Status != "stopped" {
		t.Fatalf("session was not stopped: %#v", stopped)
	}

	dir, err := sessions.SessionDir(stopped.ID)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "waveform-20pps.json")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if info, err := os.Stat(cache); err == nil && info.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stopping the recording did not build the waveform")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The cached envelope is what the review page receives, so it must describe
	// the recording rather than merely exist.
	request := httptest.NewRequest(http.MethodGet, "/api/sessions/"+stopped.ID+"/waveform", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("waveform request failed: %d %s", recorder.Code, strings.TrimSpace(recorder.Body.String()))
	}
	var envelope struct {
		PointsPerSecond int     `json:"pointsPerSecond"`
		Duration        float64 `json:"durationSeconds"`
		PeaksBase64     string  `json:"peaksBase64"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.PointsPerSecond != 20 || envelope.Duration <= 0 || envelope.PeaksBase64 == "" {
		t.Fatalf("unexpected waveform envelope: %#v", envelope)
	}
}
