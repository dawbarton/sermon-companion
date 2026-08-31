package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	Listen  string          `json:"listen"`
	FFmpeg  string          `json:"ffmpeg"`
	FFprobe string          `json:"ffprobe"`
	Church  string          `json:"church"`
	Capture CaptureConfig   `json:"capture"`
	Presets []Preset        `json:"presets"`
	Master  MasteringConfig `json:"mastering"`
	// RetentionDays is how long a lossless recording is kept after the service.
	// Zero or a negative value keeps every recording indefinitely. Pointers
	// distinguish an omitted setting from a deliberate zero.
	RetentionDays *int `json:"retentionDays"`
}

type CaptureConfig struct {
	Backend    string   `json:"backend"`
	Driver     string   `json:"driver"`
	DeviceID   string   `json:"deviceId,omitempty"`
	Device     string   `json:"device"`
	InputArgs  []string `json:"inputArgs,omitempty"`
	SampleRate int      `json:"sampleRate"`
	Channels   int      `json:"channels"`
	PeriodMS   int      `json:"periodMilliseconds"`
	BufferSecs int      `json:"bufferSeconds"`
}

type Preset struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type MasteringConfig struct {
	IntegratedLUFS float64 `json:"integratedLUFS"`
	LoudnessRange  float64 `json:"loudnessRangeLU"`
	TruePeakDB     float64 `json:"truePeakDB"`
	// PeakLimitDB is the ceiling a limiter holds the finished audio below, in
	// dBFS, after the loudness normalisation. loudnorm aims at a true-peak
	// target but does not guarantee it once the result is resampled and encoded,
	// so the limiter is a backstop against clipping. Pointers distinguish an
	// omitted setting from a deliberate 0 dBFS.
	PeakLimitDB *float64 `json:"peakLimitDB"`
	// GapSeconds is the silence placed between consecutive segments in the MP3
	// unless a service overrides it. It applies to the export alone; the
	// recording and the reviewed times are untouched.
	GapSeconds *float64 `json:"gapSeconds"`
	// Mono downmixes the export to a single channel before the loudness is
	// measured. A service is one speaker through one mix, so both captured
	// channels carry the same audio, and a single channel is what the -19 LUFS
	// spoken-word target assumes. A pointer distinguishes an omitted setting
	// from a deliberate false.
	Mono *bool `json:"mono"`
	// MP3Quality is the LAME variable-bitrate level, 0 for the largest files
	// and 9 for the smallest. Speech needs far less than a constant 128 kbit/s.
	MP3Quality *int `json:"mp3Quality"`
}

func DefaultConfig() Config {
	driver := "avfoundation"
	device := "default"
	if runtime.GOOS == "windows" {
		driver = "dshow"
		device = "CHANGE ME: HDMI capture audio device"
	}
	return Config{
		Listen:        "127.0.0.1:8765",
		FFmpeg:        "ffmpeg",
		FFprobe:       "ffprobe",
		Church:        "Church",
		Capture:       CaptureConfig{Backend: "miniaudio", Driver: driver, Device: device, SampleRate: 48000, Channels: 2, PeriodMS: 20, BufferSecs: 10},
		Presets:       []Preset{{Kind: "reading", Label: "Reading"}, {Kind: "sermon", Label: "Sermon"}, {Kind: "questions", Label: "Q&A"}},
		Master:        MasteringConfig{IntegratedLUFS: -19, LoudnessRange: 11, TruePeakDB: -1.5, PeakLimitDB: floatPointer(-1), GapSeconds: floatPointer(2), Mono: boolPointer(true), MP3Quality: intPointer(5)},
		RetentionDays: intPointer(60),
	}
}

func intPointer(v int) *int { return &v }

func floatPointer(v float64) *float64 { return &v }

func boolPointer(v bool) *bool { return &v }

// KeepRecordingsFor reports how long a lossless recording is kept, and whether
// any retention limit applies at all.
func (c Config) KeepRecordingsFor() (int, bool) {
	if c.RetentionDays == nil || *c.RetentionDays <= 0 {
		return 0, false
	}
	return *c.RetentionDays, true
}

