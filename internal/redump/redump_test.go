package redump

import (
	"gameforge/internal/datfile"

	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sample = `clrmamepro (
	name "Sony - PlayStation"
	version "2026.10.07"
)

game (
	name "Ridge Racer (Europe)"
	region "Europe"
	serial "SCES-00001"
	rom ( name "Ridge Racer (Europe) (Track 01).bin" size 4085424 crc 014D98FC md5 06C8 sha1 E8B0 serial "SCES-00001" )
)
game (
	name "Ridge Racer (Europe)"
	region "Europe"
	serial "SCES-00001"
	rom ( name "Ridge Racer (Europe) (Track 01).bin" size 4085424 crc 014D98FC md5 06C8 sha1 E8B0 serial "SCES-00001" )
)
game (
	name "Ridge Racer Revolution (Europe)"
	serial "SCES-00242*"
	rom ( name "Ridge Racer Revolution (Europe) (Track 01).bin" size 11056752 crc CAD3E183 serial "SCES-00242*" )
)
game (
	name "Ridge Racer Revolution (Europe) (EDC)"
	serial "SCES-00242*"
	rom ( name "Ridge Racer Revolution (Europe) (EDC) (Track 01).bin" size 11056752 crc A69BB9D4 serial "SCES-00242*" )
)
game (
	name "Ridge Racer Revolution (Europe) (Rev 1)"
	serial "SCES-00242"
	rom ( name "Ridge Racer Revolution (Europe) (Rev 1) (Track 01).bin" size 11059104 crc 12345678 serial "SCES-00242" )
)
game (
	name "Some Game (Germany)"
	serial "SLES-01324/P, SLES-01325"
	rom ( name "Some Game (Germany).bin" size 100 crc 1 )
)
game (
	name "No Code (USA) (Unl)"
	rom ( name "No Code (USA) (Unl).bin" size 100 crc 1 )
)
`

func TestParseAndMatch(t *testing.T) {
	l, err := Parse("PS1", strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if l.Version != "2026.10.07" || len(l.Games) != 7 {
		t.Fatalf("version %q, %d games", l.Version, len(l.Games))
	}
	if g := l.Games[0]; g.Track != 1 || g.Size != 4085424 || g.CRC != 0x014D98FC || !reflect.DeepEqual(g.Codes, []string{"SCES00001"}) {
		t.Errorf("game 0: %+v", g)
	}
	if g := l.Games[5]; !reflect.DeepEqual(g.Codes, []string{"SLES01324", "SLES01325"}) || g.Track != 0 {
		t.Errorf("game 5: %+v", g)
	}
	db := &DB{Lists: map[string]*List{"PS1": l}}
	db.build()

	size := func(s int64) func(int) (int64, bool) { return func(int) (int64, bool) { return s, true } }
	crcCalls := 0
	crc := func(c uint32) func(int) (uint32, error) {
		return func(int) (uint32, error) { crcCalls++; return c, nil }
	}
	for _, c := range []struct {
		code      string
		track     Track
		want      string
		ambiguous int
		crcs      int
	}{
		{"SCES-00001", Track{}, "Ridge Racer (Europe)", 0, 0}, // listed twice, one name
		{"SCES-00242", Track{Size: size(11059104), CRC: crc(0)}, "Ridge Racer Revolution (Europe) (Rev 1)", 0, 0},
		{"SCES-00242", Track{Size: size(11056752), CRC: crc(0xA69BB9D4)}, "Ridge Racer Revolution (Europe) (EDC)", 0, 1},
		{"SCES-00242", Track{Size: size(11056752), CRC: crc(0xDEADBEEF)}, "", 2, 1},
		{"SLES-01325", Track{}, "Some Game (Germany)", 0, 0},
		{"SLUS-99999", Track{}, "", 0, 0},
	} {
		crcCalls = 0
		c.track.Rev = -1
		name, amb, err := db.Match("PS1", c.code, c.track)
		if err != nil || name != c.want || len(amb) != c.ambiguous || crcCalls != c.crcs {
			t.Errorf("%s: %q %v %v (%d CRCs)", c.code, name, amb, err, crcCalls)
		}
	}
}

func TestCode(t *testing.T) {
	for _, c := range []struct{ platform, in, want string }{
		{"PS2", "SLES-51313-ANZ", "SLES51313"},
		{"PSP", "ULES-01513/0046931", "ULES01513"},
		{"PS1", "SCUS_944.55", "SCUS94455"},
		{"GameCube", "DL-DOL-GALE-USA", "GALE"},
		{"GameCube", "GALE01", "GALE"},
		{"Wii", "RVL-R23E-USA-B0", "R23E"},
		{"Dreamcast", "T-46603D-50", "T46603D50"},
		{"Saturn", "MK81803-50", "MK8180350"},
	} {
		if got := Code(c.platform, c.in); got != c.want {
			t.Errorf("Code(%s, %s) = %s, want %s", c.platform, c.in, got, c.want)
		}
	}
	// A Sega code without the region suffix still finds the game.
	l, _ := Parse("Dreamcast", strings.NewReader("game (\n\tname \"X (Europe)\"\n\tserial \"T-46603D-50\"\n\trom ( name \"X (Europe) (Track 3).bin\" size 1 crc 2 )\n)\n"))
	db := &DB{Lists: map[string]*List{"Dreamcast": l}}
	db.build()
	if name, _, _ := db.Match("Dreamcast", "T-46603D", Track{Rev: -1}); name != "X (Europe)" {
		t.Errorf("suffix match: %q", name)
	}
}

func TestDownloadAndCache(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(sample))
	}))
	defer srv.Close()
	old := datfile.BaseURL
	datfile.BaseURL = srv.URL + "/"
	defer func() { datfile.BaseURL = old }()

	db, err := Download(context.Background(), nil, []string{"PS1"}, nil)
	if err != nil || !db.Has("PS1") || db.Has("PS2") {
		t.Fatalf("download: %v", err)
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := db.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || len(loaded.Lists["PS1"].Games) != 7 {
		t.Fatalf("load: %v", err)
	}
	if name, _, _ := loaded.Match("PS1", "SCES-00001", Track{Rev: -1}); name != "Ridge Racer (Europe)" {
		t.Errorf("after loading: %q", name)
	}
	again, err := Download(context.Background(), loaded, []string{"PS1"}, nil)
	if err != nil || hits != 2 || len(again.Lists["PS1"].Games) != 7 {
		t.Errorf("second download: %v, %d requests", err, hits)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "none")); !os.IsNotExist(err) {
		t.Errorf("missing cache: %v", err)
	}
}

