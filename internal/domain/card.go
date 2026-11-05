// Package domain holds Hootdex's pure business types and rules.
// It must only import the Go standard library.
package domain

import (
	"fmt"
	"strings"
	"time"
)

// Category is the card's sport / TCG family (master_checklists.category).
type Category string

const (
	CategoryBaseball   Category = "BASEBALL"
	CategoryBasketball Category = "BASKETBALL"
	CategoryFootball   Category = "FOOTBALL"
	CategoryHockey     Category = "HOCKEY"
	CategoryGolf       Category = "GOLF"
	CategoryPokemon    Category = "POKEMON"
	CategoryYugioh     Category = "YUGIOH"
	CategoryMiscTCG    Category = "MISC_TCG"
)

// AllCategories lists every category allowed by the schema CHECK constraint.
var AllCategories = []Category{
	CategoryBaseball, CategoryBasketball, CategoryFootball, CategoryHockey,
	CategoryGolf, CategoryPokemon, CategoryYugioh, CategoryMiscTCG,
}

// Valid reports whether c is one of the schema-approved categories.
func (c Category) Valid() bool {
	for _, k := range AllCategories {
		if c == k {
			return true
		}
	}
	return false
}

// IsTCG reports whether the category is a trading-card game (collector number on the front).
func (c Category) IsTCG() bool {
	return c == CategoryPokemon || c == CategoryYugioh || c == CategoryMiscTCG
}

// SKUCode is the two-letter category code used in SKUs (e.g. "FB").
func (c Category) SKUCode() string {
	switch c {
	case CategoryBaseball:
		return "BB"
	case CategoryBasketball:
		return "BK"
	case CategoryFootball:
		return "FB"
	case CategoryHockey:
		return "HK"
	case CategoryGolf:
		return "GF"
	case CategoryPokemon:
		return "PK"
	case CategoryYugioh:
		return "YG"
	default:
		return "TC"
	}
}

// RawCondition is the estimated condition of an ungraded card.
type RawCondition string

const (
	ConditionNMMT RawCondition = "NM-MT"
	ConditionNM   RawCondition = "NM"
	ConditionEXMT RawCondition = "EX-MT"
	ConditionVGEX RawCondition = "VG-EX"
	ConditionPoor RawCondition = "POOR"
)

// Valid reports whether the condition is allowed by the schema ("" means unknown and is valid).
func (r RawCondition) Valid() bool {
	switch r {
	case "", ConditionNMMT, ConditionNM, ConditionEXMT, ConditionVGEX, ConditionPoor:
		return true
	}
	return false
}

// GradingCompany identifies the slab grader ("RAW" when ungraded).
type GradingCompany string

const (
	GraderRaw GradingCompany = "RAW"
	GraderPSA GradingCompany = "PSA"
	GraderBGS GradingCompany = "BGS"
	GraderSGC GradingCompany = "SGC"
	GraderCGC GradingCompany = "CGC"
)

// Valid reports whether the grader is known ("" is treated as RAW).
func (g GradingCompany) Valid() bool {
	switch g {
	case "", GraderRaw, GraderPSA, GraderBGS, GraderSGC, GraderCGC:
		return true
	}
	return false
}

// InventoryStatus is the lifecycle state of a physical card in the shop.
type InventoryStatus string

const (
	StatusInReview  InventoryStatus = "IN_REVIEW"
	StatusInStock   InventoryStatus = "IN_STOCK"
	StatusListed    InventoryStatus = "LISTED"
	StatusSold      InventoryStatus = "SOLD"
	StatusVaultHold InventoryStatus = "VAULT_HOLD"
)

// Valid reports whether the status is allowed by the schema.
func (s InventoryStatus) Valid() bool {
	switch s {
	case StatusInReview, StatusInStock, StatusListed, StatusSold, StatusVaultHold:
		return true
	}
	return false
}

// BaseVariant is the default parallel name for a non-parallel card.
const BaseVariant = "Base"

// ChecklistEntry is one ground-truth row of master_checklists.
type ChecklistEntry struct {
	ID                int64    `json:"id"`
	Category          Category `json:"category"`
	ReleaseYear       int      `json:"release_year"`
	Manufacturer      string   `json:"manufacturer"`
	SetName           string   `json:"set_name"`
	CardNumber        string   `json:"card_number"`
	PlayerOrCharacter string   `json:"player_or_character"`
	TeamOrType        string   `json:"team_or_type,omitempty"`
	IsRookieBase      bool     `json:"is_rookie_base"`
	KnownParallels    []string `json:"known_parallels"`
}

// DisplayName renders "2023 Panini Prizm #339 C.J. Stroud".
func (e ChecklistEntry) DisplayName() string {
	return fmt.Sprintf("%d %s %s #%s %s", e.ReleaseYear, e.Manufacturer, e.SetName, e.CardNumber, e.PlayerOrCharacter)
}

// Validate checks the invariants enforced by the schema.
func (e ChecklistEntry) Validate() error {
	switch {
	case !e.Category.Valid():
		return fmt.Errorf("%w: category %q", ErrInvalid, e.Category)
	case e.ReleaseYear < 1860 || e.ReleaseYear > 2100:
		return fmt.Errorf("%w: release_year %d", ErrInvalid, e.ReleaseYear)
	case strings.TrimSpace(e.Manufacturer) == "", strings.TrimSpace(e.SetName) == "",
		strings.TrimSpace(e.CardNumber) == "", strings.TrimSpace(e.PlayerOrCharacter) == "":
		return fmt.Errorf("%w: manufacturer, set_name, card_number and player_or_character are required", ErrInvalid)
	}
	return nil
}

