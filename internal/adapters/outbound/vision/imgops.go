package vision

import (
	"image"
	"image/color"
	"math"
	"sort"
)

// plane is a single-channel float32 image (luminance 0..255).
type plane struct {
	w, h int
	pix  []float32
}

func newPlane(w, h int) *plane { return &plane{w: w, h: h, pix: make([]float32, w*h)} }

func (p *plane) at(x, y int) float32 {
	if x < 0 {
		x = 0
	} else if x >= p.w {
		x = p.w - 1
	}
	if y < 0 {
		y = 0
	} else if y >= p.h {
		y = p.h - 1
	}
	return p.pix[y*p.w+x]
}

// lumaPlane converts an NRGBA image to Rec.601 luma.
func lumaPlane(img *image.NRGBA) *plane {
	b := img.Bounds()
	p := newPlane(b.Dx(), b.Dy())
	for y := 0; y < p.h; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < p.w; x++ {
			i := x * 4
			p.pix[y*p.w+x] = 0.299*float32(row[i]) + 0.587*float32(row[i+1]) + 0.114*float32(row[i+2])
		}
	}
	return p
}

// sigmaForKernel mirrors OpenCV's getGaussianKernel default sigma for a ksize.
func sigmaForKernel(ksize int) float64 { return 0.3*((float64(ksize)-1)*0.5-1) + 0.8 }

func gaussianKernel(sigma float64, radius int) []float32 {
	k := make([]float32, 2*radius+1)
	var sum float64
	for i := -radius; i <= radius; i++ {
		v := math.Exp(-float64(i*i) / (2 * sigma * sigma))
		k[i+radius] = float32(v)
		sum += v
	}
	for i := range k {
		k[i] /= float32(sum)
	}
	return k
}

// gaussianBlur is a separable Gaussian with replicated borders. radius 0 picks ceil(3σ).
func gaussianBlur(p *plane, sigma float64, radius int) *plane {
	if radius <= 0 {
		radius = int(math.Ceil(3 * sigma))
	}
	k := gaussianKernel(sigma, radius)
	tmp := newPlane(p.w, p.h)
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			var s float32
			for i := -radius; i <= radius; i++ {
				s += k[i+radius] * p.at(x+i, y)
			}
			tmp.pix[y*p.w+x] = s
		}
	}
	out := newPlane(p.w, p.h)
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			var s float32
			for i := -radius; i <= radius; i++ {
				s += k[i+radius] * tmp.at(x, y+i)
			}
			out.pix[y*p.w+x] = s
		}
	}
	return out
}

// sobelMagnitude returns the gradient magnitude.
func sobelMagnitude(p *plane) *plane {
	out := newPlane(p.w, p.h)
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			gx := -p.at(x-1, y-1) - 2*p.at(x-1, y) - p.at(x-1, y+1) + p.at(x+1, y-1) + 2*p.at(x+1, y) + p.at(x+1, y+1)
			gy := -p.at(x-1, y-1) - 2*p.at(x, y-1) - p.at(x+1, y-1) + p.at(x-1, y+1) + 2*p.at(x, y+1) + p.at(x+1, y+1)
			out.pix[y*p.w+x] = float32(math.Hypot(float64(gx), float64(gy)))
		}
	}
	return out
}

