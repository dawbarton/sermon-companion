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
	segments := exportSegments(session.Segments)
	if len(segments) == 0 {
		return errors.New("there are no complete, included segments to export")
	}
	dir, _ := m.store.SessionDir(id)
	input := filepath.Join(dir, session.AudioFile)
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("recording not found: %w", err)
	}

	started := time.Now().UTC()
	_, err = m.store.Update(id, "export.started", map[string]any{"segments": segmentIDs(segments)}, func(s *store.Session) error {
		s.Export = &store.ExportInfo{Status: "running", StartedAt: started}
		return nil
	})
	if err != nil {
		return err
	}

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
	for i, segment := range segments {
		measurement, output, err := m.normaliseSegment(input, workDir, i, segment, logFile)
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
	outputName := nextOutputName(exportDir)
	tempOutput := filepath.Join(workDir, "master.part.mp3")
	args := []string{"-hide_banner", "-y", "-f", "concat", "-safe", "0", "-i", "concat.txt", "-vn", "-c:a", "libmp3lame", "-b:a", m.config.Master.MP3Bitrate, "-ar", strconv.Itoa(m.config.Capture.SampleRate), "-metadata", "title=" + session.Title, "-metadata", "comment=Created by Sermon Companion", tempOutput}
	if err := runLogged(m.config.FFmpeg, args, workDir, logFile); err != nil {
		return m.fail(id, fmt.Errorf("create MP3: %w", err))
	}
	finalPath := filepath.Join(exportDir, outputName)
	if err := os.Rename(tempOutput, finalPath); err != nil {
		return m.fail(id, err)
	}
	ended := time.Now().UTC()
	_, err = m.store.Update(id, "export.completed", map[string]any{"output": filepath.Join("exports", outputName)}, func(s *store.Session) error {
		s.Export = &store.ExportInfo{Status: "completed", StartedAt: started, EndedAt: &ended, Output: filepath.ToSlash(filepath.Join("exports", outputName))}
		return nil
	})
	if err == nil {
		_ = os.RemoveAll(workDir) // Derived, reproducible intermediates only.
	}
	return err
}

func (m *Master) normaliseSegment(input, workDir string, index int, segment store.Segment, logFile *os.File) (measurement, string, error) {
	duration := *segment.End - segment.Start
	target := targetFilter(m.config.Master)
	common := []string{"-hide_banner", "-nostats", "-ss", seconds(segment.Start), "-t", seconds(duration), "-i", input}
	analyseArgs := append(append([]string{}, common...), "-vn", "-af", target+":print_format=json", "-f", "null", "-")
	analysis, err := runCapture(m.config.FFmpeg, analyseArgs, "", logFile)
	if err != nil {
		return measurement{}, "", err
	}
	measured, err := parseMeasurement(analysis)
	if err != nil {
		return measurement{}, "", err
	}
	filter := target + ":measured_I=" + measured.InputI + ":measured_LRA=" + measured.InputLRA + ":measured_TP=" + measured.InputTP + ":measured_thresh=" + measured.InputThresh + ":offset=" + measured.TargetOffset + ":linear=true:print_format=summary"
	// loudnorm upsamples internally to 192 kHz for true-peak detection. Resample
	// explicitly inside the filter graph before handing frames to FLAC; relying
	// on the output -ar option can leave an invalid encoder block size.
	filter += ",aresample=" + strconv.Itoa(m.config.Capture.SampleRate) + ",aformat=sample_fmts=s16,asetnsamples=n=4096:p=0"
	output := filepath.Join(workDir, fmt.Sprintf("segment-%03d.flac", index+1))
	renderArgs := append(append([]string{}, common...), "-vn", "-af", filter, "-ar", strconv.Itoa(m.config.Capture.SampleRate), "-c:a", "flac", "-compression_level", "5", output)
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
		if segment.Include && segment.End != nil && *segment.End > segment.Start {
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

func seconds(value float64) string { return strconv.FormatFloat(value, 'f', 3, 64) }

func nextOutputName(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "sermon.mp3")); os.IsNotExist(err) {
		return "sermon.mp3"
	}
	return "sermon-" + time.Now().Format("20060102-150405") + ".mp3"
}
