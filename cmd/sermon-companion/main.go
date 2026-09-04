package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dawbarton/sermon-companion/internal/app"
	"github.com/dawbarton/sermon-companion/internal/applog"
	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

// version is replaced at release build time with -ldflags "-X main.version=…".
var version = "dev"

// applicationDirectory is the folder the recordings and settings live in,
// beneath the platform's own location for application data.
const applicationDirectory = "Sermon Companion"

func main() {
	defaultData := defaultDataDir()
	dataDir := flag.String("data-dir", defaultData, "directory for configuration and recordings")
	configPath := flag.String("config", "", "configuration file (default: DATA-DIR/config.json)")
	demo := flag.Bool("demo", false, "capture a synthetic tone instead of an audio device")
	listDevices := flag.Bool("list-devices", false, "list available capture devices")
	openReview := flag.Bool("open", false, "open the review page in a browser at start-up")
	noTray := flag.Bool("no-tray", false, "run without a system tray icon")
	// The review page is no longer opened at start-up, so this flag has nothing
	// left to suppress. It is still accepted so that an existing shortcut or
	// scheduled task does not fail to start with no console to report why.
	_ = flag.Bool("no-open", false, "accepted and ignored; the review page is no longer opened automatically")
	showVersion := flag.Bool("version", false, "print the application version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("Sermon Companion %s\n", version)
		return
	}

	messages, logErr := applog.New(*dataDir)
	log.SetOutput(io.MultiWriter(os.Stderr, messages))
	defer messages.Close()
	log.Printf("Sermon Companion %s starting; data directory %s", version, *dataDir)
	if logErr != nil {
		log.Printf("the log file could not be opened, so messages are kept only until this application closes: %v", logErr)
	}
	reportLegacyDataDir(*dataDir)

	if *configPath == "" {
		*configPath = filepath.Join(*dataDir, "config.json")
	}
	settings, err := config.LoadSettings(*configPath)
	if err != nil {
		fail(err.Error())
	}
	settings.SetRuntimeOverrides(runtimeOverrides(*demo))

	if *listDevices {
		if err := capture.PrintDevices(settings.Get(), os.Stdout); err != nil {
			fail(err.Error())
		}
		return
	}

	c := settings.Get()
	reviewURL := app.LocalURL(c.Listen)
	// Bind before anything else is set up. With no console window, a second
	// copy started by mistake would otherwise fail invisibly; showing the
	// operator the review page is what they were reaching for anyway.
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		if alreadyRunning(reviewURL) {
			log.Printf("Sermon Companion is already running; opening %s", reviewURL)
			_ = app.OpenInBrowser(reviewURL)
			return
		}
		fail(fmt.Sprintf("Sermon Companion cannot listen on %s: %v", c.Listen, err))
	}

	sessions, err := store.New(*dataDir)
	if err != nil {
		fail(err.Error())
	}
	captureManager := capture.New(settings, sessions)
	if err := captureManager.RecoverInterrupted(); err != nil {
		log.Printf("recover interrupted recordings: %v", err)
	}
	if deleted, err := captureManager.ApplyRetention(time.Now()); err != nil {
		log.Printf("apply recording retention: %v", err)
	} else if len(deleted) > 0 {
		days, _ := c.KeepRecordingsFor()
		log.Printf("deleted %d service(s) older than %d days: %s", len(deleted), days, strings.Join(deleted, ", "))
	}
	reportCaptureDevice(settings.Get())

	mastering := master.New(c, sessions)
	server := app.NewServer(settings, sessions, captureManager, mastering, app.StaticFiles)
	server.SetLog(messages)
	httpServer := &http.Server{Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("Sermon Companion is ready at %s (OBS dock: %sdock)", reviewURL, reviewURL)
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("local server stopped: %v", err)
		}
	}()
	if *openReview {
		go func() {
			if err := app.OpenInBrowser(reviewURL); err != nil {
				log.Printf("open %s in a browser: %v", reviewURL, err)
			}
		}()
	}

	finished := make(chan struct{})
	var once sync.Once
	quit := func() { once.Do(func() { close(finished) }) }

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		log.Print("closing at the operating system's request")
		stopTray()
		quit()
	}()

	useTray := trayAvailable() && !*noTray
	if useTray {
		runTray(trayActions{
			Review: func() { openPage(reviewURL) },
			Log:    func() { openPage(reviewURL + "log") },
			Exited: quit,
		})
		<-finished
	} else {
		log.Print("running without a system tray icon; interrupt to stop")
		<-finished
	}

	if _, _, _, active := captureManager.Active(); active {
		log.Print("stopping active recording safely")
		if _, err := captureManager.Stop(); err != nil {
			log.Printf("stop recording: %v", err)
		}
	}
	_ = httpServer.Close()
	log.Print("Sermon Companion has closed")
}

