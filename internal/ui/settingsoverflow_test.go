package ui

// F2 — settings help text running past the card's right border.
//
// The report named the FONTS section (the system-fonts line and the font-chain
// line). The census below shows it was never two rows: Ctx.Checkbox drew its label
// at whatever the string measured, with nothing bounding it, and the settings form
// carries dozens of sentence-long help labels. On any window narrower than the
// sentence they all ran off the card — the two the tester happened to be looking at
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
// harvested only the checkboxes could not see the second class at all — which is
// how a hundred-odd 100-to-190-character help lines went on hard-clipping after the
// first fix.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

// settingsCheckboxArg / settingsLabelArg name the LABEL argument's index for each
// chrome draw call the settings form uses. Both tables feed one harvester, so the
// census can be scoped to the checkbox rows alone (the inertness gate) or to every
// bounded label in the card (the overflow gate) without two parsers drifting.
var (
	settingsCheckboxArg = map[string]int{
		"Checkbox":   2, // Checkbox(x, y, label, value)
		"CheckboxIn": 1, // CheckboxIn(r, label, value)
	}
	settingsLabelArg = map[string]int{
		"Checkbox":     2,
		"CheckboxIn":   1,
		"Label":        2, // Label(x, y, text, col)
		"LabelClipped": 3, // LabelClipped(x, y, maxW, text, col)
	}
	// The bare help lines ALONE — the class the checkbox-only census could not see.
	// Harvested separately so the anti-vacuity gate can name it directly instead of
	// inferring it from corpus sizes.
	settingsHelpArg = map[string]int{
		"Label":        2,
		"LabelClipped": 3,
	}
)

// settingsLabelLiterals is every string literal passed as the label argument of one
// of `kinds` in settings.go. Parsed, not grepped, so a label built from a
// concatenation or a variable is skipped rather than mis-harvested.
func settingsLabelLiterals(t *testing.T, kinds map[string]int, wantMin int) []string {
	t.Helper()
	const file = "settings.go"
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		idx, ok := kinds[sel.Sel.Name]
		if !ok || idx >= len(call.Args) {
			return true
		}
		lit, ok := call.Args[idx].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := stringLitValue(lit.Value); err == nil && s != "" {
			out = append(out, s)
		}
		return true
	})
	if len(out) < wantMin {
		t.Fatalf("harvested only %d settings labels (want ≥ %d) — the parse is wrong, so this gate would pass vacuously", len(out), wantMin)
	}
	return out
}

// settingsCheckboxLabels is the checkbox-row corpus.
func settingsCheckboxLabels(t *testing.T) []string {
	t.Helper()
	return settingsLabelLiterals(t, settingsCheckboxArg, settingsMinCheckboxLabels)
}

// settingsRowLabels is the FULL corpus of bounded labels in the settings card —
// checkbox rows and the bare help lines both.
func settingsRowLabels(t *testing.T) []string {
	t.Helper()
	return settingsLabelLiterals(t, settingsLabelArg, settingsMinRowLabels)
}

// settingsHelpLabels is the bare-help-line corpus alone.
func settingsHelpLabels(t *testing.T) []string {
	t.Helper()
	return settingsLabelLiterals(t, settingsHelpArg, settingsMinHelpLabels)
}

// Anti-vacuity floors for the two corpora, and the length that makes a label part
// of the class the first fix missed. settingsHelpLineRunes is deliberately well
// under the longest line in the form (about 190 characters) so ordinary editing
// cannot trip it, but far above any checkbox label, so it can only be satisfied by
// a bare help line actually being in the corpus.
const (
	settingsMinCheckboxLabels = 50
	settingsMinRowLabels      = 120
	settingsMinHelpLabels     = 40
	settingsHelpLineRunes     = 150
)

// stringLitValue unquotes a Go string literal (interpreted or raw).
func stringLitValue(lit string) (string, error) {
	if len(lit) >= 2 && lit[0] == '`' {
		return lit[1 : len(lit)-1], nil
	}
	return strconvUnquote(lit)
}

