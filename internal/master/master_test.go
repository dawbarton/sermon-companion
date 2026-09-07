package master

import (
	"context"
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

func TestPrepareMakesRunningExportDurable(t *testing.T) {
	mastering, sessions, session := preparationFixture(t)
	job, err := mastering.Prepare(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.startRevision != stored.Revision || stored.Export == nil || stored.Export.Status != "running" {
		t.Fatalf("prepared export = %#v, revision %d", stored.Export, stored.Revision)
	}
}

func TestPrepareWithoutSegmentsUsesTheEntireRecording(t *testing.T) {
	mastering, sessions, session := preparationFixture(t)
	if _, err := sessions.Update(session.ID, "test.remove_segments", nil, func(s *store.Session) error {
		s.Segments = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	job, err := mastering.Prepare(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(job.segments) != 1 {
		t.Fatalf("export plan has %d segments, want one", len(job.segments))
	}
	segment := job.segments[0]
	if segment.ID != "entire-recording" || segment.StartFrame != 0 || segment.EndFrame == nil || *segment.EndFrame != 48_000 {
		t.Fatalf("whole-recording segment = %#v", segment)
	}
}

func TestPrepareDoesNotOverrideDeliberatelyExcludedSegments(t *testing.T) {
	mastering, sessions, session := preparationFixture(t)
	if _, err := sessions.Update(session.ID, "test.exclude_segment", nil, func(s *store.Session) error {
		s.Segments[0].Include = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := mastering.Prepare(session.ID); err == nil {
		t.Fatal("an excluded segment was replaced by a whole-recording export")
	}
}

func TestPrepareFailureDoesNotLeaveRunningExport(t *testing.T) {
	mastering, sessions, session := preparationFixture(t)
	dir, _ := sessions.SessionDir(session.ID)
	if err := os.Remove(filepath.Join(dir, "audio.flac")); err != nil {
		t.Fatal(err)
	}
	if _, err := mastering.Prepare(session.ID); err == nil {
		t.Fatal("missing recording was accepted")
	}
	stored, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Export != nil {
		t.Fatalf("failed preparation left export state %#v", stored.Export)
	}
}

func TestChangedSessionIsRejectedBeforePublication(t *testing.T) {
	mastering, sessions, session := preparationFixture(t)
	job, err := mastering.Prepare(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Update(session.ID, "test.concurrent_change", nil, func(s *store.Session) error {
		s.Title = "Changed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := mastering.readyToPublish(job, "test.mp3"); err == nil {
		t.Fatal("changed session was accepted for publication")
	}
}

func TestCancelledExportRecordsFailure(t *testing.T) {
	mastering, sessions, session := preparationFixture(t)
	job, err := mastering.Prepare(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mastering.Run(ctx, job); err == nil {
		t.Fatal("cancelled export succeeded")
	}
	stored, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Export == nil || stored.Export.Status != "failed" || stored.Export.Error != "export cancelled because Sermon Companion is closing" {
		t.Fatalf("cancelled export state = %#v", stored.Export)
	}
}

func preparationFixture(t *testing.T) (*Master, *store.Store, *store.Session) {
	t.Helper()
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Create("Preparation", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := sessions.SessionDir(session.ID)
	if err := os.WriteFile(filepath.Join(dir, "audio.flac"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	end, endFrame := 1.0, uint64(48_000)
	if _, err := sessions.Update(session.ID, "test.ready", nil, func(s *store.Session) error {
		s.Status, s.AudioFile, s.Duration = "stopped", "audio.flac", 1
		s.Capture = store.CaptureInfo{SampleRate: 48_000, TotalFrames: endFrame}
		s.Segments = []store.Segment{{ID: "one", Label: "Sermon", End: &end, EndFrame: &endFrame, Include: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return New(config.DefaultConfig(), sessions), sessions, session
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

func TestGroupByLabelKeepsOnePieceOfSpeechTogether(t *testing.T) {
	end := func(v float64) *float64 { return &v }
	segments := []store.Segment{
		{ID: "a", Label: "Sermon", End: end(1)},
		{ID: "b", Label: "Reading", End: end(2)},
		{ID: "c", Label: " sermon ", End: end(3)},
		{ID: "d", Label: "Notices", End: end(4)},
	}
	groups := groupByLabel(segments)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(groups))
	}
	// Groups follow the order their first segment appears in, and a label typed
	// with different capitals or stray spaces is the same piece of speech.
	if groups[0].label != "Sermon" || len(groups[0].segments) != 2 {
		t.Fatalf("first group = %q with %d segments", groups[0].label, len(groups[0].segments))
	}
	if groups[0].segments[0].ID != "a" || groups[0].segments[1].ID != "c" {
		t.Fatalf("sermon group holds %q and %q", groups[0].segments[0].ID, groups[0].segments[1].ID)
	}
	if groups[1].label != "Reading" || groups[2].label != "Notices" {
		t.Fatalf("later groups = %q, %q", groups[1].label, groups[2].label)
	}
	if labelKey(segments[2]) != labelKey(segments[0]) {
		t.Fatal("label keys differ by capitals or spacing")
	}
}
