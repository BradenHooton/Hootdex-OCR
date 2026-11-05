package domain

import "math"

// Hoot-Score weights (OCR_DOMAIN_CONTEXT.md §5).
const (
	weightValue     = 0.45
	weightImpact    = 0.25
	weightScarcity  = 0.15
	weightCondition = 0.15
	rookieBonus     = 15.0
)

// valueAnchors maps market price (USD) to ValueScore; interpolation is linear in log10(price).
var valueAnchors = []struct{ dollars, score float64 }{
	{1.00, 15},
	{25.00, 55},
	{100.00, 80},
	{500.00, 100},
}

// CalculateHootScore returns the composite 0..100 score for a card, rounded to 2 decimals:
//
//	HootScore = min(100, 0.45*Value + 0.25*Impact + 0.15*Scarcity + 0.15*Condition)
func CalculateHootScore(card *Card) float64 {
	if card == nil {
		return 0
	}
	s := weightValue*ValueScore(card.MarketPriceCents) +
		weightImpact*ImpactScore(card.PlayerImpactTier, card.IsRookie) +
		weightScarcity*ScarcityScore(card) +
		weightCondition*ConditionScore(card)
	return round2(math.Min(100, s))
}

// ValueScore log-scales market price: $1=15, $25=55, $100=80, $500+=100.
// Below $1 the score falls linearly to 0 at $0.
func ValueScore(priceCents int64) float64 {
	if priceCents <= 0 {
		return 0
	}
	d := float64(priceCents) / 100
	first, last := valueAnchors[0], valueAnchors[len(valueAnchors)-1]
	if d <= first.dollars {
		return first.score * d / first.dollars
	}
	if d >= last.dollars {
		return last.score
	}
	for i := 1; i < len(valueAnchors); i++ {
		lo, hi := valueAnchors[i-1], valueAnchors[i]
		if d <= hi.dollars {
			t := (math.Log10(d) - math.Log10(lo.dollars)) / (math.Log10(hi.dollars) - math.Log10(lo.dollars))
			return lo.score + t*(hi.score-lo.score)
		}
	}
	return last.score
}

// ImpactScore maps player_impact_tier (1..5) to 100/80/55/30/10, +15 for rookies (capped at 100).
// Out-of-range tiers fall back to the schema default tier.
func ImpactScore(tier int, isRookie bool) float64 {
	scores := map[int]float64{1: 100, 2: 80, 3: 55, 4: 30, 5: 10}
	s, ok := scores[tier]
	if !ok {
		s = scores[DefaultImpactTier]
	}
	if isRookie {
		s += rookieBonus
	}
	return math.Min(100, s)
}

// ScarcityScore: 1/1=100, /10 or less=95, /25=85, /99=75, /299=60,
// numbered above /299=50, non-numbered parallel/holo=40, base=10.
func ScarcityScore(card *Card) float64 {
	if card.IsNumbered() {
		switch m := *card.SerialMax; {
		case m == 1:
			return 100
		case m <= 10:
			return 95
		case m <= 25:
			return 85
		case m <= 99:
			return 75
		case m <= 299:
			return 60
		default:
			return 50
		}
	}
	if card.IsParallel() {
		return 40
	}
	return 10
}

// ConditionScore: grade 10=100, 9.5=95, 9 or raw NM-MT=85, raw NM (or graded 8–8.5)=70,
// EX-MT or lower=35. An ungraded card with no estimate is treated as NM.
func ConditionScore(card *Card) float64 {
	if card.IsGraded && card.GradeValue != nil {
		switch g := *card.GradeValue; {
		case g >= 10:
			return 100
		case g >= 9.5:
			return 95
		case g >= 9:
			return 85
		case g >= 8:
			return 70
		default:
			return 35
		}
	}
	switch card.EstimatedRawCondition {
	case ConditionNMMT:
		return 85
	case ConditionNM, "":
		return 70
	default:
		return 35
	}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
