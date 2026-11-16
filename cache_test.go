package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveCaches(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "gameforge.json")
	os.WriteFile(filepath.Join(dir, "redump.cache"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(dir, "titledb.cache"), []byte("old"), 0o644)
	os.MkdirAll(filepath.Join(dir, "cache"), 0o755)
	os.WriteFile(filepath.Join(dir, "cache", "titledb.cache"), []byte("new"), 0o644)
	moveCaches(cfg)
	for name, want := range map[string]string{"redump.cache": "old", "titledb.cache": "new"} {
		if b, _ := os.ReadFile(filepath.Join(dir, "cache", name)); string(b) != want {
			t.Errorf("%s: %q", name, b)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s still next to the configuration", name)
		}
	}
}
