
# OCR_DOMAIN_CONTEXT.md — Trading Card Vision, Extraction & Valuation Rules

## 1. Physical Scanner & Image Pairing Rules (RICOH fi-8000)
1. **Duplex Naming Convention:** The scanner outputs pairs sequentially. The file watcher must group files by prefix or timestamp window (within 1500ms) into a `ScanPair{FrontPath, BackPath}`.
2. **Standard Dimensions:** Standard Sports/Magic/Pokémon cards measure `2.5" x 3.5"` (aspect ratio ~`0.714`). Yu-Gi-Oh! Japanese-size cards measure `2.32" x 3.38"`. Graded slabs (PSA/BGS/SGC) measure approximately `3.25" x 5.3"`.
3. **Aspect Ratio Classification:** During OpenCV contour cropping, compute `width / height` of the largest bounding quadrilateral:
   - If `height / width > 1.50`: Classify as `GRADED_SLAB` and run a secondary crop on the top 22% of the front image to isolate the grading label (Flip) for high-priority Tesseract OCR.
   - Otherwise: Classify as `RAW_CARD`.

---

## 2. OpenCV / Image Preprocessing Pipeline
Raw trading card scans suffer from holographic foil reflection, dark borders, and busy backgrounds. Execute these steps in order before passing regions to Tesseract:
1. **Border Detection & Perspective Deskew:** Convert scan to grayscale $\rightarrow$ Gaussian blur `(5,5)` $\rightarrow$ Canny edge detection $\rightarrow$ Find largest external contour $\rightarrow$ `approxPolyDP` $\rightarrow$ Apply 4-point perspective transform to flatten the card to a canonical `750x1050` px image (or `900x1450` px for slabs).
2. **Centering & Corner Inspection (Condition Estimation):**
   - Measure pixel thickness of left vs. right and top vs. bottom borders on the canonical crop to calculate `centering_ratio_lr` and `centering_ratio_tb`.
   - Extract `60x60` px patches of all 4 corners on the back scan (where dark card stock shows white paper fray best) to calculate `corner_wear_score` based on high-luminance edge pixels.
3. **Zonal Bounding Box Slicing (For Tesseract):**
   - `FRONT_HEADER`: Top 18% of card (Set logo, Player Name on vintage/TCG, HP).
   - `FRONT_FOOTER`: Bottom 22% of card (Player Name, Team, RC badge, TCG collector number `102/102`).
   - `BACK_HEADER`: Top 20% of card back (Card Number `#339`, Player bio header).
   - `BACK_FOOTER`: Bottom 15% of card back (Copyright year `© 2023`, Manufacturer legal line, "PRIZM" parallel stamp).
4. **Glare-Resistant Binarization:** Apply CLAHE (`clipLimit=2.0, tileGridSize=(8,8)`) followed by Adaptive Gaussian Thresholding (`blockSize=15, C=4`) on each sliced zone so foil backgrounds do not wash out text.

---

## 3. Domain Typography & Extraction Heuristics
- **Source of Truth Priority:**
  - **Card Number:** Always prioritize `BACK_HEADER` or `BACK_FOOTER` for sports cards, and `FRONT_FOOTER` (bottom-left/right) for Pokémon/TCG.
  - **Release Year:** Extract ONLY from the copyright line in `BACK_FOOTER` (e.g., `© 2023 Panini America, Inc.`). NEVER use career statistics years or player birth dates printed in the stat table.
  - **Parallel Identification:** Check the back of Panini cards near the card number for the word `"PRIZM"` (indicates a refractor/silver parallel even if the front scan flattens the rainbow sheen).
  - **Serial vs. Collector Number:**
    - Collector numbers in TCGs look like `025/165` or `TG14/TG30` in small standard ink at the bottom corner.
    - Serial numbers on sports/collectible cards (`045/199`, `1/1`, `08/10`) are foil-stamped, often using a stencil/metallic font on the card face or back.

### Common OCR Character Confusions (Sanitization Dictionary)
| Visual Context | Common OCR Misread | True Value | Resolution Rule |
|---|---|---|---|
| Serial / Card Number | `#I4Z/Z0O`, `O4S/I99` | `#142/200`, `045/199` | Inside numeric patterns (`^\d+/\d+$`), map `O/o -> 0`, `I/l/| -> 1`, `Z -> 2`, `S -> 5`, `B -> 8`. |
| Panini Brands | `PRI2M`, `0PTIC`, `SELECT` | `Prizm`, `Optic`, `Select` | Normalize against known `master_checklists.set_name` vocabulary. |
| Topps Numbering | `U5175`, `U S 175` | `US175` | Strip internal whitespace; map `U5` prefix on Topps Update to `US`. |
| Rookie Badge | `R C`, `RG`, `[RC]` | `is_rookie = true` | Trigger rookie flag if token matches or if `master_checklists.is_rookie_base = 1`. |

---

## 4. Runtime System Prompt (For Local Ollama VLM / Claude Cleanup API)
Embed this exact prompt template in `internal/adapters/outbound/vision/prompts.go`:

```text
You are an expert sports and trading card cataloger and authenticator for Hoot Yeah card shop.
You will receive:
1. Cropped images (or raw OCR text dumps) from the FRONT and BACK of a single trading card.
2. A list of top candidate checklist entries retrieved from our local SQLite Master Checklist database.

RULES FOR EXTRACTION:
1. RELEASE YEAR: Must come from the copyright fine print at the very bottom of the card back (e.g., "© 2023"). Never use the last season year listed in the player's statistics table.
2. CARD NUMBER: For sports cards, extract the official card number from the card back (e.g., "339", "US175", "BDA-CJ"). For Pokémon/Yu-Gi-Oh!, extract from the bottom corner of the front (e.g., "199/165", "LOB-005").
3. SERIAL NUMBER: Only populate `serial_number` and `serial_max` if the card has a limited print-run stamp (e.g., "045/199"). Do not confuse Pokémon set totals (e.g., "025/102") with serial numbers.
4. CHECKLIST MATCHING: If one of the provided SQLite Master Checklist candidates matches the card, normalize `manufacturer`, `set_name`, `card_number`, and `player_or_character` to the exact spelling in that candidate.
5. CONFIDENCE SCORING: Assign a `confidence_score` between 0.00 and 1.00. If the player name, year, or card number is illegible or ambiguous, score below 0.85 so it routes to the human review queue.

Respond ONLY with a valid JSON object matching the CardExtractionPayload schema. No markdown fences, no commentary.
5. Hoot-Score Valuation FormulaImplement this deterministic formula inside internal/domain/valuation.go:$$\text{HootScore} = \min\left(100.0, (\text{ValueScore} \times 0.45) + (\text{ImpactScore} \times 0.25) + (\text{ScarcityScore} \times 0.15) + (\text{ConditionScore} \times 0.15)\right)$$ValueScore (0–100): Logarithmic scaling of market_price_cents where $1.00 = 15, $25.00 = 55, $100.00 = 80, $500.00+ = 100.ImpactScore (0–100): Mapped from player_impact_tier (Tier 1 = 100, Tier 2 = 80, Tier 3 = 55, Tier 4 = 30, Tier 5 = 10). Add +15 bonus (capped at 100) if is_rookie == true.ScarcityScore (0–100): 1/1 = 100, /10 or less = 95, /25 = 85, /99 = 75, /299 = 60, Non-numbered Parallel/Holo = 40, Base = 10.ConditionScore (0–100): PSA/BGS 10 = 100, Grade 9.5 = 95, Grade 9 / Raw NM-MT = 85, Raw NM = 70, EX-MT or lower = 35.
