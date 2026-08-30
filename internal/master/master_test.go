package master

import (
	"testing"
	"time"

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

func TestExportSegmentsFiltersAndSorts(t *testing.T) {
	end10, end20, end30 := 10.0, 20.0, 30.0
	now := time.Now()
	segments := []store.Segment{
		{ID: "later", Start: 20, End: &end30, Include: true, CreatedAt: now},
		{ID: "excluded", Start: 10, End: &end20, Include: false, CreatedAt: now},
		{ID: "archived", Start: 10, End: &end20, Include: true, Archived: true, CreatedAt: now},
		{ID: "open", Start: 5, Include: true, CreatedAt: now},
		{ID: "first", Start: 0, End: &end10, Include: true, CreatedAt: now},
	}
	got := exportSegments(segments)
	if len(got) != 2 || got[0].ID != "first" || got[1].ID != "later" {
		t.Fatalf("unexpected export order: %#v", got)
	}
}
