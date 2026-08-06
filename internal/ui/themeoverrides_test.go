package ui

// Gates for the AsyncAO tier's [overrides] applier (themeoverrides.go).
//
// Two shapes, because the defect had two halves and either one alone would have
// let it ship:
//
//   - the DISCIPLINE gates below drive foldSidecarOverrides directly: it rewrites,
//     never adds, it refuses a key nothing paints, and it honours AO2's second
//     spelling of the one widget that has two.
//   - the SHIPPED-CONTENT gate drives the real ingest chain over the fourteen
//     themes in themes/. That is the half that matters, because the tier was
//     PARSED, EXPOSED and DOCUMENTED for a whole wave with no applier behind it:
//     every unit test of the reader passed, every theme loaded, and ~60 rows per
//     theme did nothing. A gate that only measured this package against itself
//     would have passed then too.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// The call site
// ---------------------------------------------------------------------------

// ovLiveThemeName is the fixture theme the end-to-end gate below applies. Its own
// folder, not one of ours: the point is to drive the client's real apply, and a
// shipped theme would let the gate pass on a rect that happened to match.
const ovLiveThemeName = "ovlivepath"

// ovLiveMovedKey is the widget the fixture moves and ovLiveStillStockKey the one it
// leaves alone. Both must be EDITABLE and both must come from AO2's stock backstop
// rather than from the fixture's design file, so the pair measures two different
// things with one apply: the override landed, and it landed on that key ONLY.
const (
	ovLiveMovedKey      = "emote_dropdown"
	ovLiveStillStockKey = "sfx_dropdown"
)

// ovLiveRect is deliberately nothing like AO2's stock emote_dropdown
// {5, 380, 105, 20}: every component differs, so a partial write cannot read as a
// clean one, and it is far from any other stock rect so a mis-keyed write is loud.
var ovLiveRect = theme.Rect{X: 123, Y: 234, W: 145, H: 26}

// ovLiveCanvas is the fixture's `courtroom`, and it is AO2's OWN 714x579 on
// purpose: applyAO2DefaultRects refuses a default that would land on an
// author-placed widget unless the theme works on that canvas
// (ao2defaultrects.go), so this is what makes the backstop fill every key the
// two-line design file is silent about.
var ovLiveCanvas = theme.Rect{X: 0, Y: 0, W: 714, H: 579}

