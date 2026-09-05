package store

import (
	"fmt"
	"sort"
)

// SnapSegmentBoundaries aligns a user-edited boundary with an existing
// neighbour when their difference is below the supplied display precision in
// frames. Frame positions are canonical; the seconds fields are only display
// values and are synchronised by the caller after snapping.
func SnapSegmentBoundaries(all []Segment, segmentID string, toleranceFrames uint64) {
	var candidate *Segment
	for index := range all {
		if all[index].ID == segmentID {
			candidate = &all[index]
			break
		}
	}
	if candidate == nil || candidate.Archived || candidate.EndFrame == nil {
		return
	}
	for index := range all {
		other := &all[index]
		if other.ID == segmentID || other.Archived || other.EndFrame == nil {
			continue
		}
		if frameDistance(candidate.StartFrame, *other.EndFrame) <= toleranceFrames {
			candidate.StartFrame = *other.EndFrame
		}
		if frameDistance(*candidate.EndFrame, other.StartFrame) <= toleranceFrames {
			*candidate.EndFrame = other.StartFrame
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
		if !segment.Archived && segment.EndFrame != nil {
			if *segment.EndFrame <= segment.StartFrame {
				return fmt.Errorf("segment %q must end after it starts", segment.Label)
			}
			segments = append(segments, segment)
		}
	}
	sort.SliceStable(segments, func(i, j int) bool { return segments[i].StartFrame < segments[j].StartFrame })
	for index := 1; index < len(segments); index++ {
		previous, current := segments[index-1], segments[index]
		if current.StartFrame < *previous.EndFrame {
			return fmt.Errorf("segment %q overlaps %q; segment boundaries may touch but not cross", current.Label, previous.Label)
		}
	}
	return nil
}

func frameDistance(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}
