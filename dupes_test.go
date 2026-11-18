package main

import (
	"archive/zip"
	"context"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gameforge/internal/config"
	"gameforge/internal/nointro"
)

// TestDupes groups the same Neo Geo game as a .neo, a .neo in a zip and
// MAME's set (by its short name), and two copies of an unknown ROM (by
// its CRC), but not different games that share a name.
func TestDupes(t *testing.T) {
	dir := t.TempDir()
	star := []byte("blazing star rom")
	other := []byte("some unknown rom")
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	zipOf := func(name string, files map[string][]byte) string {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		for n, d := range files {
			w, _ := zw.Create(n)
			w.Write(d)
		}
		zw.Close()
		f.Close()
		return p
	}
	paths := []string{
		write("Star.neo", star),
		zipOf("Some name.zip", map[string][]byte{"Blazing Star.neo": star}),
		zipOf("blazstar.zip", map[string][]byte{"239-p1.p1": []byte("p"), "239-c1.c1": []byte("c")}),
		write("Other.neo", other),
		zipOf("copy.zip", map[string][]byte{"x.neo": other}),
		write("Tetris.gb", []byte("gb")),
		write("Tetris.nes", []byte("nes")),
	}
	db := nointro.New(map[string]*nointro.List{"Neo Geo": {Games: []nointro.Game{
		{Name: "Blazing Star", Size: int64(len(star)), CRC: crc32.ChecksumIEEE(star), Short: "blazstar"},
	}}})
	u := &ui{}
	var files []dupeFile
	for _, p := range paths {
		r := scanRom(&convItem{path: p})
		files = append(files, dupeKeys(context.Background(), u, db, &r))
	}
	var got [][]string
	for _, g := range dupeGroups(files) {
		var names []string
		for _, f := range g {
			names = append(names, filepath.Base(f.item.path))
		}
		got = append(got, names)
	}
	want := [][]string{
		{"Star.neo", "Some name.zip", "blazstar.zip"},
		{"Other.neo", "copy.zip"},
	}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("groups %q, want %q", got, want)
	}
	if l := dupeLabel(dupeGroups(files)[0]); l != "Blazing Star" {
		t.Errorf("label %q", l)
	}
}

// TestKeep chooses the file kept of a group by the order of formats, then
// the database name, then the size.
func TestKeep(t *testing.T) {
	if got := keepText(""); got != "7z, neo, zip" {
		t.Errorf("default %q", got)
	}
	if got := keepText(" .ZIP;neo  7z,zip"); got != "zip, neo, 7z" {
		t.Errorf("parsed %q", got)
	}
	if got := keepText(" , "); got != "7z, neo, zip" {
		t.Errorf("empty %q", got)
	}
	f := func(path, game string, size int64) dupeFile {
		return dupeFile{item: &convItem{path: path}, game: game, size: size}
	}
	order := keepOrder(defaultKeep)
	cases := []struct {
		g    []dupeFile
		want int
	}{
		{[]dupeFile{f("blazstar.zip", "Blazing Star", 9), f("Blazing Star.neo", "Blazing Star", 5), f("x.7z", "Blazing Star", 1)}, 2},
		{[]dupeFile{f("blazstar.zip", "Blazing Star", 9), f("Blazing Star.neo", "Blazing Star", 5)}, 1},
		{[]dupeFile{f("bs.7z", "Blazing Star", 9), f("Blazing Star.7z", "Blazing Star", 5)}, 1},
		{[]dupeFile{f("a.7z", "", 5), f("b.7z", "", 9)}, 1},
		{[]dupeFile{f("a.sfc", "", 5), f("b.smc", "", 9), f("c.zip", "", 1)}, 2},
		{[]dupeFile{f("a.sfc", "", 5), f("b.smc", "", 9)}, 1},
	}
	for i, c := range cases {
		if got := keepIndex(c.g, order); got != c.want {
			t.Errorf("case %d: kept %d, want %d", i, got, c.want)
		}
	}
	if got := keepIndex([]dupeFile{f("a.7z", "", 1), f("b.zip", "", 1)}, keepOrder("zip")); got != 1 {
		t.Errorf("own order: kept %d", got)
	}
}

// TestMarkGroups checks only the extra copies, by the order of formats
// chosen now, and notices when the list changed since the check.
func TestMarkGroups(t *testing.T) {
	tab := &convTab{checked: map[string]bool{}}
	item := func(p string) *convItem {
		it := &convItem{path: p, ok: true, tab: tab}
		tab.items = append(tab.items, it)
		tab.checked[p] = true
		return it
	}
	neo, sz, lone := item("Blazing Star.neo"), item("Blazing Star.7z"), item("Other.7z")
	tab.dupes = []dupeGroup{{files: []dupeFile{{item: neo, game: "Blazing Star"}, {item: sz, game: "Blazing Star"}}}}
	u := &ui{cfg: &config.Config{}}
	if !tab.dupesListed() {
		t.Fatal("not listed")
	}
	markGroups(u, tab.dupes)
	if !tab.checked[neo.path] || tab.checked[sz.path] || tab.checked[lone.path] {
		t.Errorf("default order: %v", tab.checked)
	}
	u.cfg.DupesKeep = "neo"
	markGroups(u, tab.dupes)
	if tab.checked[neo.path] || !tab.checked[sz.path] {
		t.Errorf("own order: %v", tab.checked)
	}
	tab.items = tab.items[1:] // the .neo was deleted
	if tab.dupesListed() {
		t.Errorf("still listed")
	}
}
