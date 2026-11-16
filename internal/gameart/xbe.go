package gameart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
)

// XBE finds the title image of an Xbox game in its default.xbe (the
// section $$XTIMAGE, or the save image $$XSIMAGE when there is none),
// which has size bytes, and returns it as PNG.
func XBE(r io.ReaderAt, size int64) ([]byte, error) {
	hdr := make([]byte, 0x178)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return nil, err
	}
	if string(hdr[:4]) != "XBEH" {
		return nil, errors.New("not an XBE")
	}
	le := binary.LittleEndian
	base := int64(le.Uint32(hdr[0x104:]))
	count := int64(le.Uint32(hdr[0x11C:]))
	at := int64(le.Uint32(hdr[0x120:])) - base
	if count == 0 || count > 1000 || at < 0 || at+count*56 > size {
		return nil, errors.New("invalid XBE section table")
	}
	secs := make([]byte, count*56)
	if _, err := r.ReadAt(secs, at); err != nil {
		return nil, err
	}
	found := map[string][2]int64{}
	for i := range count {
		s := secs[i*56:]
		name := make([]byte, 16)
		n, _ := r.ReadAt(name, int64(le.Uint32(s[20:]))-base)
		name = name[:n]
		for j, c := range name {
			if c == 0 {
				name = name[:j]
				break
			}
		}
		found[string(name)] = [2]int64{int64(le.Uint32(s[12:])), int64(le.Uint32(s[16:]))}
	}
	for _, want := range []string{"$$XTIMAGE", "$$XSIMAGE"} {
		sec, ok := found[want]
		if !ok || sec[1] < 20 || sec[1] > 4<<20 || sec[0]+sec[1] > size {
			continue
		}
		b := make([]byte, sec[1])
		if _, err := r.ReadAt(b, sec[0]); err != nil {
			return nil, err
		}
		img, err := decodeXPR(b)
		if err != nil {
			return nil, err
		}
		return encode(img)
	}
	return nil, errors.New("the XBE has no title image")
}

