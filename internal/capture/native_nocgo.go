//go:build !cgo

package capture

import (
	"errors"
	"os"

	"github.com/dawbarton/sermon-companion/internal/config"
)

func startMiniaudioCapture(config.Config, string, *os.File) (activeCapture, error) {
	return nil, errors.New("miniaudio capture requires a cgo-enabled build")
}
