package capture

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

type controlledCapture struct {
	done chan captureResult
	once sync.Once
	info store.CaptureInfo
}

func newControlledCapture() *controlledCapture {
	return &controlledCapture{done: make(chan captureResult, 1), info: store.CaptureInfo{Backend: "test", SampleRate: 48_000, Channels: 2, SampleFormat: "s16le"}}
}

func (c *controlledCapture) PositionAt(time.Time) Position { return Position{} }
func (c *controlledCapture) Latest() Position              { return Position{} }
func (c *controlledCapture) Info() store.CaptureInfo       { return c.info }
func (c *controlledCapture) Stop()                         { c.finish(errors.New("stopped")) }
func (c *controlledCapture) Abort() error                  { c.Stop(); return nil }
func (c *controlledCapture) Done() <-chan captureResult    { return c.done }
func (c *controlledCapture) finish(err error) {
	c.once.Do(func() {
		c.done <- captureResult{Info: c.info, Error: err}
		close(c.done)
	})
}

func TestFailedCaptureStartedSnapshotDoesNotWedgeManager(t *testing.T) {
	root := t.TempDir()
	sessions, err := store.New(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := New(config.NewSettings("", config.DefaultConfig()), sessions)
	active := newControlledCapture()
	var snapshotPath string
	var original []byte
	manager.startNative = func(_ config.Config, partPath string, _ *os.File) (activeCapture, error) {
		snapshotPath = filepath.Join(filepath.Dir(partPath), "session.json")
		original = corruptSnapshotSchema(t, snapshotPath)
		return active, nil
	}

	if _, err := manager.Start("Failure test"); err == nil {
		t.Fatal("capture start succeeded after its snapshot became unwritable")
	}
	if _, _, _, running := manager.Active(); running {
		t.Fatal("manager retained a failed active capture")
	}
	if !strings.Contains(manager.LastError(), "could not start") {
		t.Fatalf("last error = %q", manager.LastError())
	}
	if err := os.WriteFile(snapshotPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureCompletionPersistenceFailureRemainsVisible(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := New(config.NewSettings("", config.DefaultConfig()), sessions)
	active := newControlledCapture()
	manager.startNative = func(config.Config, string, *os.File) (activeCapture, error) { return active, nil }
	session, err := manager.Start("Failure test")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	snapshotPath := filepath.Join(dir, "session.json")
	original := corruptSnapshotSchema(t, snapshotPath)
	active.finish(errors.New("encoder stopped"))

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, _, _, running := manager.Active(); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capture completion did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(manager.LastError(), "could not be saved") {
		t.Fatalf("last error = %q", manager.LastError())
	}
	if err := os.WriteFile(snapshotPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedCaptureReportsMissingAudio(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Interrupted", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Update(session.ID, "test.recording", nil, func(s *store.Session) error {
		s.Status = "recording"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := New(config.NewSettings("", config.DefaultConfig()), sessions)
	if err := manager.RecoverInterrupted(); err != nil {
		t.Fatal(err)
	}
	recovered, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "interrupted" || !strings.Contains(recovered.Error, "could not be found") {
		t.Fatalf("recovered session = %#v", recovered)
	}
}

func corruptSnapshotSchema(t *testing.T, path string) []byte {
	t.Helper()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(original, &document); err != nil {
		t.Fatal(err)
	}
	document["schemaVersion"] = float64(store.SchemaVersion + 1)
	corrupt, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	return original
}
