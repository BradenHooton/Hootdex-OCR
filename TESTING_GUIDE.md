# TESTING_GUIDE.md — Testing Hootdex as a User

This guide walks you through trying Hootdex the way the shop will use it: scanning cards, reviewing
uncertain ones, and browsing inventory. No programming knowledge is needed beyond copying commands
into a terminal.

There are two paths:

- **Part A – 10-minute demo (start here).** Loads sample cards so you can explore every screen.
  No scanner or OCR software needed.
- **Part B – Real cards.** Install Tesseract (the OCR engine), then scan or upload your own cards.

Each test has checkboxes so you can track what you've tried. Part F explains what to send when
something looks wrong.

> All commands are run from the project folder (`Hootdex-OCR`). On Windows, use the PowerShell
> variants where shown.

---

## Before you start

You need:

| Tool | Check it's installed | Get it |
|---|---|---|
| Go 1.23 or newer | `go version` | https://go.dev/dl/ |
| Node.js 20 or newer | `node --version` | https://nodejs.org/ |
| Tesseract (Part B only) | `tesseract --version` | Linux: `sudo apt install tesseract-ocr` · macOS: `brew install tesseract` · Windows: the UB-Mannheim installer, then add it to `PATH` |

Build the operator screens once (repeat after pulling new code):

```bash
cd web
npm install
npm run build
cd ..
```

---

## Part A — 10-minute demo with sample cards

### A1. Create the sample data

This renders five synthetic card scans with recorded OCR text and runs them through the full
pipeline. Three cards get stocked automatically; two are left for you to review.

```bash
go run ./cmd/cli/ocrtest/main.go -data ./data-demo
```

You should see a table ending in `all OCR scenarios: PASS`.

> Use a fresh folder each time. If `./data-demo` already exists, delete it first, or the run reports
> failures because the SKUs have already been used.

### A2. Start Hootdex on the sample data

macOS / Linux:

```bash
HOOTDEX_DATA_DIR=./data-demo go run ./cmd/server/main.go
```

Windows (PowerShell):

```powershell
$env:HOOTDEX_DATA_DIR="./data-demo"; go run ./cmd/server/main.go
```

Leave this terminal open, then open **http://localhost:8080** in your browser. Stop the server with
`Ctrl+C`.

### A3. Explore

**Status bar (top of every screen)**

- [ ] A green dot and "Live" show next to the queue counts.
- [ ] "0/2 workers busy" is shown (two OCR workers, idle).
- [ ] The Review tab has an amber **2** badge.

**Review queue** (`Alt+1`)

- [ ] The card image shows on the left with dashed blue boxes (the OCR zones) and amber lines
      (the measured print borders used for centering).
- [ ] `O` toggles the zone overlay. `F` / `B` switch front and back (back is greyed out for the
      front-only Charizard). `+` / `-` zoom, `0` resets. Drag the image to pan when zoomed in.
- [ ] Below the image: format, centering L/R and T/B, condition estimate, corner whitening.
- [ ] On the right: "Card 1 of 2", the OCR confidence in amber, and why the card is here.
- [ ] The "Checklist matches" list shows candidates with match-score bars. Press `1` (with the cursor
      outside a text box) to snap the form to the first match; the row highlights.
- [ ] Fields the OCR was unsure about are outlined in amber with "check this". Editing a field
      clears its amber.
- [ ] `J` / `K` move between cards.
- [ ] Type in the checklist search box (e.g. `Charizard 4/102`) and press Enter to search manually.
- [ ] Approve the Charizard with **Ctrl+Enter** (⌘+Enter on Mac). A green "Approved HY-PK-…"
      message appears and the next card loads.
- [ ] The second card (illegible scan) has almost nothing filled in. Try approving it as-is: you
      should get a red error asking for the missing fields. Then reject it with **Ctrl+Backspace**
      (⌘+Backspace). The queue should now say "Nothing to review".

**Inventory** (`Alt+2`)

- [ ] Four cards are listed (three auto-stocked plus your approved Charizard), sorted by Hoot score.
- [ ] Each row shows the SKU, card details, badges (RC, serial, parallel), grade/condition, market
      and asking price, Hoot score and a storage badge (Vault › Case › Box › Row › Slot). Hover the
      storage badge to see the full location.
- [ ] Prices marked with `*` are estimates (no live price source is configured).
- [ ] Filters work: search "stroud", category, year, minimum Hoot, box, status.
- [ ] Click a column header to sort.
- [ ] **eBay CSV**, **TCGplayer CSV** and **JSON** each download a file. Open the CSVs in a
      spreadsheet and check the titles, prices and SKUs look right.

**Upload** (`Alt+3`)

- [ ] The scanner feed on the right lists the events from your review session.

When you're done, stop the server (`Ctrl+C`) and delete `./data-demo` if you like.

---

## Part B — Testing with real cards

### B1. One-time setup

```bash
go run ./cmd/cli/seed/main.go
```

This creates `./data/hootdex.db` with the reference checklist (50 cards) and 20 storage slots, and
ends with `matcher self-check: PASS`.

> **Know the limits of the reference checklist.** It covers 2023 Panini Prizm football, 2024 Topps
> Chrome baseball, 2023-24 Upper Deck Young Guns, 1999 Pokémon Base Set and Scarlet & Violet 151.
> Several card numbers in it are placeholders. Cards outside it, or with the wrong number, will
> land in review. That's expected and is itself a useful test of the review flow.

### B2. Start Hootdex

```bash
go run ./cmd/server/main.go
```

Open http://localhost:8080. Hootdex creates these folders:

