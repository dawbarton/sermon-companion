// Tests for recovering a service whose recording was interrupted. The length
// of the source audio bounds every segment the operator can edit, so these
// cover measuring it from a FLAC the encoder never closed, and what the
// application does when it cannot be measured at all.
package capture

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

// writeUnclosedFLAC writes the file an interrupted encoder leaves behind: one
// whose STREAMINFO header never received the total sample count, because that
// is written by rewinding on close, and whose final frame is torn. FFprobe can
// read no length from it at all, which is the condition under test.
func writeUnclosedFLAC(t *testing.T, ffmpeg, path string, seconds float64) {
	t.Helper()
	complete := filepath.Join(t.TempDir(), "complete.flac")
	command := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration="+strconv.FormatFloat(seconds, 'f', -1, 64),
		"-ac", "2", "-c:a", "flac", complete)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("encode reference FLAC: %v: %s", err, output)
	}
	data, err := os.ReadFile(complete)
	if err != nil {
		t.Fatal(err)
	}
	// STREAMINFO begins after the "fLaC" marker and the four-byte metadata
	// block header. Its 36-bit total sample count is the low nibble of the
	// fourteenth byte followed by the next four.
	const streamInfo = 4 + 4
	if len(data) < streamInfo+18 {
		t.Fatalf("reference FLAC is too short to doctor: %d bytes", len(data))
	}
	data[streamInfo+13] &= 0xf0
	for offset := streamInfo + 14; offset < streamInfo+18; offset++ {
		data[offset] = 0
	}
	if err := os.WriteFile(path, data[:len(data)-200], 0o644); err != nil {
		t.Fatal(err)
	}
	// If a future FFprobe learns to answer for a file like this, the decode
	// fallback these tests exist to cover would stop being reached quietly.
	if ffprobe, err := exec.LookPath("ffprobe"); err == nil {
		if duration, err := probeDuration(ffprobe, path); err == nil {
			t.Fatalf("FFprobe read a duration of %v from an unclosed FLAC; this test no longer exercises the decode fallback", duration)
		}
	}
}

