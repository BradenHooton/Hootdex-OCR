// Package ocrharness runs the full ingestion pipeline end-to-end against synthetic card
// scans and recorded noisy OCR outputs (no Tesseract/Ollama/Claude needed). It backs the
// Sprint 3 verification gate: cmd/cli/ocrtest and its Go test.
package ocrharness

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hootyeah/hootdex/internal/adapters/outbound/sqlite"
	"github.com/hootyeah/hootdex/internal/adapters/outbound/vision"
	"github.com/hootyeah/hootdex/internal/application"
	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/platform/uuid"
	"github.com/hootyeah/hootdex/internal/seed"
	"github.com/hootyeah/hootdex/internal/testutil/synthcard"
)

// Scenario is one scanned card with the noisy text Tesseract "read" from each zone.
type Scenario struct {
	Name       string
	Zones      map[domain.ZoneName]string
	FrontOnly  bool
	WantStatus domain.JobStatus
	WantSKU    string // expected SKU when stocked
}

// Scenarios are recorded noisy OCR dumps covering auto-stock and review routing.
func Scenarios() []Scenario {
	return []Scenario{
		{Name: "stroud-silver-prizm", WantStatus: domain.JobCompleted, WantSKU: "HY-FB-2023-PRZ-339-001", Zones: map[domain.ZoneName]string{
			domain.ZoneFrontHeader: "PRI2M", domain.ZoneFrontFooter: "C.J. STR0UD [RC] HOUSTON TEXANS",
			domain.ZoneBackHeader: "#339 C.J. STROUD QB PRIZM", domain.ZoneBackFooter: "©2O23 Panini America, Inc. 2022 STATS"}},
		{Name: "bryce-young-numbered", WantStatus: domain.JobCompleted, WantSKU: "HY-FB-2023-PRZ-301-001", Zones: map[domain.ZoneName]string{
			domain.ZoneFrontFooter: "BRYCE Y0UNG RC O4S/I99 PANTHERS", domain.ZoneBackHeader: "#3O1 BRYCE YOUNG",
			domain.ZoneBackFooter: "(C) 2023 PANINI AMERICA INC"}},
		{Name: "bedard-young-guns", WantStatus: domain.JobCompleted, WantSKU: "HY-HK-2023-YGN-201-001", Zones: map[domain.ZoneName]string{
			domain.ZoneFrontHeader: "UPPER DECK Y0UNG GUNS", domain.ZoneFrontFooter: "C0NN0R BEDARD BLACKHAWKS",
			domain.ZoneBackHeader: "#2O1 CONNOR BEDARD CENTER", domain.ZoneBackFooter: "© 2023 The Upper Deck Company"}},
		{Name: "charizard-base-front-only", FrontOnly: true, WantStatus: domain.JobNeedsReview, Zones: map[domain.ZoneName]string{
			domain.ZoneFrontHeader: "Charizard 120 HP", domain.ZoneFrontFooter: "illus. Mitsuhiro Arita 4/1O2"}},
		{Name: "illegible-glare", WantStatus: domain.JobNeedsReview, Zones: map[domain.ZoneName]string{
			domain.ZoneFrontFooter: "~~ #@ .. ||", domain.ZoneBackHeader: "x1", domain.ZoneBackFooter: ""}},
	}
}

// Outcome is the per-scenario result.
type Outcome struct {
	Scenario Scenario
	Job      *domain.IngestionJob
	Record   *domain.InventoryRecord // nil when not stocked
}

// Report summarises a harness run.
type Report struct {
	Outcomes []Outcome
	DBPath   string
}

// Failures lists scenario expectations that were not met.
func (r Report) Failures() []string {
	var out []string
	for _, o := range r.Outcomes {
		if o.Job.Status != o.Scenario.WantStatus {
			out = append(out, fmt.Sprintf("%s: status %s, want %s (%s)", o.Scenario.Name, o.Job.Status, o.Scenario.WantStatus, o.Job.ErrorMessage))
			continue
		}
		if o.Scenario.WantStatus == domain.JobCompleted {
			switch {
			case o.Record == nil:
				out = append(out, o.Scenario.Name+": no inventory card")
			case o.Record.Card.SKU != o.Scenario.WantSKU:
				out = append(out, fmt.Sprintf("%s: sku %s, want %s", o.Scenario.Name, o.Record.Card.SKU, o.Scenario.WantSKU))
			case o.Record.Location == nil:
				out = append(out, o.Scenario.Name+": no storage slot")
			case o.Record.Card.InventoryStatus != domain.StatusInStock:
				out = append(out, o.Scenario.Name+": inventory status "+string(o.Record.Card.InventoryStatus))
			}
		}
	}
	return out
}

