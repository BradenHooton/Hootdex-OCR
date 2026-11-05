package vision

import (
	"errors"
	"image"
	"math"

	"github.com/disintegration/imaging"

	"github.com/hootyeah/hootdex/internal/domain"
)

const (
	detectMaxDim       = 800  // detection runs on a downscaled copy
	bgColorDistance    = 45.0 // RGB distance separating card from scanner bed
	edgeMagnitude      = 120.0
	minCardAreaFrac    = 0.15
	preCroppedAspectTo = 0.06 // ±6% of a known card/slab aspect => scanner may have auto-cropped
	preCroppedMargin   = 0.07 // ...and the detected region sits within 7% of every image edge
)

// knownAspects are width/height ratios of standard cards, Japanese-size cards and slabs.
var knownAspects = []float64{2.5 / 3.5, 2.32 / 3.38, 3.25 / 5.3}

// detectCardQuad finds the card's four corners (TL, TR, BR, BL) in source pixels.
//
// Pure-Go analogue of Gaussian blur -> Canny -> largest external contour -> approxPolyDP:
// pixels that differ from the scanner-bed colour or sit on a strong Sobel edge form a mask,
// which is closed, hole-filled, reduced to its largest 4-connected component, and the
// component's extreme points (min/max of x+y and x-y) give the quadrilateral corners.
func detectCardQuad(src *image.NRGBA) [4]domain.Point {
	b := src.Bounds()
	full := [4]domain.Point{{X: 0, Y: 0}, {X: float64(b.Dx() - 1), Y: 0}, {X: float64(b.Dx() - 1), Y: float64(b.Dy() - 1)}, {X: 0, Y: float64(b.Dy() - 1)}}

	scale := math.Min(1, float64(detectMaxDim)/math.Max(float64(b.Dx()), float64(b.Dy())))
	sw, sh := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	small := imaging.Blur(imaging.Resize(src, sw, sh, imaging.Linear), 1.2)

	bg := borderMedianColor(small)
	gray := lumaPlane(small)
	edges := sobelMagnitude(gray)
	mask := make([]bool, sw*sh)
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			i := y*small.Stride + x*4
			d := math.Sqrt(sq(float64(small.Pix[i])-bg[0]) + sq(float64(small.Pix[i+1])-bg[1]) + sq(float64(small.Pix[i+2])-bg[2]))
			mask[y*sw+x] = d > bgColorDistance || edges.pix[y*sw+x] > edgeMagnitude
		}
	}
	mask = erode(dilate(mask, sw, sh, 2), sw, sh, 2)
	fillHoles(mask, sw, sh)
	comp, area := largestComponent(mask, sw, sh)

	if area < int(minCardAreaFrac*float64(sw*sh)) {
		return full
	}
	if nearKnownAspect(float64(b.Dx())/float64(b.Dy())) && hugsEdges(comp, sw, sh) {
		// The RICOH auto-crop already isolated the card: the image border *is* the card's
		// print border and the detected "component" is just the inner artwork.
		return full
	}
	var q [4]domain.Point
	best := [4]float64{math.Inf(1), math.Inf(-1), math.Inf(-1), math.Inf(1)} // min(x+y), max(x-y), max(x+y), min(x-y)
	for i, in := range comp {
		if !in {
			continue
		}
		x, y := float64(i%sw), float64(i/sw)
		if s := x + y; s < best[0] {
			best[0], q[0] = s, domain.Point{X: x, Y: y}
		}
		if d := x - y; d > best[1] {
			best[1], q[1] = d, domain.Point{X: x, Y: y}
		}
		if s := x + y; s > best[2] {
			best[2], q[2] = s, domain.Point{X: x, Y: y}
		}
		if d := x - y; d < best[3] {
			best[3], q[3] = d, domain.Point{X: x, Y: y}
		}
	}
	for i := range q {
		q[i] = domain.Point{X: (q[i].X + 0.5) / scale, Y: (q[i].Y + 0.5) / scale}
	}
	return q
}

// hugsEdges reports whether the component's bounding box is within preCroppedMargin of all edges.
func hugsEdges(comp []bool, w, h int) bool {
	x0, y0, x1, y1 := w, h, -1, -1
	for i, in := range comp {
		if in {
			x, y := i%w, i/w
			x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
		}
	}
	mx, my := preCroppedMargin*float64(w), preCroppedMargin*float64(h)
	return float64(x0) < mx && float64(y0) < my && float64(w-1-x1) < mx && float64(h-1-y1) < my
}

func nearKnownAspect(a float64) bool {
	for _, k := range knownAspects {
		if math.Abs(a-k)/k <= preCroppedAspectTo || math.Abs(1/a-k)/k <= preCroppedAspectTo {
			return true
		}
	}
	return false
}

// quadSize returns the mean edge lengths (width, height) of a TL,TR,BR,BL quad.
func quadSize(q [4]domain.Point) (w, h float64) {
	d := func(a, b domain.Point) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }
	return (d(q[0], q[1]) + d(q[3], q[2])) / 2, (d(q[0], q[3]) + d(q[1], q[2])) / 2
}

// orientPortrait rotates a landscape quad's corner order so the warp outputs portrait.
func orientPortrait(q [4]domain.Point) [4]domain.Point {
	if w, h := quadSize(q); w > h {
		return [4]domain.Point{q[3], q[0], q[1], q[2]}
	}
	return q
}

