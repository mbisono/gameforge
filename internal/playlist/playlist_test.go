package playlist

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisc(t *testing.T) {
	for _, c := range []struct {
		name, game string
		n          int
		ok         bool
	}{
		{"Colony Wars (Disc 1).chd", "Colony Wars", 1, true},
		{"Final Fantasy VII (USA) (Disc 2).chd", "Final Fantasy VII (USA)", 2, true},
		{"Metal Gear Solid (Europe) (Disc 2 of 2) (En,Fr).chd", "Metal Gear Solid (Europe) (En,Fr)", 2, true},
		{"Game [Disk 3].chd", "Game", 3, true},
		{"Game (CD2).chd", "Game", 2, true},
		{"Gran Turismo (USA).chd", "", 0, false},
	} {
		g, n, ok := Disc(c.name)
		if g != c.game || n != c.n || ok != c.ok {
			t.Errorf("%s: got %q %d %v", c.name, g, n, ok)
		}
	}
}

func TestUpdate(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"Colony Wars (Disc 2).chd", "Colony Wars (Disc 1).chd", "Other (Disc 1).chd", "Colony Wars (Disc 1).cue"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	path, discs, err := Update(filepath.Join(dir, "Colony Wars (Disc 2).chd"))
	if err != nil || filepath.Base(path) != "Colony Wars.m3u" || len(discs) != 2 {
		t.Fatalf("got %q %v %v", path, discs, err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "Colony Wars (Disc 1).chd\nColony Wars (Disc 2).chd\n" {
		t.Fatalf("content %q", b)
	}
	// Unchanged: nothing to write.
	if p, _, _ := Update(filepath.Join(dir, "Colony Wars (Disc 1).chd")); p != "" {
		t.Error("rewrote an up-to-date playlist")
	}
	// A single disc gets no playlist.
	if p, _, _ := Update(filepath.Join(dir, "Other (Disc 1).chd")); p != "" {
		t.Error("playlist for one disc")
	}
}
