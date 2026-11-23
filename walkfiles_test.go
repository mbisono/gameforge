package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestWalkFiles reads subfolders only when asked to.
func TestWalkFiles(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub", "deeper"), 0o755)
	for _, p := range []string{"a.iso", "sub/b.iso", "sub/deeper/c.iso"} {
		os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), nil, 0o644)
	}
	names := func(root string, sub bool) []string {
		var got []string
		walkFiles(root, sub, func(p string) { got = append(got, filepath.Base(p)) })
		slices.Sort(got)
		return got
	}
	if got := names(dir, false); !slices.Equal(got, []string{"a.iso"}) {
		t.Errorf("without subfolders: %v", got)
	}
	if got := names(dir, true); !slices.Equal(got, []string{"a.iso", "b.iso", "c.iso"}) {
		t.Errorf("with subfolders: %v", got)
	}
	if got := names(filepath.Join(dir, "sub", "b.iso"), false); !slices.Equal(got, []string{"b.iso"}) {
		t.Errorf("a file: %v", got)
	}
}
