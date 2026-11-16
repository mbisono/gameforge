package discinfo

import (
	"bytes"
	"context"
	"encoding/binary"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"gameforge/internal/rvz"
)

// gcWithBanner makes a GameCube disc whose root has opening.bnr, with a
// decoy of the same name in a subfolder.
func gcWithBanner() []byte {
	img := make([]byte, 2<<20)
	copy(img, "GALE01")
	binary.BigEndian.PutUint32(img[0x1c:], 0xc2339f3d)
	const fstOff, bnrOff, decoyOff = 0x10000, 0x20000, 0x30000
	names := []byte("sub\x00opening.bnr\x00")
	fst := make([]byte, 4*12)
	entry := func(i int, dir bool, name, a, b uint32) {
		e := fst[i*12:]
		binary.BigEndian.PutUint32(e, name)
		if dir {
			e[0] = 1
		}
		binary.BigEndian.PutUint32(e[4:], a)
		binary.BigEndian.PutUint32(e[8:], b)
	}
	entry(0, true, 0, 0, 4)              // root, 4 entries
	entry(1, true, 0, 0, 3)              // sub/, up to entry 3
	entry(2, false, 4, decoyOff, 0x1960) // sub/opening.bnr
	entry(3, false, 4, bnrOff, 0x1960)   // opening.bnr
	fst = append(fst, names...)
	copy(img[fstOff:], fst)
	binary.BigEndian.PutUint32(img[0x424:], fstOff)
	binary.BigEndian.PutUint32(img[0x428:], uint32(len(fst)))

	bnr := img[bnrOff:]
	copy(bnr, "BNR1")
	px := bnr[bannerImgOff:]
	binary.BigEndian.PutUint16(px[0:], 0xfc00) // opaque red (RGB555)
	binary.BigEndian.PutUint16(px[2:], 0x70f0) // ARGB3444: opaque green
	binary.BigEndian.PutUint16(px[4:], 0x000f) // transparent blue
	copy(img[decoyOff:], "XXXX")               // not a banner
	return img
}

func TestGameCubeBanner(t *testing.T) {
	img := gcWithBanner()
	b, err := GameCubeBanner(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	pic, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if pic.Bounds().Dx() != 96 || pic.Bounds().Dy() != 32 {
		t.Fatalf("size %v", pic.Bounds())
	}
	for x, want := range [][4]uint32{{0xffff, 0, 0, 0xffff}, {0, 0xffff, 0, 0xffff}, {0, 0, 0, 0}} {
		r, g, b, a := pic.At(x, 0).RGBA()
		if [4]uint32{r, g, b, a} != want {
			t.Errorf("pixel %d: %v %v %v %v", x, r, g, b, a)
		}
	}

	// The same banner from an RVZ of the disc.
	dir := t.TempDir()
	in, out := filepath.Join(dir, "g.iso"), filepath.Join(dir, "g.rvz")
	os.WriteFile(in, img, 0o644)
	if _, err := rvz.Compress(context.Background(), in, out, rvz.Options{Level: zstd.SpeedDefault}, nil); err != nil {
		t.Fatal(err)
	}
	r, err := rvz.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fromRVZ, err := GameCubeBanner(r, r.ISOSize)
	if err != nil || !bytes.Equal(fromRVZ, b) {
		t.Errorf("banner from the RVZ differs (%v)", err)
	}

	if _, err := GameCubeBanner(bytes.NewReader(make([]byte, 1<<20)), 1<<20); err == nil {
		t.Error("banner found on an empty image")
	}
}
