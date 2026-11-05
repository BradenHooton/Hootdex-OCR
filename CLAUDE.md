# CLAUDE.md — Hootdex-OCR Core System Constitution

## 1. Project Overview & Domain Intent
You are building **Hootdex**, a high-throughput trading card inventory, OCR extraction, condition estimation, and shop management platform for **Hoot Yeah**. 
The primary workflow ingests 300–600 DPI duplex scans (front and back) from a **RICOH fi-8000** high-speed scanner into a watched hot-folder, preprocesses the images using computer vision, extracts structured card attributes via an interchangeable OCR/Vision-Language Model (VLM) pipeline, resolves identity against a local SQLite master checklist using fuzzy string matching, calculates the custom **Hoot-Score**, assigns a 5-tier physical storage location, and routes low-confidence scans to a React human-in-the-loop verification queue.

Read and strictly adhere to these companion root specifications before writing any code:
- `@SCHEMA.md`: Authoritative SQLite DDL, FTS5 virtual tables, and JSON contracts.
- `@OCR_DOMAIN_CONTEXT.md`: Trading card anatomy, OCR preprocessing rules, and runtime LLM/VLM system prompts.
- `@SPRINTS.md`: Sequential execution roadmap and atomic engineering tasks.

---

## 2. Hardware & Environment Constraints
- **Scanner Ingestion:** RICOH fi-8000 duplex scanner dropping paired images (`<batch>_<seq>_front.jpg` and `<batch>_<seq>_back.jpg`) into `./data/inbox/`.
- **Local Compute Node (Linux/Windows PC):** AMD Ryzen 5 3600 CPU, **AMD Radeon RX 5700 XT (8GB VRAM)** running local models via **ROCm / OpenCL** (Ollama), 32GB System RAM. **NEVER** write CUDA-dependent code (`torch.cuda`, `bitsandbytes` CUDA-only bindings) or attempt to load models requiring >6.5GB VRAM.
- **Secondary Development Node:** Apple MacBook Pro 16" (M3 Pro, 36GB Unified Memory). Ensure all Go/CGO and Docker configurations support both `linux/amd64` and `darwin/arm64`.
- **Database:** Local **SQLite 3** (`./data/hootdex.db`) configured with `PRAGMA journal_mode=WAL;`, `PRAGMA synchronous=NORMAL;`, `PRAGMA busy_timeout=5000;`, and `PRAGMA foreign_keys=ON;`.

---

## 3. Tech Stack & Approved Dependencies
- **Backend Language:** Go 1.23+
- **HTTP / WebSocket Router:** Standard library `net/http` (Go 1.22+ enhanced routing) or `github.com/go-chi/chi/v5`.
- **Database Driver:** `github.com/mattn/go-sqlite3` (compiled with `--tags "fts5"`) or `modernc.org/sqlite`.
- **File Watcher:** `github.com/fsnotify/fsnotify` with file-lock/debounce verification (wait until file size stabilizes for 250ms before reading).
- **Computer Vision / Image Processing:**
  - Pure Go baseline: `github.com/disintegration/imaging` + `image/color` for cropping, rotation, and contrast adjustments.
  - Advanced CV (via CGO/Docker): `gocv.io/x/gocv` (OpenCV 4) for Canny edge detection, contour perspective warp, and CLAHE binarization.
- **OCR & Vision Providers (Behind Hexagonal Port):**
  - Local Tesseract: `github.com/otiai10/gosseract/v2` (CGO) or CLI wrapper over `tesseract` binary.
  - Local VLM: HTTP client targeting local Ollama (`http://localhost:11434/api/generate`) with structured JSON format enforcement.
  - Cloud LLM Fallback: Anthropic Messages API (`claude-3-5-sonnet-latest`) or OpenAI-compatible structured output endpoint.
- **Fuzzy String Matching:** `github.com/lithammer/fuzzysearch/fuzzy` and Levenshtein distance calculation.
- **Frontend:** React 18+, TypeScript 5+ (strict mode), Vite, Tailwind CSS, TanStack Query v5, `@tanstack/react-table`, Lucide React icons.

---

## 4. Architectural Rules (Hexagonal Architecture / Ports & Adapters)
Enforce strict separation between core domain logic and external infrastructure:
```text
/cmd
  /server          # Main entrypoint, dependency injection, graceful shutdown
  /cli             # Seed scripts, bulk import tools, manual OCR test runner
/internal
  /domain          # Pure Go structs, value objects, Hoot-Score math, zero external imports
  /ports           # Interfaces ONLY (CardRepository, OCRExtractor, PriceScraper, ImageProcessor)
  /application     # Use-case orchestrators (IngestionPipeline, ReviewService, InventoryService)
  /adapters
    /inbound
      /http        # REST handlers, SSE/WebSocket progress broadcasters, middleware
      /fsnotify    # Hot-folder scanner daemon watching RICOH fi-8000 output
    /outbound
      /sqlite      # SQLite repositories, FTS5 checklist matcher, migrations
      /vision      # OpenCV/Pure-Go image preprocessors, Tesseract adapter, Ollama VLM adapter, Claude API adapter
      /market      # TCGplayer / eBay / SportsCardPro price scrapers with rate limiting
/web               # React + TypeScript + Vite + Tailwind frontend
```

Mandatory Engineering Standards
Context Propagation: Every I/O, database, and OCR/LLM method signature MUST accept ctx context.Context as its first parameter.

Explicit Error Wrapping: Never swallow errors. Always wrap with context: fmt.Errorf("vision.PreprocessCard [%s]: %w", pairID, err).

No Global Mutable State: Pass all dependencies explicitly via constructor functions (NewIngestionService(...)).

Concurrency & Backpressure: Use a bounded worker pool (default 2 concurrent OCR workers to protect the 8GB VRAM on the RX 5700 XT) backed by a persistent SQLite ingestion_jobs queue so crashes never drop scanned cards.

Test-Driven Verification: Every domain service and adapter must have unit tests (_test.go). Never mock SQLite in repository tests—use an in-memory or temporary file SQLite instance initialized with @SCHEMA.md.

5. Development Commands
Run Backend Tests: go test -v -race ./...

Run Single Package Test: go test -v ./internal/application/...

Start Dev Backend: go run ./cmd/server/main.go

Frontend Typecheck & Build: cd web && npm run typecheck && npm run build
