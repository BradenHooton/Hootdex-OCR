// Package market implements price lookup adapters: a rate-limited PriceCharting API client
// and a deterministic fallback estimator, composed by PriceService.
package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// ErrNoPrice means a source had no usable price for the card.
var ErrNoPrice = errors.New("no price available")

// RateLimiter spaces calls at least `interval` apart (shared across goroutines).
type RateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	now      func() time.Time
}

// NewRateLimiter allows one call per interval.
func NewRateLimiter(interval time.Duration) *RateLimiter {
	return &RateLimiter{interval: interval, now: time.Now}
}

// Wait blocks until the caller may proceed or ctx is done.
func (l *RateLimiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	now := l.now()
	at := l.next
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.interval)
	l.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// PriceChartingClient queries the PriceCharting product API (prices in pennies).
// https://www.pricecharting.com/api-documentation
type PriceChartingClient struct {
	token   string
	baseURL string
	client  *http.Client
	limiter *RateLimiter
}

var _ ports.PriceScraper = (*PriceChartingClient)(nil)

// NewPriceChartingClient builds a client limited to one request per `every` (default 1s).
func NewPriceChartingClient(token, baseURL string, every time.Duration, hc *http.Client) *PriceChartingClient {
	if baseURL == "" {
		baseURL = "https://www.pricecharting.com"
	}
	if every <= 0 {
		every = time.Second
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &PriceChartingClient{token: token, baseURL: strings.TrimRight(baseURL, "/"), client: hc, limiter: NewRateLimiter(every)}
}

// Name implements ports.PriceScraper.
func (c *PriceChartingClient) Name() string { return "pricecharting" }

// SearchQuery renders the product query: "2023 Panini Prizm C.J. Stroud #339 Silver Prizm".
func SearchQuery(card *domain.Card) string {
	q := fmt.Sprintf("%d %s %s %s #%s", card.ReleaseYear, card.Manufacturer, card.SetName, card.PlayerOrCharacter, card.CardNumber)
	if card.IsParallel() && !strings.HasPrefix(card.ParallelVariant, "Numbered") {
		q += " " + card.ParallelVariant
	}
	return q
}

type pcProduct struct {
	Status      string `json:"status"`
	ErrorMsg    string `json:"error-message"`
	ID          string `json:"id"`
	LoosePrice  int64  `json:"loose-price"`       // ungraded
	GradedPrice int64  `json:"graded-price"`      // grade 9
	Grade95     int64  `json:"box-only-price"`    // grade 9.5
	PSA10Price  int64  `json:"manual-only-price"` // PSA 10
	BGS10Price  int64  `json:"bgs-10-price"`
}

// Quote implements ports.PriceScraper.
func (c *PriceChartingClient) Quote(ctx context.Context, card *domain.Card) (domain.PriceQuote, error) {
	wrap := func(err error) error { return fmt.Errorf("market.PriceCharting [%s]: %w", card.ID, err) }
	if c.token == "" {
		return domain.PriceQuote{}, wrap(errors.New("PRICECHARTING_TOKEN not configured"))
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return domain.PriceQuote{}, wrap(err)
	}
	u := c.baseURL + "/api/product?" + url.Values{"t": {c.token}, "q": {SearchQuery(card)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return domain.PriceQuote{}, wrap(err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return domain.PriceQuote{}, wrap(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return domain.PriceQuote{}, wrap(err)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.PriceQuote{}, wrap(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	var p pcProduct
	if err := json.Unmarshal(body, &p); err != nil {
		return domain.PriceQuote{}, wrap(err)
	}
	if p.Status != "success" {
		return domain.PriceQuote{}, wrap(fmt.Errorf("%w: %s", ErrNoPrice, p.ErrorMsg))
	}
	price := p.LoosePrice
	if card.IsGraded && card.GradeValue != nil {
		switch g := *card.GradeValue; {
		case g >= 10 && card.GradingCompany == domain.GraderBGS && p.BGS10Price > 0:
			price = p.BGS10Price
		case g >= 10 && p.PSA10Price > 0:
			price = p.PSA10Price
		case g >= 9.5 && p.Grade95 > 0:
			price = p.Grade95
		case g >= 9 && p.GradedPrice > 0:
			price = p.GradedPrice
		}
	}
	if price <= 0 {
		return domain.PriceQuote{}, wrap(ErrNoPrice)
	}
	link := ""
	if p.ID != "" {
		link = c.baseURL + "/offers?product=" + url.QueryEscape(p.ID)
	}
	return domain.PriceQuote{PriceCents: price, Source: domain.PricePriceCharting, ListingURL: link, CapturedAt: time.Now().UTC()}, nil
}

// Estimator is the deterministic fallback: a transparent multiplier model over category,
// player impact tier, rookie status, scarcity, autograph/memorabilia and condition.
type Estimator struct{}

var _ ports.PriceScraper = Estimator{}

// Name implements ports.PriceScraper.
func (Estimator) Name() string { return "estimator" }

// Quote implements ports.PriceScraper. It never fails.
func (Estimator) Quote(_ context.Context, card *domain.Card) (domain.PriceQuote, error) {
	return domain.PriceQuote{PriceCents: EstimateCents(card), Source: domain.PriceManual, Estimated: true, CapturedAt: time.Now().UTC()}, nil
}

// EstimateCents computes the fallback price, rounded to the nearest 25¢ (minimum 25¢).
func EstimateCents(c *domain.Card) int64 {
	base := map[domain.Category]float64{
		domain.CategoryFootball: 150, domain.CategoryBasketball: 175, domain.CategoryBaseball: 150, domain.CategoryHockey: 125,
		domain.CategoryGolf: 100, domain.CategoryPokemon: 200, domain.CategoryYugioh: 100, domain.CategoryMiscTCG: 75,
	}[c.Category]
	if base == 0 {
		base = 100
	}
	tier := map[int]float64{1: 8, 2: 4, 3: 1.5, 4: 1, 5: 0.5}[c.PlayerImpactTier]
	if tier == 0 {
		tier = 1.5
	}
	v := base * tier
	if c.IsRookie {
		v *= 2.5
	}
	if c.IsNumbered() {
		switch m := *c.SerialMax; {
		case m == 1:
			v *= 40
		case m <= 10:
			v *= 15
		case m <= 25:
			v *= 8
		case m <= 99:
			v *= 4
		case m <= 299:
			v *= 2.5
		default:
			v *= 1.5
		}
	} else if c.IsParallel() {
		v *= 2
	}
	if c.IsAutograph {
		v *= 5
	}
	if c.IsMemorabilia {
		v *= 2
	}
	if c.Category.IsTCG() && c.ReleaseYear > 0 && c.ReleaseYear <= 2000 {
		v *= 4 // WOTC-era vintage premium
	} else if c.ReleaseYear > 0 && c.ReleaseYear < 1980 {
		v *= 3
	}
	if c.IsGraded && c.GradeValue != nil {
		switch g := *c.GradeValue; {
		case g >= 10:
			v *= 6
		case g >= 9.5:
			v *= 3.5
		case g >= 9:
			v *= 2
		case g >= 8:
			v *= 1.2
		default:
			v *= 0.8
		}
	} else {
		v *= map[domain.RawCondition]float64{domain.ConditionNMMT: 1, domain.ConditionNM: 0.8, domain.ConditionEXMT: 0.5,
			domain.ConditionVGEX: 0.3, domain.ConditionPoor: 0.15, "": 0.8}[c.EstimatedRawCondition]
	}
	cents := int64(math.Round(v/25) * 25)
	if cents < 25 {
		cents = 25
	}
	return cents
}

// PriceService tries each live source in order and falls back to the estimator.
type PriceService struct {
	sources  []ports.PriceScraper
	fallback ports.PriceScraper
	log      *slog.Logger
}

var _ ports.PriceScraper = (*PriceService)(nil)

// NewPriceService composes sources (may be empty) with the deterministic fallback.
func NewPriceService(log *slog.Logger, sources ...ports.PriceScraper) *PriceService {
	if log == nil {
		log = slog.Default()
	}
	return &PriceService{sources: sources, fallback: Estimator{}, log: log}
}

// Name implements ports.PriceScraper.
func (s *PriceService) Name() string { return "price-service" }

// Quote implements ports.PriceScraper.
func (s *PriceService) Quote(ctx context.Context, card *domain.Card) (domain.PriceQuote, error) {
	for _, src := range s.sources {
		q, err := src.Quote(ctx, card)
		if err == nil && q.PriceCents > 0 {
			return q, nil
		}
		if ctx.Err() != nil {
			return domain.PriceQuote{}, fmt.Errorf("market.PriceService [%s]: %w", card.ID, ctx.Err())
		}
		s.log.DebugContext(ctx, "price source miss", "source", src.Name(), "card_id", card.ID, "err", err)
	}
	return s.fallback.Quote(ctx, card)
}
