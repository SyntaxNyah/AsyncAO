package ui

// Encapsulation tests for the text-seam fix (v1.93.0): a named log row's
// selection/highlight math now shares drawLogLineNamed's logRowSplit gate
// instead of re-deriving (and, before this, mismeasuring) it.
//
// Two user reports landed here:
//   - OOC "you have to select empty space past the text to select it all" —
//     logPrefixWidth always measured a row at PLAIN weight while
//     drawLogLineNamed drew its speaker BOLD, so the true drawn width was
//     under-measured by the bold/plain advance delta on every named row.
//   - the funky-fonts report's one CONFIRMED defect: drawLogLineNamed could
//     draw a row's timestamp prefix, then fall through to redraw the WHOLE
//     line on top of it if the speaker's own width lookup ever failed — a
//     partial draw committed before the split was known to succeed.
//
// Every test below drives a REAL production entry point (logPointAt,
// drawLogSelHighlight, logPrefixWidth itself, or drawLogLineNamed's own
// source) — none re-implements the split/measure logic to check itself.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// oocRowSelectionFixture builds a headless App with a real font-loaded Ctx (via
// scaleTestApp) and one OOC entry with a speaker, wrapped at a width wide enough
// that the entry never splits across rows — so display row 0 IS the whole entry
// and a rune offset into it maps 1:1 onto the source text. BoldNamesOn is left at
// its real default (ON, config.AssetPreferences.BoldNamesOn): the report this
// pins happens on a stock install with nothing touched in Settings.
func oocRowSelectionFixture(t *testing.T, text, speaker string) (a *App, wide int32) {
	t.Helper()
	a = scaleTestApp(t)
	a.oocPct = DefaultScalePct
	a.oocLog = []string{text}
	a.oocSpeakers = []string{speaker}
	a.oocSeq++
	const fixtureWidth = 4000 // wider than any fixture string can wrap
	lines := a.oocWrapped(fixtureWidth - logWrapIndentPx)
	if len(lines) != 1 || lines[0] != text {
		t.Fatalf("fixture must wrap to exactly one row equal to the source text, got %d rows: %v", len(lines), lines)
	}
	if len(a.oocWrapName) != 1 || a.oocWrapName[0] != speaker {
		t.Fatalf("fixture's speaker didn't reach oocWrapName: got %v, want [%q]", a.oocWrapName, speaker)
	}
	return a, fixtureWidth
}

// TestLogPrefixWidthUsesBoldWeightForNamedOOCRow is the direct proof for the OOC
// report: at the rune offset ending a bold-drawn speaker, logPrefixWidth must
// return the width the BOLD glyphs actually occupy (what drawLogLineNamed draws),
// not the plain width the old code always measured.
func TestLogPrefixWidthUsesBoldWeightForNamedOOCRow(t *testing.T) {
	const speaker = "Wright"
	const line = "Wright: hold it, that's not how it happened"
	a, _ := oocRowSelectionFixture(t, line, speaker)
	c := a.ctx
	if !a.d.Prefs.BoldNamesOn() {
		t.Fatal("fixture assumes the real BoldNamesOn default (ON) — this report reproduces on a stock install")
	}

	font := a.logRowFont(logSelOOC, line) // the SAME font resolution logPointAt/drawLogSelHighlight use
	runes := []rune(line)
	nameEndRunes := len([]rune(speaker))

	got := a.logPrefixWidth(logSelOOC, 0, line, font, runes, nameEndRunes)
	wantBold, ok := c.fontTextWidthWeight(font, speaker, true) // the width drawLogLineNamed's bold raster occupies
	if !ok {
		t.Fatal("could not measure the bold speaker width")
	}
	if got != wantBold {
		t.Errorf("logPrefixWidth(off=len(speaker)) = %d, want the BOLD width %d", got, wantBold)
	}
	if plain, _ := c.fontTextWidthWeight(font, speaker, false); got == plain {
		t.Errorf("logPrefixWidth returned the PLAIN width (%d) for a row whose speaker draws BOLD — "+
			"this is the exact bug: plain undershoots the true glyph width, so a click has to travel "+
			"past the drawn text into empty space before hitTestRune concludes the row is fully covered", plain)
	}
}