// TestRealDats parses the real databases in REDUMP_DATS.
func TestRealDats(t *testing.T) {
	dir := os.Getenv("REDUMP_DATS")
	if dir == "" {
		t.Skip("REDUMP_DATS not set")
	}
	db := &DB{Lists: map[string]*List{}}
	for p, f := range Files {
		r, err := os.Open(filepath.Join(dir, f+".dat"))
		if err != nil {
			t.Fatal(err)
		}
		l, err := Parse(p, r)
		r.Close()
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		db.Lists[p] = l
		t.Logf("%s: %d games (%s)", p, len(l.Games), l.Version)
	}
	db.build()
	for _, c := range []struct{ platform, code, want string }{
		{"PS1", "SCES-00001", "Ridge Racer (Europe)"},
		{"PS2", "SLUS-21409", "LEGO Star Wars II - The Original Trilogy (USA)"},
		{"Dreamcast", "T44306N", "Fatal Fury - Mark of the Wolves (USA)"},
		{"GameCube", "GALE01", "Super Smash Bros. Melee (USA) (En,Ja) (Rev 2)"},
	} {
		name, amb, err := db.Match(c.platform, c.code, Track{Regions: []string{"USA"}, Rev: 2})
		t.Logf("%s %s: %q %v %v", c.platform, c.code, name, amb, err)
		if name != c.want && !contains(amb, c.want) {
			t.Errorf("%s: %q not found", c.code, c.want)
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// TestXbox finds Xbox games by the code and regions of an XISO, which has
// no size or CRC of the disc.
func TestXbox(t *testing.T) {
	const dat = `game (
	name "Burnout 3 - Takedown (Europe) (En,Es,Nl,Sv)"
	region "Europe"
	serial "EA-091"
	rom ( name "Burnout 3 - Takedown (Europe) (En,Es,Nl,Sv).iso" size 7825162240 crc 8ABB8436 serial "EA-091" )
)
game (
	name "Burnout 3 - Takedown (Europe) (Fr,De,It)"
	region "Europe"
	serial "EA-091"
	rom ( name "Burnout 3 - Takedown (Europe) (Fr,De,It).iso" size 7825162240 crc 6224448C serial "EA-091" )
)
game (
	name "Burnout 3 - Takedown (USA)"
	region "USA"
	serial "EA-091"
	rom ( name "Burnout 3 - Takedown (USA).iso" size 7825162240 crc 602C1D40 serial "EA-091" )
)
game (
	name "Half-Life 2 (USA, Europe) (En,Fr,De,Es,It)"
	serial "EA-145"
	rom ( name "Half-Life 2 (USA, Europe) (En,Fr,De,Es,It).iso" size 7825162240 crc 2F9D3611 serial "EA-145" )
)
`
	l, err := Parse("Xbox", strings.NewReader(dat))
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{Lists: map[string]*List{"Xbox": l}}
	db.build()
	for _, c := range []struct {
		code      string
		regions   []string
		want      string
		ambiguous int
	}{
		{"EA-091", []string{"USA"}, "Burnout 3 - Takedown (USA)", 0},
		{"EA-091", []string{"Europe"}, "", 2},
		{"EA-145", []string{"USA", "Japan", "Europe"}, "Half-Life 2 (USA, Europe) (En,Fr,De,Es,It)", 0},
	} {
		name, amb, err := db.Match("Xbox", c.code, Track{Regions: c.regions, Rev: -1})
		if err != nil || name != c.want || len(amb) != c.ambiguous {
			t.Errorf("%s %v: %q %v %v", c.code, c.regions, name, amb, err)
		}
	}
	// A disc dump's CRC tells the European versions apart.
	crc := func(int) (uint32, error) { return 0x6224448C, nil }
	size := func(int) (int64, bool) { return 7825162240, true }
	if name, _, _ := db.Match("Xbox", "EA-091", Track{Regions: []string{"Europe"}, Size: size, CRC: crc, Rev: -1}); name != "Burnout 3 - Takedown (Europe) (Fr,De,It)" {
		t.Errorf("dump: %q", name)
	}
}
