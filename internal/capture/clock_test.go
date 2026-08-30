package capture

import (
	"testing"
	"time"
)

func TestFrameClockInterpolatesOperatorTimeInAudioFrames(t *testing.T) {
	clock := newFrameClock(48_000)
	start := time.Now()
	clock.accept(480, start.Add(10*time.Millisecond))
	clock.accept(480, start.Add(20*time.Millisecond))
	position := clock.positionAt(start.Add(15*time.Millisecond), time.Millisecond)
	if position.Frames != 720 || position.Seconds != 0.015 || position.Estimated {
		t.Fatalf("unexpected position: %#v", position)
	}
}

func TestFrameClockUsesLatestFrameWhenFollowingCallbackDoesNotArrive(t *testing.T) {
	clock := newFrameClock(48_000)
	start := time.Now()
	clock.accept(480, start)
	position := clock.positionAt(start.Add(time.Second), time.Millisecond)
	if position.Frames != 480 || !position.Estimated {
		t.Fatalf("unexpected position: %#v", position)
	}
}

func TestFrameClockKeepsEncoderReportsMonotonic(t *testing.T) {
	clock := newFrameClock(48_000)
	start := time.Now()
	clock.acceptTotal(48_000, start)
	clock.acceptTotal(24_000, start.Add(200*time.Millisecond))
	if position := clock.latest(); position.Frames != 48_000 {
		t.Fatalf("unexpected position: %#v", position)
	}
}

func TestFrameClockClampsRequestsOlderThanEveryAnchor(t *testing.T) {
	clock := newFrameClock(48_000)
	start := time.Now()
	clock.accept(480, start)
	clock.accept(480, start.Add(10*time.Millisecond))
	position := clock.positionAt(start.Add(-time.Second), time.Millisecond)
	if position.Frames != 480 || !position.Estimated {
		t.Fatalf("unexpected position: %#v", position)
	}
}
