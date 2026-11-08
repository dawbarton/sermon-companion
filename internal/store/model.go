package store

import "time"

// SchemaVersion 3 added the talk's details for the church website and the
// record of uploading it. An older application would drop both when it next
// saved the session, losing the record of what is on the website, so the
// version is raised to make it refuse the session instead.
const SchemaVersion = 3

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
	// Talk is what the church website is told about the service. It is nil
	// until the upload page is first opened. None of it goes into the MP3, so
	// changing it never makes an export stale.
	Talk *Talk `json:"talk,omitempty"`
	// Publish records the upload of the MP3 to the church website.
	Publish *PublishInfo `json:"publish,omitempty"`
	Error   string       `json:"error,omitempty"`
}

// Recording reports whether the service's audio is still being captured,
// including the moment before its first audio has arrived. A recording
// service cannot be edited, exported, uploaded, or deleted.
func (s *Session) Recording() bool { return s.Status == "recording" || s.Status == "starting" }

// CloseOpenSegments ends every segment still open at a position after its
// start, as when a recording stops or the next segment begins.
func CloseOpenSegments(session *Session, frames uint64, seconds float64, now time.Time) {
	for index := range session.Segments {
		segment := &session.Segments[index]
		if segment.EndFrame == nil && frames > segment.StartFrame {
			end, endSeconds := frames, seconds
			segment.EndFrame, segment.End, segment.UpdatedAt = &end, &endSeconds, now
		}
	}
}

// Talk is the description of a service's talk that the church website shows.
type Talk struct {
	Title string `json:"title"`
	// Date is the day of the service, YYYY-MM-DD.
	Date string `json:"date"`
	// Church, Speakers, and Series are names matched against the site's lists.
	Church   string   `json:"church"`
	Speakers []string `json:"speakers"`
	// NewSpeakers and NewSeries record that the operator confirmed a name is
	// not on the site yet and is to be added, rather than a typing slip.
	NewSpeakers []string `json:"newSpeakers,omitempty"`
	Series      string   `json:"series"`
	NewSeries   bool     `json:"newSeries,omitempty"`
	Passage     string   `json:"passage"`
	Podcast     bool     `json:"podcast"`
	VideoURL    string   `json:"videoUrl"`
	// Image is a link to, or the ID of, an image already on the site. Empty
	// means the image of the latest talk in the same series.
	Image string `json:"image"`
	// Source says where the details were first filled in from.
	Source string `json:"source,omitempty"`
}

// PublishProgress is what an upload has created on the website so far, so
// that a failed one carries on without creating anything twice.
type PublishProgress struct {
	// Speakers and Series are the names the upload added to the site's lists,
	// with the IDs the site gave them.
	Speakers map[string]int `json:"speakers,omitempty"`
	Series   map[string]int `json:"series,omitempty"`
	PostID   int            `json:"postId,omitempty"`
	MediaID  int            `json:"mediaId,omitempty"`
	Finished bool           `json:"finished,omitempty"`
	Link     string         `json:"link,omitempty"`
}

// Empty reports whether nothing has been created yet.
func (p PublishProgress) Empty() bool {
	return len(p.Speakers) == 0 && len(p.Series) == 0 && p.PostID == 0 && p.MediaID == 0 && !p.Finished
}

// PublishInfo is the state of a service's upload to the website.
type PublishInfo struct {
	// Status is "running", "completed", or "failed".
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	Site      string     `json:"site"`
	// MP3 and MP3Finished identify the export that was uploaded, so that an
	// MP3 made again afterwards is recognised as not being the one on the site.
	MP3         string          `json:"mp3,omitempty"`
	MP3Finished *time.Time      `json:"mp3Finished,omitempty"`
	Progress    PublishProgress `json:"progress"`
	Error       string          `json:"error,omitempty"`
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