// Run seeds a database under dataDir, renders synthetic scans, enqueues them, and drains
// the pipeline with the given worker count using the recorded OCR fixtures.
func Run(ctx context.Context, dataDir string, workers int, logOut io.Writer) (Report, error) {
	log := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dbPath := filepath.Join(dataDir, "hootdex.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return Report{}, err
	}
	defer db.Close()
	checklist, storage := sqlite.NewChecklistRepository(db), sqlite.NewStorageRepository(db)
	jobs, inventory := sqlite.NewJobRepository(db), sqlite.NewInventoryRepository(db)
	if _, err := seed.Apply(ctx, checklist, storage); err != nil {
		return Report{}, err
	}

	archive := filepath.Join(dataDir, "archive", "OCRTEST")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		return Report{}, fmt.Errorf("ocrharness: %w", err)
	}
	intake := application.NewIngestionService(jobs, nil, uuid.New, log)
	byJob := map[string]Scenario{}
	var order []string
	for i, sc := range Scenarios() {
		front := filepath.Join(archive, fmt.Sprintf("OCRTEST_%04d_front.jpg", i+1))
		if err := synthcard.Save(synthcard.Render(synthcard.Front()), front); err != nil {
			return Report{}, fmt.Errorf("ocrharness: %w", err)
		}
		back := ""
		if !sc.FrontOnly {
			back = filepath.Join(archive, fmt.Sprintf("OCRTEST_%04d_back.jpg", i+1))
			if err := synthcard.Save(synthcard.Render(synthcard.Back()), back); err != nil {
				return Report{}, fmt.Errorf("ocrharness: %w", err)
			}
		}
		job, err := intake.EnqueueScan(ctx, domain.ScanPair{BatchID: "OCRTEST", Prefix: fmt.Sprintf("OCRTEST_%04d", i+1), FrontPath: front, BackPath: back})
		if err != nil {
			return Report{}, err
		}
		byJob[job.ID] = sc
		order = append(order, job.ID)
	}

	// Recorded OCR: zone images live at <processed>/<job_id>/<ZONE>.png.
	rec := vision.RecognizerFunc(func(_ context.Context, path string, z domain.ZoneName) (string, error) {
		sc, ok := byJob[filepath.Base(filepath.Dir(path))]
		if !ok {
			return "", fmt.Errorf("no fixture for %s", path)
		}
		return sc.Zones[z], nil
	})
	pipe := application.NewPipeline(application.PipelineDeps{
		Jobs: jobs, Inventory: inventory,
		Preprocess: application.NewPreprocessService(jobs, vision.NewPreprocessor(log), filepath.Join(dataDir, "processed"), nil, log),
		Extractor:  application.NewExtractionRouter(vision.NewTesseractExtractor(rec, checklist, log), false, log),
		NewID:      uuid.New, Log: log,
	}, application.PipelineConfig{Workers: workers})
	if err := pipe.Drain(ctx); err != nil {
		return Report{}, err
	}

	rep := Report{DBPath: dbPath}
	cards, err := inventory.List(ctx, domain.InventoryFilter{Limit: 1000})
	if err != nil {
		return Report{}, err
	}
	byJobCard := map[string]domain.InventoryRecord{}
	for _, c := range cards {
		byJobCard[c.Card.IngestionJobID] = c
	}
	for _, id := range order {
		j, err := jobs.Get(ctx, id)
		if err != nil {
			return Report{}, err
		}
		o := Outcome{Scenario: byJob[id], Job: j}
		if rec, ok := byJobCard[id]; ok {
			r := rec
			o.Record = &r
		}
		rep.Outcomes = append(rep.Outcomes, o)
	}
	return rep, nil
}

// Format renders a human-readable table.
func (r Report) Format() string {
	var b strings.Builder
	outs := append([]Outcome(nil), r.Outcomes...)
	sort.SliceStable(outs, func(i, j int) bool { return outs[i].Scenario.Name < outs[j].Scenario.Name })
	fmt.Fprintf(&b, "%-28s %-13s %-6s %-26s %-36s %s\n", "SCENARIO", "STATUS", "CONF", "SKU", "SLOT", "HOOT")
	for _, o := range outs {
		sku, slot, hoot := "-", "-", "-"
		if o.Record != nil {
			sku, hoot = o.Record.Card.SKU, fmt.Sprintf("%.1f", o.Record.Card.HootScore)
			if o.Record.Location != nil {
				slot = o.Record.Location.ID
			}
		}
		fmt.Fprintf(&b, "%-28s %-13s %-6.2f %-26s %-36s %s\n", o.Scenario.Name, o.Job.Status, o.Job.ConfidenceScore, sku, slot, hoot)
	}
	return b.String()
}
