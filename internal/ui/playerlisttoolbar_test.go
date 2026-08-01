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
		roster: "12 here · live",
		status: playerStatusButtonLabel(courtroom.StatusNone),
	}
}

// toolbarItemName turns an id into something a failure message can name; an
// anonymous "item 3 overlaps item 4" would send the reader back to the source.
func toolbarItemName(id int) string {
	switch id {
	case plItemLive:
		return plLiveLabel
	case plItemRefresh:
		return plRefreshLabel
	case plItemFetchLabel:
		return plFetchLabel
	case plItemFetch:
		return "fetch button"
	case plItemLegacy:
		return plLegacyLabel
	case plItemSort:
		return "Sort"
	case plItemRooms:
		return "Rooms"
	case plItemStatusText:
		return "roster status line"
	case plItemStatus:
		return "Status"
	case plItemPairs:
		return plPairsLabel
	case plItemFollow:
		return plFollowLabel
	}
	return "unknown"
}

// TestPlayerToolbarNeverOverlapsAtAnyWidth is the headline pin, and the one the
// reported bug fails: it sweeps every width from absurd to generous, in both roster
// modes and with the Rooms button present and absent, and asserts that no two placed
// controls intersect and that every one of them stays inside the panel.
//
// It goes red if any control is ever placed on top of another (the reported
// symptom), if one is allowed to run past the panel's right or bottom edge, or if
// the strip claims more height than it left the roster.
func TestPlayerToolbarNeverOverlapsAtAnyWidth(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, multiArea := range []bool{false, true} {
			for w := int32(0); w <= 720; w++ {
				panel := sdl.Rect{X: 17, Y: 43, W: w, H: toolbarThemeHeightPx} // odd origin: a plan that ignores r.X/r.Y shows up here
				plan := planPlayerToolbar(panel, toolbarTestLabels(), legacy, multiArea, toolbarTestMeasure)
				for i := 0; i < plan.n; i++ {
					it := &plan.items[i]
					if it.r.W <= 0 || it.r.H <= 0 {
						t.Fatalf("legacy=%v multi=%v W=%d: %s placed with an empty rect %+v — an invisible but clickable control",
							legacy, multiArea, w, toolbarItemName(it.id), it.r)
					}
					if it.r.X < panel.X || it.r.X+it.r.W > panel.X+panel.W {
						t.Fatalf("legacy=%v multi=%v W=%d: %s spans x=[%d,%d), outside the panel's [%d,%d)",
							legacy, multiArea, w, toolbarItemName(it.id),
							it.r.X, it.r.X+it.r.W, panel.X, panel.X+panel.W)
					}
					if it.r.Y < panel.Y || it.r.Y+it.r.H > panel.Y+plan.h {
						t.Fatalf("legacy=%v multi=%v W=%d: %s spans y=[%d,%d), outside the %d px the strip reserved at %d",
							legacy, multiArea, w, toolbarItemName(it.id),
							it.r.Y, it.r.Y+it.r.H, plan.h, panel.Y)
					}
					for j := i + 1; j < plan.n; j++ {
						other := &plan.items[j]
						if ov := intersectRect(it.r, other.r); ov.W > 0 && ov.H > 0 {
							t.Fatalf("legacy=%v multi=%v W=%d: %s %+v overlaps %s %+v by %dx%d px",
								legacy, multiArea, w, toolbarItemName(it.id), it.r,
								toolbarItemName(other.id), other.r, ov.W, ov.H)
						}
					}
				}
				if plan.h != 0 && plan.h > panel.H-plStripMinBodyPx {
					t.Fatalf("legacy=%v multi=%v W=%d: the strip took %d of %d px, leaving the roster less than the %d px floor",
						legacy, multiArea, w, plan.h, panel.H, plStripMinBodyPx)
				}
			}
		}
	}
}

