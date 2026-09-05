//go:build !windows

package proc

import "os/exec"

// hideConsole does nothing away from Windows, where starting a child process
// never creates a window.
func hideConsole(command *exec.Cmd) {}
