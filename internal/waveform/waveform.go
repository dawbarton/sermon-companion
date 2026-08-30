package waveform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/dawbarton/sermon-companion/internal/store"
)

const (
	cacheVersion     = 1
	decodeRate       = 8000
	defaultPointRate = 20
)

// Envelope is a compact, mono peak envelope suitable for drawing a long
// recording in a browser. PeaksBase64 contains unsigned 8-bit peak amplitudes.
type Envelope struct {
	Version         int     `json:"version"`
	PointsPerSecond int     `json:"pointsPerSecond"`
	Duration        float64 `json:"durationSeconds"`
	PeaksBase64     string  `json:"peaksBase64"`
}

type cachedEnvelope struct {
	Envelope
	SourceSize    int64 `json:"sourceSize"`
	SourceModTime int64 `json:"sourceModTimeUnixNano"`
}

type Generator struct {
	ffmpeg string
	store  *store.Store
	mu     sync.Mutex
}

func New(ffmpeg string, sessions *store.Store) *Generator {
	return &Generator{ffmpeg: ffmpeg, store: sessions}
}

func (g *Generator) Generate(ctx context.Context, id string) (Envelope, error) {
	// Waveform generation is infrequent and FFmpeg-heavy. Serialising it avoids
	// duplicate decodes when multiple review tabs request the same new session.
	g.mu.Lock()
	defer g.mu.Unlock()

	session, err := g.store.Get(id)
	if err != nil {
		return Envelope{}, err
	}
	if session.Status == "recording" || session.Status == "starting" {
		return Envelope{}, errors.New("the waveform is available after recording stops")
	}
	dir, _ := g.store.SessionDir(id)
	source := filepath.Join(dir, session.AudioFile)
	info, err := os.Stat(source)
	if err != nil {
		return Envelope{}, fmt.Errorf("recording not found: %w", err)
	}
	cachePath := filepath.Join(dir, fmt.Sprintf("waveform-%dpps.json", defaultPointRate))
	if cached, ok := readCache(cachePath, info); ok {
		return cached.Envelope, nil
	}

	peaks, duration, err := g.decode(ctx, source, defaultPointRate)
	if err != nil {
		return Envelope{}, err
	}
	envelope := Envelope{
		Version:         cacheVersion,
		PointsPerSecond: defaultPointRate,
		Duration:        duration,
		PeaksBase64:     base64.StdEncoding.EncodeToString(peaks),
	}
	cached := cachedEnvelope{Envelope: envelope, SourceSize: info.Size(), SourceModTime: info.ModTime().UnixNano()}
	encoded, err := json.Marshal(cached)
	if err != nil {
		return Envelope{}, err
	}
	tmp := cachePath + ".tmp"
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0o644); err != nil {
		return Envelope{}, err
	}
	if err := os.Rename(tmp, cachePath); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func readCache(path string, source os.FileInfo) (cachedEnvelope, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cachedEnvelope{}, false
	}
	var cached cachedEnvelope
	if json.Unmarshal(data, &cached) != nil || cached.Version != cacheVersion || cached.PointsPerSecond != defaultPointRate {
		return cachedEnvelope{}, false
	}
	if cached.SourceSize != source.Size() || cached.SourceModTime != source.ModTime().UnixNano() {
		return cachedEnvelope{}, false
	}
	return cached, true
}

func (g *Generator) decode(ctx context.Context, source string, pointRate int) ([]byte, float64, error) {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-i", source, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", strconv.Itoa(decodeRate), "-f", "s16le", "-c:a", "pcm_s16le", "-"}
	cmd := exec.CommandContext(ctx, g.ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("start FFmpeg waveform decode: %w", err)
	}
	peaks, samples, readErr := readPeaks(stdout, decodeRate/pointRate)
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, 0, readErr
	}
	if waitErr != nil {
		return nil, 0, fmt.Errorf("decode waveform: %w: %s", waitErr, stderr.String())
	}
	return peaks, float64(samples) / decodeRate, nil
}

func readPeaks(reader io.Reader, samplesPerPoint int) ([]byte, int64, error) {
	if samplesPerPoint <= 0 {
		return nil, 0, errors.New("samples per waveform point must be positive")
	}
	buffer := make([]byte, 64*1024)
	peaks := make([]byte, 0, 10000)
	var pending byte
	hasPending := false
	pointSamples, pointPeak := 0, 0
	var totalSamples int64
	consume := func(sample int16) {
		amplitude := int(sample)
		if amplitude < 0 {
			amplitude = -amplitude
		}
		if amplitude > pointPeak {
			pointPeak = amplitude
		}
		pointSamples++
		totalSamples++
		if pointSamples == samplesPerPoint {
			peaks = append(peaks, quantise(pointPeak))
			pointSamples, pointPeak = 0, 0
		}
	}
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			start := 0
			if hasPending {
				consume(int16(binary.LittleEndian.Uint16([]byte{pending, buffer[0]})))
				start, hasPending = 1, false
			}
			for start+1 < n {
				consume(int16(binary.LittleEndian.Uint16(buffer[start : start+2])))
				start += 2
			}
			if start < n {
				pending, hasPending = buffer[start], true
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}
	}
	if hasPending {
		return nil, 0, errors.New("waveform PCM stream ended on a partial sample")
	}
	if pointSamples > 0 {
		peaks = append(peaks, quantise(pointPeak))
	}
	return peaks, totalSamples, nil
}

func quantise(amplitude int) byte {
	return byte(math.Round(math.Min(float64(amplitude), 32768) * 255 / 32768))
}