// ovWriteLiveTheme writes the fixture to a fresh root and returns the root to hand
// to SetTheme. Two files and nothing else:
//
//   - a courtroom_design.ini with `courtroom` + `viewport` and NO widget keys.
//     That pair is what themeLayoutIn validates on and what gates the AO2
//     backstop, so this is the "a two-key design file plus a sidecar is a whole
//     layout" case foldSidecarOverrides was written for — and it is why the
//     assertions below can name AO2's stock rects as the not-applied answer.
//   - a sidecar with one [overrides] row.
func ovWriteLiveTheme(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, theme.ThemesDirName, ovLiveThemeName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	design := fmt.Sprintf("%s = %d, %d, %d, %d\n%s = 0, 0, 714, 382\n",
		ao2CanvasKey, ovLiveCanvas.X, ovLiveCanvas.Y, ovLiveCanvas.W, ovLiveCanvas.H, ao2ViewportKey)
	if err := os.WriteFile(filepath.Join(dir, theme.DesignFileName), []byte(design), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := fmt.Sprintf("[theme]\nname = %s\ncredit = All original.\n\n[overrides]\n%s = %d, %d, %d, %d\n",
		ovLiveThemeName, ovLiveMovedKey, ovLiveRect.X, ovLiveRect.Y, ovLiveRect.W, ovLiveRect.H)
	if err := os.WriteFile(filepath.Join(dir, theme.SidecarFileName), []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestThemeApplyRunsTheOverridesTier is the gate on the CALL SITE, and it is the
// one gate in this file that a mirror cannot fake.
//
// Every other check here reaches foldSidecarOverrides through ovIngestLayout, a
// hand-written copy of pollThemeApply's chain. That is the right shape for pinning
// the applier's discipline and the wrong shape for pinning that anything calls it:
// delete the `foldSidecarOverrides(res.layout, res.sidecar)` line from app.go and
// every one of them still passes, because the mirror keeps calling it. The tier
// shipped in exactly that state for a whole wave — parsed, documented, exposed,
// and never once invoked on the path a player takes.
//
// So this one drives applyThemeAsync and reads a.themeRects: the preference, the
// apply goroutine, theme.Load, the design tier, the AO2 backstop, the fold,
// pollThemeApply's landing and the player's own layout-editor pass. The same shape
// panelfonts_test.go uses for the font rows, for the same reason.
//
// THE MUTATION IT ANSWERS TO: replace the fold's call in app.go with
// `_ = res.sidecar` and this test must fail on ovLiveMovedKey. If it does not, the
// gate is measuring the mirror again.
func TestThemeApplyRunsTheOverridesTier(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetTheme(ovLiveThemeName, ovWriteLiveTheme(t))
	awaitThemeApply(t, a)

	// The fixture loaded at all. Without this a missing theme would land an empty
	// map and every assertion below would read as "the fold did not run".
	if got, ok := a.themeRects[ao2CanvasKey]; !ok || got != ovLiveCanvas {
		t.Fatalf("the fixture theme did not load: %s = %+v (present=%v), want %+v — nothing below is "+
			"measuring the overrides tier", ao2CanvasKey, got, ok, ovLiveCanvas)
	}

	// The unmoved key proves the AO2 backstop ran, which is what makes the moved
	// key's stock value a meaningful "not applied" answer.
	stockUnmoved := ao2DefaultDesignRects[ovLiveStillStockKey]
	if got, ok := a.themeRects[ovLiveStillStockKey]; !ok || got != stockUnmoved {
		t.Fatalf("%s = %+v (present=%v), want AO2's stock %+v — the backstop did not fill the keys the "+
			"fixture's design file is silent about, so this gate cannot tell an applied override from an "+
			"unapplied one", ovLiveStillStockKey, got, ok, stockUnmoved)
	}

	stockMoved := ao2DefaultDesignRects[ovLiveMovedKey]
	if ovLiveRect == stockMoved {
		t.Fatalf("the fixture overrides %s to AO2's own stock rect %+v — the gate would pass with the "+
			"fold deleted", ovLiveMovedKey, stockMoved)
	}
	got, ok := a.themeRects[ovLiveMovedKey]
	if !ok {
		t.Fatalf("%s is absent from a.themeRects entirely", ovLiveMovedKey)
	}
	if got == stockMoved {
		t.Fatalf("%s landed at AO2's stock %+v, not the theme's authored %+v — pollThemeApply is not "+
			"running the [overrides] tier (app.go, foldSidecarOverrides). Every [overrides] row in every "+
			"theme is inert in that state, and the free elements anchored on those rects resolve against "+
			"the stock geometry instead.", ovLiveMovedKey, stockMoved, ovLiveRect)
	}
	if got != ovLiveRect {
		t.Errorf("%s landed at %+v, want the authored %+v — something between the fold and a.themeRects "+
			"rewrote it", ovLiveMovedKey, got, ovLiveRect)
	}
	// themeRectsOrig is the PRISTINE map the layout editor resets to. An override
	// that reached only the working copy would silently un-apply on the first reset.
	if orig := a.themeRectsOrig[ovLiveMovedKey]; orig != ovLiveRect {
		t.Errorf("themeRectsOrig has %s = %+v, want %+v — the author's rect must be the rect "+
			"\"reset layout\" restores, not the stock one", ovLiveMovedKey, orig, ovLiveRect)
	}
}

// ---------------------------------------------------------------------------
// The discipline
// ---------------------------------------------------------------------------

// ovTestRect / ovTestOther are two distinguishable rects. Named per hard rule 9,
// and distinguishable on every component so a partial write cannot look like a
// clean one.
var (
	ovTestRect  = theme.Rect{X: 11, Y: 22, W: 33, H: 44}
	ovTestOther = theme.Rect{X: 55, Y: 66, W: 77, H: 88}
)

// ovSidecar builds a sidecar carrying exactly the given [overrides] rows, in
// order. It goes through the REAL reader rather than assembling the struct, so a
// row this test believes it wrote is a row the reader would actually produce.
func ovSidecar(t *testing.T, body string) *theme.Sidecar {
	t.Helper()
	sc, err := theme.ParseSidecar([]byte("[overrides]\n" + body))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if sc == nil {
		t.Fatal("fixture parsed to a nil sidecar")
	}
	return sc
}

// TestSidecarOverridesRewriteExistingKeysOnly pins the applier's whole contract.
//
// The "never adds" arm is the load-bearing one. A key absent from the resolved
// design is a widget this build does not place — themeSlots' inert rows, and the
// keys AO2's own default file is silent about — so writing one in would put a rect
// in the map with no draw site behind it: the ghost box the registry was built to
// end (themeslots.go:12-14), and, for an inert key, a box the layout editor would
// then refuse to move.
func TestSidecarOverridesRewriteExistingKeysOnly(t *testing.T) {
	// The map deliberately carries ONE editable key and one inert one, so "present
	// but not editable" and "editable but not present" are both measured.
	layout := map[string]theme.Rect{
		"emote_dropdown": ovTestOther,
		"player_list":    ovTestOther, // slotStateInert: ingested for audit, painted by nothing
	}
	sc := ovSidecar(t, ""+
		"emote_dropdown = 11, 22, 33, 44\n"+ // present + editable: applied
		"player_list    = 11, 22, 33, 44\n"+ // present, NOT editable: refused
		"ic_chatlog     = 11, 22, 33, 44\n"+ // editable, NOT present: refused, never added
		"courtroom      = 11, 22, 33, 44\n"+ // the canvas itself is `fixed`: refused
		"not_a_widget   = 11, 22, 33, 44\n") // no themeSlots row at all: refused
	foldSidecarOverrides(layout, sc)

	if got := layout["emote_dropdown"]; got != ovTestRect {
		t.Errorf("emote_dropdown is %+v, want the override %+v — a present, editable key must be rewritten",
			got, ovTestRect)
	}
	if got := layout["player_list"]; got != ovTestOther {
		t.Errorf("player_list is %+v, want its design rect %+v untouched — nothing paints it, so an "+
			"override there is a rect the editor cannot move and the screen never shows", got, ovTestOther)
	}
	for _, key := range []string{"ic_chatlog", "courtroom", "not_a_widget"} {
		if r, ok := layout[key]; ok {
			t.Errorf("the fold ADDED %q = %+v — it must rewrite existing keys only (layoutedit.go:832); "+
				"a key the resolved design does not carry is a widget this build does not place", key, r)
		}
	}
	if len(layout) != 2 {
		t.Errorf("the fold changed the key set (%d keys, want 2) — it may only change values", len(layout))
	}
}

// TestSidecarOverrideNilSidecarIsInert is the stock-AO2-theme case: no sidecar, no
// edits, and specifically no panic. A nil *Sidecar reaches this applier on every
// theme that is not an AsyncAO one, and Overrides is a FIELD read, not one of the
// nil-safe accessor methods.
func TestSidecarOverrideNilSidecarIsInert(t *testing.T) {
	layout := map[string]theme.Rect{"emote_dropdown": ovTestOther}
	foldSidecarOverrides(layout, nil)
	if got := layout["emote_dropdown"]; got != ovTestOther {
		t.Errorf("a nil sidecar changed the layout: %+v", got)
	}
}

// TestSidecarOverrideHonoursAO2SecondSpelling pins the one aliased widget.
//
// AO2 probes "immediate" first and falls back to "pre_no_interrupt"
// (courtroom.cpp:1072; themedToggleRect, theme_layout.go:444-459), but AO2's own
// stock design file spells it `pre_no_interrupt`, so that is the only spelling
// applyAO2DefaultRects can supply. An author who writes the spelling AO2 reads
// FIRST — themes/thh_trial writes exactly that — would otherwise have authored a
// row that resolves to nothing at all, which is the silent failure this whole file
// exists to end.
func TestSidecarOverrideHonoursAO2SecondSpelling(t *testing.T) {
	// Only the stock spelling is present, which is what the backstop produces.
	layout := map[string]theme.Rect{"pre_no_interrupt": ovTestOther}
	foldSidecarOverrides(layout, ovSidecar(t, "immediate = 11, 22, 33, 44\n"))
	if got := layout["pre_no_interrupt"]; got != ovTestRect {
		t.Errorf("an `immediate` override left pre_no_interrupt at %+v, want %+v — both spellings index "+
			"one themeSlots row, so the edit belongs in whichever the map carries", got, ovTestRect)
	}
	if _, added := layout["immediate"]; added {
		t.Error("the fold added `immediate` — the alias resolves into the EXISTING spelling; adding the " +
			"other one would be the add this applier does not do")
	}

	// ...and the mirror: the theme declares the first spelling itself, so the row
	// lands there and the stock one is left alone.
	both := map[string]theme.Rect{"immediate": ovTestOther, "pre_no_interrupt": ovTestOther}
	foldSidecarOverrides(both, ovSidecar(t, "immediate = 11, 22, 33, 44\n"))
	if got := both["immediate"]; got != ovTestRect {
		t.Errorf("immediate is %+v, want %+v — a present key is always its own target", got, ovTestRect)
	}
	if got := both["pre_no_interrupt"]; got != ovTestOther {
		t.Errorf("pre_no_interrupt is %+v, want %+v — the alias must not write both", got, ovTestOther)
	}
}

// ---------------------------------------------------------------------------
// The shipped content
// ---------------------------------------------------------------------------

// ovIngestLayout reproduces pollThemeApply's courtroom-geometry chain
// (app.go:8990-9010) for one loaded theme: the theme's own design rects, then
// AO2's stock backstop, then the sidecar's [overrides].
//
// A mirror rather than a shared helper, exactly as buildAceCensusAt is
// (themecensus_test.go:186-217) — the point of these gates is that the rects a
// PLAYER gets come out of this order, so the order is written out where a reader
// can compare it to the call site line by line.
func ovIngestLayout(th *theme.Theme) map[string]theme.Rect {
	layout := make(map[string]theme.Rect, len(themeLayoutKeys))
	for _, key := range themeLayoutKeys {
		if r, ok := th.ElementRect(key); ok {
			layout[key] = r
		}
	}
	applyAO2DefaultRects(layout)
	foldSidecarOverrides(layout, th.Sidecar())
	return layout
}

// ovLoadShippedThemes loads every themes/<name> that carries a sidecar through the
// REAL theme.Load, so the design tier, the fallback tiers and the sidecar all
// resolve the way they do in the client.
func ovLoadShippedThemes(t *testing.T) map[string]*theme.Theme {
	t.Helper()
	root := uiRepoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, shippedThemeDir))
	if err != nil {
		t.Fatalf("read %s: %v", shippedThemeDir, err)
	}
	out := map[string]*theme.Theme{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, shippedThemeDir, e.Name(), theme.SidecarFileName)); err != nil {
			continue // an AO2-only theme folder: no AsyncAO tier to check
		}
		th, err := theme.Load(e.Name(), "", []string{root})
		if err != nil {
			t.Errorf("themes/%s: %v", e.Name(), err)
			continue
		}
		if err := th.SidecarErr(); err != nil {
			t.Errorf("themes/%s: sidecar refused: %v — a theme WE ship must parse, or its entire "+
				"AsyncAO tier is silently dropped", e.Name(), err)
			continue
		}
		out[e.Name()] = th
	}
	if len(out) < shippedThemeMin {
		t.Fatalf("loaded %d shipped themes, want at least %d — the gate is measuring nothing",
			len(out), shippedThemeMin)
	}
	return out
}

