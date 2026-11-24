package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestRenameFile never replaces another file, but allows a change of case.
func TestRenameFile(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.chd"), filepath.Join(dir, "b.chd")
	os.WriteFile(a, []byte("a"), 0o644)
	os.WriteFile(b, []byte("b"), 0o644)
	if err := renameFile(a, b); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("over another file: %v", err)
	}

	// Change of case only.
	upper := filepath.Join(dir, "A.chd")
	if err := renameFile(a, upper); err != nil {
		t.Fatal(err)
	}
	if names, _ := filepath.Glob(filepath.Join(dir, "?.chd")); len(names) != 2 {
		t.Fatalf("files: %v", names)
	}

	// Where the file system tells case apart, "game.chd" and "Game.chd"
	// are two files: renaming one to the other must not replace it.
	lower, other := filepath.Join(dir, "game.chd"), filepath.Join(dir, "Game.chd")
	os.WriteFile(lower, []byte("1"), 0o644)
	os.WriteFile(other, []byte("2"), 0o644)
	if b, _ := os.ReadFile(lower); string(b) != "1" {
		t.Log("case-insensitive file system: only the first part ran")
		return
	}
	if err := renameFile(lower, other); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("case-only rename over another file: %v", err)
	}
	if b, _ := os.ReadFile(other); string(b) != "2" {
		t.Fatal("the other file was replaced")
	}
}
