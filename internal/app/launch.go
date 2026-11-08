// Asking the operating system to open something for the operator: a folder, a
// page in the browser, or the settings file in an editor. The dock runs inside
// OBS's embedded browser and the application has no window, so these are how
// either can show the operator anything outside the page itself.

package app

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
)

// openReviewPage exists because the dock runs inside OBS's embedded browser,
// where an ordinary link would open in the dock panel itself.
func (s *Server) openReviewPage(w http.ResponseWriter, _ *http.Request) {
	url := LocalURL(s.settings.Get().Listen)
	if err := s.openLink(url); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("open the review page: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "opened", "url": url})
}

// openConfigFile hands config.json to a text editor. The application has no
// window and no console, so the review page is the only place an operator can
// be pointed at the file that decides how the next service records.
func (s *Server) openConfigFile(w http.ResponseWriter, _ *http.Request) {
	path := s.settings.Path()
	if path == "" {
		writeError(w, http.StatusNotFound, errors.New("this application is running without a configuration file"))
		return
	}
	if _, err := os.Stat(path); err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("the configuration file %s could not be opened: %w", path, err))
		return
	}
	if err := s.openEditor(path); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("open %s in an editor: %w", path, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "opened", "path": path})
}

// openFolder hands a directory to the operating system's file manager. These
// shell launchers are windowed programs rather than console ones, so unlike
// FFmpeg they need no internal/proc: hiding their window would hide the very
// window the operator asked for.
func openFolder(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer.exe", path)
	case "darwin":
		command = exec.Command("open", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	return start(command)
}

// openInEditor hands a text file to an editor the operator already has. Windows
// is sent to Notepad by name rather than through the file association, because
// .json belongs to whatever developer tool was installed last, or to nothing at
// all, and the operator would be asked to choose a program instead of being
// shown their settings. Like openFolder these are windowed programs, so they are
// started directly rather than through internal/proc, whose purpose is hiding a
// console window; here the window is the point.
func openInEditor(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("notepad.exe", path)
	case "darwin":
		// -t opens the default editor for plain text rather than the
		// application registered for .json.
		command = exec.Command("open", "-t", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	return start(command)
}

// OpenInBrowser opens a local page in the operator's default web browser.
func OpenInBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return start(command)
}

// LocalURL turns a listen address into a page address an operator can open,
// substituting the loopback host for a wildcard or omitted one.
func LocalURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

func start(command *exec.Cmd) error {
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
