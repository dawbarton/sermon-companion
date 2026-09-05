package proc

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Windows CREATE_NO_WINDOW process creation flag. It runs
// a console program with no console at all, which is what a background service
// wants: HideWindow alone would still create one and merely hide it, leaving the
// child attached to a console that can be closed underneath it.
const createNoWindow = 0x08000000

func hideConsole(command *exec.Cmd) {
	attributes := command.SysProcAttr
	if attributes == nil {
		attributes = &syscall.SysProcAttr{}
		command.SysProcAttr = attributes
	}
	attributes.HideWindow = true
	attributes.CreationFlags |= createNoWindow
}
