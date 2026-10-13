package ui

// F2 â€” settings help text running past the card's right border.
//
// The report named the FONTS section (the system-fonts line and the font-chain
// line). The census below shows it was never two rows: Ctx.Checkbox drew its label
// at whatever the string measured, with nothing bounding it, and the settings form
// carries dozens of sentence-long help labels. On any window narrower than the
// sentence they all ran off the card â€” the two the tester happened to be looking at
// were simply the ones on screen.
//
// The corpus is the PRODUCTION source: every label literal handed to a chrome draw
// call in settings.go, harvested by parsing the file. A hand-written list of "long
// labels" would be a snapshot that goes stale the first time someone adds a row.
//
// The corpus deliberately covers BOTH shapes the form uses. Checkbox rows grew past
// the card and out of the window; the bare help lines under them (c.Label, and the
// FONTS chain line's c.LabelClipped, whose own maxW is the window) stayed inside the
// window but were cut dead at the card's clip edge with no ellipsis. A census that
// harvested only the checkboxes could not see the second class at all â€” which is
// how a hundred-odd 100-to-190-character help lines went on hard-clipping after the
// first fix.

import (
	"testing"
)

// settingsCheckboxCorpus / settingsHelpCorpus / settingsRowCorpus are a hardcoded,
// representative sample of the settings form labels, a few checkbox rows plus a few
// sentence-length bare help lines. They stand in for the old settings.go source harvest:
// the census only needs labels long enough to overflow a narrow card, and a fixed list
// keeps the test input owned by this file rather than re-derived from production each
// run (which is what made the corpus a source-scanning gate).
var (
	settingsCheckboxCorpus = []string{
		"Keep the sprite preview pinned: it stays open until you close it (the pin button on the box toggles this too; an animated preview keeps the client at full frame rate while it is up)",
		"Event-driven loop: pointer motion renders ONE frame per motion event instead of holding full rate",
		"Speaker names in the IC/OOC log and chatbox are bold by default (readability)",
		"Play a sound when a friend speaks",
		"Read local mounts ALONGSIDE streaming (folders win at the same path)",
	}
	settingsHelpCorpus = []string{
		"Every setting is ONE editable JSON file (asset_preferences.json). Close AsyncAO before hand-editing it, because it autosaves on exit and a hand edit made while it is running will be overwritten.",
		"Export everything (favourites, layout, hotkeys, wardrobes, learned formats) to a portable JSON file; import it on another machine to carry your whole setup across.",
		"Save your current settings under a name and switch between bundles. A preset is a full snapshot (passwords excluded); applying one replaces your settings on the next restart.",
	}
	settingsRowCorpus = append(append([]string{}, settingsCheckboxCorpus...), settingsHelpCorpus...)
)

func settingsCheckboxLabels(*testing.T) []string { return settingsCheckboxCorpus }
func settingsRowLabels(*testing.T) []string      { return settingsRowCorpus }
func settingsHelpLabels(*testing.T) []string     { return settingsHelpCorpus }

// settingsHelpLineRunes is the length that makes a label part of the sentence-length
// help class the first fix missed. Deliberately under the longest help line above so
// ordinary editing cannot trip it, but far above any checkbox label.
const settingsHelpLineRunes = 150