// TestPlayerToolbarStatusNeverCoversPairs pins the half of the bug that had nothing
// to do with narrow panels: the Status button was positioned at
// `follX - statBtnW - 12` — anchored on the FOLLOW checkbox — while the Pairs
// checkbox sat at `follX - pairW - 12`, i.e. the same anchor. The two therefore
// occupied the same span at EVERY width, including a maximised window, which is why
// the report saw a stray "s" (the tail of "Pairs") under "Status: none".
//
// The sweep above already covers this; it is spelled out separately because it is the
// concrete defect, and because a regression here would otherwise read as one of a
// thousand anonymous width failures.
func TestPlayerToolbarStatusNeverCoversPairs(t *testing.T) {
	for _, w := range []int32{toolbarThemeWidthPx, tornTabDefaultW, toolbarClassicWidthPx, 900, 1600} {
		panel := sdl.Rect{X: 0, Y: 0, W: w, H: toolbarThemeHeightPx}
		plan := planPlayerToolbar(panel, toolbarTestLabels(), false, false, toolbarTestMeasure)
		var status, pairs sdl.Rect
		var haveStatus, havePairs bool
		for i := 0; i < plan.n; i++ {
			switch plan.items[i].id {
			case plItemStatus:
				status, haveStatus = plan.items[i].r, true
			case plItemPairs:
				pairs, havePairs = plan.items[i].r, true
			}
		}
		if !haveStatus || !havePairs {
			t.Fatalf("W=%d: Status and Pairs must both be placed at this width (status=%v pairs=%v)", w, haveStatus, havePairs)
		}
		if ov := intersectRect(status, pairs); ov.W > 0 && ov.H > 0 {
			t.Errorf("W=%d: Status %+v covers Pairs %+v by %dx%d px", w, status, pairs, ov.W, ov.H)
		}
	}
}

// TestPlayerToolbarKeepsEveryControlInAThemesPanel is the counterweight to the
// overlap sweep: laying out nothing would satisfy "no overlaps" trivially. At the
// width a real theme gives this panel every control must still be REACHABLE, because
// Pairs, Follow and Status exist nowhere else in the UI — dropping them inside a
// theme would delete the only way to reach those features, the same argument
// drawCourtroomThemed makes for keeping the Music/Areas/Player List chips.
//
// It goes red if the planner ever "fixes" a narrow panel by dropping a control
// instead of wrapping.
func TestPlayerToolbarKeepsEveryControlInAThemesPanel(t *testing.T) {
	panel := sdl.Rect{X: 0, Y: 0, W: toolbarThemeWidthPx, H: toolbarThemeHeightPx}
	for _, tc := range []struct {
		name   string
		legacy bool
		want   []int
	}{
		{"live", false, []int{plItemLive, plItemRefresh, plItemLegacy, plItemSort, plItemStatusText, plItemStatus, plItemPairs, plItemFollow}},
		{"legacy", true, []int{plItemFetchLabel, plItemFetch, plItemLegacy, plItemSort, plItemStatusText, plItemStatus, plItemPairs, plItemFollow}},
	} {
		plan := planPlayerToolbar(panel, toolbarTestLabels(), tc.legacy, true, toolbarTestMeasure)
		seen := map[int]int{}
		for i := 0; i < plan.n; i++ {
			seen[plan.items[i].id]++
		}
		for _, id := range tc.want {
			if seen[id] == 0 {
				t.Errorf("%s: %s is missing from a %d px panel — a theme user cannot reach it at all",
					tc.name, toolbarItemName(id), toolbarThemeWidthPx)
			}
		}
		if seen[plItemRooms] == 0 {
			t.Errorf("%s: the Rooms button is missing although the roster spans areas", tc.name)
		}
		if tc.legacy && seen[plItemFetch] != len(plFetchCmds) {
			t.Errorf("legacy: %d of %d fetch buttons placed", seen[plItemFetch], len(plFetchCmds))
		}
		t.Logf("%s @ %dpx: %d controls in %d px of strip", tc.name, toolbarThemeWidthPx, plan.n, plan.h)
	}
}

// TestPlayerToolbarWidePanelKeepsTheOldTwoRowShape guards against fixing the narrow
// case by regressing the wide one. The classic docked panel and a torn-off tab were
// never broken, and the strip's metrics were chosen so a panel with room lays out in
// exactly the two rows it always did — 2*plStripLinePitch + plStripBodyGapPx = 54 px,
// which is what the old `r.Y += 26` then `r.Y += 28` reserved.
//
// It goes red if the strip starts wrapping (or stops wrapping to two rows) at a width
// where it used to fit, silently moving the roster up or down the panel.
func TestPlayerToolbarWidePanelKeepsTheOldTwoRowShape(t *testing.T) {
	const oldTwoRowHeight = int32(54) // the pre-strip `+= 26` and `+= 28`
	panel := sdl.Rect{X: 0, Y: 0, W: toolbarClassicWidthPx, H: 400}
	plan := planPlayerToolbar(panel, toolbarTestLabels(), false, false, toolbarTestMeasure)
	if plan.h != oldTwoRowHeight {
		t.Errorf("a %d px panel reserves %d px for the toolbar, want the historical %d",
			toolbarClassicWidthPx, plan.h, oldTwoRowHeight)
	}
	rows := map[int32]bool{}
	for i := 0; i < plan.n; i++ {
		rows[plan.items[i].r.Y] = true
	}
	if len(rows) != 2 {
		t.Errorf("a %d px panel laid the toolbar out over %d rows, want 2", toolbarClassicWidthPx, len(rows))
	}
	// The mode half and the sort half must stay on SEPARATE rows even when both would
	// fit on one: newline() is what keeps "where the roster comes from" apart from
	// "how it is shown", and losing it would reshuffle the whole strip.
	var legacyY, sortY int32 = -1, -1
	for i := 0; i < plan.n; i++ {
		switch plan.items[i].id {
		case plItemLegacy:
			legacyY = plan.items[i].r.Y
		case plItemSort:
			sortY = plan.items[i].r.Y
		}
	}
	if legacyY < 0 || sortY < 0 {
		t.Fatalf("a %d px panel must place both halves (legacyY=%d sortY=%d)", toolbarClassicWidthPx, legacyY, sortY)
	}
	if sortY != legacyY+plStripLinePitch {
		t.Errorf("Sort sits at y=%d, want one line (%d px) below the mode row at y=%d",
			sortY, plStripLinePitch, legacyY)
	}
}

