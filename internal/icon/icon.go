// Package icon draws the application icon: three small tiles (base, update,
// DLC) whose lines join into one bigger tile. It is drawn at each size instead
// of being scaled, so the small sizes stay sharp.
package icon

import (
	"image"
	"image/color"
	"math"
)

// Render draws the icon at size×size pixels.
func Render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // samples per axis for antialiasing
	s := float64(size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := (float64(x) + (float64(sx)+0.5)/ss) / s
					py := (float64(y) + (float64(sy)+0.5)/ss) / s
					cr, cg, cb, ca := sample(px, py, s)
					r, g, b, a = r+cr*ca, g+cg*ca, b+cb*ca, a+ca
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{
				uint8(math.Round(r / a * 255)), uint8(math.Round(g / a * 255)),
				uint8(math.Round(b / a * 255)), uint8(math.Round(a / ss / ss * 255)),
			})
		}
	}
	return img
}

// sample returns the color (0..1) and coverage at a point of the unit square.
// s is the size in pixels; lines are kept at least about one pixel wide.
func sample(x, y, s float64) (r, g, b, a float64) {
	if roundRect(x, y, 0.04, 0.04, 0.96, 0.96, 0.2) > 0 {
		return 0, 0, 0, 0
	}
	// Background: vertical gradient in the accent blue of the check boxes.
	r, g, b = mix(0x1e, 0x00, y)/255, mix(0x88, 0x55, y)/255, mix(0xe0, 0xa8, y)/255

	half := math.Max(0.026, 0.55/s) // half the line width
	white := false
	for _, cy := range []float64{0.28, 0.5, 0.72} {
		if roundRect(x, y, 0.13, cy-0.075, 0.33, cy+0.075, 0.035) <= 0 {
			white = true
		}
		// Line from the tile to the joining point.
		if segment(x, y, 0.33, cy, 0.53, 0.5) <= half {
			white = true
		}
	}
	if segment(x, y, 0.53, 0.5, 0.6, 0.5) <= half {
		white = true
	}
	if roundRect(x, y, 0.6, 0.27, 0.87, 0.73, 0.05) <= 0 {
		white = true
	}
	if white {
		r, g, b = 1, 1, 1
	}
	return r, g, b, 1
}

func mix(a, b, t float64) float64 { return a + (b-a)*t }

// roundRect is the signed distance to a rounded rectangle.
func roundRect(x, y, x0, y0, x1, y1, rad float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-rad, (y1-y0)/2-rad
	dx, dy := math.Abs(x-cx)-hx, math.Abs(y-cy)-hy
	out := math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
	return out + math.Min(math.Max(dx, dy), 0) - rad
}

// segment is the distance to the segment (x0,y0)-(x1,y1).
func segment(x, y, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	t := math.Max(0, math.Min(1, ((x-x0)*dx+(y-y0)*dy)/(dx*dx+dy*dy)))
	return math.Hypot(x-(x0+t*dx), y-(y0+t*dy))
}
