package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestConfigRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	for name, contents := range map[string]string{
		"unknown field": `{"retentionDay":0}`,
		"second value":  `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreateConfig(path); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestConfigRejectsUnsafeNumericRanges(t *testing.T) {
	for name, contents := range map[string]string{
		"retention overflow": `{"retentionDays":1000000}`,
		"capture allocation": `{"capture":{"bufferSeconds":1000000}}`,
		"invalid loudness":   `{"mastering":{"integratedLUFS":-100}}`,
		"invalid port":       `{"listen":"127.0.0.1:99999"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadOrCreateConfig(path)
			if err == nil || !strings.Contains(err.Error(), "config") {
				t.Fatalf("LoadOrCreateConfig() error = %v", err)
			}
		})
	}
}

func TestPublishSectionIsOptionalAndSurvivesWriteBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// A configuration without the section loads without it and is written back
	// without it, so an installation that does not upload never gains one.
	if err := os.WriteFile(path, []byte(`{"church":"Emmanuel Bishopston"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Get().Publish != nil {
		t.Fatal("a publish section appeared from nowhere")
	}
	if _, err := settings.Update(func(c *Config) error { c.Capture.Device = "Other"; return nil }); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(path)
	if strings.Contains(string(written), "publish") {
		t.Fatalf("write-back added a publish section:\n%s", written)
	}

	section := `{"publish":{"site":"https://example.org","user":"uploader","status":"private","podcast":false,` +
		`"programme":"C:\\Users\\av\\Programme.xlsx","programmeSheet":"Autumn","youtubeChannel":"UCReU3dVxdLKPuSs59dBgG6Q"}}`
	if err := os.WriteFile(path, []byte(section), 0o644); err != nil {
		t.Fatal(err)
	}
	settings, err = LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Update(func(c *Config) error { c.Capture.Device = "Other"; return nil }); err != nil {
		t.Fatal(err)
	}
	reread, err := LoadOrCreateConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	p := reread.Publish
	if p == nil || p.Site != "https://example.org" || p.User != "uploader" || p.PublishStatus() != "private" ||
		p.PodcastByDefault() || p.Programme != `C:\Users\av\Programme.xlsx` || p.ProgrammeSheet != "Autumn" ||
		p.YouTubeChannel != "UCReU3dVxdLKPuSs59dBgG6Q" || p.Type() != "Sermon" {
		t.Fatalf("publish section after write-back = %#v", p)
	}

	// The copy handed out is independent of the live settings.
	copied := settings.Get()
	*copied.Publish.Podcast = true
	copied.Publish.Site = "https://elsewhere.org"
	if again := settings.Get().Publish; again.Site != "https://example.org" || again.PodcastByDefault() {
		t.Fatalf("changing a copy altered the settings: %#v", again)
	}
}

func TestPublishSectionIsValidated(t *testing.T) {
	for name, section := range map[string]string{
		"plain http":    `{"site":"http://example.org","user":"u"}`,
		"no site":       `{"user":"u"}`,
		"no user":       `{"site":"https://example.org"}`,
		"status":        `{"site":"https://example.org","user":"u","status":"pending"}`,
		"programme":     `{"site":"https://example.org","user":"u","programme":"Programme.docx"}`,
		"channel":       `{"site":"https://example.org","user":"u","youtubeChannel":"@emmanuelbishopston"}`,
		"unknown field": `{"site":"https://example.org","user":"u","password":"secret"}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"publish":`+section+`}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateConfig(path); err == nil {
			t.Errorf("%s: accepted %s", name, section)
		}
	}
}

func TestPasswordIsReadBesideConfigWithoutSpaces(t *testing.T) {
	dir := t.TempDir()
	settings := NewSettings(filepath.Join(dir, "config.json"), DefaultConfig())
	if _, err := settings.Password(); err == nil || !strings.Contains(err.Error(), PasswordFileName) {
		t.Fatalf("a missing password file gave %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, PasswordFileName), []byte("abcd EFGH 1234\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	password, err := settings.Password()
	if err != nil || password != "abcdEFGH1234" {
		t.Fatalf("password = %q, %v", password, err)
	}
}
