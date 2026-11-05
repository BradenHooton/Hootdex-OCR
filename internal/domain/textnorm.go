package domain

import (
	"strings"
	"unicode"
)

// OCR character de-confusion tables (OCR_DOMAIN_CONTEXT.md §3).
var (
	// Inside numeric-looking tokens: letters that OCR commonly emits for digits.
	letterToDigit = map[rune]rune{'O': '0', 'Q': '0', 'D': '0', 'I': '1', 'L': '1', '|': '1', 'Z': '2', 'S': '5', 'B': '8', 'G': '6'}
	// Inside word-looking tokens: digits that OCR commonly emits for letters.
	digitToLetter = map[rune]rune{'0': 'O', '1': 'I', '2': 'Z', '5': 'S', '8': 'B', '4': 'A', '3': 'E', '6': 'G', '7': 'T'}
)

// OCRToken is one whitespace-delimited token from OCR text with its de-confused variants.
// Variants[0] is always the cleaned raw form.
type OCRToken struct {
	Raw      string
	Variants []string
}

// TokenizeOCR uppercases text, drops punctuation that OCR scatters inside names
// ("C.J." -> "CJ"), keeps '/' and '-' inside tokens (card numbers like "4/102", "LOB-005"),
// and expands each token into its plausible de-confused readings.
func TokenizeOCR(text string) []OCRToken {
	var b strings.Builder
	for _, r := range strings.ToUpper(text) {
		switch {
		case r == '.' || r == '\'' || r == '’' || r == '#' || r == '©':
			if r == '#' || r == '©' {
				b.WriteRune(' ')
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '/' || r == '-' || r == '|':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	var out []OCRToken
	for _, f := range strings.Fields(b.String()) {
		f = strings.Trim(f, "-/")
		if f == "" {
			continue
		}
		out = append(out, OCRToken{Raw: f, Variants: TokenVariants(f)})
	}
	return out
}

// TokenVariants returns the raw token plus its numeric and alphabetic de-confusions:
// "PR1ZM" -> [PR1ZM PRIZM], "I4Z/Z0O" -> [I4Z/Z0O 142/200], "U5175" -> [U5175 US175].
func TokenVariants(tok string) []string {
	tok = strings.ToUpper(tok)
	variants := []string{tok}
	add := func(v string) {
		for _, x := range variants {
			if x == v {
				return
			}
		}
		variants = append(variants, v)
	}
	letters, digits := 0, 0
	for _, r := range tok {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsLetter(r):
			letters++
		}
	}
	// Topps Update numbering: "U5175" / "U S 175" is really "US175".
	if strings.HasPrefix(tok, "U5") && len(tok) > 2 && isAllDigits(tok[2:]) {
		add("US" + tok[2:])
	}
	if digits > 0 && letters > 0 {
		if digits >= letters || strings.Contains(tok, "/") {
			add(mapRunes(tok, letterToDigit))
		}
		if letters >= digits {
			add(mapRunes(tok, digitToLetter))
		}
	}
	if digits == 0 && strings.ContainsAny(tok, "|") {
		add(strings.ReplaceAll(tok, "|", "I"))
	}
	return variants
}

// AlphaNum strips everything but A-Z / 0-9 (uppercased): "C.J. Stroud" -> "CJSTROUD".
func AlphaNum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NameTokens splits a proper name into uppercase alphanumeric tokens, joining dotted
// initials: "C.J. Stroud" -> [CJ STROUD], "Jaxon Smith-Njigba" -> [JAXON SMITH NJIGBA].
func NameTokens(s string) []string {
	s = strings.NewReplacer(".", "", "'", "", "’", "").Replace(s)
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToUpper(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if t := AlphaNum(f); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// NormalizeCardNumber canonicalises a card/collector number for comparison:
// "#004/102" -> "4/102", "us175" -> "US175", "LOB-005" -> "LOB5".
func NormalizeCardNumber(s string) string {
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#")), "/")
	for i, p := range parts {
		p = AlphaNum(p)
		// Strip leading zeros on each numeric run so "025" == "25" and "LOB-005" == "LOB5".
		var b strings.Builder
		prevDigit := false
		leading := true
		for _, r := range p {
			isDigit := r >= '0' && r <= '9'
			if isDigit && !prevDigit {
				leading = true
			}
			if isDigit && leading && r == '0' {
				prevDigit = true
				continue
			}
			if isDigit {
				leading = false
			}
			if !isDigit && prevDigit && leading {
				b.WriteRune('0') // the run was all zeros
			}
			prevDigit = isDigit
			b.WriteRune(r)
		}
		if prevDigit && leading {
			b.WriteRune('0')
		}
		parts[i] = b.String()
	}
	return strings.Join(parts, "/")
}

func mapRunes(s string, m map[rune]rune) string {
	return strings.Map(func(r rune) rune {
		if v, ok := m[r]; ok {
			return v
		}
		return r
	}, s)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
