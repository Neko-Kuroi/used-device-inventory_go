package util

import (
	"math"
	"strings"
	"unicode"
)

// digitValue returns the value of a Unicode decimal digit (category
// Nd), which is exactly the set Python's int() accepts as digits:
// ASCII, full-width, Arabic-Indic, Devanagari, and the rest.
func digitValue(r rune) (int, bool) {
	if '0' <= r && r <= '9' {
		return int(r - '0'), true
	}
	if !unicode.IsDigit(r) {
		return 0, false
	}
	// Every Nd range is one script's block of ten digits starting at
	// the range's low end, so the value is the offset within it.
	for _, rr := range unicode.Nd.R16 {
		lo, hi := rune(rr.Lo), rune(rr.Hi)
		if lo <= r && r <= hi {
			return int((r - lo) % 10), true
		}
	}
	for _, rr := range unicode.Nd.R32 {
		lo, hi := rune(rr.Lo), rune(rr.Hi)
		if lo <= r && r <= hi {
			return int((r - lo) % 10), true
		}
	}
	return 0, false
}

// ToInt is Python's int(str) for form input: surrounding whitespace
// is ignored, an ASCII sign is allowed, digits may be any Unicode
// decimal digits, and single underscores may appear between digits
// ("1_000" is 1000). The second result is false for anything
// Python's int() would reject with ValueError, and for values
// outside int64 (Python would accept those as big integers; the
// fields this serves are prices and counts stored in SQLite INTEGER
// columns, where Python's own sqlite3 overflows as well).
//
// Note: the whitespace int() ignores is NOT the set str.strip()
// removes. int() rejects U+001C..U+001F at the edges, which strip()
// removes (measured against CPython; see the generated vectors), so
// this uses unicode.IsSpace, not PyStrip.
func ToInt(s string) (int64, bool) {
	s = strings.TrimFunc(s, unicode.IsSpace)
	if s == "" {
		return 0, false
	}
	neg := false
	if s[0] == '+' || s[0] == '-' {
		neg = s[0] == '-'
		s = s[1:]
	}
	runes := []rune(s)
	if len(runes) == 0 {
		return 0, false
	}
	var value int64
	seenDigit := false
	prevUnderscore := false
	for i, r := range runes {
		if r == '_' {
			if !seenDigit || prevUnderscore || i == len(runes)-1 {
				return 0, false
			}
			prevUnderscore = true
			continue
		}
		d, ok := digitValue(r)
		if !ok {
			return 0, false
		}
		if value > (math.MaxInt64-int64(d))/10 {
			return 0, false
		}
		value = value*10 + int64(d)
		seenDigit = true
		prevUnderscore = false
	}
	if !seenDigit || prevUnderscore {
		return 0, false
	}
	if neg {
		value = -value
	}
	return value, true
}
