package capture

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/dawbarton/sermon-companion/internal/config"
)

func InputArgs(c config.CaptureConfig) ([]string, error) {
	switch strings.ToLower(c.Driver) {
	case "dshow":
		if strings.TrimSpace(c.Device) == "" || strings.HasPrefix(c.Device, "CHANGE ME") {
			return nil, errors.New("select the Windows HDMI audio device in config.json")
		}
		return []string{"-thread_queue_size", "1024", "-f", "dshow", "-i", "audio=" + c.Device}, nil
	case "avfoundation":
		device := c.Device
		if device == "" {
			device = "default"
		}
		return []string{"-thread_queue_size", "1024", "-f", "avfoundation", "-i", ":" + device}, nil
	case "lavfi":
		source := c.Device
		if source == "" {
			source = "sine=frequency=440:sample_rate=48000"
		}
		return []string{"-re", "-f", "lavfi", "-i", source}, nil
	case "custom":
		if len(c.InputArgs) == 0 {
			return nil, errors.New("custom capture requires inputArgs")
		}
		return append([]string(nil), c.InputArgs...), nil
	default:
		return nil, fmt.Errorf("unknown capture driver %q", c.Driver)
	}
}

func DeviceListArgs() []string {
	if runtime.GOOS == "windows" {
		return []string{"-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy"}
	}
	if runtime.GOOS == "darwin" {
		return []string{"-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", ""}
	}
	return []string{"-hide_banner", "-sources"}
}
