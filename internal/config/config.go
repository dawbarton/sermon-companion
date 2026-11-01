package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/dawbarton/sermon-companion/internal/atomicfile"
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
	// Publish connects the review page to the church website. Without it the
	// application sends nothing anywhere and the upload button is not shown.
	Publish *PublishConfig `json:"publish,omitempty"`
}

// PublishConfig says where talks are uploaded and where their details come
// from. The application password is deliberately not here: it is read from
// PasswordFileName beside config.json, so the settings file can be opened and
// shown on screen without showing the secret.
type PublishConfig struct {
	// Site is the WordPress site's address, which must use HTTPS.
	Site string `json:"site"`
	// User is the WordPress account the application password belongs to.
	User string `json:"user"`
	// Status is what an upload is published as: "publish", "private", or
	// "draft". A private or draft upload is for testing.
	Status string `json:"status,omitempty"`
	// ResourceType is the site's name for the kind of resource a talk is.
	ResourceType string `json:"resourceType,omitempty"`
	// Podcast is whether a talk goes into the site's podcast feed unless the
	// operator says otherwise. A pointer distinguishes an omitted setting from a
	// deliberate false.
	Podcast *bool `json:"podcast,omitempty"`
	// Programme is the preaching programme spreadsheet the talk details are
	// filled in from, an .xlsx or .csv file. It may be empty.
	Programme string `json:"programme,omitempty"`
	// ProgrammeSheet names the worksheet to read; the first is used if empty.
	ProgrammeSheet string `json:"programmeSheet,omitempty"`
	// YouTubeChannel is the channel ID, beginning UC, whose latest video is
	// offered as the talk's video. It may be empty.
	YouTubeChannel string `json:"youtubeChannel,omitempty"`
}

// PasswordFileName is the file beside config.json holding the WordPress
// application password.
const PasswordFileName = "website-password.txt"

// PublishStatus is the status an upload is given.
func (p PublishConfig) PublishStatus() string {
	if strings.TrimSpace(p.Status) == "" {
		return "publish"
	}
	return strings.ToLower(strings.TrimSpace(p.Status))
}

// Type is the site's name for the kind of resource a talk is filed as.
func (p PublishConfig) Type() string {
	if strings.TrimSpace(p.ResourceType) == "" {
		return "Sermon"
	}
	return strings.TrimSpace(p.ResourceType)
}

// PodcastByDefault reports whether a talk goes into the podcast feed unless
// the operator says otherwise.
func (p PublishConfig) PodcastByDefault() bool { return p.Podcast == nil || *p.Podcast }

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

const (
	minimumSampleRate    = 8_000
	maximumSampleRate    = 384_000
	maximumChannels      = 32
	maximumPeriodMS      = 1_000
	maximumBufferSeconds = 300
	maximumRetentionDays = 36_500
)

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
	if err := validateConfig(defaults); err != nil {
		return Config{}, fmt.Errorf("invalid built-in configuration: %w", err)
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Config{}, err
		}
		encoded, _ := json.MarshalIndent(defaults, "", "  ")
		if err := atomicfile.Write(path, append(encoded, '\n'), 0o644); err != nil {
			return Config{}, fmt.Errorf("create config: %w", err)
		}
		return defaults, nil
	}
	if err != nil {
		return Config{}, err
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return Config{}, fmt.Errorf("read config: trailing content: %w", err)
	}
	applyConfigDefaults(&config, defaults)
	if err := validateConfig(config); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return config, nil
}