// recordingSession builds a service as it is left on disk while recording,
// including the frame count written when the capture began. That count stands
// in for the first miniaudio callback, which is what used to survive a
// recovery and make every real segment look out of range.
func recordingSession(t *testing.T, sessions *store.Store, segments ...store.Segment) *store.Session {
	t.Helper()
	session, err := sessions.Create("Interrupted", "Test Church", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := sessions.Update(session.ID, "test.recording", nil, func(s *store.Session) error {
		s.Status, s.AudioFile = "recording", "audio.part.flac"
		s.Capture = store.CaptureInfo{Backend: "test", SampleRate: 48_000, Channels: 2, SampleFormat: "s16le", TotalFrames: 960, WrittenFrames: 960}
		s.Segments = append(s.Segments, segments...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func mediaToolConfig(t *testing.T) config.Config {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("FFprobe is not installed")
	}
	c := config.DefaultConfig()
	c.FFmpeg, c.FFprobe = ffmpeg, ffprobe
	return c
}

func TestInterruptedCaptureMeasuresUnclosedRecording(t *testing.T) {
	c := mediaToolConfig(t)
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := recordingSession(t, sessions,
		store.Segment{ID: "seg_reading", Kind: "reading", Label: "Reading", StartFrame: 48_000, EndFrame: framePointer(96_000), Start: 1, End: secondPointer(2), Include: true, CreatedAt: now, UpdatedAt: now},
		store.Segment{ID: "seg_sermon", Kind: "sermon", Label: "Sermon", StartFrame: 144_000, Start: 3, Include: true, CreatedAt: now, UpdatedAt: now})
	dir, _ := sessions.SessionDir(session.ID)
	writeUnclosedFLAC(t, c.FFmpeg, filepath.Join(dir, "audio.part.flac"), 6)

	manager := New(config.NewSettings("", c), sessions)
	if err := manager.RecoverInterrupted(); err != nil {
		t.Fatal(err)
	}
	recovered, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "interrupted" {
		t.Fatalf("status = %q", recovered.Status)
	}
	if math.Abs(recovered.Duration-6) > 0.3 {
		t.Fatalf("recovered duration = %v, want about 6", recovered.Duration)
	}
	if recovered.Capture.AudioDuration != recovered.Duration {
		t.Fatalf("capture duration %v does not match session duration %v", recovered.Capture.AudioDuration, recovered.Duration)
	}
	wantFrames := uint64(recovered.Duration*48_000 + 0.5)
	if recovered.Capture.TotalFrames != wantFrames || recovered.Capture.WrittenFrames != wantFrames {
		t.Fatalf("frame counts %d/%d do not match the measured length %d", recovered.Capture.TotalFrames, recovered.Capture.WrittenFrames, wantFrames)
	}
	open := segmentByID(t, recovered, "seg_sermon")
	if open.EndFrame == nil || *open.EndFrame != recovered.Capture.TotalFrames {
		t.Fatalf("the segment left open by the interruption was not closed at the measured end: %#v", open)
	}
}

func TestUnmeasurableRecordingLeavesTheLengthUnknown(t *testing.T) {
	c := mediaToolConfig(t)
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session := recordingSession(t, sessions)
	dir, _ := sessions.SessionDir(session.ID)
	if err := os.WriteFile(filepath.Join(dir, "audio.part.flac"), []byte("not audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := New(config.NewSettings("", c), sessions)
	if err := manager.RecoverInterrupted(); err != nil {
		t.Fatal(err)
	}
	recovered, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Zero means unknown, and the segment rules read an unknown length as
	// unbounded. Any other value is a length no real segment fits inside,
	// which is what closed the review page to every edit.
	if recovered.Capture.TotalFrames != 0 || recovered.Capture.WrittenFrames != 0 || recovered.Duration != 0 {
		t.Fatalf("an unmeasurable recording kept a length: duration=%v frames=%d", recovered.Duration, recovered.Capture.TotalFrames)
	}
	if !strings.Contains(recovered.Error, "could not be measured") {
		t.Fatalf("the recovery message does not explain the missing length: %q", recovered.Error)
	}
}

func TestInterruptedSessionWithStaleFrameCountIsRemeasured(t *testing.T) {
	c := mediaToolConfig(t)
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session := recordingSession(t, sessions)
	dir, _ := sessions.SessionDir(session.ID)
	writeUnclosedFLAC(t, c.FFmpeg, filepath.Join(dir, "audio.part.flac"), 6)
	// The state an earlier version left behind: closed as interrupted, but
	// still carrying the frame count written when the recording began.
	ended := time.Now().UTC()
	if _, err := sessions.Update(session.ID, "test.interrupted", nil, func(s *store.Session) error {
		s.Status, s.EndedAt, s.Duration = "interrupted", &ended, 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	manager := New(config.NewSettings("", c), sessions)
	if err := manager.RecoverInterrupted(); err != nil {
		t.Fatal(err)
	}
	repaired, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(repaired.Duration-6) > 0.3 {
		t.Fatalf("repaired duration = %v, want about 6", repaired.Duration)
	}
	if repaired.Status != "interrupted" || repaired.EndedAt == nil || !repaired.EndedAt.Equal(ended) {
		t.Fatalf("the repair changed how the service finished: %#v", repaired)
	}
	if needsLengthRepair(repaired) {
		t.Fatal("a repaired service would be measured again at every start-up")
	}
}

func TestStartedCaptureRecordsNoFrameCounts(t *testing.T) {
	sessions, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := New(config.NewSettings("", config.DefaultConfig()), sessions)
	active := newControlledCapture()
	// The miniaudio backend returns only once the first audio callback has
	// run, so its counters are never zero by the time the manager reads them.
	active.info.TotalFrames, active.info.CallbackCount, active.info.QueueHighWaterFrames = 960, 1, 960
	manager.startNative = func(config.Config, string, *os.File) (activeCapture, error) { return active, nil }
	session, err := manager.Start("Counter test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = manager.Stop() })
	if session.Capture.TotalFrames != 0 || session.Capture.CallbackCount != 0 || session.Capture.QueueHighWaterFrames != 0 {
		t.Fatalf("a capture that has just started recorded frame counts: %#v", session.Capture)
	}
	stored, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Capture.TotalFrames != 0 {
		t.Fatalf("a capture that has just started persisted a frame count: %d", stored.Capture.TotalFrames)
	}
	if stored.Capture.SampleRate != active.info.SampleRate {
		t.Fatalf("the capture format was lost with the counters: %#v", stored.Capture)
	}
}

func TestTrimSegmentsToRecording(t *testing.T) {
	now := time.Now().UTC()
	session := &store.Session{Segments: []store.Segment{
		{ID: "inside", StartFrame: 0, EndFrame: framePointer(48_000), Start: 0, End: secondPointer(1), Include: true},
		{ID: "overruns", StartFrame: 48_000, EndFrame: framePointer(200_000), Start: 1, End: secondPointer(4.1666), Include: true},
		{ID: "beyond", StartFrame: 150_000, EndFrame: framePointer(180_000), Start: 3.125, End: secondPointer(3.75), Include: true},
	}}
	trimSegmentsToRecording(session, 96_000, 48_000, now)

	if *session.Segments[0].EndFrame != 48_000 || session.Segments[0].Archived {
		t.Fatalf("a segment inside the recording was changed: %#v", session.Segments[0])
	}
	if *session.Segments[1].EndFrame != 96_000 || *session.Segments[1].End != 2 {
		t.Fatalf("an overrunning segment was not trimmed to the recording: %#v", session.Segments[1])
	}
	if !session.Segments[2].Archived {
		t.Fatalf("a segment with no audio behind it was left active: %#v", session.Segments[2])
	}
	if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
		t.Fatalf("trimming produced an invalid timeline: %v", err)
	}
}

func TestTrimSegmentsToRecordingLeavesAnUnknownLengthAlone(t *testing.T) {
	session := &store.Session{Segments: []store.Segment{
		{ID: "any", StartFrame: 48_000, EndFrame: framePointer(200_000), Start: 1, End: secondPointer(4.1666), Include: true},
	}}
	trimSegmentsToRecording(session, 0, 48_000, time.Now().UTC())
	if *session.Segments[0].EndFrame != 200_000 || session.Segments[0].Archived {
		t.Fatalf("segments were trimmed against an unknown length: %#v", session.Segments[0])
	}
}

func TestFurthestReportedPosition(t *testing.T) {
	progress := "frame=0\nout_time_us=1000000\nprogress=continue\nout_time_us=62122667\nprogress=end\nout_time_us=\n"
	duration, ok := furthestReportedPosition(progress)
	if !ok || math.Abs(duration-62.122667) > 1e-6 {
		t.Fatalf("duration = %v, ok = %v", duration, ok)
	}
	if _, ok := furthestReportedPosition("out_time_us=0\nprogress=end\n"); ok {
		t.Fatal("a recording with no audio reported a position")
	}
	if _, ok := furthestReportedPosition(""); ok {
		t.Fatal("empty progress output reported a position")
	}
}

func framePointer(value uint64) *uint64    { return &value }
func secondPointer(value float64) *float64 { return &value }

func segmentByID(t *testing.T, session *store.Session, id string) store.Segment {
	t.Helper()
	for _, segment := range session.Segments {
		if segment.ID == id {
			return segment
		}
	}
	t.Fatalf("segment %q is missing from %#v", id, session.Segments)
	return store.Segment{}
}
