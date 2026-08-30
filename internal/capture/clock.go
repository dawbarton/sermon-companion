package capture

import (
	"math"
	"sync"
	"time"
)

type Position struct {
	Frames    uint64
	Seconds   float64
	Estimated bool
}

type clockAnchor struct {
	at     time.Time
	frames uint64
}

// frameClock maps local monotonic time to the frame positions accepted from
// the audio callback. Keeping the mapping in the audio sample domain prevents
// device-clock drift from accumulating in live segment timestamps.
type frameClock struct {
	mu         sync.Mutex
	sampleRate int
	anchors    []clockAnchor
	changed    chan struct{}
}

func newFrameClock(sampleRate int) *frameClock {
	return &frameClock{sampleRate: sampleRate, changed: make(chan struct{})}
}

func (c *frameClock) accept(frameCount uint32, at time.Time) Position {
	c.mu.Lock()
	defer c.mu.Unlock()
	frames := uint64(frameCount)
	if len(c.anchors) > 0 {
		frames += c.anchors[len(c.anchors)-1].frames
	}
	return c.anchorLocked(frames, at)
}

// acceptTotal records an absolute frame position reported by an encoder rather
// than a delta from a device callback. A report that would move the clock
// backwards is held at the previous position so interpolation never reverses.
func (c *frameClock) acceptTotal(frames uint64, at time.Time) Position {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.anchors) > 0 && frames < c.anchors[len(c.anchors)-1].frames {
		frames = c.anchors[len(c.anchors)-1].frames
	}
	return c.anchorLocked(frames, at)
}

func (c *frameClock) anchorLocked(frames uint64, at time.Time) Position {
	c.anchors = append(c.anchors, clockAnchor{at: at, frames: frames})
	if len(c.anchors) > 16 {
		copy(c.anchors, c.anchors[len(c.anchors)-16:])
		c.anchors = c.anchors[:16]
	}
	close(c.changed)
	c.changed = make(chan struct{})
	return c.position(frames, false)
}

func (c *frameClock) latest() Position {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.anchors) == 0 {
		return Position{Estimated: true}
	}
	return c.position(c.anchors[len(c.anchors)-1].frames, false)
}

// positionAt waits briefly for the callback following the requested time, then
// interpolates between audio-frame anchors. The bounded wait is normally one
// callback period and avoids a growing wall-clock/audio-clock discrepancy.
func (c *frameClock) positionAt(at time.Time, wait time.Duration) Position {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		if position, ok := c.interpolateLocked(at); ok {
			c.mu.Unlock()
			return position
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-deadline.C:
			position := c.latest()
			position.Estimated = true
			return position
		}
	}
}

func (c *frameClock) interpolateLocked(at time.Time) (Position, bool) {
	if len(c.anchors) == 0 {
		return Position{}, false
	}
	if !c.anchors[len(c.anchors)-1].at.Before(at) {
		// A request older than every retained anchor cannot be interpolated;
		// extrapolating backwards would run the frame count below zero.
		if at.Before(c.anchors[0].at) {
			return c.position(c.anchors[0].frames, true), true
		}
		for index := 1; index < len(c.anchors); index++ {
			before, after := c.anchors[index-1], c.anchors[index]
			if after.at.Before(at) {
				continue
			}
			span := after.at.Sub(before.at)
			if span <= 0 {
				return c.position(after.frames, true), true
			}
			fraction := float64(at.Sub(before.at)) / float64(span)
			frames := float64(before.frames) + fraction*float64(after.frames-before.frames)
			return c.position(uint64(math.Round(frames)), false), true
		}
		return c.position(c.anchors[0].frames, true), true
	}
	return Position{}, false
}

func (c *frameClock) position(frames uint64, estimated bool) Position {
	seconds := 0.0
	if c.sampleRate > 0 {
		seconds = float64(frames) / float64(c.sampleRate)
	}
	return Position{Frames: frames, Seconds: seconds, Estimated: estimated}
}
