package main

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gameforge/internal/config"
	"gameforge/internal/sevenzip"
)

func TestSevenZipConverter(t *testing.T) {
	dir := t.TempDir()
	rom := bytes.Repeat([]byte("GBC ROM DATA "), 20_000)
	os.WriteFile(filepath.Join(dir, "Game (USA).gbc"), rom, 0o644)
	os.WriteFile(filepath.Join(dir, "Game (USA).state.auto"), []byte("save"), 0o644)
	os.WriteFile(filepath.Join(dir, "Game (USA).png"), []byte("png"), 0o644)

	// An arcade-style ZIP: several ROM files, deflated and stored.
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	when := time.Date(1991, 3, 1, 10, 0, 0, 0, time.UTC)
	files := map[string][]byte{"sf2.01": bytes.Repeat([]byte{1, 2, 3, 4}, 30_000), "sf2.02": []byte("short"), "empty.bin": nil}
	for _, name := range []string{"sf2.01", "sf2.02", "empty.bin"} {
		method := zip.Deflate
		if name == "sf2.02" {
			method = zip.Store
		}
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: when})
		w.Write(files[name])
	}
	zw.Close()
	os.WriteFile(filepath.Join(dir, "sf2.zip"), zbuf.Bytes(), 0o644)

	u := &ui{cfg: &config.Config{SevenZipDelete: true}}
	sevenZip.ui = u
	removed := map[string]bool{}
	removeFile = func(p string) error { removed[filepath.Base(p)] = true; return nil }
	defer func() { removeFile, sevenZip.ui = removeOriginal, nil }()

	tab := &convTab{conv: sevenZipConverter, checked: map[string]bool{}}
	tab.add(u, []string{dir})
	var listed []string
	for _, it := range tab.items {
		listed = append(listed, filepath.Base(it.path))
		if !it.ok {
			t.Errorf("%s not convertible: %v", it.path, it.detail)
		}
	}
	if strings.Join(listed, ",") != "Game (USA).gbc,sf2.zip" {
		t.Fatalf("listed %v", listed)
	}
	if len(tab.pool) != 4 {
		t.Errorf("pool has %d files", len(tab.pool))
	}

	// Unchecking .gbc removes it from the list; checking .png adds it.
	u.cfg.SevenZipExts = map[string]bool{".gbc": false, ".png": true}
	tab.refilter(u)
	listed = nil
	for _, it := range tab.items {
		listed = append(listed, filepath.Base(it.path))
	}
	if strings.Join(listed, ",") != "Game (USA).png,sf2.zip" {
		t.Fatalf("after the change, listed %v", listed)
	}
	u.cfg.SevenZipExts = nil
	tab.refilter(u)

	for _, it := range tab.items {
		out := filepath.Join(dir, sevenZipConverter.outName(it))
		in, size, err := sevenZipConverter.run(context.Background(), u, it, out, false, func(bool, int64, int64) {})
		if err != nil {
			t.Fatalf("%s: %v", it.path, err)
		}
		st, _ := os.Stat(out)
		if size != st.Size() || in == 0 {
			t.Errorf("%s: sizes %d -> %d", out, in, size)
		}
		entries, err := sevenzip.Check(context.Background(), out, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(out, "sf2.7z") {
			if len(entries) != 3 || entries[0].Name != "sf2.01" || !entries[0].Modified.Equal(when) || entries[2].Dir {
				t.Errorf("sf2.7z entries %+v", entries)
			}
		}
		if exe := os.Getenv("SEVENZIP"); exe != "" {
			if b, err := exec.Command(exe, "t", out).CombinedOutput(); err != nil {
				t.Errorf("7z t %s: %v\n%s", out, err, b)
			}
		}
	}
	if !removed["Game (USA).gbc"] || !removed["sf2.zip"] || len(removed) != 2 {
		t.Errorf("removed %v", removed)
	}
}
