package capture

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/dawbarton/sermon-companion/internal/config"
)

type Device struct {
	ID        string
	Name      string
	IsDefault bool
}

func PrintDevices(c config.Config, output io.Writer) error {
	if strings.EqualFold(c.Capture.Backend, "miniaudio") {
		devices, err := listMiniaudioDevices()
		if err != nil {
			return err
		}
		fmt.Fprintln(output, "Capture devices:")
		for _, device := range devices {
			defaultText := ""
			if device.IsDefault {
				defaultText = " (default)"
			}
			fmt.Fprintf(output, "  %s%s\n    id: %s\n", device.Name, defaultText, device.ID)
		}
		return nil
	}
	command := exec.Command(c.FFmpeg, DeviceListArgs()...)
	command.Stdout, command.Stderr = output, output
	_ = command.Run() // Device-list commands conventionally return a non-zero status after listing.
	return nil
}
