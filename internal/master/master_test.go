package master

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

func TestParseMeasurement(t *testing.T) {
	output := []byte(`ordinary FFmpeg output
{
  "input_i": "-22.31",
  "input_tp": "-3.20",
  "input_lra": "5.10",
  "input_thresh": "-32.40",
  "target_offset": "0.12"
}
more output`)
	got, err := parseMeasurement(output)
	if err != nil {
		t.Fatal(err)
	}
	if got.InputI != "-22.31" || got.TargetOffset != "0.12" {
		t.Fatalf("unexpected measurement: %#v", got)
	}
}

func TestOutputNameUsesServiceDateAndSafeChurchName(t *testing.T) {
	session := &store.Session{
		StartedAt: time.Date(2026, 8, 30, 10, 0, 0, 0, time.Local),
		Church:    " St Mary's Church ",
	}
	if got := outputName(session); got != "2026-08-30-St-Marys-Church.mp3" {
		t.Fatalf("outputName() = %q", got)
	}
	if got := filenamePart("Christ Church & St Peter's"); got != "Christ-Church-St-Peters" {
		t.Fatalf("filenamePart() = %q", got)
	}
}

func TestPublishOutputRetainsPreviousExport(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "2026-08-30-Church.mp3")
	tempPath := filepath.Join(dir, "new.mp3")
	if err := os.WriteFile(finalPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tempPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 30, 12, 34, 56, 123, time.UTC)
	if err := publishOutput(tempPath, finalPath, started); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(finalPath); err != nil || string(data) != "new" {
		t.Fatalf("current export = %q, %v", data, err)
	}
	previous := filepath.Join(dir, "previous", "2026-08-30-Church-20260830-123456.000000123.mp3")
	if data, err := os.ReadFile(previous); err != nil || string(data) != "old" {
		t.Fatalf("previous export = %q, %v", data, err)
	}
}

func TestExportSegmentsFiltersAndSorts(t *testing.T) {
	end10, end20, end30 := 10.0, 20.0, 30.0
	endFrame10, endFrame20, endFrame30 := uint64(480_000), uint64(960_000), uint64(1_440_000)
	now := time.Now()
	segments := []store.Segment{
		{ID: "later", StartFrame: 960_000, EndFrame: &endFrame30, Start: 20, End: &end30, Include: true, CreatedAt: now},
		{ID: "excluded", StartFrame: 480_000, EndFrame: &endFrame20, Start: 10, End: &end20, Include: false, CreatedAt: now},
		{ID: "archived", StartFrame: 480_000, EndFrame: &endFrame20, Start: 10, End: &end20, Include: true, Archived: true, CreatedAt: now},
		{ID: "open", Start: 5, Include: true, CreatedAt: now},
		{ID: "first", Start: 0, EndFrame: &endFrame10, End: &end10, Include: true, CreatedAt: now},
	}
	got := exportSegments(segments)
	if len(got) != 2 || got[0].ID != "first" || got[1].ID != "later" {
		t.Fatalf("unexpected export order: %#v", got)
	}
}

func TestPeakLimiterUsesTheConfiguredCeiling(t *testing.T) {
	mastering := config.DefaultConfig().Master
	if got := peakLimiter(mastering); got != "alimiter=limit=0.891251:level=0" {
		t.Fatalf("peakLimiter() = %q", got)
	}
	full := 0.0
	mastering.PeakLimitDB = &full
	if got := peakLimiter(mastering); got != "alimiter=limit=1.000000:level=0" {
		t.Fatalf("peakLimiter() at full scale = %q", got)
	}
}

func TestGapPrefersTheServiceOverTheConfiguredDefault(t *testing.T) {
	mastering := config.DefaultConfig().Master
	if got := gapBetweenSegments(mastering, &store.Session{}); got != 2 {
		t.Fatalf("gap without a service setting = %g", got)
	}
	five, silly := 5.0, 600.0
	if got := gapBetweenSegments(mastering, &store.Session{GapSeconds: &five}); got != 5 {
		t.Fatalf("gap set on the service = %g", got)
	}
	if got := gapBetweenSegments(mastering, &store.Session{GapSeconds: &silly}); got != config.MaximumGapSeconds {
		t.Fatalf("unclamped gap = %g", got)
	}
}
