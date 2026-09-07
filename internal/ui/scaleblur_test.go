package ui

// F1 / F1b — the "everything above 1345x840 goes blurry" report, and the smeared
// speaker names inside it.
//
// The report named a threshold to the pixel, and the arithmetic matches it exactly:
// autoScaleRefWidth/Height are 1280x800 and the auto scale snaps to
// config.UIScaleStepPercent, so 1280x1.05 = 1344 and 800x1.05 = 840 are the first
// window sizes that leave 100%. Everything the frame then draws is multiplied by
// 1.05 by the renderer, and any surface still rasterized at 100% is stretched.
//
// WHAT THESE GATES PIN.
//
//   - The text pipeline re-rasterizes AT the new scale when that step is crossed
//     (TestAutoScaleStepRerastersTextAtDeviceScale). This is #77 Part A's wiring —
//     SetAutoScaleFromWindow → Ctx.SetUIScale → Ctx.SetTextDevScale — and deleting
//     any link in it turns every glyph in the client into an upscaled 100% raster.
//     Nothing tested it at the threshold before.
//   - Weight is rasterized into the glyphs, not painted twice
//     (TestBoldLabelIsOneRasterNotTwoDraws + TestNoFauxBoldSecondPassRemains). The
//     faux-bold idiom drew the same texture again one LOGICAL pixel to the right;
//     at 105% the renderer put the copies 1.05 device px apart on different
//     sub-pixel phases, which is why names read DOUBLED beside message text on the
//     same row that stayed crisp.
//   - The device-exact blit (DrawScaled) reaches EVERY live emoji/mixed-script text
//     surface, not just the chatbox body it started on
//     (TestMessageRasterDrawIsExportOnly, TestLabelCoveringCenteredDeviceExactAtFractionalScale).
//     labelEmojiWeight/labelCoveringCentered and the text field's emoji-fallback
//     branch used to call the plain, non-device-exact Draw — the raster's spans were
//     already built from device-sibling fonts (emojiRasterWeight/fieldRaster fold
//     textDevPct), but blitting them through Draw threw that precision away by
//     projecting back to logical BEFORE ren.SetScale multiplied it up again, the
//     exact rounding mismatch deviceExact's own doc measures for the chatbox crawl.
//   - Floating reaction badges (#2) rasterize at the device scale and blit
//     device-exact too (TestEnsureReactBadgeBuildsFromDeviceFont,
//     TestReactionBadgeCachePurgesOnDeviceScaleChange,
//     TestBadgeDeviceExactDiffersFromResampleAtFractionalScale in
//     internal/render/badge_test.go). ensureReactBadge used to build from EmojiFont
//     (a fixed logical pixel size) and Badge.Draw had no SetScale bracket at all, so
//     every float softened at any UI scale other than 100% — and the badge cache
//     never invalidated on a scale change, so ensureReactBadge now purges it on a
//     textDevPct mismatch (the same idiom SetTextDevScale uses for the label/emoji
//     caches).

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// autoScaleStepW / autoScaleStepH are the FIRST window size that leaves 100%,
// derived from the production constants rather than typed as 1344x840 — a test that
// hardcoded the tester's numbers would go quiet the day the reference size moved.
// (config.MinAutoUIScalePercent is the auto floor, 100 — NOT MinUIScalePercent,
// which is the 75% floor of the MANUAL slider and never governs this path.)
var (
	autoScaleStepW = int32(autoScaleRefWidth) * (config.MinAutoUIScalePercent + config.UIScaleStepPercent) / config.MinAutoUIScalePercent
	autoScaleStepH = int32(autoScaleRefHeight) * (config.MinAutoUIScalePercent + config.UIScaleStepPercent) / config.MinAutoUIScalePercent
)

// scaleTestApp is testTabApp with a REAL Ctx (fonts + renderer), which the device
// font rebuild needs.
func scaleTestApp(t *testing.T) *App {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	t.Cleanup(cleanup)
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	t.Cleanup(c.Destroy)
	a := testTabApp(t)
	a.ctx = c
	return a
}

