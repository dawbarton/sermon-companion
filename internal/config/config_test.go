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

func TestMasteringGapAndPeakLimitDefaults(t *testing.T) {
	d := DefaultConfig().Master
	if d.PeakLimitDBFS() != -1 || d.GapBetweenSegments() != 2 || !d.MonoDownmix() {
		t.Fatalf("mastering defaults = %g dBFS, %g s, mono=%v", d.PeakLimitDBFS(), d.GapBetweenSegments(), d.MonoDownmix())
	}

	// A config written before either setting existed keeps the defaults, while a
	// deliberate zero is honoured rather than read as an omission.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mastering":{"mp3Quality":7}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	older, err := LoadOrCreateConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !older.Master.MonoDownmix() {
		t.Fatal("an older config lost the mono downmix")
	}
	if older.Master.PeakLimitDBFS() != -1 || older.Master.GapBetweenSegments() != 2 {
		t.Fatalf("upgraded config = %g dBFS, %g s", older.Master.PeakLimitDBFS(), older.Master.GapBetweenSegments())
	}
	zeroed := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(zeroed, []byte(`{"mastering":{"peakLimitDB":0,"gapSeconds":0,"mono":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	explicit, err := LoadOrCreateConfig(zeroed)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Master.PeakLimitDBFS() != 0 || explicit.Master.GapBetweenSegments() != 0 || explicit.Master.MonoDownmix() {
		t.Fatalf("explicit settings = %g dBFS, %g s, mono=%v", explicit.Master.PeakLimitDBFS(), explicit.Master.GapBetweenSegments(), explicit.Master.MonoDownmix())
	}

	// Values outside what FFmpeg's limiter accepts, or beyond a sensible pause,
	// are brought back into range rather than failing an export hours later.
	deep := MasteringConfig{PeakLimitDB: floatPointer(-96), GapSeconds: floatPointer(600)}
	if deep.PeakLimitDBFS() != -24 || deep.GapBetweenSegments() != MaximumGapSeconds {
		t.Fatalf("clamped = %g dBFS, %g s", deep.PeakLimitDBFS(), deep.GapBetweenSegments())
	}
	if ClampGapSeconds(-5) != 0 {
		t.Fatalf("ClampGapSeconds(-5) = %g", ClampGapSeconds(-5))
	}
}
