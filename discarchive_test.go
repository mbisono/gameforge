package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gameforge/internal/chd"
	"gameforge/internal/i18n"
	"gameforge/internal/sevenzip"
)

// literalECM wraps data in an ECM file of literal records only.
func literalECM(data []byte) []byte {
	out := []byte("ECM\x00")
	for len(data) > 0 {
		n := min(len(data), 1<<20)
		v := uint64(n - 1)
		c := byte(v&0x1f) << 2
		v >>= 5
		for v != 0 {
			out = append(out, c|0x80)
			c, v = byte(v&0x7f), v>>7
		}
		out = append(out, c)
		out = append(out, data[:n]...)
		data = data[n:]
	}
	return out
}

func finishECM(out, data []byte) []byte {
	out = append(out, 0xfc, 0xff, 0xff, 0xff, 0x3f)
	edc := ^crc32.Update(^uint32(0), crc32.MakeTable(0xd8018001), data)
	return binary.LittleEndian.AppendUint32(out, edc)
}

// TestDiscArchives converts the same disc from a CUE sheet, a ZIP, a 7z
// and a .bin.ecm, and expects the same CHD from each.
func TestDiscArchives(t *testing.T) {
	i18n.Set("en")
	rng := rand.New(rand.NewPCG(3, 4))
	bin := make([]byte, 200*2352)
	for i := range bin {
		bin[i] = byte(rng.IntN(256))
	}
	for s := 0; s < len(bin); s += 2352 { // sectors with a sync pattern and a Mode 1 header
		copy(bin[s:], []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0})
		bin[s+15] = 1
	}
	cue := "FILE \"Game.bin\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n"
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Game.bin"), bin, 0o644)
	os.WriteFile(filepath.Join(dir, "Game.cue"), []byte(cue), 0o644)
	ref := filepath.Join(t.TempDir(), "ref.chd")
	if _, err := chd.ConvertImage(context.Background(), filepath.Join(dir, "Game.cue"), ref, chd.Options{}); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(ref)

	// A ZIP and a 7z with the CUE sheet and the BIN file.
	arch := t.TempDir()
	zipPath := filepath.Join(arch, "Game (USA).zip")
	zf, _ := os.Create(zipPath)
	zw := zip.NewWriter(zf)
	for _, f := range []struct {
		name string
		data []byte
	}{{"Game.cue", []byte(cue)}, {"Game.bin", bin}} {
		w, _ := zw.Create(f.name)
		w.Write(f.data)
	}
	zw.Close()
	zf.Close()
	szPath := filepath.Join(arch, "Game (Europe).7z")
	src := func(name string, data []byte) sevenzip.Source {
		return sevenzip.Source{Name: name, Size: int64(len(data)), Modified: time.Now(),
			Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }}
	}
	if _, err := sevenzip.Write(context.Background(), szPath, []sevenzip.Source{src("Game.cue", []byte(cue)), src("Game.bin", bin)}, nil); err != nil {
		t.Fatal(err)
	}
	// A ROM archive is not a disc.
	romPath := filepath.Join(arch, "rom.zip")
	rf, _ := os.Create(romPath)
	rw := zip.NewWriter(rf)
	w, _ := rw.Create("rom.sfc")
	w.Write([]byte("rom"))
	rw.Close()
	rf.Close()
	if chdConverter.accept(romPath) {
		t.Error("a ZIP of a ROM is listed in the Discs tab")
	}

	u := &ui{}
	for _, p := range []string{zipPath, szPath} {
		if !chdConverter.accept(p) {
			t.Fatalf("%s is not listed", filepath.Base(p))
		}
		it := &convItem{path: p}
		describeDisc(it)
		if !it.ok || it.kind != "archive" || !strings.Contains(stringOf(it.detail), "Game.cue + 1 other file(s)") {
			t.Fatalf("%s: %+v %v", filepath.Base(p), it, stringOf(it.detail))
		}
		outDir := t.TempDir()
		out := filepath.Join(outDir, chdConverter.outName(it))
		if !strings.HasSuffix(out, discStem(p)+".chd") {
			t.Fatalf("name %s", out)
		}
		if _, _, err := convertDisc(context.Background(), u, it, out, true, func(bool, int64, int64) {}); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(out); !bytes.Equal(got, want) {
			t.Errorf("%s: CHD differs", filepath.Base(p))
		}
		if left, _ := os.ReadDir(outDir); len(left) != 1 {
			t.Errorf("%s: left %d entries in the output folder", filepath.Base(p), len(left))
		}
	}

	// game.bin.ecm: listed alone, but not next to a CUE sheet naming it.
	ecmDir := t.TempDir()
	ecm := filepath.Join(ecmDir, "Game.bin.ecm")
	os.WriteFile(ecm, finishECM(literalECM(bin), bin), 0o644)
	if !chdConverter.accept(ecm) {
		t.Fatal("a .bin.ecm alone is not listed")
	}
	it := &convItem{path: ecm}
	describeDisc(it)
	if !it.ok || chdConverter.outName(it) != "Game.chd" {
		t.Fatalf("ECM: %+v, name %s", it, chdConverter.outName(it))
	}
	out := filepath.Join(t.TempDir(), "Game.chd")
	if _, _, err := convertDisc(context.Background(), u, it, out, false, func(bool, int64, int64) {}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, want) {
		t.Error("ECM: CHD differs")
	}
	os.WriteFile(filepath.Join(ecmDir, "Game.cue"), []byte(cue), 0o644)
	if chdConverter.accept(ecm) {
		t.Error("a .bin.ecm named by a CUE sheet is listed too")
	}
	// A damaged ECM file (wrong checksum) is refused and leaves no CHD.
	bad := finishECM(literalECM(bin), bin)
	bad[len(bad)-1] ^= 1
	os.WriteFile(ecm, bad, 0o644)
	it = &convItem{path: filepath.Join(ecmDir, "Game.cue")}
	describeDisc(it)
	out = filepath.Join(t.TempDir(), "Game.chd")
	if _, _, err := convertDisc(context.Background(), u, it, out, false, func(bool, int64, int64) {}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("damaged ECM: %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a CHD was left from a damaged ECM file")
	}
}

// TestRemoveStaleTemp removes unpack folders a day old, not newer ones
// (another conversion may be using them).
func TestRemoveStaleTemp(t *testing.T) {
	dir := t.TempDir()
	old, fresh := filepath.Join(dir, ".gameforge-1"), filepath.Join(dir, ".gameforge-2")
	for _, p := range []string{old, fresh} {
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "disc.bin"), []byte("x"), 0o644)
	}
	day := time.Now().Add(-25 * time.Hour)
	os.Chtimes(old, day, day)
	removeStaleTemp(dir)
	if _, err := os.Stat(old); err == nil {
		t.Error("the old folder is still there")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("the new folder was removed")
	}
}
