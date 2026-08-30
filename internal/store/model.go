package store

import "time"

const SchemaVersion = 1

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
	Revision      int64       `json:"revision"`
	Segments      []Segment   `json:"segments"`
	Markers       []Marker    `json:"markers"`
	Export        *ExportInfo `json:"export,omitempty"`
	Error         string      `json:"error,omitempty"`
}

type Segment struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label"`
	Start     float64   `json:"startSeconds"`
	End       *float64  `json:"endSeconds,omitempty"`
	Include   bool      `json:"include"`
	Archived  bool      `json:"archived,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Marker struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label"`
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
