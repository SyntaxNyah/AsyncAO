package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// TestAreaMenuRowsAreAllReachableActions pins the area kebab's model (areamenu.go):
// every row maps to something that already exists, the one toggle is the one that
// keeps the menu open, and the two server rows grey out with no session rather than
// vanishing.
func TestAreaMenuRowsAreAllReachableActions(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	for _, row := range areaMenuRows {
		if row.kind == musicMenuSeparator {
			continue
		}
		if !isAreaMenuKind(row.kind) {
			t.Errorf("row %q is not in the AREA kind range — musicheader.go would dispatch it to the music block", row.label)
		}
		if row.label == "" {
			t.Errorf("an area row has no label; Ctx.Tooltip no-ops under the menu's modal fence, so the label IS the explanation")
		}
	}
	if a.areaMenuRowEnabled(areaMenuBackground) || a.areaMenuRowEnabled(areaMenuRefresh) {
		t.Error("the two server rows must grey out with no session — both send an OOC command")
	}
	if !areaMenuRowIsToggle(areaMenuPerAreaLog) {
		t.Error("the per-area-log row is a switch: the menu must stay open for it")
	}
	for _, k := range []musicMenuKind{areaMenuBackground, areaMenuRefresh, areaMenuCMPanel} {
		if areaMenuRowIsToggle(k) {
			t.Errorf("row %d DOES something — the menu must close behind it", k)
		}
	}
	// The CM row is a ROUTE, and the panel it routes to returns on its first line
	// without CM (cmpanel.go) — nothing draws and nothing is said. So the row must grey
	// out on the same gate the menu bar's copy of this action uses, or the click is
	// dead: drawMusicMenu dispatches only rows it drew enabled, which is why this has to
	// be asserted here and not inferred from areaMenuAct working.
	if a.areaMenuRowEnabled(areaMenuCMPanel) {
		t.Error("the CM row is offered without CM — the panel it opens self-closes, so the click does nothing and explains nothing")
	}
	a.amICMNow = true
	if !a.areaMenuRowEnabled(areaMenuCMPanel) {
		t.Error("the CM row stays grey while we DO hold CM — greyed rather than hidden means it must come back when the server grants it")
	}
	// The CM route is not a duplicate control: it opens the panel that already owns
	// lock / unlock / area-kick.
	a.areaMenuAct(areaMenuCMPanel)
	if !a.showCMPanel {
		t.Error("the CM row must open the CM panel rather than growing its own lock/kick copies")
	}
}

