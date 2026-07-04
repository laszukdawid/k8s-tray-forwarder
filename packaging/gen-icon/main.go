// Command gen-icon renders the placeholder application icon (packaging/icon.png).
//
// It draws a rounded-square badge in Kubernetes blue with two opposing arrows —
// a "port-forward" motif — using only the standard library. Everything is drawn
// on a supersampled canvas and box-downscaled so the edges are anti-aliased
// without pulling in an image/vector dependency.
//
// This is a stand-in: drop a real 1024x1024 packaging/icon.png over it whenever
// a designed icon exists. Regenerate with:
//
//	go run ./packaging/gen-icon -o packaging/icon.png
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	size  = 1024 // final icon edge, in pixels
	scale = 4    // supersampling factor for anti-aliasing
)

func main() {
	out := flag.String("o", "icon.png", "output PNG path")
	flag.Parse()

	big := render(size * scale)
	small := downscale(big, size)

	f, err := os.Create(*out)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, small); err != nil {
		fatal(err)
	}
}

// render draws the icon at the given (supersampled) edge length.
func render(n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, n, n))

	// Rounded-square badge with a top-to-bottom Kubernetes-blue gradient. A
	// margin keeps the badge off the very edge so it reads well once macOS
	// composites it against the Dock/Finder background.
	margin := float64(n) * 0.06
	radius := float64(n) * 0.20
	top := color.RGBA{0x3B, 0x7A, 0xF0, 0xFF}    // lighter blue
	bottom := color.RGBA{0x27, 0x54, 0xC4, 0xFF} // deeper blue

	for y := 0; y < n; y++ {
		t := float64(y) / float64(n-1)
		row := lerp(top, bottom, t)
		for x := 0; x < n; x++ {
			if insideRoundRect(float64(x), float64(y), margin, margin, float64(n)-margin, float64(n)-margin, radius) {
				img.SetRGBA(x, y, row)
			}
		}
	}

	// Two opposing arrows (top points right, bottom points left) — the classic
	// bidirectional "forwarding" glyph, in white.
	white := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	drawArrow(img, n, 0.36, true, white)  // upper arrow, pointing right
	drawArrow(img, n, 0.64, false, white) // lower arrow, pointing left

	return img
}

// drawArrow draws a horizontal arrow centered vertically at cy*n. When right is
// true the head points right; otherwise it points left.
func drawArrow(img *image.RGBA, n int, cy float64, right bool, c color.RGBA) {
	fn := float64(n)
	yc := cy * fn
	thick := fn * 0.055 // shaft half-height
	headH := fn * 0.11  // head half-height

	// Horizontal extent of the whole arrow.
	x0 := fn * 0.26
	x1 := fn * 0.74
	headLen := fn * 0.16

	var shaftStart, shaftEnd, tip, base float64
	if right {
		shaftStart, shaftEnd = x0, x1-headLen
		tip, base = x1, x1-headLen
	} else {
		shaftStart, shaftEnd = x0+headLen, x1
		tip, base = x0, x0+headLen
	}

	for y := 0; y < n; y++ {
		fy := float64(y)
		for x := 0; x < n; x++ {
			fx := float64(x)
			// Shaft: a horizontal bar.
			inShaft := fx >= shaftStart && fx <= shaftEnd && math.Abs(fy-yc) <= thick
			// Head: a triangle that tapers linearly from full height at the base
			// to a point at the tip. frac runs 0 at the tip -> 1 at the base.
			inHead := false
			if (right && fx >= base && fx <= tip) || (!right && fx >= tip && fx <= base) {
				frac := (fx - tip) / (base - tip) // (base-tip) is signed; ratio is >=0
				if math.Abs(fy-yc) <= headH*frac {
					inHead = true
				}
			}
			if inShaft || inHead {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// insideRoundRect reports whether (px,py) lies within the rounded rectangle with
// the given corners and corner radius.
func insideRoundRect(px, py, x0, y0, x1, y1, r float64) bool {
	if px < x0 || px > x1 || py < y0 || py > y1 {
		return false
	}
	// Clamp the point into the inner rectangle; distance to that clamp is the
	// distance to the rounded edge.
	cx := math.Max(x0+r, math.Min(px, x1-r))
	cy := math.Max(y0+r, math.Min(py, y1-r))
	dx := px - cx
	dy := py - cy
	return dx*dx+dy*dy <= r*r
}

// lerp linearly interpolates between two opaque colors.
func lerp(a, b color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 0xFF,
	}
}

// downscale box-filters src down to an nxn image, giving cheap anti-aliasing.
func downscale(src *image.RGBA, n int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	f := src.Bounds().Dx() / n
	area := float64(f * f)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var r, g, b, a float64
			for sy := 0; sy < f; sy++ {
				for sx := 0; sx < f; sx++ {
					px := src.RGBAAt(x*f+sx, y*f+sy)
					r += float64(px.R)
					g += float64(px.G)
					b += float64(px.B)
					a += float64(px.A)
				}
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r / area),
				G: uint8(g / area),
				B: uint8(b / area),
				A: uint8(a / area),
			})
		}
	}
	return dst
}

func fatal(err error) {
	os.Stderr.WriteString("gen-icon: " + err.Error() + "\n")
	os.Exit(1)
}
