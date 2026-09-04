package main

import (
	"syscall"
	"unsafe"
)

// showFatalMessage puts a problem in front of the operator. The application is
// built without a console on Windows, so a message written to standard error
// would be seen by nobody.
func showFatalMessage(message string) {
	const mbIconError = 0x00000010
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	title, titleErr := syscall.UTF16PtrFromString("Sermon Companion")
	body, bodyErr := syscall.UTF16PtrFromString(message)
	if titleErr != nil || bodyErr != nil {
		return
	}
	_, _, _ = messageBox.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), mbIconError)
}
