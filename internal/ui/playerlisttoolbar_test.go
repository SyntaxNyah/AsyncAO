package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

// The Players tab's toolbar is drawn into three panels of three different widths —
// the classic docked panel, a torn-off tab (tornTabDefaultW = 320, user-resizable
// below that) and a theme's music_list rect — and it used to lay itself out with
// hardcoded offsets that only held at the widest of the three. These tests pin the
// property that failure had: across every plausible width, no two controls may
// share a pixel and none may leave the panel.
//
// They drive the REAL planner. An earlier draft re-derived the placements in the
// test body, which would have passed with the production arithmetic deleted; the
// planner was split out of drawPlayerList precisely so there is something here to be
// wrong about.

const (
	// toolbarTestGlyphPx is the per-rune width the stub measure reports. The chrome
	// font is loaded from a TTF at runtime and is unavailable headlessly (Ctx.TextWidth
	// returns 0 with no font), so the tests need their own measure — and it has to be
	// REALISTIC, not the 1 px/rune of checkboxin_test.go's fixedWidth, or every label
	// would fit everywhere and the wrap path would never run. 7 px is close to the
	// average advance of the 14 px UI face for Latin text.
	toolbarTestGlyphPx = int32(7)
	// toolbarThemeWidthPx is the usable width a theme really hands this panel:
	// aceattorney2x declares music_list = 0,323,216,277 and insetThemedBody takes
	// themedLogInsetPx off the left, leaving 212. This is the width the bug was
	// reported at.
	toolbarThemeWidthPx = int32(212)
	// toolbarThemeHeightPx is that same rect's usable height: 277 less
	// insetThemedBody's two margins, less the Music/Areas/Player List chip strip.
	toolbarThemeHeightPx = 277 - 2*themedLogInsetPx - themedStripH
	// toolbarClassicWidthPx stands in for the classic docked right-hand panel, which
	// is comfortably wide — the width at which the OLD code looked correct.
	toolbarClassicWidthPx = int32(500)
)

// toolbarTestMeasure is the injected text measurement: proportional to rune count so
// a longer label really is wider, and independent of any font being loaded.
func toolbarTestMeasure(s string) int32 { return int32(len([]rune(s))) * toolbarTestGlyphPx }

// toolbarTestLabels are representative frame-varying labels — the defaults a fresh
// session shows, at their real lengths.
func toolbarTestLabels() plToolbarLabels {
	return plToolbarLabels{
		sort:   "Sort: " + playerSortLabel(playerSortUID),
		rooms:  "Rooms: " + areaSortLabel(areaSortGas),
		status: playerStatusButtonLabel(courtroom.StatusNone),
	}
}

// toolbarItemName turns an id into something a failure message can name; an
// anonymous "item 3 overlaps item 4" would send the reader back to the source.
func toolbarItemName(id int) string {
	switch id {
	case plItemSort:
		return "Sort"
	case plItemRooms:
		return "Rooms"
	case plItemStatus:
		return "Status"
	case plItemMenu:
		return "⋮ overflow"
	}
	return "unknown"
}

// toolbarPlanRect is the rect a plan placed for id, or ok=false when it was dropped.
func toolbarPlanRect(plan plToolbarPlan, id int) (sdl.Rect, bool) {
	for i := 0; i < plan.n; i++ {
		if plan.items[i].id == id {
			return plan.items[i].r, true
		}
	}
	return sdl.Rect{}, false
}

