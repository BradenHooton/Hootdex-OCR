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
	"path/filepath"
	"strconv"
	"strings"
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

// --- configuration ---------------------------------------------------------

// Config is the server configuration, read from environment variables.
type Config struct {
	DataDir          string
	DBPath           string
	HTTPAddr         string
	WebDir           string
	Workers          int
	PollInterval     time.Duration
	EnableLLMCleanup bool
	EnableOllama     bool
	OllamaURL        string
	OllamaModel      string
	AnthropicAPIKey  string
	ClaudeModel      string
	ClaudeFallbacks  bool
	TesseractBin     string
	TesseractLang    string
	PriceChartingKey string
	MarkupPct        float64
	LogLevel         slog.Level
}

func (c Config) InboxDir() string     { return filepath.Join(c.DataDir, "inbox") }
func (c Config) ArchiveDir() string   { return filepath.Join(c.DataDir, "archive") }
func (c Config) ProcessedDir() string { return filepath.Join(c.DataDir, "processed") }

// LoadConfig reads the environment (getenv is injectable for tests).
func LoadConfig(getenv func(string) string) (Config, error) {
	str := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	var errs []string
	boolean := func(k string, def bool) bool {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q is not a boolean", k, v))
		}
		return b
	}
	integer := func(k string, def int) int {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q is not an integer", k, v))
		}
		return n
	}
	c := Config{
		DataDir:          str("HOOTDEX_DATA_DIR", "./data"),
		HTTPAddr:         str("HTTP_ADDR", ":8080"),
		WebDir:           str("WEB_DIR", "./web/dist"),
		Workers:          integer("OCR_WORKERS", 2),
		PollInterval:     time.Duration(integer("POLL_INTERVAL_MS", 1000)) * time.Millisecond,
		EnableLLMCleanup: boolean("ENABLE_LLM_CLEANUP", false),
		EnableOllama:     boolean("ENABLE_OLLAMA", true),
		OllamaURL:        str("OLLAMA_URL", "http://localhost:11434"),
		OllamaModel:      str("OLLAMA_MODEL", ""),
		AnthropicAPIKey:  getenv("ANTHROPIC_API_KEY"),
		ClaudeModel:      str("CLAUDE_MODEL", ""),
		ClaudeFallbacks:  boolean("CLAUDE_FALLBACKS", true),
		TesseractBin:     str("TESSERACT_BIN", "tesseract"),
		TesseractLang:    str("TESSERACT_LANG", "eng"),
		PriceChartingKey: getenv("PRICECHARTING_TOKEN"),
		LogLevel:         slog.LevelInfo,
	}
	c.DBPath = str("HOOTDEX_DB", filepath.Join(c.DataDir, "hootdex.db"))
	if v := str("ASKING_MARKUP_PCT", ""); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			errs = append(errs, fmt.Sprintf("ASKING_MARKUP_PCT=%q is not a number", v))
		}
		c.MarkupPct = f
	}
	if err := c.LogLevel.UnmarshalText([]byte(str("LOG_LEVEL", "INFO"))); err != nil {
		errs = append(errs, err.Error())
	}
	if c.Workers < 1 || c.Workers > 8 {
		errs = append(errs, fmt.Sprintf("OCR_WORKERS=%d must be 1..8 (2 protects the 8GB VRAM)", c.Workers))
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("config: %s", strings.Join(errs, "; "))
	}
	return c, nil
}
