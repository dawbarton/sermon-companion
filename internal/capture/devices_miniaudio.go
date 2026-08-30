//go:build cgo

package capture

import (
	"fmt"

	"github.com/gen2brain/malgo"
)

func listMiniaudioDevices() ([]Device, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("initialise miniaudio: %w", err)
	}
	defer func() {
		_ = context.Uninit()
		context.Free()
	}()
	infos, err := context.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("list miniaudio capture devices: %w", err)
	}
	devices := make([]Device, 0, len(infos))
	for index := range infos {
		devices = append(devices, Device{ID: infos[index].ID.String(), Name: infos[index].Name(), IsDefault: infos[index].IsDefault != 0})
	}
	return devices, nil
}
