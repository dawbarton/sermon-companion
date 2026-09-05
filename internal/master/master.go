package master

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/proc"
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
	gap := gapBetweenSegments(m.config.Master, session)
	channels := "stereo"
	if m.config.Master.MonoDownmix() {
		channels = "mono"
	}
	fmt.Fprintf(logFile, "\n[%s] export started: %d segments, %g s between them, %s, %g LUFS limited to %g dBFS\n", started.Format(time.RFC3339), len(segments), gap, channels, m.config.Master.IntegratedLUFS, m.config.Master.PeakLimitDBFS())

	files := make([]string, 0, len(segments))
	recordingRate := session.Capture.SampleRate
	if recordingRate <= 0 {
		recordingRate = m.config.Capture.SampleRate
	}
	// Everything the operator gave one label is measured as a single piece of
	// speech, so the gain is the same across all of it.
	measured := make(map[string]measurement, len(segments))
	for _, group := range groupByLabel(segments) {
		value, err := m.measureGroup(input, group, logFile)
		if err != nil {
			return m.fail(id, fmt.Errorf("measure %q: %w", group.label, err))
		}
		fmt.Fprintf(logFile, "%q measured across %d segment(s): I=%s LUFS, TP=%s dBTP\n", group.label, len(group.segments), value.InputI, value.InputTP)
		measured[group.key] = value
	}
	for i, segment := range segments {
		// The silence goes after every segment but the last, so the MP3 neither
		// opens nor ends on a pause.
		pad := gap
		if i == len(segments)-1 {
			pad = 0
		}
		output, err := m.renderSegment(input, workDir, i, segment, measured[labelKey(segment)], recordingRate, pad, logFile)
		if err != nil {
			return m.fail(id, fmt.Errorf("normalise %q: %w", segment.Label, err))
		}
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

// measureGroup runs the analysis pass over every segment carrying one label at
// once. The segments are joined inside the filter graph, so FFmpeg measures the
// speech as the operator hears it rather than as the pieces it was cut into.
func (m *Master) measureGroup(input string, group segmentGroup, logFile *os.File) (measurement, error) {
	graph := new(strings.Builder)
	fmt.Fprintf(graph, "[0:a]asplit=%d", len(group.segments))
	for i := range group.segments {
		fmt.Fprintf(graph, "[whole%d]", i)
	}
	graph.WriteString(";")
	for i, segment := range group.segments {
		trim, err := trimFilter(segment)
		if err != nil {
			return measurement{}, err
		}
		fmt.Fprintf(graph, "[whole%d]%s[part%d];", i, trim, i)
	}
	for i := range group.segments {
		fmt.Fprintf(graph, "[part%d]", i)
	}
	fmt.Fprintf(graph, "concat=n=%d:v=0:a=1,%s%s:print_format=json[measured]", len(group.segments), m.downmixFilter(), targetFilter(m.config.Master))
	args := []string{"-hide_banner", "-nostats", "-i", input, "-filter_complex", graph.String(), "-map", "[measured]", "-vn", "-f", "null", "-"}
	analysis, err := runCapture(m.config.FFmpeg, args, "", logFile)
	if err != nil {
		return measurement{}, err
	}
	return parseMeasurement(analysis)
}

// renderSegment applies the gain its group was measured for, so two halves of
// one talk come out at the same level as each other.
func (m *Master) renderSegment(input, workDir string, index int, segment store.Segment, measured measurement, recordingRate int, padSeconds float64, logFile *os.File) (string, error) {
	trimmed, err := trimFilter(segment)
	if err != nil {
		return "", err
	}
	if measured.InputI == "" {
		return "", errors.New("this segment was not measured")
	}
	target := targetFilter(m.config.Master)
	common := []string{"-hide_banner", "-nostats", "-i", input}
	trim := trimmed + "," + m.downmixFilter()
	filter := trim + target + ":measured_I=" + measured.InputI + ":measured_LRA=" + measured.InputLRA + ":measured_TP=" + measured.InputTP + ":measured_thresh=" + measured.InputThresh + ":offset=" + measured.TargetOffset + ":linear=true:print_format=summary"
	// loudnorm upsamples internally to 192 kHz for true-peak detection. Resample
	// explicitly inside the filter graph before handing frames to FLAC; relying
	// on the output -ar option can leave an invalid encoder block size.
	filter += ",aresample=" + strconv.Itoa(recordingRate) + "," + peakLimiter(m.config.Master)
	if padSeconds > 0 {
		// The silence is appended after the loudness pass, so a pause between
		// segments cannot pull the measured level of the speech about.
		filter += fmt.Sprintf(",apad=pad_dur=%g", padSeconds)
	}
	filter += ",aformat=sample_fmts=s16,asetnsamples=n=4096:p=0"
	output := filepath.Join(workDir, fmt.Sprintf("segment-%03d.flac", index+1))
	renderArgs := append(append([]string{}, common...), "-vn", "-af", filter, "-ar", strconv.Itoa(recordingRate), "-c:a", "flac", "-compression_level", "5", output)
	if err := runLogged(m.config.FFmpeg, renderArgs, "", logFile); err != nil {
		return "", err
	}
	return output, nil
}

// trimFilter cuts one segment out of the recording by exact audio frame.
func trimFilter(segment store.Segment) (string, error) {
	if segment.EndFrame == nil || *segment.EndFrame <= segment.StartFrame {
		return "", errors.New("segment has invalid audio-frame boundaries")
	}
	return fmt.Sprintf("atrim=start_sample=%d:end_sample=%d,asetpts=PTS-STARTPTS", segment.StartFrame, *segment.EndFrame), nil
}

// downmixFilter folds the export to one channel, and is placed ahead of the
// loudness in both passes. Two identical channels measure about 3 LU louder
// than the one channel they carry, so normalising first and folding afterwards
// would land below the target. It ends with a comma, or is empty.
func (m *Master) downmixFilter() string {
	if m.config.Master.MonoDownmix() {
		return "aformat=channel_layouts=mono,"
	}
	return ""
}

type segmentGroup struct {
	key      string
	label    string
	segments []store.Segment
}

func labelKey(segment store.Segment) string { return strings.ToLower(strings.TrimSpace(segment.Label)) }

// groupByLabel gathers the segments the operator gave one label. A sermon split
// in two so that something in the middle can be dropped is one piece of speech,
// and levelling the halves apart would leave a step where the cut was made.
// Groups appear in the order their first segment does, and each keeps its
// segments in the order they were given.
func groupByLabel(segments []store.Segment) []segmentGroup {
	groups := make([]segmentGroup, 0, len(segments))
	at := make(map[string]int, len(segments))
	for _, segment := range segments {
		key := labelKey(segment)
		index, seen := at[key]
		if !seen {
			at[key] = len(groups)
			groups = append(groups, segmentGroup{key: key, label: segment.Label})
			index = at[key]
		}
		groups[index].segments = append(groups[index].segments, segment)
	}
	return groups
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

// peakLimiter holds the normalised segment below the configured ceiling.
// loudnorm aims at a true peak but does not guarantee it once its 192 kHz
// working audio is resampled, and the MP3 encoder can overshoot again. The
// limiter's own auto-levelling is switched off: it would raise every segment to
// the ceiling and undo the loudness normalisation.
func peakLimiter(c config.MasteringConfig) string {
	return fmt.Sprintf("alimiter=limit=%.6f:level=0", math.Pow(10, c.PeakLimitDBFS()/20))
}

// gapBetweenSegments is the silence placed between segments: what the operator
// set for this service, or the configured default where they set nothing.
func gapBetweenSegments(c config.MasteringConfig, session *store.Session) float64 {
	if session.GapSeconds != nil {
		return config.ClampGapSeconds(*session.GapSeconds)
	}
	return c.GapBetweenSegments()
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
	cmd := proc.Command(program, args...)
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
	cmd := proc.Command(program, args...)
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
