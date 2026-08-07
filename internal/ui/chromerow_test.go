package ui

// THE CHROME-ROW REFLOW GUARD (v1.90.0 W7b).
//
// These gates DRIVE the shipping planner and the shipping Music header — no
// re-implementation of the placement anywhere below, because a mirror of the cursor
// would pass while the real one overlapped (rule 11).
//
// The headline is TestSqueezedMusicPanelNeverStacksItsHeader: the reported defect was
// a screenshot of an imported AO2 theme whose music panel drew Expand all and
// Collapse all on top of each other, so the gate is the same arithmetic — a squeezed
// design rect, through the real chip strip and the real header planner, asserting that
// no two boxes intersect.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// chromeProbeCtls is a row with the Music header's real shape: five fixed icon
// buttons, one flexible readout, and a second-line pair.
func chromeProbeCtls() []chromeCtl {
	return []chromeCtl{
		{id: 1, w: musicIconPx, min: musicIconPx, prio: musicDropShortcut},
		{id: 2, w: musicIconPx, min: musicIconPx, prio: musicDropShortcut},
		{id: 3, w: musicIconPx, min: musicIconPx, prio: musicDropOnlyRoute},
		{id: 4, w: musicIconPx, min: musicIconPx, prio: musicDropOnlyRoute},
		{id: 5, w: musicIconPx, min: musicIconPx, prio: chromeCtlNeverDrop},
		{id: 6, w: 180, min: musicNameMinPx, prio: musicDropReadout},
		{id: 7, w: 140, min: musicSearchMinPx, prio: musicDropSearch, brk: true},
		{id: 8, w: 40, min: 40, prio: musicDropReadout},
	}
}

// assertNoOverlap is the ONE assertion every gate here shares: every placed box is
// inside the block, and no two of them intersect.
func assertNoOverlap(t *testing.T, what string, block sdl.Rect, p *chromeRowPlan) {
	t.Helper()
	for i := 0; i < p.placed(); i++ {
		a := p.slot[i].r
		if a.W <= 0 || a.H <= 0 {
			t.Errorf("%s: control %d was placed with a degenerate rect %+v", what, p.slot[i].id, a)
		}
		if a.X < block.X || a.Y < block.Y || a.X+a.W > block.X+block.W || a.Y+a.H > block.Y+block.H {
			t.Errorf("%s: control %d at %+v is outside its block %+v — a control that runs out of "+
				"its panel paints and hit-tests across the theme's stage", what, p.slot[i].id, a, block)
		}
		for j := i + 1; j < p.placed(); j++ {
			b := p.slot[j].r
			if v := intersectRect(a, b); v.W > 0 && v.H > 0 {
				t.Fatalf("%s: controls %d %+v and %d %+v OVERLAP by %+v — Ctx.Button ends in "+
					"`hover && c.clicked` with no click consumption, so one press fires both",
					what, p.slot[i].id, a, p.slot[j].id, b, v)
			}
		}
	}
}

// TestChromeRowNeverOverlapsAtAnySize is the totality claim, swept.
//
// Every width from "narrower than one control" to "roomier than the natural row" and
// every height from "no room for a line at all" to "room for three": for ALL of them
// the plan is either empty or non-overlapping. There is no size at which the planner
// is allowed to stack.
func TestChromeRowNeverOverlapsAtAnySize(t *testing.T) {
	want := chromeProbeCtls()
	for w := int32(0); w <= 420; w += 3 {
		for h := int32(0); h <= 140; h += 7 {
			block := sdl.Rect{X: 17, Y: 23, W: w, H: h}
			p := planChromeRow(block, want, musicHeaderMaxLines)
			assertNoOverlap(t, "block "+itoa(int(w))+"x"+itoa(int(h)), block, &p)
			// The row must never claim more height than the block, or the body under it
			// goes negative and the list draws off the panel.
			if p.h > h {
				t.Fatalf("%dx%d: the row claimed %d px of a %d px block", w, h, p.h, h)
			}
			if body := chromeRowBody(block, &p); body.H < 0 {
				t.Fatalf("%dx%d: the body under the row is %d px tall", w, h, body.H)
			}
		}
	}
}

