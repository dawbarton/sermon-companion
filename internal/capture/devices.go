package capture

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/dawbarton/sermon-companion/internal/config"
)

type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

// ErrDevicesNotEnumerable reports that the configured backend cannot produce a
// list the dock can offer as a dropdown. The FFmpeg backends describe their
// devices as free text meant for a person to read, and inventing structure from
// it would give the operator a list that quietly disagrees with what FFmpeg
// will actually open.
var ErrDevicesNotEnumerable = errors.New("the current capture backend does not provide a selectable device list")

// enumerationMu serialises device enumeration. Each call initialises its own
// miniaudio context, and there is no reason to have two of them interrogating
// the audio subsystem at once.
var enumerationMu sync.Mutex

// Available lists the capture devices the operator can choose between.
func Available(c config.Config) ([]Device, error) {
	if !strings.EqualFold(c.Capture.Backend, "miniaudio") {
		return nil, ErrDevicesNotEnumerable
	}
	enumerationMu.Lock()
	defer enumerationMu.Unlock()
	return listMiniaudioDevices()
}

func PrintDevices(c config.Config, output io.Writer) error {
	if strings.EqualFold(c.Capture.Backend, "miniaudio") {
		devices, err := Available(c)
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
