package domain

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// "© 2023", "(c) 2023", "©2023 Panini America", "COPYRIGHT 1999". OCR often renders © as "(C)", "C)" or "@".
	copyrightRe = regexp.MustCompile(`(?i)(?:©|\(c\)|c\)|@|copyright)\s*([12][0-9OIlZSB]{3})\b`)
	// Serial / print-run pattern after numeric de-confusion: "045/199", "1/1", "08 / 10".
	serialRe = regexp.MustCompile(`\b(\d{1,4})\s*/\s*(\d{1,4})\b`)
	// Raw serial-ish tokens before de-confusion (letters OCR mistakes for digits allowed).
	serialishRe = regexp.MustCompile(`#?[0-9OoIl|ZzSsB]{1,4}\s*/\s*[0-9OoIl|ZzSsB]{1,4}`)
	// Rookie badge variants: "RC", "R C", "RG", "[RC]", "ROOKIE".
	rookieRe = regexp.MustCompile(`(?i)(?:^|[^A-Z])(?:\[?R\s?C\]?|RG|ROOKIE(?:\s+CARD)?)(?:[^A-Z]|$)`)
	// Graded slab label: "PSA 10", "BGS 9.5", "SGC 9", "CGC 9.5" with an optional cert number.
	gradeRe = regexp.MustCompile(`(?i)\b(PSA|BGS|SGC|CGC|BECKETT)\b[^0-9]{0,40}?\b(10|[1-9](?:\.5)?)\b`)
	certRe  = regexp.MustCompile(`\b(\d{7,10})\b`)
	// Sports card numbers on the back header: "#339", "No. 339", "US175", "BDA-CJ".
	sportsNumberRe = regexp.MustCompile(`(?i)(?:#|\bNO\.?\s*)\s*([A-Z]{1,4}-[A-Z0-9]{1,4}|[A-Z]{0,4}[0-9OIlZSB]{1,4}[A-Z]?)\b`)
)

// ExtractCopyrightYear returns the release year from the copyright legal line
// (never from stat tables). OCR digit confusions inside the year are repaired.
func ExtractCopyrightYear(text string) (int, bool) {
	m := copyrightRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	y, err := strconv.Atoi(DeconfuseNumeric(m[1]))
	if err != nil || y < 1860 || y > 2100 {
		return 0, false
	}
	return y, true
}

// DeconfuseNumeric maps OCR letter look-alikes to digits inside a numeric token:
// "#I4Z/Z0O" -> "142/200", "O4S/I99" -> "045/199".
func DeconfuseNumeric(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	return strings.Map(func(r rune) rune {
		switch r {
		case 'O', 'o', 'Q', 'D':
			return '0'
		case 'I', 'l', '|', 'i':
			return '1'
		case 'Z', 'z':
			return '2'
		case 'S', 's':
			return '5'
		case 'B':
			return '8'
		case 'G':
			return '6'
		}
		return r
	}, s)
}

// ParseSerial finds a limited print-run stamp ("045/199") in text, repairing OCR
// confusions. It returns the normalized "NNN/MMM" and the print run. Callers must not use
// it on TCG collector-number zones ("025/165" is a set position, not a serial).
func ParseSerial(text string) (string, int, bool) {
	for _, tok := range serialishRe.FindAllString(text, -1) {
		m := serialRe.FindStringSubmatch(DeconfuseNumeric(tok))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		max, _ := strconv.Atoi(m[2])
		if max < 1 || max > 9999 || n < 1 || n > max {
			continue
		}
		width := len(m[2])
		if len(m[1]) > width {
			width = len(m[1])
		}
		return leftPad(m[1], width) + "/" + m[2], max, true
	}
	return "", 0, false
}