// TestPlayerToolbarNeverOverlapsAtAnyWidth is the headline pin, and the one the
// reported bug fails: it sweeps every width from absurd to generous, with the Rooms
// button present and absent, and asserts that no two placed controls intersect and
// that every one of them stays inside the panel.
//
// It goes red if any control is ever placed on top of another (the reported
// symptom), if one is allowed to run past the panel's right or bottom edge, or if
// the strip claims more height than it left the roster.
func TestPlayerToolbarNeverOverlapsAtAnyWidth(t *testing.T) {
	for _, multiArea := range []bool{false, true} {
		for w := int32(0); w <= 720; w++ {
			panel := sdl.Rect{X: 17, Y: 43, W: w, H: toolbarThemeHeightPx} // odd origin: a plan that ignores r.X/r.Y shows up here
			plan := planPlayerToolbar(panel, toolbarTestLabels(), multiArea, toolbarTestMeasure)
			for i := 0; i < plan.n; i++ {
				it := &plan.items[i]
				if it.r.W <= 0 || it.r.H <= 0 {
					t.Fatalf("multi=%v W=%d: %s placed with an empty rect %+v — an invisible but clickable control",
						multiArea, w, toolbarItemName(it.id), it.r)
				}
				if it.r.X < panel.X || it.r.X+it.r.W > panel.X+panel.W {
					t.Fatalf("multi=%v W=%d: %s spans x=[%d,%d), outside the panel's [%d,%d)",
						multiArea, w, toolbarItemName(it.id),
						it.r.X, it.r.X+it.r.W, panel.X, panel.X+panel.W)
				}
				if it.r.Y < panel.Y || it.r.Y+it.r.H > panel.Y+plan.h {
					t.Fatalf("multi=%v W=%d: %s spans y=[%d,%d), outside the %d px the strip reserved at %d",
						multiArea, w, toolbarItemName(it.id),
						it.r.Y, it.r.Y+it.r.H, plan.h, panel.Y)
				}
				for j := i + 1; j < plan.n; j++ {
					other := &plan.items[j]
					if ov := intersectRect(it.r, other.r); ov.W > 0 && ov.H > 0 {
						t.Fatalf("multi=%v W=%d: %s %+v overlaps %s %+v by %dx%d px",
							multiArea, w, toolbarItemName(it.id), it.r,
							toolbarItemName(other.id), other.r, ov.W, ov.H)
					}
				}
			}
			if plan.h != 0 && plan.h > panel.H-plStripMinBodyPx {
				t.Fatalf("multi=%v W=%d: the strip took %d of %d px, leaving the roster less than the %d px floor",
					multiArea, w, plan.h, panel.H, plStripMinBodyPx)
			}
		}
	}
}

// TestPlayerToolbarIsOneRowOnARealPanel is the debloat's own pin: three header rows
// (the ● LIVE / Refresh / Legacy mode row, the Pairs/Follow row and the
// "12 here · live" readout) had to collapse to ONE. On any panel AsyncAO itself
// creates — the classic dock, a freshly torn-off tab — the whole toolbar is a single
// line of Sort · Rooms · Status · ⋮, and it reserves the one row's worth of height
// (plStripLinePitch + plStripBodyGapPx) that the old strip spent per row.
//
// It goes red if a control creeps back into the strip, or if the metrics change so
// that a comfortable panel starts wrapping again.
func TestPlayerToolbarIsOneRowOnARealPanel(t *testing.T) {
	const oneRowHeight = plStripLinePitch + plStripBodyGapPx
	// A SINGLE-AREA roster (the ordinary case: Sort · Status · ⋮) fits one line from a
	// freshly torn-off tab upwards. With area groups the Rooms button joins it and the
	// line needs a docked panel's width — which is where the roster actually lives.
	for _, tc := range []struct {
		w         int32
		multiArea bool
		want      int
	}{
		{tornTabDefaultW, false, 3},
		{toolbarClassicWidthPx, false, 3},
		{toolbarClassicWidthPx, true, 4},
		{900, true, 4},
		{1600, true, 4},
	} {
		panel := sdl.Rect{X: 0, Y: 0, W: tc.w, H: 400}
		plan := planPlayerToolbar(panel, toolbarTestLabels(), tc.multiArea, toolbarTestMeasure)
		if plan.h != oneRowHeight {
			t.Errorf("W=%d multi=%v: reserves %d px of toolbar, want one row (%d)", tc.w, tc.multiArea, plan.h, oneRowHeight)
		}
		rows := map[int32]bool{}
		for i := 0; i < plan.n; i++ {
			rows[plan.items[i].r.Y] = true
		}
		if len(rows) != 1 {
			t.Errorf("W=%d multi=%v: laid the toolbar out over %d rows, want 1", tc.w, tc.multiArea, len(rows))
		}
		if plan.n != tc.want {
			t.Errorf("W=%d multi=%v: placed %d controls, want %d", tc.w, tc.multiArea, plan.n, tc.want)
		}
	}
}

