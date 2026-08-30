//go:build !cgo

package capture

import "errors"

func listMiniaudioDevices() ([]Device, error) {
	return nil, errors.New("miniaudio capture requires a cgo-enabled build")
}