// ChecklistCandidate is a ranked fuzzy-match result.
type ChecklistCandidate struct {
	Entry ChecklistEntry `json:"entry"`
	Score float64        `json:"score"` // 0.0 .. 1.0
}

// Card is a physical card in shop inventory (inventory_cards row).
type Card struct {
	ID                string
	SKU               string
	IngestionJobID    string
	ChecklistID       *int64
	StorageLocationID string

	// Core identity
	Category          Category
	ReleaseYear       int
	Manufacturer      string
	SetName           string
	CardNumber        string
	PlayerOrCharacter string
	TeamOrType        string
	ParallelVariant   string
	SerialNumber      string // "045/199" or "" when unnumbered
	SerialMax         *int
	IsRookie          bool
	IsAutograph       bool
	IsMemorabilia     bool

	// Condition & grading
	IsGraded              bool
	GradingCompany        GradingCompany
	GradeValue            *float64
	CertNumber            string
	EstimatedRawCondition RawCondition
	CenteringRatioLR      string
	CenteringRatioTB      string
	CornerWearScore       *float64

	// Valuation
	MarketPriceCents     int64
	AcquisitionCostCents int64
	AskingPriceCents     int64
	PlayerImpactTier     int // 1 = HOF/superstar .. 5 = common
	ScarcityMultiplier   float64
	HootScore            float64

	// Listing & status
	InventoryStatus     InventoryStatus
	MarketplaceMetaJSON string
	FrontImageURL       string
	BackImageURL        string
	CreatedAt           time.Time
	UpdatedAt           time.Time

	// PendingPriceQuote is transient (not a column): when set, the repository records it in
	// price_snapshots in the same transaction that inserts the card.
	PendingPriceQuote *PriceQuote
}

// NewCardFromPayload builds an inventory card from an extraction payload,
// applying schema defaults. Valuation fields are left for the caller.
func NewCardFromPayload(p CardExtractionPayload) *Card {
	c := &Card{
		PlayerImpactTier:    DefaultImpactTier,
		ScarcityMultiplier:  1.0,
		InventoryStatus:     StatusInReview,
		MarketplaceMetaJSON: "{}",
	}
	c.ApplyPayload(p)
	return c
}

// ApplyPayload copies identity and condition attributes from p onto the card.
func (c *Card) ApplyPayload(p CardExtractionPayload) {
	c.Category = p.Category
	c.ReleaseYear = p.ReleaseYear
	c.Manufacturer = p.Manufacturer
	c.SetName = p.SetName
	c.CardNumber = p.CardNumber
	c.PlayerOrCharacter = p.PlayerOrCharacter
	c.TeamOrType = deref(p.TeamOrType)
	c.ParallelVariant = p.ParallelVariant
	if strings.TrimSpace(c.ParallelVariant) == "" {
		c.ParallelVariant = BaseVariant
	}
	c.SerialNumber = deref(p.SerialNumber)
	c.SerialMax = p.SerialMax
	c.IsRookie = p.IsRookie
	c.IsAutograph = p.IsAutograph
	c.IsMemorabilia = p.IsMemorabilia
	c.IsGraded = p.IsGraded
	c.GradingCompany = p.GradingCompany
	if c.GradingCompany == "" {
		c.GradingCompany = GraderRaw
	}
	c.GradeValue = p.GradeValue
	c.CertNumber = deref(p.CertNumber)
	c.EstimatedRawCondition = RawCondition(deref(p.EstimatedRawCondition))
	c.CenteringRatioLR = deref(p.CenteringRatioLR)
	c.CenteringRatioTB = deref(p.CenteringRatioTB)
}

// IsNumbered reports whether the card carries a serial print-run stamp.
func (c *Card) IsNumbered() bool { return c.SerialMax != nil && *c.SerialMax > 0 }

// IsParallel reports whether the card is a non-base parallel / holo.
func (c *Card) IsParallel() bool {
	v := strings.TrimSpace(c.ParallelVariant)
	return v != "" && !strings.EqualFold(v, BaseVariant)
}

// Validate checks the schema-level invariants before persistence.
func (c *Card) Validate() error {
	switch {
	case !c.Category.Valid():
		return fmt.Errorf("%w: category %q", ErrInvalid, c.Category)
	case c.ReleaseYear <= 0:
		return fmt.Errorf("%w: release_year required", ErrInvalid)
	case c.Manufacturer == "" || c.SetName == "" || c.CardNumber == "" || c.PlayerOrCharacter == "":
		return fmt.Errorf("%w: identity fields required", ErrInvalid)
	case c.PlayerImpactTier < 1 || c.PlayerImpactTier > 5:
		return fmt.Errorf("%w: player_impact_tier %d", ErrInvalid, c.PlayerImpactTier)
	case !c.InventoryStatus.Valid():
		return fmt.Errorf("%w: inventory_status %q", ErrInvalid, c.InventoryStatus)
	case !c.EstimatedRawCondition.Valid():
		return fmt.Errorf("%w: estimated_raw_condition %q", ErrInvalid, c.EstimatedRawCondition)
	case !c.GradingCompany.Valid():
		return fmt.Errorf("%w: grading_company %q", ErrInvalid, c.GradingCompany)
	case c.FrontImageURL == "":
		return fmt.Errorf("%w: front_image_url required", ErrInvalid)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// StrPtr returns a pointer to s, or nil when s is empty.
func StrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// DefaultImpactTier is the schema default for player_impact_tier (3 = solid starter).
const DefaultImpactTier = 3
