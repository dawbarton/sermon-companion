package applog

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteKeepsTailAndFile(t *testing.T) {
	dir := t.TempDir()
	messages, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer messages.Close()
	logger := log.New(messages, "", 0)
	logger.Print("first")
	logger.Print("second")
	tail := messages.Tail(0)
	if len(tail) != 2 || tail[0] != "first" || tail[1] != "second" {
		t.Fatalf("tail = %q, want the two messages in order", tail)
	}
	data, err := os.ReadFile(filepath.Join(dir, "logs", "sermon-companion.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first\nsecond\n" {
		t.Fatalf("file = %q, want both messages", data)
	}
}

func TestTailIsBounded(t *testing.T) {
	messages, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer messages.Close()
	for index := 0; index < TailLines+50; index++ {
		fmt.Fprintf(messages, "line %d\n", index)
	}
	tail := messages.Tail(0)
	if len(tail) != TailLines {
		t.Fatalf("tail length = %d, want %d", len(tail), TailLines)
	}
	if tail[len(tail)-1] != fmt.Sprintf("line %d", TailLines+49) {
		t.Fatalf("last line = %q, want the most recent", tail[len(tail)-1])
	}
	if tail[0] != "line 50" {
		t.Fatalf("first line = %q, want the oldest retained", tail[0])
	}
}

// A write that does not end in a newline belongs to the line that follows it,
// not to a line of its own.
func TestPartialLinesAreJoined(t *testing.T) {
	messages, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer messages.Close()
	fmt.Fprint(messages, "half a ")
	if len(messages.Tail(0)) != 0 {
		t.Fatal("an unfinished line was recorded before its newline")
	}
	fmt.Fprint(messages, "line\n")
	if tail := messages.Tail(0); len(tail) != 1 || tail[0] != "half a line" {
		t.Fatalf("tail = %q, want the joined line", tail)
	}
}

func TestRotationKeepsOnePreviousFile(t *testing.T) {
	dir := t.TempDir()
	messages, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer messages.Close()
	block := strings.Repeat("x", 4096) + "\n"
	for written := 0; written < MaxFileBytes+len(block); written += len(block) {
		fmt.Fprint(messages, block)
	}
	current, err := os.Stat(filepath.Join(dir, "logs", "sermon-companion.log"))
	if err != nil {
		t.Fatal(err)
	}
	if current.Size() >= MaxFileBytes {
		t.Fatalf("current log is %d bytes, want it rotated below %d", current.Size(), MaxFileBytes)
	}
	if _, err := os.Stat(filepath.Join(dir, "logs", "sermon-companion.log.1")); err != nil {
		t.Fatalf("previous log was not kept: %v", err)
	}
}

// Losing the file copy must not cost the messages the interface can show, since
// that is what an operator is asked to look at first.
func TestTailSurvivesAnUnopenableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logs"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	messages, err := New(dir)
	if err == nil {
		t.Fatal("expected the unusable log location to be reported")
	}
	fmt.Fprint(messages, "still recorded\n")
	if tail := messages.Tail(0); len(tail) != 1 || tail[0] != "still recorded" {
		t.Fatalf("tail = %q, want the message kept in memory", tail)
	}
	if messages.Path() != "" {
		t.Fatalf("path = %q, want no file to be claimed", messages.Path())
	}
}

// Standard error is not connected on Windows, where the application is linked
// without a console. If that failure could stop the message reaching the log,
// the log page and the log file would both be empty in the ordinary case of an
// operator double-clicking the application.
func TestTeeKeepsTheLogWhenTheConsoleFails(t *testing.T) {
	messages, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer messages.Close()
	logger := log.New(Tee(messages, failingWriter{}), "", 0)
	logger.Print("recorded despite the missing console")
	tail := messages.Tail(0)
	if len(tail) != 1 || tail[0] != "recorded despite the missing console" {
		t.Fatalf("tail = %q, want the message kept", tail)
	}
}

func TestTeeAlsoWritesToTheConsole(t *testing.T) {
	messages, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer messages.Close()
	var console strings.Builder
	logger := log.New(Tee(messages, &console), "", 0)
	logger.Print("shown in both places")
	if got := strings.TrimSpace(console.String()); got != "shown in both places" {
		t.Fatalf("console got %q, want the message", got)
	}
	if tail := messages.Tail(0); len(tail) != 1 {
		t.Fatalf("tail = %q, want the message", tail)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("the handle is invalid") }