// TestPlayerToolbarWrapsRatherThanClipsWhenNarrow pins the chosen degradation
// STRATEGY, not just its absence of overlap: between the classic width and a theme's,
// the strip must gain lines rather than start clamping controls to slivers. A plan
// that silently narrowed every button to a third of its label would satisfy the
// overlap sweep and be unusable.
//
// It goes red if a control at a theme's width is drawn narrower than the label it was
// measured for while a further line was still available.
func TestPlayerToolbarWrapsRatherThanClipsWhenNarrow(t *testing.T) {
	panel := sdl.Rect{X: 0, Y: 0, W: toolbarThemeWidthPx, H: toolbarThemeHeightPx}
	wide := planPlayerToolbar(sdl.Rect{X: 0, Y: 0, W: toolbarClassicWidthPx, H: toolbarThemeHeightPx},
		toolbarTestLabels(), false, false, toolbarTestMeasure)
	narrow := planPlayerToolbar(panel, toolbarTestLabels(), false, false, toolbarTestMeasure)
	if narrow.h <= wide.h {
		t.Errorf("a %d px panel took %d px of strip and a %d px panel %d — the narrow one must WRAP, not squeeze",
			toolbarThemeWidthPx, narrow.h, toolbarClassicWidthPx, wide.h)
	}
	if narrow.n != wide.n {
		t.Errorf("wrapping lost controls: %d placed at %d px vs %d at %d px",
			narrow.n, toolbarThemeWidthPx, wide.n, toolbarClassicWidthPx)
	}
	// Every fixed-width control must be its full measured width — only the roster
	// status line (the sole flex item) is allowed to shrink.
	for i := 0; i < narrow.n; i++ {
		it := &narrow.items[i]
		if it.id == plItemStatusText {
			continue
		}
		want := plBtnW(it.label, toolbarTestMeasure)
		switch it.id {
		case plItemLive, plItemFetchLabel:
			want = toolbarTestMeasure(it.label)
		case plItemLegacy, plItemPairs, plItemFollow:
			want = plCheckW(it.label, toolbarTestMeasure)
		case plItemStatus:
			want = plBtnW(plStatusWidestLabel, toolbarTestMeasure) // sized to the widest, not the current
		}
		if it.r.W != want {
			t.Errorf("%s was placed %d px wide, want its full %d px (a clipped label with room to wrap)",
				toolbarItemName(it.id), it.r.W, want)
		}
	}
	t.Logf("theme panel %dpx: %d controls over %d px of strip (wide: %d px)",
		toolbarThemeWidthPx, narrow.n, narrow.h, wide.h)
}

