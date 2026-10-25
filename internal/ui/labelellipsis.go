package ui

// Choosing a truncation marker the FACE can actually draw.
// marker (AO2-Client courtroom.cpp:6749-6774) and is fine for every label drawn in
// This is a SEPARATE file from the truncation because it is a separate concern —
// the truncation knows how many characters fit, this knows what the face can draw —
// and because the answer depends on machinery (fontSet coverage) that the pure,
// Pinned by labelellipsis_test.go: the ladder, that the probe is per-FACE and not
// per-chain, and that the marker feeds AO2's truncation arithmetic unchanged.

import (
	"github.com/veandco/go-sdl2/ttf"
	"golang.org/x/image/font/sfnt"
)

// truncateEllipsisASCII is the fallback marker for a face with no U+2026: three
// FULL STOPS, the typographic ancestor of the ellipsis and a character every face
// that can draw a sentence already has.
const truncateEllipsisASCII = "..."

// ellipsisFor returns the truncation marker `f` can draw, walking down from AO2's
// own U+2026 to three periods to nothing at all.
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

// ellipsisForCoverage is the ladder itself, given the two cmap answers: the policy,
// separated from the machinery that obtains them.
// It is split out because NO face this client bundles occupies the middle rung —
// goregular, OpenDyslexic and every system text face carry both U+2026 and '.', and
// Twemoji carries neither — so a test driving ellipsisFor with real faces can only
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

// faceCover is the cmap of the ONE face f, or nil when there is no answer: f is nil,
// f belongs to no built set (the chrome-only and headless paths), or its cover face
// failed to parse. nil means "don't guess", and every caller keeps its pre-existing
// behaviour on it.
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

// ellipsisRune / fullStopRune are the two code points ellipsisFor probes. Named
// because a bare '…' in a coverage call reads as text, not as the question being
// asked of the font (hard rule 9).
const (
	ellipsisRune = '…' // U+2026 HORIZONTAL ELLIPSIS — AO2's marker
	fullStopRune = '.' // U+002E FULL STOP — the degrade target
)
