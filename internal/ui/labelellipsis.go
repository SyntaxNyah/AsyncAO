package ui

// Choosing a truncation marker the FACE can actually draw.
//
// truncateLabelTo (ui.go) appends U+2026 HORIZONTAL ELLIPSIS, which is AO2's own
// marker (AO2-Client courtroom.cpp:6749-6774) and is fine for every label drawn in
// the client's own chrome font — that face carries it. It is NOT fine for the
// surfaces that draw in a THEME-declared face: courtroom_fonts.ini names a family,
// the family resolves to whatever display font the theme author liked, and display
// faces routinely ship Latin plus punctuation and nothing else. U+2026 then has no
// glyph, the single-face draw has no per-rune fallback to route it through, and the
// player sees the .notdef box at the end of every shortened label — reported on the
// music banner as "[Cat] Also Sprac[]", "Climax R[]" and "...Killi[]".
//
// The cut itself was never the bug: truncateLabelTo has always sliced []rune, so it
// cannot split a rune. What it did was append a rune nobody checked for.
//
// This is a SEPARATE file from the truncation because it is a separate concern —
// the truncation knows how many characters fit, this knows what the face can draw —
// and because the answer depends on machinery (fontSet coverage) that the pure,
// renderer-free truncateLabelTo deliberately does not touch.
//
// Pinned by labelellipsis_test.go: the ladder, that the probe is per-FACE and not
// per-chain, and that the marker feeds AO2's truncation arithmetic unchanged.

import (
	"github.com/veandco/go-sdl2/ttf"
	"golang.org/x/image/font/sfnt"
)

// truncateEllipsisASCII is the fallback marker for a face with no U+2026: three
// FULL STOPS, the typographic ancestor of the ellipsis and a character every face
// that can draw a sentence already has.
//
// Not "..", not a single ".", and not U+22EF or any of the other Unicode ellipsis
// relatives: three periods is what every text renderer degrades an ellipsis to, and
// each of the alternatives is either ambiguous with a real full stop or just as
// likely to be missing as U+2026 itself.
const truncateEllipsisASCII = "..."

// ellipsisFor returns the truncation marker `f` can draw, walking down from AO2's
// own U+2026 to three periods to nothing at all.
//
// The empty string is a real answer, not a failure: a face that has neither an
// ellipsis nor a full stop cannot mark a cut, and cutting silently is strictly
// better than drawing a box that says "your font is broken" in the middle of a
// track name. truncateLabelToMarker handles it — the chop is then exactly one rune
// per pass and the guard against collapsing to the marker alone is trivially true.
//
// f == nil, or a face that belongs to no built set (the chrome-only and headless
// paths), keeps U+2026: that is the pre-existing behaviour and the client's own
// faces all carry the glyph. Refusing to guess in the direction of a change is what
// keeps every existing truncation byte-identical.
//
// THE QUESTION IS ASKED OF ONE FACE, NOT OF THE CHAIN. This shipped asking
// Ctx.setCovers, which answers "does ANY face in f's set have this rune" — and
// buildSet appends the embedded goregular face to EVERY set as the last resort
// (ui.go: "Embedded last resort"), while goregular carries both U+2026 and '.'. So
// the set answer was structurally always yes, the ladder never left its first rung,
// and the .notdef box this file exists to remove kept drawing: the marker is
// rasterised by LabelClippedFontWeight → textTextureBold, which renders the WHOLE
// string with the ONE face handed in, with no per-rune fallback to route an
// uncovered marker through. faceCover therefore resolves f's OWN cmap.
//
// COST: one bounded pointer scan (setOf, then f's index in that set) plus one cmap
// probe per call, and it is called only on the branch where a label did not fit —
// the same branch that was already about to walk the string re-measuring it. The
// fits case never gets here.
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
//
// It is split out because NO face this client bundles occupies the middle rung —
// goregular, OpenDyslexic and every system text face carry both U+2026 and '.', and
// Twemoji carries neither — so a test driving ellipsisFor with real faces can only
// reach the first and last rungs. The rung that DOES matter in the field (a theme's
// display face with punctuation but no ellipsis, which is the shape that put a
// .notdef box at the end of the music banner) is reachable only here, and a ladder
// with an untestable middle rung is how the previous version shipped inert.
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
//
// setOf resolves the SET; the index of f inside that set is what picks its own cover,
// because buildSet appends fonts and covers in lockstep (a skipped face skips both).
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
