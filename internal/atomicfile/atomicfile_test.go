package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesACompleteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("contents = %q, want new", data)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file remains: %v", err)
	}
}

func TestReplaceRequiresSiblingFiles(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "one", "file"), filepath.Join(root, "two", "file")
	if err := Replace(first, second); err == nil {
		t.Fatal("replacement across directories was accepted")
	}
}
