package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveOriginal sends a scratch file to the Recycle Bin; it runs only
// with TRASHTEST=1, so that tests do not fill the Recycle Bin.
func TestRemoveOriginal(t *testing.T) {
	if os.Getenv("TRASHTEST") == "" {
		t.Skip("TRASHTEST not set")
	}
	p := filepath.Join(t.TempDir(), "gameforge trash test ✓.txt")
	if err := os.WriteFile(p, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeOriginal(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
}
