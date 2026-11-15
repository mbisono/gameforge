package sevenzip

import (
	"bytes"
	"context"
	"hash/crc32"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type file struct {
	name string
	data []byte
}

func testFiles() []file {
	rnd := rand.New(rand.NewPCG(1, 2))
	random := make([]byte, 200_000)
	for i := range random {
		random[i] = byte(rnd.Uint32())
	}
	text := bytes.Repeat([]byte("GameForge 7z test line, compressible. "), 80_000)
	return []file{
		{"game.rom", text},
		{"sub/ação ✓.bin", random},
		{"empty.txt", nil},
		{"small.dat", []byte{1, 2, 3}},
	}
}

func sources(files []file, when time.Time) []Source {
	var out []Source
	for _, f := range files {
		out = append(out, Source{
			Name: f.name, Size: int64(len(f.data)), Modified: when,
			Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(f.data)), nil },
		})
	}
	return out
}

func writeArchive(t *testing.T, files []file) (string, Result) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "test.7z")
	when := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	res, err := Write(context.Background(), out, sources(files, when), nil)
	if err != nil {
		t.Fatal(err)
	}
	return out, res
}

func checkEntries(t *testing.T, got []Entry, files []file) {
	t.Helper()
	if len(got) != len(files) {
		t.Fatalf("%d entries, want %d", len(got), len(files))
	}
	for i, f := range files {
		e := got[i]
		if e.Name != f.name || e.Size != int64(len(f.data)) || e.CRC != crc32.ChecksumIEEE(f.data) || e.Dir {
			t.Errorf("entry %d = %+v, want %s (%d bytes)", i, e, f.name, len(f.data))
		}
	}
}

func TestRoundTrip(t *testing.T) {
	files := testFiles()
	out, res := writeArchive(t, files)
	checkEntries(t, res.Entries, files)
	got, err := Check(context.Background(), out, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkEntries(t, got, files)
	if !got[0].Modified.Equal(time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)) {
		t.Errorf("time %v", got[0].Modified)
	}
	st, _ := os.Stat(out)
	if st.Size() != res.OutputSize {
		t.Errorf("size %d, result says %d", st.Size(), res.OutputSize)
	}
	if res.OutputSize > 260_000 {
		t.Errorf("archive too large: %d bytes", res.OutputSize)
	}
}

func TestSingleAndEmpty(t *testing.T) {
	for _, files := range [][]file{
		{{"only.gbc", bytes.Repeat([]byte{7}, 50_000)}},
		{{"a", nil}, {"b", nil}},
	} {
		out, _ := writeArchive(t, files)
		got, err := Check(context.Background(), out, nil)
		if err != nil {
			t.Fatal(err)
		}
		checkEntries(t, got, files)
	}
}

func TestList(t *testing.T) {
	files := testFiles()
	out, _ := writeArchive(t, files)
	got, err := List(out)
	if err != nil {
		t.Fatal(err)
	}
	checkEntries(t, got, files)
}

func TestCorruption(t *testing.T) {
	files := testFiles()
	out, res := writeArchive(t, files)
	b, _ := os.ReadFile(out)
	b[sigHeaderSize+int(res.OutputSize-sigHeaderSize)/3] ^= 0x55 // inside the packed data
	os.WriteFile(out, b, 0o644)
	if _, err := Check(context.Background(), out, nil); err == nil {
		t.Fatal("corrupt archive passed the check")
	}
}

func TestNumbers(t *testing.T) {
	for _, v := range []uint64{0, 1, 0x7F, 0x80, 0x3FFF, 0x4000, 1 << 20, 1<<56 - 1, 1 << 56, 1<<64 - 1} {
		var b hbuf
		b.num(v)
		p := &parser{b: b.Bytes()}
		if got := p.num(); got != v || p.err != nil || len(p.b) != 0 {
			t.Errorf("num %#x: got %#x (%v, %d left)", v, got, p.err, len(p.b))
		}
	}
}

// Test7Zip checks the archives against 7-Zip; it runs only when SEVENZIP
// points to 7z.exe (or 7z).
func Test7Zip(t *testing.T) {
	exe := os.Getenv("SEVENZIP")
	if exe == "" {
		t.Skip("SEVENZIP not set")
	}
	for _, set := range [][]file{
		{{"only.gbc", bytes.Repeat([]byte{7}, 50_000)}},
		{{"a", nil}, {"b", nil}},
	} {
		out, _ := writeArchive(t, set)
		b, err := exec.Command(exe, "l", "-slt", out).CombinedOutput()
		if err != nil || !strings.Contains(string(b), "Path = "+set[0].name) || strings.Contains(string(b), "Folder = +") {
			t.Fatalf("7z l: %v\n%s", err, b)
		}
		if b, err := exec.Command(exe, "t", out).CombinedOutput(); err != nil || !strings.Contains(string(b), "Everything is Ok") {
			t.Fatalf("7z t: %v\n%s", err, b)
		}
	}
	files := testFiles()
	out, _ := writeArchive(t, files)
	if b, err := exec.Command(exe, "t", out).CombinedOutput(); err != nil || !strings.Contains(string(b), "Everything is Ok") {
		t.Fatalf("7z t: %v\n%s", err, b)
	}
	dir := t.TempDir()
	if b, err := exec.Command(exe, "x", "-o"+dir, out).CombinedOutput(); err != nil {
		t.Fatalf("7z x: %v\n%s", err, b)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.name)))
		if err != nil || !bytes.Equal(got, f.data) {
			t.Errorf("%s extracted differently (%v)", f.name, err)
		}
	}

	// The reader opens 7-Zip's own archives (with a plain header).
	src := t.TempDir()
	for _, f := range files {
		p := filepath.Join(src, filepath.FromSlash(f.name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, f.data, 0o644)
	}
	theirs := filepath.Join(t.TempDir(), "theirs.7z")
	cmd := exec.Command(exe, "a", "-mhc=off", "-m0=lzma2", "-mx=9", "-mmt=1", theirs, ".")
	cmd.Dir = src
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("7z a: %v\n%s", err, b)
	}
	entries, err := Check(context.Background(), theirs, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		for _, f := range files {
			if e.Name == f.name && e.Size == int64(len(f.data)) && e.CRC == crc32.ChecksumIEEE(f.data) {
				found++
			}
		}
	}
	if found != len(files) {
		t.Errorf("found %d of %d files in 7-Zip's archive: %+v", found, len(files), entries)
	}

	// List reads 7-Zip's default archives, whose header is compressed,
	// with one solid block or a block per file.
	for _, args := range [][]string{{"-mx=5"}, {"-mx=5", "-ms=off"}, {"-mx=9", "-m0=lzma"}} {
		def := filepath.Join(t.TempDir(), "default.7z")
		cmd := exec.Command(exe, append(append([]string{"a"}, args...), def, ".")...)
		cmd.Dir = src
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("7z a: %v\n%s", err, b)
		}
		listed, err := List(def)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		found := 0
		for _, e := range listed {
			for _, f := range files {
				if e.Name == f.name && e.Size == int64(len(f.data)) && e.CRC == crc32.ChecksumIEEE(f.data) {
					found++
				}
			}
		}
		if found != len(files) {
			t.Errorf("%v: listed %d of %d files: %+v", args, found, len(files), listed)
		}
	}
}