// TestShippedThemeOverridesAllResolve is the shipped-content gate: every
// `[overrides]` row in every theme we ship must land on a key the resolved design
// actually carries, and the rect in the finished map must BE the authored one.
//
// This is the gate that was missing when the tier shipped unapplied. The rows
// parsed, the reader's own tests passed, the themes loaded, and every widget in
// every one of the fourteen sat at AO2's stock coordinates — while the free
// elements authored against the authored rects resolved their anchors against
// those stock ones, so the decoration landed in places nobody wrote (see
// themeoverrides.go's header for the worked example).
//
// It fails a row two ways, and both are real defects rather than style:
//   - UNRESOLVABLE: the key names no widget this build places, so the row can
//     never do anything. Silent today — [overrides] has no degrade note, because
//     an unknown key is not a parse error.
//   - NOT APPLIED: the key resolved but the finished map disagrees with the file,
//     which means something downstream of the fold overwrote it.
func TestShippedThemeOverridesAllResolve(t *testing.T) {
	themes := ovLoadShippedThemes(t)
	rows := 0
	for name, th := range themes {
		sc := th.Sidecar()
		if sc == nil {
			t.Errorf("themes/%s: theme.Load found no sidecar beside a file that exists", name)
			continue
		}
		layout := ovIngestLayout(th)
		if len(sc.Overrides) == 0 {
			t.Errorf("themes/%s declares no [overrides] at all — with a two-key courtroom_design.ini "+
				"that theme is AO2's stock layout wearing a palette", name)
			continue
		}
		for _, o := range sc.Overrides {
			rows++
			key, ok := sidecarOverrideTarget(layout, o.Key)
			if !ok {
				t.Errorf("themes/%s [overrides] %s = %+v resolves to NOTHING: the resolved design "+
					"(the theme's courtroom_design.ini + AO2's stock backstop) carries no editable key "+
					"by that name, so the row is inert and nothing says so. Fix the spelling, or give "+
					"the key a themeSlots row with a painter behind it.", name, o.Key, o.Rect)
				continue
			}
			if got := layout[key]; got != o.Rect {
				t.Errorf("themes/%s [overrides] %s = %+v, but the finished layout has %s = %+v — the "+
					"authored rect did not survive the ingest chain. If this theme declares BOTH AO2 "+
					"spellings of one widget (immediate / pre_no_interrupt) they must agree: they are "+
					"two names for one rect, and the last row would silently win.",
					name, o.Key, o.Rect, key, got)
			}
		}
	}
	if rows == 0 {
		t.Fatal("no shipped theme declares an override — the gate is vacuous")
	}
	t.Logf("checked %d [overrides] rows across %d shipped themes", rows, len(themes))
}