// PeakLimitDBFS is the level, in dBFS, that the finished audio is limited to.
// FFmpeg's alimiter accepts a ceiling no lower than -24 dBFS.
func (c MasteringConfig) PeakLimitDBFS() float64 {
	if c.PeakLimitDB == nil {
		return -1
	}
	return min(0, max(-24, *c.PeakLimitDB))
}

// GapBetweenSegments is the default silence, in seconds, inserted between
// segments of an exported MP3.
func (c MasteringConfig) GapBetweenSegments() float64 {
	if c.GapSeconds == nil {
		return 2
	}
	return ClampGapSeconds(*c.GapSeconds)
}

// MaximumGapSeconds bounds the silence between segments. Longer than half a
// minute reads as a fault rather than a pause.
const MaximumGapSeconds = 30.0

// ClampGapSeconds keeps a requested gap within what is useful for a service.
func ClampGapSeconds(seconds float64) float64 {
	return min(MaximumGapSeconds, max(0, seconds))
}

// MonoDownmix reports whether the export is reduced to a single channel.
func (c MasteringConfig) MonoDownmix() bool { return c.Mono == nil || *c.Mono }

// MP3QualityLevel is the LAME variable-bitrate level to encode with.
func (c MasteringConfig) MP3QualityLevel() int {
	if c.MP3Quality == nil {
		return 5
	}
	return min(9, max(0, *c.MP3Quality))
}

func LoadOrCreateConfig(path string) (Config, error) {
	defaults := DefaultConfig()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Config{}, err
		}
		encoded, _ := json.MarshalIndent(defaults, "", "  ")
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			return Config{}, fmt.Errorf("create config: %w", err)
		}
		return defaults, nil
	}
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	applyConfigDefaults(&config, defaults)
	return config, nil
}

func applyConfigDefaults(c *Config, d Config) {
	if c.Listen == "" {
		c.Listen = d.Listen
	}
	if c.FFmpeg == "" {
		c.FFmpeg = d.FFmpeg
	}
	if c.FFprobe == "" {
		c.FFprobe = d.FFprobe
	}
	if c.Church == "" {
		c.Church = d.Church
	}
	if c.Capture.Driver == "" {
		c.Capture.Driver = d.Capture.Driver
	}
	if c.Capture.Backend == "" {
		c.Capture.Backend = d.Capture.Backend
	}
	if c.Capture.SampleRate == 0 {
		c.Capture.SampleRate = d.Capture.SampleRate
	}
	if c.Capture.Channels == 0 {
		c.Capture.Channels = d.Capture.Channels
	}
	if c.Capture.PeriodMS == 0 {
		c.Capture.PeriodMS = d.Capture.PeriodMS
	}
	if c.Capture.BufferSecs == 0 {
		c.Capture.BufferSecs = d.Capture.BufferSecs
	}
	if len(c.Presets) == 0 {
		c.Presets = d.Presets
	}
	if c.Master.IntegratedLUFS == 0 {
		c.Master.IntegratedLUFS = d.Master.IntegratedLUFS
	}
	if c.Master.LoudnessRange == 0 {
		c.Master.LoudnessRange = d.Master.LoudnessRange
	}
	if c.Master.TruePeakDB == 0 {
		c.Master.TruePeakDB = d.Master.TruePeakDB
	}
	if c.Master.PeakLimitDB == nil {
		c.Master.PeakLimitDB = d.Master.PeakLimitDB
	}
	if c.Master.GapSeconds == nil {
		c.Master.GapSeconds = d.Master.GapSeconds
	}
	if c.Master.Mono == nil {
		c.Master.Mono = d.Master.Mono
	}
	if c.Master.MP3Quality == nil {
		c.Master.MP3Quality = d.Master.MP3Quality
	}
	if c.RetentionDays == nil {
		c.RetentionDays = d.RetentionDays
	}
}
