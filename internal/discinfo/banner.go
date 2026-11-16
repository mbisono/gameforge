package discinfo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"

	"gameforge/internal/gameart"
)

// GameCube banners (opening.bnr) hold a 96×32 image in RGB5A3 format,
// in 4×4 pixel tiles, after a 0x20-byte header ("BNR1" or "BNR2").
const (
	bannerW      = 96
	bannerH      = 32
	bannerImgOff = 0x20
	bannerImgLen = bannerW * bannerH * 2
)

// GameCubeBanner reads the banner image of a GameCube disc, from the file
// opening.bnr in the disc's root, and returns it as PNG. r reads the disc
// image (an ISO, or an RVZ through its reader), which has size bytes.
func GameCubeBanner(r io.ReaderAt, size int64) ([]byte, error) {
	off, n, err := gcRootFile(r, size, "opening.bnr")
	if err != nil {
		return nil, err
	}
	if n < bannerImgOff+bannerImgLen {
		return nil, errors.New("banner too small")
	}
	b := make([]byte, bannerImgOff+bannerImgLen)
	if _, err := r.ReadAt(b, off); err != nil {
		return nil, err
	}
	if m := string(b[:4]); m != "BNR1" && m != "BNR2" {
		return nil, errors.New("not a GameCube banner")
	}
	img := decodeRGB5A3(b[bannerImgOff:], bannerW, bannerH)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// gcRootFile finds a file in the root folder of a GameCube disc's file
// system table and returns its offset and size.
func gcRootFile(r io.ReaderAt, size int64, name string) (int64, int64, error) {
	hdr := make([]byte, 0x440)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return 0, 0, err
	}
	if binary.BigEndian.Uint32(hdr[0x1c:]) != 0xc2339f3d {
		return 0, 0, errors.New("not a GameCube disc")
	}
	fstOff := int64(binary.BigEndian.Uint32(hdr[0x424:]))
	fstSize := int64(binary.BigEndian.Uint32(hdr[0x428:]))
	if fstOff <= 0 || fstSize < 12 || fstOff+fstSize > size || fstSize > 64<<20 {
		return 0, 0, errors.New("no file system table")
	}
	fst := make([]byte, fstSize)
	if _, err := r.ReadAt(fst, fstOff); err != nil {
		return 0, 0, err
	}
	count := int64(binary.BigEndian.Uint32(fst[8:]))
	if count < 1 || count*12 > fstSize {
		return 0, 0, errors.New("invalid file system table")
	}
	names := fst[count*12:]
	// Root entries only: a folder's entry tells the index after its
	// contents, which are skipped.
	for i := int64(1); i < count; {
		e := fst[i*12:]
		if e[0] != 0 {
			next := int64(binary.BigEndian.Uint32(e[8:]))
			if next <= i {
				return 0, 0, errors.New("invalid file system table")
			}
			i = next
			continue
		}
		at := int(binary.BigEndian.Uint32(e[:4]) & 0xffffff)
		if at < len(names) {
			n := names[at:]
			if z := bytes.IndexByte(n, 0); z >= 0 {
				n = n[:z]
			}
			if strings.EqualFold(string(n), name) {
				off, n := int64(binary.BigEndian.Uint32(e[4:])), int64(binary.BigEndian.Uint32(e[8:]))
				if off+n > size {
					return 0, 0, errors.New("file beyond the disc")
				}
				return off, n, nil
			}
		}
		i++
	}
	return 0, 0, errors.New(name + " not found")
}

// decodeRGB5A3 decodes an RGB5A3 texture of 4×4 tiles: each 16-bit pixel
// is RGB555 opaque when its top bit is set, otherwise ARGB3444.
func decodeRGB5A3(b []byte, w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	p := 0
	for ty := 0; ty < h; ty += 4 {
		for tx := 0; tx < w; tx += 4 {
			for y := ty; y < ty+4; y++ {
				for x := tx; x < tx+4; x++ {
					v := binary.BigEndian.Uint16(b[p:])
					p += 2
					var c color.NRGBA
					if v&0x8000 != 0 {
						c = color.NRGBA{scale5(v >> 10), scale5(v >> 5), scale5(v), 255}
					} else {
						a := uint8(v>>12) & 7
						c = color.NRGBA{
							R: uint8((v>>8)&0xf) * 0x11, G: uint8((v>>4)&0xf) * 0x11, B: uint8(v&0xf) * 0x11,
							A: a<<5 | a<<2 | a>>1,
						}
					}
					img.SetNRGBA(x, y, c)
				}
			}
		}
	}
	return img
}

func scale5(v uint16) uint8 {
	v &= 0x1f
	return uint8(v<<3 | v>>2)
}

// DreamcastLabel reads the disc label of a Dreamcast game, the file
// 0GDTEX.PVR in the root of its file system, and returns it as PNG. read
// returns a sector's 2048 bytes by its address on the disc, and start is
// the first sector of the file system's track (45000 on a GD-ROM).
func DreamcastLabel(read func(lba int64) ([]byte, error), start int64) ([]byte, error) {
	abs := func(lba int) ([]byte, error) { return read(int64(lba)) }
	pvd, err := read(start + 16)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(pvd, []byte("\x01CD001")) {
		return nil, errors.New("no ISO 9660 file system")
	}
	root := extent{int(binary.LittleEndian.Uint32(pvd[158:])), int(binary.LittleEndian.Uint32(pvd[166:]))}
	dir, err := listDir(abs, root)
	if err != nil {
		return nil, err
	}
	e, ok := dir["0GDTEX.PVR"]
	if !ok {
		return nil, errors.New("the disc has no label image")
	}
	b, err := readFileMax(abs, e, 4<<20)
	if err != nil {
		return nil, err
	}
	return gameart.PVR(b)
}
