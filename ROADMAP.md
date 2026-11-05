# ROADMAP.md — Hootdex After Sprint 5

Sprints 1–5 (`SPRINTS.md`) delivered the core loop: scan → preprocess → OCR → checklist match →
Hoot-Score → storage slot → review UI → inventory. This roadmap covers what comes next, in phases.
Each phase builds on the one before it. Every phase has a goal and exit criteria, so it's clear when
it's done.

**Short version**

| Phase | Theme | Why it comes here |
|---|---|---|
| 0 | Make it real | The pipeline has only run on synthetic scans and placeholder checklist data |
| 1 | Accuracy loop | Every human correction should make the OCR better, measurably |
| 2 | Sell what's stocked | Inventory only earns money once it's listed and sold |
| 3 | Run the shop | Buying, grading submissions and stock audits use the same scan pipeline |
| 4 | Scale and intelligence | Only once real usage data shows the need |

More detail below.

---

## Guiding principles

These hold across every phase. Revisit them before adding anything large.

- **Local-first.** The shop PC runs everything: SQLite, Tesseract, Ollama. Cloud services (Claude,
  marketplaces, backups) are optional add-ons. The shop keeps working without internet.
- **A human approves anything uncertain.** Automation raises throughput; it never quietly guesses.
  The 0.85 review threshold, and the cap on LLM-invented identities, stay unless data says otherwise.
- **Keep the hexagonal boundaries.** New integrations (marketplaces, POS, new scanners, Postgres)
  arrive as adapters behind existing or new ports. The domain package stays standard-library only.
- **Measure before tuning.** Thresholds, weights and model choices change only against a
  ground-truth evaluation set (built in Phase 1), never by feel.
- **Every scan is durable.** The `ingestion_jobs` queue, archive folder and transactional finalize
  are what make crash-safety possible; new features must not bypass them.

---

## Phase 0 — Make it real (production readiness)

**Goal:** run a real day of RICOH scanning at the shop with confidence.

### Must do

- **Validate on the real hardware.**
  - Run a batch of 200–500 real cards (mixed sports, Pokémon and slabs) through the RICOH fi-8000.
  - Record the scanner's actual file naming, DPI, background colour and auto-crop behaviour.
  - Tune the detection thresholds in `vision/geometry.go` and the Tesseract page-segmentation mode per zone.
- **Load a real master checklist.**
  - The seeded 2023 Prizm and 2024 Topps Chrome card numbers are placeholders.
  - Add `cmd/cli/import-checklist`, which reads CSV exports (Beckett, Cardboard Connection, TCGplayer catalog dumps) through `ChecklistRepository.Upsert`.
  - Use dry-run and diff modes so a re-import never clobbers corrections.
- **Free a storage slot when a card leaves.**
  - `StorageLocation.Release()` exists but nothing calls it, so `occupied_count` only ever goes up.
  - Add an inventory status-change use case (`SOLD`, removed, moved) that decrements the old slot and increments the new one in one transaction.
- **Edit and correct inventory.**
  - Today a card is write-once after approval.
  - Add `PATCH /api/v1/inventory/{id}` (identity, condition, price, status) and `POST /api/v1/inventory/{id}/move` (storage), with an audit trail (see the next item).
- **Audit log.**
  - Add an `audit_events` table: who, what, before/after JSON, when.
  - Record approvals, rejections, edits, moves and price changes.
  - Needed for shop trust and for the Phase 1 feedback loop.
- **Retry and requeue from the UI.**
  - Failed jobs (corrupt file, OCR crash) currently stay `FAILED`.
  - Add `POST /api/v1/jobs/{id}/retry` and a "Failed scans" list.
- **Duplicate scan detection.**
  - Hash the original files (SHA-256), plus a perceptual hash of the cropped front.
  - Warn when the same physical card is scanned twice, which happens on re-feeds.
- **Basic access control.**
  - The API is unauthenticated. Fine on one PC; not on a shop LAN or tablet.
  - Add local operator accounts (PIN or password, session cookie) and record the operator in the audit log.
- **Backups.**
  - Nightly SQLite online backup (`VACUUM INTO` or Litestream) plus an archive-folder sync to an external drive or cloud bucket.
  - Write a restore runbook and test it.

