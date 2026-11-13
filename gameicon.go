package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"

	. "modernc.org/tk9.0"

	"gameforge/internal/merge"
)

// Game rows show the check box and the game's icon side by side. A list row
// has a single image, so both are drawn into one.

// rowHeight is the list's row height in pixels, set by setRowHeight.
var rowHeight int

// gameImgs caches the row images by icon and check state.
var gameImgs = map[string]*Img{}

// setRowHeight makes the list rows tall enough for a readable icon.
func setRowHeight() {
	var line int
	fmt.Sscan(tcl("font", "metrics", "TkDefaultFont", "-linespace"), &line)
	rowHeight = max(line, 16) + int(math.Round(6*pixelsPerPoint()))
	tcl("ttk::style", "configure", "Treeview", "-rowheight", fmt.Sprint(rowHeight))
}

// gameMark returns the image option of a game row: the check box plus the
// game's icon when it has one.
func gameMark(g *merge.Game, checked bool) Opt {
	c := g.Control()
	if c == nil || c.Icon == nil || rowHeight == 0 {
		return checkMark(checked)
	}
	key := fmt.Sprintf("%p/%v", c, checked)
	if img := gameImgs[key]; img != nil {
		return Image(img)
	}
	icon, err := jpeg.Decode(bytes.NewReader(c.Icon))
	if err != nil {
		return checkMark(checked)
	}
	state := 0
	if checked {
		state = 1
	}
	box := checkBoxImage(state) // includes a transparent gap on the right
	size := rowHeight - 4
	bb := box.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, bb.Dx()+size+4, size))
	draw.Draw(out, bb.Add(image.Pt(0, (size-bb.Dy())/2)), box, bb.Min, draw.Src)
	draw.Draw(out, image.Rect(bb.Dx(), 0, bb.Dx()+size, size), scale(icon, size), image.Point{}, draw.Src)
	img := NewPhoto(Data(out))
	gameImgs[key] = img
	return Image(img)
}

// scale shrinks src to size×size by averaging the source pixels under each
// destination pixel.
func scale(src image.Image, size int) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	fx, fy := float64(b.Dx())/float64(size), float64(b.Dy())/float64(size)
	for y := range size {
		y0, y1 := b.Min.Y+int(float64(y)*fy), b.Min.Y+max(int(float64(y+1)*fy), int(float64(y)*fy)+1)
		for x := range size {
			x0, x1 := b.Min.X+int(float64(x)*fx), b.Min.X+max(int(float64(x+1)*fx), int(float64(x)*fx)+1)
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
