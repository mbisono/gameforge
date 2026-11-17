package nointro

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameforge/internal/datfile"
)

var (
	romData = bytes.Repeat([]byte("cartridge data "), 2000)
	n64Data = append([]byte{0x80, 0x37, 0x12, 0x40}, bytes.Repeat([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 1000)...)
)

func sample() string {
	ines := append([]byte("NES\x1a\x02\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"), romData...)
	var b strings.Builder
	b.WriteString("clrmamepro (\n\tname \"Test\"\n\tversion \"2026.11.14\"\n)\n\n")
	game := func(gameName, romName string, data []byte) {
		fmt.Fprintf(&b, "game (\n\tname %q\n\tregion \"USA\"\n\trom ( name %q size %d crc %08X md5 00 sha1 00 )\n)\n",
			gameName, romName, len(data), crc32.ChecksumIEEE(data))
	}
	game("Some Game (USA)", "Some Game (USA) (Unl).nes", ines)
	game("Some Game (USA)", "Some Game (USA) (Unl).unh", romData)
	game("Dr. Something (World)", "Dr. Something (World).z64", n64Data)
	return b.String()
}

func TestParseFind(t *testing.T) {
	l, err := Parse(strings.NewReader(sample()))
	if err != nil {
		t.Fatal(err)
	}
	if l.Version != "2026.11.14" || len(l.Games) != 3 {
		t.Fatalf("parsed %+v", l)
	}
	if l.Games[0].Name != "Some Game (USA) (Unl)" || l.Games[2].Name != "Dr. Something (World)" {
		t.Errorf("names %q, %q", l.Games[0].Name, l.Games[2].Name)
	}
	db := &DB{Lists: map[string]*List{"NES": l}}
	db.build()
	m := db.Find(crc32.ChecksumIEEE(romData), int64(len(romData)), "NES")
	if len(m) != 1 || m[0].Name != "Some Game (USA) (Unl)" || m[0].System != "NES" {
		t.Errorf("find: %+v", m)
	}
	if m := db.Find(crc32.ChecksumIEEE(romData), int64(len(romData))+1, ""); len(m) != 0 {
		t.Errorf("size must match: %+v", m)
	}
}

func TestIdentifyFixes(t *testing.T) {
	l, _ := Parse(strings.NewReader(sample()))
	db := &DB{Lists: map[string]*List{"NES": l}}
	db.build()
	check := func(what string, data []byte, ext, want string) {
		t.Helper()
		open := func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
		m, fix, err := db.Identify(context.Background(), crc32.ChecksumIEEE(data), int64(len(data)), ext, open, nil)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		got := ""
		if len(m) > 0 {
			got = m[0].Name
		}
		if got != want {
			t.Errorf("%s: %q (fix %+v), want %q", what, got, fix, want)
		}
	}
	// A different iNES header than the database's: found without it.
	other := append([]byte("NES\x1a\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00"), romData...)
	check("iNES", other, ".nes", "Some Game (USA) (Unl)")
	check("copier header", append(make([]byte, 512), romData...), ".smc", "")
	check("unknown", []byte("nothing"), ".gb", "")

	v64 := append([]byte(nil), n64Data...)
	swap(v64, 2)
	check("v64", v64, ".v64", "Dr. Something (World)")
	n64 := append([]byte(nil), n64Data...)
	swap(n64, 4)
	check("n64", n64, ".n64", "Dr. Something (World)")
	check("z64", n64Data, ".z64", "Dr. Something (World)")
}

func TestSystemOf(t *testing.T) {
	for ext, want := range map[string]string{".GBC": "Game Boy Color", ".smc": "SNES", ".bin": "", ".nes": "NES"} {
		if got := SystemOf(ext); got != want {
			t.Errorf("%s: %q", ext, got)
		}
	}
	for _, s := range Systems {
		if fileOf(s.Name) != s.File {
			t.Errorf("%s: file", s.Name)
		}
	}
}

func TestDownloadAndCache(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/metadat/no-intro/Nintendo - Nintendo Entertainment System.dat" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(sample()))
	}))
	defer srv.Close()
	old := datfile.BaseURL
	datfile.BaseURL = srv.URL + "/"
	defer func() { datfile.BaseURL = old }()

	db, err := Download(context.Background(), nil, []string{"NES"}, nil)
	if err != nil || !db.Has("NES") || db.Has("SNES") {
		t.Fatalf("download: %v", err)
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := db.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || len(loaded.Lists["NES"].Games) != 3 {
		t.Fatalf("load: %v", err)
	}
	if m := loaded.Find(crc32.ChecksumIEEE(n64Data), int64(len(n64Data)), ""); len(m) != 1 {
		t.Errorf("after loading: %+v", m)
	}
	again, err := Download(context.Background(), loaded, []string{"NES"}, nil)
	if err != nil || hits != 2 || len(again.Lists["NES"].Games) != 3 {
		t.Errorf("second download: %v, %d requests", err, hits)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "none")); !os.IsNotExist(err) {
		t.Errorf("missing cache: %v", err)
	}
}

// TestRealDats parses the real databases in NOINTRO_DATS.
func TestRealDats(t *testing.T) {
	dir := os.Getenv("NOINTRO_DATS")
	if dir == "" {
		t.Skip("NOINTRO_DATS not set")
	}
	for _, s := range Systems {
		f, err := os.Open(filepath.Join(dir, s.File+".dat"))
		if err != nil {
			continue
		}
		l, err := Parse(f)
		f.Close()
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
			continue
		}
		t.Logf("%s: %d ROMs, %s", s.Name, len(l.Games), l.Version)
	}
}

// TestNeoGeo reads libretro's own Neo Geo list, whose files have the
// emulator's short names: a ROM gets the name of its game.
func TestNeoGeo(t *testing.T) {
	const dat = "clrmamepro (\n\tname \"SNK - Neo Geo\"\n)\n\ngame (\n\tname \"Aero Fighters 2 / Sonic Wings 2\"\n\tpublisher \"Video System Co.\"\n\trom ( name \"sonicwi2.neo\" size 13897728 crc AC1851BB )\n)\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dat/SNK - Neo Geo.dat" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(dat))
	}))
	defer srv.Close()
	old := datfile.BaseURL
	datfile.BaseURL = srv.URL + "/"
	defer func() { datfile.BaseURL = old }()

	if s := SystemOf(".NEO"); s != "Neo Geo" {
		t.Fatalf("SystemOf(.neo) = %q", s)
	}
	db, err := Download(context.Background(), nil, []string{"Neo Geo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := db.Find(0xAC1851BB, 13897728, "Neo Geo")
	if len(m) != 1 || m[0].Name != "Aero Fighters 2 - Sonic Wings 2" {
		t.Errorf("found %+v", m)
	}
	if !db.HasShort("Neo Geo") || db.ByShort("Neo Geo", "SonicWi2") != "Aero Fighters 2 - Sonic Wings 2" {
		t.Errorf("short name not found")
	}

	// A list saved without short names (before 2.12.0) is fetched again
	// even if the server says it did not change.
	db.Lists["Neo Geo"].ETag = "x"
	for i := range db.Lists["Neo Geo"].Games {
		db.Lists["Neo Geo"].Games[i].Short = ""
	}
	db.build()
	if db.HasShort("Neo Geo") {
		t.Fatal("short names left")
	}
	db, err = Download(context.Background(), db, []string{"Neo Geo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !db.HasShort("Neo Geo") {
		t.Errorf("list without short names not fetched again")
	}
}