// TestChromeRowDegradesInTheStatedOrder pins the four rungs as BEHAVIOUR, because
// "it fits somehow" is not the contract — WHICH control survives is.
func TestChromeRowDegradesInTheStatedOrder(t *testing.T) {
	want := chromeProbeCtls()
	tall := int32(chromeRowMinBodyPx + chromeRowLinePitch*3)

	// Roomy: everything is placed, at its natural width.
	roomy := sdl.Rect{X: 0, Y: 0, W: 600, H: tall}
	p := planChromeRow(roomy, want, musicHeaderMaxLines)
	if p.placed() != len(want) {
		t.Fatalf("a 600 px row placed %d of %d controls", p.placed(), len(want))
	}
	if r, _ := p.rect(6); r.W != 180 {
		t.Errorf("the readout was placed at %d px, want its natural 180 — a row that fits must not be segmented", r.W)
	}

	// Squeezed until something has to go: the ⋮ (id 5) NEVER goes, and the two menu
	// shortcuts (1, 2) go before the two controls with no other route (3, 4).
	for w := int32(200); w >= 40; w -= 10 {
		p := planChromeRow(sdl.Rect{X: 0, Y: 0, W: w, H: tall}, want, musicHeaderMaxLines)
		if _, ok := p.rect(5); !ok && p.placed() > 0 {
			t.Fatalf("width %d dropped the ⋮ overflow menu while keeping %d other controls — every row "+
				"it hosts is reachable only through it", w, p.placed())
		}
		_, keptOnly3 := p.rect(3)
		_, keptShortcut1 := p.rect(1)
		if keptShortcut1 && !keptOnly3 {
			t.Errorf("width %d kept a ⋮-menu shortcut (Stop) and dropped a control with no other route "+
				"(Expand all) — the drop order is a reachability argument, not a taste ranking", w)
		}
	}
}

// TestChromeRowPlanNeverAllocates is the frame-path gate. The Music header is planned
// on every courtroom frame that draws the panel, so a planner that allocated would be
// a per-frame allocation on the themed courtroom — the exact class the whole-screen
// alloc gates exist to catch.
func TestChromeRowPlanNeverAllocates(t *testing.T) {
	want := chromeProbeCtls()
	block := sdl.Rect{X: 0, Y: 0, W: 212, H: 240}
	if n := testing.AllocsPerRun(200, func() {
		p := planChromeRow(block, want, musicHeaderMaxLines)
		_, _ = p.rect(5)
	}); n != 0 {
		t.Errorf("planChromeRow allocated %.0f times per call, want 0 — the plan is a stack value by "+
			"construction (fixed arrays, no slices, no closures)", n)
	}
}

