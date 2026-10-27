// Package parse turns untrusted text into values. It is part of internal/helper
// (see package value for the scope rule). AO wire parsing and theme INI rules
// stay in their own packages.
package parse

import (
	"image/color"
	"strconv"
	"strings"
)

// AtoiOr parses a base-10 integer, ignoring surrounding space. Empty or
// malformed input gives fallback, never an error. That is AO2 parity: AO2 reads
// numbers with QString::toInt(), which yields 0 for anything it cannot parse
// (Qt 6.5.3: "8SS" → 0), and uses the result unchecked (AO2-Client
// text_file_functions.cpp:212-239). Rejecting such input would make AsyncAO
// stricter than the client every theme and server was written against.
func AtoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return n
}

const (
	hexRGBDigits  = 6 // the digit counts HexColor accepts
	hexRGBADigits = 8
	opaque        = 0xff
	alphaIndex    = 3
	nibbleBits    = 4
)

// HexColor parses "rrggbb" or "rrggbbaa", in either case, with an optional
// leading '#' and surrounding space. Six digits are opaque; alpha is straight
// (not premultiplied), as everyone who types a hex colour means it.
func HexColor(s string) (color.NRGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != hexRGBDigits && len(s) != hexRGBADigits {
		return color.NRGBA{}, false
	}
	c := [hexRGBADigits / 2]uint8{alphaIndex: opaque}
	for i := 0; i < len(s); i += 2 {
		hi, ok1 := hexDigit(s[i])
		lo, ok2 := hexDigit(s[i+1])
		if !ok1 || !ok2 {
			return color.NRGBA{}, false
		}
		c[i/2] = hi<<nibbleBits | lo
	}
	return color.NRGBA{R: c[0], G: c[1], B: c[2], A: c[3]}, true
}

func hexDigit(b byte) (uint8, bool) {
	const letterBase = 10
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + letterBase, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + letterBase, true
	}
	return 0, false
}
