package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsUpdateSavesAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Update(func(c *Config) error {
		c.Capture.DeviceID, c.Capture.Device = "device-id", "HDMI capture"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := settings.Get().Capture.DeviceID; got != "device-id" {
		t.Fatalf("live device ID = %q, want %q", got, "device-id")
	}
	reloaded, err := LoadOrCreateConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Capture.DeviceID != "device-id" || reloaded.Capture.Device != "HDMI capture" {
		t.Fatalf("saved device = %q/%q, want device-id/HDMI capture", reloaded.Capture.DeviceID, reloaded.Capture.Device)
	}
}

// A rejected change must leave both the running application and the file as
// they were, so a refused device cannot half-apply.
func TestSettingsUpdateKeepsPreviousValueOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	before := settings.Get().Church
	if _, err := settings.Update(func(c *Config) error {
		c.Church = "Changed"
		return os.ErrInvalid
	}); err == nil {
		t.Fatal("expected the rejected change to be reported")
	}
	if got := settings.Get().Church; got != before {
		t.Fatalf("church = %q after a rejected change, want %q", got, before)
	}
	reloaded, _ := LoadOrCreateConfig(path)
	if reloaded.Church != before {
		t.Fatalf("saved church = %q, want %q", reloaded.Church, before)
	}
}

// A path worked out at start-up applies to the running application but must not
// reach config.json, where it would be wrong once the folder moved.
func TestRuntimeOverridesAreNotSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	settings.SetRuntimeOverrides(func(c *Config) { c.FFmpeg = "/opt/app/ffmpeg" })
	if got := settings.Get().FFmpeg; got != "/opt/app/ffmpeg" {
		t.Fatalf("live FFmpeg = %q, want the resolved path", got)
	}
	if _, err := settings.Update(func(c *Config) error { c.Capture.DeviceID = "chosen"; return nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.FFmpeg != "ffmpeg" {
		t.Fatalf("saved FFmpeg = %q, want the configured value to be left alone", saved.FFmpeg)
	}
	if saved.Capture.DeviceID != "chosen" {
		t.Fatalf("saved device ID = %q, want chosen", saved.Capture.DeviceID)
	}
}

// Get must hand back an independent copy, or a caller could alter the live
// configuration by writing through a shared slice.
func TestGetDoesNotShareState(t *testing.T) {
	settings := NewSettings("", DefaultConfig())
	taken := settings.Get()
	taken.Presets[0].Label = "Altered"
	*taken.RetentionDays = 999
	fresh := settings.Get()
	if fresh.Presets[0].Label == "Altered" {
		t.Fatal("presets are shared with the caller")
	}
	if *fresh.RetentionDays == 999 {
		t.Fatal("retention is shared with the caller")
	}
}
