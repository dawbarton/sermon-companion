package capture

import (
	"time"

	"github.com/dawbarton/sermon-companion/internal/store"
)

type captureResult struct {
	Info     store.CaptureInfo
	PartPath string
	Error    error
}

type activeCapture interface {
	PositionAt(time.Time) Position
	Latest() Position
	Info() store.CaptureInfo
	Stop()
	Done() <-chan captureResult
}
