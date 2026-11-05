package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// MaxListingTitle is eBay's (and TCGplayer's practical) title limit.
const MaxListingTitle = 80

// PriceSource values allowed by price_snapshots.source.
type PriceSource string

const (
	PriceEbaySold      PriceSource = "EBAY_SOLD"
	PriceTCGplayer     PriceSource = "TCGPLAYER_MARKET"
	PricePriceCharting PriceSource = "PRICECHARTING"
	PriceManual        PriceSource = "MANUAL" // includes the deterministic fallback estimate
)

// PriceQuote is one market observation.
type PriceQuote struct {
	PriceCents int64       `json:"price_cents"`
	Source     PriceSource `json:"source"`
	ListingURL string      `json:"listing_url,omitempty"`
	Estimated  bool        `json:"estimated"` // true when produced by the fallback estimator
	CapturedAt time.Time   `json:"captured_at"`
}

// MarketplaceMeta is persisted in inventory_cards.marketplace_meta_json.
type MarketplaceMeta struct {
	EbayTitle          string      `json:"ebay_title"`
	TCGplayerTitle     string      `json:"tcgplayer_title"`
	EbayCategoryID     string      `json:"ebay_category_id"`
	EbayConditionID    string      `json:"ebay_condition_id"`
	EbayCardCondition  string      `json:"ebay_card_condition,omitempty"` // ungraded condition descriptor
	TCGplayerCondition string      `json:"tcgplayer_condition"`
	PriceSource        PriceSource `json:"price_source,omitempty"`
	PriceEstimated     bool        `json:"price_estimated"`
	PricedAt           time.Time   `json:"priced_at,omitempty"`
}

// eBay leaf categories and condition IDs for trading cards.
const (
	ebaySportsSingles  = "261328" // Sports Trading Card Singles
	ebayPokemonSingles = "183454" // Pokémon CCG Individual Cards
	ebayYugiohSingles  = "183454" // CCG Individual Cards
	ebayGraded         = "2750"   // Graded
	ebayUngraded       = "4000"   // Ungraded
)

// ListingTitle builds an SEO-ready title of at most 80 characters, e.g.
// "2023 Panini Prizm #339 C.J. Stroud Silver Prizm RC Rookie Houston Texans NM-MT".
// Lower-priority descriptors are dropped first when the title is too long.
func ListingTitle(c *Card) string {
	type part struct {
		text     string
		priority int // higher survives longer
	}
	parts := []part{
		{fmt.Sprintf("%d", c.ReleaseYear), 100},
		{c.Manufacturer, 60},
		{c.SetName, 95},
		{"#" + strings.TrimPrefix(c.CardNumber, "#"), 90},
		{c.PlayerOrCharacter, 100},
	}
	if c.IsParallel() && !strings.HasPrefix(c.ParallelVariant, "Numbered") {
		parts = append(parts, part{c.ParallelVariant, 85})
	}
	if c.IsNumbered() {
		parts = append(parts, part{fmt.Sprintf("/%d", *c.SerialMax), 88})
	}
	if c.IsRookie {
		parts = append(parts, part{"RC", 87}, part{"Rookie", 40})
	}
	if c.IsAutograph {
		parts = append(parts, part{"Auto", 86})
	}
	if c.IsMemorabilia {
		parts = append(parts, part{"Patch", 70})
	}
	if c.IsGraded && c.GradeValue != nil {
		parts = append(parts, part{fmt.Sprintf("%s %s", c.GradingCompany, trimGrade(*c.GradeValue)), 92})
	}
	if c.TeamOrType != "" && !c.Category.IsTCG() {
		parts = append(parts, part{c.TeamOrType, 30})
	}
	if !c.IsGraded && c.EstimatedRawCondition != "" {
		parts = append(parts, part{string(c.EstimatedRawCondition), 20})
	}
	for {
		var words []string
		for _, p := range parts {
			if strings.TrimSpace(p.text) != "" {
				words = append(words, strings.TrimSpace(p.text))
			}
		}
		title := strings.Join(words, " ")
		if len(title) <= MaxListingTitle {
			return title
		}
		// Drop the lowest-priority part; if only essentials remain, hard-truncate.
		lowest := -1
		for i, p := range parts {
			if p.priority < 90 && (lowest < 0 || p.priority < parts[lowest].priority) {
				lowest = i
			}
		}
		if lowest < 0 {
			return strings.TrimSpace(title[:MaxListingTitle])
		}
		parts = append(parts[:lowest], parts[lowest+1:]...)
	}
}

func trimGrade(g float64) string {
	if g == math.Trunc(g) {
		return fmt.Sprintf("%.0f", g)
	}
	return fmt.Sprintf("%.1f", g)
}

// tcgplayerCondition maps the raw estimate to TCGplayer's condition vocabulary.
func tcgplayerCondition(c *Card) string {
	switch {
	case c.IsGraded:
		return "Graded"
	case c.EstimatedRawCondition == ConditionNMMT || c.EstimatedRawCondition == ConditionNM || c.EstimatedRawCondition == "":
		return "Near Mint"
	case c.EstimatedRawCondition == ConditionEXMT:
		return "Lightly Played"
	case c.EstimatedRawCondition == ConditionVGEX:
		return "Moderately Played"
	default:
		return "Heavily Played"
	}
}

func ebayCardCondition(c *Card) string {
	switch c.EstimatedRawCondition {
	case ConditionNMMT, ConditionNM, "":
		return "Near Mint or Better"
	case ConditionEXMT:
		return "Excellent"
	case ConditionVGEX:
		return "Very Good"
	default:
		return "Poor"
	}
}

// BuildMarketplaceMeta derives listing metadata for eBay Bulk Seller Hub and TCGplayer.
func BuildMarketplaceMeta(c *Card, q *PriceQuote) MarketplaceMeta {
	m := MarketplaceMeta{EbayTitle: ListingTitle(c), EbayCategoryID: ebaySportsSingles, EbayConditionID: ebayUngraded,
		TCGplayerCondition: tcgplayerCondition(c)}
	tcg := *c
	tcg.TeamOrType = "" // TCGplayer titles lead with product identity; team adds noise
	m.TCGplayerTitle = ListingTitle(&tcg)
	switch c.Category {
	case CategoryPokemon:
		m.EbayCategoryID = ebayPokemonSingles
	case CategoryYugioh, CategoryMiscTCG:
		m.EbayCategoryID = ebayYugiohSingles
	}
	if c.IsGraded {
		m.EbayConditionID = ebayGraded
	} else {
		m.EbayCardCondition = ebayCardCondition(c)
	}
	if q != nil {
		m.PriceSource, m.PriceEstimated, m.PricedAt = q.Source, q.Estimated, q.CapturedAt
	}
	return m
}

// AskingPrice marks the market price up by markupPct and rounds to a .99 price point
// (minimum $0.99).
func AskingPrice(marketCents int64, markupPct float64) int64 {
	if marketCents <= 0 {
		return 99
	}
	v := float64(marketCents) * (1 + markupPct/100)
	dollars := math.Ceil(v / 100)
	return int64(dollars*100) - 1
}
