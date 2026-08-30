package store

import "testing"

func TestValidateNoSegmentOverlaps(t *testing.T) {
	end10, end20, end30 := 10.0, 20.0, 30.0
	touching := []Segment{{ID: "one", Label: "One", Start: 0, End: &end10}, {ID: "two", Label: "Two", Start: 10, End: &end20}}
	if err := ValidateNoSegmentOverlaps(touching); err != nil {
		t.Fatalf("touching segments rejected: %v", err)
	}
	overlapping := []Segment{{ID: "one", Label: "One", Start: 0, End: &end20}, {ID: "two", Label: "Two", Start: 10, End: &end30}}
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
	previousEnd, editedEnd := 10.47, 20.0
	segments := []Segment{
		{ID: "previous", Label: "Previous", Start: 0, End: &previousEnd},
		{ID: "edited", Label: "Edited", Start: 10.5, End: &editedEnd},
	}
	SnapSegmentBoundaries(segments, "edited", 0.051)
	if segments[1].Start != previousEnd {
		t.Fatalf("start = %v, want exact neighbour %v", segments[1].Start, previousEnd)
	}
	if err := ValidateNoSegmentOverlaps(segments); err != nil {
		t.Fatalf("snapped segments overlap: %v", err)
	}
}
