package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/dawbarton/sermon-companion/internal/app"
	"github.com/dawbarton/sermon-companion/internal/capture"
	"github.com/dawbarton/sermon-companion/internal/config"
	"github.com/dawbarton/sermon-companion/internal/master"
	"github.com/dawbarton/sermon-companion/internal/store"
)

// version is replaced at release build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	defaultData := defaultDataDir()
	dataDir := flag.String("data-dir", defaultData, "directory for configuration and recordings")
	configPath := flag.String("config", "", "configuration file (default: DATA-DIR/config.json)")
	demo := flag.Bool("demo", false, "capture a synthetic tone instead of an audio device")
	listDevices := flag.Bool("list-devices", false, "list available capture devices")
	noOpen := flag.Bool("no-open", false, "do not open the review page in a browser")
	showVersion := flag.Bool("version", false, "print the application version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("Sermon Companion %s\n", version)
		return
	}

	if *configPath == "" {
		*configPath = filepath.Join(*dataDir, "config.json")
	}
	c, err := config.LoadOrCreateConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	resolveBundledFFmpeg(&c)
	if *demo {
		c.Capture.Backend = "ffmpeg"
		c.Capture.Driver, c.Capture.Device = "lavfi", "sine=frequency=440:sample_rate=48000"
	}
	if *listDevices {
		if err := capture.PrintDevices(c, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}

	sessions, err := store.New(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	captureManager := capture.New(c, sessions)
	if err := captureManager.RecoverInterrupted(); err != nil {
		log.Printf("recover interrupted recordings: %v", err)
	}
	mastering := master.New(c, sessions)
	server := app.NewServer(c, sessions, captureManager, mastering, app.StaticFiles)
	httpServer := &http.Server{Addr: c.Listen, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		log.Printf("Sermon Companion is ready at http://%s/ (OBS dock: http://%s/dock)", c.Listen, c.Listen)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	if !*noOpen {
		go openBrowser("http://" + c.Listen + "/")
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals
	if _, _, _, active := captureManager.Active(); active {
		log.Print("stopping active recording safely")
		if _, err := captureManager.Stop(); err != nil {
			log.Printf("stop recording: %v", err)
		}
	}
	_ = httpServer.Close()
}

func defaultDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "sermon-companion-data"
	}
	return filepath.Join(dir, "Sermon Companion")
}

func openBrowser(url string) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Open %s in a browser.\n", url)
	}
}

func resolveBundledFFmpeg(c *config.Config) {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(executable)
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

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
