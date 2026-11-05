// Command seed loads the reference master checklist (50 rows) and 20 five-tier storage
// locations into ./data/hootdex.db, then self-checks the fuzzy matcher.
//
//	go run ./cmd/cli/seed/main.go [-db ./data/hootdex.db] [-query "2023 PR1ZM #339 CJ STR0UD"]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/hootyeah/hootdex/internal/adapters/outbound/sqlite"
	"github.com/hootyeah/hootdex/internal/seed"
)

func main() {
	dbPath := flag.String("db", envOr("HOOTDEX_DB", "./data/hootdex.db"), "SQLite database path")
	query := flag.String("query", "2023 PR1ZM #339 CJ STR0UD", "OCR text used to self-check the fuzzy matcher")
	flag.Parse()
	if err := run(context.Background(), *dbPath, *query); err != nil {
		slog.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dbPath, query string) error {
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("seed.run: %w", err)
	}
	defer db.Close()

	checklist := sqlite.NewChecklistRepository(db)
	res, err := seed.Apply(ctx, checklist, sqlite.NewStorageRepository(db))
	if err != nil {
		return fmt.Errorf("seed.run: %w", err)
	}
	total, err := checklist.Count(ctx)
	if err != nil {
		return fmt.Errorf("seed.run: %w", err)
	}
	fmt.Printf("seeded %s: %d checklist rows upserted (%d total), %d storage locations\n", dbPath, res.ChecklistRows, total, res.Locations)

	cands, err := checklist.MatchCandidates(ctx, query, 0, "")
	if err != nil {
		return fmt.Errorf("seed.run: match: %w", err)
	}
	fmt.Printf("\nfuzzy match for %q:\n", query)
	for i, c := range cands {
		if i == 5 {
			break
		}
		fmt.Printf("  %d. %-55s score=%.3f\n", i+1, c.Entry.DisplayName(), c.Score)
	}
	if len(cands) == 0 || cands[0].Score <= 0.90 {
		return fmt.Errorf("seed.run: matcher self-check failed for %q", query)
	}
	fmt.Println("\nmatcher self-check: PASS")
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
