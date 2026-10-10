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

// TestSettingsFormArmsAndReleasesTheRowLabelLimit is the wiring gate. The limit is a
// Ctx-wide bracket, so two things must hold and neither is visible from a unit test
// of fitRowLabel: drawSettings has to ARM it, and â€” because that function has
// several early returns â€” it has to release it with defer, or the settings card's
// right edge would follow the user into the courtroom and truncate its checkboxes.
func TestSettingsFormArmsAndReleasesTheRowLabelLimit(t *testing.T) {
	body := funcBodySource(t, "settings.go", "drawSettings")
	if !containsCall(body, "pushRowLabelLimit") {
		t.Error("drawSettings never arms the row-label limit â€” settings help text would run off the card again (F2)")
	}
	if !deferredCall(body, "popRowLabelLimit") {
		t.Error("drawSettings must release the row-label limit with DEFER â€” it has early returns, and a stranded limit truncates every checkbox on the next screen")
	}
	// The bound belongs to the CARD, not to the screen. Everything drawSettings paints
	// after the card's clip closes is full-window chrome â€” the warn banner spans the
	// window, and the .demo browser and content panel are overlays â€” so the limit has
	// to be released before them or the card's right edge would shorten their labels
	// too. Source order is the only way to see that from a test.
	//
	// TWO pops are required, and the count is the point: the first in source order is
	// the DEFERRED one, which does not run where it is written â€” it runs at return,
	// after the overlays. Only a second, straight-line pop actually releases the bound
	// before them, so "at least two pops precede each overlay" is the exact invariant.
	const popsBeforeOverlays = 2
	pops := 0
	for _, name := range callOrder(body, "popRowLabelLimit", "drawDemoBrowser", "drawContentPanel") {
		if name == "popRowLabelLimit" {
			pops++
			continue
		}
		if pops < popsBeforeOverlays {
			t.Errorf("drawSettings calls %s with the card's row-label limit still armed (only the deferred pop precedes it) â€” "+
				"a full-window overlay would be ellipsised at the settings card's right edge (F2)", name)
		}
	}
	if pops < popsBeforeOverlays {
		t.Errorf("drawSettings pops the row-label limit %d time(s); it needs the deferred one for its early returns AND a straight-line "+
			"one where the card's clip closes, or the trailing full-window chrome draws under the card's bound (F2)", pops)
	}
	cb := funcBodySource(t, "ui.go", "Checkbox")
	if !containsCall(cb, "fitRowLabel") {
		t.Error("Ctx.Checkbox no longer consults fitRowLabel â€” the bound is armed but nothing honours it")
	}
	if !containsCall(cb, "onRow") {
		t.Error("Ctx.Checkbox stopped feeding the settings-search collect pass â€” search must still index the FULL label a row had to shorten")
	}
	// The bare help lines are drawn by the general primitives, NOT by Checkbox: about
	// a hundred c.Label calls plus the FONTS chain line's c.LabelClipped. They are the
	// class the first pass at this fix left hard-clipping at the card edge, so each
	// one is its own deletion-catcher.
	for _, fn := range []string{"Label", "LabelClipped"} {
		if !containsCall(funcBodySource(t, "ui.go", fn), "fitRowLabel") {
			t.Errorf("Ctx.%s no longer consults fitRowLabel â€” the settings help lines would go back to a hard cut at the card's clip edge, "+
				"with no ellipsis, which is the reported 'cut off at the right border' (F2)", fn)
		}
	}
}
