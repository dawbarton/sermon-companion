package capture

import (
	"reflect"
	"testing"

	"github.com/dawbarton/sermon-companion/internal/config"
)

func TestInputArgsArePlatformAdapters(t *testing.T) {
	tests := []struct {
		name  string
		input config.CaptureConfig
		want  []string
	}{
		{"Windows DirectShow", config.CaptureConfig{Driver: "dshow", Device: "HDMI Audio"}, []string{"-thread_queue_size", "1024", "-f", "dshow", "-i", "audio=HDMI Audio"}},
		{"macOS AVFoundation", config.CaptureConfig{Driver: "avfoundation", Device: "2"}, []string{"-thread_queue_size", "1024", "-f", "avfoundation", "-i", ":2"}},
		{"synthetic", config.CaptureConfig{Driver: "lavfi", Device: "sine=frequency=440"}, []string{"-re", "-f", "lavfi", "-i", "sine=frequency=440"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := InputArgs(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestDirectShowRequiresConfiguredDevice(t *testing.T) {
	if _, err := InputArgs(config.CaptureConfig{Driver: "dshow", Device: "CHANGE ME: device"}); err == nil {
		t.Fatal("expected a configuration error")
	}
}
