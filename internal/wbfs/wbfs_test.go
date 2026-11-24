package wbfs

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// makeWBFS stores the clusters of disc that are not all zero in a WBFS
// image with 32 KiB clusters, split into parts of at most split bytes.
func makeWBFS(t *testing.T, dir string, disc []byte, split int) string {
	const hd, cl = 512, 0x8000
	clusters := 2 * layerSectors * wiiSector / cl
	infoSize := (256 + 2*clusters + hd - 1) / hd * hd
	first := (hd + infoSize + cl - 1) / cl // the first cluster after the header
	img := make([]byte, first*cl)
	copy(img, "WBFS")
	img[8], img[9], img[12] = 9, 15, 1
	copy(img[hd:], disc[:256])
	next := first
	for c := 0; c*cl < len(disc); c++ {
		part := disc[c*cl : min((c+1)*cl, len(disc))]
		if bytes.Count(part, []byte{0}) == len(part) {
			continue
		}
		binary.BigEndian.PutUint16(img[hd+256+2*c:], uint16(next))
		img = append(img, make([]byte, cl)...)
		copy(img[next*cl:], part)
		next++
	}
	binary.BigEndian.PutUint32(img[4:], uint32(len(img)/hd))
	path := filepath.Join(dir, "game.wbfs")
	for i := 0; len(img) > 0; i++ {
		n := min(split, len(img))
		name := path
		if i > 0 {
			name = filepath.Join(dir, "game.wbf"+string(rune('0'+i)))
		}
		os.WriteFile(name, img[:n], 0o644)
		img = img[n:]
	}
	return path
}

func TestReader(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	disc := make([]byte, 40*0x8000+1234)
	for c := 0; c < 40; c++ {
		if c%3 == 1 {
			continue // left out of the image: zeros
		}
		for i := c * 0x8000; i < (c+1)*0x8000; i++ {
			disc[i] = byte(rng.IntN(256))
		}
	}
	copy(disc, "RSPE01")
	for _, split := range []int{1 << 30, 300000} {
		path := makeWBFS(t, t.TempDir(), disc, split)
		r, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if r.Size() != SingleLayer || string(r.Header()[:6]) != "RSPE01" {
			t.Fatalf("size %d, header %q", r.Size(), r.Header()[:6])
		}
		for range 100 {
			off := rng.IntN(len(disc))
			n := min(rng.IntN(100000), len(disc)-off)
			got := make([]byte, n)
			if _, err := r.ReadAt(got, int64(off)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, disc[off:off+n]) {
				t.Fatalf("split %d: data at %d differs", split, off)
			}
		}
		// Past the stored data, the disc reads as zeros up to its size.
		tail := make([]byte, 100)
		if _, err := r.ReadAt(tail, SingleLayer-100); err != nil || bytes.Count(tail, []byte{0}) != 100 {
			t.Fatalf("tail: %v", err)
		}
	}
}
