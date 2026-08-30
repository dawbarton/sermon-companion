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
}

type CaptureConfig struct {
	Driver     string   `json:"driver"`
	Device     string   `json:"device"`
	InputArgs  []string `json:"inputArgs,omitempty"`
	SampleRate int      `json:"sampleRate"`
	Channels   int      `json:"channels"`
}

type Preset struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type MasteringConfig struct {
	IntegratedLUFS float64 `json:"integratedLUFS"`
	LoudnessRange  float64 `json:"loudnessRangeLU"`
	TruePeakDB     float64 `json:"truePeakDB"`
	MP3Bitrate     string  `json:"mp3Bitrate"`
}

func DefaultConfig() Config {
	driver := "avfoundation"
	device := "default"
	if runtime.GOOS == "windows" {
		driver = "dshow"
		device = "CHANGE ME: HDMI capture audio device"
	}
	return Config{
		Listen:  "127.0.0.1:8765",
		FFmpeg:  "ffmpeg",
		FFprobe: "ffprobe",
		Church:  "Church",
		Capture: CaptureConfig{Driver: driver, Device: device, SampleRate: 48000, Channels: 2},
		Presets: []Preset{{Kind: "reading", Label: "Reading"}, {Kind: "sermon", Label: "Sermon"}, {Kind: "questions", Label: "Q&A"}},
		Master:  MasteringConfig{IntegratedLUFS: -16, LoudnessRange: 11, TruePeakDB: -1.5, MP3Bitrate: "128k"},
	}
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
	if c.Capture.SampleRate == 0 {
		c.Capture.SampleRate = d.Capture.SampleRate
	}
	if c.Capture.Channels == 0 {
		c.Capture.Channels = d.Capture.Channels
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
	if c.Master.MP3Bitrate == "" {
		c.Master.MP3Bitrate = d.Master.MP3Bitrate
	}
}
