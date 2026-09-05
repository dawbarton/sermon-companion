package store

import "testing"

func TestValidateNoSegmentOverlaps(t *testing.T) {
	end10, end20, end30 := uint64(10), uint64(20), uint64(30)
	touching := []Segment{{ID: "one", Label: "One", StartFrame: 0, EndFrame: &end10}, {ID: "two", Label: "Two", StartFrame: 10, EndFrame: &end20}}
	if err := ValidateNoSegmentOverlaps(touching); err != nil {
		t.Fatalf("touching segments rejected: %v", err)
	}
	overlapping := []Segment{{ID: "one", Label: "One", StartFrame: 0, EndFrame: &end20}, {ID: "two", Label: "Two", StartFrame: 10, EndFrame: &end30}}
	if err := ValidateNoSegmentOverlaps(overlapping); err == nil {
		t.Fatal("overlapping segments accepted")
	}
	overlapping[1].Archived = true
	if err := ValidateNoSegmentOverlaps(overlapping); err != nil {
		t.Fatalf("archived segment constrained the timeline: %v", err)
	}
	overlapping[1].Archived, overlapping[1].Include = false, false
	if err := ValidateNoSegmentOverlaps(overlapping); err == nil {
		t.Fatal("excluded overlapping segment was accepted")
	}
}

func TestSnapSegmentBoundariesPreservesExactJoinsAfterDisplayRounding(t *testing.T) {
	previousEnd, editedEnd := uint64(10_470), uint64(20_000)
	segments := []Segment{
		{ID: "previous", Label: "Previous", StartFrame: 0, EndFrame: &previousEnd},
		{ID: "edited", Label: "Edited", StartFrame: 10_500, EndFrame: &editedEnd},
	}
	SnapSegmentBoundaries(segments, "edited", 51)
	if segments[1].StartFrame != previousEnd {
		t.Fatalf("start frame = %v, want exact neighbour %v", segments[1].StartFrame, previousEnd)
	}
	if err := ValidateNoSegmentOverlaps(segments); err != nil {
		t.Fatalf("snapped segments overlap: %v", err)
	}
}
