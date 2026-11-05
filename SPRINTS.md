# SPRINTS.md — Execution Roadmap & Atomic Task Breakdown

**Instructions for Claude Code:** Work through these sprints sequentially. Do NOT advance to the next sprint until the current sprint's **Verification Gate** passes with zero errors. Commit working code at the completion of each task.

---

## Sprint 1: Hexagonal Foundation, SQLite Persistence & FTS5 Master Checklist
**Objective:** Initialize the Go module, SQLite WAL database, 5-tier storage hierarchy, and FTS5 fuzzy checklist search engine.

- [ ] **Task 1.1: Project Scaffolding & Domain Models**
  - Initialize `go.mod` (`github.com/hootyeah/hootdex`) and directory tree (`/cmd/server`, `/cmd/cli`, `/internal/domain`, `/internal/ports`, `/internal/application`, `/internal/adapters/...`).
  - Create domain structs in `internal/domain/card.go`, `internal/domain/storage.go`, and `internal/domain/job.go` matching `@SCHEMA.md`.
- [ ] **Task 1.2: Hoot-Score & Storage Allocation Domain Logic**
  - Implement `CalculateHootScore(card *Card) float64` in `internal/domain/valuation.go` following `@OCR_DOMAIN_CONTEXT.md`.
  - Write comprehensive table-driven unit tests in `internal/domain/valuation_test.go`.
- [ ] **Task 1.3: SQLite Adapter & Migration Runner**
  - Implement `internal/adapters/outbound/sqlite/db.go` with WAL pragmas and auto-migration of `@SCHEMA.md`.
  - Implement repositories for `StorageRepository`, `JobRepository`, and `InventoryRepository`.
- [ ] **Task 1.4: FTS5 Master Checklist & Fuzzy Matcher**
  - Implement `ChecklistRepository.MatchCandidates(ctx, rawText, yearHint, categoryHint)` using SQLite FTS5 + `lithammer/fuzzysearch` Levenshtein ranking.
  - Create `/cmd/cli/seed/main.go` to seed 50 realistic test checklist items across Football (2023 Panini Prizm), Baseball (2024 Topps Chrome), Hockey (Upper Deck Young Guns), and Pokémon (1999 Base Set & Scarlet/Violet 151) plus 20 5-tier storage locations.
- **Sprint 1 Verification Gate:** Run `go test -v -race ./internal/...` and `go run ./cmd/cli/seed/main.go`. Confirm `./data/hootdex.db` is created and fuzzy matching resolves `"2023 PR1ZM #339 CJ STR0UD"` to `"2023 Panini Prizm #339 C.J. Stroud"` with score > 0.90.

---

## Sprint 2: Hot-Folder Watcher & Computer Vision Preprocessing Pipeline
**Objective:** Ingest duplex scans from the RICOH fi-8000 hot-folder, deskew/crop cards, estimate centering/corner wear, and slice OCR zones.

- [ ] **Task 2.1: Debounced Duplex File Watcher (`fsnotify`)**
  - Implement `internal/adapters/inbound/fsnotify/watcher.go` to monitor `./data/inbox/`.
  - Pair `<prefix>_front.jpg` and `<prefix>_back.jpg` (or `_001.jpg` / `_002.jpg`), verify file lock release via size-stability check, move originals to `./data/archive/<batch_id>/`, and enqueue a `PENDING` record in `ingestion_jobs`.
- [ ] **Task 2.2: Card Deskew, Cropping & Condition Estimator**
  - Implement `ImageProcessor` port in `internal/adapters/outbound/vision/preprocessor.go`.
  - Detect card outer border, crop to canonical dimensions (`750x1050`), compute left/right and top/bottom border pixel ratios (`centering_ratio_lr`, `centering_ratio_tb`), and inspect the 4 back corners to compute `corner_wear_score`.
- [ ] **Task 2.3: Zonal Slicing & Glare-Resistant Binarization**
  - Slice `FRONT_HEADER`, `FRONT_FOOTER`, `BACK_HEADER`, `BACK_FOOTER`, and `SLAB_FLIP` (if aspect ratio indicates a graded slab).
  - Apply grayscale contrast enhancement and adaptive thresholding for Tesseract consumption.
- **Sprint 2 Verification Gate:** Generate synthetic front/back test card images in a test fixture, drop them into `./data/inbox/`, and verify via `go test` that the watcher pairs them, generates cropped/binarized zones in `./data/processed/`, calculates centering ratios, and updates the SQLite job status.

---

## Sprint 3: Multi-Engine OCR / VLM Extraction & LLM Sanitization Loop
**Objective:** Build the pluggable `OCRExtractor` adapters supporting Local Tesseract, Local Ollama VLM (ROCm), and Claude API structured cleanup.

- [ ] **Task 3.1: Extraction Port & Regex Sanitization Engine**
  - Define `OCRExtractor` interface in `internal/ports/vision.go`.
  - Implement deterministic regex cleaners in `internal/domain/sanitizer.go` for copyright year extraction (`©\s*(19\d{2}|20\d{2})`), serial number parsing (`\b(\d{1,4})\s*/\s*(\d{1,4})\b`), and OCR character de-confusion (`#I4Z/Z0O` $\rightarrow$ `142/200`).