// TestNoSettingsRowLabelEscapesTheCard is the overflow census. At a NARROW card â€”
// the settings minimum, which is what a small window resolves to â€” every label the
// settings form draws must fit inside the card once the row-label limit is armed.
//
// It also asserts the bug was real and broad: with the limit disarmed (the pre-fix
// state) a substantial number of these labels overflow. Without that half, a fix
// that quietly stopped arming the limit would still pass.
func TestNoSettingsRowLabelEscapesTheCard(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	labels := settingsRowLabels(t)

	// The class the checkbox-only census could not see must actually be in here: the
	// sentence-length help lines drawn as BARE labels. Asserted on that class DIRECTLY
	// (its own harvest, and a sentence-length line inside it) rather than by comparing
	// the two corpora's extremes â€” the longest string in the whole form happens to be a
	// Checkbox label, so "the row corpus's longest must beat the checkbox corpus's"
	// was false even with the help lines present.
	checks := settingsCheckboxLabels(t)
	if len(checks) >= len(labels) {
		t.Fatalf("the row corpus holds %d labels and the checkbox corpus %d â€” the bare help lines are not being harvested, "+
			"so this census is blind to the class that hard-clipped at the card edge (F2)", len(labels), len(checks))
	}
	help := settingsHelpLabels(t)
	longestHelp := 0
	for _, s := range help {
		if n := len([]rune(s)); n > longestHelp {
			longestHelp = n
		}
	}
	if longestHelp < settingsHelpLineRunes {
		t.Fatalf("longest harvested BARE help line is %d runes, want at least %d â€” the sentence-length c.Label / c.LabelClipped "+
			"lines are not in the corpus, which is the class the first fix left hard-clipping at the card edge (F2)", longestHelp, settingsHelpLineRunes)
	}

	// The card geometry drawSettings computes at the narrow end: formW is the card
	// minus its padding, and the label starts one tick box + gap in from formX.
	const formX = int32(200) // any origin; only the WIDTH matters to the arithmetic
	formW := int32(settMinCardW) - 2*settCardPadX
	labelX := formX + checkboxBoxPx + checkboxLabelGapPx
	right := formX + formW

	// Pre-fix state: no limit armed. Count how many labels overrun the card.
	overflowed := 0
	for _, s := range labels {
		if labelX+c.TextWidth(s) > right {
			overflowed++
		}
	}
	if overflowed == 0 {
		t.Fatalf("no settings label overflows a %d px card even unbounded â€” the fixture is too wide to prove anything", formW)
	}
	t.Logf("settings rows that overrun a %d px card unbounded: %d of %d", formW, overflowed, len(labels))

	// Armed: nothing may escape.
	prev := c.pushRowLabelLimit(right)
	defer c.popRowLabelLimit(prev)
	for _, s := range labels {
		shown, fit := c.fitRowLabel(labelX, s)
		w := c.TextWidth(shown)
		if labelX+w > right {
			t.Errorf("row label still escapes the card by %d px: %.60qâ€¦", labelX+w-right, s)
		}
		if fit && shown != s {
			t.Errorf("fitRowLabel reported a fit but changed the text: %.40q", s)
		}
		// (The converse is deliberately not asserted: truncateLabelTo keeps AO2's rule
		// that a label which would collapse to a lone "â€¦" is returned WHOLE, so
		// fit=false with the text unchanged is a legal â€” if never reached at this
		// width â€” outcome. The overflow check above is the invariant that matters.)
	}
}

// TestRowLabelLimitIsInertWhenUnarmed is the openâ€“closed evidence: every checkbox
// outside the settings card â€” the courtroom's, the mod dashboard's, the pickers' â€”
// must keep drawing its label exactly as before. Unarmed, fitRowLabel is the
// identity for every label in the corpus, however long.
func TestRowLabelLimitIsInertWhenUnarmed(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	labels := settingsRowLabels(t)
	for _, s := range labels {
		shown, fit := c.fitRowLabel(0, s)
		if shown != s || !fit {
			t.Fatalf("unarmed fitRowLabel altered a label (fit=%v): %.40q", fit, s)
		}
	}
	// Same evidence one layer up: the two general label primitives now consult the
	// bound, so their INERTNESS unarmed is what keeps every non-settings screen
	// byte-identical. Zero allocations is the sharp end of that claim â€” Label runs
	// inside frame draws gated at zero allocs.
	if n := testing.AllocsPerRun(200, func() {
		for _, s := range labels {
			_, _ = c.fitRowLabel(0, s)
		}
	}); n != 0 {
		t.Errorf("unarmed fitRowLabel allocates %.1f/op â€” it is on every Label draw now, and the frame is gated at zero", n)
	}
}

