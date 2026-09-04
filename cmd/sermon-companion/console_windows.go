package main

import (
	"os"
	"syscall"
)

// attachParentConsole gives the application somewhere to print when it was run
// from a command prompt. The Windows executable is linked without a console so
// that double-clicking it does not open a black window beside the tray icon,
// which also leaves --version and --list-devices with nowhere to write. Where
// the output is already directed somewhere, as it is when another program
// captures it, nothing is changed.
func attachParentConsole() {
	if handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil && handle != 0 && handle != syscall.InvalidHandle {
		return
	}
	const attachParentProcess = ^uintptr(0)
	attach := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	if result, _, _ := attach.Call(attachParentProcess); result == 0 {
		return // Started from Explorer or the tray: there is no console to use.
	}
	console, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	os.Stdout, os.Stderr = console, console
}