func openPage(url string) {
	if err := app.OpenInBrowser(url); err != nil {
		log.Printf("open %s in a browser: %v", url, err)
	}
}

// alreadyRunning asks whether the address in use is answering as another copy
// of this application rather than as some unrelated program.
func alreadyRunning(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(url + "api/status")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<10))
	return response.StatusCode == http.StatusOK
}

// fail reports a problem that stops the application before it can serve
// anything. There may be no console to print to, so it is written to the log
// file and, on Windows, shown to the operator directly.
func fail(message string) {
	log.Print(message)
	showFatalMessage(message)
	os.Exit(1)
}

func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		// A service is roughly 500 MB. The roaming profile is copied between
		// machines and synchronised by some domain configurations, which is not
		// somewhere to put recordings, so the local profile is used instead.
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, applicationDirectory)
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "sermon-companion-data"
	}
	return filepath.Join(dir, applicationDirectory)
}

// reportLegacyDataDir notes settings and recordings left where versions before
// 0.6.0 kept them on Windows. Nothing is moved automatically: the recordings are
// large, and which of them are still wanted is the operator's decision.
func reportLegacyDataDir(current string) {
	if runtime.GOOS != "windows" {
		return
	}
	roaming := os.Getenv("APPDATA")
	if roaming == "" {
		return
	}
	previous := filepath.Join(roaming, applicationDirectory)
	if previous == current {
		return
	}
	if _, err := os.Stat(previous); err != nil {
		return
	}
	log.Printf("settings and recordings from an earlier version are still in %s; this version uses %s. Copy across anything still wanted, then delete the old folder.", previous, current)
}

func reportCaptureDevice(c config.Config) {
	if !strings.EqualFold(c.Capture.Backend, "miniaudio") {
		log.Printf("capture backend %q, device %q", c.Capture.Backend, c.Capture.Device)
		return
	}
	devices, err := capture.Available(c)
	if err != nil {
		log.Printf("capture devices could not be listed: %v", err)
		return
	}
	log.Printf("%d capture device(s) available; configured device is %q", len(devices), c.Capture.Device)
}

// runtimeOverrides are the values worked out at start-up rather than configured.
// They apply to this run alone and are deliberately not written to config.json,
// where an absolute path would be wrong as soon as the folder moved.
func runtimeOverrides(demo bool) func(*config.Config) {
	executable, err := os.Executable()
	dir := ""
	if err == nil {
		dir = filepath.Dir(executable)
	}
	return func(c *config.Config) {
		if dir != "" {
			ffmpegName, ffprobeName := "ffmpeg", "ffprobe"
			if runtime.GOOS == "windows" {
				ffmpegName, ffprobeName = "ffmpeg.exe", "ffprobe.exe"
			}
			if c.FFmpeg == "ffmpeg" {
				if path := filepath.Join(dir, ffmpegName); fileExists(path) {
					c.FFmpeg = path
				}
			}
			if c.FFprobe == "ffprobe" {
				if path := filepath.Join(dir, ffprobeName); fileExists(path) {
					c.FFprobe = path
				}
			}
		}
		if demo {
			c.Capture.Backend = "ffmpeg"
			c.Capture.Driver, c.Capture.Device = "lavfi", "sine=frequency=440:sample_rate=48000"
		}
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