- [ ] **Task 3.2: Local Tesseract + Checklist RAG Adapter**
  - Implement `TesseractExtractor` executing OCR on the sliced zones from Sprint 2, querying `ChecklistRepository.MatchCandidates`, and assembling a `CardExtractionPayload`.
- [ ] **Task 3.3: Local Ollama VLM & Cloud Claude Fallback Adapter**
  - Implement `OllamaVLMExtractor` (`http://localhost:11434/api/generate`) and `ClaudeAPIExtractor` using the system prompt from `@OCR_DOMAIN_CONTEXT.md` and strict JSON validation.
  - Implement the fallback router: run local extraction first; if `confidence_score < 0.85` and `ENABLE_LLM_CLEANUP=true`, pass raw OCR text + top 15 SQLite checklist candidates to the LLM to resolve identity.
- [ ] **Task 3.4: Bounded Worker Pool Orchestrator**
  - Build `internal/application/pipeline.go` with configurable concurrency (default 2 workers) polling `ingestion_jobs`, executing preprocessing $\rightarrow$ extraction $\rightarrow$ checklist matching $\rightarrow$ Hoot-Score calculation $\rightarrow$ auto-assigning the next open 5-tier `storage_location_id`.
  - Route cards with `confidence_score >= 0.85` to `IN_STOCK` and `< 0.85` to `NEEDS_REVIEW`.
- **Sprint 3 Verification Gate:** Run `/cmd/cli/ocrtest/main.go` against mocked/sample noisy OCR outputs and synthetic card images. Verify end-to-end persistence into `inventory_cards` with accurate SKU generation and storage slot assignment.

---

## Sprint 4: Marketplace Valuation Engine & REST/SSE API Server
**Objective:** Build the HTTP API, Server-Sent Events (SSE) real-time scanner feed, and marketplace metadata/pricing adapters.

- [ ] **Task 4.1: Marketplace Price & Listing Metadata Generator**
  - Implement `PriceService` in `internal/adapters/outbound/market/` with a rate-limited scraper/API client and deterministic fallback estimator.
  - Generate SEO-ready eBay/TCGplayer listing titles (max 80 chars, e.g., `"2023 Panini Prizm #339 C.J. Stroud Silver Prizm RC Rookie Houston Texans NM-MT"`) stored in `marketplace_meta_json`.
- [ ] **Task 4.2: REST API & SSE Real-Time Pipeline Stream**
  - Build HTTP handlers in `internal/adapters/inbound/http/`:
    - `GET /api/v1/jobs/stream` (SSE endpoint pushing live scan progress to the UI).
    - `GET /api/v1/review-queue` (List `NEEDS_REVIEW` jobs with front/back images and candidate checklist matches).
    - `POST /api/v1/review-queue/{id}/approve` (Accepts human edits, finalizes SKU, assigns 5-tier storage slot, recalculates Hoot-Score).
    - `GET /api/v1/inventory` (Filterable by sport/TCG, year, Hoot-Score, storage box, status).
    - `POST /api/v1/upload` (Manual multipart front/back image upload).
- **Sprint 4 Verification Gate:** Run integration tests against `httptest.Server` verifying all endpoints, SSE payload streaming, and transactional storage slot increments.

---

## Sprint 5: React + TypeScript Operator Workstation & Verification UI
**Objective:** Build a keyboard-driven operator UI for Hoot Yeah shop management and rapid human-in-the-loop OCR verification.

- [ ] **Task 5.1: Vite + React + Tailwind Workstation Shell**
  - Scaffold `/web` with TypeScript strict mode, Tailwind CSS, and dark-mode operator aesthetics.
  - Build live SSE scanner status bar showing queue depth, active worker status, and throughput speed.
- [ ] **Task 5.2: Split-Screen Human-in-the-Loop Review Queue**
  - Left pane: Interactive zoomable Front & Back cropped card scans with bounding-box overlays and centering/corner metrics.
  - Right pane: Auto-populated form bound to `CardExtractionPayload` highlighting low-confidence fields in amber, plus 1-click "Snap to Master Checklist Candidate" buttons and hotkeys (`Cmd/Ctrl + Enter` to Approve & Next).
- [ ] **Task 5.3: Inventory Vault Browser & 5-Tier Storage Map**
  - Build `@tanstack/react-table` inventory grid displaying SKU, Card details, Grade/Condition, Market Price, Hoot-Score badge, and exact `Vault -> Case -> Box -> Row -> Slot` badge.
  - Add 1-click CSV/JSON export formatted for eBay Bulk Seller Hub and TCGplayer Seller Portal.
- **Sprint 5 Verification Gate:** `cd web && npm run typecheck && npm run build` succeeds with zero warnings; Go server serves static build or proxies cleanly.