// TestPlayerToolbarCompactsLabelsBelowATornTabsWidth pins the step that keeps
// wrapping affordable. Wrapping alone put FIVE lines of toolbar into a theme's
// 212 px panel — over half the list's height — because "Refresh details" and
// "Legacy snapshot" are long enough to own a line each. Below plStripCompactPx the
// two switch to their short forms and the whole mode half fits on one line again.
//
// It goes red if the ladder stops firing (a theme panel goes back to five lines) or
// if it starts firing on panels AsyncAO itself creates, shortening labels on a wide
// docked panel that had room for them all along.
func TestPlayerToolbarCompactsLabelsBelowATornTabsWidth(t *testing.T) {
	labelOf := func(plan plToolbarPlan, id int) string {
		for i := 0; i < plan.n; i++ {
			if plan.items[i].id == id {
				return plan.items[i].label
			}
		}
		return ""
	}
	narrow := planPlayerToolbar(sdl.Rect{X: 0, Y: 0, W: toolbarThemeWidthPx, H: toolbarThemeHeightPx},
		toolbarTestLabels(), false, false, toolbarTestMeasure)
	if got := labelOf(narrow, plItemRefresh); got != plRefreshShortLabel {
		t.Errorf("a %d px panel labels Refresh %q, want the compact %q", toolbarThemeWidthPx, got, plRefreshShortLabel)
	}
	if got := labelOf(narrow, plItemLegacy); got != plLegacyShortLabel {
		t.Errorf("a %d px panel labels the mode box %q, want the compact %q", toolbarThemeWidthPx, got, plLegacyShortLabel)
	}
	// The point of compacting: the mode half collapses back onto a single line.
	modeRows := map[int32]bool{}
	for i := 0; i < narrow.n; i++ {
		switch narrow.items[i].id {
		case plItemLive, plItemRefresh, plItemLegacy:
			modeRows[narrow.items[i].r.Y] = true
		}
	}
	if len(modeRows) != 1 {
		t.Errorf("the mode half wrapped over %d lines at %d px even compacted — the short labels are not short enough",
			len(modeRows), toolbarThemeWidthPx)
	}
	// At and above the threshold the full labels come back.
	wide := planPlayerToolbar(sdl.Rect{X: 0, Y: 0, W: plStripCompactPx, H: toolbarThemeHeightPx},
		toolbarTestLabels(), false, false, toolbarTestMeasure)
	if got := labelOf(wide, plItemRefresh); got != plRefreshLabel {
		t.Errorf("a %d px panel labels Refresh %q, want the full %q", plStripCompactPx, got, plRefreshLabel)
	}
	if got := labelOf(wide, plItemLegacy); got != plLegacyLabel {
		t.Errorf("a %d px panel labels the mode box %q, want the full %q", plStripCompactPx, got, plLegacyLabel)
	}
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
		plan := planPlayerToolbar(panel, toolbarTestLabels(), false, true, toolbarTestMeasure)
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
//
// It goes red if a sub-tick-box panel ever gets a sliver of chrome painted into it,
// or if the strip reserves height for controls it did not place.
func TestPlayerToolbarDegradesToNothingWhenAbsurdlyNarrow(t *testing.T) {
	for w := int32(0); w < plStripMinItemPx; w++ {
		panel := sdl.Rect{X: 0, Y: 0, W: w, H: toolbarThemeHeightPx}
		plan := planPlayerToolbar(panel, toolbarTestLabels(), false, false, toolbarTestMeasure)
		if plan.n != 0 || plan.h != 0 {
			t.Errorf("W=%d (under the %d px floor): placed %d controls in %d px, want nothing at all",
				w, plStripMinItemPx, plan.n, plan.h)
		}
	}
	// And one pixel above the floor it must start placing again, or the floor is
	// really a permanent blackout.
	panel := sdl.Rect{X: 0, Y: 0, W: plStripMinItemPx, H: toolbarThemeHeightPx}
	if plan := planPlayerToolbar(panel, toolbarTestLabels(), false, false, toolbarTestMeasure); plan.n == 0 {
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
		_ = planPlayerToolbar(panel, labels, false, true, toolbarTestMeasure)
	}); n != 0 {
		t.Errorf("planPlayerToolbar allocates %.1f/op with a plain measure, want 0", n)
	}
	c := &Ctx{} // no font: TextWidth returns 0, which is fine — this measures the CALL, not the text
	if n := testing.AllocsPerRun(200, func() {
		_ = planPlayerToolbar(panel, labels, false, true, c.TextWidth)
	}); n != 0 {
		t.Errorf("planPlayerToolbar allocates %.1f/op with Ctx.TextWidth, want 0 — the measure func is escaping", n)
	}
}

// TestCheckboxWidthHasOneFormula guards the split made for the planner: the layout
// pass measures a tick box without a renderer while the widget draws one with, and
// two copies of `box + gap + text` would drift the moment either changed.
func TestCheckboxWidthHasOneFormula(t *testing.T) {
	c := &Ctx{}
	for _, label := range []string{plPairsLabel, plFollowLabel, plLegacyLabel} {
		if got, want := c.CheckboxWidth(label), checkboxWidthFor(label, c.TextWidth); got != want {
			t.Errorf("CheckboxWidth(%q) = %d but checkboxWidthFor = %d — the two have drifted", label, got, want)
		}
	}
}
