// Package icon draws memors-tray's icons (a brain) in
// code, so no image files are committed: the menu-bar template image and
// the app icon the bundle script turns into an .icns.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Template is the menu-bar icon: black on transparent, 64 px, which macOS
// draws at 16 pt and recolours for light and dark menu bars.
func Template() []byte {
	const n = 64
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			a := brain(float64(x)+0.5, float64(y)+0.5, n, 0.08)
			img.SetNRGBA(x, y, color.NRGBA{A: uint8(math.Round(a * 255))})
		}
	}
	return encode(img)
}

// App is the application icon at size px: a white brain on a rounded
// forest-green square, inset the way macOS app icons are.
func App(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	bg := color.NRGBA{R: 0x1f, G: 0x5c, B: 0x4f}
	inset, radius := s*0.09, s*0.2
	glyph := s * 0.56
	off := (s - glyph) / 2
	for y := range size {
		for x := range size {
			px, py := float64(x)+0.5, float64(y)+0.5
			box := coverage(roundedBox(px, py, inset, s-inset, radius))
			if box == 0 {
				continue
			}
			mark := brain(px-off, py-off, glyph, 0)
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(lerp(float64(bg.R), 255, mark)),
				G: uint8(lerp(float64(bg.G), 255, mark)),
				B: uint8(lerp(float64(bg.B), 255, mark)),
				A: uint8(math.Round(box * 255)),
			})
		}
	}
	return encode(img)
}

// brain is the coverage (0..1) of a brain glyph filling an n x n box at
// point (x, y): two hemispheres and a lower lobe with a stem, carved by a
// centre fissure and a few folds. pad shrinks the glyph inside its box.
func brain(x, y, n, pad float64) float64 {
	p := n * pad
	m := n - 2*p
	x, y = (x-p)/m, (y-p)/m // unit box
	disc := func(cx, cy, r float64) float64 { return (math.Hypot(x-cx, y-cy) - r) * m }
	// Lumpy outline: a cluster of discs per hemisphere, mirrored about x = 0.5.
	lobes := [][3]float64{{0.33, 0.40, 0.25}, {0.24, 0.24, 0.15}, {0.40, 0.16, 0.13}, {0.14, 0.42, 0.13}, {0.22, 0.62, 0.14}, {0.40, 0.64, 0.15}}
	body := math.Inf(1)
	for _, l := range lobes {
		body = math.Min(body, disc(l[0], l[1], l[2]))
		body = math.Min(body, disc(1-l[0], l[1], l[2]))
	}
	body = math.Min(body, segment(x, y, 0.5, 0.66, 0.56, 0.92)*m-0.05*m)
	cut := func(ax, ay, bx, by float64) float64 { return segment(x, y, ax, ay, bx, by)*m - 0.02*m }
	carve := cut(0.5, 0.08, 0.5, 0.68)
	for _, side := range []float64{0, 1} {
		fx := func(v float64) float64 { return math.Abs(side - v) }
		carve = math.Min(carve, cut(fx(0.18), 0.30, fx(0.30), 0.38))
		carve = math.Min(carve, cut(fx(0.30), 0.38, fx(0.40), 0.30))
		carve = math.Min(carve, cut(fx(0.16), 0.54, fx(0.30), 0.50))
		carve = math.Min(carve, cut(fx(0.30), 0.50, fx(0.36), 0.58))
	}
	return coverage(math.Max(body, -carve))
}

// segment is the distance from (x, y) to the segment a-b.
func segment(x, y, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := ((x-ax)*dx + (y-ay)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(x-(ax+t*dx), y-(ay+t*dy))
}

// roundedBox is the signed distance to a rounded square spanning lo..hi.
func roundedBox(x, y, lo, hi, r float64) float64 {
	c := (lo + hi) / 2
	h := (hi-lo)/2 - r
	qx, qy := math.Abs(x-c)-h, math.Abs(y-c)-h
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

// coverage turns a signed distance (negative inside) into pixel coverage.
func coverage(d float64) float64 { return math.Max(0, math.Min(1, 0.5-d)) }

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
