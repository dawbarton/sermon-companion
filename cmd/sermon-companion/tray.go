//go:build windows || darwin

package main

import (
	"runtime"

	"fyne.io/systray"
)

// trayActions are what the icon offers. The application has no window of its
// own: the tray is the only thing an operator can click when it is not
// recording, so opening the review page must be the obvious action.
type trayActions struct {
	Review func()
	Log    func()
	Exited func()
}

func trayAvailable() bool { return true }

// runTray shows the icon and does not return until the tray has quit.
func runTray(actions trayActions) {
	systray.Run(func() {
		if runtime.GOOS == "windows" {
			systray.SetIcon(iconICO())
		} else {
			systray.SetIcon(iconPNG())
		}
		systray.SetTooltip("Sermon Companion")
		systray.SetOnTapped(actions.Review)
		review := systray.AddMenuItem("Review recordings", "Open the review and MP3 page in your web browser")
		messages := systray.AddMenuItem("Show log", "Open the messages this application has recorded")
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit Sermon Companion", "Stop recording and close the application")
		go func() {
			for {
				select {
				case <-review.ClickedCh:
					actions.Review()
				case <-messages.ClickedCh:
					actions.Log()
				case <-quit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, actions.Exited)
}

// stopTray asks the icon to go away, which lets runTray return.
func stopTray() { systray.Quit() }
