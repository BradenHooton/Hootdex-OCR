package vision

import (
	"math"

	"github.com/hootyeah/hootdex/internal/domain"
)

const (
	borderInset       = 3    // skip warp/cut artefacts at the very edge
	borderRefWidth    = 5    // pixels used to sample the border colour
	borderJump        = 40.0 // luma change marking the border -> artwork transition
	borderRun         = 3    // consecutive pixels required past the jump
	borderMaxFrac     = 0.25 // borders thicker than 25% are not borders
	borderScanLines   = 41
	borderMinHitRatio = 0.5

	wearBandOuter   = 2
	wearBandInner   = 10
	wearInteriorMin = 20
	wearMinLuma     = 170.0
	wearLumaDelta   = 60.0
	wearSaturation  = 0.40 // a 40% whitened edge band saturates the score at 1.0
)

// measureBorders estimates print-border thickness on each side of a canonical crop by
// scanning inward from each edge for the first sustained luma change. ok=false for
// borderless (full-bleed) designs where no consistent transition exists.
func measureBorders(p *plane) (domain.BorderWidths, bool) {
	sm := gaussianBlur(p, 1.0, 0)
	side := func(n int, sample func(line, d int) float32, limit int) (float64, bool) {
		var hits []float64
		for i := 0; i < borderScanLines; i++ {
			line := int(0.2*float64(n) + 0.6*float64(n)*float64(i)/float64(borderScanLines-1))
			var ref float32
			for d := borderInset; d < borderInset+borderRefWidth; d++ {
				ref += sample(line, d)
			}
			ref /= borderRefWidth
			run := 0
			for d := borderInset + borderRefWidth; d < limit; d++ {
				if math.Abs(float64(sample(line, d)-ref)) > borderJump {
					run++
					if run == borderRun {
						hits = append(hits, float64(d-borderRun+1))
						break
					}
				} else {
					run = 0
				}
			}
		}
		if float64(len(hits)) < borderMinHitRatio*borderScanLines {
			return 0, false
		}
		return median(hits), true
	}
	limW, limH := int(borderMaxFrac*float64(p.w)), int(borderMaxFrac*float64(p.h))
	l, ok1 := side(p.h, func(y, d int) float32 { return sm.at(d, y) }, limW)
	r, ok2 := side(p.h, func(y, d int) float32 { return sm.at(p.w-1-d, y) }, limW)
	t, ok3 := side(p.w, func(x, d int) float32 { return sm.at(x, d) }, limH)
	b, ok4 := side(p.w, func(x, d int) float32 { return sm.at(x, p.h-1-d) }, limH)
	return domain.BorderWidths{Left: l, Right: r, Top: t, Bottom: b}, ok1 && ok2 && ok3 && ok4
}

// cornerWear inspects 60x60 corner patches (scaled to the canonical width) for
// high-luminance fray along the outer edges, relative to the local card-stock tone.
// Returns per-corner whitened fractions (TL, TR, BR, BL) and the 0..1 score.
func cornerWear(p *plane) ([]float64, float64) {
	size := int(math.Round(float64(domain.CornerPatchSize) * float64(p.w) / float64(domain.CanonicalCardWidth)))
	size = clampInt(size, 16, min(p.w, p.h)/2)
	type corner struct{ ox, oy, sx, sy int } // origin + direction into the card
	corners := []corner{{0, 0, 1, 1}, {p.w - 1, 0, -1, 1}, {p.w - 1, p.h - 1, -1, -1}, {0, p.h - 1, 1, -1}}
	fractions := make([]float64, 0, 4)
	total := 0.0
	for _, c := range corners {
		var interior []float64
		var band []float64
		for dy := 0; dy < size; dy++ {
			for dx := 0; dx < size; dx++ {
				v := float64(p.at(c.ox+c.sx*dx, c.oy+c.sy*dy))
				edge := min(dx, dy)
				switch {
				case edge >= wearBandOuter && edge < wearBandInner:
					band = append(band, v)
				case edge >= wearInteriorMin:
					interior = append(interior, v)
				}
			}
		}
		stock := median(interior)
		thresh := math.Max(stock+wearLumaDelta, wearMinLuma)
		white := 0
		for _, v := range band {
			if v > thresh {
				white++
			}
		}
		f := 0.0
		if len(band) > 0 {
			f = float64(white) / float64(len(band))
		}
		fractions = append(fractions, math.Round(f*1000)/1000)
		total += f
	}
	score := math.Min(1, total/4/wearSaturation)
	return fractions, math.Round(score*1000) / 1000
}
