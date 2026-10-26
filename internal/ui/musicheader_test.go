package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// The Music panel's header used to be unconditional draws interleaved with
// CONDITIONAL row advances: "Stop music" drew at the body origin and advanced
// nothing, and the 28 px covering it belonged to the now-playing label — which a
// theme's music_display plate takes over. With the plate external AND the search
// external, Expand all was painted at the identical origin as Stop music, and
// since Ctx.Button ends in `hover && c.clicked` with no click consumption, ONE
// click fired BOTH: expanding the categories also stopped the music. With only the
// plate external, the search TextField landed on the button instead.
//
// These drive the REAL planner across the whole flag matrix, so the property the
// bug violated is the thing asserted.

func musicHeaderItemName(id int) string {
	switch id {
	case musicItemStop:
		return "stop"
	case musicItemRandom:
		return "random"
	case musicItemExpand:
		return "expand all"
	case musicItemCollapse:
		return "collapse all"
	case musicItemMenu:
		return "overflow menu"
	case musicItemNowPlaying:
		return "now-playing readout"
	case musicItemSearch:
		return "search field"
	case musicItemCount:
		return "shown/total count"
	}
	return "unknown"
}

// TestMusicHeaderNeverOverlapsAtAnyWidth is the headline pin. It sweeps every
// plausible panel width in all four external-element combinations and asserts that
// no two placed controls share a pixel and that none leaves the panel.
func TestMusicHeaderNeverOverlapsAtAnyWidth(t *testing.T) {
	const now = musicNowPrefix + "Trial (Suspense)"
	const count = "128 / 640"
	for _, searchExternal := range []bool{false, true} {
		for _, plate := range []bool{false, true} {
			for w := int32(0); w <= 720; w++ {
				// Odd origin: a plan that ignores r.X/r.Y shows up here.
				panel := sdl.Rect{X: 17, Y: 43, W: w, H: toolbarThemeHeightPx}
				plan := planMusicHeader(panel, now, count, searchExternal, plate, toolbarTestMeasure)
				for i := 0; i < plan.n; i++ {
					it := &plan.items[i]
					if it.r.W <= 0 || it.r.H <= 0 {
						t.Fatalf("search=%v plate=%v W=%d: %s placed with an empty rect %+v",
							searchExternal, plate, w, musicHeaderItemName(it.id), it.r)
					}
					if it.r.X < panel.X || it.r.X+it.r.W > panel.X+panel.W {
						t.Fatalf("search=%v plate=%v W=%d: %s spans x=[%d,%d), outside [%d,%d)",
							searchExternal, plate, w, musicHeaderItemName(it.id),
							it.r.X, it.r.X+it.r.W, panel.X, panel.X+panel.W)
					}
					if it.r.Y < panel.Y || it.r.Y+it.r.H > panel.Y+plan.h {
						t.Fatalf("search=%v plate=%v W=%d: %s spans y=[%d,%d), outside the header it reported (h=%d)",
							searchExternal, plate, w, musicHeaderItemName(it.id),
							it.r.Y, it.r.Y+it.r.H, plan.h)
					}
					for j := i + 1; j < plan.n; j++ {
						other := &plan.items[j]
						if _, hit := it.r.Intersect(&other.r); hit {
							t.Fatalf("search=%v plate=%v W=%d: %s %+v overlaps %s %+v — one click would fire both",
								searchExternal, plate, w, musicHeaderItemName(it.id), it.r,
								musicHeaderItemName(other.id), other.r)
						}
					}
				}
				if plan.h > panel.H {
					t.Fatalf("search=%v plate=%v W=%d: the header claimed %d px of a %d px panel",
						searchExternal, plate, w, plan.h, panel.H)
				}
			}
		}
	}
}

