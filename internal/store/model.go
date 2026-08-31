package store

import "time"

const SchemaVersion = 2

type Session struct {
	SchemaVersion int         `json:"schemaVersion"`
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Church        string      `json:"church"`
	Status        string      `json:"status"`
	StartedAt     time.Time   `json:"startedAt"`
	EndedAt       *time.Time  `json:"endedAt,omitempty"`
	Duration      float64     `json:"durationSeconds"`
	AudioFile     string      `json:"audioFile"`
	Capture       CaptureInfo `json:"capture"`
	Revision      int64       `json:"revision"`
	Segments      []Segment   `json:"segments"`
	Markers       []Marker    `json:"markers"`
	// GapSeconds is the silence placed between segments when this service is
	// exported. Nil takes the configured default, so a service recorded before
	// the setting existed still exports with the operator's usual gap.
	GapSeconds *float64    `json:"gapSeconds,omitempty"`
	Export     *ExportInfo `json:"export,omitempty"`
	Error      string      `json:"error,omitempty"`
}

type CaptureInfo struct {
	Backend              string  `json:"backend"`
	DeviceID             string  `json:"deviceId,omitempty"`
	DeviceName           string  `json:"deviceName,omitempty"`
	SampleRate           int     `json:"sampleRate"`
	Channels             int     `json:"channels"`
	SampleFormat         string  `json:"sampleFormat"`
	TotalFrames          uint64  `json:"totalFrames"`
	WrittenFrames        uint64  `json:"writtenFrames"`
	DroppedFrames        uint64  `json:"droppedFrames"`
	CallbackCount        uint64  `json:"callbackCount"`
	QueueHighWaterFrames uint64  `json:"queueHighWaterFrames"`
	WallDuration         float64 `json:"wallDurationSeconds"`
	AudioDuration        float64 `json:"audioDurationSeconds"`
	ClockDriftPPM        float64 `json:"clockDriftPPM"`
}

type Segment struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Label      string    `json:"label"`
	StartFrame uint64    `json:"startFrame"`
	EndFrame   *uint64   `json:"endFrame,omitempty"`
	Start      float64   `json:"startSeconds"`
	End        *float64  `json:"endSeconds,omitempty"`
	Include    bool      `json:"include"`
	Archived   bool      `json:"archived,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Marker struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label"`
	AtFrame   uint64    `json:"atFrame"`
	At        float64   `json:"atSeconds"`
	CreatedAt time.Time `json:"createdAt"`
}

type ExportInfo struct {
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	Output    string     `json:"output,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type Event struct {
	Sequence  int64       `json:"sequence"`
	At        time.Time   `json:"at"`
	Type      string      `json:"type"`
	SessionID string      `json:"sessionId"`
	Payload   interface{} `json:"payload,omitempty"`
}
