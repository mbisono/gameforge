package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
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

// discMark returns the image option of a disc row with an icon (a PSP
// game's ICON0.PNG): the check box (state 1, 0 or -1 for disabled; -2
// for none, leaving its space empty so that icons line up) and the icon,
// keeping its proportions. key identifies the icon for the cache.
func discMark(icon []byte, state int, key string) (Opt, bool) {
	if len(icon) == 0 || rowHeight == 0 {
		return nil, false
	}
	key = fmt.Sprintf("disc/%s/%d", key, state)
	if img := gameImgs[key]; img != nil {
		return Image(img), true
	}
	out, err := discImage(icon, state, rowHeight-4)
	if err != nil {
		return nil, false
	}
	img := NewPhoto(Data(out))
	gameImgs[key] = img
	return Image(img), true
}

// discImage draws a disc row's image h pixels high: see discMark.
func discImage(icon []byte, state, h int) (image.Image, error) {
	src, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		return nil, err
	}
	if src.Bounds().Dy() == 0 {
		return nil, fmt.Errorf("empty icon")
	}
	box := checkBoxImage(max(state, -1))
	w := max(1, h*src.Bounds().Dx()/src.Bounds().Dy())
	bb := box.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, bb.Dx()+w+4, h))
	if state >= -1 {
		draw.Draw(out, bb.Add(image.Pt(0, (h-bb.Dy())/2)), box, bb.Min, draw.Src)
	}
	draw.Draw(out, image.Rect(bb.Dx(), 0, bb.Dx()+w, h), scaleTo(src, w, h), image.Point{}, draw.Over)
	return out, nil
}

// scale shrinks src to size×size by averaging the source pixels under each
// destination pixel.
func scale(src image.Image, size int) image.Image { return scaleTo(src, size, size) }

// scaleTo shrinks src to w×h by averaging the source pixels under each
// destination pixel.
func scaleTo(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	fx, fy := float64(b.Dx())/float64(w), float64(b.Dy())/float64(h)
	for y := range h {
		y0, y1 := b.Min.Y+int(float64(y)*fy), b.Min.Y+max(int(float64(y+1)*fy), int(float64(y)*fy)+1)
		for x := range w {
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