func TestAutoScaleStepRerastersTextAtDeviceScale(t *testing.T) {
	a := scaleTestApp(t)
	c := a.ctx
	a.d.Prefs.SetUIScaleAuto(true)

	// One pixel UNDER the step on both axes: still 100%, still 1:1, nothing stretched.
	a.SetAutoScaleFromWindow(autoScaleStepW-1, autoScaleStepH-1)
	if a.detectedScalePct != config.MinAutoUIScalePercent {
		t.Fatalf("window %dx%d detected %d%%, want %d%% — the step moved",
			autoScaleStepW-1, autoScaleStepH-1, a.detectedScalePct, config.MinAutoUIScalePercent)
	}
	if c.textDevPct != int32(config.MinAutoUIScalePercent) {
		t.Fatalf("textDevPct = %d at 100%% UI scale, want %d", c.textDevPct, config.MinAutoUIScalePercent)
	}

	// A label cached at 100%, and a measured width, so we can prove the step really
	// invalidates the rasters rather than merely moving a number.
	const probe = "The witness may step down."
	if _, ok := c.textTexture(probe, ColText, c.font); !ok {
		t.Fatal("could not cache a probe label at 100%")
	}
	c.widthCache[probe] = c.TextWidth(probe)

	// The step itself. The renderer is about to draw the whole frame at 105%; the
	// fonts MUST be reopened at 105% or every glyph on screen becomes a 100% raster
	// stretched up — which is the reported blur.
	a.SetAutoScaleFromWindow(autoScaleStepW, autoScaleStepH)
	want := config.MinAutoUIScalePercent + config.UIScaleStepPercent
	if a.detectedScalePct != want {
		t.Fatalf("window %dx%d detected %d%%, want %d%%", autoScaleStepW, autoScaleStepH, a.detectedScalePct, want)
	}
	if int(c.textDevPct) != a.detectedScalePct {
		t.Errorf("textDevPct = %d but the frame draws at %d%% — text would rasterize at one scale and blit at another (the #77 wiring is cut)",
			c.textDevPct, a.detectedScalePct)
	}
	if _, ok := c.textCache[textKey{text: probe, color: ColText, font: c.font}]; ok {
		t.Error("the 100% label texture survived the scale step — it would be stretched, not re-rasterized")
	}
	if _, ok := c.widthCache[probe]; ok {
		t.Error("the 100% width memo survived the scale step — layout would measure the old face")
	}
}

// TestBoldLabelIsOneRasterNotTwoDraws pins F1b's mechanism: a bold label is its own
// cached texture at its own measured width, so the draw is ONE device-exact blit.
// The weight must also never leak onto the shared face — every other label drawn
// after a bold one would silently thicken.
func TestBoldLabelIsOneRasterNotTwoDraws(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	const speaker = "Miles Edgeworth"
	plain, ok := c.textTextureBold(speaker, ColText, c.font, false)
	if !ok {
		t.Fatal("plain label did not rasterize")
	}
	boldT, ok := c.textTextureBold(speaker, ColText, c.font, true)
	if !ok {
		t.Fatal("bold label did not rasterize")
	}
	if boldT.w <= plain.w {
		t.Errorf("bold texture is %d px wide, plain is %d — SDL_ttf's emboldening must widen the glyphs, or the weight never reached the raster",
			boldT.w, plain.w)
	}
	// Two ENTRIES, so a bold name and the same word drawn plain elsewhere on screen
	// cannot share one texture and settle each other's weight.
	if _, ok := c.textCache[textKey{text: speaker, color: ColText, font: c.font, bold: false}]; !ok {
		t.Error("the plain entry is missing from the label cache")
	}
	if _, ok := c.textCache[textKey{text: speaker, color: ColText, font: c.font, bold: true}]; !ok {
		t.Error("the bold entry is missing from the label cache — bold is not part of the key")
	}
	if got := c.font.GetStyle(); got != ttf.STYLE_NORMAL {
		t.Errorf("the chrome face was left at style %d after a bold label — the weight leaks into every plain label drawn next", got)
	}

	// The measurement twin: the log places the message at name-start + name-WIDTH, so
	// a bold name measured plain would slide the message onto its last glyph.
	pw, okp := c.fontTextWidthWeight(c.font, speaker, false)
	bw, okb := c.fontTextWidthWeight(c.font, speaker, true)
	if !okp || !okb {
		t.Fatalf("weighted measure failed: plain ok=%v bold ok=%v", okp, okb)
	}
	if bw <= pw {
		t.Errorf("fontTextWidthWeight(bold) = %d <= plain %d — the bold row would be laid out with plain advances", bw, pw)
	}
	if got := c.font.GetStyle(); got != ttf.STYLE_NORMAL {
		t.Errorf("the chrome face was left at style %d after a bold MEASURE", got)
	}
	// And the plain call is still byte-identical to what it always was.
	if again, _ := c.fontTextWidth(c.font, speaker); again != pw {
		t.Errorf("fontTextWidth disagrees with fontTextWidthWeight(false): %d vs %d", again, pw)
	}
}

