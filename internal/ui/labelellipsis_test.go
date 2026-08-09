package ui

// The truncation MARKER (labelellipsis.go) and the arithmetic it feeds
// (truncateChopFor / truncateLabelToMarker, ui.go).
//
// Both shipped untested. truncateChopFor's own doc comment named a test that was
// never written, and the marker ladder shipped INERT: it asked Ctx.setCovers, which
// answers for the whole font CHAIN, and buildSet appends the embedded goregular face
// to every chain as the last resort — goregular has U+2026, so the answer was always
// "yes" and the fallback rungs were unreachable. These pin both halves.

import (
	"strings"
	"testing"
)

// TestTruncateChopMatchesAO2ForTheEllipsis is the test truncateChopFor's comment
// cites. AO2 chops TWO characters and appends a one-character ellipsis
// (AO2-Client courtroom.cpp:6751-6756), so each pass nets exactly one original
// character narrower; truncateChopFor generalises that to any marker, and for AO2's
// own marker it must still be literally 2.
func TestTruncateChopMatchesAO2ForTheEllipsis(t *testing.T) {
	if got := truncateChopFor(truncateEllipsis); got != truncateChopRunes {
		t.Errorf("truncateChopFor(%q) = %d, want %d — AO2's chop(2)", truncateEllipsis, got, truncateChopRunes)
	}
	if truncateChopRunes != 2 {
		t.Errorf("truncateChopRunes = %d, want 2 (courtroom.cpp:6753 chop(2))", truncateChopRunes)
	}
	// The generalisation: marker runes plus one, so the NET loss stays one original
	// rune whatever the marker costs.
	for _, tc := range []struct {
		marker string
		want   int
	}{
		{"…", 2},                   // AO2's own
		{truncateEllipsisASCII, 4}, // three periods
		{"", 1},                    // a face with neither: one rune per pass
		{"……", 3},                  // two-rune marker, for the arithmetic
	} {
		if got := truncateChopFor(tc.marker); got != tc.want {
			t.Errorf("truncateChopFor(%q) = %d, want %d", tc.marker, got, tc.want)
		}
	}

	// Drive the loop the number feeds: with a one-px-per-rune measure, each pass
	// must remove exactly ONE original rune, which is what makes the generalised
	// version byte-identical to AO2's for AO2's marker.
	measure := func(s string) int32 { return int32(len([]rune(s))) }
	const label = "ABCDEFGHIJ" // 10 runes
	for _, tc := range []struct {
		marker string
		avail  int32
		want   string
	}{
		{truncateEllipsis, 10, label},  // fits exactly — AO2's loop condition is >
		{truncateEllipsis, 5, "ABCD…"}, // 5 runes wide, marker included
		{truncateEllipsis, 9, "ABCDEFGH…"},
		{truncateEllipsisASCII, 5, "AB..."},
		{"", 5, "ABCDE"},
	} {
		if got := truncateLabelToMarker(label, tc.avail, tc.marker, measure); got != tc.want {
			t.Errorf("truncateLabelToMarker(%q, %d, %q) = %q, want %q", label, tc.avail, tc.marker, got, tc.want)
		}
	}
	// AO2's safeguard: a chop that would leave nothing but the marker abandons the
	// truncation and keeps the ORIGINAL (courtroom.cpp:6757-6764).
	if got := truncateLabelToMarker("AB", 1, truncateEllipsis, measure); got != "AB" {
		t.Errorf("collapsing to the marker alone must keep the original, got %q", got)
	}
	// …and with the empty marker the same guard must still terminate rather than
	// spin: an unsatisfiable width returns the original.
	if got := truncateLabelToMarker("AB", 0, "", measure); got != "AB" {
		t.Errorf("avail<=0 must return the label untouched, got %q", got)
	}
}

