// Command server runs Hootdex: the RICOH hot-folder watcher, the bounded OCR worker
// pool, and the REST/SSE API (which also serves the built operator UI).
//
//	go run ./cmd/server/main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	fswatch "github.com/hootyeah/hootdex/internal/adapters/inbound/fsnotify"
	httpapi "github.com/hootyeah/hootdex/internal/adapters/inbound/http"
	"github.com/hootyeah/hootdex/internal/adapters/outbound/market"
	"github.com/hootyeah/hootdex/internal/adapters/outbound/sqlite"
	"github.com/hootyeah/hootdex/internal/adapters/outbound/vision"
	"github.com/hootyeah/hootdex/internal/application"
	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/platform/uuid"
	"github.com/hootyeah/hootdex/internal/ports"
)

func main() {
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, log); err != nil {
		log.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// wakingIntake nudges the worker pool whenever a scan is enqueued.
type wakingIntake struct {
	ports.ScanIntake
	wake func()
}

func (w wakingIntake) EnqueueScan(ctx context.Context, p domain.ScanPair) (*domain.IngestionJob, error) {
	j, err := w.ScanIntake.EnqueueScan(ctx, p)
	if err == nil {
		w.wake()
	}
	return j, err
}

func run(ctx context.Context, cfg Config, log *slog.Logger) error {
	db, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("server.run: %w", err)
	}
	defer db.Close()
	jobs, inventory := sqlite.NewJobRepository(db), sqlite.NewInventoryRepository(db)
	storage, checklist := sqlite.NewStorageRepository(db), sqlite.NewChecklistRepository(db)
	if n, err := checklist.Count(ctx); err == nil && n == 0 {
		log.Warn("master checklist is empty; run `go run ./cmd/cli/seed/main.go` to load reference data")
	}

	broker := httpapi.NewBroker(nil, log)

	// Price lookups: PriceCharting when a token is configured, deterministic estimate otherwise.
	var sources []ports.PriceScraper
	if cfg.PriceChartingKey != "" {
		sources = append(sources, market.NewPriceChartingClient(cfg.PriceChartingKey, "", time.Second, nil))
	}
	valuer := application.NewValuationService(market.NewPriceService(log, sources...), cfg.MarkupPct)

	// Extraction: local Tesseract first; optional LLM cleanup chain (Ollama VLM, then Claude).
	local := vision.NewTesseractExtractor(&vision.TesseractCLI{Bin: cfg.TesseractBin, Lang: cfg.TesseractLang}, checklist, log)
	var cleanup []ports.OCRExtractor
	if cfg.EnableLLMCleanup {
		if cfg.EnableOllama {
			cleanup = append(cleanup, vision.NewOllamaVLMExtractor(vision.OllamaConfig{BaseURL: cfg.OllamaURL, Model: cfg.OllamaModel, SendImages: true}))
		}
		if cfg.AnthropicAPIKey != "" {
			cleanup = append(cleanup, vision.NewClaudeAPIExtractor(vision.ClaudeConfig{APIKey: cfg.AnthropicAPIKey, Model: cfg.ClaudeModel,
				Fallbacks: cfg.ClaudeFallbacks, SendImages: true}))
		}
		log.Info("LLM cleanup enabled", "engines", len(cleanup))
	}

	pipe := application.NewPipeline(application.PipelineDeps{
		Jobs: jobs, Inventory: inventory, Valuer: valuer, Events: broker, NewID: uuid.New, Log: log,
		Preprocess: application.NewPreprocessService(jobs, vision.NewPreprocessor(log), cfg.ProcessedDir(), broker, log),
		Extractor:  application.NewExtractionRouter(local, cfg.EnableLLMCleanup, log, cleanup...),
	}, application.PipelineConfig{Workers: cfg.Workers, PollInterval: cfg.PollInterval})

	invSvc := application.NewInventoryService(inventory, storage, checklist, jobs)
	broker.SetSnapshot(httpapi.SnapshotEvents(invSvc, pipe.Stats))
	intake := wakingIntake{ScanIntake: application.NewIngestionService(jobs, broker, uuid.New, log), wake: pipe.Wake}

	watcher, err := fswatch.New(fswatch.Config{InboxDir: cfg.InboxDir(), ArchiveDir: cfg.ArchiveDir(), Logger: log}, intake)
	if err != nil {
		return fmt.Errorf("server.run: %w", err)
	}
	webDir := ""
	if fi, err := os.Stat(cfg.WebDir); err == nil && fi.IsDir() {
		webDir = cfg.WebDir
	} else {
		log.Warn("web UI build not found; API only (run `cd web && npm run build`)", "dir", cfg.WebDir)
	}
	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewHandler(httpapi.Deps{
			Review:    application.NewReviewService(jobs, inventory, checklist, valuer, broker, uuid.New, log),
			Inventory: invSvc, Intake: intake, Broker: broker, Stats: pipe.Stats, Wake: pipe.Wake,
			UploadDir: cfg.ArchiveDir(), WebDir: webDir, NewID: uuid.New, Log: log,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 3)
	var wg sync.WaitGroup
	start := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				errCh <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	start("pipeline", func() error { return pipe.Run(runCtx) })
	start("watcher", func() error { return watcher.Run(runCtx) })
	start("http", func() error {
		log.Info("http listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case runErr = <-errCh:
	}
	// Graceful shutdown: stop accepting HTTP, let in-flight OCR jobs finish or requeue.
	shutCtx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	cancel()
	wg.Wait()
	log.Info("shutdown complete")
	return runErr
}
