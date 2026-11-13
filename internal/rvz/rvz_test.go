package rvz

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestLFGRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, off := range []int{0, 4, 7, 100, 2084, 5000, 0x7000} {
		seed := make([]byte, SeedSize)
		for i := range seed {
			seed[i] = byte(rng.Uint32())
		}
		data := make([]byte, blockBytes-off)
		junk(seed, int64(off), data)
		got, n := recoverSeed(data, off)
		if n != len(data) {
			t.Fatalf("offset %d: reproduced %d of %d bytes", off, n, len(data))
		}
		again := make([]byte, len(data))
		junk(got[:], int64(off), again)
		if !bytes.Equal(again, data) {
			t.Fatalf("offset %d: recovered seed gives other data", off)
		}
	}
	// Ordinary data is not mistaken for padding.
	data := make([]byte, 8192)
	for i := range data {
		data[i] = byte(rng.IntN(7))
	}
	if _, n := recoverSeed(data, 0); n != 0 {
		t.Fatalf("plain data recognized as padding (%d bytes)", n)
	}
}

// gcDisc builds a GameCube disc: header, FST, files, and generator padding
// (a new seed per 32 KiB block) after each file, with a few zero bytes in
// between as on real discs.
func gcDisc(rng *rand.Rand, size int) []byte {
	img := make([]byte, size)
	seeds := map[int64][]byte{}
	seedFor := func(block int64) []byte {
		if s, ok := seeds[block]; ok {
			return s
		}
		s := make([]byte, SeedSize)
		for i := range s {
			s[i] = byte(rng.Uint32())
		}
		seeds[block] = s
		return s
	}
	fill := func(from, to int64) { // padding over [from, to)
		for b := from &^ (blockBytes - 1); b < to; b += blockBytes {
			s, e := max(from, b), min(to, b+blockBytes)
			buf := make([]byte, blockBytes)
			junk(seedFor(b/blockBytes), b, buf)
			copy(img[s:e], buf[s-b:e-b])
		}
	}
	binary.BigEndian.PutUint32(img[0x1c:], gcMagic)
	copy(img, "GTST01")
	// Files of various sizes, some compressible.
	type f struct{ off, size int64 }
	var files []f
	pos := int64(0x50000)
	for i := 0; i < 40 && pos < int64(size)-400000; i++ {
		n := int64(1000 + rng.IntN(300000))
		files = append(files, f{pos, n})
		for k := pos; k < pos+n; k++ {
			if i%3 == 0 {
				img[k] = byte(rng.Uint32())
			} else {
				img[k] = byte(rng.IntN(5))
			}
		}
		gap := int64(32) // zeros after the file
		next := (pos + n + gap + 0x7fff) &^ 0x7fff
		next += int64(rng.IntN(4)) * blockBytes
		fill(pos+n+gap, next)
		pos = next
	}
	fill(pos, int64(size))
	// FST after the header: root, then the files.
	fstOff := int64(0x2440)
	fst := make([]byte, 12*(len(files)+1))
	fst[0] = 1
	binary.BigEndian.PutUint32(fst[8:], uint32(len(files)+1))
	for i, x := range files {
		e := fst[12*(i+1):]
		binary.BigEndian.PutUint32(e[4:], uint32(x.off))
		binary.BigEndian.PutUint32(e[8:], uint32(x.size))
	}
	copy(img[fstOff:], fst)
	binary.BigEndian.PutUint32(img[0x424:], uint32(fstOff))
	binary.BigEndian.PutUint32(img[0x428:], uint32(len(fst)))
	return img
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	dir := t.TempDir()
	for _, size := range []int{24 << 20, 24<<20 + 0x8000} {
		img := gcDisc(rng, size)
		in := filepath.Join(dir, "game.iso")
		os.WriteFile(in, img, 0o644)
		out := filepath.Join(dir, "game.rvz")
		os.Remove(out)
		res, err := Compress(context.Background(), in, out, Options{Level: zstd.SpeedDefault}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.SHA1 != sha1.Sum(img) {
			t.Fatal("SHA-1 of the source")
		}
		t.Logf("%d -> %d bytes", size, res.OutputSize)
		if res.OutputSize > int64(size)/4 {
			t.Errorf("padding not packed: %d -> %d", size, res.OutputSize)
		}
		sum, err := Hash(context.Background(), out, nil)
		if err != nil || sum != res.SHA1 {
			t.Fatalf("hash %x %v", sum, err)
		}
		back := filepath.Join(dir, "back.iso")
		os.Remove(back)
		if _, err := Decompress(context.Background(), out, back, nil); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(back)
		if !bytes.Equal(got, img) {
			t.Fatal("decompressed image differs")
		}
	}
}

func TestNotGameCube(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.iso")
	os.WriteFile(p, make([]byte, 1<<20), 0o644)
	if _, err := Compress(context.Background(), p, filepath.Join(dir, "x.rvz"), Options{}, nil); err == nil {
		t.Fatal("compressed a non-GameCube image")
	}
	if _, err := Open(p); err == nil {
		t.Fatal("opened a non-RVZ file")
	}
}