// TestNoFauxBoldSecondPassRemains is the deletion-catcher for the CLASS, not for one
// site. The removed idiom has a shape:
//
//	if <something Bold> {
//	    <label draw at x+1>
//	}
//	<the same label draw at x>
//
// so the gate is: no `if` whose condition mentions a bold flag may have a body that
// is nothing but a single label draw. Re-adding a faux-bold pass anywhere in the
// package — including in a file that does not exist yet — reproduces exactly that
// shape and fails here. It reads the PRODUCTION sources, so it cannot be satisfied
// by a test-local re-implementation.
func TestNoFauxBoldSecondPassRemains(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob the package: %v (%d files)", err, len(files))
	}
	labelDraws := map[string]bool{
		"Label": true, "LabelClipped": true, "LabelClippedFont": true,
		"LabelClippedFontAlpha": true, "labelEmoji": true, "labelName": true,
		"labelNameElem": true, "labelCoveringCentered": true,
	}
	scanned := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		ast.Inspect(file, func(n ast.Node) bool {
			ifs, ok := n.(*ast.IfStmt)
			if !ok || ifs.Body == nil || len(ifs.Body.List) != 1 {
				return true
			}
			if !mentionsBold(ifs.Cond) {
				return true
			}
			es, ok := ifs.Body.List[0].(*ast.ExprStmt)
			if !ok {
				return true
			}
			call, ok := es.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !labelDraws[sel.Sel.Name] {
				return true
			}
			t.Errorf("%s:%d — a bold-gated block whose whole body is %s(...) is the faux-bold second pass. "+
				"Weight belongs in the raster (LabelClippedFontWeight / labelEmojiWeight): a 1 px LOGICAL offset "+
				"is multiplied by the UI scale, and the two copies then land on different sub-pixel phases (F1b).",
				name, fset.Position(ifs.Pos()).Line, sel.Sel.Name)
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no production files — the gate would pass vacuously")
	}
}

// mentionsBold reports whether an expression names a bold flag: the identifier
// itself, a field/method whose name ends in "Bold", or either side of a || / &&.
func mentionsBold(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Ident:
			if strings.HasSuffix(v.Name, "Bold") || v.Name == "bold" {
				found = true
			}
		case *ast.SelectorExpr:
			if strings.HasSuffix(v.Sel.Name, "Bold") {
				found = true
			}
		}
		return !found
	})
	return found
}

// TestWeightedLabelMatchesPlainWhenNotBold is the open–closed evidence for the new
// seam: every existing caller passes bold=false, and at bold=false the weighted
// entry point must produce the identical cached texture the old one did. If it
// didn't, "no visual change for non-bold surfaces" would be a claim rather than a
// fact.
func TestWeightedLabelMatchesPlainWhenNotBold(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	const probe = "Court is now in session."
	col := sdl.Color{R: 200, G: 210, B: 220, A: 255}
	viaPlain, ok1 := c.textTexture(probe, col, c.font)
	viaWeight, ok2 := c.textTextureBold(probe, col, c.font, false)
	if !ok1 || !ok2 {
		t.Fatalf("rasterize failed: plain=%v weighted=%v", ok1, ok2)
	}
	if viaPlain.tex != viaWeight.tex || viaPlain.src != viaWeight.src || viaPlain.w != viaWeight.w {
		t.Error("textTextureBold(false) returned a DIFFERENT texture than textTexture — the unweighted path must stay byte-identical")
	}
}

