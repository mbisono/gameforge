package main

import (
	"bytes"
	"context"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gameforge/internal/nointro"
	"gameforge/internal/sevenzip"
)

// TestROMTools runs the write step of the .smd and copier header tools.
func TestROMTools(t *testing.T) {
	dir := t.TempDir()
	rom := make([]byte, 32<<10)
	for i := range rom {
		rom[i] = byte(i * 5)
	}
	ctx := context.Background()

	// .smd → .md
	smd := make([]byte, 512, 512+len(rom))
	smd[8], smd[9] = 0xaa, 0xbb
	for at := 0; at < len(rom); at += 16 << 10 {
		blk := rom[at : at+16<<10]
		for i := 1; i < len(blk); i += 2 {
			smd = append(smd, blk[i])
		}
		for i := 0; i < len(blk); i += 2 {
			smd = append(smd, blk[i])
		}
	}
	src := filepath.Join(dir, "Game.smd")
	os.WriteFile(src, smd, 0o644)
	if !smdJob.pick(src) || smdJob.target(src) != filepath.Join(dir, "Game.md") {
		t.Fatal("smd: not picked or wrong name")
	}
	if err := smdJob.write(ctx, src, smdJob.target(src), nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(smdJob.target(src)); !bytes.Equal(got, rom) {
		t.Fatal("smd: decoded ROM differs")
	}
	if err := smdJob.write(ctx, src, smdJob.target(src), nil); err == nil {
		t.Fatal("smd: wrote over an existing file")
	}

	// Copier headers: a .smc becomes .sfc; a .pce keeps its name.
	headered := append(make([]byte, 512), rom...)
	smc := filepath.Join(dir, "Game.smc")
	pce := filepath.Join(dir, "Game.pce")
	plain := filepath.Join(dir, "Plain.sfc")
	os.WriteFile(smc, headered, 0o644)
	os.WriteFile(pce, headered, 0o644)
	os.WriteFile(plain, rom, 0o644)
	if !headerJob.pick(smc) || !headerJob.pick(pce) || headerJob.pick(plain) {
		t.Fatal("copier headers: wrong files picked")
	}
	if headerJob.target(smc) != filepath.Join(dir, "Game.sfc") || headerJob.target(pce) != pce {
		t.Fatal("copier headers: wrong names")
	}
	for _, p := range []string{smc, pce} {
		dst := headerJob.target(p)
		if dst == p {
			dst = p + ".gameforge.tmp"
		}
		if err := headerJob.write(ctx, p, dst, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(dst); !bytes.Equal(got, rom) {
			t.Fatalf("%s: header not removed", filepath.Base(p))
		}
	}
}

// TestSMDIn7z identifies a Mega Drive ROM in an .smd file inside a 7z,
// which the CRC in the 7z's header does not tell.
func TestSMDIn7z(t *testing.T) {
	rom := make([]byte, 32<<10)
	for i := range rom {
		rom[i] = byte(i*11 + 3)
	}
	smd := make([]byte, 512, 512+len(rom))
	smd[8], smd[9] = 0xaa, 0xbb
	for at := 0; at < len(rom); at += 16 << 10 {
		blk := rom[at : at+16<<10]
		for i := 1; i < len(blk); i += 2 {
			smd = append(smd, blk[i])
		}
		for i := 0; i < len(blk); i += 2 {
			smd = append(smd, blk[i])
		}
	}
	path := filepath.Join(t.TempDir(), "game.7z")
	src := sevenzip.Source{Name: "game.smd", Size: int64(len(smd)), Modified: time.Now(),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(smd)), nil }}
	if _, err := sevenzip.Write(context.Background(), path, []sevenzip.Source{src}, nil); err != nil {
		t.Fatal(err)
	}
	db := nointro.New(map[string]*nointro.List{"Mega Drive": {Games: []nointro.Game{{Name: "Game (USA)", Size: int64(len(rom)), CRC: crc32.ChecksumIEEE(rom)}}}})
	r := scanRom(&convItem{path: path})
	m, _, err := db.Identify(context.Background(), r.crc, r.size, r.ext(), r.open(), nil)
	if err != nil || len(m) != 1 || m[0].Name != "Game (USA)" {
		t.Fatalf("got %v, %v", m, err)
	}
}
