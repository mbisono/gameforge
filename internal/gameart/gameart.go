// Package gameart reads the pictures that games carry inside their files
// (the Nintendo DS banner icon, the Dreamcast disc label 0GDTEX.PVR and
// the Xbox title image in default.xbe) and returns them as PNG.
package gameart

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
)

// maxSide is the largest side of a returned image: the lists show the
// pictures at row height, so larger ones are shrunk to keep them light.
const maxSide = 128

// encode shrinks img to at most maxSide on its longer side and returns it
// as PNG.
func encode(img image.Image) ([]byte, error) {
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return nil, errors.New("empty image")
	}
	if s := max(b.Dx(), b.Dy()); s > maxSide {
		img = shrink(img, max(1, b.Dx()*maxSide/s), max(1, b.Dy()*maxSide/s))
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// shrink scales src down to w×h by averaging the pixels under each
// destination pixel (with premultiplied alpha, so transparent pixels add
// no color).
func shrink(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+max((y+1)*b.Dy()/h, y*b.Dy()/h+1)
		for x := range w {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+max((x+1)*b.Dx()/w, x*b.Dx()/w+1)
			var r, g, bl, a, n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					r, g, bl, a, n = r+cr, g+cg, bl+cb, a+ca, n+1
				}
			}
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return dst
}

// interleave returns the position of pixel (x, y) in a texture of w×h
// pixels stored in Morton order, as the Dreamcast (twiddled, yFirst) and
// the Xbox (swizzled) store them: the bits of x and y alternate while
// both sides still have bits, then the longer side's bits follow.
func interleave(x, y, w, h int, yFirst bool) int {
	idx, bit := 0, 0
	for i := 0; 1<<i < w || 1<<i < h; i++ {
		a, an, b, bn := x, w, y, h
		if yFirst {
			a, an, b, bn = y, h, x, w
		}
		if 1<<i < an {
			idx |= (a >> i & 1) << bit
			bit++
		}
		if 1<<i < bn {
			idx |= (b >> i & 1) << bit
			bit++
		}
	}
	return idx
}

// expand turns a field of n bits into 8 bits.
func expand(v uint32, n uint) uint8 {
	v &= 1<<n - 1
	return uint8(v * 255 / (1<<n - 1))
}

func argb1555(c uint16) color.NRGBA {
	a := uint8(0)
	if c&0x8000 != 0 {
		a = 255
	}
	return color.NRGBA{expand(uint32(c)>>10, 5), expand(uint32(c)>>5, 5), expand(uint32(c), 5), a}
}

func rgb565(c uint16) color.NRGBA {
	return color.NRGBA{expand(uint32(c)>>11, 5), expand(uint32(c)>>5, 6), expand(uint32(c), 5), 255}
}

func argb4444(c uint16) color.NRGBA {
	return color.NRGBA{expand(uint32(c)>>8, 4), expand(uint32(c)>>4, 4), expand(uint32(c), 4), expand(uint32(c)>>12, 4)}
}