// exportOnlyDrawFiles are the two passes that legitimately keep MessageRaster's plain,
// non-device-exact Draw: comicexport.go and gifexport.go render at a resolution the
// live UI scale must never leak into (Draw's own doc comment: "Draw keeps the scaled
// projection, which is what the offscreen/export passes want"). Every other file must
// reach a message/emoji/field raster through DrawScaled instead.
var exportOnlyDrawFiles = map[string]bool{
	"comicexport.go": true,
	"gifexport.go":   true,
}

// TestMessageRasterDrawIsExportOnly is the deletion-catcher for the WHOLE Draw-vs-
// DrawScaled class (#1), not just the three call sites this fix touched
// (labelEmojiWeight, labelCoveringCentered, the text field's emoji-fallback branch):
// any call named Draw with MessageRaster.Draw's exact 4-argument shape
// (ren, visibleRunes, x, y — DrawScaled's own shape tacks on renderPct and clip, so it
// can never collide with this count) outside the two export passes throws away the
// device precision emojiRaster/fieldRaster already bake into their fonts and
// reintroduces the resample deviceExact's own doc measures. It reads the PRODUCTION
// sources of the whole package, so a FIFTH call site added anywhere later fails here
// without the gate needing an update — it is the "not just this instance" gate the
// blur report asked for.
func TestMessageRasterDrawIsExportOnly(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil || len(names) == 0 {
		t.Fatalf("glob the package: %v (%d files)", err, len(names))
	}
	scanned := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || callName(call) != "Draw" || len(call.Args) != 4 {
				return true
			}
			if exportOnlyDrawFiles[name] {
				return true
			}
			t.Errorf("%s:%d — a 4-argument .Draw(ren, visibleRunes, x, y) call outside the export passes. "+
				"Live UI text must call DrawScaled(ren, visibleRunes, x, y, renderPct, clip) so a device-rasterized "+
				"emoji/field raster blits device-exact instead of being resampled by ren.SetScale.",
				name, fset.Position(call.Pos()).Line)
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no production files — the gate would pass vacuously")
	}
}

// TestLabelCoveringCenteredDeviceExactAtFractionalScale is the fails-at-a-fractional-
// scale / passes-at-100% regression pin the blur report asked for, driven through the
// REAL production draw path (labelCoveringCentered), not a reimplementation of
// DrawScaled's contract. It compares the production call's painted pixels against the
// SAME underlying raster (same emojiRaster cache key, so literally the same texture)
// drawn through the plain Draw it used to call.
//
//   - At 100% deviceExact is false for BOTH calls by design (the identity scale has
//     nothing to fold), so they must be byte-identical — a regression here would be a
//     DIFFERENT bug, and this test correctly does not claim to catch one.
//   - At 105% the production call takes the device-exact branch and the comparison
//     call does not, so they must differ. If labelCoveringCentered ever reverts to
//     calling Draw again, this assertion goes back to passing where it must fail.
func TestLabelCoveringCenteredDeviceExactAtFractionalScale(t *testing.T) {
	const canvasW, canvasH = 400, 200
	const text = "React \U0001F600 label" // U+1F600 is supplementary-plane → NeedsEmojiFallback is unconditionally true

	paint := func(t *testing.T, pct int, viaProduction bool) []byte {
		t.Helper()
		a := scaleTestApp(t)
		c := a.ctx
		c.SetEmojiFont(twemojiTTF)
		c.SetUIScale(pct)
		col := sdl.Color{R: 255, G: 255, B: 255, A: 255}

		ren := c.Ren
		_ = ren.SetDrawColor(0, 0, 0, 255)
		_ = ren.Clear()
		_ = ren.SetScale(float32(pct)/100, float32(pct)/100)
		if viaProduction {
			c.labelCoveringCentered(4, 0, 40, 300, text, col)
		} else {
			// The exact args labelCoveringCentered itself passes to emojiRaster — same
			// cache key, so this is the SAME *render.MessageRaster instance, drawn
			// through the method the call site used to call.
			face := c.chromeFaceFor(text)
			m := c.emojiRaster(text, col, face, c.EmojiFont(DefaultScalePct))
			if m == nil {
				t.Fatal("emojiRaster returned nil — precondition for this A/B failed")
			}
			hgt := m.Height()
			m.Draw(ren, m.TotalRunes(), 4, 0+(40-hgt)/2)
		}
		_ = ren.SetScale(1, 1)

		pix := make([]byte, canvasW*canvasH*4)
		if err := ren.ReadPixels(&sdl.Rect{X: 0, Y: 0, W: canvasW, H: canvasH}, uint32(sdl.PIXELFORMAT_ARGB8888), unsafe.Pointer(&pix[0]), canvasW*4); err != nil {
			t.Skipf("ReadPixels: %v", err)
		}
		return pix
	}

	for _, pct := range []int{100, 105} {
		t.Run(map[int]string{100: "100pct", 105: "105pct"}[pct], func(t *testing.T) {
			production := paint(t, pct, true)
			viaPlainDraw := paint(t, pct, false)
			differ := !bytes.Equal(production, viaPlainDraw)
			if pct == DefaultScalePct && differ {
				t.Error("at 100% labelCoveringCentered's output differs from a direct Draw call on the same raster — deviceExact should be false for both at the identity scale, so nothing should differ here")
			}
			if pct != DefaultScalePct && !differ {
				t.Error("at 105% labelCoveringCentered's output is byte-identical to a direct Draw call on the same raster — the device-exact bracket isn't firing (the #1 blur gap is back)")
			}
		})
	}
}

