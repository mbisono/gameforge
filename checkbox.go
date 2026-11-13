package main

import (
	"image"
	"image/color"
	"math"
	"strconv"

	. "modernc.org/tk9.0"
)

// Check box images for the file list. Text glyphs (☐/☑) come from different
// fallback fonts and do not line up, so the boxes are drawn here, sized for
// the screen resolution.

var checkImgs struct {
	on, off, disabled *Img
}

func checkImage(state int) *Img {
	if checkImgs.on == nil {
		checkImgs.on = NewPhoto(Data(checkBoxImage(1)))
		checkImgs.off = NewPhoto(Data(checkBoxImage(0)))
		checkImgs.disabled = NewPhoto(Data(checkBoxImage(-1)))
	}
	switch state {
	case 1:
		return checkImgs.on
	case 0:
		return checkImgs.off
	}
	return checkImgs.disabled
}

// pixelsPerPoint returns Tk's screen scaling.
func pixelsPerPoint() float64 {
	scaling, _ := strconv.ParseFloat(tcl("tk", "scaling"), 64)
	return max(scaling, 1)
}

// checkBoxSize is the side of a check box in pixels, for the screen scaling.
func checkBoxSize() int { return max(13, int(math.Round(11*pixelsPerPoint()))) }

// checkBoxImage draws the box for a state: 1 checked, 0 unchecked, -1
// disabled.
func checkBoxImage(state int) image.Image {
	size := checkBoxSize()
	switch state {
	case 1:
		accent := color.RGBA{0x00, 0x67, 0xc0, 0xff}
		return drawCheckBox(size, accent, accent, true)
	case 0:
		if darkTheme {
			return drawCheckBox(size, color.RGBA{0x9a, 0x9a, 0x9a, 0xff}, color.RGBA{0x2b, 0x2b, 0x2b, 0xff}, false)
		}
		return drawCheckBox(size, color.RGBA{0x70, 0x70, 0x70, 0xff}, color.White, false)
	}
	if darkTheme {
		return drawCheckBox(size, color.RGBA{0x50, 0x50, 0x50, 0xff}, color.RGBA{0x20, 0x20, 0x20, 0xff}, false)
	}
	return drawCheckBox(size, color.RGBA{0xc0, 0xc0, 0xc0, 0xff}, color.RGBA{0xf0, 0xf0, 0xf0, 0xff}, false)
}

// resetImages drops the drawn images so that they are drawn again in the
// colors of a new theme.
func resetImages() {
	checkImgs.on, checkImgs.off, checkImgs.disabled = nil, nil, nil
	clear(gameImgs)
}

// drawCheckBox draws a size×size box with a 1px border and, if checked, a
// white check mark. A transparent column on the right keeps it off the text.
func drawCheckBox(size int, border, fill color.Color, checked bool) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size+4, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := fill
			if x == 0 || y == 0 || x == size-1 || y == size-1 {
				c = border
			}
			img.Set(x, y, c)
		}
	}
	if checked {
		// Two strokes: down from the left to the bottom point, then up to the right.
		s := float64(size)
		stroke := max(1.5, s/9)
		line(img, 0.22*s, 0.52*s, 0.42*s, 0.72*s, stroke)
		line(img, 0.42*s, 0.72*s, 0.78*s, 0.30*s, stroke)
	}
	return img
}

// line draws a white antialiased line of the given width.
func line(img *image.RGBA, x0, y0, x1, y1, width float64) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			// Distance from the pixel center to the segment.
			dx, dy := x1-x0, y1-y0
			t := max(0, min(1, ((px-x0)*dx+(py-y0)*dy)/(dx*dx+dy*dy)))
			d := math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
			a := max(0, min(1, width/2-d+0.5))
			if a == 0 {
				continue
			}
			r, g, bl, _ := img.At(x, y).RGBA()
			mix := func(v uint32) uint8 { return uint8(float64(v>>8)*(1-a) + 255*a) }
			img.Set(x, y, color.RGBA{mix(r), mix(g), mix(bl), 0xff})
		}
	}
}