// TestNoSettingsRowLabelEscapesTheCard is the overflow census. At a NARROW card —
// the settings minimum, which is what a small window resolves to — every label the
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
	// the two corpora's extremes — the longest string in the whole form happens to be a
	// Checkbox label, so "the row corpus's longest must beat the checkbox corpus's"
	// was false even with the help lines present.
	checks := settingsCheckboxLabels(t)
	if len(checks) >= len(labels) {
		t.Fatalf("the row corpus holds %d labels and the checkbox corpus %d — the bare help lines are not being harvested, "+
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
		t.Fatalf("longest harvested BARE help line is %d runes, want at least %d — the sentence-length c.Label / c.LabelClipped "+
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
		t.Fatalf("no settings label overflows a %d px card even unbounded — the fixture is too wide to prove anything", formW)
	}
	t.Logf("settings rows that overrun a %d px card unbounded: %d of %d", formW, overflowed, len(labels))

	// Armed: nothing may escape.
	prev := c.pushRowLabelLimit(right)
	defer c.popRowLabelLimit(prev)
	for _, s := range labels {
		shown, fit := c.fitRowLabel(labelX, s)
		w := c.TextWidth(shown)
		if labelX+w > right {
			t.Errorf("row label still escapes the card by %d px: %.60q…", labelX+w-right, s)
		}
		if fit && shown != s {
			t.Errorf("fitRowLabel reported a fit but changed the text: %.40q", s)
		}
		// (The converse is deliberately not asserted: truncateLabelTo keeps AO2's rule
		// that a label which would collapse to a lone "…" is returned WHOLE, so
		// fit=false with the text unchanged is a legal — if never reached at this
		// width — outcome. The overflow check above is the invariant that matters.)
	}
}

// TestRowLabelLimitIsInertWhenUnarmed is the open–closed evidence: every checkbox
// outside the settings card — the courtroom's, the mod dashboard's, the pickers' —
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
	// byte-identical. Zero allocations is the sharp end of that claim — Label runs
	// inside frame draws gated at zero allocs.
	if n := testing.AllocsPerRun(200, func() {
		for _, s := range labels {
			_, _ = c.fitRowLabel(0, s)
		}
	}); n != 0 {
		t.Errorf("unarmed fitRowLabel allocates %.1f/op — it is on every Label draw now, and the frame is gated at zero", n)
	}
}

// TestSettingsFormArmsAndReleasesTheRowLabelLimit is the wiring gate. The limit is a
// Ctx-wide bracket, so two things must hold and neither is visible from a unit test
// of fitRowLabel: drawSettings has to ARM it, and — because that function has
// several early returns — it has to release it with defer, or the settings card's
// right edge would follow the user into the courtroom and truncate its checkboxes.
func TestSettingsFormArmsAndReleasesTheRowLabelLimit(t *testing.T) {
	body := funcBodySource(t, "settings.go", "drawSettings")
	if !containsCall(body, "pushRowLabelLimit") {
		t.Error("drawSettings never arms the row-label limit — settings help text would run off the card again (F2)")
	}
	if !deferredCall(body, "popRowLabelLimit") {
		t.Error("drawSettings must release the row-label limit with DEFER — it has early returns, and a stranded limit truncates every checkbox on the next screen")
	}
	// The bound belongs to the CARD, not to the screen. Everything drawSettings paints
	// after the card's clip closes is full-window chrome — the warn banner spans the
	// window, and the .demo browser and content panel are overlays — so the limit has
	// to be released before them or the card's right edge would shorten their labels
	// too. Source order is the only way to see that from a test.
	//
	// TWO pops are required, and the count is the point: the first in source order is
	// the DEFERRED one, which does not run where it is written — it runs at return,
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
			t.Errorf("drawSettings calls %s with the card's row-label limit still armed (only the deferred pop precedes it) — "+
				"a full-window overlay would be ellipsised at the settings card's right edge (F2)", name)
		}
	}
	if pops < popsBeforeOverlays {
		t.Errorf("drawSettings pops the row-label limit %d time(s); it needs the deferred one for its early returns AND a straight-line "+
			"one where the card's clip closes, or the trailing full-window chrome draws under the card's bound (F2)", pops)
	}
	cb := funcBodySource(t, "ui.go", "Checkbox")
	if !containsCall(cb, "fitRowLabel") {
		t.Error("Ctx.Checkbox no longer consults fitRowLabel — the bound is armed but nothing honours it")
	}
	if !containsCall(cb, "onRow") {
		t.Error("Ctx.Checkbox stopped feeding the settings-search collect pass — search must still index the FULL label a row had to shorten")
	}
	// The bare help lines are drawn by the general primitives, NOT by Checkbox: about
	// a hundred c.Label calls plus the FONTS chain line's c.LabelClipped. They are the
	// class the first pass at this fix left hard-clipping at the card edge, so each
	// one is its own deletion-catcher.
	for _, fn := range []string{"Label", "LabelClipped"} {
		if !containsCall(funcBodySource(t, "ui.go", fn), "fitRowLabel") {
			t.Errorf("Ctx.%s no longer consults fitRowLabel — the settings help lines would go back to a hard cut at the card's clip edge, "+
				"with no ellipsis, which is the reported 'cut off at the right border' (F2)", fn)
		}
	}
}
