package master

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/store"
)

type Master struct {
	config config.Config
	store  *store.Store
}

type measurement struct {
	InputI       string `json:"input_i"`
	InputTP      string `json:"input_tp"`
	InputLRA     string `json:"input_lra"`
	InputThresh  string `json:"input_thresh"`
	TargetOffset string `json:"target_offset"`
}

func New(c config.Config, sessions *store.Store) *Master {
	return &Master{config: c, store: sessions}
}

func (m *Master) Export(id string) error {
	session, err := m.store.Get(id)
	if err != nil {
		return err
	}
	if session.Status == "recording" || session.Status == "starting" {
		return errors.New("stop the recording before exporting")
	}
	if session.AudioRemovedAt != nil {
		return errors.New("the lossless recording was removed after the retention period, so the MP3 cannot be created again")
	}
	if strings.TrimSpace(session.Church) == "" {
		session.Church = m.config.Church
	}
	if err := store.ValidateNoSegmentOverlaps(session.Segments); err != nil {
		return err
	}
	segments := exportSegments(session.Segments)
	if len(segments) == 0 {
		return errors.New("there are no complete, included segments to export")
	}
	dir, _ := m.store.SessionDir(id)
	input, err := m.store.SessionFile(id, session.AudioFile)
	if err != nil {
		return err
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("recording not found: %w", err)
	}

	started := time.Now().UTC()
	begun, err := m.store.Update(id, "export.started", map[string]any{"segments": segmentIDs(segments)}, func(s *store.Session) error {
		s.Export = &store.ExportInfo{Status: "running", StartedAt: started}
		return nil
	})
	if err != nil {
		return err
	}
	startRevision := begun.Revision

	exportDir := filepath.Join(dir, "exports")
	workDir := filepath.Join(exportDir, ".work-"+started.Format("20060102-150405"))
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return m.fail(id, err)
	}
	logFile, err := os.OpenFile(filepath.Join(exportDir, "mastering.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return m.fail(id, err)
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "\n[%s] export started\n", started.Format(time.RFC3339))

	files := make([]string, 0, len(segments))
	recordingRate := session.Capture.SampleRate
	if recordingRate <= 0 {
		recordingRate = m.config.Capture.SampleRate
	}
	for i, segment := range segments {
		measurement, output, err := m.normaliseSegment(input, workDir, i, segment, recordingRate, logFile)
		if err != nil {
			return m.fail(id, fmt.Errorf("normalise %q: %w", segment.Label, err))
		}
		fmt.Fprintf(logFile, "segment %d %q measured I=%s LUFS, TP=%s dBTP\n", i+1, segment.Label, measurement.InputI, measurement.InputTP)
		files = append(files, output)
	}

	list := new(strings.Builder)
	for _, name := range files {
		fmt.Fprintf(list, "file '%s'\n", filepath.Base(name))
	}
	if err := os.WriteFile(filepath.Join(workDir, "concat.txt"), []byte(list.String()), 0o644); err != nil {
		return m.fail(id, err)
	}
	outputName := outputName(session)
	tempOutput := filepath.Join(workDir, "master.part.mp3")
	args := []string{"-hide_banner", "-y", "-f", "concat", "-safe", "0", "-i", "concat.txt", "-vn", "-c:a", "libmp3lame", "-q:a", strconv.Itoa(m.config.Master.MP3QualityLevel()), "-ar", strconv.Itoa(recordingRate), "-metadata", "title=" + session.Title, "-metadata", "comment=Created by Sermon Companion", tempOutput}
	if err := runLogged(m.config.FFmpeg, args, workDir, logFile); err != nil {
		return m.fail(id, fmt.Errorf("create MP3: %w", err))
	}
	finalPath := filepath.Join(exportDir, outputName)
	if err := publishOutput(tempOutput, finalPath, started); err != nil {
		return m.fail(id, err)
	}
	ended := time.Now().UTC()
	_, err = m.store.Update(id, "export.completed", map[string]any{"output": filepath.Join("exports", outputName)}, func(s *store.Session) error {
		info := &store.ExportInfo{Status: "completed", StartedAt: started, EndedAt: &ended, Output: filepath.ToSlash(filepath.Join("exports", outputName))}
		// Segment and metadata edits are accepted while an export runs, so an
		// MP3 built from the earlier snapshot must not be published as current.
		if s.Revision != startRevision {
			info.Status = "stale"
			info.Error = "Service details or segments changed while this MP3 was being created."
		}
		s.Export = info
		return nil
	})
	if err == nil {
		_ = os.RemoveAll(workDir) // Derived, reproducible intermediates only.
	}
	return err
}