// TestEnsureReactBadgeBuildsFromDeviceFont is the AST wiring gate for #2: ensureReactBadge
// must resolve its glyph through emojiDeviceFont (not the logical-only EmojiFont) and hand
// RasterizeBadge the CURRENT textDevPct as its devScale argument. Reads the production
// source, so a revert to EmojiFont — the exact regression this fix closes — fails here
// even though nothing about ensureReactBadge's return type or nil-handling changed.
func TestEnsureReactBadgeBuildsFromDeviceFont(t *testing.T) {
	body := funcBodySource(t, "reactions.go", "ensureReactBadge")
	if !containsCall(body, "emojiDeviceFont") {
		t.Error("ensureReactBadge no longer resolves the emoji face through emojiDeviceFont — reaction floats would build from the fixed LOGICAL face again and blur at any scale != 100%")
	}
	calls := callsNamed(body, "RasterizeBadge")
	if len(calls) != 1 {
		t.Fatalf("ensureReactBadge calls RasterizeBadge %d times, want exactly 1", len(calls))
	}
	if len(calls[0].Args) < 5 {
		t.Fatal("RasterizeBadge call dropped the devScale argument (want 5 args: ren, font, text, col, devScale)")
	}
	if !mentionsIdent(calls[0].Args[4], "textDevPct") {
		t.Error("RasterizeBadge's devScale argument no longer reads textDevPct — a badge would freeze at whatever scale built it")
	}
}

// TestReactionBadgeCachePurgesOnDeviceScaleChange drives ensureReactBadge (not a
// reimplementation of it) across a real UI-scale change and proves the WHOLE chain
// actually moved: a new badge object is built (the cache purge fired), its DEVICE pixel
// footprint (RawSize) grew with the scale, and its LOGICAL footprint (Size) — what the
// float's on-screen position and spread math uses — stayed the same, so a reaction float
// does not visibly change size just because the window's UI scale changed.
func TestReactionBadgeCachePurgesOnDeviceScaleChange(t *testing.T) {
	a := scaleTestApp(t)
	c := a.ctx
	c.SetEmojiFont(twemojiTTF)

	c.SetUIScale(100)
	b100 := a.ensureReactBadge(0)
	if b100 == nil {
		t.Fatal("ensureReactBadge returned nil at 100% with a real emoji face loaded")
	}
	w100, h100 := b100.RawSize()
	lw100, lh100 := b100.Size()

	c.SetUIScale(150)
	b150 := a.ensureReactBadge(0)
	if b150 == nil {
		t.Fatal("ensureReactBadge returned nil at 150%")
	}
	if b150 == b100 {
		t.Error("the reaction badge survived a UI-scale change — ensureReactBadge's purge-on-mismatch didn't fire, so it will blit a stale-scale texture through the non-exact fallback forever")
	}
	w150, h150 := b150.RawSize()
	if w150 <= w100 || h150 <= h100 {
		t.Errorf("badge RAW (device) size did not grow with the UI scale: 100%%=%dx%d 150%%=%dx%d", w100, h100, w150, h150)
	}
	lw150, lh150 := b150.Size()
	if abs32(lw150-lw100) > 1 || abs32(lh150-lh100) > 1 {
		t.Errorf("LOGICAL badge size drifted across a UI-scale change: 100%%=%dx%d 150%%=%dx%d — a reaction float must not visibly change size just because the scale changed", lw100, lh100, lw150, lh150)
	}
}