// TestLogPrefixWidthCoversTheRowsTrueDrawnWidthAtFullSelection is the direct
// proof behind drawLogSelHighlight's rectangle: a FULL-ROW selection
// (off == len(runes)) is drawn out to logPrefixWidth(off), so if that
// measurement falls short of the row's TRUE drawn width (bold speaker + plain
// remainder), the highlight box stops short of the last few glyphs — and
// because hitTestRune's off can never exceed len(runes), no amount of
// dragging further right can ever close that gap once the (buggy) plain
// measurement of the whole line has already been reached. This is the
// code-level shape of "you have to select empty space past the text to
// select it all": the box visibly stops before the text really ends, and
// nothing the user does with the mouse can make it catch up.
func TestLogPrefixWidthCoversTheRowsTrueDrawnWidthAtFullSelection(t *testing.T) {
	const speaker = "Wright"
	const line = "Wright: hold it"
	a, _ := oocRowSelectionFixture(t, line, speaker)
	c := a.ctx
	font := a.logRowFont(logSelOOC, line)
	runes := []rune(line)

	// The row's TRUE drawn width: the bold speaker plus the plain remainder — the
	// exact two segments drawLogLineNamed draws it in.
	boldW, ok := c.fontTextWidthWeight(font, speaker, true)
	if !ok {
		t.Fatal("could not measure the bold speaker width")
	}
	restW, ok := c.fontTextWidthWeight(font, line[len(speaker):], false)
	if !ok {
		t.Fatal("could not measure the plain remainder width")
	}
	trueRight := boldW + restW

	got := a.logPrefixWidth(logSelOOC, 0, line, font, runes, len(runes))
	if got != trueRight {
		t.Errorf("logPrefixWidth(off=len(runes)) = %d, want the row's TRUE drawn width %d (bold speaker + plain remainder) — "+
			"a full-row selection highlight is drawn out to exactly this value", got, trueRight)
	}
}

// TestLogPointAtDoesNotReportFullSelectionBeforeTheRowsTrueRightEdge drives the
// actual mouse→rune entry point (logPointAt). Between the OLD (plain) whole-line
// measurement and the row's TRUE drawn width there is a band of x positions that
// are still visibly over the row's bold-widened glyphs but which the plain
// measurement already (wrongly) considered "past the end of the line" — so the
// old code reported the whole row selected there, capping off at len(runes)
// with visible text still to its right, and no further drag could ever move it.
// The fix must not report the row fully selected until x actually reaches the
// true right edge.
func TestLogPointAtDoesNotReportFullSelectionBeforeTheRowsTrueRightEdge(t *testing.T) {
	const speaker = "Wright"
	const line = "Wright: hold it"
	a, wide := oocRowSelectionFixture(t, line, speaker)
	c := a.ctx
	font := a.logRowFont(logSelOOC, line)
	runes := []rune(line)

	boldW, ok := c.fontTextWidthWeight(font, speaker, true)
	if !ok {
		t.Fatal("could not measure the bold speaker width")
	}
	restW, ok := c.fontTextWidthWeight(font, line[len(speaker):], false)
	if !ok {
		t.Fatal("could not measure the plain remainder width")
	}
	trueRight := boldW + restW
	plainRight, _ := c.fontTextWidthWeight(font, line, false)

	// Fixture sanity: there must be room strictly between the two measurements,
	// or bold isn't actually wider here and this test can't distinguish the
	// fixed behaviour from the bug.
	if plainRight+1 >= trueRight {
		t.Fatalf("fixture invalid: need trueRight (%d) to exceed plainRight (%d) by more than one pixel", trueRight, plainRight)
	}
	midpoint := plainRight + (trueRight-plainRight)/2 // strictly between the two by construction above

	const listX, listY, lineH = 10, 10, int32(20)
	list := sdl.Rect{X: listX, Y: listY, W: wide, H: 200}

	if p := a.logPointAt(logSelOOC, list.X, list.Y, 0, lineH, list.X+midpoint, list.Y+lineH/2); p.off == len(runes) {
		t.Errorf("logPointAt at x=%d (still short of the true right edge %d) already reports the whole row selected (off=%d) — "+
			"this is the OLD plain-only measurement's threshold (%d), not the row's real drawn width", midpoint, trueRight, p.off, plainRight)
	}
	if p := a.logPointAt(logSelOOC, list.X, list.Y, 0, lineH, list.X+trueRight, list.Y+lineH/2); p.off != len(runes) {
		t.Errorf("logPointAt at the row's true drawn right edge (x=%d) returned off=%d, want %d (the whole row)", trueRight, p.off, len(runes))
	}
}

