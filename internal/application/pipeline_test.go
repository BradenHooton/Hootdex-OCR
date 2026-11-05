package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hootyeah/hootdex/internal/adapters/outbound/sqlite"
	"github.com/hootyeah/hootdex/internal/application"
	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/platform/uuid"
	"github.com/hootyeah/hootdex/internal/ports"
	"github.com/hootyeah/hootdex/internal/seed"
)

type stubProcessor struct{ err error }

func (s stubProcessor) Preprocess(_ context.Context, req ports.PreprocessRequest) (*domain.PreprocessResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	w := 0.05
	return &domain.PreprocessResult{CardType: domain.CardTypeRaw, CroppedFrontPath: req.OutDir + "/front.jpg",
		CenteringLR: "51/49", CenteringTB: "52/48", CornerWearScore: &w, EstimatedCondition: domain.ConditionNMMT}, nil
}

type fixedValuer struct{ cents int64 }

func (v fixedValuer) Value(_ context.Context, c *domain.Card) error {
	c.MarketPriceCents = v.cents
	return nil
}

type env struct {
	db        *sqlite.DB
	jobs      *sqlite.JobRepository
	inventory *sqlite.InventoryRepository
	storage   *sqlite.StorageRepository
	pub       *recordingPublisher
}

func newEnv(t *testing.T, seedStorage bool) env {
	db := openDB(t)
	e := env{db: db, jobs: sqlite.NewJobRepository(db), inventory: sqlite.NewInventoryRepository(db), storage: sqlite.NewStorageRepository(db), pub: &recordingPublisher{}}
	if seedStorage {
		if _, err := seed.Apply(context.Background(), sqlite.NewChecklistRepository(db), e.storage); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e env) pipeline(t *testing.T, proc ports.ImageProcessor, ex ports.OCRExtractor) *application.Pipeline {
	return application.NewPipeline(application.PipelineDeps{
		Jobs: e.jobs, Inventory: e.inventory, Extractor: ex, Valuer: fixedValuer{cents: 10000},
		Preprocess: application.NewPreprocessService(e.jobs, proc, t.TempDir(), e.pub, nil),
		Events:     e.pub, NewID: uuid.New,
	}, application.PipelineConfig{Workers: 2, PollInterval: 20 * time.Millisecond})
}

func (e env) enqueue(t *testing.T, n int) []string {
	var ids []string
	svc := application.NewIngestionService(e.jobs, nil, uuid.New, nil)
	for i := 0; i < n; i++ {
		j, err := svc.EnqueueScan(context.Background(), domain.ScanPair{BatchID: "B", FrontPath: "/a/f.jpg", BackPath: "/a/b.jpg"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	return ids
}

func TestPipelineStocksHighConfidenceWithValuation(t *testing.T) {
	e := newEnv(t, true)
	ids := e.enqueue(t, 3)
	ex := &stubExtractor{kind: domain.ExtractorTesseract, res: result(domain.ExtractorTesseract, 0.95, "C.J. Stroud")}
	ex.res.Payload.ParallelVariant = "Silver Prizm"
	ex.res.Payload.IsRookie = true
	p := e.pipeline(t, stubProcessor{}, ex)
	if err := p.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	cards, _ := e.inventory.List(context.Background(), domain.InventoryFilter{})
	if len(cards) != 3 {
		t.Fatalf("stocked %d cards", len(cards))
	}
	skus := map[string]bool{}
	for _, c := range cards {
		skus[c.Card.SKU] = true
		if c.Card.MarketPriceCents != 10000 || c.Card.HootScore <= 50 || c.Card.InventoryStatus != domain.StatusInStock || c.Location == nil {
			t.Fatalf("card %+v", c.Card)
		}
		if c.Card.FrontImageURL != application.JobImageURL(c.Card.IngestionJobID, "front") {
			t.Fatalf("image url %s", c.Card.FrontImageURL)
		}
	}
	if !skus["HY-FB-2023-PRZ-339-001"] || !skus["HY-FB-2023-PRZ-339-003"] {
		t.Fatalf("sku sequence %v", skus)
	}
	for _, id := range ids {
		j, _ := e.jobs.Get(context.Background(), id)
		if j.Status != domain.JobCompleted || j.ExtractedPayloadJSON == "" || j.ExtractorUsed != domain.ExtractorTesseract {
			t.Fatalf("job %+v", j)
		}
	}
	if s := p.Stats(); s.Processed != 3 || s.Workers != 2 {
		t.Fatalf("stats %+v", s)
	}
}

func TestPipelineRoutesLowConfidenceAndFullStorageToReview(t *testing.T) {
	e := newEnv(t, false) // no storage slots at all
	ids := e.enqueue(t, 2)
	low := &stubExtractor{kind: domain.ExtractorTesseract, res: result(domain.ExtractorTesseract, 0.6, "C.J. Stroud")}
	if err := e.pipeline(t, stubProcessor{}, low).Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		j, _ := e.jobs.Get(context.Background(), id)
		if j.Status != domain.JobNeedsReview {
			t.Fatalf("status %s", j.Status)
		}
	}
	ids = e.enqueue(t, 1)
	high := &stubExtractor{kind: domain.ExtractorTesseract, res: result(domain.ExtractorTesseract, 0.95, "C.J. Stroud")}
	if err := e.pipeline(t, stubProcessor{}, high).Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, _ := e.jobs.Get(context.Background(), ids[0])
	if j.Status != domain.JobNeedsReview || j.ErrorMessage == "" {
		t.Fatalf("full storage must route to review: %+v", j)
	}
}

func TestPipelineRetriesThenFails(t *testing.T) {
	e := newEnv(t, true)
	ids := e.enqueue(t, 1)
	p := e.pipeline(t, stubProcessor{err: errors.New("corrupt jpeg")}, &stubExtractor{})
	if err := p.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, _ := e.jobs.Get(context.Background(), ids[0])
	if j.Status != domain.JobFailed || j.RetryCount != domain.MaxJobRetries || j.ErrorMessage == "" {
		t.Fatalf("job %+v", j)
	}
	if s := p.Stats(); s.Failed != 1 {
		t.Fatalf("stats %+v", s)
	}
	var sawFailed bool
	for _, ty := range e.pub.types() {
		sawFailed = sawFailed || ty == domain.EventJobFailed
	}
	if !sawFailed {
		t.Fatal("no job.failed event")
	}
}

func TestPipelineRunRequeuesAndStops(t *testing.T) {
	e := newEnv(t, true)
	ids := e.enqueue(t, 1)
	if _, err := e.jobs.ClaimNextPending(context.Background()); err != nil { // simulate a crash mid-job
		t.Fatal(err)
	}
	ex := &stubExtractor{kind: domain.ExtractorTesseract, res: result(domain.ExtractorTesseract, 0.95, "C.J. Stroud")}
	p := e.pipeline(t, stubProcessor{}, ex)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j, _ := e.jobs.Get(context.Background(), ids[0]); j.Status == domain.JobCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	j, _ := e.jobs.Get(context.Background(), ids[0])
	if j.Status != domain.JobCompleted || j.RetryCount != 1 {
		t.Fatalf("crash-recovered job: %+v", j)
	}
}
