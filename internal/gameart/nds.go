package gameart

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
)

// A Nintendo DS ROM tells at 0x68 where its banner is. The banner starts
// with a version, then holds the 32×32 icon at 0x20: 4 bits a pixel in
// 8×8 tiles, with 16 BGR555 colors at 0x220 (color 0 is transparent).
const (
	ndsBannerAt = 0x68
	ndsIconLen  = 0x240
)

// ErrDone stops a copy into an NDSIcon once the icon has been read.
var ErrDone = errors.New("icon read")

// NDSIcon collects the banner icon of a Nintendo DS ROM written to it from
// the start, so that a ROM inside an archive is read only up to the icon.
// Write returns ErrDone once the icon is complete.
type NDSIcon struct {
	n    int64
	hdr  [ndsBannerAt + 4]byte
	at   int64
	icon [ndsIconLen]byte
	got  int
}

func (s *NDSIcon) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		switch {
		case s.n < int64(len(s.hdr)):
			k := copy(s.hdr[s.n:], p)
			s.n, p = s.n+int64(k), p[k:]
			if s.n == int64(len(s.hdr)) {
				s.at = int64(binary.LittleEndian.Uint32(s.hdr[ndsBannerAt:]))
				if s.at < int64(len(s.hdr)) {
					return total, errors.New("the ROM has no banner")
				}
			}
		case s.n < s.at:
			k := min(int64(len(p)), s.at-s.n)
			s.n, p = s.n+k, p[k:]
		default:
			k := copy(s.icon[s.got:], p)
			s.got, s.n, p = s.got+k, s.n+int64(k), p[k:]
			if s.got == ndsIconLen {
				return total, ErrDone
			}
		}
	}
	return total, nil
}

// PNG returns the icon read, as PNG.
func (s *NDSIcon) PNG() ([]byte, error) {
	if s.got < ndsIconLen {
		return nil, errors.New("the ROM ends before its banner")
	}
	b := s.icon[:]
	if v := binary.LittleEndian.Uint16(b); v != 1 && v != 2 && v != 3 && v != 0x103 {
		return nil, errors.New("not a DS banner")
	}
	var pal [16]color.NRGBA
	for i := range pal {
		c := uint32(binary.LittleEndian.Uint16(b[0x220+2*i:]))
		pal[i] = color.NRGBA{expand(c, 5), expand(c>>5, 5), expand(c>>10, 5), 255}
	}
	pal[0].A = 0
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for i := range 32 * 32 {
		tile, in := i/64, i%64
		x, y := tile%4*8+in%8, tile/4*8+in/8
		v := b[0x20+i/2]
		if i%2 == 1 {
			v >>= 4
		}
		img.SetNRGBA(x, y, pal[v&15])
	}
	return encode(img)
}

// NDS reads the banner icon of a Nintendo DS ROM from r, which starts at
// the ROM's first byte, and returns it as PNG.
func NDS(r io.Reader) ([]byte, error) {
	var s NDSIcon
	if _, err := io.Copy(&s, r); err != nil && !errors.Is(err, ErrDone) {
		return nil, err
	}
	return s.PNG()
}
