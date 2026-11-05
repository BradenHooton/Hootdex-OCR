package ports

import (
	"context"

	"github.com/hootyeah/hootdex/internal/domain"
)

// PriceScraper returns a market price observation for a card.
// Implementations: rate-limited marketplace/API clients and a deterministic estimator.
type PriceScraper interface {
	Name() string
	Quote(ctx context.Context, card *domain.Card) (domain.PriceQuote, error)
}
