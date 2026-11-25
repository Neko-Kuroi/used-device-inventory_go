// Package util holds small helpers that reproduce Python's behaviour
// where the Python version's behaviour is the specification.
package util

import (
	"strings"
	"unicode"
)

// isPySpace reports whether r is whitespace for Python's str.strip()
// and int(): Unicode whitespace plus U+001C..U+001F, which Python
// counts as whitespace but Go's unicode.IsSpace does not.
func isPySpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// PyStrip is Python's str.strip() with no arguments: it removes
// leading and trailing whitespace, including the full-width space
// (U+3000) that appears in Japanese input.
func PyStrip(s string) string {
	return strings.TrimFunc(s, isPySpace)
}

// PyLStrip is Python's str.lstrip() with no arguments.
func PyLStrip(s string) string {
	return strings.TrimLeftFunc(s, isPySpace)
}