// ParseCollectorNumber finds a TCG collector number ("4/102", "025/165", "TG14/TG30").
func ParseCollectorNumber(text string) (string, bool) {
	tcgRe := regexp.MustCompile(`(?i)\b([A-Z]{0,3}[0-9OIlZSB]{1,3})\s*/\s*([A-Z]{0,3}[0-9OIlZSB]{2,3})\b`)
	m := tcgRe.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	fix := func(s string) string {
		// keep a letter prefix (TG, SV) and de-confuse the numeric tail
		i := 0
		for i < len(s) && s[i] >= 'A' && s[i] <= 'Z' && !strings.ContainsRune("OIlZSB", rune(s[i])) {
			i++
		}
		return strings.ToUpper(s[:i]) + DeconfuseNumeric(s[i:])
	}
	return fix(m[1]) + "/" + fix(m[2]), true
}

// ParseSportsCardNumber extracts "#339" / "NO. US175" style card numbers from a card back header.
func ParseSportsCardNumber(text string) (string, bool) {
	m := sportsNumberRe.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	tok := strings.ToUpper(m[1])
	// Topps Update: "U5175" -> "US175"
	if strings.HasPrefix(tok, "U5") && isAllDigits(tok[2:]) {
		return "US" + tok[2:], true
	}
	// De-confuse the trailing numeric run only.
	i := len(tok)
	for i > 0 && strings.ContainsRune("0123456789OIZSB", rune(tok[i-1])) {
		i--
	}
	return tok[:i] + DeconfuseNumeric(tok[i:]), true
}

// HasRookieBadge reports whether OCR text contains an RC shield / ROOKIE token.
func HasRookieBadge(text string) bool { return rookieRe.MatchString(strings.ToUpper(text)) }

// HasPrizmStamp reports the "PRIZM" parallel stamp on a Panini card back
// (de-confused, so "PRI2M" counts).
func HasPrizmStamp(backText string) bool {
	for _, t := range TokenizeOCR(backText) {
		for _, v := range t.Variants {
			if v == "PRIZM" {
				return true
			}
		}
	}
	return false
}

// SlabLabel is the parsed grading-company flip.
type SlabLabel struct {
	Company GradingCompany
	Grade   float64
	Cert    string
}

// ParseSlabLabel reads grader, grade and cert number from SLAB_FLIP OCR text.
func ParseSlabLabel(text string) (SlabLabel, bool) {
	m := gradeRe.FindStringSubmatch(text)
	if m == nil {
		return SlabLabel{}, false
	}
	company := GradingCompany(strings.ToUpper(m[1]))
	if company == "BECKETT" {
		company = GraderBGS
	}
	g, err := strconv.ParseFloat(m[2], 64)
	if err != nil {
		return SlabLabel{}, false
	}
	out := SlabLabel{Company: company, Grade: g}
	if c := certRe.FindStringSubmatch(text); c != nil {
		out.Cert = c[1]
	}
	return out, true
}

// BestVocabularyMatch normalizes a noisy token against a known vocabulary
// ("PRI2M" -> "Prizm", "0PTIC" -> "Optic"). Returns ok=false below minSimilarity.
func BestVocabularyMatch(raw string, vocabulary []string, minSimilarity float64) (string, float64, bool) {
	best, bestScore := "", 0.0
	var variants []string
	for _, t := range TokenizeOCR(raw) {
		variants = append(variants, t.Variants...)
	}
	variants = append(variants, AlphaNum(raw))
	for _, v := range vocabulary {
		target := AlphaNum(v)
		for _, cand := range variants {
			if s := Similarity(AlphaNum(cand), target); s > bestScore {
				best, bestScore = v, s
			}
		}
	}
	return best, bestScore, bestScore >= minSimilarity && best != ""
}

// Similarity is 1 - normalized Levenshtein distance (0..1).
func Similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	m := len(ra)
	if len(rb) > m {
		m = len(rb)
	}
	return 1 - float64(prev[len(rb)])/float64(m)
}

func leftPad(s string, width int) string {
	for len(s) < width {
		s = "0" + s
	}
	return s
}
