package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdatePlaylists(t *testing.T) {
	dir := t.TempDir()
	m3u := filepath.Join(dir, "Game.m3u")
	os.WriteFile(m3u, []byte("game (disc 1).chd\r\nGame (Disc 2).chd\r\nOther.chd\r\n"), 0o644)
	untouched := filepath.Join(dir, "Other.m3u")
	os.WriteFile(untouched, []byte("Other.chd\n"), 0o644)
	changed := updatePlaylists(map[string]string{
		filepath.Join(dir, "Game (Disc 1).chd"): filepath.Join(dir, "Game (USA) (Disc 1).chd"),
		filepath.Join(dir, "Game (Disc 2).chd"): filepath.Join(dir, "Game (USA) (Disc 2).chd"),
	})
	if len(changed) != 1 || changed[0] != m3u {
		t.Fatalf("changed %v", changed)
	}
	b, _ := os.ReadFile(m3u)
	if want := "Game (USA) (Disc 1).chd\r\nGame (USA) (Disc 2).chd\r\nOther.chd\r\n"; string(b) != want {
		t.Errorf("playlist:\n%q\nwant\n%q", b, want)
	}
}

func TestFileSafe(t *testing.T) {
	for in, want := range map[string]string{
		"Ridge Racer (Europe)":           "Ridge Racer (Europe)",
		"Grand Theft Auto: Liberty City": "Grand Theft Auto - Liberty City",
		`What?/Why* "Now"`:               `What__Why_ _Now_`,
		"Trailing dot.":                  "Trailing dot",
	} {
		if got := fileSafe(in); got != want {
			t.Errorf("fileSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
