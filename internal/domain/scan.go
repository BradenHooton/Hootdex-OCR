package domain

import (
	"fmt"
	"math"
)

// ScanPair is a duplex front/back scan grouped by the hot-folder watcher.
type ScanPair struct {
	BatchID   string `json:"batch_id"`
	Prefix    string `json:"prefix"`
	FrontPath string `json:"front_path"`
	BackPath  string `json:"back_path,omitempty"` // empty for single-sided scans
}

// CardType is the physical format detected from the aspect ratio.
type CardType string

const (
	CardTypeRaw    CardType = "RAW_CARD"
	CardTypeGraded CardType = "GRADED_SLAB"
)

// Canonical deskewed output sizes (OCR_DOMAIN_CONTEXT.md §2).
const (
	CanonicalCardWidth  = 750
	CanonicalCardHeight = 1050
	CanonicalSlabWidth  = 900
	CanonicalSlabHeight = 1450
	SlabAspectThreshold = 1.50 // height / width above this is a graded slab
	CornerPatchSize     = 60
)

// ClassifyAspect returns GRADED_SLAB when height/width > 1.50, otherwise RAW_CARD.
func ClassifyAspect(width, height float64) CardType {
	if width > 0 && height/width > SlabAspectThreshold {
		return CardTypeGraded
	}
	return CardTypeRaw
}

// CanonicalSize returns the deskew target dimensions for a card type.
func CanonicalSize(t CardType) (w, h int) {
	if t == CardTypeGraded {
		return CanonicalSlabWidth, CanonicalSlabHeight
	}
	return CanonicalCardWidth, CanonicalCardHeight
}

// ZoneName identifies an OCR region of interest.
type ZoneName string

const (
	ZoneFrontHeader ZoneName = "FRONT_HEADER"
	ZoneFrontFooter ZoneName = "FRONT_FOOTER"
	ZoneBackHeader  ZoneName = "BACK_HEADER"
	ZoneBackFooter  ZoneName = "BACK_FOOTER"
	ZoneSlabFlip    ZoneName = "SLAB_FLIP"
)

// Side is the face of the card a zone is sliced from.
type Side string

const (
	SideFront Side = "FRONT"
	SideBack  Side = "BACK"
)

// zoneSpec defines each zone as a vertical band: start fraction and height fraction.
var zoneSpec = map[ZoneName]struct {
	side          Side
	start, height float64
}{
	ZoneFrontHeader: {SideFront, 0, 0.18},
	ZoneFrontFooter: {SideFront, 1 - 0.22, 0.22},
	ZoneBackHeader:  {SideBack, 0, 0.20},
	ZoneBackFooter:  {SideBack, 1 - 0.15, 0.15},
	ZoneSlabFlip:    {SideFront, 0, 0.22},
}

// ZoneSide returns which face a zone belongs to.
func ZoneSide(z ZoneName) Side { return zoneSpec[z].side }

// ZonesFor lists the zones to slice for a card type and whether a back scan exists.
func ZonesFor(t CardType, hasBack bool) []ZoneName {
	zones := []ZoneName{ZoneFrontHeader, ZoneFrontFooter}
	if hasBack {
		zones = append(zones, ZoneBackHeader, ZoneBackFooter)
	}
	if t == CardTypeGraded {
		zones = append(zones, ZoneSlabFlip)
	}
	return zones
}

// Rect is an axis-aligned pixel rectangle.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// ZoneBounds returns the pixel rectangle of a zone on a canonical image of size w x h.
func ZoneBounds(z ZoneName, w, h int) Rect {
	s := zoneSpec[z]
	y := int(math.Round(s.start * float64(h)))
	zh := int(math.Round(s.height * float64(h)))
	if y+zh > h {
		zh = h - y
	}
	return Rect{X: 0, Y: y, W: w, H: zh}
}

// ZoneImage is a sliced + binarized OCR region.
type ZoneImage struct {
	Zone   ZoneName `json:"zone"`
	Side   Side     `json:"side"`
	Bounds Rect     `json:"bounds"`
	Path   string   `json:"path"`
}

// Point is a 2-D coordinate in source-image pixels.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// PreprocessResult is the image pipeline output persisted as preprocess_meta_json.
type PreprocessResult struct {
	CardType           CardType      `json:"card_type"`
	CroppedFrontPath   string        `json:"cropped_front_path"`
	CroppedBackPath    string        `json:"cropped_back_path,omitempty"`
	CanonicalWidth     int           `json:"canonical_width"`
	CanonicalHeight    int           `json:"canonical_height"`
	FrontQuad          [4]Point      `json:"front_quad"` // TL, TR, BR, BL in source pixels
	BackQuad           *[4]Point     `json:"back_quad,omitempty"`
	Zones              []ZoneImage   `json:"zones"`
	CenteringLR        string        `json:"centering_ratio_lr,omitempty"`
	CenteringTB        string        `json:"centering_ratio_tb,omitempty"`
	BorderPx           *BorderWidths `json:"border_px,omitempty"`
	CornerWearScore    *float64      `json:"corner_wear_score,omitempty"`
	CornerWear         []float64     `json:"corner_wear,omitempty"` // TL, TR, BR, BL
	EstimatedCondition RawCondition  `json:"estimated_condition,omitempty"`
}

// Zone returns the zone image with the given name, if present.
func (r *PreprocessResult) Zone(z ZoneName) (ZoneImage, bool) {
	for _, zi := range r.Zones {
		if zi.Zone == z {
			return zi, true
		}
	}
	return ZoneImage{}, false
}

// BorderWidths are measured print-border thicknesses on the canonical front crop.
type BorderWidths struct {
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
}

// CenteringRatio renders two opposite border widths as a "52/48" ratio string.
func CenteringRatio(a, b float64) (string, error) {
	if a < 0 || b < 0 || a+b == 0 {
		return "", fmt.Errorf("%w: centering borders %.1f/%.1f", ErrInvalid, a, b)
	}
	pa := int(math.Round(a / (a + b) * 100))
	return fmt.Sprintf("%d/%d", pa, 100-pa), nil
}

// worstSide parses "52/48" and returns the larger share (52).
func worstSide(ratio string) (int, bool) {
	var a, b int
	if _, err := fmt.Sscanf(ratio, "%d/%d", &a, &b); err != nil {
		return 0, false
	}
	if b > a {
		a = b
	}
	return a, true
}

// EstimateRawCondition grades a raw card from centering and corner wear, using
// PSA-style centering tolerances (55/45 for NM-MT, 60/40 NM, 70/30 EX-MT).
// Unknown inputs are treated as neutral (perfect centering / no wear).
func EstimateRawCondition(lr, tb string, cornerWear *float64) RawCondition {
	worst := 50
	for _, r := range []string{lr, tb} {
		if w, ok := worstSide(r); ok && w > worst {
			worst = w
		}
	}
	wear := 0.0
	if cornerWear != nil {
		wear = *cornerWear
	}
	switch {
	case worst <= 55 && wear < 0.15:
		return ConditionNMMT
	case worst <= 60 && wear < 0.30:
		return ConditionNM
	case worst <= 70 && wear < 0.50:
		return ConditionEXMT
	case wear < 0.75:
		return ConditionVGEX
	default:
		return ConditionPoor
	}
}
