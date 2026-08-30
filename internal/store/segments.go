package store

import (
	"fmt"
	"math"
	"sort"
)

// SnapSegmentBoundaries aligns a user-edited boundary with an existing
// neighbour when their difference is below the supplied display precision.
// This preserves exact, non-overlapping joins when a UI rounds stored times.
func SnapSegmentBoundaries(all []Segment, segmentID string, tolerance float64) {
	var candidate *Segment
	for index := range all {
		if all[index].ID == segmentID {
			candidate = &all[index]
			break
		}
	}
	if candidate == nil || candidate.Archived || candidate.End == nil {
		return
	}
	for index := range all {
		other := &all[index]
		if other.ID == segmentID || other.Archived || other.End == nil {
			continue
		}
		if math.Abs(candidate.Start-*other.End) <= tolerance {
			candidate.Start = *other.End
		}
		if math.Abs(*candidate.End-other.Start) <= tolerance {
			*candidate.End = other.Start
		}
	}
}

// ValidateNoSegmentOverlaps permits touching boundaries, but rejects any
// duplicated interval between complete, non-archived segments. Excluded
// segments remain constrained so re-enabling one cannot silently duplicate
// audio in a later export.
func ValidateNoSegmentOverlaps(all []Segment) error {
	segments := make([]Segment, 0, len(all))
	for _, segment := range all {
		if !segment.Archived && segment.End != nil {
			segments = append(segments, segment)
		}
	}
	sort.SliceStable(segments, func(i, j int) bool { return segments[i].Start < segments[j].Start })
	for index := 1; index < len(segments); index++ {
		previous, current := segments[index-1], segments[index]
		if current.Start < *previous.End-1e-6 {
			return fmt.Errorf("segment %q overlaps %q; segment boundaries may touch but not cross", current.Label, previous.Label)
		}
	}
	return nil
}
