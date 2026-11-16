package gameart

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
	"testing/iotest"
)

// decode returns a function that decodes a PNG returned with an error.
func decode(t *testing.T) func([]byte, error) image.Image {
	return func(b []byte, err error) image.Image {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
}

func nrgba(c color.Color) color.NRGBA { return color.NRGBAModel.Convert(c).(color.NRGBA) }

func TestNDS(t *testing.T) {
	const at = 0x1000
	rom := make([]byte, at+ndsIconLen+100)
	binary.LittleEndian.PutUint32(rom[ndsBannerAt:], at)
	binary.LittleEndian.PutUint16(rom[at:], 1)
	b := rom[at:]
	binary.LittleEndian.PutUint16(b[0x220+2:], 0x001F) // color 1: red
	binary.LittleEndian.PutUint16(b[0x220+4:], 0x7C00) // color 2: blue
	// Pixel (9, 0) is in the second tile, first row, second pixel: the
	// high nibble of the tile's first byte. Pixel (0, 8) starts tile 4.
	b[0x20+32] = 0x10
	b[0x20+4*32] = 0x02
	// One byte at a time, as from a slow stream.
	img := decode(t)(NDS(iotest.OneByteReader(bytes.NewReader(rom))))
	if c := nrgba(img.At(9, 0)); c != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("(9,0) = %v", c)
	}
	if c := nrgba(img.At(0, 8)); c != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("(0,8) = %v", c)
	}
	if c := nrgba(img.At(8, 0)); c.A != 0 {
		t.Errorf("color 0 is not transparent: %v", c)
	}
	if _, err := NDS(bytes.NewReader(rom[:at+10])); err == nil {
		t.Error("a ROM cut before its banner gave an icon")
	}
}

func pvrHeader(pix, format byte, w, h int, data []byte) []byte {
	b := append([]byte("GBIX\x08\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00PVRT"), make([]byte, 12)...)
	p := b[16:]
	binary.LittleEndian.PutUint32(p[4:], uint32(8+len(data)))
	p[8], p[9] = pix, format
	binary.LittleEndian.PutUint16(p[12:], uint16(w))
	binary.LittleEndian.PutUint16(p[14:], uint16(h))
	return append(b, data...)
}

func TestPVR(t *testing.T) {
	// 4×4 twiddled RGB565: pixel (1, 0) is texel 2, pixel (0, 1) texel 1.
	data := make([]byte, 32)
	binary.LittleEndian.PutUint16(data[2*2:], 0xF800) // red
	binary.LittleEndian.PutUint16(data[1*2:], 0x001F) // blue
	img := decode(t)(PVR(pvrHeader(1, pvrTwiddled, 4, 4, data)))
	if c := nrgba(img.At(1, 0)); c != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("twiddled (1,0) = %v", c)
	}
	if c := nrgba(img.At(0, 1)); c != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("twiddled (0,1) = %v", c)
	}

	// The same with mipmaps: 6 bytes of padding, 1×1 and 2×2 come first.
	mip := append(make([]byte, 6+2+8), data...)
	img = decode(t)(PVR(pvrHeader(1, pvrTwiddledMipmaps, 4, 4, mip)))
	if c := nrgba(img.At(1, 0)); c != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("mipmaps (1,0) = %v", c)
	}

	// 4×4 VQ ARGB1555: entry 1 is white, transparent, red, blue; the
	// second 2×2 block (twiddled index 2, at x 2..3) uses it.
	book := make([]byte, 2048)
	for k, c := range []uint16{0xFFFF, 0x7FFF, 0xFC00, 0x801F} {
		binary.LittleEndian.PutUint16(book[(4+k)*2:], c)
	}
	vq := append(book, 0, 0, 1, 0)
	img = decode(t)(PVR(pvrHeader(0, pvrVQ, 4, 4, vq)))
	for _, c := range []struct {
		x, y int
		want color.NRGBA
	}{{2, 0, color.NRGBA{255, 255, 255, 255}}, {2, 1, color.NRGBA{}}, {3, 0, color.NRGBA{255, 0, 0, 255}}, {3, 1, color.NRGBA{0, 0, 255, 255}}} {
		got := nrgba(img.At(c.x, c.y))
		if c.want.A == 0 && got.A == 0 {
			continue
		}
		if got != c.want {
			t.Errorf("VQ (%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}

	// Large labels are shrunk.
	img = decode(t)(PVR(pvrHeader(1, pvrRectangle, 256, 256, make([]byte, 256*256*2))))
	if img.Bounds().Dx() != maxSide {
		t.Errorf("256×256 label became %v", img.Bounds())
	}
	if _, err := PVR(pvrHeader(5, pvrTwiddled, 4, 4, data)); err == nil {
		t.Error("palette texture decoded")
	}
}

// xbe builds a default.xbe whose $$XTIMAGE section holds tex.
func xbe(tex []byte) []byte {
	const base = 0x10000
	b := make([]byte, 0x1000)
	copy(b, "XBEH")
	le := binary.LittleEndian
	le.PutUint32(b[0x104:], base)
	le.PutUint32(b[0x11C:], 2)
	le.PutUint32(b[0x120:], base+0x200)
	for i, name := range []string{".text", "$$XTIMAGE"} {
		copy(b[0x300+i*16:], name)
		s := b[0x200+i*56:]
		le.PutUint32(s[20:], uint32(base+0x300+i*16))
		le.PutUint32(s[12:], 0x1000)
		le.PutUint32(s[16:], uint32(len(tex)))
	}
	return append(b, tex...)
}

func xpr(kind uint32, w, h int, data []byte) []byte {
	b := make([]byte, 64)
	copy(b, "XPR0")
	le := binary.LittleEndian
	le.PutUint32(b[4:], uint32(64+len(data)))
	le.PutUint32(b[8:], 64)
	lw, lh := 0, 0
	for 1<<lw < w {
		lw++
	}
	for 1<<lh < h {
		lh++
	}
	le.PutUint32(b[24:], kind<<8|uint32(lw)<<20|uint32(lh)<<24)
	return append(b, data...)
}

func TestXBE(t *testing.T) {
	// DXT1 4×4: color 0 red, color 1 blue, first row 0,1,2,3.
	block := []byte{0x00, 0xF8, 0x1F, 0x00, 0b11100100, 0, 0, 0}
	f := xbe(xpr(0x0C, 4, 4, block))
	img := decode(t)(XBE(bytes.NewReader(f), int64(len(f))))
	want := []color.NRGBA{{255, 0, 0, 255}, {0, 0, 255, 255}, {170, 0, 85, 255}, {85, 0, 170, 255}}
	for x, w := range want {
		if c := nrgba(img.At(x, 0)); c != w {
			t.Errorf("DXT1 (%d,0) = %v, want %v", x, c, w)
		}
	}

	// Swizzled A8R8G8B8 4×2: pixel (1, 0) is texel 1, (0, 1) texel 2.
	data := make([]byte, 4*8)
	binary.LittleEndian.PutUint32(data[4:], 0xFFFF0000)
	binary.LittleEndian.PutUint32(data[8:], 0x800000FF)
	f = xbe(xpr(0x06, 4, 2, data))
	img = decode(t)(XBE(bytes.NewReader(f), int64(len(f))))
	if c := nrgba(img.At(1, 0)); c != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("swizzled (1,0) = %v", c)
	}
	if c := nrgba(img.At(0, 1)); c != (color.NRGBA{0, 0, 255, 128}) {
		t.Errorf("swizzled (0,1) = %v", c)
	}
	if _, err := XBE(bytes.NewReader([]byte("nope")), 4); err == nil {
		t.Error("not an XBE")
	}
}
