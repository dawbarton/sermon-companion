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
	// Closing is called when the operator asks the application to close. It
	// must be safe to call more than once.
	Closing func()
}

func trayAvailable() bool { return true }

// runTray shows the icon and does not return until the tray has quit. Its
// return is what says the application is closing: systray's own exit callback
// cannot carry that on its own, because macOS reaches this point by stopping
// the run loop, and the callback runs there only when the process is actually
// terminating.
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
					// Say that the application is closing here, where the
					// operator asked for it, rather than relying on a callback
					// that only one of the two platforms invokes.
					actions.Closing()
					systray.Quit()
					return
				}
			}
		}()
	}, actions.Closing)
}

// stopTray asks the icon to go away, which lets runTray return.
func stopTray() { systray.Quit() }
