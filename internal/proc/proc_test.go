package proc

import (
	"context"
	"runtime"
	"testing"
)

// TestCommandHidesConsoleOnWindows guards the reason this package exists: a
// child console program started from the windowless application must not be
// given a console window of its own.
func TestCommandHidesConsoleOnWindows(t *testing.T) {
	command := Command("ffmpeg", "-version")
	if got := command.Args; len(got) != 2 || got[1] != "-version" {
		t.Fatalf("unexpected arguments %v", got)
	}
	if runtime.GOOS != "windows" {
		if command.SysProcAttr != nil {
			t.Fatalf("process attributes should be untouched away from Windows")
		}
		return
	}
	if command.SysProcAttr == nil {
		t.Fatalf("process attributes were not set on Windows")
	}
}

func TestCommandContextHidesConsoleOnWindows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := CommandContext(ctx, "ffmpeg", "-version")
	if (runtime.GOOS == "windows") != (command.SysProcAttr != nil) {
		t.Fatalf("process attributes do not match the platform")
	}
}