// TestPlayerToolbarKeepsEveryControlInAThemesPanel is the counterweight to the
// overlap sweep: laying out nothing would satisfy "no overlaps" trivially. At the
// width a real theme gives this panel every control must still be REACHABLE — and
// the ⋮ most of all, since it is now the only route to Refresh, the legacy snapshot,
// pair status and follow.
//
// It goes red if the planner ever "fixes" a narrow panel by dropping a control
// instead of wrapping.
func TestPlayerToolbarKeepsEveryControlInAThemesPanel(t *testing.T) {
	panel := sdl.Rect{X: 0, Y: 0, W: toolbarThemeWidthPx, H: toolbarThemeHeightPx}
	plan := planPlayerToolbar(panel, toolbarTestLabels(), true, toolbarTestMeasure)
	for _, id := range []int{plItemSort, plItemRooms, plItemStatus, plItemMenu} {
		if _, ok := toolbarPlanRect(plan, id); !ok {
			t.Errorf("%s is missing from a %d px panel — a theme user cannot reach it at all",
				toolbarItemName(id), toolbarThemeWidthPx)
		}
	}
	// Single-area rosters have no groups to order, so Rooms is the one control that
	// may legitimately be absent — and only then.
	single := planPlayerToolbar(panel, toolbarTestLabels(), false, toolbarTestMeasure)
	if _, ok := toolbarPlanRect(single, plItemRooms); ok {
		t.Error("Rooms was placed for a single-area roster — there is nothing for it to order")
	}
	for _, id := range []int{plItemSort, plItemStatus, plItemMenu} {
		if _, ok := toolbarPlanRect(single, id); !ok {
			t.Errorf("%s is missing from a single-area %d px panel", toolbarItemName(id), toolbarThemeWidthPx)
		}
	}
	t.Logf("theme panel %dpx: %d controls in %d px of strip", toolbarThemeWidthPx, plan.n, plan.h)
}

// TestPlayerToolbarWrapsRatherThanClipsWhenNarrow pins the chosen degradation
// STRATEGY, not just its absence of overlap: between the classic width and a theme's,
// the strip must gain lines rather than start clamping controls to slivers. A plan
// that silently narrowed every button to a third of its label would satisfy the
// overlap sweep and be unusable.
func TestPlayerToolbarWrapsRatherThanClipsWhenNarrow(t *testing.T) {
	panel := sdl.Rect{X: 0, Y: 0, W: toolbarThemeWidthPx, H: toolbarThemeHeightPx}
	wide := planPlayerToolbar(sdl.Rect{X: 0, Y: 0, W: toolbarClassicWidthPx, H: toolbarThemeHeightPx},
		toolbarTestLabels(), true, toolbarTestMeasure)
	narrow := planPlayerToolbar(panel, toolbarTestLabels(), true, toolbarTestMeasure)
	if narrow.h <= wide.h {
		t.Errorf("a %d px panel took %d px of strip and a %d px panel %d — the narrow one must WRAP, not squeeze",
			toolbarThemeWidthPx, narrow.h, toolbarClassicWidthPx, wide.h)
	}
	if narrow.n != wide.n {
		t.Errorf("wrapping lost controls: %d placed at %d px vs %d at %d px",
			narrow.n, toolbarThemeWidthPx, wide.n, toolbarClassicWidthPx)
	}
	// Every control must be its full measured width — nothing in this strip flexes.
	for i := 0; i < narrow.n; i++ {
		it := &narrow.items[i]
		want := plBtnW(it.label, toolbarTestMeasure)
		switch it.id {
		case plItemStatus:
			want = plBtnW(plStatusWidestLabel, toolbarTestMeasure) // sized to the widest, not the current
		case plItemMenu:
			want = plStripMenuPx
		}
		if it.r.W != want {
			t.Errorf("%s was placed %d px wide, want its full %d px (a clipped label with room to wrap)",
				toolbarItemName(it.id), it.r.W, want)
		}
	}
	t.Logf("theme panel %dpx: %d controls over %d px of strip (wide: %d px)",
		toolbarThemeWidthPx, narrow.n, narrow.h, wide.h)
}