// TestEllipsisLadderPrefersAO2sMarker pins the policy: U+2026, then three periods,
// then nothing. The middle rung is only reachable here — no face this client bundles
// has '.' without U+2026 (goregular and OpenDyslexic have both, Twemoji neither) —
// which is precisely why it is a function and not three inline ifs.
func TestEllipsisLadderPrefersAO2sMarker(t *testing.T) {
	for _, tc := range []struct {
		hasEllipsis, hasFullStop bool
		want                     string
		why                      string
	}{
		{true, true, truncateEllipsis, "an ordinary text face keeps AO2's marker"},
		{true, false, truncateEllipsis, "the ellipsis wins even without a full stop"},
		{false, true, truncateEllipsisASCII, "a display face with punctuation degrades to three periods"},
		{false, false, "", "a face with neither cuts silently rather than drawing a box"},
	} {
		if got := ellipsisForCoverage(tc.hasEllipsis, tc.hasFullStop); got != tc.want {
			t.Errorf("ellipsisForCoverage(%v, %v) = %q, want %q — %s", tc.hasEllipsis, tc.hasFullStop, got, tc.want, tc.why)
		}
	}
	// The degrade target is three FULL STOPS, not two, not one, and not another
	// Unicode ellipsis relative that is just as likely to be missing.
	if truncateEllipsisASCII != "..." {
		t.Errorf("the ASCII fallback is %q, want %q", truncateEllipsisASCII, "...")
	}
	if ellipsisRune != '…' || fullStopRune != '.' {
		t.Errorf("the probed runes drifted: %q / %q", ellipsisRune, fullStopRune)
	}
}

// TestEllipsisForAsksTheDrawingFace is the regression gate for the inert version.
//
// A THEME-declared face is the whole reason this code exists, and the face used here
// (Twemoji) covers neither U+2026 nor '.', while the SET it lives in ends — as every
// set does — with the embedded goregular last resort, which covers both. Asking the
// set therefore said "U+2026 is fine" and the .notdef box kept drawing; asking the
// face says "this one can draw neither", which is the answer the single-face
// rasteriser (textTextureBold) actually needs.
func TestEllipsisForAsksTheDrawingFace(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	defer c.Destroy()

	// No face and a face in no built set (the chrome-only and headless paths) keep
	// AO2's marker: refusing to guess is what leaves every existing truncation
	// byte-identical.
	if got := c.ellipsisFor(nil); got != truncateEllipsis {
		t.Errorf("nil face = %q, want %q", got, truncateEllipsis)
	}
	if c.setOf(c.font) == nil { // the chrome face belongs to no scaled set
		if got := c.ellipsisFor(c.font); got != truncateEllipsis {
			t.Errorf("an unresolvable face = %q, want %q", got, truncateEllipsis)
		}
	}

	// A face out of a real scaled set: the embedded text face, which has U+2026.
	if primary := c.LogFontFor(DefaultScalePct, "probe"); primary != nil && c.setOf(primary) != nil {
		if got := c.ellipsisFor(primary); got != truncateEllipsis {
			t.Errorf("the embedded text face = %q, want AO2's %q", got, truncateEllipsis)
		}
		if c.faceCover(primary) == nil {
			t.Error("a face out of a built set must resolve its own cover")
		}
	}

	// The theme-declared face, installed through the production path.
	c.SetThemeFaces([][]byte{twemojiTTF})
	defer c.SetThemeFaces(nil)
	face := c.ThemeFont(elemMusicName, 0, DefaultScalePct)
	if face == nil {
		t.Skip("the theme face did not open in this environment")
	}
	set := c.setOf(face)
	if set == nil {
		t.Skip("the theme face is not in a built set here")
	}
	// The premise, asserted rather than assumed: the CHAIN covers the ellipsis (the
	// embedded last resort does) while the drawing FACE does not.
	if !c.setCovers(set, ellipsisRune) {
		t.Fatal("the theme set does not cover U+2026 — the fixture no longer reproduces the chain-vs-face split this gate exists for")
	}
	if cov := c.faceCover(face); cov == nil || coverHasRune(cov, &c.sfntBuf, ellipsisRune) {
		t.Fatal("the theme FACE covers U+2026 — pick a face that does not, or this proves nothing")
	}
	if got := c.ellipsisFor(face); got != "" {
		t.Errorf("a face with neither U+2026 nor '.' returned %q, want the empty marker — the probe is answering for the chain again", got)
	}
	// End to end: the shortened label carries no marker the face cannot draw.
	if shown := c.fitLabelToFont(face, "[Cat] Also Sprach Zarathustra", 40); strings.ContainsRune(shown, ellipsisRune) {
		t.Errorf("fitLabelToFont appended U+2026 in a face that has no glyph for it: %q", shown)
	}
}