// TestDrawLogLineNamedGatesEveryDrawOnLogRowSplit is the deletion-catcher for the
// funky-fonts double-draw defect: it reads drawLogLineNamed's PRODUCTION source
// and requires logRowSplit — the function that proves BOTH the prefix and the
// speaker will measure — to be the FIRST relevant call in the function body. A
// LabelClippedFontWeight / LabelClippedFont / labelEmoji draw ahead of it would
// mean some segment can be painted before the code has committed to (or ruled
// out) the split, which is exactly the shape of the old bug: the timestamp
// prefix drew, then a failed speaker-width lookup fell through to redraw the
// WHOLE line on top of it via the plain-weight fallback.
func TestDrawLogLineNamedGatesEveryDrawOnLogRowSplit(t *testing.T) {
	body := funcBodySource(t, "logselect_wire.go", "drawLogLineNamed")
	order := callOrder(body, "logRowSplit", "LabelClippedFontWeight", "LabelClippedFont", "labelEmoji")
	if len(order) == 0 {
		t.Fatal("drawLogLineNamed calls none of logRowSplit / LabelClippedFontWeight / LabelClippedFont / labelEmoji — the gate would pass vacuously")
	}
	if order[0] != "logRowSplit" {
		t.Errorf("drawLogLineNamed's first relevant call is %s, want logRowSplit — "+
			"a draw call ahead of the split decision can commit a partial draw before the code "+
			"knows whether the rest of the split will succeed (the funky-fonts double-draw)", order[0])
	}
}

// TestLogPrefixWidthUsesBoldWeightForNamedICRow covers the IC twin of the OOC
// fix: IC's speaker split is additionally gated on NameColorsOn (screens.go),
// so this turns it on explicitly rather than relying on BoldNamesOn alone.
func TestLogPrefixWidthUsesBoldWeightForNamedICRow(t *testing.T) {
	a := scaleTestApp(t)
	a.logPct = DefaultScalePct
	a.d.Prefs.SetNameColors(true) // IC's split additionally requires this (OOC's doesn't — see logRowNameStyle)
	const speaker = "Wright"
	a.icLog = []icEntry{{text: "Wright: hold it, that's not how it happened", speaker: speaker}}
	a.icLogSeq++
	const fixtureWidth = 4000
	rows := a.icWrapped(fixtureWidth-logWrapIndentPx, false)
	if len(rows) != 1 {
		t.Fatalf("fixture must not wrap, got %d rows", len(rows))
	}

	c := a.ctx
	font := a.logRowFont(logSelIC, rows[0].text)
	runes := []rune(rows[0].text)
	nameEndRunes := len([]rune(speaker))

	got := a.logPrefixWidth(logSelIC, 0, rows[0].text, font, runes, nameEndRunes)
	want, ok := c.fontTextWidthWeight(font, speaker, true)
	if !ok {
		t.Fatal("could not measure the bold speaker width")
	}
	if got != want {
		t.Errorf("IC logPrefixWidth(off=len(speaker)) = %d, want the BOLD width %d", got, want)
	}
}

// --- v1.94.0: the MOTD-selection offset -------------------------------------------
//
// User report: dragging a selection over a wrapped continuation row highlighted
// "see fit." but Ctrl+C copied "u see fi" — same length, shifted ~2 characters
// left. Root cause: logPrefixWidth measured a row's selection width by calling
// fontTextWidthWeight, which runs SizeUTF8 on the LOGICAL font directly. The row
// is actually DRAWN on deviceTextFont(font) (textTextureBold), then folded back
// to logical with uiLogicalFromDevice (cachedText.logicalW(), the same call
// blitLabel paints from). Those are two independently-hinted FreeType
// rasterizations at two different point sizes: they agree bit-for-bit only at
// textDevPct==100 (deviceTextFont is the identity there) and drift apart, GROWING
// with prefix length, at every fractional UI scale — the default condition for
// most users (UIScaleAuto ships ON). It is not MOTD-specific machinery: IC and
// OOC share logPrefixWidth verbatim: MOTD is simply the longest text most logs
// ever contain, so it's the first place the drift crosses a whole character.
//
// The fix routes logPrefixWidth through devTextWidthWeight — the SAME two steps
// (device-face SizeUTF8, uiLogicalFromDevice fold) the draw performs — instead of
// fontTextWidthWeight's logical-only SizeUTF8. At textDevPct==100 the two are
// provably identical (deviceTextFont(font)==font, uiLogicalFromDevice is the
// identity), which is why every existing test above this comment — none of which
// sets a fractional UI scale — is untouched evidence that the fix changes
// NOTHING at the default scale.
//
// fractionalScalePct is 105%: config.MinAutoUIScalePercent + UIScaleStepPercent,
// the FIRST step UIScaleAuto's own detection leaves 100% at (scaleblur_test.go's
// autoScaleStepW/H), not an arbitrary probe value.
const fractionalScalePct = config.MinAutoUIScalePercent + config.UIScaleStepPercent

