package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/seed"
)

func seededChecklist(t *testing.T) *ChecklistRepository {
	t.Helper()
	db := openTestDB(t)
	repo := NewChecklistRepository(db)
	res, err := seed.Apply(context.Background(), repo, NewStorageRepository(db))
	if err != nil {
		t.Fatal(err)
	}
	if res.ChecklistRows != 50 || res.Locations != 20 {
		t.Fatalf("seed result %+v", res)
	}
	return repo
}

// Sprint 1 Verification Gate: "2023 PR1ZM #339 CJ STR0UD" -> 2023 Panini Prizm #339 C.J. Stroud, score > 0.90.
func TestMatchCandidatesSprint1Gate(t *testing.T) {
	repo := seededChecklist(t)
	got, err := repo.MatchCandidates(context.Background(), "2023 PR1ZM #339 CJ STR0UD", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	top := got[0]
	if top.Entry.DisplayName() != "2023 Panini Prizm #339 C.J. Stroud" {
		t.Fatalf("top = %s (%.3f)", top.Entry.DisplayName(), top.Score)
	}
	if top.Score <= 0.90 {
		t.Fatalf("score %.3f <= 0.90", top.Score)
	}
	if len(got) > 1 && got[1].Score >= top.Score {
		t.Fatalf("runner-up %s ties top", got[1].Entry.DisplayName())
	}
	if len(got) > MaxCandidates {
		t.Fatalf("returned %d > %d", len(got), MaxCandidates)
	}
}

func TestMatchCandidatesNoisyInputs(t *testing.T) {
	repo := seededChecklist(t)
	tests := []struct {
		name     string
		text     string
		year     int
		category domain.Category
		want     string
		minScore float64
	}{
		{"pokemon base charizard", "Charizard 120 HP 4/102 © 1999 Wizards", 0, "", "1999 Wizards of the Coast Base Set #4/102 Charizard", 0.75},
		{"sv151 zero padded", "PIKACHU 025/165 SV 151", 2023, domain.CategoryPokemon, "2023 Pokemon Company Scarlet & Violet 151 #025/165 Pikachu", 0.80},
		{"hockey young guns", "YOUNG GUNS C0NN0R BEDARD 201 UPPER DECK", 2023, "", "2023 Upper Deck Young Guns #201 Connor Bedard", 0.85},
		{"topps chrome hyphenated noise", "2024 TOPPS CHR0ME #220 JACKS0N H0LLIDAY RC", 0, "", "2024 Topps Topps Chrome #220 Jackson Holliday", 0.90},
		{"surname only with year hint", "PRIZM 301 YOUNG", 2023, domain.CategoryFootball, "2023 Panini Prizm #301 Bryce Young", 0.75},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.MatchCandidates(context.Background(), tt.text, tt.year, tt.category)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 || got[0].Entry.DisplayName() != tt.want {
				var top string
				if len(got) > 0 {
					top = got[0].Entry.DisplayName()
				}
				t.Fatalf("top = %q want %q", top, tt.want)
			}
			if got[0].Score < tt.minScore {
				t.Fatalf("score %.3f < %.2f", got[0].Score, tt.minScore)
			}
		})
	}
}

func TestMatchCandidatesGarbledFallsBackToYearScan(t *testing.T) {
	repo := seededChecklist(t)
	// No FTS-matchable token survives, but the year hint still yields ranked candidates.
	got, err := repo.MatchCandidates(context.Background(), "x7#~ qq", 2024, domain.CategoryBaseball)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected fallback candidates")
	}
	for _, c := range got {
		if c.Entry.ReleaseYear != 2024 && c.Entry.Category != domain.CategoryBaseball {
			t.Fatalf("unexpected candidate %s", c.Entry.DisplayName())
		}
	}
	none, err := repo.MatchCandidates(context.Background(), "   ", 0, "")
	if err != nil || len(none) != 0 {
		t.Fatalf("empty query: %v %v", none, err)
	}
}

func TestMatchCandidatesHandlesFTSSyntaxInOCR(t *testing.T) {
	repo := seededChecklist(t)
	if _, err := repo.MatchCandidates(context.Background(), `"PRIZM" AND (NEAR OR *) - ^339`, 0, ""); err != nil {
		t.Fatalf("FTS syntax leaked into query: %v", err)
	}
}

func TestChecklistUpsertIsIdempotentAndReindexes(t *testing.T) {
	repo := seededChecklist(t)
	ctx := context.Background()
	e := seed.Checklist()[0]
	e.TeamOrType = "HOU Texans"
	if err := repo.Upsert(ctx, &e); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.Count(ctx); n != 50 {
		t.Fatalf("count %d after upsert", n)
	}
	got, err := repo.Get(ctx, e.ID)
	if err != nil || got.TeamOrType != "HOU Texans" || len(got.KnownParallels) < 2 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := repo.Get(ctx, 99999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	bad := domain.ChecklistEntry{Category: "CRICKET"}
	if err := repo.Upsert(ctx, &bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
}