// ovPickerKey is the widget this gate exists for, and ovPickerStageKey the one rect
// it is allowed to lie on: the stage. Several themes seat client controls over the
// viewport deliberately (the penalty bars and verdict buttons in half the corpus do
// it), so an overlap there is a layout, not a collision.
const (
	ovPickerKey      = "effects_dropdown"
	ovPickerStageKey = ao2ViewportKey
)

// ovPickerElementBand is the ONE band whose free elements can bury the picker.
//
// drawCourtroomThemed's order is fixed and hand-written: BandBackdrop
// (theme_layout.go:795) → stage widgets → BandMid (:834) → panel widgets, and
// drawEffectsPicker is one of those (:1192-1193) → BandOverlay (:1418). A backdrop or a
// mid plate under the picker is therefore the corpus's own idiom — the whole
// button-dressing batch is plates that sit UNDER the control they dress — and only
// an overlay element paints over a live control.
const ovPickerElementBand = theme.BandOverlay

// ovCoversCanvas reports whether a resolved element swallows the WHOLE design
// canvas.
//
// Canvas-wide atmosphere is the one thing over the picker that is not a placement
// mistake: golden_land's `pollen`, a rain sheet, a vignette. They lie over every
// widget in their theme at once, the picker no more than anything else, so flagging
// them would say nothing about where the picker sits. It is the only geometric
// carve-out and it is the narrowest one that admits the idiom — an element one pixel
// short of the canvas is back inside the gate.
func ovCoversCanvas(r, canvas sdl.Rect) bool {
	return canvas.W > 0 && canvas.H > 0 &&
		r.X <= canvas.X && r.Y <= canvas.Y &&
		r.X+r.W >= canvas.X+canvas.W && r.Y+r.H >= canvas.Y+canvas.H
}

