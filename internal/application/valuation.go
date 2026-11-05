package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// DefaultMarkupPct is the asking-price markup over market value.
const DefaultMarkupPct = 10.0

// ValuationService prices cards and generates marketplace listing metadata.
type ValuationService struct {
	prices    ports.PriceScraper
	markupPct float64
}

var _ CardValuer = (*ValuationService)(nil)

// NewValuationService constructs the service (markupPct <= 0 uses DefaultMarkupPct).
func NewValuationService(prices ports.PriceScraper, markupPct float64) *ValuationService {
	if markupPct <= 0 {
		markupPct = DefaultMarkupPct
	}
	return &ValuationService{prices: prices, markupPct: markupPct}
}

// Value sets market/asking prices, the pending price snapshot and marketplace_meta_json.
// Listing metadata is generated even when pricing fails.
func (s *ValuationService) Value(ctx context.Context, card *domain.Card) error {
	var quote *domain.PriceQuote
	var priceErr error
	if q, err := s.prices.Quote(ctx, card); err != nil {
		priceErr = fmt.Errorf("application.Value [%s]: %w", card.ID, err)
	} else {
		quote = &q
		card.MarketPriceCents = q.PriceCents
		card.PendingPriceQuote = quote
		if card.AskingPriceCents == 0 {
			card.AskingPriceCents = domain.AskingPrice(q.PriceCents, s.markupPct)
		}
	}
	meta, err := json.Marshal(domain.BuildMarketplaceMeta(card, quote))
	if err != nil {
		return fmt.Errorf("application.Value [%s]: %w", card.ID, err)
	}
	card.MarketplaceMetaJSON = string(meta)
	return priceErr
}
