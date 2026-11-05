# Hootdex-OCR

Trading-card inventory, OCR extraction, condition estimation and shop management for **Hoot Yeah**.
RICOH fi-8000 duplex scans land in a hot folder, get deskewed and sliced into OCR zones, are matched
against a SQLite master checklist, valued with the Hoot-Score, assigned a 5-tier storage slot, and
routed to a keyboard-driven review UI when the OCR is unsure.

See `CLAUDE.md`, `SCHEMA.md`, `OCR_DOMAIN_CONTEXT.md` and `SPRINTS.md` for the specifications.

## Quick start

```bash
go run ./cmd/cli/seed/main.go          # create ./data/hootdex.db, load checklist + storage
cd web && npm install && npm run build # build the operator UI (served by the Go server)
go run ./cmd/server/main.go            # http://localhost:8080
```

Drop `<batch>_<seq>_front.jpg` / `<batch>_<seq>_back.jpg` pairs into `./data/inbox/`.
Originals move to `./data/archive/<batch>/`, crops and binarized zones go to `./data/processed/<job>/`.

For UI development run `cd web && npm run dev` (Vite proxies `/api` to `:8080`).

## Requirements

- Go 1.23+ (pure-Go SQLite with FTS5 — no CGO or build tags needed; linux/amd64 and darwin/arm64).
- `tesseract` on `PATH` for local OCR (`apt install tesseract-ocr` / `brew install tesseract`).
- Optional: Ollama with a small VLM (default `qwen2.5vl:3b`, ~3.2 GB — fits the RX 5700 XT budget).
- Optional: `ANTHROPIC_API_KEY` for Claude cleanup of low-confidence scans.
- Node 20+ to build the UI.

## Configuration (environment)

| Variable | Default | Purpose |
|---|---|---|
| `HOOTDEX_DATA_DIR` | `./data` | inbox / archive / processed / database root |
| `HOOTDEX_DB` | `$HOOTDEX_DATA_DIR/hootdex.db` | SQLite path |
| `HTTP_ADDR` | `:8080` | API + UI listen address |
| `OCR_WORKERS` | `2` | concurrent pipeline workers (protects 8 GB VRAM) |
| `ENABLE_LLM_CLEANUP` | `false` | send < 0.85 confidence scans to the LLM chain |
| `ENABLE_OLLAMA`, `OLLAMA_URL`, `OLLAMA_MODEL` | `true`, `http://localhost:11434`, `qwen2.5vl:3b` | local VLM |
| `ANTHROPIC_API_KEY`, `CLAUDE_MODEL`, `CLAUDE_FALLBACKS` | –, `claude-opus-5-5`, `true` | cloud cleanup |
| `TESSERACT_BIN`, `TESSERACT_LANG` | `tesseract`, `eng` | local OCR |
| `PRICECHARTING_TOKEN` | – | live prices (otherwise a deterministic estimate) |
| `ASKING_MARKUP_PCT` | `10` | asking price over market |
| `WEB_DIR` | `./web/dist` | built UI to serve |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR` |

## Verification

```bash
go test -race ./...                        # all sprints' tests, incl. gate tests
go run ./cmd/cli/seed/main.go              # Sprint 1 gate (matcher self-check)
go run ./cmd/cli/ocrtest/main.go           # Sprint 3 gate (end-to-end OCR replay)
cd web && npm run typecheck && npm run build   # Sprint 5 gate
```

## Operator keys

| Keys | Action |
|---|---|
| `Alt+1/2/3` | Review / Inventory / Upload |
| `Ctrl/⌘+Enter` | Approve card and go to the next |
| `Ctrl/⌘+Backspace` | Reject the scan |
| `1`…`9` (in review, outside a text field) | Snap to checklist candidate |
| `J` / `K` | Next / previous card |
| `F` / `B`, `+` / `-` / `0`, `O` | Front/back, zoom, reset, toggle OCR zones |