// ovStrokeOnly reports an element that paints only its OUTLINE.
//
// paintElementShape and paintElementPane fill nothing when the fill alpha is 0 and
// draw pen-wide edges instead, so such an element's RECT is not what it covers —
// and the whole keyline family in this corpus is authored as a few px of OUTSET on
// the widget it dresses, which means its rect contains that widget entirely while
// the paint is four hairlines around it. Judged by the rect, purgatorio's
// `stage_frame` (a 568 x 428 gilt frame around the whole stage) would read as
// burying a picker sitting in clear air 130 px inside it, which is not a defect and
// not a thing an author could fix.
func ovStrokeOnly(el *theme.Element) bool {
	switch el.Kind {
	case theme.ElemShape, theme.ElemPane:
		return el.Fill.A == 0 && el.Stroke.A > 0 && el.StrokePx > 0
	}
	return false
}

// ovEdgeBands is the four pen-wide edges a stroke-only element actually paints, in
// the same pen clamp the painters use (never more than half the short side).
//
// AXIS-ALIGNED, which OVER-states the paint for the chamfered silhouettes (hex,
// ribbon, tape and the two round ones inset their bands per row). That is the safe
// direction for a gate: it can report a collision the silhouette would have missed
// by a pixel, never miss one it makes.
func ovEdgeBands(r sdl.Rect, pen int32) [4]sdl.Rect {
	if maxPen := min32(r.W, r.H) / 2; pen > maxPen {
		pen = maxPen
	}
	if pen < 1 {
		pen = 1
	}
	return [4]sdl.Rect{
		{X: r.X, Y: r.Y, W: r.W, H: pen},             // top
		{X: r.X, Y: r.Y + r.H - pen, W: r.W, H: pen}, // bottom
		{X: r.X, Y: r.Y, W: pen, H: r.H},             // left
		{X: r.X + r.W - pen, Y: r.Y, W: pen, H: r.H}, // right
	}
}