// TestAreaMenuButtonTakesRoomFromItsOwnRow pins the layout rule the roster toolbar
// was rebuilt to enforce: a control added to a header row must SHRINK its neighbours,
// not draw over them — a theme's music_list panel is 212 px wide and has no slack.
//
// It DRIVES splitAreaHeaderRow, which is the row's entire arithmetic, and asserts
// only RELATIONSHIPS between the rects that come back. The first cut of this gate
// recomputed the split from the same constants the drawer used and compared the answer
// with itself: a tautology that could not fail, and did not, when the count label was
// pointed back at the full panel width and drew under the ⋮.
func TestAreaMenuButtonTakesRoomFromItsOwnRow(t *testing.T) {
	// 212 px is the theme music_list with no slack; the others are a classic docked tab
	// and a torn-off panel dragged wider.
	for _, w := range []int32{212, 260, 420} {
		// A non-zero origin on purpose: every part must be placed relative to the ROW it
		// was handed, never to the window.
		row := sdl.Rect{X: 37, Y: 11, W: w, H: fieldH}
		got := splitAreaHeaderRow(row)

		if got.button.W <= 0 || got.button.X+got.button.W != row.X+row.W {
			t.Errorf("w=%d: the ⋮ (%d..%d) is not flush with the row's right edge %d",
				w, got.button.X, got.button.X+got.button.W, row.X+row.W)
		}
		if got.button.Y != row.Y || got.button.H != row.H {
			t.Errorf("w=%d: the ⋮ does not fill the row's height (%d+%d vs %d+%d)",
				w, got.button.Y, got.button.H, row.Y, row.H)
		}
		// The two things that used to be laid out against the full panel width. Strictly
		// LESS than the button's left edge, not merely non-overlapping: the row is
		// supposed to leave visible clearance, and touching edges read as a collision.
		if right := got.count.X + got.count.W; right >= got.button.X {
			t.Errorf("w=%d: the counter (right edge %d) reaches under the ⋮ (left edge %d)",
				w, right, got.button.X)
		}
		if right := got.field.X + got.field.W; right >= got.count.X {
			t.Errorf("w=%d: the search field (right edge %d) runs into the counter (left edge %d)",
				w, right, got.count.X)
		}
		// …and nothing escapes the row it was split out of.
		if got.field.X < row.X || got.field.Y != row.Y || got.field.H != row.H {
			t.Errorf("w=%d: the search field %+v is not inside its row %+v", w, got.field, row)
		}
		if got.field.W <= 0 {
			t.Errorf("w=%d: the search field has no width left (%d) — the header cannot be typed in",
				w, got.field.W)
		}
		if got.count.Y < row.Y || got.count.Y+got.count.H > row.Y+row.H {
			t.Errorf("w=%d: the counter's line %d..%d escapes the row %d..%d",
				w, got.count.Y, got.count.Y+got.count.H, row.Y, row.H+row.Y)
		}
	}

	// The ⋮ takes a FIXED bite and the field absorbs every pixel of a width change —
	// which is what "shrink its neighbours" means, stated as a relationship rather than
	// as a second copy of the numbers.
	const widen = int32(40)
	narrow := splitAreaHeaderRow(sdl.Rect{X: 0, Y: 0, W: 212, H: fieldH})
	wide := splitAreaHeaderRow(sdl.Rect{X: 0, Y: 0, W: 212 + widen, H: fieldH})
	if narrow.button.W != wide.button.W {
		t.Errorf("the ⋮ scaled with the panel (%d → %d) — it is a fixed square control",
			narrow.button.W, wide.button.W)
	}
	if got := wide.field.W - narrow.field.W; got != widen {
		t.Errorf("widening the row by %d px gave the search field %d px — the field is what absorbs it",
			widen, got)
	}
	if narrow.count.W != wide.count.W {
		t.Errorf("the counter's room scaled with the panel (%d → %d) — it holds one short \"n / m\"",
			narrow.count.W, wide.count.W)
	}
}

// TestEachListMenuHoldsOnlyItsOwnRows: the Music, Areas and player-list views open one shared popup, and each sees only
// its own block (#85), with no divider at either end.
func TestEachListMenuHoldsOnlyItsOwnRows(t *testing.T) {
	const themed = false // the classic panel; the volume row's answer is not what's under test

	blockOf := func(k musicMenuKind) musicMenuSource {
		switch {
		case isAreaMenuKind(k):
			return musicMenuFromAreas
		case isRosterMenuKind(k):
			return musicMenuFromRoster
		}
		return musicMenuFromMusic
	}
	for _, tc := range []struct {
		name string
		tab  int
		from musicMenuSource
		torn bool
	}{
		{"music", logTabMusic, musicMenuFromMusic, false},
		{"areas", logTabAreas, musicMenuFromAreas, false},
		{"roster", logTabPlayers, musicMenuFromRoster, false},
		// A torn Areas panel draws whatever logTab says and carries its own ⋮ (torntabs.go).
		{"torn areas", logTabMusic, musicMenuFromAreas, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{}
			a.logTab = tc.tab
			if tc.torn {
				a.classicOv = map[string][4]float64{tornKeyFor(logTabAreas): {}}
			}
			a.musicMenuFrom = tc.from
			first, last := -1, -1
			for i, row := range musicMenuRows {
				drawn := a.musicMenuRowDrawn(i, themed)
				if drawn {
					if first < 0 {
						first = i
					}
					last = i
				}
				if row.kind == musicMenuSeparator {
					continue
				}
				if own := blockOf(row.kind) == tc.from; drawn != own {
					t.Errorf("%q drawn = %v, want %v", row.label, drawn, own)
				}
			}
			if first < 0 {
				t.Fatal("the menu is empty")
			}
			if musicMenuRows[first].kind == musicMenuSeparator || musicMenuRows[last].kind == musicMenuSeparator {
				t.Error("the menu starts or ends with a divider")
			}
		})
	}

	// A fully hidden Areas tab draws in none of the three places, torn or not.
	hidden := &App{}
	hidden.logTab = logTabAreas
	hidden.musicMenuFrom = musicMenuFromAreas
	hidden.hidden = map[string]bool{panelTabAreas: true}
	for i := range musicMenuRows {
		if hidden.musicMenuRowDrawn(i, themed) {
			t.Errorf("a hidden Areas tab still draws %q", musicMenuRows[i].label)
		}
	}
}
