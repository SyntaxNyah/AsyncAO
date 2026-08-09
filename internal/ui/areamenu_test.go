package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
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

// TestAreaMenuIsWiredIntoTheSharedPane is the encapsulation gate for the kebab.
// The rows only appear because musicheader.go appends them and forwards every
// predicate; drop any one of those and the block is either invisible or
// mis-dispatched into the music switch, which would silently do the wrong thing.
func TestAreaMenuIsWiredIntoTheSharedPane(t *testing.T) {
	seen := map[musicMenuKind]bool{}
	for _, row := range musicMenuRows {
		seen[row.kind] = true
	}
	for _, row := range areaMenuRows {
		if row.kind != musicMenuSeparator && !seen[row.kind] {
			t.Errorf("area row %q never reaches the shared menu — musicMenuRows does not carry it", row.label)
		}
	}
	// musicMenuRowLive is in the list because it is the forward the first cut of this
	// kit shipped WITHOUT, which put all five area rows in every user's music menu.
	for _, fn := range []string{"musicMenuRowLive", "musicMenuRowEnabled", "musicMenuRowChecked", "musicMenuRowIsToggle", "musicMenuAct"} {
		if !containsCall(funcBodySource(t, "musicheader.go", fn), "isAreaMenuKind") {
			t.Errorf("%s does not forward the AREA block — an area row falls into the music switch", fn)
		}
	}
	if !containsCall(funcBodySource(t, "screens.go", "drawAreaList"), "drawAreaMenuButton") {
		t.Fatal("the area list draws no ⋮ — the rows exist but nothing opens them from the Areas view")
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

// TestAreaListHeaderIsLaidOutBySplitAreaHeaderRow is the deletion-catcher that makes
// the test above mean something for the SCREEN. splitAreaHeaderRow can be perfectly
// correct and perfectly ignored: drawAreaList draws three things on that row, and any
// one of them can be rebuilt out of the panel rect in place, which is exactly the
// regression that reached this file — the counter drawn at r.X+r.W-142, straight under
// the ⋮, with the whole package green.
//
// A behavioural test cannot see it. The overlap is between a label and a button drawn
// by two unrelated calls in a single-pass drawer with no return value, and the pane it
// draws into needs a live renderer; what is wrong is the SOURCE of a coordinate. So the
// gate reads the coordinates: it learns what drawAreaList calls the split's result and
// then requires the ⋮'s rect, the search field's rect and the counter's X and Y all to
// derive from that name.
func TestAreaListHeaderIsLaidOutBySplitAreaHeaderRow(t *testing.T) {
	body := funcBodySource(t, "screens.go", "drawAreaList")
	head := bindingOf(body, "splitAreaHeaderRow")
	if head == "" {
		t.Fatal("drawAreaList does not bind splitAreaHeaderRow's result — the header row's parts are " +
			"being placed by hand again, and nothing keeps them out from under the ⋮")
	}

	btn := callsNamed(body, "drawAreaMenuButton")
	if len(btn) != 1 {
		t.Fatalf("drawAreaList draws the ⋮ %d times, want exactly 1", len(btn))
	}
	if len(btn[0].Args) == 0 || !mentionsIdent(btn[0].Args[0], head) {
		t.Errorf("the ⋮ is placed from something other than %s — the button and the row's other "+
			"occupants no longer agree on where its left edge is", head)
	}

	field := callsNamed(body, "TextField")
	if len(field) != 1 {
		t.Fatalf("drawAreaList draws %d text fields, want exactly 1 (the area search box)", len(field))
	}
	// TextField(id, rect, value, placeholder) — the rect is the one that can drift.
	if len(field[0].Args) < 2 || !mentionsIdent(field[0].Args[1], head) {
		t.Errorf("the search field's rect is not derived from %s — it can reach under the ⋮ again", head)
	}

	// The counter is the Label whose text comes from areaCount.text(shown, total). There
	// are two of them, one per branch of the search-field-is-external switch; the
	// external branch left-aligns a lone "3 / 3" on the card column and owes the split
	// nothing, so the requirement is that the OTHER one — the one sharing the row with
	// the ⋮ — is laid out from the split.
	counters, fromSplit := 0, 0
	for _, call := range callsNamed(body, "Label") {
		if len(call.Args) < 3 || !containsCall(call.Args[2], "text") {
			continue
		}
		counters++
		if mentionsIdent(call.Args[0], head) && mentionsIdent(call.Args[1], head) {
			fromSplit++
		}
	}
	if counters == 0 {
		t.Fatal("drawAreaList draws no \"shown / total\" counter — this gate now checks nothing")
	}
	if fromSplit == 0 {
		t.Errorf("none of the %d counter labels takes both coordinates from %s — the one that shares "+
			"the header row with the ⋮ is being placed against the full panel width, which draws it "+
			"underneath the button", counters, head)
	}
}

// TestMusicViewMenuCarriesNoAreaRow is the gate the shared pane demands back in
// exchange for hosting the block. Three triggers open ONE popup — the Music
// header's ⋮, the roster toolbar's ⋮ and now the Areas view's — so a block that
// forgets to gate itself is not a dead row, it is five rows appearing in a menu
// about a list the user is not looking at. That is exactly how the first cut of
// this kit shipped.
//
// It drives musicMenuRowDrawn (the pane's own row filter) rather than re-deciding
// liveness, and asserts BOTH directions: nothing of the block on the Music view,
// all of it — divider included — on the Areas view. The divider matters on its own,
// because it carries the shared musicMenuSeparator kind and so is invisible to
// every per-kind predicate: gate the four rows and forget the fifth and the music
// menu ends in a rule under its last row with nothing beneath it.
func TestMusicViewMenuCarriesNoAreaRow(t *testing.T) {
	const themed = false // the classic panel; the volume row's answer is not what's under test

	drawn := func(a *App) (areaRows int, height int32) {
		for i, row := range musicMenuRows {
			if !a.musicMenuRowDrawn(i, themed) {
				continue
			}
			if isAreaMenuKind(row.kind) {
				areaRows++
			}
		}
		return areaRows, a.musicMenuHeight(themed)
	}

	// logTab lives on the EMBEDDED sessionState (app.go), so it cannot be set by name
	// in an App literal — onView builds the whole view state one way for all five
	// cases below.
	onView := func(tab int) *App {
		a := &App{}
		a.logTab = tab
		return a
	}

	music := onView(logTabMusic)
	if n, _ := drawn(music); n != 0 {
		t.Errorf("the Music view's ⋮ offers %d area row(s) — they act on a list it is not showing", n)
	}
	if n, _ := drawn(onView(logTabPlayers)); n != 0 {
		t.Errorf("the roster toolbar's ⋮ offers %d area row(s)", n)
	}

	areas := onView(logTabAreas)
	wantRows := 0
	for _, row := range areaMenuRows {
		if isAreaMenuKind(row.kind) {
			wantRows++
		}
	}
	gotRows, areasH := drawn(areas)
	if gotRows != wantRows {
		t.Fatalf("the Areas view's own ⋮ offers %d of its %d rows — the gate is too tight to be useful", gotRows, wantRows)
	}
	// The block's divider must go dark WITH the block: the height difference is the
	// four rows AND one separator, never the rows alone.
	_, musicH := drawn(music)
	if want := int32(wantRows)*musicMenuItemH + musicMenuSepH; areasH-musicH != want {
		t.Errorf("the AREA block costs %d px on the Areas view and 0 on the Music view, want %d px "+
			"(%d rows + its divider) — a divider left behind draws a rule under the last roster row",
			areasH-musicH, want, wantRows)
	}

	// A TORN Areas panel draws whatever logTab says and carries its own ⋮
	// (torntabs.go), so the rows have to be live there too or that button is inert.
	torn := onView(logTabMusic)
	torn.classicOv = map[string][4]float64{tornKeyFor(logTabAreas): {}}
	if n, _ := drawn(torn); n != wantRows {
		t.Errorf("a torn-off Areas panel offers %d of its %d rows — its ⋮ opens a menu with nothing in it", n, wantRows)
	}
	// …and a fully hidden Areas tab draws in none of the three places, torn or not.
	hidden := onView(logTabAreas)
	hidden.hidden = map[string]bool{panelTabAreas: true}
	if n, _ := drawn(hidden); n != 0 {
		t.Errorf("a hidden Areas tab still offers %d area row(s)", n)
	}
}

// TestEveryMusicMenuConsumerAsksMusicMenuRowDrawn is the seam gate for the row
// filter itself. musicMenuRowDrawn exists because three passes over musicMenuRows
// must agree on which rows paint — the height, the width and the renderer — and the
// only reason they agree is that all three ask IT rather than the per-kind
// musicMenuRowLive underneath.
//
// The behavioural arm above cannot catch a consumer that stops asking: it drives
// musicMenuRowDrawn and musicMenuHeight, so reverting drawMusicMenu's loop to the
// bare musicMenuRowLive leaves the suite green while the Music view paints the AREA
// block's divider — that separator carries the shared musicMenuSeparator kind, so
// musicMenuRowLive says yes — as a stray rule at the panel's bottom edge, past the
// height musicMenuRect reserved. Hence a deletion-catcher on the call itself, one
// arm per consumer, which is also what keeps the next block appended to this pane
// from re-learning the same lesson.
func TestEveryMusicMenuConsumerAsksMusicMenuRowDrawn(t *testing.T) {
	for _, fn := range []string{"musicMenuHeight", "musicMenuRect", "drawMusicMenu"} {
		if !containsCall(funcBodySource(t, "musicheader.go", fn), "musicMenuRowDrawn") {
			t.Errorf("%s filters musicMenuRows without musicMenuRowDrawn — the three passes can now disagree, "+
				"and a block's trailing divider draws with nothing beneath it", fn)
		}
	}
}
