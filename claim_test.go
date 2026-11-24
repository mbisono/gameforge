package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gameforge/internal/config"
	"gameforge/internal/sevenzip"
)

// TestSameOutputOnce converts two items with the same output name at the
// same time: only one may write it.
func TestSameOutputOnce(t *testing.T) {
	dir := t.TempDir()
	u := &ui{cfg: &config.Config{}}
	tab := &convTab{checked: map[string]bool{}}
	var items []*convItem
	for _, n := range []string{"Game.gb", "Game.zip"} {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte(n), 0o644)
		items = append(items, &convItem{path: p, tab: tab, ok: true})
	}
	var runs atomic.Int32
	act := convAction{
		outName: func(*convItem) string { return "Game.7z" },
		run: func(ctx context.Context, u *ui, it *convItem, out string, verify bool,
			progress func(bool, int64, int64)) (int64, int64, error) {
			runs.Add(1)
			time.Sleep(50 * time.Millisecond)
			return 1, 1, os.WriteFile(out, []byte(it.path), 0o644)
		},
	}
	done := tab.convertAll(context.Background(), u, items, "", false, 2, act)
	if runs.Load() != 1 || len(done) != 1 {
		t.Fatalf("%d runs, %d converted; want 1 and 1", runs.Load(), len(done))
	}
}

// TestPanicFailsOneItem: a panic while converting one item fails that
// item; the others are converted and the app goes on.
func TestPanicFailsOneItem(t *testing.T) {
	dir := t.TempDir()
	u := &ui{cfg: &config.Config{}}
	tab := &convTab{checked: map[string]bool{}}
	var items []*convItem
	for _, n := range []string{"bad.iso", "good.iso"} {
		p := filepath.Join(dir, n)
		os.WriteFile(p, nil, 0o644)
		items = append(items, &convItem{path: p, tab: tab, ok: true})
	}
	act := convAction{
		outName: func(it *convItem) string { return filepath.Base(it.path) + ".out" },
		run: func(ctx context.Context, u *ui, it *convItem, out string, verify bool,
			progress func(bool, int64, int64)) (int64, int64, error) {
			if filepath.Base(it.path) == "bad.iso" {
				var b []byte
				_ = b[5] // index out of range
			}
			return 1, 1, os.WriteFile(out, nil, 0o644)
		},
	}
	for _, jobs := range []int{1, 2} {
		os.Remove(filepath.Join(dir, "good.iso.out"))
		done := tab.convertAll(context.Background(), u, items, "", false, jobs, act)
		if len(done) != 1 || filepath.Base(done[0].item.path) != "good.iso" {
			t.Fatalf("jobs %d: converted %v", jobs, done)
		}
	}
}

// TestReplaceRemoved: after "delete the originals", the list shows the
// archives instead of the files that are gone.
func TestReplaceRemoved(t *testing.T) {
	dir := t.TempDir()
	u := &ui{cfg: &config.Config{}}
	sevenZip.ui = u
	defer func() { sevenZip.ui = nil }()
	rom := filepath.Join(dir, "Game (USA).gb")
	other := filepath.Join(dir, "Other (USA).gb")
	os.WriteFile(rom, []byte("ROM DATA"), 0o644)
	os.WriteFile(other, []byte("OTHER"), 0o644)
	tab := &convTab{conv: sevenZipConverter, checked: map[string]bool{}}
	tab.add(u, []string{dir})

	out := filepath.Join(dir, "Game (USA).7z")
	src := sevenzip.Source{Name: "Game (USA).gb", Size: 8, Modified: time.Now(),
		Open: func() (io.ReadCloser, error) { return os.Open(rom) }}
	if _, err := sevenzip.Write(context.Background(), out, []sevenzip.Source{src}, nil); err != nil {
		t.Fatal(err)
	}
	os.Remove(rom)
	var it *convItem
	for _, i := range tab.items {
		if i.path == rom {
			it = i
		}
	}
	tab.replaceRemoved(u, []converted{{it, out}})
	var listed []string
	for _, i := range tab.items {
		listed = append(listed, filepath.Base(i.path))
	}
	if strings.Join(listed, ",") != "Other (USA).gb,Game (USA).7z" {
		t.Fatalf("listed %v", listed)
	}
	if _, ok := tab.checked[rom]; ok {
		t.Error("the removed file is still checked")
	}
}
