// Command ocrtest is the manual OCR test runner (Sprint 3 gate). It renders synthetic
// card scans, replays recorded noisy OCR outputs through the full pipeline (preprocess ->
// Tesseract-style extraction -> checklist match -> Hoot-Score -> storage slot) and prints
// the resulting inventory, exiting non-zero if any expectation fails.
//
//	go run ./cmd/cli/ocrtest/main.go [-data ./data/ocrtest] [-workers 2]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hootyeah/hootdex/internal/testutil/ocrharness"
)

func main() {
	data := flag.String("data", "", "data directory for the test database and images (default: a fresh temp dir)")
	workers := flag.Int("workers", 2, "concurrent pipeline workers")
	flag.Parse()
	if err := run(*data, *workers); err != nil {
		fmt.Fprintln(os.Stderr, "ocrtest:", err)
		os.Exit(1)
	}
}

func run(data string, workers int) error {
	if data == "" {
		tmp, err := os.MkdirTemp("", "hootdex-ocrtest-*")
		if err != nil {
			return fmt.Errorf("ocrtest.run: %w", err)
		}
		data = tmp
	}
	data, _ = filepath.Abs(data)
	rep, err := ocrharness.Run(context.Background(), data, workers, os.Stderr)
	if err != nil {
		return fmt.Errorf("ocrtest.run: %w", err)
	}
	fmt.Printf("database: %s\n\n%s\n", rep.DBPath, rep.Format())
	if f := rep.Failures(); len(f) > 0 {
		for _, msg := range f {
			fmt.Println("FAIL:", msg)
		}
		return fmt.Errorf("%d expectation(s) failed", len(f))
	}
	fmt.Println("all OCR scenarios: PASS")
	return nil
}