// TestPlayerToolbarStopsBeforeEatingTheRoster is the VERTICAL half of the
// degradation contract. Wrapping is unbounded by nature, and a short panel — a
// theme with a stubby music_list, or a torn-off tab dragged small — could otherwise
// end up all toolbar and no list. The strip stops opening lines once the roster would
// fall below plStripMinBodyPx.
//
// It goes red if the toolbar ever leaves the list less than one area-header row, or
// if it reserves height it did not place any control on.
func TestPlayerToolbarStopsBeforeEatingTheRoster(t *testing.T) {
	for h := int32(0); h <= 260; h++ {
		panel := sdl.Rect{X: 0, Y: 0, W: toolbarThemeWidthPx, H: h}
		plan := planPlayerToolbar(panel, toolbarTestLabels(), true, toolbarTestMeasure)
		if plan.h == 0 {
			if plan.n != 0 {
				t.Fatalf("H=%d: %d controls placed in a zero-height strip", h, plan.n)
			}
			continue
		}
		if plan.h+plStripMinBodyPx > h {
			t.Fatalf("H=%d: the toolbar took %d px, leaving the roster %d — under the %d px floor",
				h, plan.h, h-plan.h, plStripMinBodyPx)
		}
		if plan.n == 0 {
			t.Fatalf("H=%d: the strip reserved %d px without placing a single control", h, plan.h)
		}
	}
}

// TestPlayerToolbarDegradesToNothingWhenAbsurdlyNarrow pins the floor. Below a bare
// tick box and its gap there is no honest way to draw a labelled control, so the
// strip paints none and hands the whole panel to the roster — insetThemedBody's
// "a rect too small to hold the margin is returned untouched" rule.
func TestPlayerToolbarDegradesToNothingWhenAbsurdlyNarrow(t *testing.T) {
	for w := int32(0); w < plStripMinItemPx; w++ {
		panel := sdl.Rect{X: 0, Y: 0, W: w, H: toolbarThemeHeightPx}
		plan := planPlayerToolbar(panel, toolbarTestLabels(), false, toolbarTestMeasure)
		if plan.n != 0 || plan.h != 0 {
			t.Errorf("W=%d (under the %d px floor): placed %d controls in %d px, want nothing at all",
				w, plStripMinItemPx, plan.n, plan.h)
		}
	}
	// And one pixel above the floor it must start placing again, or the floor is
	// really a permanent blackout.
	panel := sdl.Rect{X: 0, Y: 0, W: plStripMinItemPx, H: toolbarThemeHeightPx}
	if plan := planPlayerToolbar(panel, toolbarTestLabels(), false, toolbarTestMeasure); plan.n == 0 {
		t.Errorf("W=%d (exactly the floor): nothing placed, so the floor is off by one", plStripMinItemPx)
	}
}

