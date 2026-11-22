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
	"slices"
	"time"

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

// iconWidth is how wide an icon is drawn in a list row, keeping its
// proportions; 0 if it is not a PNG.
func iconWidth(icon []byte, h int) int {
	c, err := png.DecodeConfig(bytes.NewReader(icon))
	if err != nil || c.Height == 0 {
		return 0
	}
	return max(1, h*c.Width/c.Height)
}

// discMark returns the image option of a disc row with an icon (a PSP
// game's ICON0.PNG, a disc label): the check box (state 1, 0 or -1 for
// disabled; -2 for none, leaving its space empty so that icons line up)
// and the icon, keeping its proportions, centered in width pixels so that
// the names line up. A row without an icon gets the empty space when the
// list has other icons (width > 0). key identifies the icon for the cache.
func discMark(icon []byte, state int, key string, width int) (Opt, bool) {
	if rowHeight == 0 || width == 0 {
		return nil, false
	}
	if len(icon) == 0 {
		key = "none"
	}
	key = fmt.Sprintf("disc/%s/%d/%d", key, state, width)
	if img := gameImgs[key]; img != nil {
		return Image(img), true
	}
	out, err := discImage(icon, state, rowHeight-4, width)
	if err != nil {
		out, _ = discImage(nil, state, rowHeight-4, width)
	}
	img := NewPhoto(Data(out))
	gameImgs[key] = img
	return Image(img), true
}

// discImage draws a disc row's image h pixels high with the icon (or
// nothing, for nil) in width pixels: see discMark.
func discImage(icon []byte, state, h, width int) (image.Image, error) {
	box := checkBoxImage(max(state, -1))
	bb := box.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, bb.Dx()+width+4, h))
	if state >= -1 {
		draw.Draw(out, bb.Add(image.Pt(0, (h-bb.Dy())/2)), box, bb.Min, draw.Src)
	}
	if icon == nil {
		return out, nil
	}
	src, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		return nil, err
	}
	if src.Bounds().Dy() == 0 {
		return nil, fmt.Errorf("empty icon")
	}
	w := min(width, max(1, h*src.Bounds().Dx()/src.Bounds().Dy()))
	x := bb.Dx() + (width-w)/2
	draw.Draw(out, image.Rect(x, 0, x+w, h), scaleTo(src, w, h), image.Point{}, draw.Over)
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

// loadIconLater queues an item's slow icon, read in the background one at
// a time; the lists are shown again as the icons come in.
func (u *ui) loadIconLater(it *convItem) {
	u.iconMu.Lock()
	u.iconJobs = append(u.iconJobs, it)
	run := !u.iconRunning
	u.iconRunning = true
	u.iconMu.Unlock()
	if run {
		go u.readIcons()
	}
}

func (u *ui) readIcons() {
	type found struct {
		it   *convItem
		icon []byte
	}
	var batch []found
	last := time.Now()
	show := func() {
		b := batch
		batch = nil
		last = time.Now()
		onGUI(func() {
			// Only lists that still hold an item are shown again.
			tabs := map[*convTab]bool{}
			for _, f := range b {
				f.it.icon = f.icon
				if slices.Contains(f.it.tab.items, f.it) {
					tabs[f.it.tab] = true
				}
			}
			for t := range tabs {
				t.refresh(u)
			}
		})
	}
	for {
		u.iconMu.Lock()
		if len(u.iconJobs) == 0 {
			u.iconRunning = false
			u.iconMu.Unlock()
			if len(batch) > 0 {
				show()
			}
			return
		}
		it := u.iconJobs[0]
		u.iconJobs = u.iconJobs[1:]
		u.iconMu.Unlock()
		if icon := it.iconLater(); icon != nil {
			batch = append(batch, found{it, icon})
		}
		if len(batch) > 0 && time.Since(last) > 500*time.Millisecond {
			show()
		}
	}
}
