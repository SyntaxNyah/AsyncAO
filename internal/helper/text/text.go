// Package text shapes strings without breaking UTF-8.
package text

import "unicode/utf8"

// Truncate cuts s to at most maxRunes runes; a non-positive maxRunes gives "".
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

// TruncateBytes cuts s to at most maxBytes bytes without ending mid-character; a non-positive maxBytes gives "".
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
