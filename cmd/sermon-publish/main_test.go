// Tests for the command's own checks: required flags, the refusal to do more
// than plan at this stage, and a talk file whose typos are caught.
package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresEveryFlagAndOnlyPlans(t *testing.T) {
	err := run([]string{"-site", "https://example.org"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "is required") {
		t.Errorf("error = %v", err)
	}
	full := []string{"-site", "https://example.org", "-user", "u", "-password-file", "p", "-session", "s", "-talk", "t", "-status", "private"}
	err = run(full, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "only -dry-run") {
		t.Errorf("without -dry-run nothing should be attempted yet: %v", err)
	}
}

func TestReadTalkCatchesTypos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "talk.json")
	os.WriteFile(path, []byte(`{"title":"The Lost Sheep","speaker":"Ian Taylor","newSpeeker":true}`), 0o644)
	if _, err := readTalk(path); err == nil || !strings.Contains(err.Error(), "newSpeeker") {
		t.Errorf("an unknown key should be reported: %v", err)
	}
	os.WriteFile(path, []byte(`{"title":"The Lost Sheep","speaker":"Ian Taylor","newSpeaker":true,"passage":"Luke 15:1-7"}`), 0o644)
	talk, err := readTalk(path)
	if err != nil || !talk.NewSpeaker || talk.Passage != "Luke 15:1-7" {
		t.Errorf("talk %+v, err %v", talk, err)
	}
}