// clahe applies Contrast Limited Adaptive Histogram Equalization over a tiles x tiles grid
// with bilinear interpolation between tile mappings (OpenCV semantics for clipLimit).
func clahe(p *plane, clipLimit float64, tiles int) *plane {
	if tiles < 1 {
		tiles = 8
	}
	tw := int(math.Ceil(float64(p.w) / float64(tiles)))
	th := int(math.Ceil(float64(p.h) / float64(tiles)))
	luts := make([][256]float32, tiles*tiles)
	for ty := 0; ty < tiles; ty++ {
		for tx := 0; tx < tiles; tx++ {
			var hist [256]float64
			x0, y0 := tx*tw, ty*th
			x1, y1 := min(x0+tw, p.w), min(y0+th, p.h)
			n := 0
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					hist[clamp255(p.pix[y*p.w+x])]++
					n++
				}
			}
			lut := &luts[ty*tiles+tx]
			if n == 0 {
				for i := range lut {
					lut[i] = float32(i)
				}
				continue
			}
			limit := math.Max(1, clipLimit*float64(n)/256)
			excess := 0.0
			for i := range hist {
				if hist[i] > limit {
					excess += hist[i] - limit
					hist[i] = limit
				}
			}
			bonus := excess / 256
			cdf := 0.0
			for i := range hist {
				cdf += hist[i] + bonus
				lut[i] = float32(math.Min(255, cdf*255/float64(n)))
			}
		}
	}
	out := newPlane(p.w, p.h)
	for y := 0; y < p.h; y++ {
		fy := (float64(y)+0.5)/float64(th) - 0.5
		ty0 := int(math.Floor(fy))
		wy := float32(fy - float64(ty0))
		ty1 := ty0 + 1
		ty0, ty1 = clampInt(ty0, 0, tiles-1), clampInt(ty1, 0, tiles-1)
		for x := 0; x < p.w; x++ {
			fx := (float64(x)+0.5)/float64(tw) - 0.5
			tx0 := int(math.Floor(fx))
			wx := float32(fx - float64(tx0))
			tx1 := tx0 + 1
			tx0, tx1 = clampInt(tx0, 0, tiles-1), clampInt(tx1, 0, tiles-1)
			v := clamp255(p.pix[y*p.w+x])
			a := luts[ty0*tiles+tx0][v]*(1-wx) + luts[ty0*tiles+tx1][v]*wx
			b := luts[ty1*tiles+tx0][v]*(1-wx) + luts[ty1*tiles+tx1][v]*wx
			out.pix[y*p.w+x] = a*(1-wy) + b*wy
		}
	}
	return out
}

// adaptiveGaussianThreshold mirrors cv::adaptiveThreshold(ADAPTIVE_THRESH_GAUSSIAN_C, THRESH_BINARY):
// dst = 255 if src > gaussian_mean(blockSize) - C else 0.
func adaptiveGaussianThreshold(p *plane, blockSize int, c float32) *image.Gray {
	mean := gaussianBlur(p, sigmaForKernel(blockSize), blockSize/2)
	out := image.NewGray(image.Rect(0, 0, p.w, p.h))
	for i, v := range p.pix {
		if v > mean.pix[i]-c {
			out.Pix[i] = 255
		}
	}
	return out
}

// crop copies a sub-rectangle.
func (p *plane) crop(x, y, w, h int) *plane {
	out := newPlane(w, h)
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			out.pix[yy*w+xx] = p.at(x+xx, y+yy)
		}
	}
	return out
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 0 {
		return (s[m-1] + s[m]) / 2
	}
	return s[m]
}

func clamp255(v float32) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return int(v)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// sampleBilinear reads an NRGBA pixel at fractional coordinates (edge-clamped).
func sampleBilinear(img *image.NRGBA, x, y float64) color.NRGBA {
	b := img.Bounds()
	x = math.Max(0, math.Min(float64(b.Dx()-1), x))
	y = math.Max(0, math.Min(float64(b.Dy()-1), y))
	x0, y0 := int(x), int(y)
	x1, y1 := min(x0+1, b.Dx()-1), min(y0+1, b.Dy()-1)
	fx, fy := x-float64(x0), y-float64(y0)
	px := func(xx, yy, c int) float64 { return float64(img.Pix[yy*img.Stride+xx*4+c]) }
	var out [4]uint8
	for c := 0; c < 4; c++ {
		top := px(x0, y0, c)*(1-fx) + px(x1, y0, c)*fx
		bot := px(x0, y1, c)*(1-fx) + px(x1, y1, c)*fx
		out[c] = uint8(math.Round(top*(1-fy) + bot*fy))
	}
	return color.NRGBA{out[0], out[1], out[2], out[3]}
}
