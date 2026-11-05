package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ReviewThreshold is the confidence below which a scan is routed to human review.
const ReviewThreshold = 0.85

// CardExtractionPayload is the canonical JSON contract every OCR/VLM adapter returns (SCHEMA.md §3).
type CardExtractionPayload struct {
	Category              Category       `json:"category"`
	ReleaseYear           int            `json:"release_year"`
	Manufacturer          string         `json:"manufacturer"`
	SetName               string         `json:"set_name"`
	CardNumber            string         `json:"card_number"`
	PlayerOrCharacter     string         `json:"player_or_character"`
	TeamOrType            *string        `json:"team_or_type"`
	ParallelVariant       string         `json:"parallel_variant"`
	SerialNumber          *string        `json:"serial_number"`
	SerialMax             *int           `json:"serial_max"`
	IsRookie              bool           `json:"is_rookie"`
	IsAutograph           bool           `json:"is_autograph"`
	IsMemorabilia         bool           `json:"is_memorabilia"`
	IsGraded              bool           `json:"is_graded"`
	GradingCompany        GradingCompany `json:"grading_company"`
	GradeValue            *float64       `json:"grade_value"`
	CertNumber            *string        `json:"cert_number"`
	EstimatedRawCondition *string        `json:"estimated_raw_condition"`
	CenteringRatioLR      *string        `json:"centering_ratio_lr"`
	CenteringRatioTB      *string        `json:"centering_ratio_tb"`
	ConfidenceScore       float64        `json:"confidence_score"`
	ExtractionNotes       string         `json:"extraction_notes"`
}

var ratioRe = regexp.MustCompile(`^\d{1,3}/\d{1,3}$`)

// Normalize fills defaults (Base variant, RAW grader) and trims strings.
func (p *CardExtractionPayload) Normalize() {
	p.Category = Category(strings.ToUpper(strings.TrimSpace(string(p.Category))))
	p.Manufacturer = strings.TrimSpace(p.Manufacturer)
	p.SetName = strings.TrimSpace(p.SetName)
	p.CardNumber = strings.TrimPrefix(strings.TrimSpace(p.CardNumber), "#")
	p.PlayerOrCharacter = strings.TrimSpace(p.PlayerOrCharacter)
	p.ParallelVariant = strings.TrimSpace(p.ParallelVariant)
	if p.ParallelVariant == "" {
		p.ParallelVariant = BaseVariant
	}
	p.GradingCompany = GradingCompany(strings.ToUpper(strings.TrimSpace(string(p.GradingCompany))))
	if p.GradingCompany == "" {
		p.GradingCompany = GraderRaw
	}
	if p.IsGraded && p.GradingCompany == GraderRaw {
		p.IsGraded = p.GradeValue != nil
	}
	for _, sp := range []**string{&p.TeamOrType, &p.SerialNumber, &p.CertNumber, &p.EstimatedRawCondition, &p.CenteringRatioLR, &p.CenteringRatioTB} {
		if *sp != nil && strings.TrimSpace(**sp) == "" {
			*sp = nil
		}
	}
	if p.ConfidenceScore < 0 {
		p.ConfidenceScore = 0
	}
	if p.ConfidenceScore > 1 {
		p.ConfidenceScore = 1
	}
}

// Validate enforces the contract's structural rules. Identity fields may be empty
// for low-confidence payloads, but every populated field must be well-formed.
func (p *CardExtractionPayload) Validate() error {
	var errs []string
	if p.Category != "" && !p.Category.Valid() {
		errs = append(errs, fmt.Sprintf("category %q", p.Category))
	}
	if p.ReleaseYear != 0 && (p.ReleaseYear < 1860 || p.ReleaseYear > 2100) {
		errs = append(errs, fmt.Sprintf("release_year %d", p.ReleaseYear))
	}
	if !p.GradingCompany.Valid() {
		errs = append(errs, fmt.Sprintf("grading_company %q", p.GradingCompany))
	}
	if p.EstimatedRawCondition != nil && !RawCondition(*p.EstimatedRawCondition).Valid() {
		errs = append(errs, fmt.Sprintf("estimated_raw_condition %q", *p.EstimatedRawCondition))
	}
	for name, v := range map[string]*string{"centering_ratio_lr": p.CenteringRatioLR, "centering_ratio_tb": p.CenteringRatioTB} {
		if v != nil && !ratioRe.MatchString(*v) {
			errs = append(errs, fmt.Sprintf("%s %q", name, *v))
		}
	}
	if p.SerialMax != nil && *p.SerialMax <= 0 {
		errs = append(errs, "serial_max must be positive")
	}
	if p.GradeValue != nil && (*p.GradeValue < 1 || *p.GradeValue > 10) {
		errs = append(errs, fmt.Sprintf("grade_value %.1f", *p.GradeValue))
	}
	if p.ConfidenceScore < 0 || p.ConfidenceScore > 1 {
		errs = append(errs, fmt.Sprintf("confidence_score %.2f", p.ConfidenceScore))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: payload: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	return nil
}

// IsComplete reports whether every identity field needed to create inventory is present.
func (p *CardExtractionPayload) IsComplete() bool {
	return p.Category.Valid() && p.ReleaseYear > 0 && p.Manufacturer != "" &&
		p.SetName != "" && p.CardNumber != "" && p.PlayerOrCharacter != ""
}

// NeedsReview reports whether the payload must be routed to the human verification queue.
func (p *CardExtractionPayload) NeedsReview() bool {
	return p.ConfidenceScore < ReviewThreshold || !p.IsComplete()
}

// ParsePayloadJSON strictly decodes, normalizes and validates a payload.
func ParsePayloadJSON(data []byte) (*CardExtractionPayload, error) {
	var p CardExtractionPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%w: payload json: %v", ErrInvalid, err)
	}
	p.Normalize()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// SnapToChecklist overwrites identity spelling with the checklist entry (prompt rule 4).
func (p *CardExtractionPayload) SnapToChecklist(e ChecklistEntry) {
	p.Category = e.Category
	p.ReleaseYear = e.ReleaseYear
	p.Manufacturer = e.Manufacturer
	p.SetName = e.SetName
	p.CardNumber = e.CardNumber
	p.PlayerOrCharacter = e.PlayerOrCharacter
	if e.TeamOrType != "" {
		p.TeamOrType = StrPtr(e.TeamOrType)
	}
	if e.IsRookieBase {
		p.IsRookie = true
	}
}
