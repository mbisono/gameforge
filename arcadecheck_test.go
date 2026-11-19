package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameforge/internal/arcade"
	"gameforge/internal/i18n"
	"gameforge/internal/nointro"
)

// TestArcadeFolder reads sets from ZIPs, finds the BIOS set next to them
// and shows the results.
func TestArcadeFolder(t *testing.T) {
	dir := t.TempDir()
	mkzip := func(name string, files map[string]string) string {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		for n, d := range files {
			w, _ := zw.Create(n)
			w.Write([]byte(d))
		}
		zw.Close()
		f.Close()
		return p
	}
	game := mkzip("game.zip", map[string]string{"g.p1": "program"})
	mkzip("bios.zip", map[string]string{"b.rom": "bios"})
	files, err := archiveFiles(game)
	if err != nil || len(files) != 1 || files[0].Name != "g.p1" || files[0].Size != 7 {
		t.Fatalf("files %+v, %v", files, err)
	}
	if setArchive(dir, "bios") == "" || setArchive(dir, "none") != "" {
		t.Errorf("setArchive")
	}
	crc := func(p string) uint32 { fs, _ := archiveFiles(p); return fs[0].CRC }
	l := &arcade.List{Sets: map[string]*arcade.Set{
		"bios": {Name: "bios", Bios: true, Works: true, Roms: []arcade.Rom{{Name: "b.rom", Size: 4, CRC: crc(filepath.Join(dir, "bios.zip"))}}},
		"game": {Name: "game", Title: "Game", RomOf: "bios", Works: true, Roms: []arcade.Rom{
			{Name: "g.p1", Size: 7, CRC: files[0].CRC},
			{Name: "b.rom", Merge: "b.rom", Size: 4, CRC: crc(filepath.Join(dir, "bios.zip"))},
		}},
	}}
	other := func(set string) ([]arcade.File, bool) {
		p := setArchive(dir, set)
		if p == "" {
			return nil, false
		}
		fs, err := archiveFiles(p)
		return fs, err == nil
	}
	i18n.Set("en")
	emu, _ := arcade.EmulatorByID("fbneo")
	r := l.Audit("game", files, other)
	st, line := arcadeResult(emu, "game.zip", r, nil)
	if !r.OK() || st.String() != "✓ FBNeo: Game" || !strings.Contains(line.String(), "(+ bios.zip)") {
		t.Errorf("complete: %q, %q", st, line)
	}
	os.Remove(filepath.Join(dir, "bios.zip"))
	r = l.Audit("game", files, other)
	st, _ = arcadeResult(emu, "game.zip", r, nil)
	if !r.OK() || st.String() != "✓ FBNeo: Game" || len(r.Bios) != 1 || r.Bios[0] != "bios" {
		t.Errorf("no BIOS: %q", st)
	}
}

// TestMissingChds finds a set's CHDs in a folder named after it or after
// its parent.
func TestMissingChds(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ikaruga"), 0o755)
	os.WriteFile(filepath.Join(dir, "ikaruga", "gdl-0010.chd"), nil, 0o644)
	l := &arcade.List{Sets: map[string]*arcade.Set{
		"ikaruga":  {Name: "ikaruga", Chds: []string{"gdl-0010"}},
		"ikarugaj": {Name: "ikarugaj", CloneOf: "ikaruga", Chds: []string{"gdl-0010"}},
		"radirgy":  {Name: "radirgy", Chds: []string{"gdl-0032a"}},
	}}
	if m := missingChds(dir, l.Sets["ikaruga"], l); len(m) != 0 {
		t.Errorf("ikaruga: %v", m)
	}
	if m := missingChds(dir, l.Sets["ikarugaj"], l); len(m) != 0 {
		t.Errorf("clone: %v", m)
	}
	if m := missingChds(dir, l.Sets["radirgy"], l); len(m) != 1 || m[0] != "radirgy/gdl-0032a.chd" {
		t.Errorf("radirgy: %v", m)
	}
	emu, _ := arcade.EmulatorByID("flycast")
	st, _ := arcadeResult(emu, "radirgy.zip", arcade.Result{Set: l.Sets["radirgy"]}, []string{"radirgy/gdl-0032a.chd"})
	if st.String() != "✗ Flycast: needs the CHD radirgy/gdl-0032a.chd" {
		t.Errorf("status %q", st)
	}
}

// TestNeoResult tells a .neo file from an arcade set, with and without
// libretro's Neo Geo list.
func TestNeoResult(t *testing.T) {
	i18n.Set("en")
	emu, _ := arcade.EmulatorByID("fbneo")
	files := []arcade.File{{Name: "Aero Fighters 2.neo", Size: 13897728, CRC: 0xAC1851BB}}
	if _, _, ok := neoResult(emu, "a.7z", []arcade.File{{Name: "075-p1.p1", Size: 1, CRC: 2}}, nil); ok {
		t.Errorf("not a .neo file")
	}
	st, line, ok := neoResult(emu, "Aero Fighters 2.7z", files, nil)
	if !ok || st.String() != "– FBNeo: .neo file, not an arcade set" || !strings.Contains(line.String(), "Rename…") {
		t.Errorf("no list: %q, %q", st, line)
	}
	l, err := nointro.Parse(strings.NewReader(`clrmamepro (
	name "SNK - Neo Geo"
)

game (
	name "Aero Fighters 2 / Sonic Wings 2"
	rom ( name "sonicwi2.neo" size 13897728 crc AC1851BB )
)
`))
	if err != nil {
		t.Fatal(err)
	}
	db := nointro.New(map[string]*nointro.List{"Neo Geo": l})
	if _, line, _ := neoResult(emu, "x.7z", files, db); !strings.Contains(line.String(), "knows its CRC: ") {
		t.Errorf("known: %q", line)
	}
	files[0].CRC++
	if _, line, _ := neoResult(emu, "x.7z", files, db); !strings.HasSuffix(line.String(), "not in libretro's Neo Geo list.") {
		t.Errorf("unknown: %q", line)
	}
}
