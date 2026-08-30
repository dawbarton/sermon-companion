package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExistingConfigGetsChurchDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"listen":"127.0.0.1:9999"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadOrCreateConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Church != "Church" {
		t.Fatalf("church default = %q", c.Church)
	}
	if c.Capture.Backend != "miniaudio" || c.Capture.PeriodMS != 20 || c.Capture.BufferSecs != 10 {
		t.Fatalf("unexpected native capture defaults: %#v", c.Capture)
	}
}
