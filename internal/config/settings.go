package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Settings holds the configuration the running application is using and, when
// it came from a file, writes changes back to that file. Configuration used to
// be a value read once at start-up; the capture device is now chosen in the OBS
// dock, so the selection has to reach both the capture manager and config.json.
type Settings struct {
	mu   sync.RWMutex
	path string
	// saved is what config.json should contain; live is that with the values
	// resolved at start-up applied over it. Keeping the two apart means a
	// device chosen in the dock does not also write the launcher's idea of
	// where ffmpeg.exe lives into the file, which would then be wrong as soon
	// as the application folder moved.
	saved     Config
	live      Config
	overrides func(*Config)
}

// NewSettings wraps an already loaded configuration. An empty path keeps the
// settings in memory alone, which is what the tests use.
func NewSettings(path string, c Config) *Settings {
	return &Settings{path: path, saved: c.clone(), live: c.clone()}
}

// LoadSettings reads config.json, creating it with the defaults on first run.
func LoadSettings(path string) (*Settings, error) {
	c, err := LoadOrCreateConfig(path)
	if err != nil {
		return nil, err
	}
	return NewSettings(path, c), nil
}

// Path is the configuration file the settings are saved to, or an empty string
// for in-memory settings.
func (s *Settings) Path() string { return s.path }

// SetRuntimeOverrides records values the application resolved for itself at
// start-up, such as an FFmpeg found beside the executable. They apply to the
// running application but are never written to config.json.
func (s *Settings) SetRuntimeOverrides(apply func(*Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrides = apply
	s.refreshLive()
}

// refreshLive rebuilds the effective configuration. The caller holds the lock.
func (s *Settings) refreshLive() {
	s.live = s.saved.clone()
	if s.overrides != nil {
		s.overrides(&s.live)
	}
}

// Get returns an independent copy of the current configuration, so a caller
// cannot alter the live settings by writing through a shared slice or pointer.
func (s *Settings) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.live.clone()
}

// Update applies a change and saves it. The change takes effect only if it is
// both accepted and safely written, so a failed save cannot leave the running
// application disagreeing with config.json.
func (s *Settings) Update(change func(*Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := s.saved.clone()
	if err := change(&candidate); err != nil {
		return s.live.clone(), err
	}
	if s.path != "" {
		if err := saveConfig(s.path, candidate); err != nil {
			return s.live.clone(), err
		}
	}
	s.saved = candidate
	s.refreshLive()
	return s.live.clone(), nil
}

// saveConfig replaces config.json atomically. A power loss during a device
// change must leave the previous configuration intact rather than a truncated
// file the application cannot start from.
func saveConfig(path string, c Config) error {
	encoded, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func (c Config) clone() Config {
	out := c
	if c.Capture.InputArgs != nil {
		out.Capture.InputArgs = append([]string(nil), c.Capture.InputArgs...)
	}
	if c.Presets != nil {
		out.Presets = append([]Preset(nil), c.Presets...)
	}
	out.RetentionDays = clonePointer(c.RetentionDays)
	out.Master.PeakLimitDB = clonePointer(c.Master.PeakLimitDB)
	out.Master.GapSeconds = clonePointer(c.Master.GapSeconds)
	out.Master.Mono = clonePointer(c.Master.Mono)
	out.Master.MP3Quality = clonePointer(c.Master.MP3Quality)
	return out
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
