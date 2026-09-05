// Package proc starts the helper programs the application depends on. The
// application itself has no console, so a child console program such as FFmpeg
// or FFprobe would be given a console window of its own: an empty black window
// appears whenever a recording starts, and closing it takes the recording with
// it. Every child process therefore has to be started through this package
// rather than through os/exec directly.
package proc

import (
	"context"
	"os/exec"
)

// Command returns an exec.Cmd that runs without a console window.
func Command(name string, args ...string) *exec.Cmd {
	command := exec.Command(name, args...)
	hideConsole(command)
	return command
}

// CommandContext is Command with the context cancellation of exec.CommandContext.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...)
	hideConsole(command)
	return command
}
