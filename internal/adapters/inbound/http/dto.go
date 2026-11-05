package httpapi

import (
	"encoding/json"
	"time"

	"github.com/hootyeah/hootdex/internal/domain"
)

// cardDTO is the JSON shape of an inventory card for the UI.
type cardDTO struct {
	ID                    string                  `json:"id"`
	SKU                   string                  `json:"sku"`
	IngestionJobID        string                  `json:"ingestion_job_id,omitempty"`
	ChecklistID           *int64                  `json:"checklist_id,omitempty"`
	Category              domain.Category         `json:"category"`
	ReleaseYear           int                     `json:"release_year"`
	Manufacturer          string                  `json:"manufacturer"`
	SetName               string                  `json:"set_name"`
	CardNumber            string                  `json:"card_number"`
	PlayerOrCharacter     string                  `json:"player_or_character"`
	TeamOrType            string                  `json:"team_or_type,omitempty"`
	ParallelVariant       string                  `json:"parallel_variant"`
	SerialNumber          string                  `json:"serial_number,omitempty"`
	SerialMax             *int                    `json:"serial_max,omitempty"`
	IsRookie              bool                    `json:"is_rookie"`
	IsAutograph           bool                    `json:"is_autograph"`
	IsMemorabilia         bool                    `json:"is_memorabilia"`
	IsGraded              bool                    `json:"is_graded"`
	GradingCompany        domain.GradingCompany   `json:"grading_company,omitempty"`
	GradeValue            *float64                `json:"grade_value,omitempty"`
	CertNumber            string                  `json:"cert_number,omitempty"`
	EstimatedRawCondition domain.RawCondition     `json:"estimated_raw_condition,omitempty"`
	CenteringRatioLR      string                  `json:"centering_ratio_lr,omitempty"`
	CenteringRatioTB      string                  `json:"centering_ratio_tb,omitempty"`
	CornerWearScore       *float64                `json:"corner_wear_score,omitempty"`
	MarketPriceCents      int64                   `json:"market_price_cents"`
	AcquisitionCostCents  int64                   `json:"acquisition_cost_cents"`
	AskingPriceCents      int64                   `json:"asking_price_cents"`
	PlayerImpactTier      int                     `json:"player_impact_tier"`
	HootScore             float64                 `json:"hoot_score"`
	InventoryStatus       domain.InventoryStatus  `json:"inventory_status"`
	MarketplaceMeta       json.RawMessage         `json:"marketplace_meta"`
	FrontImageURL         string                  `json:"front_image_url"`
	BackImageURL          string                  `json:"back_image_url,omitempty"`
	Location              *domain.StorageLocation `json:"location,omitempty"`
	CreatedAt             time.Time               `json:"created_at"`
	UpdatedAt             time.Time               `json:"updated_at"`
}

func toCardDTO(rec domain.InventoryRecord) cardDTO {
	c := rec.Card
	meta := json.RawMessage(c.MarketplaceMetaJSON)
	if !json.Valid(meta) {
		meta = json.RawMessage("{}")
	}
	return cardDTO{
		ID: c.ID, SKU: c.SKU, IngestionJobID: c.IngestionJobID, ChecklistID: c.ChecklistID, Category: c.Category,
		ReleaseYear: c.ReleaseYear, Manufacturer: c.Manufacturer, SetName: c.SetName, CardNumber: c.CardNumber,
		PlayerOrCharacter: c.PlayerOrCharacter, TeamOrType: c.TeamOrType, ParallelVariant: c.ParallelVariant,
		SerialNumber: c.SerialNumber, SerialMax: c.SerialMax, IsRookie: c.IsRookie, IsAutograph: c.IsAutograph,
		IsMemorabilia: c.IsMemorabilia, IsGraded: c.IsGraded, GradingCompany: c.GradingCompany, GradeValue: c.GradeValue,
		CertNumber: c.CertNumber, EstimatedRawCondition: c.EstimatedRawCondition, CenteringRatioLR: c.CenteringRatioLR,
		CenteringRatioTB: c.CenteringRatioTB, CornerWearScore: c.CornerWearScore, MarketPriceCents: c.MarketPriceCents,
		AcquisitionCostCents: c.AcquisitionCostCents, AskingPriceCents: c.AskingPriceCents, PlayerImpactTier: c.PlayerImpactTier,
		HootScore: c.HootScore, InventoryStatus: c.InventoryStatus, MarketplaceMeta: meta, FrontImageURL: c.FrontImageURL,
		BackImageURL: c.BackImageURL, Location: rec.Location, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}