// ovElementHit is the intersection of what an element PAINTS with the picker, which
// is the element's rect for every kind that fills its box and its four edges for a
// keyline.
func ovElementHit(el *theme.Element, r, pick sdl.Rect) (sdl.Rect, bool) {
	if !ovStrokeOnly(el) {
		return r.Intersect(&pick)
	}
	bands := ovEdgeBands(r, int32(el.StrokePx))
	for i := range bands {
		if hit, ok := bands[i].Intersect(&pick); ok {
			return hit, true
		}
	}
	return sdl.Rect{}, false
}

// ovThemeLayoutAt drives the client's real geometry chain for one shipped theme at
// that theme's OWN canvas size.
//
// Driving it at exactly the canvas pins ThemeFitNative at scale 1 with no letterbox
// (the same reasoning as ovProbeCanvasW/H), so every rect it returns can be read
// straight out of the theme file — and the corpus is not one size: eight themes are
// 714x579 and six are 714x668, so the canvas has to come from the theme rather than
// from a constant.
//
// Returns nil after reporting, so a caller can `continue` on a theme whose geometry
// never resolved instead of measuring a zero layout.
func ovThemeLayoutAt(t *testing.T, a *App, name string, th *theme.Theme) *themeLayoutCache {
	t.Helper()
	layout := ovIngestLayout(th)
	canvas, ok := layout[ao2CanvasKey]
	if !ok || canvas.W <= 0 || canvas.H <= 0 {
		t.Errorf("themes/%s carries no usable %s rect (%+v, present=%v) — there is no canvas to resolve "+
			"its elements in", name, ao2CanvasKey, canvas, ok)
		return nil
	}
	a.themeRects = layout
	a.themeSidecar = th.Sidecar()
	a.themeLay.valid = false
	lay := a.themeLayout(int32(canvas.W), int32(canvas.H))
	if lay == nil || !lay.valid {
		t.Errorf("themes/%s produced no canvas at its own %dx%d", name, canvas.W, canvas.H)
		return nil
	}
	if lay.scaleX != 1 || lay.scaleY != 1 || lay.area.X != 0 || lay.area.Y != 0 {
		t.Errorf("themes/%s must resolve at 1:1 with no offset (scale %v,%v area %+v) or the numbers stop "+
			"matching the theme file", name, lay.scaleX, lay.scaleY, lay.area)
		return nil
	}
	return lay
}

