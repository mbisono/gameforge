package ciso

import (
	"bytes"
	"context"
	"crypto/sha1"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/pierrec/lz4/v4"
)

func testImage(rng *rand.Rand, size int) []byte {
	img := make([]byte, size)
	for i := range img {
		switch {
		case i < size/3:
			img[i] = byte(rng.IntN(8)) // compressible
		case i < size/2:
			img[i] = byte(rng.Uint32()) // incompressible: stored plain
		default: // zeros
		}
	}
	return img
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	dir := t.TempDir()
	for _, size := range []int{1000 * blockSize, 777*blockSize + 100} {
		img := testImage(rng, size)
		in := filepath.Join(dir, "game.iso")
		os.WriteFile(in, img, 0o644)
		want := sha1.Sum(img)
		for _, format := range []Format{CSO, ZSO} {
			out := filepath.Join(dir, "game"+format.Ext())
			os.Remove(out)
			res, err := Compress(context.Background(), in, out, format, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.SHA1 != want || res.OutputSize >= int64(size) {
				t.Fatalf("%v: result %+v", format, res)
			}
			st, _ := os.Stat(out)
			if st.Size() != res.OutputSize {
				t.Errorf("%v: file size %d, result %d", format, st.Size(), res.OutputSize)
			}
			r, err := Open(out)
			if err != nil {
				t.Fatal(err)
			}
			if r.Header.Format != format || r.Header.Size != int64(size) || r.Header.BlockSize != blockSize {
				t.Errorf("%v: header %+v", format, r.Header)
			}
			r.Close()
			sum, err := Hash(context.Background(), out, nil)
			if err != nil || sum != want {
				t.Fatalf("%v: hash %x %v", format, sum, err)
			}
			back := filepath.Join(dir, "back.iso")
			os.Remove(back)
			if _, err := Decompress(context.Background(), out, back, nil); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(back)
			if !bytes.Equal(got, img) {
				t.Fatalf("%v: decompressed image differs", format)
			}
		}
	}
}

// A shifted index (as for images over 2 GiB) with padded blocks.
func TestShiftedIndex(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	img := testImage(rng, 300*blockSize)
	dir := t.TempDir()
	in := filepath.Join(dir, "x.iso")
	os.WriteFile(in, img, 0o644)
	for _, format := range []Format{CSO, ZSO} {
		out := filepath.Join(dir, "x"+format.Ext())
		if _, err := Compress(context.Background(), in, out, format, nil); err != nil {
			t.Fatal(err)
		}
		// Rewrite with index shift 2: every block at a multiple of 4 bytes.
		r, _ := Open(out)
		var blocks [][]byte
		var plain []bool
		for i := range r.Blocks() {
			s := int64(r.index[i]&^plainFlag) << r.Header.Shift
			e := int64(r.index[i+1]&^plainFlag) << r.Header.Shift
			b := make([]byte, e-s)
			r.f.ReadAt(b, s)
			blocks, plain = append(blocks, b), append(plain, r.index[i]&plainFlag != 0)
		}
		r.Close()
		hdr, _ := os.ReadFile(out)
		hdr = hdr[:headerSize]
		hdr[21] = 2
		index := make([]byte, (len(blocks)+1)*4)
		var data []byte
		pos := len(hdr) + len(index)
		for i, b := range blocks {
			for pos%4 != 0 {
				data = append(data, 0xee) // padding the reader must ignore
				pos++
			}
			v := uint32(pos >> 2)
			if plain[i] {
				v |= plainFlag
			}
			index[4*i], index[4*i+1], index[4*i+2], index[4*i+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
			data = append(data, b...)
			pos += len(b)
		}
		for pos%4 != 0 {
			data = append(data, 0)
			pos++
		}
		v := uint32(pos >> 2)
		n := len(blocks)
		index[4*n], index[4*n+1], index[4*n+2], index[4*n+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		shifted := filepath.Join(dir, "shifted"+format.Ext())
		os.WriteFile(shifted, append(append(hdr, index...), data...), 0o644)
		sum, err := Hash(context.Background(), shifted, nil)
		if err != nil || sum != sha1.Sum(img) {
			t.Fatalf("%v: shifted image: %x %v", format, sum, err)
		}
	}
}

func TestLZ4Decode(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	for k := range 200 {
		src := make([]byte, blockSize)
		for i := range src {
			if k%3 == 0 {
				src[i] = byte(rng.Uint32())
			} else {
				src[i] = byte(rng.IntN(1 + k%20))
			}
		}
		var c lz4.CompressorHC
		buf := make([]byte, lz4.CompressBlockBound(len(src)))
		n, err := c.CompressBlock(src, buf)
		if err != nil || n == 0 {
			continue
		}
		dst := make([]byte, len(src))
		if err := lz4Decode(append(buf[:n:n], 0, 0, 0), dst); err != nil || !bytes.Equal(dst, src) {
			t.Fatalf("case %d: %v", k, err)
		}
	}
}

func TestNotCSO(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.cso")
	os.WriteFile(p, make([]byte, 100), 0o644)
	if _, err := Open(p); err == nil {
		t.Fatal("opened a non-CSO file")
	}
}