// motdSelectionFixtureLine is long enough (over 100 runes) that a per-glyph
// rounding drift of a fraction of a pixel per character accumulates past one
// whole character by the tested offsets — both recon probes measured the drift
// GROWING with prefix length and needed 30-40+ runes before it became visible.
const motdSelectionFixtureLine = "You may present your evidence to the court whenever you feel it necessary, and you may use it in any area you see fit."

// drawnPrefixWidth is the row's TRUE on-screen width for its first n runes,
// read off the PRODUCTION draw function itself (textTextureBold — the exact
// call LabelClippedFontWeight/labelEmoji make) rather than any reimplementation
// of the width math: cachedText.logicalW() is precisely the quantity blitLabel
// blits (ui.go:2911-2928's own doc comment).
func drawnPrefixWidth(t *testing.T, c *Ctx, font *ttf.Font, runes []rune, n int) int32 {
	t.Helper()
	tex, ok := c.textTextureBold(string(runes[:n]), ColText, font, false)
	if !ok {
		t.Fatalf("textTextureBold failed to rasterize a %d-rune prefix", n)
	}
	return tex.logicalW()
}

// TestLogPrefixWidthMatchesTheActualDrawAtFractionalScale is the direct
// regression pin for the MOTD-selection offset: logPrefixWidth must return the
// SAME number the row's own draw produces, not a second, independently-hinted
// estimate of it. At 100% (see the companion tests above, none of which set a
// scale) the two measurements collapse to the identical call; this test is what
// forces them apart by setting a real fractional UI scale and comparing against
// the production draw as ground truth.
//
// Deleting the fix (routing logPrefixWidth's width lookups back through
// fontTextWidthWeight instead of devTextWidthWeight) reproduces the drift this
// asserts against — confirmed by running this test against the unfixed tree
// before adding the fix (see the implementer's report).
func TestLogPrefixWidthMatchesTheActualDrawAtFractionalScale(t *testing.T) {
	a, _ := oocRowSelectionFixture(t, motdSelectionFixtureLine, "") // no speaker: the MOTD continuation-row shape
	c := a.ctx
	c.SetUIScale(fractionalScalePct)

	font := a.logRowFont(logSelOOC, motdSelectionFixtureLine) // the SAME resolution logPointAt/drawLogSelHighlight use
	runes := []rune(motdSelectionFixtureLine)

	// A short prefix, so the long-prefix assertion below has a same-fixture
	// baseline: per both dossiers' probes the drift GROWS with prefix length,
	// so this one is expected to be small but is not asserted to be zero (a
	// live run against the unfixed code measured 2px of drift here too).
	const shortOff = 8
	if got, want := a.logPrefixWidth(logSelOOC, 0, motdSelectionFixtureLine, font, runes, shortOff),
		drawnPrefixWidth(t, c, font, runes, shortOff); abs32(got-want) > 1 {
		t.Errorf("logPrefixWidth(off=%d) = %d, want the drawn width %d (±1px)", shortOff, got, want)
	}

	// A long prefix, deep enough for the per-glyph drift to exceed a whole
	// character — the assertion that fails on the unfixed code.
	longOff := len(runes) - 4
	got, want := a.logPrefixWidth(logSelOOC, 0, motdSelectionFixtureLine, font, runes, longOff), drawnPrefixWidth(t, c, font, runes, longOff)
	if abs32(got-want) > 1 {
		t.Errorf("logPrefixWidth(off=%d) = %d, want the drawn width %d (±1px) at %d%% UI scale — "+
			"the selection measurement has drifted from the actual glyph raster (the MOTD-selection offset)",
			longOff, got, want, fractionalScalePct)
	}
}

