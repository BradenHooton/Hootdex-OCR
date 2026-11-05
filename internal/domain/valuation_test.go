package domain

import (
	"errors"
	"math"
	"testing"
)

func intp(i int) *int           { return &i }
func floatp(f float64) *float64 { return &f }

func approx(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestValueScoreAnchors(t *testing.T) {
	tests := []struct {
		name  string
		cents int64
		want  float64
	}{
		{"zero", 0, 0},
		{"negative", -500, 0},
		{"fifty cents", 50, 7.5},
		{"$1", 100, 15},
		{"$25", 2500, 55},
		{"$100", 10000, 80},
		{"$500", 50000, 100},
		{"$10k caps", 1_000_000, 100},
		{"$5 between anchors", 500, 15 + 40*math.Log10(5)/math.Log10(25)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValueScore(tt.cents); !approx(got, tt.want) {
				t.Fatalf("ValueScore(%d) = %.3f, want %.3f", tt.cents, got, tt.want)
			}
		})
	}
}

func TestValueScoreMonotonic(t *testing.T) {
	prev := -1.0
	for c := int64(0); c <= 60000; c += 37 {
		v := ValueScore(c)
		if v < prev {
			t.Fatalf("ValueScore not monotonic at %d: %.3f < %.3f", c, v, prev)
		}
		prev = v
	}
}

func TestImpactScore(t *testing.T) {
	tests := []struct {
		tier   int
		rookie bool
		want   float64
	}{
		{1, false, 100}, {1, true, 100}, {2, false, 80}, {2, true, 95},
		{3, false, 55}, {3, true, 70}, {4, false, 30}, {5, false, 10}, {5, true, 25},
		{0, false, 55}, {9, true, 70},
	}
	for _, tt := range tests {
		if got := ImpactScore(tt.tier, tt.rookie); got != tt.want {
			t.Errorf("ImpactScore(%d,%v) = %v, want %v", tt.tier, tt.rookie, got, tt.want)
		}
	}
}

