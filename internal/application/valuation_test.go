package application_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hootyeah/hootdex/internal/adapters/outbound/market"
	"github.com/hootyeah/hootdex/internal/application"
	"github.com/hootyeah/hootdex/internal/domain"
)

func TestValuationServiceSetsPriceAndListingMeta(t *testing.T) {
	c := domain.NewCardFromPayload(result(domain.ExtractorTesseract, 0.9, "C.J. Stroud").Payload)
	c.ID, c.IsRookie, c.ParallelVariant, c.PlayerImpactTier, c.EstimatedRawCondition = "c1", true, "Silver Prizm", 2, domain.ConditionNMMT
	if err := application.NewValuationService(market.NewPriceService(nil), 0).Value(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if c.MarketPriceCents != 3000 || c.AskingPriceCents != 3399 || c.PendingPriceQuote == nil || !c.PendingPriceQuote.Estimated {
		t.Fatalf("pricing %+v", c)
	}
	var m domain.MarketplaceMeta
	if err := json.Unmarshal([]byte(c.MarketplaceMetaJSON), &m); err != nil {
		t.Fatal(err)
	}
	if m.EbayTitle == "" || len(m.EbayTitle) > 80 || m.EbayCategoryID != "261328" || m.PriceSource != domain.PriceManual {
		t.Fatalf("meta %+v", m)
	}
}