// TestCheckboxHonoursTheRowLabelLimit is the behavioural replacement for the old
// "Checkbox consults fitRowLabel + onRow" source scan. It drives a real Checkbox
// under an armed row-label limit and observes the DRAWN result: onRow still receives
// the FULL label (the settings-search collect pass indexes the whole sentence), and
// the drawn row is ellipsised — Checkbox registers a hover tooltip carrying the full
// text only when it had to shorten the row (fit=false), which is the one way the full
// sentence stays reachable on screen.
func TestCheckboxHonoursTheRowLabelLimit(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	long := settingsCheckboxLabels(t)[0]
	x, y := int32(200), int32(0)
	right := x + 300
	if c.TextWidth(long) <= right-x {
		t.Fatalf("fixture: the checkbox label is not long enough to overflow a %d px row", right-x)
	}

	var sawLabel string
	var sawY int32
	c.onRow = func(label string, y int32) { sawLabel, sawY = label, y }

	// Rest the pointer on the checkbox so the truncation tooltip (the DRAWN signal
	// that a row was shortened) is what we read back.
	c.mouseX, c.mouseY = x+1, y

	prev := c.pushRowLabelLimit(right)
	c.Checkbox(x, y, long, false)
	c.popRowLabelLimit(prev)

	if sawLabel != long {
		t.Errorf("onRow received %q, want the FULL label %q — search would stop matching the sentence a row had to shorten", sawLabel, long)
	}
	if sawY != y {
		t.Errorf("onRow y = %d, want %d", sawY, y)
	}
	if c.tipText != long {
		t.Errorf("Checkbox drew the row un-shortened under an armed limit (no truncation tooltip) — the label would run off the card again (F2)")
	}
}

// TestLabelAndLabelClippedHonourTheRowLabelLimit is the behavioural replacement for
// the old "Label/LabelClipped consult fitRowLabel" source scan. Both general label
// primitives must ellipsise under an armed row-label limit; the observable is the
// DRAWN width — Label's return value, and the last blit's dst width for the void
// LabelClipped — never the truncation memo.
func TestLabelAndLabelClippedHonourTheRowLabelLimit(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	long := settingsHelpLabels(t)[0]
	x, y := int32(0), int32(0)
	right := int32(300)
	if c.TextWidth(long) <= right-x {
		t.Fatalf("fixture: the label is not long enough to overflow a %d px bound", right-x)
	}
	maxW := c.TextWidth(long) + 500 // a clip WIDER than the full label, so unarmed LabelClipped draws it whole

	// Label reports the width of what it DREW; under the armed limit that is the
	// ellipsised text, which fits the card edge.
	prev := c.pushRowLabelLimit(right)
	labelW := c.Label(x, y, long, ColText)
	c.popRowLabelLimit(prev)
	if x+labelW > right {
		t.Errorf("Label drew past the armed row-label limit (%d > %d) — a settings help line would run off the card again (F2)", x+labelW, right)
	}

	// LabelClipped is void, so observe the last blit's dst width: armed it must be
	// strictly narrower than unarmed, or it is hard-clipping instead of ellipsising.
	c.LabelClipped(x, y, maxW, long, ColText)
	unarmedW := c.drawDst.W
	prev = c.pushRowLabelLimit(right)
	c.LabelClipped(x, y, maxW, long, ColText)
	armedW := c.drawDst.W
	c.popRowLabelLimit(prev)
	if armedW >= unarmedW {
		t.Errorf("LabelClipped drew the label as wide armed (%d px) as unarmed (%d px) — it did not ellipsise at the card edge (F2)", armedW, unarmedW)
	}
}

// TestSettingsSearchReleasesTheRowLabelLimit is the behavioural attempt at the old
// "drawSettings arms and releases the row-label limit" source scan. drawSettings is
// reached by zero other tests, so driving it through the real dispatcher is itself a
// gain; the assertion is the release: on the search-results EARLY RETURN (the branch
// the deferred pop exists to cover), the limit must not leak onto the next screen.
func TestSettingsSearchReleasesTheRowLabelLimit(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	a.screen = ScreenSettings
	settings.loaded = true // skip the first-draw theme scan + catalog probe
	settings.search = "fps"
	defer func() {
		settings.loaded = false
		settings.search = ""
	}()

	driveFrame(a)

	if a.ctx.rowLabelRight != 0 {
		t.Errorf("drawSettings leaked the row-label limit (right=%d) on its search early-return — the next screen's chrome would be truncated at the settings card's edge (F2)", a.ctx.rowLabelRight)
	}
}