// TestShippedThemesPlaceTheEffectsPickerClear is the gate on the ONE widget that
// nothing placed.
//
// `effects_dropdown` is slotStateHandDrawn (themeslots.go) and drawEffectsPicker
// paints it (theme_layout.go), so on an effects-capable server it draws and takes
// clicks. Every shipped theme's header nevertheless listed it among the INERT keys
// and left it out of [overrides] — so while the tier was unapplied it did not matter,
// and the moment the tier was wired up it became the one widget still sitting at
// AO2's stock {330, 352, 89, 22} while everything around it moved. On thh_trial that
// rect lies WHOLLY INSIDE the theme's own ic_chatlog: the picker drew over the court
// record.
//
// Three claims, and the third is the one no reviewer can hold in their head:
//
//  1. every theme we ship declares the key,
//  2. the rect it declares is disjoint from every other [overrides] ROW that theme
//     declares, except the stage, and
//  3. it is disjoint from every OVERLAY-BAND [element.*] that theme declares, once
//     each element's anchor is resolved against the theme's own finished layout.
//
// Claim 3 is here because claims 1 and 2 alone shipped two collisions green. An
// element is not an [overrides] row: it anchors to a widget and carries a signed
// delta, so nothing about its authored numbers looks like the picker's rect —
// requiem_chiru's `epitaph` is `rect = 8, 258, -549, -559` on a 714x579 canvas, which
// is the picker's {8, 258, 165, 20} to the pixel, and golden_land's `wordmark` is
// `8, 6, -300, -218` on the viewport, which covered 98% of its picker. Both were
// invisible to a loop over sc.Overrides.
//
// Two scopes keep claim 3 honest rather than noisy: ovPickerElementBand (only the
// band that paints AFTER the picker) and ovCoversCanvas (canvas-wide atmosphere is a
// wash over everything, not a placement). An element that ANCHORS to the picker is
// exempt too, and that one is authorship rather than geometry: every dressed widget
// in this corpus wears a keyline anchored to itself at a few px of outset, so a theme
// that dresses the picker the way it dresses its other dropdowns is saying where that
// decoration goes on purpose.
//
// Deliberately NOT generalised to all pairs. The corpus is full of intended overlaps
// — ao2_chatbox rides the viewport (courtroom.cpp:3301), server_chatlog and ms_chatlog
// SHARE one rect because ooc_toggle swaps them, `immediate` and `pre_no_interrupt` are
// two spellings of one widget — so an all-pairs check would need an allow-list per
// theme, which is a second place for the truth to live. One widget, one rule.
func TestShippedThemesPlaceTheEffectsPickerClear(t *testing.T) {
	themes := ovLoadShippedThemes(t)
	a := testTabApp(t)
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	placed, judged := 0, 0
	for name, th := range themes {
		sc := th.Sidecar()
		if sc == nil {
			continue // reported by ovLoadShippedThemes' own arm
		}
		pick, ok := sc.OverrideRect(ovPickerKey)
		if !ok {
			t.Errorf("themes/%s declares no [overrides] %s — the picker keeps AO2's stock rect while the "+
				"rest of the theme moves, which is how it ended up drawing on top of a panel. It is not "+
				"inert: themeslots.go carries it as slotStateHandDrawn and drawEffectsPicker paints it.",
				name, ovPickerKey)
			continue
		}
		placed++
		if stock := ao2DefaultDesignRects[ovPickerKey]; pick == stock {
			t.Errorf("themes/%s overrides %s to AO2's own stock rect %+v, which is the same as not "+
				"declaring it", name, ovPickerKey, stock)
		}
		for _, o := range sc.Overrides {
			if o.Key == ovPickerKey || o.Key == ovPickerStageKey {
				continue
			}
			if w, h := ovOverlap(pick, o.Rect); w > 0 && h > 0 {
				t.Errorf("themes/%s: %s %+v overlaps %s %+v by %d x %d = %d square px — the picker is a "+
					"live control, so it has to have a seat of its own",
					name, ovPickerKey, pick, o.Key, o.Rect, w, h, w*h)
			}
		}

		// Claim 3: the theme's own decoration. Resolved through the real chain rather
		// than read off the file, because an element's authored rect is a DELTA on its
		// anchor and only the finished layout knows where that anchor ended up.
		lay := ovThemeLayoutAt(t, a, name, th)
		if lay == nil {
			continue // reported by ovThemeLayoutAt
		}
		abs, ok := lay.rect(ovPickerKey)
		if !ok {
			t.Errorf("themes/%s: %s is absent from the finished layout even though the theme declares it — "+
				"the [overrides] row never reached the map", name, ovPickerKey)
			continue
		}
		for i := range sc.Elements {
			el := &sc.Elements[i]
			if el.Band != ovPickerElementBand || el.Anchor == ovPickerKey {
				continue
			}
			r, resolved := a.resolveElementRect(lay, sc, el)
			if !resolved || ovCoversCanvas(r, lay.area) {
				continue
			}
			judged++
			hit, hits := ovElementHit(el, r, abs)
			if !hits {
				continue
			}
			t.Errorf("themes/%s: [element.%s] resolves to %+v and paints over %s %+v across %d x %d = %d "+
				"square px. It is in the overlay band, which paints AFTER the panel widgets "+
				"(theme_layout.go:1192-1193 then :1418), so it is drawn straight over a live control. Move one "+
				"of the two — and fix whichever comment says the other one is alone there.",
				name, el.ID, r, ovPickerKey, abs, hit.W, hit.H, hit.W*hit.H)
		}
	}
	if placed == 0 {
		t.Fatal("no shipped theme places the effects picker — the gate is vacuous")
	}
	if judged == 0 {
		t.Fatal("not one overlay-band element was measured against the picker — claim 3 is vacuous")
	}
	t.Logf("%d shipped themes place %s clear of their own [overrides] rows and of %d resolved "+
		"overlay-band elements", placed, ovPickerKey, judged)
}

// ovOverlap is the intersection extent of two design rects, in the same half-open
// convention the painter uses: touching edges are NOT an overlap, because a row that
// begins exactly where the one above ends is how every rail in the corpus is built.
func ovOverlap(a, b theme.Rect) (w, h int) {
	return min(a.X+a.W, b.X+b.W) - max(a.X, b.X), min(a.Y+a.H, b.Y+b.H) - max(a.Y, b.Y)
}

// ovProbeTheme / ovProbeWidget / ovProbeElement are the worked example from the
// bug report, pinned as a gate. thh_trial's `rail_mat` is a backdrop seat under the
// selector rail: it anchors emote_dropdown with the delta below, and the theme's own
// comment beside it documents where that is supposed to land.
const (
	ovProbeTheme   = "thh_trial"
	ovProbeWidget  = "emote_dropdown"
	ovProbeElement = "rail_mat"
)