| Folder | What goes there |
|---|---|
| `data/inbox/` | Drop scans here. Hootdex picks them up automatically. |
| `data/archive/<batch>/` | Original scans, moved here once picked up. |
| `data/processed/<job>/` | Cropped faces and the cleaned-up OCR zone images. |

### B3. Scan through the hot folder

Name each card's files as a pair:

```
<batch>_<number>_front.jpg
<batch>_<number>_back.jpg
```

For example `B1_0001_front.jpg` and `B1_0001_back.jpg`. `_001.jpg` / `_002.jpg` also work, and so do
`.png` and `.tif`. Files with other names are paired by arrival order (within 1.5 seconds).

**With the RICOH fi-8000:** set the scan destination to `data/inbox`, scan duplex at 300–600 DPI,
and set the file naming to produce front/back pairs like the above.

**Without a scanner:** photograph a card front and back on a plain dark background, square to the
camera, and copy the two photos into `data/inbox` with the names above.

Tests:

- [ ] The status bar "in queue" count rises, then the workers light up.
- [ ] Within a few seconds the files disappear from `inbox` and appear under `archive/<batch>/`.
- [ ] High-confidence cards show up in **Inventory** with a SKU and storage slot; uncertain ones
      appear in **Review**.
- [ ] Drop a single front with no back. After about 10 seconds it's processed on its own.
- [ ] Drop 10–20 pairs at once. Only two are worked on at a time ("2/2 workers busy"), and
      cards-per-minute updates.

### B4. Upload by hand

- [ ] On the **Upload** tab, choose a front (and optionally a back) and click **Upload scan**. You
      get "Uploaded. Job … is in the queue." and the card appears in Review or Inventory shortly after.
- [ ] Try uploading a non-image file (e.g. a `.txt` renamed to `.jpg`). It should be refused.

### B5. Review real cards

Repeat the review checks from Part A on your own cards, plus:

- [ ] Pick a specific slot from **Storage slot** instead of "Next open slot" before approving. The
      card's storage badge in Inventory matches your choice.
- [ ] Set **Player impact** and **Paid ($)**, approve, and confirm the Hoot score reflects the tier.
- [ ] For a graded slab, tick **Graded slab** and fill in grader, grade and cert number.

### B6. Crash safety

- [ ] Drop a batch of 10+ pairs, and stop the server with `Ctrl+C` while workers are busy.
- [ ] Start it again. The interrupted cards are picked back up, and none are lost or duplicated.
      Check that the inventory count plus the review count matches what you dropped in.

### B7. Optional: LLM cleanup

When local OCR is below 85% confidence, Hootdex can ask an AI model to resolve the card. Both
options are off unless configured.

- **Local (Ollama):** install Ollama, run `ollama pull qwen2.5vl:3b`, then start Hootdex with
  `ENABLE_LLM_CLEANUP=true`.
- **Cloud (Claude):** also set `ANTHROPIC_API_KEY=<your key>`. This sends card images and OCR text
  to Anthropic and costs money per card.

```bash
ENABLE_LLM_CLEANUP=true ANTHROPIC_API_KEY=sk-ant-... go run ./cmd/server/main.go
```

- [ ] The review queue gets shorter for the same set of cards compared with running without cleanup.
- [ ] Spot-check auto-stocked cards for wrong identities. Any wrong card that skipped review is
      the most important thing to report.

### B8. Optional: live prices

Set `PRICECHARTING_TOKEN=<your token>` (requires a PriceCharting subscription). Market prices
then lose the `*` estimate marker.

---

## What to expect today (not bugs)

These are known limitations, tracked in `ROADMAP.md`:

- **Without Tesseract, every real scan fails.** It is retried three times, then counted as
  "failed" in the status bar. There's no screen for failed scans yet; their originals are in
  `data/archive/`.
- **Cards can't be edited, moved or marked sold after approval**, and selling a card doesn't free
  its storage slot.
- **Prices are estimates** unless a PriceCharting token is set.
- **No login.** Anyone who can reach port 8080 can use it, so keep it on the shop PC or a trusted
  network.
- **Detection of parallels** (refractors, colour Prizms) is basic; expect to correct these in review.

---

## Troubleshooting

| What you see | What to do |
|---|---|
| Browser shows a 404 page or "web UI build not found" in the terminal | Run the `npm run build` step in "Before you start", from the `web` folder |
| `address already in use` on start | Another copy is running. Stop it, or start with `HTTP_ADDR=:8081` |
| Status bar shows a red dot and "Reconnecting" | The server stopped or restarted. Check the terminal for errors |
| Files stay in `data/inbox` | Check the extension (`.jpg`, `.jpeg`, `.png`, `.tif`); hidden files starting with `.` are ignored |
| Every card fails | Tesseract isn't installed or isn't on `PATH`. Run `tesseract --version`, or set `TESSERACT_BIN` to its full path |
| "master checklist is empty" warning | Run `go run ./cmd/cli/seed/main.go` |
| Everything lands in review | Normal for cards not in the reference checklist (see B1) |
| More detail needed | Start with `LOG_LEVEL=DEBUG` |

To start over, stop the server and delete the `data` folder, then run the seed step again.
**This deletes all inventory**, so back up `data/` first if it holds real stock.

---

## Part F — Reporting a problem

Include:

1. **What you did** (steps), **what you expected**, and **what happened**.
2. The card's **SKU** or the job ID (shown in the Upload tab's scanner feed).
3. The **scan files** from `data/archive/<batch>/`, and if it's an OCR problem, the zone images
   from `data/processed/<job>/`.
4. The **terminal output** around the time of the problem (with `LOG_LEVEL=DEBUG` if you can
   reproduce it).
5. A **screenshot** if it's a display problem.

The most valuable reports are cards that were **auto-stocked with the wrong identity**, followed by
cards that took a long time to correct in review.
