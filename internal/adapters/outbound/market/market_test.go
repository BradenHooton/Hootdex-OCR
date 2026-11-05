package market

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hootyeah/hootdex/internal/domain"
)

func card() *domain.Card {
	return &domain.Card{ID: "c1", Category: domain.CategoryFootball, ReleaseYear: 2023, Manufacturer: "Panini", SetName: "Prizm",
		CardNumber: "339", PlayerOrCharacter: "C.J. Stroud", ParallelVariant: "Silver Prizm", IsRookie: true, PlayerImpactTier: 2,
		EstimatedRawCondition: domain.ConditionNMMT}
}

func TestRateLimiterSpacesCalls(t *testing.T) {
	l := NewRateLimiter(40 * time.Millisecond)
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 110*time.Millisecond {
		t.Fatalf("4 calls took %v, expected >= 120ms spacing", el)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := NewRateLimiter(time.Hour)
	_ = slow.Wait(context.Background())
	if err := slow.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestPriceChartingClient(t *testing.T) {
	var gotQ string
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotQ = r.URL.Query().Get("q")
		if r.URL.Query().Get("t") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "id": "123", "loose-price": 4200, "graded-price": 9000, "manual-only-price": 25000})
	}))
	defer srv.Close()
	c := NewPriceChartingClient("tok", srv.URL, time.Millisecond, nil)
	q, err := c.Quote(context.Background(), card())
	if err != nil || q.PriceCents != 4200 || q.Source != domain.PricePriceCharting || !strings.Contains(q.ListingURL, "product=123") {
		t.Fatalf("raw quote %+v %v", q, err)
	}
	if gotQ != "2023 Panini Prizm C.J. Stroud #339 Silver Prizm" {
		t.Fatalf("query %q", gotQ)
	}
	g := card()
	ten := 10.0
	g.IsGraded, g.GradingCompany, g.GradeValue = true, domain.GraderPSA, &ten
	if q, _ := c.Quote(context.Background(), g); q.PriceCents != 25000 {
		t.Fatalf("PSA 10 price %d", q.PriceCents)
	}
	if _, err := NewPriceChartingClient("", srv.URL, 0, nil).Quote(context.Background(), card()); err == nil {
		t.Fatal("missing token must error")
	}
	if _, err := NewPriceChartingClient("bad", srv.URL, time.Millisecond, nil).Quote(context.Background(), card()); err == nil {
		t.Fatal("401 must error")
	}
}

func TestEstimatorIsDeterministicAndOrdered(t *testing.T) {
	base := card()
	a, b := EstimateCents(base), EstimateCents(base)
	if a != b || a%25 != 0 || a <= 0 {
		t.Fatalf("estimate %d %d", a, b)
	}
	// 150 * 4 (tier 2) * 2.5 (RC) * 2 (parallel) * 1 (NM-MT) = 3000
	if a != 3000 {
		t.Fatalf("estimate %d want 3000", a)
	}
	numbered := *base
	m := 10
	numbered.SerialMax = &m
	common := &domain.Card{Category: domain.CategoryFootball, PlayerImpactTier: 5, ParallelVariant: "Base", EstimatedRawCondition: domain.ConditionPoor}
	if EstimateCents(&numbered) <= a || EstimateCents(common) >= a || EstimateCents(common) < 25 {
		t.Fatal("estimator ordering wrong")
	}
}

type failingSource struct{}

func (failingSource) Name() string { return "down" }
func (failingSource) Quote(context.Context, *domain.Card) (domain.PriceQuote, error) {
	return domain.PriceQuote{}, errors.New("down")
}

func TestPriceServiceFallsBack(t *testing.T) {
	q, err := NewPriceService(nil, failingSource{}).Quote(context.Background(), card())
	if err != nil || !q.Estimated || q.Source != domain.PriceManual || q.PriceCents != 3000 {
		t.Fatalf("fallback quote %+v %v", q, err)
	}
}