// TestMusicHeaderKeepsItsRowWhenTheThemeTakesTheLabel is the regression the fix is
// for: a theme that supplies BOTH a music_display plate and a music_search rect
// used to leave the whole header on one origin. The buttons must still be placed,
// still be disjoint, and the header must still reserve a row for them.
func TestMusicHeaderKeepsItsRowWhenTheThemeTakesTheLabel(t *testing.T) {
	panel := sdl.Rect{X: 4, Y: 353, W: toolbarThemeWidthPx, H: toolbarThemeHeightPx}
	plan := planMusicHeader(panel, musicNothingPlaying, "0 / 0", true, true, toolbarTestMeasure)
	for _, id := range []int{musicItemStop, musicItemRandom, musicItemExpand, musicItemCollapse, musicItemMenu} {
		if _, ok := plan.rect(id); !ok {
			t.Fatalf("%s was dropped from a %d px themed panel", musicHeaderItemName(id), panel.W)
		}
	}
	if _, ok := plan.rect(musicItemNowPlaying); ok {
		t.Error("the theme's plate owns the readout — it must not be placed twice")
	}
	if _, ok := plan.rect(musicItemSearch); ok {
		t.Error("the theme's music_search owns the field — it must not be placed twice")
	}
	if plan.h <= 0 {
		t.Fatal("the header reserved NO height while still drawing buttons — the exact overlap bug")
	}
	stop, _ := plan.rect(musicItemStop)
	expand, _ := plan.rect(musicItemExpand)
	if stop == expand {
		t.Fatalf("stop and expand share the rect %+v", stop)
	}
}

// TestMusicHeaderRowTwoIsSkippedWholeWithAnExternalSearch pins the pixels-back
// rule: an external music_search must cost the track list NOTHING, not leave a gap
// the theme never budgeted for.
func TestMusicHeaderRowTwoIsSkippedWholeWithAnExternalSearch(t *testing.T) {
	panel := sdl.Rect{X: 0, Y: 0, W: 300, H: 240}
	withSearch := planMusicHeader(panel, musicNothingPlaying, "0 / 0", false, false, toolbarTestMeasure)
	without := planMusicHeader(panel, musicNothingPlaying, "0 / 0", true, false, toolbarTestMeasure)
	if without.h >= withSearch.h {
		t.Errorf("external search reclaimed nothing: %d px vs %d px", without.h, withSearch.h)
	}
	if without.h != plStripLinePitch+plStripBodyGapPx {
		t.Errorf("a one-row header took %d px, want one line pitch + the body gap", without.h)
	}
}

// TestMusicMenuTogglesKeepTheMenuOpen pins the deliberate departure from Qt, which
// closes its context menu on every click. Setting three playback flags in a row is
// exactly what this menu exists for, so the toggle rows must not close it — while
// the two immediate ACTIONS still do.
func TestMusicMenuTogglesKeepTheMenuOpen(t *testing.T) {
	for _, row := range musicMenuRows {
		switch row.kind {
		case musicMenuVolumeView, musicMenuFadeIn, musicMenuFadeOut, musicMenuSyncPos, musicMenuLoop:
			if !musicMenuRowIsToggle(row.kind) {
				t.Errorf("%q is a toggle: acting on it must leave the menu open", row.label)
			}
		case musicMenuStop, musicMenuRandom:
			if musicMenuRowIsToggle(row.kind) {
				t.Errorf("%q is an action: it must close the menu", row.label)
			}
		}
	}
}

// TestMusicMenuCoversEveryDebloatedControl pins that nothing became unreachable.
// The header lost the "Volume"/"Track list" toggle and the two text fold buttons;
// every one of those functions has to still have a home.
func TestMusicMenuCoversEveryDebloatedControl(t *testing.T) {
	want := map[musicMenuKind]bool{
		musicMenuVolumeView: false, musicMenuFadeIn: false, musicMenuFadeOut: false,
		musicMenuSyncPos: false, musicMenuLoop: false, musicMenuStop: false, musicMenuRandom: false,
	}
	for _, row := range musicMenuRows {
		if _, ok := want[row.kind]; ok {
			want[row.kind] = true
		}
		if row.kind != musicMenuSeparator && row.label == "" {
			t.Errorf("kind %d has no label — menu rows are self-describing (Ctx.Tooltip no-ops under modalOn)", row.kind)
		}
	}
	for k, present := range want {
		if !present {
			t.Errorf("kind %d has no menu row and no header button — the function is unreachable", k)
		}
	}
}