### Should do

- **gocv adapter** behind `ImageProcessor`, built in a Docker image for linux/amd64 and darwin/arm64. Compare its accuracy against the pure-Go preprocessor on the Phase 0 batch, and keep the better one as default.
- **Packaging.**
  - A single Go binary that embeds `web/dist` (`go:embed`).
  - A systemd/Windows service wrapper.
  - A multi-arch Docker Compose stack: Hootdex plus Ollama with ROCm.
- **CI**: `go test -race ./...`, `go vet`, `npm run typecheck && npm run build` on every PR.

**Exit criteria**
- One full scanning day completes with no lost scans.
- The checklist covers the shop's actual stock.
- Slot occupancy matches physical reality after sales.
- Backups have been restored at least once.

---

## Phase 1 — The accuracy loop

**Goal:** make every review correction improve the system, and know exactly how good the OCR is.

- **Ground-truth capture.**
  - When an operator approves a card, store the extractor's original payload alongside the human-verified one (both are already in the job row; keep them immutable).
  - This becomes a labelled dataset at zero extra cost.
- **Evaluation set and harness.**
  - Extend `ocrharness` to replay a frozen set of real archived scans with their verified payloads.
  - Report per-field accuracy (year, number, player, parallel, serial, grade), auto-accept rate, and *false* auto-accepts. False auto-accepts are the costly error: a wrong card stocked without review.
  - Run it in CI against a small sample.
- **Confidence calibration.**
  - Use the eval set to check that "0.85" really means about 85% correct.
  - Tune the matcher weights (`sqlite/matcher.go`) and the penalties in `tesseract.go`.
  - Consider per-category thresholds; Pokémon collector numbers are far more reliable than sports backs.
- **Per-field confidence.**
  - Today confidence is one number.
  - Field-level scores would let the UI highlight exactly what to check, and let auto-accept require high confidence on the identity fields specifically.
- **Better parallel and variant detection.**
  - Foil and colour analysis on the cropped front: refractor shimmer, colour borders for Prizm parallels, holo pattern for Pokémon.
  - Detect serial-number stamp regions.
  - Parallels drive most of the price spread, so this is high value.
- **Set-symbol and logo recognition.** Small template-matching or embedding model for set logos and Pokémon set symbols, to disambiguate when the text is weak.
- **LLM cleanup evaluation.**
  - Measure Ollama versus Claude on the eval set: accuracy, latency, cost per card.
  - Decide routing by data. For example, Ollama first and Claude only on a second failure, or Claude only for high-value candidates.
- **Review UI speed metrics.** Time-per-card and corrections-per-card shown to the operator, to find which fields slow review down.

**Exit criteria**
- A published accuracy report on a real eval set of at least 1,000 cards.
- False auto-accept rate below an agreed target (e.g. < 1%).
- Review queue share trending down release over release.

---

## Phase 2 — Sell what's stocked

**Goal:** turn inventory into listings and sales with minimal manual work.

- **Real market prices.**
  - Add more price sources behind `PriceScraper`: TCGplayer Market Price API for TCG cards, and eBay sold comps where API access is available. eBay's sold-listing data has restricted API access; plan for a partner program or manual comp entry.
  - Keep the deterministic estimator as the last fallback, and always show the price source.
- **Scheduled repricing.**
  - Background job re-quotes stock older than N days and writes `price_snapshots`.
  - Flag cards whose price moved by more than X% for the operator.
- **Marketplace listing integration.**
  - Move from CSV export to API listing: the eBay Inventory/Offer APIs, and TCGplayer Seller API if the shop qualifies.
  - Track listing IDs in `marketplace_meta_json`, or in a new `listings` table once multiple channels are live.
  - Status becomes `LISTED` automatically.
- **Sale sync.**
  - Poll or receive webhooks for sold orders.
  - Mark the card `SOLD`, release its slot (from Phase 0), record the realized price, and end listings on the other channels to prevent double-selling.
- **Pick lists.**
  - For a batch of sales, generate a pick list sorted by Vault → Case → Box → Row → Slot.
  - The 5-tier hierarchy was built for exactly this.
