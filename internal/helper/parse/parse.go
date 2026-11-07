// Package parse turns untrusted text into values; AO wire parsing and theme INI rules stay in their own packages.
package parse

import (
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

// AtoiOr gives fallback for unparsable input: AO2 reads numbers with QString::toInt and uses the 0 unchecked
// (AO2-Client text_file_functions.cpp:212-239), so rejecting them would be stricter than the client.
func AtoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return n
}

// Uint8 reads a decimal clamped to [lo, hi]; ok is false, and the result 0 clamped, when s is not a number.
func Uint8(s string, lo, hi uint8) (uint8, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		n = 0 // out of range counts as no number, as in AtoiOr
	}
	// Bounded by constants before the narrowing, which a code scanner can follow and a generic clamp hides.
	if n < 0 {
		n = 0
	} else if n > math.MaxUint8 {
		n = math.MaxUint8
	}
	return value.Clamp(uint8(n), lo, hi), err == nil
}

// Int16 reads a decimal clamped to [lo, hi]; ok is false, and the result 0 clamped, when s is not a number.
func Int16(s string, lo, hi int16) (int16, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		n = 0 // out of range counts as no number, as in AtoiOr
	}
	if n < math.MinInt16 {
		n = math.MinInt16
	} else if n > math.MaxInt16 {
		n = math.MaxInt16
	}
	return value.Clamp(int16(n), lo, hi), err == nil
}

// Int32 reads a decimal clamped to [lo, hi]; ok is false, and the result 0 clamped, when s is not a number.
func Int32(s string, lo, hi int32) (int32, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		n = 0 // out of range counts as no number, as in AtoiOr
	}
	if n < math.MinInt32 {
		n = math.MinInt32
	} else if n > math.MaxInt32 {
		n = math.MaxInt32
	}
	return value.Clamp(int32(n), lo, hi), err == nil
}

const (
	hexRGBDigits  = 6 // the digit counts HexColor accepts
	hexRGBADigits = 8
	opaque        = 0xff
	alphaIndex    = 3
	nibbleBits    = 4
)

// HexColor parses "rrggbb" or "rrggbbaa", with an optional '#'; alpha is straight, not premultiplied.
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