func validateConfig(c Config) error {
	backend := strings.ToLower(strings.TrimSpace(c.Capture.Backend))
	if backend != "miniaudio" && backend != "ffmpeg" {
		return fmt.Errorf("capture.backend must be %q or %q", "miniaudio", "ffmpeg")
	}
	if c.Capture.SampleRate < minimumSampleRate || c.Capture.SampleRate > maximumSampleRate {
		return fmt.Errorf("capture.sampleRate must be between %d and %d", minimumSampleRate, maximumSampleRate)
	}
	if c.Capture.Channels < 1 || c.Capture.Channels > maximumChannels {
		return fmt.Errorf("capture.channels must be between 1 and %d", maximumChannels)
	}
	if c.Capture.PeriodMS < 1 || c.Capture.PeriodMS > maximumPeriodMS {
		return fmt.Errorf("capture.periodMilliseconds must be between 1 and %d", maximumPeriodMS)
	}
	if c.Capture.BufferSecs < 1 || c.Capture.BufferSecs > maximumBufferSeconds {
		return fmt.Errorf("capture.bufferSeconds must be between 1 and %d", maximumBufferSeconds)
	}
	if backend == "ffmpeg" {
		driver := strings.ToLower(strings.TrimSpace(c.Capture.Driver))
		switch driver {
		case "dshow", "avfoundation", "lavfi":
		case "custom":
			if len(c.Capture.InputArgs) == 0 {
				return errors.New("capture.inputArgs is required for the custom driver")
			}
		default:
			return fmt.Errorf("capture.driver %q is not supported", c.Capture.Driver)
		}
	}
	if _, port, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen must be a host and port: %w", err)
	} else if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65_535 {
		return errors.New("listen port must be between 1 and 65535")
	}
	if strings.TrimSpace(c.FFmpeg) == "" || strings.TrimSpace(c.FFprobe) == "" {
		return errors.New("ffmpeg and ffprobe paths are required")
	}
	if len(c.Presets) == 0 {
		return errors.New("at least one preset is required")
	}
	seenPresets := make(map[string]struct{}, len(c.Presets))
	for index, preset := range c.Presets {
		kind := strings.ToLower(strings.TrimSpace(preset.Kind))
		if kind == "" || strings.TrimSpace(preset.Label) == "" {
			return fmt.Errorf("presets[%d] requires both kind and label", index)
		}
		if _, exists := seenPresets[kind]; exists {
			return fmt.Errorf("preset kind %q is duplicated", preset.Kind)
		}
		seenPresets[kind] = struct{}{}
	}
	if !finiteBetween(c.Master.IntegratedLUFS, -70, -5) {
		return errors.New("mastering.integratedLUFS must be between -70 and -5")
	}
	if !finiteBetween(c.Master.LoudnessRange, 1, 50) {
		return errors.New("mastering.loudnessRangeLU must be between 1 and 50")
	}
	if !finiteBetween(c.Master.TruePeakDB, -9, 0) {
		return errors.New("mastering.truePeakDB must be between -9 and 0")
	}
	if c.Master.PeakLimitDB != nil && !finiteBetween(*c.Master.PeakLimitDB, -24, 0) {
		return errors.New("mastering.peakLimitDB must be between -24 and 0")
	}
	if c.Master.GapSeconds != nil && !finiteBetween(*c.Master.GapSeconds, 0, MaximumGapSeconds) {
		return fmt.Errorf("mastering.gapSeconds must be between 0 and %g", MaximumGapSeconds)
	}
	if c.Master.MP3Quality != nil && (*c.Master.MP3Quality < 0 || *c.Master.MP3Quality > 9) {
		return errors.New("mastering.mp3Quality must be between 0 and 9")
	}
	if c.RetentionDays != nil && *c.RetentionDays > maximumRetentionDays {
		return fmt.Errorf("retentionDays must not exceed %d", maximumRetentionDays)
	}
	if c.Publish != nil {
		if err := validatePublish(*c.Publish); err != nil {
			return err
		}
	}
	return nil
}

func validatePublish(p PublishConfig) error {
	site, err := url.Parse(strings.TrimSpace(p.Site))
	if err != nil || site.Host == "" || site.Scheme != "https" {
		// The application password travels with every request, and WordPress
		// refuses one over plain HTTP anyway.
		return errors.New("publish.site must be the website's address, beginning https://")
	}
	if strings.TrimSpace(p.User) == "" {
		return errors.New("publish.user is required")
	}
	switch p.PublishStatus() {
	case "publish", "private", "draft":
	default:
		return fmt.Errorf("publish.status must be %q, %q, or %q", "publish", "private", "draft")
	}
	if p.Programme != "" {
		switch strings.ToLower(filepath.Ext(p.Programme)) {
		case ".xlsx", ".csv":
		default:
			return errors.New("publish.programme must be an .xlsx or .csv file")
		}
	}
	if channel := strings.TrimSpace(p.YouTubeChannel); channel != "" && !youTubeChannelPattern.MatchString(channel) {
		return errors.New("publish.youtubeChannel must be a channel ID, beginning UC and 24 characters long, not a handle such as @church")
	}
	return nil
}

var youTubeChannelPattern = regexp.MustCompile(`^UC[0-9A-Za-z_-]{22}$`)

func finiteBetween(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
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