// F1 continued — "blur is still there" after v1.93.0/12d1965. That fix reached
// only labelEmoji/labelCoveringCentered's RARE emoji/mixed-script raster branch,
// the text field's own emoji-fallback branch, and reaction badges. It never
// touched blitLabel (ui.go), the single choke point every PLAIN Label/Heading/
// LabelClipped*/Button/Checkbox/Dropdown/Tooltip call in the client draws
// through — including the exact widgets in the report's own screenshot
// (Settings > WINDOW: a Checkbox, a Label, a Button, a Label). blitLabel now
// carries the same device-exact bracket (blitLabelExact), gated the same way
// MessageRaster/Badge already are (uiDeviceExactAt).

// TestLabelDeviceExactAtFractionalScale is the Ctx.Label counterpart to
// TestLabelCoveringCenteredDeviceExactAtFractionalScale (labelCoveringCentered)
// and TestBadgeDeviceExactDiffersFromResampleAtFractionalScale
// (internal/render/badge_test.go) for the surface neither of those closes: the
// plain, non-emoji, non-focused-field label path.
//
// It drives the REAL production entry point (Ctx.Label) twice against the SAME
// cached glyph texture: once with blitLabel's device-exact gate telling the
// truth, once with it LIED to via beginRenderScaleOverride — the exact
// production knob an offscreen export bracket already uses to decouple
// Ctx.RenderScalePct from the live ambient ren.SetScale — forcing blitLabel down
// the pre-generalization blitLabelScaled branch while the renderer's ACTUAL
// scale (ren.SetScale, set once before either call, exactly as main.go sets it
// per frame) never changes. This is not a reimplementation of blitLabel's gate:
// it is the same knob RenderScalePct-based export brackets use for real.
//
//   - At 100% uiDeviceExactAt excludes the identity scale unconditionally for
//     BOTH calls (same exclusion MessageRaster.deviceExact/Badge.Draw use), so
//     the two paints must be byte-identical — a difference here would be a
//     DIFFERENT bug than the one this test targets.
//   - At 115%, "M" is a minimal reproducer of blitLabelScaled's src/dst
//     rounding mismatch (blitLabel's own doc): the OLD path's independently
//     re-derived width/height land the SAME device-sized glyph texture at a
//     measurably different device row than its own device-exact 1:1 copy. The
//     margin strip strictly ABOVE the glyph's own first ink row — found
//     dynamically from the device-exact (correct) render, not a hardcoded row,
//     so this does not silently stop testing anything if the embedded font
//     ever changes — is pure background by construction in a correct render;
//     the OLD path bleeds real (non-pure-white) pixels into it.
//
// Pure black-on-white text makes "not background" unambiguous, and the margin
// sits entirely outside the glyph's own ink (not "inside a glyph"), so the
// font's own antialiasing at the glyph's real edges cannot contaminate the
// signal — the strip is either untouched background or it is evidence of the
// mismatch, nothing else.
//
// DELETION CATCHER: reverting blitLabel to call blitLabelScaled unconditionally
// (deleting the uiDeviceExactAt branch, or blitLabelExact itself) collapses the
// "truth" and "lied" calls onto the identical code path — production and
// reference become the same function call on the same inputs — and the 115%
// subtest's "old path bleeds into the margin, the fixed path does not" assertion
// goes back to failing exactly where it must.
func TestLabelDeviceExactAtFractionalScale(t *testing.T) {
	const canvasW, canvasH = 60, 40
	const text = "M"

	paint := func(t *testing.T, pct int, exact bool) []byte {
		t.Helper()
		a := scaleTestApp(t)
		c := a.ctx
		c.SetUIScale(pct)
		col := sdl.Color{R: 0, G: 0, B: 0, A: 255} // pure black on pure white bg: any non-pure pixel is unambiguous

		ren := c.Ren
		_ = ren.SetDrawColor(255, 255, 255, 255)
		_ = ren.Clear()
		_ = ren.SetScale(float32(pct)/100, float32(pct)/100) // the frame's ambient scale, set once, as main.go does
		if !exact {
			// Lie that the renderer is at the identity scale — uiDeviceExactAt
			// excludes DefaultScalePct unconditionally — so blitLabel takes the
			// OLD blitLabelScaled branch even though the ambient SetScale above
			// is still fractional. Production knob, not a gate reimplementation:
			// this is the same beginRenderScaleOverride an offscreen export
			// bracket calls to keep RenderScalePct honest inside its own SetScale.
			c.beginRenderScaleOverride(DefaultScalePct)
		}
		c.Label(4, 4, text, col)
		if !exact {
			c.endRenderScaleOverride()
		}
		_ = ren.SetScale(1, 1)

		pix := make([]byte, canvasW*canvasH*4)
		if err := ren.ReadPixels(&sdl.Rect{X: 0, Y: 0, W: canvasW, H: canvasH}, uint32(sdl.PIXELFORMAT_ARGB8888), unsafe.Pointer(&pix[0]), canvasW*4); err != nil {
			t.Skipf("ReadPixels: %v", err)
		}
		return pix
	}

	for _, pct := range []int{100, 115} {
		t.Run(map[int]string{100: "100pct", 115: "115pct"}[pct], func(t *testing.T) {
			exact := paint(t, pct, true)
			lied := paint(t, pct, false)

			if pct == DefaultScalePct {
				if !bytes.Equal(exact, lied) {
					t.Error("at 100% Ctx.Label's painted pixels depend on the device-exact gate — uiDeviceExactAt should exclude the identity scale for both calls, so nothing should differ here")
				}
				return
			}

			// Find the glyph's own first ink row in the CORRECT (device-exact)
			// render, scanning top-down over the whole canvas. Every row strictly
			// above it is background BY CONSTRUCTION in a correct render — this
			// is a property of how `top` was found, not an assumption.
			top := int32(-1)
		scanTop:
			for y := int32(0); y < canvasH; y++ {
				for x := int32(0); x < canvasW; x++ {
					off := (y*canvasW + x) * 4
					b, g, r := exact[off], exact[off+1], exact[off+2]
					if r != 255 || g != 255 || b != 255 {
						top = y
						break scanTop
					}
				}
			}
			if top <= 0 {
				t.Fatalf("precondition failed: the device-exact render has no blank margin above its own glyph (top=%d) — this pct/text pair does not exercise the mismatch, pick another one rather than weaken the assertion below", top)
			}

			// blended counts pixels that are neither pure white nor pure black —
			// on a pure black-on-white render that is only possible where a
			// filtered copy interpolated across a transparency/ink boundary.
			blended := func(pix []byte) int {
				n := 0
				for y := int32(0); y < top; y++ {
					for x := int32(0); x < canvasW; x++ {
						off := (y*canvasW + x) * 4
						b, g, r := pix[off], pix[off+1], pix[off+2]
						if r > 0 && r < 255 && r == g && g == b {
							n++
						}
					}
				}
				return n
			}

			if n := blended(exact); n != 0 {
				t.Errorf("the device-exact render has %d blended pixel(s) above its own glyph's first ink row (row<%d) — the harness's own precondition is broken", n, top)
			}
			if n := blended(lied); n == 0 {
				t.Errorf("at %d%% the OLD (blitLabelScaled) render shows zero blended pixels above the device-exact glyph's own first ink row (row<%d) — this pct/text pair no longer reproduces the src/dst rounding mismatch; this is the KNOWN-BUGGY reference path, so a zero reading here means the test fixture needs revisiting, not that anything got fixed", pct, top)
			}
		})
	}
}

