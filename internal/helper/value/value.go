// Package value keeps values inside bounds. It is part of internal/helper: the
// one place for value coercion and parsing of untrusted input, split into
// stdlib-only category packages (value, text, parse) that every layer may use.
// A function belongs here only when it has callers in at least two packages.
package value

import "cmp"

// Clamp returns v bounded to [lo, hi]. When the range is inverted (hi < lo),
// lo wins, so a caller whose upper bound shrinks below its lower bound (a panel
// narrower than its minimum) gets the minimum rather than a value outside it.
func Clamp[T cmp.Ordered](v, lo, hi T) T {
	if hi < lo || v < lo {
		return lo
	}
	return min(v, hi)
}

// integer is every integer type (the standard library has no such constraint;
// golang.org/x/exp/constraints does, and is not worth a dependency).
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// ClampSet is Clamp for settings where 0 means "unset, use the default": 0
// stays 0, every other value is clamped to [lo, hi].
func ClampSet[T integer](v, lo, hi T) T {
	if v == 0 {
		return 0
	}
	return Clamp(v, lo, hi)
}