func TestScarcityScore(t *testing.T) {
	tests := []struct {
		name    string
		max     *int
		variant string
		want    float64
	}{
		{"1/1", intp(1), "Gold Vinyl", 100},
		{"/5", intp(5), "Black", 95},
		{"/10", intp(10), "Gold", 95},
		{"/25", intp(25), "Red", 85},
		{"/49", intp(49), "Blue", 75},
		{"/99", intp(99), "Blue", 75},
		{"/199", intp(199), "Purple", 60},
		{"/299", intp(299), "Orange", 60},
		{"/499", intp(499), "Green", 50},
		{"silver parallel", nil, "Silver Prizm", 40},
		{"holo", nil, "Holo Rare", 40},
		{"base", nil, "Base", 10},
		{"empty variant", nil, "", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Card{SerialMax: tt.max, ParallelVariant: tt.variant}
			if got := ScarcityScore(c); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestConditionScore(t *testing.T) {
	tests := []struct {
		name   string
		graded bool
		grade  *float64
		raw    RawCondition
		want   float64
	}{
		{"PSA 10", true, floatp(10), "", 100},
		{"BGS 9.5", true, floatp(9.5), "", 95},
		{"PSA 9", true, floatp(9), "", 85},
		{"PSA 8", true, floatp(8), "", 70},
		{"PSA 6", true, floatp(6), "", 35},
		{"raw NM-MT", false, nil, ConditionNMMT, 85},
		{"raw NM", false, nil, ConditionNM, 70},
		{"raw EX-MT", false, nil, ConditionEXMT, 35},
		{"raw VG-EX", false, nil, ConditionVGEX, 35},
		{"raw poor", false, nil, ConditionPoor, 35},
		{"raw unknown", false, nil, "", 70},
		{"graded without value falls back to raw", true, nil, ConditionNMMT, 85},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Card{IsGraded: tt.graded, GradeValue: tt.grade, EstimatedRawCondition: tt.raw}
			if got := ConditionScore(c); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestCalculateHootScore(t *testing.T) {
	tests := []struct {
		name string
		card *Card
		want float64
	}{
		{"nil card", nil, 0},
		{
			"C.J. Stroud Silver Prizm RC raw NM-MT $100",
			&Card{MarketPriceCents: 10000, PlayerImpactTier: 2, IsRookie: true, ParallelVariant: "Silver Prizm", EstimatedRawCondition: ConditionNMMT},
			0.45*80 + 0.25*95 + 0.15*40 + 0.15*85,
		},
		{
			"common base $1 tier 5 raw NM",
			&Card{MarketPriceCents: 100, PlayerImpactTier: 5, ParallelVariant: "Base", EstimatedRawCondition: ConditionNM},
			0.45*15 + 0.25*10 + 0.15*10 + 0.15*70,
		},
		{
			"perfect card caps at 100",
			&Card{MarketPriceCents: 10_000_000, PlayerImpactTier: 1, IsRookie: true, SerialMax: intp(1), IsGraded: true, GradeValue: floatp(10)},
			100,
		},
		{
			"PSA 9 /25 tier 3 $25",
			&Card{MarketPriceCents: 2500, PlayerImpactTier: 3, SerialMax: intp(25), IsGraded: true, GradeValue: floatp(9)},
			0.45*55 + 0.25*55 + 0.15*85 + 0.15*85,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateHootScore(tt.card)
			if !approx(got, tt.want) {
				t.Fatalf("got %.4f want %.4f", got, tt.want)
			}
			if got < 0 || got > 100 {
				t.Fatalf("score out of range: %v", got)
			}
		})
	}
}

func TestNextAvailableLocation(t *testing.T) {
	mk := func(v, c, b, r string, s, cap, occ int) StorageLocation {
		l, err := NewStorageLocation(v, c, b, r, s, cap)
		if err != nil {
			t.Fatal(err)
		}
		l.OccupiedCount = occ
		return l
	}
	locs := []StorageLocation{
		mk("VAULT_B", "CASE_01", "BOX_01", "ROW_1", 1, 5, 0),
		mk("VAULT_A", "CASE_01", "BOX_02", "ROW_1", 1, 5, 0),
		mk("VAULT_A", "CASE_01", "BOX_01", "ROW_1", 2, 5, 0),
		mk("VAULT_A", "CASE_01", "BOX_01", "ROW_1", 1, 5, 5), // full
	}
	got, err := NextAvailableLocation(locs)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "VAULT_A-CASE_01-BOX_01-ROW_1-S002" {
		t.Fatalf("got %s", got.ID)
	}
	full := []StorageLocation{mk("V", "C", "B", "R", 1, 1, 1)}
	if _, err := NextAvailableLocation(full); !errors.Is(err, ErrNoStorageCapacity) {
		t.Fatalf("expected ErrNoStorageCapacity, got %v", err)
	}
}

func TestStorageLocationOccupyRelease(t *testing.T) {
	l, err := NewStorageLocation("vault_a", "case_01", "box_01", "row_1", 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	if l.BarcodeLabel != "HY-LOC-VAULT_A-CASE_01-BOX_01-ROW_1-S007" {
		t.Fatalf("barcode %s", l.BarcodeLabel)
	}
	if err := l.Occupy(); err != nil {
		t.Fatal(err)
	}
	if err := l.Occupy(); err != nil {
		t.Fatal(err)
	}
	if err := l.Occupy(); !errors.Is(err, ErrNoStorageCapacity) {
		t.Fatalf("expected full, got %v", err)
	}
	if err := l.Release(); err != nil || l.OccupiedCount != 1 {
		t.Fatalf("release: %v %d", err, l.OccupiedCount)
	}
	if _, err := NewStorageLocation("", "C", "B", "R", 1, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("expected invalid")
	}
	if _, err := NewStorageLocation("V", "C", "B", "R", 0, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("expected invalid slot")
	}
}

func TestSKU(t *testing.T) {
	tests := []struct {
		card *Card
		want string
	}{
		{&Card{Category: CategoryFootball, ReleaseYear: 2023, SetName: "Prizm", CardNumber: "301"}, "HY-FB-2023-PRZ-301"},
		{&Card{Category: CategoryBaseball, ReleaseYear: 2024, SetName: "Topps Chrome", CardNumber: "US175"}, "HY-BB-2024-TCH-US175"},
		{&Card{Category: CategoryPokemon, ReleaseYear: 1999, SetName: "Base Set", CardNumber: "4/102"}, "HY-PK-1999-BST-4102"},
		{&Card{Category: CategoryHockey, ReleaseYear: 2023, SetName: "", CardNumber: ""}, "HY-HK-2023-UNK-0"},
	}
	for _, tt := range tests {
		if got := SKUPrefix(tt.card); got != tt.want {
			t.Errorf("SKUPrefix = %s want %s", got, tt.want)
		}
	}
	if FormatSKU("HY-FB-2023-PRZ-301", 1) != "HY-FB-2023-PRZ-301-001" {
		t.Fatal("FormatSKU")
	}
}
