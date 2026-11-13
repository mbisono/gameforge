// Package playlist writes M3U playlists for games on several discs, which
// emulators such as RetroArch and DuckStation use to change discs.
package playlist

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// discMark matches the disc number in names such as "Game (Disc 2)",
// "Game (Disc 2 of 3)", "Game [Disk 1]" or "Game (CD2)".
var discMark = regexp.MustCompile(`(?i)\s*[(\[]\s*(?:disc|disk|cd)\s*(\d+)(?:\s*of\s*\d+)?\s*[)\]]`)

// Disc splits a file name (without folder) into the game name and its
// disc number; ok is false when the name has no disc number.
func Disc(name string) (game string, n int, ok bool) {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	m := discMark.FindStringSubmatchIndex(name)
	if m == nil {
		return "", 0, false
	}
	n, _ = strconv.Atoi(name[m[2]:m[3]])
	game = strings.Join(strings.Fields(name[:m[0]]+" "+name[m[1]:]), " ")
	return game, n, game != ""
}

// Update writes, next to the disc image file, the playlist of all discs of
// its game found in that folder with the same extension, when there are at
// least two. It returns the playlist's path and its discs, or "" when no
// playlist applies or the existing one is already up to date.
func Update(file string) (string, []string, error) {
	dir, ext := filepath.Dir(file), filepath.Ext(file)
	game, _, ok := Disc(filepath.Base(file))
	if !ok {
		return "", nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}
	type disc struct {
		name string
		n    int
	}
	var discs []disc
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ext) {
			continue
		}
		if g, n, ok := Disc(e.Name()); ok && strings.EqualFold(g, game) {
			discs = append(discs, disc{e.Name(), n})
		}
	}
	if len(discs) < 2 {
		return "", nil, nil
	}
	sort.SliceStable(discs, func(i, j int) bool { return discs[i].n < discs[j].n })
	var names []string
	var b bytes.Buffer
	for _, d := range discs {
		names = append(names, d.name)
		b.WriteString(d.name + "\n")
	}
	path := filepath.Join(dir, game+".m3u")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b.Bytes()) {
		return "", nil, nil
	}
	return path, names, os.WriteFile(path, b.Bytes(), 0o644)
}
