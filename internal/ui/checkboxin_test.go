package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// fixedWidth is a measure func with one logical pixel per rune, so the truncation
// tests read as character counts and do not depend on a font being loaded.
func fixedWidth(s string) int32 { return int32(len([]rune(s))) }

// TestTruncateLabelToMatchesAO2 pins AO2's own algorithm: chop two runes, append
// one ellipsis, re-measure (AO2-Client courtroom.cpp:6749-6774).
func TestTruncateLabelToMatchesAO2(t *testing.T) {
	for _, tc := range []struct {
		name  string
		label string
		avail int32
		want  string
	}{
		// Room to spare: untouched.
		{"fits easily", "Preanim", 20, "Preanim"},
		// EXACTLY the available width is untouched. AO2's loop condition is
		// "> label_theme_width", so equality never enters it — an off-by-one here
		// would ellipsise a label that fits.
		{"fits exactly", "Preanim", 7, "Preanim"},
		// Each pass chops 2 runes off the CURRENT string and appends 1, so the
		// ellipsis a previous pass added is one of the two chopped: pass 1 nets one
		// rune narrower, and so does every pass after it. Getting this wrong (by
		// stripping the ellipsis before chopping) silently drops an extra character
		// per pass from the second pass onward.
		{"one short", "Preanim", 6, "Prean…"},
		{"two short", "Preanim", 5, "Prea…"},
		{"three short", "Preanim", 4, "Pre…"},
		// A rect so narrow the text would collapse to a bare ellipsis: AO2 abandons
		// the truncation and keeps the original rather than draw a lone "…".
		{"collapses to ellipsis", "Preanim", 1, "Preanim"},
		{"tiny label, tiny rect", "Hi", 1, "Hi"},
		// A non-positive rect cannot be measured against; leave the label alone.
		{"zero width", "Preanim", 0, "Preanim"},
		{"negative width", "Preanim", -8, "Preanim"},
		// Multi-byte runes must chop by RUNE, not by byte.
		{"multibyte", "日本語テキスト", 4, "日本語…"},
	} {
		if got := truncateLabelTo(tc.label, tc.avail, fixedWidth); got != tc.want {
			t.Errorf("%s: truncateLabelTo(%q, %d) = %q, want %q", tc.name, tc.label, tc.avail, got, tc.want)
		}
	}
}

// TestTruncateLabelToAlwaysFitsOrGivesUp is the property the table cannot state:
// for every prefix width, the result either fits or is the untouched original.
// A result that is neither would overflow its rect silently.
func TestTruncateLabelToAlwaysFitsOrGivesUp(t *testing.T) {
	const label = "Preanim (no interrupt)"
	for avail := int32(-2); avail <= int32(len([]rune(label)))+2; avail++ {
		got := truncateLabelTo(label, avail, fixedWidth)
		if fixedWidth(got) <= avail || got == label {
			continue
		}
		t.Errorf("avail=%d: %q neither fits nor is the original", avail, got)
	}
}

// TestTruncateLabelToTerminatesOnAPathologicalMeasure guards the loop. AO2 shipped
// an explicit infinite-loop safeguard here, which means it hit one: a measure func
// that never reports a narrower string would chop forever.
//
// The rune-count guard is what stops it — the label is chopped down to
// truncateChopRunes and then handed back untouched — so this returns rather than
// hanging the suite.
func TestTruncateLabelToTerminatesOnAPathologicalMeasure(t *testing.T) {
	const label = "a label that never gets narrower"
	got := truncateLabelTo(label, 4, func(string) int32 { return 999 })
	if got != label {
		t.Errorf("a non-shrinking measure must give up and keep the original, got %q", got)
	}
}

// TestCheckboxLabelAvailSubtractsTheBox pins the arithmetic the layout pass and the
// draw site must agree on: measure with a different number than you clip with and a
// label ellipsises early or overflows.
func TestCheckboxLabelAvailSubtractsTheBox(t *testing.T) {
	c := &Ctx{}
	for _, w := range []int32{51, 60, 80, 200} {
		r := sdl.Rect{X: 10, Y: 20, W: w, H: 19}
		if got, want := c.CheckboxLabelAvail(r), w-checkboxBoxPx-checkboxLabelGapPx; got != want {
			t.Errorf("CheckboxLabelAvail(W=%d) = %d, want %d", w, got, want)
		}
	}
	// aceattorney2x's real flip rect is 51x19, which leaves 29 px — far less than
	// "Flip" needs at the theme's own font size, so the ladder really does run.
	if got := c.CheckboxLabelAvail(sdl.Rect{W: 51, H: 19}); got != 29 {
		t.Errorf("the theme's 51px flip rect leaves %d px for its label, want 29", got)
	}
}

// TestCheckboxBoxYSurvivesADownscaledRect pins the ThemeFitNative hazard: a
// downscaled canvas can hand CheckboxIn a rect SHORTER than the 16 px tick box, and
// an un-floored (r.H-box)/2 would lift the box out of its own row and into whatever
// is drawn above it.
//
// It calls the REAL checkboxBoxY. An earlier version recomputed the formula in the
// test body and asserted on its own copy, which would have passed with the floor
// deleted from production — the arithmetic was extracted precisely so this test has
// something to be wrong about.
func TestCheckboxBoxYSurvivesADownscaledRect(t *testing.T) {
	for _, h := range []int32{19, 16, 12, 8, 6, 1, 0} {
		r := sdl.Rect{X: 4, Y: 100, W: 51, H: h}
		got := checkboxBoxY(r)
		if got < r.Y {
			t.Errorf("H=%d: box at y=%d, above its own rect at %d", h, got, r.Y)
		}
		// Where there IS room, it must actually centre rather than just clamp.
		if h > checkboxBoxPx {
			if want := r.Y + (h-checkboxBoxPx)/2; got != want {
				t.Errorf("H=%d: box at y=%d, want it centred at %d", h, got, want)
			}
		} else if got != r.Y {
			t.Errorf("H=%d: box at y=%d, want the rect's own top %d", h, got, r.Y)
		}
	}
}

// TestTruncateLabelToIsAllocFreeWhenItFits pins the common case. Every themed
// toggle whose label already fits must not allocate: the layout rebuild runs them
// all, and the themed frame is gated at zero allocations.
func TestTruncateLabelToIsAllocFreeWhenItFits(t *testing.T) {
	labels := []string{"Preanim", "Flip", "Additive", "Guard", "Shownames"}
	// Sanity FIRST: prove the loop below really exercises the fits branch, or a
	// zero-alloc result would mean nothing.
	if got := truncateLabelTo(labels[0], 1000, fixedWidth); got != labels[0] {
		t.Fatalf("fixture wrong: %q", got)
	}
	n := testing.AllocsPerRun(200, func() {
		for _, l := range labels {
			_ = truncateLabelTo(l, 1000, fixedWidth)
		}
	})
	if n != 0 {
		t.Errorf("truncateLabelTo allocates %.1f/op on the fits path, want 0", n)
	}
}
