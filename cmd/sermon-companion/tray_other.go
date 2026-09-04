//go:build !windows && !darwin

package main

// The tray is built for the platforms this application is deployed on. Elsewhere
// it runs as an ordinary process that stops on an interrupt.

type trayActions struct {
	Review func()
	Log    func()
	Exited func()
}

func trayAvailable() bool { return false }

func runTray(trayActions) {}

func stopTray() {}
