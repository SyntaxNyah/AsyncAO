package ui

// Truncation marker selection. truncateLabelTo appends U+2026 (AO2's own marker, courtroom.cpp:6749-6774), but a
// theme-declared display font often lacks it and the single-face draw has no per-rune fallback, so it would show as
// a .notdef box. This file picks a marker the face can draw; truncateLabelTo stays pure and renderer-free.

import (
	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// truncateEllipsisASCII is the fallback marker for a face with no U+2026: three full stops.
const truncateEllipsisASCII = "..."

// ellipsisFor returns the marker f can draw: U+2026, else three periods, else none.
// Empty is valid: a face with neither cannot mark a cut, and cutting silently beats drawing a box.
// A nil f, or a face in no built set, keeps U+2026.
// It asks f's own cmap, not the set's: every set ends with the embedded goregular, which covers both runes,
// while the draw uses the one face with no per-rune fallback. One cmap probe, only when a label did not fit.
func (c *Ctx) ellipsisFor(f *ttf.Font) string {
	cov := c.faceCover(f)
	if cov == nil {
		return truncateEllipsis
	}
	return ellipsisForCoverage(
		coverHasRune(cov, &c.sfntBuf, ellipsisRune),
		coverHasRune(cov, &c.sfntBuf, fullStopRune),
	)
}

// ellipsisForCoverage is the ladder given the two cmap answers. No bundled face lacks only U+2026, so tests with
// real faces cannot reach the middle rung through ellipsisFor.
func ellipsisForCoverage(hasEllipsis, hasFullStop bool) string {
	switch {
	case hasEllipsis:
		return truncateEllipsis
	case hasFullStop:
		return truncateEllipsisASCII
	default:
		return ""
	}
}

// faceCover is the cmap of the one face f, or nil (don't guess) when f is nil, in no built set, or failed to parse.
// The index of f in its set picks its cover because buildSet appends fonts and covers in lockstep.
func (c *Ctx) faceCover(f *ttf.Font) *sfnt.Font {
	if f == nil {
		return nil
	}
	s := c.setOf(f)
	if s == nil {
		return nil
	}
	for i, cand := range s.fonts {
		if cand == f {
			return coverAt(s.cover, i)
		}
	}
	return nil
}

// ellipsisRune / fullStopRune are the code points ellipsisFor probes (hard rule 9).
const (
	ellipsisRune = '…' // U+2026 HORIZONTAL ELLIPSIS — AO2's marker
	fullStopRune = '.' // U+002E FULL STOP — the degrade target
)