// decodeXPR decodes an Xbox texture resource (XPR0) with one texture.
func decodeXPR(b []byte) (image.Image, error) {
	le := binary.LittleEndian
	if len(b) < 32 || string(b[:4]) != "XPR0" {
		return nil, errors.New("not an XPR texture")
	}
	dataAt := int(le.Uint32(b[8:]))
	format, sizeField := le.Uint32(b[24:]), le.Uint32(b[28:])
	if dataAt > len(b) {
		return nil, errors.New("truncated XPR")
	}
	data := b[dataAt:]
	w, h := 1<<(format>>20&15), 1<<(format>>24&15)
	if sizeField != 0 {
		w, h = int(sizeField&0xFFF)+1, int(sizeField>>12&0xFFF)+1
	}
	if w > 1024 || h > 1024 {
		return nil, fmt.Errorf("XPR size %d×%d", w, h)
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	kind := format >> 8 & 0xFF
	switch kind {
	case 0x0C, 0x0E, 0x0F: // DXT1, DXT3, DXT5
		if err := decodeDXT(img, data, kind); err != nil {
			return nil, err
		}
		return img, nil
	}
	var bpp int
	var col func(uint32) color.NRGBA
	swizzled := true
	switch kind {
	case 0x02, 0x10: // A1R5G5B5
		bpp, col = 2, func(c uint32) color.NRGBA { return argb1555(uint16(c)) }
	case 0x03: // X1R5G5B5
		bpp, col = 2, func(c uint32) color.NRGBA { return argb1555(uint16(c) | 0x8000) }
	case 0x04, 0x1D: // A4R4G4B4
		bpp, col = 2, func(c uint32) color.NRGBA { return argb4444(uint16(c)) }
	case 0x05, 0x11: // R5G6B5
		bpp, col = 2, func(c uint32) color.NRGBA { return rgb565(uint16(c)) }
	case 0x06, 0x12: // A8R8G8B8
		bpp, col = 4, func(c uint32) color.NRGBA {
			return color.NRGBA{uint8(c >> 16), uint8(c >> 8), uint8(c), uint8(c >> 24)}
		}
	case 0x07, 0x1E: // X8R8G8B8
		bpp, col = 4, func(c uint32) color.NRGBA { return color.NRGBA{uint8(c >> 16), uint8(c >> 8), uint8(c), 255} }
	default:
		return nil, fmt.Errorf("XPR format %#x is not read", kind)
	}
	if kind >= 0x10 {
		swizzled = false
	}
	if len(data) < w*h*bpp {
		return nil, errors.New("truncated XPR")
	}
	for y := range h {
		for x := range w {
			i := y*w + x
			if swizzled {
				i = interleave(x, y, w, h, false)
			}
			var c uint32
			if bpp == 2 {
				c = uint32(le.Uint16(data[i*2:]))
			} else {
				c = le.Uint32(data[i*4:])
			}
			img.SetNRGBA(x, y, col(c))
		}
	}
	return img, nil
}

// decodeDXT decodes S3TC blocks of 4×4 pixels: DXT1 (kind 0x0C), DXT3
// (0x0E) or DXT5 (0x0F).
func decodeDXT(img *image.NRGBA, data []byte, kind uint32) error {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	block := 16
	if kind == 0x0C {
		block = 8
	}
	bw, bh := (w+3)/4, (h+3)/4
	if len(data) < bw*bh*block {
		return errors.New("truncated XPR")
	}
	le := binary.LittleEndian
	for by := range bh {
		for bx := range bw {
			b := data[(by*bw+bx)*block:]
			var alpha [16]uint8
			for i := range alpha {
				alpha[i] = 255
			}
			switch kind {
			case 0x0E:
				for i := range 16 {
					alpha[i] = expand(uint32(b[i/2])>>(4*(i%2)), 4)
				}
				b = b[8:]
			case 0x0F:
				a0, a1 := uint32(b[0]), uint32(b[1])
				var pal [8]uint32
				pal[0], pal[1] = a0, a1
				if a0 > a1 {
					for i := 2; i < 8; i++ {
						pal[i] = ((8-uint32(i))*a0 + (uint32(i)-1)*a1) / 7
					}
				} else {
					for i := 2; i < 6; i++ {
						pal[i] = ((6-uint32(i))*a0 + (uint32(i)-1)*a1) / 5
					}
					pal[6], pal[7] = 0, 255
				}
				bits := uint64(b[2]) | uint64(b[3])<<8 | uint64(b[4])<<16 | uint64(b[5])<<24 | uint64(b[6])<<32 | uint64(b[7])<<40
				for i := range 16 {
					alpha[i] = uint8(pal[bits>>(3*i)&7])
				}
				b = b[8:]
			}
			c0, c1 := le.Uint16(b), le.Uint16(b[2:])
			var pal [4]color.NRGBA
			pal[0], pal[1] = rgb565(c0), rgb565(c1)
			mix := func(a, b color.NRGBA, wa, wb, d int) color.NRGBA {
				return color.NRGBA{uint8((int(a.R)*wa + int(b.R)*wb) / d), uint8((int(a.G)*wa + int(b.G)*wb) / d), uint8((int(a.B)*wa + int(b.B)*wb) / d), 255}
			}
			if c0 > c1 || kind != 0x0C {
				pal[2], pal[3] = mix(pal[0], pal[1], 2, 1, 3), mix(pal[0], pal[1], 1, 2, 3)
			} else {
				pal[2], pal[3] = mix(pal[0], pal[1], 1, 1, 2), color.NRGBA{}
			}
			bits := le.Uint32(b[4:])
			for i := range 16 {
				x, y := bx*4+i%4, by*4+i/4
				if x >= w || y >= h {
					continue
				}
				c := pal[bits>>(2*i)&3]
				if kind != 0x0C {
					c.A = alpha[i]
				}
				img.SetNRGBA(x, y, c)
			}
		}
	}
	return nil
}
