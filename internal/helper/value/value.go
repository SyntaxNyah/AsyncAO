// Package value keeps values inside bounds; a function in internal/helper needs callers in at least two packages.
package value

import "cmp"

// Clamp returns v bounded to [lo, hi]; when inverted lo wins, so a panel narrower than its minimum gets the minimum.
func Clamp[T cmp.Ordered](v, lo, hi T) T {
	if hi < lo || v < lo {
		return lo
	}
	return min(v, hi)
}

// integer is every integer type; golang.org/x/exp/constraints isn't worth a dependency.
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// ClampSet is Clamp for settings where 0 means unset: 0 stays 0.
func ClampSet[T integer](v, lo, hi T) T {
	if v == 0 {
		return 0
	}
	return Clamp(v, lo, hi)
}
