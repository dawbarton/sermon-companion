// Tests for reading a session for upload: it must find a finished MP3, and must
// refuse, without writing anything, every session it cannot be sure of.
package publish

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/store"
)

// writeSession makes a session folder holding a completed export, after mutate
// has had the chance to spoil it.
func writeSession(t *testing.T, mutate func(*store.Session)) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "s_test01")
	if err := os.MkdirAll(filepath.Join(dir, "exports"), 0o755); err != nil {
		t.Fatal(err)
	}
	ended := time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)
	session := store.Session{
		SchemaVersion: store.SchemaVersion,
		ID:            "s_test01",
		Title:         "Morning service",
		Church:        "Emmanuel Bishopston",
		StartedAt:     time.Date(2026, 9, 13, 9, 30, 0, 0, time.UTC),
		Export:        &store.ExportInfo{Status: "completed", EndedAt: &ended, Output: "exports/2026-09-13-Emmanuel-Bishopston.mp3"},
	}
	if mutate != nil {
		mutate(&session)
	}
	data, _ := json.Marshal(session)
	if err := os.WriteFile(filepath.Join(dir, snapshotName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exports", "2026-09-13-Emmanuel-Bishopston.mp3"), []byte("ID3 audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadExportFindsTheFinishedMP3(t *testing.T) {
	dir := writeSession(t, nil)
	export, err := LoadExport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if export.Name != "2026-09-13-Emmanuel-Bishopston.mp3" || export.Size != 9 || export.Session.Church != "Emmanuel Bishopston" {
		t.Errorf("export = %+v", export)
	}
}

func TestLoadExportRefusesWhatItCannotBeSureOf(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*store.Session)
		says   string
	}{
		{"no export", func(s *store.Session) { s.Export = nil }, "no MP3 has been created"},
		{"stale export", func(s *store.Session) { s.Export.Status = "stale" }, "out of date"},
		{"export running", func(s *store.Session) { s.Export.Status = "running" }, "still being created"},
		{"export failed", func(s *store.Session) { s.Export.Status = "failed"; s.Export.Error = "disk full" }, "disk full"},
		{"older schema", func(s *store.Session) { s.SchemaVersion = store.SchemaVersion - 1 }, "older format"},
		{"no church", func(s *store.Session) { s.Church = " " }, "no church"},
		{"wrong folder", func(s *store.Session) { s.ID = "s_other" }, "belongs to session"},
		{"escaping path", func(s *store.Session) { s.Export.Output = "../../secret.mp3" }, "not inside the session folder"},
		{"absolute path", func(s *store.Session) { s.Export.Output = "/etc/passwd" }, "not inside the session folder"},
		{"missing file", func(s *store.Session) { s.Export.Output = "exports/gone.mp3" }, "cannot be read"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadExport(writeSession(t, test.mutate))
			if err == nil || !strings.Contains(err.Error(), test.says) {
				t.Errorf("error = %v, want one saying %q", err, test.says)
			}
		})
	}
}

func TestLoadExportRefusesAnInterruptedSaveAndLeavesItAlone(t *testing.T) {
	dir := writeSession(t, nil)
	staged := filepath.Join(dir, stagedSnapshotName)
	if err := os.WriteFile(staged, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadExport(dir); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("error = %v", err)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Error("the staged snapshot must be left for Sermon Companion's own recovery")
	}
}

func TestLoadExportRefusesAFolderThatIsNotASession(t *testing.T) {
	if _, err := LoadExport(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a Sermon Companion session folder") {
		t.Errorf("error = %v", err)
	}
}
