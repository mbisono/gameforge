package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameforge/internal/retroach"
	"gameforge/internal/sevenzip"
)

func TestRACheck(t *testing.T) {
	dir := t.TempDir()
	gbc := bytes.Repeat([]byte("GBC ROM "), 8192)
	nes := bytes.Repeat([]byte("NES PRG "), 4096)
	sum := func(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }
	db := retroach.New(map[int]*retroach.List{
		retroach.GBC: {Games: []retroach.Game{
			{ID: 1, Title: "Crystal", Achievements: 53, Hashes: []string{sum(gbc)}},
			{ID: 3, Title: "Some Hack Base", Achievements: 10, Hashes: []string{"00"}},
		}},
		retroach.NES: {Games: []retroach.Game{{ID: 2, Title: "Some NES Game", Hashes: []string{sum(nes)}}}},
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
	seven := filepath.Join(dir, "c.7z")
	if _, err := sevenzip.Write(context.Background(), seven, []sevenzip.Source{{Name: "c.gbc", Size: int64(len(gbc)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(gbc)), nil }}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path, status string
		ok           bool
	}{
		{write("crystal.gbc", gbc), "Crystal (53", true},
		{zipOf("crystal.zip", map[string][]byte{"x.gbc": gbc}), "Crystal (53", true},
		{seven, "Crystal (53", true},
		{write("nes.nes", append([]byte("NES\x1a\x02\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"), nes...)), "Some NES Game", true},
		{write("hack.gbc", []byte("a translated hack")), "✗", false},
		{write("Some Hack Base (USA).gbc", []byte("another version")), "another version", false},
		{zipOf("sf2.zip", map[string][]byte{"sf2.01": gbc, "sf2.02": nes}), "–", false},
		{zipOf("noted.zip", map[string][]byte{"x.gbc": gbc, "readme.txt": []byte("note")}), "Crystal (53", true},
		{write("game.bin", gbc), "–", false},
		{write("game.sfc", gbc), "–", false}, // no SNES list
	} {
		status, line, ok := raCheck(context.Background(), db, scanRom(&convItem{path: c.path}))
		if ok != c.ok || status == nil || !strings.Contains(status.String(), c.status) {
			t.Errorf("%s: %v %v (%v)", filepath.Base(c.path), status, ok, line)
		}
	}
}
