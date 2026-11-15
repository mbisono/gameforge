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

func TestCompanions(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"Ridge Racer.chd", "Ridge Racer.state.auto", "Ridge Racer.state1", "ridge racer.PNG", "Ridge Racer.srm",
		"Ridge Racer.cue", "Ridge Racer Revolution.png", "Ridge Racer.m3u", "Other.png",
	} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	game := filepath.Join(dir, "Ridge Racer.chd")
	got := map[string]string{}
	for _, c := range companions(game) {
		got[filepath.Base(c)] = filepath.Base(companionName(c, game, filepath.Join(dir, "Ridge Racer (Europe).chd")))
	}
	want := map[string]string{
		"Ridge Racer.state.auto": "Ridge Racer (Europe).state.auto",
		"Ridge Racer.state1":     "Ridge Racer (Europe).state1",
		"ridge racer.PNG":        "Ridge Racer (Europe).PNG",
		"Ridge Racer.srm":        "Ridge Racer (Europe).srm",
	}
	if len(got) != len(want) {
		t.Fatalf("companions %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s → %q, want %q", k, got[k], v)
		}
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
