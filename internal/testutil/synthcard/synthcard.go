// Package synthcard renders synthetic trading-card scans (card on a scanner bed, optionally
// rotated) with known border widths and corner wear, for deterministic vision tests.
package synthcard

import (
	"image"
	"image/color"
	"math"

	"github.com/disintegration/imaging"
)

// Spec describes a synthetic scan.
type Spec struct {
	CanvasW, CanvasH int
	Background       color.NRGBA
	CardW, CardH     int     // card size in pixels
	AngleDeg         float64 // rotation around the canvas centre
	OffsetX, OffsetY float64 // card centre offset from canvas centre

	Stock                              color.NRGBA // border / card-stock colour
	Art                                color.NRGBA // inner artwork colour ("" alpha 0 = no artwork)
	BorderL, BorderR, BorderT, BorderB int         // print border widths (card pixels)
	TextBars                           bool        // dark glyph-like bars in the header/footer zones

	WornCorners []int // 0=TL 1=TR 2=BR 3=BL corners with white fray
}

// Front returns a standard 750x1050 raw card front, rotated 3°, with 40/30 L/R and 36/36 T/B borders.
func Front() Spec {
	return Spec{CanvasW: 1000, CanvasH: 1300, Background: color.NRGBA{18, 18, 22, 255},
		CardW: 750, CardH: 1050, AngleDeg: 3,
		Stock: color.NRGBA{246, 246, 242, 255}, Art: color.NRGBA{40, 90, 170, 255},
		BorderL: 40, BorderR: 30, BorderT: 36, BorderB: 36, TextBars: true}
}

// Back returns a dark card back with fray on the TL and BR corners.
func Back() Spec {
	s := Front()
	s.AngleDeg = -2
	s.Stock = color.NRGBA{28, 38, 92, 255}
	s.Art = color.NRGBA{}
	s.WornCorners = []int{0, 2}
	return s
}

// Slab returns a graded slab (approx. 3.25" x 5.3") front.
func Slab() Spec {
	return Spec{CanvasW: 1200, CanvasH: 1750, Background: color.NRGBA{18, 18, 22, 255},
		CardW: 900, CardH: 1467, AngleDeg: 1.5,
		Stock: color.NRGBA{225, 228, 232, 255}, Art: color.NRGBA{170, 40, 40, 255},
		BorderL: 60, BorderR: 60, BorderT: 330, BorderB: 60, TextBars: true}
}

// Render draws the scan.
func Render(s Spec) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, s.CanvasW, s.CanvasH))
	cx, cy := float64(s.CanvasW)/2+s.OffsetX, float64(s.CanvasH)/2+s.OffsetY
	rad := -s.AngleDeg * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	for y := 0; y < s.CanvasH; y++ {
		for x := 0; x < s.CanvasW; x++ {
			// Map canvas -> card-local coordinates (inverse rotation about the card centre).
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			lx := dx*cos - dy*sin + float64(s.CardW)/2
			ly := dx*sin + dy*cos + float64(s.CardH)/2
			c := s.Background
			if lx >= 0 && ly >= 0 && lx < float64(s.CardW) && ly < float64(s.CardH) {
				c = s.cardPixel(lx, ly)
			}
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
		}
	}
	return img
}

func (s Spec) cardPixel(x, y float64) color.NRGBA {
	w, h := float64(s.CardW), float64(s.CardH)
	// Corner / edge fray: whitened stock along the outer edges near worn corners.
	for _, c := range s.WornCorners {
		ox, oy := 0.0, 0.0
		if c == 1 || c == 2 {
			ox = w
		}
		if c == 2 || c == 3 {
			oy = h
		}
		ddx, ddy := math.Abs(x-ox), math.Abs(y-oy)
		if (ddx < 9 && ddy < 55) || (ddy < 9 && ddx < 55) || ddx+ddy < 16 {
			return color.NRGBA{245, 245, 245, 255}
		}
	}
	inArt := x >= float64(s.BorderL) && x < w-float64(s.BorderR) && y >= float64(s.BorderT) && y < h-float64(s.BorderB)
	if !inArt || s.Art.A == 0 {
		if s.TextBars && !inArt && y < float64(s.BorderT) && s.BorderT > 150 && glyph(x-80, y-60, w-160, float64(s.BorderT)-120) {
			return color.NRGBA{20, 20, 20, 255} // slab label text
		}
		return s.Stock
	}
	if s.TextBars {
		// Header bar band (inside the top 18%) and footer band (inside the bottom 22%).
		if glyph(x-0.12*w, y-0.06*h, 0.6*w, 0.06*h) || glyph(x-0.15*w, y-0.85*h, 0.7*w, 0.07*h) {
			return color.NRGBA{12, 12, 12, 255}
		}
	}
	// Gentle gradient so the artwork is not flat.
	f := y / h
	return color.NRGBA{uint8(float64(s.Art.R) * (0.8 + 0.2*f)), uint8(float64(s.Art.G) * (0.8 + 0.2*f)), s.Art.B, 255}
}

// glyph draws a row of letter-like blocks inside a (x,y,w,h) box (coords relative to box).
func glyph(x, y, w, h float64) bool {
	if x < 0 || y < 0 || x >= w || y >= h {
		return false
	}
	cell := h * 0.7
	col := math.Mod(x, cell)
	return col < cell*0.55 && (y < h*0.2 || y > h*0.8 || col < cell*0.15 || int(x/cell)%3 == 0)
}

// Save writes the image (format by extension, JPEG quality 95).
func Save(img image.Image, path string) error {
	return imaging.Save(img, path, imaging.JPEGQuality(95))
}
