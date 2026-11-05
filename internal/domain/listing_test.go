package domain

import (
	"strings"
	"testing"
)

func stroudCard() *Card {
	return &Card{Category: CategoryFootball, ReleaseYear: 2023, Manufacturer: "Panini", SetName: "Prizm", CardNumber: "339",
		PlayerOrCharacter: "C.J. Stroud", TeamOrType: "Houston Texans", ParallelVariant: "Silver Prizm", IsRookie: true,
		EstimatedRawCondition: ConditionNMMT, GradingCompany: GraderRaw}
}

func TestListingTitleSpecExample(t *testing.T) {
	want := "2023 Panini Prizm #339 C.J. Stroud Silver Prizm RC Rookie Houston Texans NM-MT"
	if got := ListingTitle(stroudCard()); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestListingTitleDropsLowPriorityToFit80(t *testing.T) {
	c := stroudCard()
	c.PlayerOrCharacter = "Jaxon Smith-Njigba"
	c.TeamOrType = "Seattle Seahawks"
	c.ParallelVariant = "Red White and Blue Prizm"
	c.SerialMax = intp(199)
	c.IsAutograph = true
	got := ListingTitle(c)
	if len(got) > MaxListingTitle {
		t.Fatalf("title %d chars: %q", len(got), got)
	}
	for _, must := range []string{"2023", "Prizm", "#339", "Jaxon Smith-Njigba", "/199", "Auto", "RC"} {
		if !strings.Contains(got, must) {
			t.Fatalf("essential %q dropped: %q", must, got)
		}
	}
	if strings.Contains(got, "NM-MT") {
		t.Fatalf("condition should be dropped first: %q", got)
	}
}

func TestListingTitleGradedAndPokemon(t *testing.T) {
	c := stroudCard()
	c.IsGraded, c.GradingCompany, c.GradeValue = true, GraderPSA, floatp(10)
	if got := ListingTitle(c); !strings.Contains(got, "PSA 10") || strings.Contains(got, "NM-MT") {
		t.Fatalf("graded: %q", got)
	}
	pk := &Card{Category: CategoryPokemon, ReleaseYear: 1999, Manufacturer: "Wizards of the Coast", SetName: "Base Set", CardNumber: "4/102",
		PlayerOrCharacter: "Charizard", TeamOrType: "Fire / Stage 2", ParallelVariant: "Holo", EstimatedRawCondition: ConditionEXMT}
	m := BuildMarketplaceMeta(pk, &PriceQuote{Source: PricePriceCharting, PriceCents: 30000})
	if m.EbayCategoryID != "183454" || m.TCGplayerCondition != "Lightly Played" || m.EbayConditionID != "4000" ||
		strings.Contains(m.EbayTitle, "Fire") || m.PriceSource != PricePriceCharting {
		t.Fatalf("pokemon meta %+v", m)
	}
	gm := BuildMarketplaceMeta(c, nil)
	if gm.EbayConditionID != "2750" || gm.TCGplayerCondition != "Graded" || gm.EbayCardCondition != "" {
		t.Fatalf("graded meta %+v", gm)
	}
}

func TestAskingPrice(t *testing.T) {
	tests := map[int64]int64{0: 99, 100: 199, 10000: 10999, 2499: 2799}
	for in, want := range tests {
		if got := AskingPrice(in, 10); got != want {
			t.Errorf("AskingPrice(%d) = %d want %d", in, got, want)
		}
	}
}