// TestMusicKebabSurvivesItsOwnOpeningClick is the behavioural pin for the dead
// ⋮ control, and it is the half TestMusicMenuCoversEveryDebloatedControl cannot
// see: that one proves every function HAS a row, this one proves the rows can be
// reached at all.
//
// musicIconButton reports `hover && c.clicked` and consumes nothing (Ctx.Button
// is the same), while drawMusicMenu runs its click-away test LATER IN THE SAME
// FRAME. The panel hangs off the button's bottom edge, so the cursor that just
// pressed the button sits ABOVE panel.Y — outside it. An unconsumed press
// therefore closed the menu on the frame it opened and the ⋮ read as inert.
// Only driving both halves of one frame catches that.
func TestMusicKebabSurvivesItsOwnOpeningClick(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	font, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Skipf("embedded font unavailable: %v", err)
	}
	defer font.Close()

	const winW, winH = int32(800), int32(600)
	// The header's ⋮ rect; the anchor below is byte-for-byte the call site's.
	btn := sdl.Rect{X: 240, Y: 40, W: 22, H: 22}

	frame := func(open func(a *App, at sdl.Point)) *App {
		t.Helper()
		a := testTabApp(t)
		a.ctx = &Ctx{
			Ren:        ren,
			font:       font,
			textCache:  map[textKey]cachedText{},
			widthCache: map[string]int32{},
		}
		c := a.ctx
		c.mouseX, c.mouseY = btn.X+btn.W/2, btn.Y+btn.H/2
		c.clicked = true
		if !a.musicIconButton(btn, iconKebab, "More") {
			t.Fatal("a click inside the ⋮ rect must press it — fix the harness, not the code")
		}
		open(a, sdl.Point{X: btn.X, Y: btn.Y + btn.H})
		a.drawMusicMenu(winW, winH) // same frame, exactly as the courtroom orders it
		return a
	}

	if a := frame(func(a *App, at sdl.Point) { a.openMusicMenuFromButton(at, false) }); !a.musicMenuOpen {
		t.Error("the ⋮ menu closed on the frame it opened — every row it hosts (volume " +
			"view, the four MC playback flags, stop, random) is then reachable only by " +
			"right-clicking the track list")
	}
	// Mutation control: without the consumption the identical frame closes it, so
	// the assertion above is load-bearing rather than vacuously true. If this one
	// goes red, the click-away rule changed — re-derive whether the consumption is
	// still what keeps the button alive.
	if a := frame(func(a *App, at sdl.Point) { a.openMusicMenu(at, false) }); a.musicMenuOpen {
		t.Error("an unconsumed opening click no longer closes the menu, so this test can " +
			"no longer prove openMusicMenuFromButton's consumption is what fixes the ⋮")
	}
}

// TestToolboxChipTipIDsCoverEveryChip pins the dwell-id table against the chip
// table: TooltipAfter keys its shared timer on the id, and this strip draws inside
// the courtroom's zero-alloc gate, so the ids must be constant strings AND there
// must be one per chip (a shared id makes two chips share one dwell timer).
func TestToolboxChipTipIDsCoverEveryChip(t *testing.T) {
	if len(toolboxChipTipIDs) < len(compactToolboxChips) {
		t.Fatalf("%d dwell ids for %d chips", len(toolboxChipTipIDs), len(compactToolboxChips))
	}
	seen := map[string]bool{toolboxTipIDGrip: true, toolboxTipIDCollapsed: true}
	for i := range compactToolboxChips {
		id := toolboxChipTipID(i)
		if seen[id] {
			t.Errorf("chip %d reuses dwell id %q", i, id)
		}
		seen[id] = true
	}
}
