package rvz

import (
	"context"
	"crypto/sha1"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// These tests compare with Dolphin's DolphinTool. They run only when
// DOLPHINTOOL names the executable, e.g.
// DOLPHINTOOL=C:\Dolphin-x64\DolphinTool.exe go test ./internal/rvz
func TestDolphinCompat(t *testing.T) {
	tool := os.Getenv("DOLPHINTOOL")
	if tool == "" {
		t.Skip("DOLPHINTOOL not set")
	}
	dir := t.TempDir()
	user := filepath.Join(dir, "user")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(tool, append(args, "-u", user)...).CombinedOutput()
		if err != nil {
			t.Fatalf("DolphinTool %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	img := gcDisc(rand.New(rand.NewPCG(7, 8)), 40<<20)
	iso := filepath.Join(dir, "game.iso")
	os.WriteFile(iso, img, 0o644)
	want := sha1.Sum(img)

	// Dolphin reads our RVZ back to the same disc.
	ours := filepath.Join(dir, "ours.rvz")
	if _, err := Compress(context.Background(), iso, ours, Options{Level: zstd.SpeedBetterCompression}, nil); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(dir, "back.iso")
	run("convert", "-i", ours, "-o", back, "-f", "iso")
	b, _ := os.ReadFile(back)
	if sha1.Sum(b) != want {
		t.Fatal("Dolphin's ISO of our RVZ differs from the disc")
	}
	t.Logf("verify:\n%s", run("verify", "-i", ours))

	// We read Dolphin's RVZ to the same disc.
	theirs := filepath.Join(dir, "theirs.rvz")
	run("convert", "-i", iso, "-o", theirs, "-f", "rvz", "-b", "131072", "-c", "zstd", "-l", "5")
	sum, err := Hash(context.Background(), theirs, nil)
	if err != nil || sum != want {
		t.Fatalf("reading Dolphin's RVZ: %x %v", sum, err)
	}
	a, _ := os.Stat(ours)
	c, _ := os.Stat(theirs)
	t.Logf("size: ours %d, Dolphin %d (%+.1f%%)", a.Size(), c.Size(), 100*float64(a.Size()-c.Size())/float64(c.Size()))
}

// A Wii disc (encrypted with a test common key): Dolphin rebuilds the
// encryption and hashes of our RVZ from the stored title key, and we read
// Dolphin's RVZ of the same disc.
func TestDolphinWii(t *testing.T) {
	tool := os.Getenv("DOLPHINTOOL")
	if tool == "" {
		t.Skip("DOLPHINTOOL not set")
	}
	dir := t.TempDir()
	user := filepath.Join(dir, "user")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(tool, append(args, "-u", user)...).CombinedOutput()
		if err != nil {
			t.Fatalf("DolphinTool %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	rng := rand.New(rand.NewPCG(13, 14))
	ck := make([]byte, 16)
	for i := range ck {
		ck[i] = byte(rng.Uint32())
	}
	img := wiiDisc(rng, ck, 150)
	iso := filepath.Join(dir, "wii.iso")
	os.WriteFile(iso, img, 0o644)
	want := sha1.Sum(img)

	ours := filepath.Join(dir, "ours.rvz")
	if _, err := Compress(context.Background(), iso, ours, Options{CommonKeys: [][]byte{ck}}, nil); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(dir, "back.iso")
	run("convert", "-i", ours, "-o", back, "-f", "iso")
	b, _ := os.ReadFile(back)
	if sha1.Sum(b) != want {
		n := 0
		for i := 0; i < len(b) && i < len(img); i++ {
			if b[i] != img[i] {
				if n == 0 {
					t.Logf("first difference at %#x (size %d vs %d)", i, len(b), len(img))
				}
				n++
			}
		}
		t.Fatalf("Dolphin's ISO of our Wii RVZ differs from the disc (%d bytes)", n)
	}

	theirs := filepath.Join(dir, "theirs.rvz")
	run("convert", "-i", iso, "-o", theirs, "-f", "rvz", "-b", "131072", "-c", "zstd", "-l", "5")
	sum, err := Hash(context.Background(), theirs, nil)
	if err != nil || sum != want {
		t.Fatalf("reading Dolphin's Wii RVZ: %x %v", sum, err)
	}
	r, _ := Open(theirs)
	t.Logf("Dolphin's RVZ: Wii=%v, %d regions", r.Wii(), len(r.regions))
	r.Close()
}