// TestBlitLabelDispatchesToDeviceExactBranch is the AST wiring/deletion-catcher
// for the WHOLE class (not just the "M"-at-115% instance
// TestLabelDeviceExactAtFractionalScale pins): it reads blitLabel's own
// production source and requires that it still gates on uiDeviceExactAt and
// still calls blitLabelExact. Someone "simplifying" blitLabel back down to a
// single unconditional blitLabelScaled body — the exact regression this whole
// file is about — fails here even on a pct/text combination the pixel test
// above does not happen to cover.
func TestBlitLabelDispatchesToDeviceExactBranch(t *testing.T) {
	body := funcBodySource(t, "ui.go", "blitLabel")
	if !containsCall(body, "uiDeviceExactAt") {
		t.Error("blitLabel no longer gates on uiDeviceExactAt — every plain Label/Button/Checkbox draw would silently fall back to the pre-fix ambient-scale blit at any fractional UI scale")
	}
	if !containsCall(body, "blitLabelExact") {
		t.Error("blitLabel no longer calls blitLabelExact — the device-exact branch this file's blur fix depends on is gone even if the gate itself survived")
	}
	if !containsCall(body, "blitLabelScaled") {
		t.Error("blitLabel no longer calls blitLabelScaled — the export/pinned-tab fallback (a renderer scale that disagrees with the texture's own devPct) has nowhere left to draw")
	}
}