func (m *Master) normaliseSegment(input, workDir string, index int, segment store.Segment, recordingRate int, logFile *os.File) (measurement, string, error) {
	if segment.EndFrame == nil || *segment.EndFrame <= segment.StartFrame {
		return measurement{}, "", errors.New("segment has invalid audio-frame boundaries")
	}
	target := targetFilter(m.config.Master)
	common := []string{"-hide_banner", "-nostats", "-i", input}
	trim := fmt.Sprintf("atrim=start_sample=%d:end_sample=%d,asetpts=PTS-STARTPTS,", segment.StartFrame, *segment.EndFrame)
	analyseArgs := append(append([]string{}, common...), "-vn", "-af", trim+target+":print_format=json", "-f", "null", "-")
	analysis, err := runCapture(m.config.FFmpeg, analyseArgs, "", logFile)
	if err != nil {
		return measurement{}, "", err
	}
	measured, err := parseMeasurement(analysis)
	if err != nil {
		return measurement{}, "", err
	}
	filter := trim + target + ":measured_I=" + measured.InputI + ":measured_LRA=" + measured.InputLRA + ":measured_TP=" + measured.InputTP + ":measured_thresh=" + measured.InputThresh + ":offset=" + measured.TargetOffset + ":linear=true:print_format=summary"
	// loudnorm upsamples internally to 192 kHz for true-peak detection. Resample
	// explicitly inside the filter graph before handing frames to FLAC; relying
	// on the output -ar option can leave an invalid encoder block size.
	filter += ",aresample=" + strconv.Itoa(recordingRate) + ",aformat=sample_fmts=s16,asetnsamples=n=4096:p=0"
	output := filepath.Join(workDir, fmt.Sprintf("segment-%03d.flac", index+1))
	renderArgs := append(append([]string{}, common...), "-vn", "-af", filter, "-ar", strconv.Itoa(recordingRate), "-c:a", "flac", "-compression_level", "5", output)
	if err := runLogged(m.config.FFmpeg, renderArgs, "", logFile); err != nil {
		return measurement{}, "", err
	}
	return measured, output, nil
}

func (m *Master) fail(id string, cause error) error {
	ended := time.Now().UTC()
	session, _ := m.store.Get(id)
	started := ended
	if session != nil && session.Export != nil {
		started = session.Export.StartedAt
	}
	_, _ = m.store.Update(id, "export.failed", map[string]any{"error": cause.Error()}, func(s *store.Session) error {
		s.Export = &store.ExportInfo{Status: "failed", StartedAt: started, EndedAt: &ended, Error: cause.Error()}
		return nil
	})
	return cause
}

func targetFilter(c config.MasteringConfig) string {
	return fmt.Sprintf("loudnorm=I=%g:LRA=%g:TP=%g", c.IntegratedLUFS, c.LoudnessRange, c.TruePeakDB)
}

func parseMeasurement(output []byte) (measurement, error) {
	start := bytes.LastIndexByte(output, '{')
	end := bytes.LastIndexByte(output, '}')
	if start < 0 || end <= start {
		return measurement{}, errors.New("FFmpeg did not return loudness measurements")
	}
	var measured measurement
	if err := json.Unmarshal(output[start:end+1], &measured); err != nil {
		return measurement{}, fmt.Errorf("parse loudness measurements: %w", err)
	}
	if measured.InputI == "" || measured.InputTP == "" {
		return measurement{}, errors.New("incomplete loudness measurements")
	}
	return measured, nil
}

func runCapture(program string, args []string, dir string, logFile *os.File) ([]byte, error) {
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	logFile.Write(output.Bytes())
	if err != nil {
		return output.Bytes(), fmt.Errorf("%w", err)
	}
	return output.Bytes(), nil
}

func runLogged(program string, args []string, dir string, logFile *os.File) error {
	cmd := exec.Command(program, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, logFile, logFile
	return cmd.Run()
}

func exportSegments(all []store.Segment) []store.Segment {
	segments := make([]store.Segment, 0, len(all))
	for _, segment := range all {
		if !segment.Archived && segment.Include && segment.EndFrame != nil && *segment.EndFrame > segment.StartFrame {
			segments = append(segments, segment)
		}
	}
	sort.SliceStable(segments, func(i, j int) bool { return segments[i].Start < segments[j].Start })
	return segments
}

func segmentIDs(segments []store.Segment) []string {
	ids := make([]string, len(segments))
	for i := range segments {
		ids[i] = segments[i].ID
	}
	return ids
}

func outputName(session *store.Session) string {
	church := filenamePart(session.Church)
	if church == "" {
		church = "Church"
	}
	return session.StartedAt.In(time.Local).Format("2006-01-02") + "-" + church + ".mp3"
}

func filenamePart(value string) string {
	var out strings.Builder
	separator := false
	for _, r := range strings.TrimSpace(value) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if separator && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(r)
			separator = false
		case r == '\'' || r == '’':
			// Apostrophes are omitted: "St Mary's" becomes "St-Marys".
		default:
			separator = out.Len() > 0
		}
	}
	return strings.TrimRight(out.String(), "-")
}

func publishOutput(tempPath, finalPath string, started time.Time) error {
	backupPath := ""
	if _, err := os.Stat(finalPath); err == nil {
		previousDir := filepath.Join(filepath.Dir(finalPath), "previous")
		if err := os.MkdirAll(previousDir, 0o755); err != nil {
			return err
		}
		base := strings.TrimSuffix(filepath.Base(finalPath), filepath.Ext(finalPath))
		backupName := base + "-" + started.Format("20060102-150405.000000000") + filepath.Ext(finalPath)
		backupPath = filepath.Join(previousDir, backupName)
		if err := os.Rename(finalPath, backupPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		if backupPath != "" {
			_ = os.Rename(backupPath, finalPath)
		}
		return err
	}
	return nil
}