// ovProbeCanvas is thh_trial's own `courtroom` rect. Driving themeLayout at exactly
// this size pins ThemeFitNative's scale at 1.0 with no bars, so every number below
// can be read straight out of the theme file (the same reasoning as censusW/censusH).
const (
	ovProbeCanvasW = 714
	ovProbeCanvasH = 579
)

// ovProbeWant is what themes/thh_trial/asyncao_theme.ini documents for [element.
// rail_mat] in its own comment: `-> 2, 306, 710, 22`. Written out as a literal
// because that comment is the theme author's stated intent, and the whole failure
// was that the client resolved something else — deriving it from the file would
// re-derive the bug.
var ovProbeWant = sdl.Rect{X: 2, Y: 306, W: 710, H: 22}

// TestShippedThemeElementLandsWhereItsThemeSaysItDoes drives the WHOLE chain —
// design tier, AO2 backstop, [overrides], canvas transform, anchor resolution — and
// checks one element against the rect its own file documents.
//
// The gate above proves the overrides reach the layout map. This proves the thing
// the player actually sees, which is a different claim: the elements are anchored to
// widgets, so an override that did not land moved the DECORATION too. Before the
// applier existed this element resolved to {-1, 380, 697, 20}: a 697 px opaque slab
// across the toggle row, because its delta was added to AO2's stock emote_dropdown
// {5, 380, 105, 20} instead of the theme's own {8, 306, 118, 22}.
func TestShippedThemeElementLandsWhereItsThemeSaysItDoes(t *testing.T) {
	th, err := theme.Load(ovProbeTheme, "", []string{uiRepoRoot(t)})
	if err != nil {
		t.Fatalf("load themes/%s: %v", ovProbeTheme, err)
	}
	sc := th.Sidecar()
	if sc == nil {
		t.Fatalf("themes/%s has no sidecar", ovProbeTheme)
	}
	src, ok := sc.FindElement(ovProbeElement)
	if !ok {
		t.Fatalf("themes/%s declares no [element.%s] — the probe is measuring a theme that changed "+
			"under it, not a defect", ovProbeTheme, ovProbeElement)
	}
	if src.Anchor != ovProbeWidget {
		t.Fatalf("[element.%s] anchors %q, not %q — same", ovProbeElement, src.Anchor, ovProbeWidget)
	}

	a := testTabApp(t)
	a.themeRects = ovIngestLayout(th)
	a.themeSidecar = sc
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	a.themeLay.valid = false
	lay := a.themeLayout(ovProbeCanvasW, ovProbeCanvasH)
	if !lay.valid {
		t.Fatal("the theme produced no canvas — there is nothing to measure")
	}
	if lay.scaleX != 1 || lay.scaleY != 1 || lay.area.X != 0 || lay.area.Y != 0 {
		t.Fatalf("the probe must run at 1:1 with no offset (scale %v,%v area %+v) or the numbers stop "+
			"matching the theme file", lay.scaleX, lay.scaleY, lay.area)
	}

	// The widget first, so a failure says WHICH half broke.
	ov, hasOv := sc.OverrideRect(ovProbeWidget)
	if !hasOv {
		t.Fatalf("themes/%s no longer overrides %s — the probe is stale", ovProbeTheme, ovProbeWidget)
	}
	stock := ao2DefaultDesignRects[ovProbeWidget]
	if ov == stock {
		t.Fatalf("themes/%s overrides %s to AO2's own stock rect %+v — the probe cannot tell an applied "+
			"override from an unapplied one", ovProbeTheme, ovProbeWidget, stock)
	}
	wantWidget := sdl.Rect{X: int32(ov.X), Y: int32(ov.Y), W: int32(ov.W), H: int32(ov.H)}
	if got, ok := lay.rect(ovProbeWidget); !ok || got != wantWidget {
		t.Fatalf("%s laid out at %+v (present=%v), want its authored %+v — the [overrides] tier did not "+
			"reach the layout", ovProbeWidget, got, ok, wantWidget)
	}

	got, resolved := a.resolveElementRect(lay, sc, src)
	if !resolved {
		t.Fatal("[element." + ovProbeElement + "] resolved to nothing")
	}
	if got != ovProbeWant {
		t.Errorf("[element.%s] resolved to %+v, want %+v — the rect its own file documents beside the "+
			"declaration. An element anchored to a widget follows that widget, so a layout tier that "+
			"never applied moved every piece of decoration authored on top of it.",
			ovProbeElement, got, ovProbeWant)
	}
}