// TestLabelAllocFreeAtFractionalScale closes the coverage gap
// TestWholeScreenGatesGoThroughAllocsPerFrame's sibling gates leave open: every
// whole-screen 0-alloc fixture in this package (wholescreenalloc_test.go) is
// pinned at uiScalePct=100, where uiDeviceExactAt is false by construction and
// blitLabelExact's new SetScale/SetClipRect bracket never runs at all. Without
// this gate, a naive implementation of that bracket (a &local rect escaping
// through cgo, say) could regress the render loop's zero-allocation contract at
// every fractional scale and nothing already in the suite would notice.
func TestLabelAllocFreeAtFractionalScale(t *testing.T) {
	a := scaleTestApp(t)
	c := a.ctx
	c.SetUIScale(115) // fractional: exercises blitLabelExact, not the 100% fast path

	// A clip is armed for part of the run so the reassert-the-clip branch inside
	// blitLabelExact is measured too, not just the no-clip path.
	draw := func() {
		c.Label(4, 4, "Fit to screen", ColText)
		cp, ch := c.pushClip(sdl.Rect{X: 0, Y: 0, W: 200, H: 60})
		c.Label(4, 20, "Custom:", ColText)
		c.popClip(cp, ch)
	}
	// Warm the label cache/atlas before measuring: only the STEADY STATE (cache
	// hit, texture already resident) is the render-loop's contract; the first
	// rasterization is a one-shot cost every AllocsPerRun-gated draw in this
	// package already excludes the same way.
	draw()
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Errorf("Ctx.Label allocates %.1f/op at a fractional UI scale (115%%) once its label cache is warm — blitLabelExact's SetScale/SetClipRect bracket must stay allocation-free like devFieldValue/MessageRaster.draw/Badge.Draw already are", n)
	}
}

// BenchmarkLabelAtFractionalScale is the wall-clock half of the perf gate the
// alloc test above cannot show: blitLabelExact adds two SetScale and up to two
// SetClipRect cgo calls per label versus the pre-fix single Ren.Copy, and
// blitLabel runs per-widget-per-frame. Run with
// `go test -run=NONE "-bench=BenchmarkLabelAtFractionalScale" -benchmem` and
// compare against BenchmarkLabelAtIdentityScale, which must stay on the
// untouched 100% fast path (nothing added there).
func BenchmarkLabelAtFractionalScale(b *testing.B) {
	ren, cleanup := newCaptureHarness(b)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		b.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()
	c.SetUIScale(115)
	c.Label(4, 4, "Fit to screen", ColText) // prime the cache/atlas

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Label(4, 4, "Fit to screen", ColText)
	}
}

// BenchmarkLabelAtIdentityScale is BenchmarkLabelAtFractionalScale's control:
// uiDeviceExactAt is false at 100% by construction, so this must show no added
// cost from the fix — the whole point of keeping the identity-scale short
// circuit.
func BenchmarkLabelAtIdentityScale(b *testing.B) {
	ren, cleanup := newCaptureHarness(b)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		b.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()
	c.Label(4, 4, "Fit to screen", ColText) // prime the cache/atlas

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Label(4, 4, "Fit to screen", ColText)
	}
}
