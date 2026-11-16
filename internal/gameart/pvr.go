package gameart

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
)

// PVR data formats (how the pixels are arranged) of the Dreamcast.
const (
	pvrTwiddled        = 0x01
	pvrTwiddledMipmaps = 0x02
	pvrVQ              = 0x03
	pvrVQMipmaps       = 0x04
	pvrRectangle       = 0x09
	pvrStride          = 0x0B
	pvrTwiddledRect    = 0x0D
	pvrSmallVQ         = 0x10
	pvrSmallVQMipmaps  = 0x11
)

// PVR decodes a Dreamcast PVR texture, such as a disc's label 0GDTEX.PVR,
// and returns it as PNG. It reads the 16-bit color formats (ARGB1555,
// RGB565, ARGB4444), twiddled, VQ-compressed or plain, with or without
// mipmaps.
func PVR(b []byte) ([]byte, error) {
	img, err := decodePVR(b)
	if err != nil {
		return nil, err
	}
	return encode(img)
}

func decodePVR(b []byte) (image.Image, error) {
	if bytes.HasPrefix(b, []byte("GBIX")) && len(b) >= 8 {
		skip := 8 + int(binary.LittleEndian.Uint32(b[4:]))
		if skip > len(b) {
			return nil, errors.New("truncated PVR")
		}
		b = b[skip:]
	}
	if len(b) < 16 || string(b[:4]) != "PVRT" {
		return nil, errors.New("not a PVR texture")
	}
	pix, format := b[8], b[9]
	w, h := int(binary.LittleEndian.Uint16(b[12:])), int(binary.LittleEndian.Uint16(b[14:]))
	data := b[16:]
	if w == 0 || h == 0 || w > 1024 || h > 1024 {
		return nil, fmt.Errorf("PVR size %d×%d", w, h)
	}
	var col func(uint16) color.NRGBA
	switch pix {
	case 0:
		col = argb1555
	case 1:
		col = rgb565
	case 2:
		col = argb4444
	default:
		return nil, fmt.Errorf("PVR color format %d is not read", pix)
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	at := func(i int) (uint16, bool) {
		if 2*i+2 > len(data) {
			return 0, false
		}
		return binary.LittleEndian.Uint16(data[2*i:]), true
	}
	// levels is the number of mipmap levels smaller than the texture.
	levels := 0
	for 1<<levels < w {
		levels++
	}
	switch format {
	case pvrTwiddled, pvrTwiddledMipmaps, pvrTwiddledRect:
		if format == pvrTwiddledMipmaps {
			if w != h {
				return nil, errors.New("PVR mipmaps of a non-square texture")
			}
			// The smaller levels come first: 1×1 at byte 6, then each
			// level after the previous one.
			off := 6 + 2*((1<<(2*levels))-1)/3
			if off > len(data) {
				return nil, errors.New("truncated PVR")
			}
			data = data[off:]
		}
		// A rectangle is a row of squares of its shorter side.
		side := min(w, h)
		for y := range h {
			for x := range w {
				sq := x/side + y/side
				i := sq*side*side + interleave(x%side, y%side, side, side, true)
				c, ok := at(i)
				if !ok {
					return nil, errors.New("truncated PVR")
				}
				img.SetNRGBA(x, y, col(c))
			}
		}
	case pvrRectangle, pvrStride:
		for y := range h {
			for x := range w {
				c, ok := at(y*w + x)
				if !ok {
					return nil, errors.New("truncated PVR")
				}
				img.SetNRGBA(x, y, col(c))
			}
		}
	case pvrVQ, pvrVQMipmaps, pvrSmallVQ, pvrSmallVQMipmaps:
		if w != h {
			return nil, errors.New("VQ PVR of a non-square texture")
		}
		mipmaps := format == pvrVQMipmaps || format == pvrSmallVQMipmaps
		entries := 256
		if format == pvrSmallVQ || format == pvrSmallVQMipmaps {
			entries = smallVQEntries(w, mipmaps)
		}
		book := entries * 4 * 2
		if book > len(data) {
			return nil, errors.New("truncated PVR")
		}
		codes, index := data[:book], data[book:]
		if mipmaps {
			// One index byte stands for 2×2 pixels: the 1×1 level takes a
			// byte, then each level after the previous one.
			off := 1
			if levels > 1 {
				off += ((1 << (2 * (levels - 1))) - 1) / 3
			}
			if off > len(index) {
				return nil, errors.New("truncated PVR")
			}
			index = index[off:]
		}
		half := w / 2
		for y := range h {
			for x := range w {
				i := interleave(x/2, y/2, half, half, true)
				if i >= len(index) {
					return nil, errors.New("truncated PVR")
				}
				e := int(index[i])
				if e >= entries {
					return nil, errors.New("invalid VQ index")
				}
				k := (x&1)*2 + y&1
				img.SetNRGBA(x, y, col(binary.LittleEndian.Uint16(codes[(e*4+k)*2:])))
			}
		}
	default:
		return nil, fmt.Errorf("PVR data format %#x is not read", format)
	}
	return img, nil
}

// smallVQEntries is the size of the code book of a small VQ texture,
// which depends on the texture's size.
func smallVQEntries(w int, mipmaps bool) int {
	switch {
	case w <= 16:
		return 16
	case w == 32 && mipmaps:
		return 64
	case w == 32:
		return 32
	case w == 64 && !mipmaps:
		return 128
	}
	return 256
}