// TestStatusButtonIsSizedToItsWidestLabel pins the reason the Status button measures
// a string it may not be drawing. Its width must not change as the status cycles: in
// a WRAPPING strip a width change does not merely nudge one button, it can reflow
// every control after it onto a different line, so the toolbar would visibly jump
// each time you set yourself AFK.
//
// It goes red when a new courtroom.Status gets a label longer than "Writing" and
// plStatusWidestLabel is not updated with it.
func TestStatusButtonIsSizedToItsWidestLabel(t *testing.T) {
	widest := toolbarTestMeasure(plStatusWidestLabel)
	for s := courtroom.Status(0); s < courtroom.StatusCount; s++ {
		label := playerStatusButtonLabel(s)
		if got := toolbarTestMeasure(label); got > widest {
			t.Errorf("%q measures %d px, wider than plStatusWidestLabel %q at %d — the button would jump on that status",
				label, got, plStatusWidestLabel, widest)
		}
	}
	// StatusNone has no label of its own; a bare "Status: " reads as a broken widget.
	if got := playerStatusButtonLabel(courtroom.StatusNone); got != plStatusPrefix+plStatusNoneLabel {
		t.Errorf("StatusNone renders as %q, want %q", got, plStatusPrefix+plStatusNoneLabel)
	}
}

// TestRosterDebloatLeftNothingUnreachable is the guard the consolidation owes the
// user. Four FUNCTIONS left the Players toolbar — Refresh details, the legacy
// snapshot switch, pair status and follow — and none of them exists anywhere else in
// the UI, so deleting the row without rehoming them would have deleted the features.
// They moved into the list panel's ⋮ overflow, which is what this asserts.
//
// It goes red if a row is dropped from musicMenuRows, if one loses its self-
// describing label (Ctx.Tooltip no-ops under modalOn, so the label is the only
// explanation a menu row gets), or if one stops behaving as a toggle.
func TestRosterDebloatLeftNothingUnreachable(t *testing.T) {
	seen := map[musicMenuKind]string{}
	for _, row := range musicMenuRows {
		if row.kind == musicMenuSeparator {
			continue
		}
		seen[row.kind] = row.label
	}
	for _, k := range []musicMenuKind{musicMenuRosterRefresh, musicMenuRosterLegacy, musicMenuPairStatus, musicMenuFollow} {
		label, ok := seen[k]
		if !ok {
			t.Errorf("roster menu kind %d has no row — the control it replaced is now unreachable", k)
			continue
		}
		if label == "" {
			t.Errorf("roster menu kind %d has an empty label — menu rows must describe themselves", k)
		}
	}
	// The three toggles must stay toggles: the menu deliberately stays OPEN on a
	// toggle row so several can be set in one visit (musicMenuRowIsToggle).
	for _, k := range []musicMenuKind{musicMenuRosterLegacy, musicMenuPairStatus, musicMenuFollow} {
		if !musicMenuRowIsToggle(k) {
			t.Errorf("roster menu kind %d is not a toggle — the menu would close on every flip", k)
		}
	}
	// Refresh is a one-shot command, so it must NOT be a toggle (the menu closes).
	if musicMenuRowIsToggle(musicMenuRosterRefresh) {
		t.Error("Refresh roster details is a one-shot command, not a toggle")
	}
}

