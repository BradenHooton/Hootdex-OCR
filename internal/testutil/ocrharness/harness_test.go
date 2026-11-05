package ocrharness

import (
	"context"
	"io"
	"testing"
)

// TestSprint3Gate runs the end-to-end OCR pipeline with 2 workers and verifies persistence
// into inventory_cards with exact SKUs, storage slot assignment and review routing.
func TestSprint3Gate(t *testing.T) {
	rep, err := Run(context.Background(), t.TempDir(), 2, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + rep.Format())
	for _, f := range rep.Failures() {
		t.Error(f)
	}
	slots := map[string]bool{}
	for _, o := range rep.Outcomes {
		if o.Record != nil {
			if o.Record.Card.HootScore <= 0 || o.Record.Card.MarketPriceCents <= 0 || o.Record.Card.EstimatedRawCondition == "" || o.Record.Card.CornerWearScore == nil {
				t.Errorf("%s: valuation/condition not populated: %+v", o.Scenario.Name, o.Record.Card)
			}
			slots[o.Record.Location.ID] = true
		}
	}
	if len(slots) == 0 {
		t.Fatal("no storage slots assigned")
	}
}
