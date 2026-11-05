package vision

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// SystemPrompt is the runtime prompt for the local Ollama VLM and the Claude cleanup API
// (verbatim from OCR_DOMAIN_CONTEXT.md §4).
const SystemPrompt = `You are an expert sports and trading card cataloger and authenticator for Hoot Yeah card shop.
You will receive:
1. Cropped images (or raw OCR text dumps) from the FRONT and BACK of a single trading card.
2. A list of top candidate checklist entries retrieved from our local SQLite Master Checklist database.

RULES FOR EXTRACTION:
1. RELEASE YEAR: Must come from the copyright fine print at the very bottom of the card back (e.g., "© 2023"). Never use the last season year listed in the player's statistics table.
2. CARD NUMBER: For sports cards, extract the official card number from the card back (e.g., "339", "US175", "BDA-CJ"). For Pokémon/Yu-Gi-Oh!, extract from the bottom corner of the front (e.g., "199/165", "LOB-005").
3. SERIAL NUMBER: Only populate ` + "`serial_number`" + ` and ` + "`serial_max`" + ` if the card has a limited print-run stamp (e.g., "045/199"). Do not confuse Pokémon set totals (e.g., "025/102") with serial numbers.
4. CHECKLIST MATCHING: If one of the provided SQLite Master Checklist candidates matches the card, normalize ` + "`manufacturer`, `set_name`, `card_number`, and `player_or_character`" + ` to the exact spelling in that candidate.
5. CONFIDENCE SCORING: Assign a ` + "`confidence_score`" + ` between 0.00 and 1.00. If the player name, year, or card number is illegible or ambiguous, score below 0.85 so it routes to the human review queue.

Respond ONLY with a valid JSON object matching the CardExtractionPayload schema. No markdown fences, no commentary.`

// MaxPromptCandidates is how many checklist candidates are sent to the LLM.
const MaxPromptCandidates = 15

// BuildUserPrompt renders the per-card message: OCR dumps, measurements and candidates.
func BuildUserPrompt(req ports.ExtractionRequest) string {
	var b strings.Builder
	b.WriteString("FRONT OCR TEXT:\n")
	b.WriteString(orNone(req.RawOCRFront))
	b.WriteString("\n\nBACK OCR TEXT:\n")
	b.WriteString(orNone(req.RawOCRBack))
	if pre := req.Preprocess; pre != nil {
		fmt.Fprintf(&b, "\n\nIMAGE MEASUREMENTS:\ncard_type=%s centering_ratio_lr=%s centering_ratio_tb=%s estimated_raw_condition=%s",
			pre.CardType, orNone(pre.CenteringLR), orNone(pre.CenteringTB), orNone(string(pre.EstimatedCondition)))
		if pre.CornerWearScore != nil {
			fmt.Fprintf(&b, " corner_wear_score=%.2f", *pre.CornerWearScore)
		}
	}
	b.WriteString("\n\nSQLITE MASTER CHECKLIST CANDIDATES (best first):\n")
	if len(req.Candidates) == 0 {
		b.WriteString("(none)\n")
	}
	for i, c := range req.Candidates {
		if i == MaxPromptCandidates {
			break
		}
		e := c.Entry
		row, _ := json.Marshal(map[string]any{
			"category": e.Category, "release_year": e.ReleaseYear, "manufacturer": e.Manufacturer, "set_name": e.SetName,
			"card_number": e.CardNumber, "player_or_character": e.PlayerOrCharacter, "team_or_type": e.TeamOrType,
			"is_rookie_base": e.IsRookieBase, "known_parallels": e.KnownParallels,
		})
		fmt.Fprintf(&b, "%d. (match score %.2f) %s\n", i+1, c.Score, row)
	}
	b.WriteString("\nReturn the CardExtractionPayload JSON for this card.")
	return b.String()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

// PayloadJSONSchema is the CardExtractionPayload contract as a JSON Schema usable by
// Claude structured outputs (output_config.format) and Ollama's `format` field.
func PayloadJSONSchema() map[string]any {
	str := map[string]any{"type": "string"}
	nullable := func(s map[string]any) map[string]any {
		return map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
	}
	enum := func(vals ...string) map[string]any { return map[string]any{"type": "string", "enum": vals} }
	cats := make([]string, 0, len(domain.AllCategories))
	for _, c := range domain.AllCategories {
		cats = append(cats, string(c))
	}
	props := map[string]any{
		"category":                enum(cats...),
		"release_year":            map[string]any{"type": "integer"},
		"manufacturer":            str,
		"set_name":                str,
		"card_number":             str,
		"player_or_character":     str,
		"team_or_type":            nullable(str),
		"parallel_variant":        str,
		"serial_number":           nullable(str),
		"serial_max":              nullable(map[string]any{"type": "integer"}),
		"is_rookie":               map[string]any{"type": "boolean"},
		"is_autograph":            map[string]any{"type": "boolean"},
		"is_memorabilia":          map[string]any{"type": "boolean"},
		"is_graded":               map[string]any{"type": "boolean"},
		"grading_company":         enum("RAW", "PSA", "BGS", "SGC", "CGC"),
		"grade_value":             nullable(map[string]any{"type": "number"}),
		"cert_number":             nullable(str),
		"estimated_raw_condition": nullable(enum("NM-MT", "NM", "EX-MT", "VG-EX", "POOR")),
		"centering_ratio_lr":      nullable(str),
		"centering_ratio_tb":      nullable(str),
		"confidence_score":        map[string]any{"type": "number"},
		"extraction_notes":        str,
	}
	required := make([]string, 0, len(props))
	for _, k := range payloadFieldOrder {
		required = append(required, k)
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

// payloadFieldOrder keeps `required` deterministic (stable bytes help prompt caching).
var payloadFieldOrder = []string{
	"category", "release_year", "manufacturer", "set_name", "card_number", "player_or_character", "team_or_type",
	"parallel_variant", "serial_number", "serial_max", "is_rookie", "is_autograph", "is_memorabilia", "is_graded",
	"grading_company", "grade_value", "cert_number", "estimated_raw_condition", "centering_ratio_lr",
	"centering_ratio_tb", "confidence_score", "extraction_notes",
}

// stripFences tolerates a model that wraps JSON in ``` fences despite instructions.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

// faceImages returns the cropped canonical front/back paths that exist for a request.
func faceImages(req ports.ExtractionRequest) []string {
	if req.Preprocess == nil {
		return nil
	}
	var out []string
	for _, p := range []string{req.Preprocess.CroppedFrontPath, req.Preprocess.CroppedBackPath} {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