// TestRosterDetailCmdIsNeverInert is the other half of that guard: a rehomed
// control that answers "unknown command" is no more reachable than a deleted one.
// The trio the toolbar dropped (/ga · /gas · /getarea) existed BECAUSE no single
// spelling works everywhere, so the one row that replaced it has to choose per
// family — the same fact rosterCmd encodes when it picks /gas over /getareas.
func TestRosterDetailCmdIsNeverInert(t *testing.T) {
	for _, tc := range []struct {
		software string
		want     string
	}{
		// Athena registers exactly ONE roster command, and it is neither /ga nor
		// /getarea: "players" (../Athena/internal/athena/commands.go:282,
		// `Usage: /players [-a]`), with no ga/gas/getarea entry anywhere in
		// initCommands and no alias field on the Command struct to hide one. A /ga
		// here would be precisely the inert row this test exists to forbid.
		{"Athena", rosterCmdPlayers},
		// Nyathena registers "players" as well (../Nyathena/internal/athena/
		// commands_registry.go:779), with "ga" as a shortcut onto the same
		// cmdPlayers handler (:310) — so the spelling that also works on Athena
		// costs it nothing.
		{"Nyathena", rosterCmdPlayers},
		// Whisker's command dispatcher maps players onto its own cmd_ga
		// (Whisker/src/commands.c3:78) and has NO getarea case whatsoever (:77-79),
		// so the long spelling falls through to "Unknown command: /getarea".
		// Whisker is also one of the families with no built-in 2.11 player list
		// (serverhelp.go, plistPlugin), i.e. exactly a family this row is the last
		// roster control for.
		{"Whisker", rosterCmdPlayers},
		// Akashi (and the WAP fork) register getarea/getareas and neither /players
		// nor a short alias (../akashi/src/aoclient.cpp:28-29) — the same asymmetry
		// that makes /gas fail there. tsuserver3/KFO spell it long too
		// (../KFO-Server/server/commands/areas.py:271,288), and so does an
		// unrecognised server.
		{"Akashi 1.8", rosterCmdGetarea},
		{"WAP-Akashi", rosterCmdGetarea},
		{"KFO-Server", rosterCmdGetarea},
		{"", rosterCmdGetarea},
		{"some-fork-nobody-has-heard-of", rosterCmdGetarea},
	} {
		a := &App{}
		a.sess = &courtroom.Session{Software: tc.software}
		if got := a.rosterDetailCmd(); got != tc.want {
			t.Errorf("software %q → %q, want %q", tc.software, got, tc.want)
		}
	}
	// The two spellings must stay DIFFERENT commands: collapsing them to one string
	// would quietly restore the single hardcoded command this exists to replace.
	if rosterCmdPlayers == rosterCmdGetarea {
		t.Error("the two area-detail spellings are the same string")
	}
	// And no session at all must not send anything odd — the row is disabled there
	// (musicMenuRowEnabled), but the accessor still has to answer.
	if got := (&App{}).rosterDetailCmd(); got != rosterCmdGetarea {
		t.Errorf("with no session rosterDetailCmd = %q, want %q", got, rosterCmdGetarea)
	}
}

// TestPlayerToolbarPlanIsAllocFree is a hard-rule gate: drawPlayerList builds a plan
// on EVERY frame the Players tab is up, and internal/ui is held to a zero-allocation
// render loop. The plan is a fixed-size value for exactly this reason.
//
// The second half passes the real Ctx.TextWidth METHOD VALUE, which is what
// drawPlayerList hands the planner. A method value is a closure over the receiver;
// if the planner ever let its measure func escape, that closure would be heap
// allocated once per frame and only this half would catch it.
func TestPlayerToolbarPlanIsAllocFree(t *testing.T) {
	panel := sdl.Rect{X: 0, Y: 0, W: toolbarThemeWidthPx, H: toolbarThemeHeightPx}
	labels := toolbarTestLabels() // built once: the caller owns the label strings
	if n := testing.AllocsPerRun(200, func() {
		_ = planPlayerToolbar(panel, labels, true, toolbarTestMeasure)
	}); n != 0 {
		t.Errorf("planPlayerToolbar allocates %.1f/op with a plain measure, want 0", n)
	}
	c := &Ctx{} // no font: TextWidth returns 0, which is fine — this measures the CALL, not the text
	if n := testing.AllocsPerRun(200, func() {
		_ = planPlayerToolbar(panel, labels, true, c.TextWidth)
	}); n != 0 {
		t.Errorf("planPlayerToolbar allocates %.1f/op with Ctx.TextWidth, want 0 — the measure func is escaping", n)
	}
}

// TestCheckboxWidthHasOneFormula guards the split made for the planner: the layout
// pass measures a tick box without a renderer while the widget draws one with, and
// two copies of `box + gap + text` would drift the moment either changed.
func TestCheckboxWidthHasOneFormula(t *testing.T) {
	c := &Ctx{}
	for _, label := range []string{"Pairs", "Follow", "Legacy snapshot", ""} {
		if got, want := c.CheckboxWidth(label), checkboxWidthFor(label, c.TextWidth); got != want {
			t.Errorf("CheckboxWidth(%q) = %d but checkboxWidthFor = %d — the two have drifted", label, got, want)
		}
	}
}
