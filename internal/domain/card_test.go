package domain

import (
	"errors"
	"testing"
)

func TestParsePayloadJSON_CanonicalExample(t *testing.T) {
	raw := `{"category":"FOOTBALL","release_year":2023,"manufacturer":"Panini","set_name":"Prizm","card_number":"339",
	"player_or_character":"C.J. Stroud","team_or_type":"Houston Texans","parallel_variant":"Silver Prizm","serial_number":null,
	"serial_max":null,"is_rookie":true,"is_autograph":false,"is_memorabilia":false,"is_graded":false,"grading_company":"RAW",
	"grade_value":null,"cert_number":null,"estimated_raw_condition":"NM-MT","centering_ratio_lr":"51/49","centering_ratio_tb":"53/47",
	"confidence_score":0.94,"extraction_notes":"ok"}`
	p, err := ParsePayloadJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !p.IsComplete() || p.NeedsReview() {
		t.Fatalf("expected complete high-confidence payload: %+v", p)
	}
	c := NewCardFromPayload(*p)
	if c.TeamOrType != "Houston Texans" || c.ParallelVariant != "Silver Prizm" || c.EstimatedRawCondition != ConditionNMMT {
		t.Fatalf("unexpected card mapping: %+v", c)
	}
	if !c.IsParallel() || c.IsNumbered() {
		t.Fatalf("parallel/numbered flags wrong")
	}
}

func TestPayloadValidateRejectsBadFields(t *testing.T) {
	bad := []string{
		`{"category":"CRICKET"}`,
		`{"release_year":1700}`,
		`{"centering_ratio_lr":"fifty"}`,
		`{"estimated_raw_condition":"MINT"}`,
		`{"grading_company":"XYZ"}`,
		`{"serial_max":-1}`,
		`not json`,
	}
	for _, b := range bad {
		if _, err := ParsePayloadJSON([]byte(b)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid, got %v", b, err)
		}
	}
}

func TestPayloadNormalizeDefaults(t *testing.T) {
	p := CardExtractionPayload{Category: "football", CardNumber: " #339 ", ConfidenceScore: 1.7, TeamOrType: StrPtr(" ")}
	p.TeamOrType = new(string)
	p.Normalize()
	if p.Category != CategoryFootball || p.CardNumber != "339" || p.ParallelVariant != BaseVariant ||
		p.GradingCompany != GraderRaw || p.ConfidenceScore != 1 || p.TeamOrType != nil {
		t.Fatalf("normalize: %+v", p)
	}
	if !p.NeedsReview() {
		t.Fatal("incomplete payload must need review")
	}
}

func TestSnapToChecklist(t *testing.T) {
	p := CardExtractionPayload{PlayerOrCharacter: "CJ STROUD", ConfidenceScore: 0.7}
	p.SnapToChecklist(ChecklistEntry{Category: CategoryFootball, ReleaseYear: 2023, Manufacturer: "Panini", SetName: "Prizm",
		CardNumber: "339", PlayerOrCharacter: "C.J. Stroud", TeamOrType: "Houston Texans", IsRookieBase: true})
	if p.PlayerOrCharacter != "C.J. Stroud" || !p.IsRookie || *p.TeamOrType != "Houston Texans" {
		t.Fatalf("snap: %+v", p)
	}
}

func TestCardValidate(t *testing.T) {
	c := &Card{Category: CategoryPokemon, ReleaseYear: 1999, Manufacturer: "Wizards of the Coast", SetName: "Base Set",
		CardNumber: "4/102", PlayerOrCharacter: "Charizard", PlayerImpactTier: 1, InventoryStatus: StatusInStock,
		GradingCompany: GraderRaw, FrontImageURL: "/img/f.jpg"}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid card rejected: %v", err)
	}
	c.PlayerImpactTier = 9
	if err := c.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected tier rejection, got %v", err)
	}
}

func TestJobTransitions(t *testing.T) {
	j, err := NewIngestionJob("id", "B1", "/f.jpg", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []JobStatus{JobPreprocessing, JobExtracting, JobMatching, JobNeedsReview, JobCompleted} {
		if err := j.TransitionTo(s); err != nil {
			t.Fatalf("transition to %s: %v", s, err)
		}
	}
	if err := j.TransitionTo(JobPending); !errors.Is(err, ErrConflict) {
		t.Fatalf("completed -> pending must fail, got %v", err)
	}
	if _, err := NewIngestionJob("", "B", "f", ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("expected invalid job")
	}
}

func TestCategoryHelpers(t *testing.T) {
	if !CategoryPokemon.IsTCG() || CategoryFootball.IsTCG() || Category("X").Valid() {
		t.Fatal("category helpers wrong")
	}
	if CategoryFootball.SKUCode() != "FB" || CategoryMiscTCG.SKUCode() != "TC" {
		t.Fatal("sku codes wrong")
	}
}
