package main

import (
	"archive/zip"
	"bytes"
	"context"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameforge/internal/config"
	"gameforge/internal/nointro"
	"gameforge/internal/sevenzip"
)

func TestRomName(t *testing.T) {
	dir := t.TempDir()
	rom := bytes.Repeat([]byte("GBC ROM DATA "), 5000)
	nes := bytes.Repeat([]byte("NES PRG "), 4096)
	db := nointro.New(map[string]*nointro.List{
		"Game Boy Color": {Games: []nointro.Game{{Name: "Some Game (USA, Europe) (Rev 1)", Size: int64(len(rom)), CRC: crc32.ChecksumIEEE(rom)}}},
		"NES":            {Games: []nointro.Game{{Name: "Other: Game (Japan)", Size: int64(len(nes)), CRC: crc32.ChecksumIEEE(nes)}}},
	})
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, data, 0o644)
		return p
	}
	zipOf := func(name string, files map[string][]byte) string {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		for n, d := range files {
			w, _ := zw.Create(n)
			w.Write(d)
		}
		zw.Close()
		return write(name, b.Bytes())
	}
	loose := write("crystal.gbc", rom)
	headered := write("other.nes", append([]byte("NES\x1a\x02\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"), nes...))
	zipped := zipOf("pkmn.zip", map[string][]byte{"whatever.gbc": rom})
	arcade := zipOf("sf2.zip", map[string][]byte{"sf2.01": rom, "sf2.02": nes})
	unknown := write("hack.gbc", []byte("a translated hack"))
	seven := filepath.Join(dir, "x.7z")
	if _, err := sevenzip.Write(context.Background(), seven, []sevenzip.Source{{Name: "x.gbc", Size: int64(len(rom)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(rom)), nil }}}, nil); err != nil {
		t.Fatal(err)
	}

	u := &ui{cfg: &config.Config{}}
	for _, c := range []struct{ path, want, skip string }{
		{loose, "Some Game (USA, Europe) (Rev 1).gbc", ""},
		{headered, "Other - Game (Japan).nes", ""},
		{zipped, "Some Game (USA, Europe) (Rev 1).zip", ""},
		{seven, "Some Game (USA, Europe) (Rev 1).7z", ""},
		{arcade, "", "2"},
		{unknown, "", "No-Intro"},
	} {
		got, skip := romName(context.Background(), u, db, scanRom(&convItem{path: c.path}))
		if got != c.want {
			t.Errorf("%s: %q, want %q (%v)", filepath.Base(c.path), got, c.want, skip)
		}
		if c.skip != "" && (skip == nil || !strings.Contains(skip.String(), c.skip)) {
			t.Errorf("%s: reason %v", filepath.Base(c.path), skip)
		}
	}
}

func TestMovedUpdatesPool(t *testing.T) {
	tab := &convTab{checked: map[string]bool{"a.gbc": true}, pool: []string{"a.gbc", "a.srm"}}
	tab.items = []*convItem{{path: "a.gbc"}}
	tab.moved("a.gbc", "B.gbc")
	tab.moved("a.srm", "B.srm")
	if tab.items[0].path != "B.gbc" || !tab.checked["B.gbc"] || tab.pool[0] != "B.gbc" || tab.pool[1] != "B.srm" {
		t.Errorf("after moving: %+v %v %v", tab.items[0], tab.checked, tab.pool)
	}
}