// TestLogPrefixWidthMatchesTheActualDrawAtFractionalScaleIC is the IC twin: the
// adversarial recon explicitly established this is NOT MOTD- or OOC-specific —
// IC and OOC share logPrefixWidth verbatim, gated on the same devPct — so a fix
// (or a test) that only covers OOC would leave IC's identical selection bug
// unpinned. NameColorsOn defaults OFF, so this IC row takes the same no-split
// plain-weight path the OOC MOTD line does.
func TestLogPrefixWidthMatchesTheActualDrawAtFractionalScaleIC(t *testing.T) {
	a := scaleTestApp(t)
	a.logPct = DefaultScalePct
	a.icLog = []icEntry{{text: motdSelectionFixtureLine, speaker: "Wright"}}
	a.icLogSeq++
	const fixtureWidth = 4000
	rows := a.icWrapped(fixtureWidth-logWrapIndentPx, false)
	if len(rows) != 1 {
		t.Fatalf("fixture must not wrap, got %d rows", len(rows))
	}
	c := a.ctx
	c.SetUIScale(fractionalScalePct)

	font := a.logRowFont(logSelIC, rows[0].text)
	runes := []rune(rows[0].text)

	longOff := len(runes) - 4
	got, want := a.logPrefixWidth(logSelIC, 0, rows[0].text, font, runes, longOff), drawnPrefixWidth(t, c, font, runes, longOff)
	if abs32(got-want) > 1 {
		t.Errorf("IC logPrefixWidth(off=%d) = %d, want the drawn width %d (±1px) at %d%% UI scale",
			longOff, got, want, fractionalScalePct)
	}
}

// TestLogSelectCopyRecoversTheHighlightedSpanAtFractionalScale drives the mouse
// hit-test (logPointAt) and the copy assembly (selectedText fed by logLineText —
// the exact call shape handleLogSelect's Ctrl+C handler uses at
// logselect_wire.go) together, seeded with REAL pixel positions read off the
// row's own production draw (drawnPrefixWidth), not hand-picked coordinates: it
// asks "if the user dragged from the pixel where rune loTrue is actually drawn
// to the pixel where rune hiTrue is actually drawn, does the clipboard receive
// that exact span?"
//
// Before the fix, logPointAt's hit-test (hitTestRune, measuring through the same
// drifted logPrefixWidth the highlight rect used) reached a given on-screen pixel
// at a SMALLER rune count than the real glyph advances did (measW grows faster
// per rune than the true raster — both recon probes), so the recovered offset
// sat to the LEFT of the pixel the user actually dragged to — same direction,
// same shape as the report ("u see fi" copied vs. "see fit." highlighted).
func TestLogSelectCopyRecoversTheHighlightedSpanAtFractionalScale(t *testing.T) {
	a, _ := oocRowSelectionFixture(t, motdSelectionFixtureLine, "")
	c := a.ctx
	c.SetUIScale(fractionalScalePct)

	font := a.logRowFont(logSelOOC, motdSelectionFixtureLine)
	runes := []rune(motdSelectionFixtureLine)

	const loTrue, hiTrue = 30, 100 // deep enough for the drift to exceed one rune (both probes)
	if hiTrue >= len(runes) {
		t.Fatalf("fixture too short: need > %d runes, got %d", hiTrue, len(runes))
	}
	xLo := drawnPrefixWidth(t, c, font, runes, loTrue)
	xHi := drawnPrefixWidth(t, c, font, runes, hiTrue)

	const listX, listY, lineH = int32(10), int32(10), int32(20)
	lo := a.logPointAt(logSelOOC, listX, listY, 0, lineH, listX+xLo, listY+lineH/2)
	hi := a.logPointAt(logSelOOC, listX, listY, 0, lineH, listX+xHi, listY+lineH/2)

	if d := lo.off - loTrue; d < -1 || d > 1 {
		t.Errorf("logPointAt at the pixel where rune %d is actually drawn returned off=%d, want %d (±1) — "+
			"the hit-test has drifted from the real glyph position", loTrue, lo.off, loTrue)
	}
	if d := hi.off - hiTrue; d < -1 || d > 1 {
		t.Errorf("logPointAt at the pixel where rune %d is actually drawn returned off=%d, want %d (±1) — "+
			"the hit-test has drifted from the real glyph position", hiTrue, hi.off, hiTrue)
	}

	// The exact call handleLogSelect's Ctrl+C handler makes.
	loP, hiP := orderSel(lo, hi)
	copied := selectedText(func(i int) string { return a.logLineText(logSelOOC, i) }, loP, hiP)
	want := string(runes[loTrue:hiTrue])
	if copied != want {
		t.Errorf("copied selection = %q, want %q (the substring under the ACTUAL drawn glyphs at the dragged pixels)", copied, want)
	}
}