// warpPerspective maps the quad onto a w x h canonical image using a homography.
func warpPerspective(src *image.NRGBA, q [4]domain.Point, w, h int) (*image.NRGBA, error) {
	dst := [4]domain.Point{{X: 0, Y: 0}, {X: float64(w - 1), Y: 0}, {X: float64(w - 1), Y: float64(h - 1)}, {X: 0, Y: float64(h - 1)}}
	H, err := homography(dst, q) // maps canonical -> source
	if err != nil {
		return nil, err
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x), float64(y)
			den := H[6]*fx + H[7]*fy + 1
			sx := (H[0]*fx + H[1]*fy + H[2]) / den
			sy := (H[3]*fx + H[4]*fy + H[5]) / den
			c := sampleBilinear(src, sx, sy)
			i := y*out.Stride + x*4
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = c.R, c.G, c.B, 255
		}
	}
	return out, nil
}

// homography solves the 8-parameter projective transform mapping from[i] -> to[i].
func homography(from, to [4]domain.Point) ([8]float64, error) {
	var a [8][9]float64
	for i := 0; i < 4; i++ {
		x, y, u, v := from[i].X, from[i].Y, to[i].X, to[i].Y
		a[2*i] = [9]float64{x, y, 1, 0, 0, 0, -x * u, -y * u, u}
		a[2*i+1] = [9]float64{0, 0, 0, x, y, 1, -x * v, -y * v, v}
	}
	for col := 0; col < 8; col++ { // Gaussian elimination with partial pivoting
		piv := col
		for r := col + 1; r < 8; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[piv][col]) {
				piv = r
			}
		}
		if math.Abs(a[piv][col]) < 1e-12 {
			return [8]float64{}, errors.New("degenerate quadrilateral")
		}
		a[col], a[piv] = a[piv], a[col]
		for r := 0; r < 8; r++ {
			if r == col {
				continue
			}
			f := a[r][col] / a[col][col]
			for c := col; c < 9; c++ {
				a[r][c] -= f * a[col][c]
			}
		}
	}
	var h [8]float64
	for i := 0; i < 8; i++ {
		h[i] = a[i][8] / a[i][i]
	}
	return h, nil
}

func borderMedianColor(img *image.NRGBA) [3]float64 {
	b := img.Bounds()
	var rs, gs, bs []float64
	add := func(x, y int) {
		i := y*img.Stride + x*4
		rs, gs, bs = append(rs, float64(img.Pix[i])), append(gs, float64(img.Pix[i+1])), append(bs, float64(img.Pix[i+2]))
	}
	const band = 3
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if x < band || y < band || x >= b.Dx()-band || y >= b.Dy()-band {
				add(x, y)
			}
		}
	}
	return [3]float64{median(rs), median(gs), median(bs)}
}

func sq(v float64) float64 { return v * v }

// dilate / erode use a (2r+1)^2 square structuring element via separable passes.
func dilate(m []bool, w, h, r int) []bool { return morph(m, w, h, r, true) }
func erode(m []bool, w, h, r int) []bool  { return morph(m, w, h, r, false) }

func morph(m []bool, w, h, r int, isDilate bool) []bool {
	pass := func(in []bool, horizontal bool) []bool {
		out := make([]bool, len(in))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := !isDilate
				for k := -r; k <= r; k++ {
					xx, yy := x, y
					if horizontal {
						xx = clampInt(x+k, 0, w-1)
					} else {
						yy = clampInt(y+k, 0, h-1)
					}
					if in[yy*w+xx] == isDilate {
						v = isDilate
						break
					}
				}
				out[y*w+x] = v
			}
		}
		return out
	}
	return pass(pass(m, true), false)
}

// fillHoles sets every false pixel not 4-connected to the image border to true.
func fillHoles(m []bool, w, h int) {
	outside := make([]bool, len(m))
	stack := make([]int, 0, 2*(w+h))
	push := func(x, y int) {
		i := y*w + x
		if !m[i] && !outside[i] {
			outside[i] = true
			stack = append(stack, i)
		}
	}
	for x := 0; x < w; x++ {
		push(x, 0)
		push(x, h-1)
	}
	for y := 0; y < h; y++ {
		push(0, y)
		push(w-1, y)
	}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		x, y := i%w, i/w
		if x > 0 {
			push(x-1, y)
		}
		if x < w-1 {
			push(x+1, y)
		}
		if y > 0 {
			push(x, y-1)
		}
		if y < h-1 {
			push(x, y+1)
		}
	}
	for i := range m {
		if !m[i] && !outside[i] {
			m[i] = true
		}
	}
}

// largestComponent returns the mask of the largest 4-connected true region and its area.
func largestComponent(m []bool, w, h int) ([]bool, int) {
	labels := make([]int32, len(m))
	var bestLabel int32
	bestArea := 0
	next := int32(0)
	stack := []int{}
	for start := range m {
		if !m[start] || labels[start] != 0 {
			continue
		}
		next++
		labels[start] = next
		stack = append(stack[:0], start)
		area := 0
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			area++
			x, y := i%w, i/w
			for _, n := range [4][2]int{{x - 1, y}, {x + 1, y}, {x, y - 1}, {x, y + 1}} {
				if n[0] < 0 || n[1] < 0 || n[0] >= w || n[1] >= h {
					continue
				}
				j := n[1]*w + n[0]
				if m[j] && labels[j] == 0 {
					labels[j] = next
					stack = append(stack, j)
				}
			}
		}
		if area > bestArea {
			bestArea, bestLabel = area, next
		}
	}
	out := make([]bool, len(m))
	for i, l := range labels {
		out[i] = l == bestLabel && l != 0
	}
	return out, bestArea
}
