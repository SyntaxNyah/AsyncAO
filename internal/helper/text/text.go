// Package text shapes strings without breaking UTF-8. It is part of
// internal/helper (see package value for the scope rule).
package text

import "unicode/utf8"

// Truncate returns s cut to at most maxRunes runes. A non-positive maxRunes
// gives "".
func Truncate(s string, maxRunes int) string {
	if len(s) <= maxRunes { // byte length ≤ maxRunes ⇒ rune count ≤ maxRunes
		return s
	}
	n := 0
	for i := range s {
		if n >= maxRunes {
			return s[:i]
		}
		n++
	}
	return s
}

// TruncateBytes returns s cut to at most maxBytes bytes, backing up to the
// start of a rune so the result never ends mid-character. A non-positive
// maxBytes gives "".
func TruncateBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := max(maxBytes, 0)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