- **Labels and barcodes.** Print slot labels (`barcode_label` already exists) and per-card SKU labels for toploaders and team bags; scan-to-find in the UI.
- **Photo set for listings.** Generate listing-ready images from the canonical crops: white background, consistent size, front and back.

**Exit criteria**
- A card can go from scan to live listing to sold to slot freed with no CSV handling.
- Realized sale prices are recorded for every sale.

---

## Phase 3 — Run the shop

**Goal:** use the same scanning engine for the rest of the shop's workflows.

- **Buylist and trade-in quoting.**
  - Scan a customer's cards at the counter (the same pipeline, in a quote-only mode that doesn't stock them).
  - Return an offer per card using price × buy percentage by Hoot-Score band.
  - On acceptance, convert the quote into stocked inventory with acquisition cost filled in.
- **Acquisition lots and cost basis.** Group cards bought together (a collection purchase) into a lot, allocate cost across the cards, and report profit per lot.
- **Grading submission tracking.**
  - Use Hoot-Score plus condition estimate to suggest grading candidates (high value, NM-MT, good centering).
  - Track submissions to PSA/BGS/SGC/CGC (sent, received, grade).
  - When the graded card returns, re-scan it so the slab flip updates the card in place.
- **Stock audits.**
  - Cycle-count a box by scanning its slot barcode and then the cards in it.
  - Report missing and unexpected cards.
- **Reporting.** Inventory value over time, aging stock, sell-through by category or set, margin by channel, best and worst lots.
- **Roles.** Owner, operator and counter staff, with permissions (pricing changes and deletions restricted).
- **In-store sales (lightweight POS).** Mark sold at the counter, with receipt and slot release. Integrate with an existing POS (e.g. Square) through an adapter rather than building payments.

**Exit criteria**
- Buying, stocking, grading and selling all run through Hootdex.
- Owner reports come from the app, not spreadsheets.

---

## Phase 4 — Scale and intelligence (only when the data justifies it)

These are long-term options. Each should be triggered by a measured need, not built speculatively.

- **Phone-camera intake.**
  - A mobile/PWA capture page that feeds the same `ScanIntake` port, for shows and buying trips away from the RICOH.
  - The perspective-warp preprocessor already handles skewed photos.
- **Fine-tuned small VLM.**
  - Once Phase 1 has collected 10k+ verified scans, fine-tune a small vision model on the shop's own data.
  - Keep it under the 6.5 GB VRAM limit and run it locally through Ollama.
  - This is the long-term path to fewer reviews and lower API cost.
- **Price prediction.** Model realized sale price from card attributes and comps, to set asking prices and flag underpriced buys. Feed it into the Hoot-Score `ValueScore`.
- **Multi-location or multi-station.**
  - If several scanning stations or a second store appear, move from SQLite to Postgres (only the `sqlite` adapter changes; ports stay) and move images to object storage.
  - Until then, SQLite in WAL mode on one PC is the right choice.
- **Customer-facing features.** Public storefront or want-list matching ("tell me when a /25 Stroud comes in"), fed from inventory and the sale-sync events.
- **Hoot-Score evolution.**
  - Re-fit the weights (0.45 / 0.25 / 0.15 / 0.15) against how quickly and how profitably cards actually sold.
  - Version the formula so historical scores stay explainable.

---

## Decisions to make along the way

| Decision | When | Inputs |
|---|---|---|
| Pure-Go vs gocv preprocessor as default | End of Phase 0 | Accuracy on the real scan batch |
| Auto-accept threshold (per category?) | Phase 1 | Calibration curve from the eval set |
| Ollama vs Claude routing and model choice | Phase 1 | Accuracy, latency, cost per card |
| Which marketplaces get API integration | Phase 2 | Sales volume by channel, API access |
| SQLite → Postgres | Only if multi-station | Concurrency and locking metrics |
| Fine-tune a local VLM | Phase 4 | Size and quality of the verified dataset |

## Deliberately out of scope (for now)

- Payments processing: integrate with an existing POS instead.
- A hosted multi-tenant SaaS version: Hootdex is built for one shop's hardware and workflow first.
- Real-time collaborative editing in the review queue: one operator per job is enough. The atomic job claim already prevents double work.