// TestSqueezedMusicPanelNeverStacksItsHeader is the REPORTED DEFECT, end to end.
//
// It drives the two real placers the themed music panel uses — fitThemedChips for the
// roster/Music/Areas chip strip and planMusicHeader for the action row — over the same
// squeezed shape an imported AO2 theme produces, and requires that nothing the panel
// draws lands on anything else it draws.
//
// The two are placed by SEPARATE calls in theme_layout.go, reconciled by
// themedStripBody, and that reconciliation is exactly what a short panel can falsify;
// so the gate asserts across BOTH, not inside either.
func TestSqueezedMusicPanelNeverStacksItsHeader(t *testing.T) {
	measure := func(s string) int32 { return int32(len(s)) * 7 } // a plausible chrome face

	// Widths from AO2's own stock default (224) down to a sliver, and heights from a
	// usable panel down to one with no room for a body — the shape a theme that
	// squeezed music_list actually produces.
	for _, w := range []int32{224, 216, 212, 160, 120, 96, 64, 40, 24} {
		for _, h := range []int32{300, 160, 90, 60, 44, 30} {
			panel := sdl.Rect{X: 40, Y: 60, W: w, H: h}
			body := insetThemedBody(panel)
			chips, n := fitThemedChips(body, themedListViews[:], themedListViews[:], logTabMusic)
			inner := themedStripBody(body, n)
			plan := planMusicHeader(inner, "Now playing: Pursuit ~ Cornered", "12 / 340", false, false, measure)

			var boxes []sdl.Rect
			var names []string
			for i := int32(0); i < n; i++ {
				boxes = append(boxes, chips[i].r)
				names = append(names, "chip "+chips[i].label)
			}
			for i := 0; i < plan.n; i++ {
				boxes = append(boxes, plan.items[i].r)
				names = append(names, "header item "+itoa(plan.items[i].id))
			}
			for i := range boxes {
				if boxes[i].W <= 0 || boxes[i].H <= 0 {
					t.Errorf("%dx%d: %s was placed with a degenerate rect %+v", w, h, names[i], boxes[i])
				}
				for j := i + 1; j < len(boxes); j++ {
					if v := intersectRect(boxes[i], boxes[j]); v.W > 0 && v.H > 0 {
						t.Fatalf("music panel %dx%d: %s %+v and %s %+v overlap by %+v — this is the "+
							"reported screenshot: one click on the overlap fires both controls",
							w, h, names[i], boxes[i], names[j], boxes[j], v)
					}
				}
				// And nothing may hang out of the panel the theme declared.
				if boxes[i].X+boxes[i].W > panel.X+panel.W {
					t.Errorf("music panel %dx%d: %s %+v runs %d px past the panel's right edge",
						w, h, names[i], boxes[i], boxes[i].X+boxes[i].W-(panel.X+panel.W))
				}
			}
		}
	}
}

// TestMusicHeaderStillPlacesEverythingAtTheStockWidth is the open–closed evidence: the
// reflow guard changed WHAT HAPPENS WHEN IT DOES NOT FIT, and nothing about the panel
// that does. A stock 216-px music_list still gets all five actions, the readout and
// the search row, on two lines.
func TestMusicHeaderStillPlacesEverythingAtTheStockWidth(t *testing.T) {
	measure := func(s string) int32 { return int32(len(s)) * 7 }
	body := insetThemedBody(sdl.Rect{X: 0, Y: 0, W: 216, H: 300})
	plan := planMusicHeader(body, musicNothingPlaying, "340 / 340", false, false, measure)
	for _, id := range []int{musicItemStop, musicItemRandom, musicItemExpand, musicItemCollapse,
		musicItemMenu, musicItemSearch, musicItemCount} {
		if _, ok := plan.rect(id); !ok {
			t.Errorf("a stock 216 px music_list dropped header item %d — the guard must only "+
				"change the squeezed case", id)
		}
	}
	// Two lines: the actions, then the search row. Three would be the header eating
	// the track list it is a header for.
	if plan.h > chromeRowLinePitch*musicHeaderMaxLines+chromeRowBodyGapPx {
		t.Errorf("the header took %d px, want at most %d (%d lines)", plan.h,
			chromeRowLinePitch*musicHeaderMaxLines+chromeRowBodyGapPx, musicHeaderMaxLines)
	}
	// The search field is on a LINE OF ITS OWN, below the icon row — the `brk` flag's
	// whole job, and the property that used to be a second cursor.
	stop, _ := plan.rect(musicItemStop)
	search, _ := plan.rect(musicItemSearch)
	if search.Y <= stop.Y {
		t.Errorf("the search field is at y=%d and Stop at y=%d — the search row must start a new line",
			search.Y, stop.Y)
	}
	if search.X != stop.X {
		t.Errorf("the search field starts at x=%d, want the row's left edge %d", search.X, stop.X)
	}
}
