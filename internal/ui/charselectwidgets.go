package ui

// THE CHAR-SELECT WIDGETS, AND THE NAME COLUMN THAT IS NOT DECORATION (#20).
//
// charselectlayout.go built the canvas and charselectgrid.go put AO2's grid on it.
// This file is the rest of AO2's char-select screen: the controls that surround the
// grid, each mapped onto the AsyncAO control that already does its job, at the rect
// the theme declares for it.
//
// WHY THE NAME COLUMN IS LOAD-BEARING. It looks like decoration — AsyncAO has a
// perfectly good grid, why draw the names twice? Because six themes in the 74-theme
// reference corpus (P5Theme, HDF-Standard, MOS 2xDark, MOSDefaultDark2.8,
// MOSDefaultLight2.8 and DR Theme) ship 714x668 charselect art whose PAINTED grid is
// ten slot columns starting at x=25, while their INI restricts char_buttons to seven
// columns from x=226. Their char_list rect covers exactly the three extra columns
// (25,36,190,596 spans x 25..215; the art's first three columns end at 218). AO2 fills
// that space with its QTreeWidget of character names. Skip it and every user of those
// six themes sees three orphan empty slot frames down the left of the screen — the
// same "the art and the client disagree" complaint issue #20 opened with, just moved
// 200 px left. AAI is the mirror case and the reason for the overlap guard below: it
// declares no char_list and gives the whole area to char_buttons = 25,36,663,596,
// using all ten painted columns itself.
//
// AO2 source for the semantics, all in AO2-Client src/charselect.cpp:
//   - char_taken is a FILTER CHECKBOX, default CHECKED (:99), and unchecking it hides
//     taken characters (filter_character_list, :360-364). It is not an indicator.
//   - char_passworded is a DEAD checkbox: its filter branch is commented out at
//     :355-358 with the note "It seems passwording characters is unimplemented yet?".
//   - char_list rows are DISABLED (not hidden) while they are taken (:381-384), and a
//     DOUBLE click on a row picks that character (:55, :168-183).
//   - char_select_left / char_select_right step whole PAGES (:116-117, courtroom.cpp
//     :6523-6533).
//   - back_to_lobby leaves the server entirely (courtroom.cpp:6517-6521).
//
// WHAT IS DELIBERATELY NOT DRAWN, and why (both recorded in the final report):
//   - char_password. AO2 sends its contents in a PW packet before CC (:203). AsyncAO
//     has no PW anywhere in internal/protocol, so a password box here would collect a
//     secret and silently drop it. A field that does nothing is worse than no field.
//   - char_passworded. Dead in AO2 itself. Shipping a checkbox that cannot filter
//     anything fails the "works out of the box, no tweaking" rule the batch is held
//     to: the first thing a user does with an unexplained checkbox is click it.
//     Omitted rather than drawn inert, and no semantics were invented for it.

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

const (
	// charListRowPadPx is the leading added to the client font's line height to get
	// one name row. Two pixels above and below the text: adjacent names are the only
	// thing separating one row's click target from the next, and this column has to
	// hold a several-thousand-character roster, so it buys separation without waste.
	charListRowPadPx int32 = 4
	// charListRowMinH is the row height used when there is no font to measure (a
	// headless stage). checkboxBoxPx is the smallest interactive box the kit ships,
	// so it is the honest floor for a clickable row.
	charListRowMinH = checkboxBoxPx
	// charListTextInsetPx is the left margin inside a name row. Qt gives a
	// QTreeWidget item the same kind of indent for free; without it the first glyph
	// sits flush against the theme's own panel edge.
	charListTextInsetPx int32 = 4
	// charListTabGapPx separates the two roster chips in the column header.
	charListTabGapPx int32 = 4
	// charListTabPadPx is the horizontal padding inside a roster chip, i.e. how much
	// wider than its label the chip is drawn. Matches the classic header's own
	// `c.TextWidth(label) + 20` chips so the two hosts of the same switch look alike.
	charListTabPadPx int32 = 10
)

// charListRows is the roster the name column shows this frame — and, just as
// importantly, the ONE filter both the column and the grid run.
//
// The two must agree exactly: the column's row order is the grid's cell order, so
// clicking a name has to be able to say "this is the n-th cell" without re-deriving
// anything. Two filters would desync the moment one of them learned about the taken
// checkbox and the other did not.
//
// A value type holding only slice headers: it is built per frame on both tabs and
// must never reach the heap.
type charListRows struct {
	// n is the row count. chars and names are alternatives, never both: chars is the
	// server roster (the Characters tab, which knows about taken slots), names is the
	// wardrobe's folder list (AsyncAO's own tab, which has no such concept).
	n     int
	chars []courtroom.CharacterSlot
	names []string
	// lower is the index-aligned lowercased name cache the search filter reads
	// (a.charLower / a.iniLower). Never re-lowered per frame.
	lower []string
}

// name is row i's display text.
func (s charListRows) name(i int) string {
	if s.chars != nil {
		return s.chars[i].Name
	}
	if i < len(s.names) {
		return s.names[i]
	}
	return ""
}

// taken reports whether row i's slot is already occupied. Always false off the
// server roster — a wardrobe folder cannot be "taken" by another player.
func (s charListRows) taken(i int) bool {
	return s.chars != nil && s.chars[i].Taken
}

// hidden is AO2's filter_character_list (AO2-Client charselect.cpp:347-372) in one
// place: the taken checkbox first, then the search text. Both the grid loop and the
// name column call it, which is what keeps a clicked name pointing at the right cell.
//
// showTaken is the char_taken checkbox's state, and it is passed rather than read off
// App on purpose: the checkbox only exists on the themed canvas, so the classic screen
// hands `true` and behaves exactly as it did before #20 instead of inheriting a filter
// it has no control for.
func (s charListRows) hidden(i int, query string, showTaken bool) bool {
	if !showTaken && s.taken(i) {
		return true
	}
	return query != "" && i < len(s.lower) && !strings.Contains(s.lower[i], query)
}

// matches counts the rows that survive the filter — the grid's content height and the
// column's. The unfiltered case skips the walk: it is an O(n) scan per frame on a
// screen that can be showing four thousand characters.
func (s charListRows) matches(query string, showTaken bool) int32 {
	if query == "" && showTaken {
		return int32(s.n)
	}
	out := int32(0)
	for i := 0; i < s.n; i++ {
		if !s.hidden(i, query, showTaken) {
			out++
		}
	}
	return out
}

// charListRowH is one name row's height for a given client font line height. A pure
// function so the column's capacity is testable without a renderer.
func charListRowH(fontH int32) int32 {
	if h := fontH + charListRowPadPx; h > charListRowMinH {
		return h
	}
	return charListRowMinH
}

// fontLineH is the client font's line height, or 0 when there is no font yet
// (headless staging). Every char-select control that carries a label sizes itself
// against this rather than a baked pixel count, so a DPI change moves them together.
func (a *App) fontLineH() int32 {
	if a.ctx == nil || a.ctx.font == nil {
		return 0
	}
	return int32(a.ctx.font.Height())
}

// charSelectWidgetsThemed reports whether the theme's canvas can host AsyncAO's
// char-select controls, i.e. whether this screen may drop its own header row.
//
// It is an ALL-OR-NOTHING test on purpose. Half a themed header — the theme's search
// box but AsyncAO's Spectate button floating over the art — is worse than either
// alternative, and it cannot be laid out sanely because the two coordinate systems
// have nothing to do with each other. So the screen either hands the whole control row
// to the theme or keeps its own, and the fallback is a screen we already ship.
//
// The requirements are deliberately only two:
//   - a themed grid. With the grid on its classic full-window plan there is no canvas
//     layout to be consistent with.
//   - char_search, spectator and back_to_lobby all resolved AND ON SCREEN. Those are
//     the three controls that make the screen finishable: filter, join without a
//     character, leave.
//
// "AND ON SCREEN" is not belt-and-braces, it is the second half of the test.
// Resolving only carries the SIZE floor: charSelectLayoutIn drops any rect that
// scaled below minThemedElementPx, which is what catches the 2592x1719 corpus theme
// at the 640x480 window floor (its 22 px design controls land on 5 px there). It says
// nothing about POSITION, and char-select rects are deliberately unclamped — correct
// for char_buttons, which overhangs its canvas by 30 px by design, but it means a
// rect can resolve at a perfectly good size somewhere the user cannot reach it. Under
// ThemeFitCrop at 1920x1080 the stock table scales by 2.69 and the canvas becomes
// {0,-336,1920,1796}: char_search lands at y=-318, back_to_lobby at y=-323 and
// spectator at y=1385 on a 1080 px window. All three "resolve", the themed row was
// therefore taken, the client header was suppressed, and the screen shipped with no
// search, no Spectate and no way out. The draw pushes lay.clip, so those controls
// were painted nowhere and hovering() rejected them.
//
// So each essential rect must keep at least minThemedElementPx of itself inside
// lay.clip — the same floor the size test uses, now applied to the part of the
// control that is actually visible and clickable. Crop and Custom then fall back to
// AsyncAO's own header, which is a screen we already ship.
//
// char_list is NOT required. It is the one AO2 widget a real theme legitimately does
// without — CCWidescreen declares it 0x0 and Mobile parks it at x=99999, both meaning
// "no name column" — and AAI-shaped themes hand the whole canvas to char_buttons, so
// an inherited default column would land under their grid. Those themes still get the
// theme's own control row; see charSelectNameListRect and charSelectTabStripRect.
func (a *App) charSelectWidgetsThemed(lay *charSelectLayoutCache, g charGridPlan) bool {
	if !lay.valid || !g.themed {
		return false
	}
	for _, key := range charSelectEssentialKeys {
		r, ok := lay.rect(key)
		if !ok {
			return false
		}
		if vis := intersectRect(r, lay.clip); vis.W < minThemedElementPx || vis.H < minThemedElementPx {
			return false
		}
	}
	return true
}

// charSelectEssentialKeys are the three controls the screen cannot be finished
// without. Named so the predicate above and its test cannot drift apart.
var charSelectEssentialKeys = [...]string{charSelectSearchKey, charSelectSpectatorKey, charSelectBackKey}

// charSelectShowTaken is AO2's char_taken FILTER for this frame — and, crucially, it
// is only ever the session's own value where the CHECKBOX that controls it is drawn.
//
// "A filter with no control is a trap" is the rule the classic screen already followed:
// it has no checkbox, so it passes true and behaves exactly as it did before #20. The
// themed screen needs the same test and did not have it. char_taken is deliberately NOT
// one of charSelectEssentialKeys — a theme without it must still get the themed control
// row — so the row can be taken on a frame where the box does not resolve at all: the
// theme hid the key (AO2's 11037 convention, which Mobile and CCWidescreen use on other
// char-select keys) or the canvas scaled it under minThemedElementPx. A session that
// cleared the box on one theme and then switched to such a theme would go on hiding
// every taken character with nothing on screen able to bring them back.
//
// The predicate reads the SAME rect drawCharSelectControlsIn draws the box at, so the
// filter and its control can never disagree about whether they exist.
func (a *App) charSelectShowTaken(themedUI bool, lay *charSelectLayoutCache) bool {
	if !themedUI {
		return true
	}
	if _, ok := lay.rect(charSelectTakenKey); !ok {
		return true
	}
	return a.charShowTaken
}

// charSelectNameListRect resolves where AO2's char_list column goes this frame, or
// reports false when this theme has no room for one.
//
// Three ways to have no room, each a real corpus case:
//   - the theme hides the key (Mobile parks it at x=99999) or declares it degenerate
//     (CCWidescreen ships char_list = 0x0). Both mean "no name column"; the layout
//     stage has already dropped the rect.
//   - the scaled column cannot hold its header plus one name, or is narrower than one
//     glyph of text beside its own scrollbar. A column that can only show a scrollbar
//     is not a column.
//   - it OVERLAPS char_buttons. AAI declares char_buttons = 25,36,663,596 and no
//     char_list, so the inherited default column (25,36,190,596) lands under its grid;
//     `alter ego (mobi)` declares one that overlaps outright. Stock AO2 draws both —
//     ui_char_list is a sibling created BEFORE ui_char_buttons, so the tree widget
//     shows through the 7 px gutters between buttons — and that is a stacking
//     artefact, not a design worth reproducing.
func (a *App) charSelectNameListRect(lay *charSelectLayoutCache) (sdl.Rect, bool) {
	r, ok := lay.rect(charSelectListKey)
	if !ok {
		return sdl.Rect{}, false
	}
	// Under Crop or a Custom pan the column can extend past the window; trim it to the
	// canvas region under the app-chrome band so neither its names nor its clicks land
	// in the menu bar. Trimming the RECT rather than pushing a clip keeps the row
	// arithmetic honest — a half-row at the top would still be clickable under a clip.
	r = intersectRect(r, lay.clip)
	rowH := charListRowH(a.fontLineH())
	// Header + at least one name, and at least one line-height of text beside the
	// scrollbar (a line height is about one wide glyph of the same font).
	if r.H < 2*rowH || r.W-scrollBarW-scrollBarGap-charListTextInsetPx < rowH {
		return sdl.Rect{}, false
	}
	if buttons, ok := lay.rect(charSelectButtonsKey); ok {
		if ov := intersectRect(r, buttons); ov.W > 0 && ov.H > 0 {
			return sdl.Rect{}, false
		}
	}
	return r, true
}

// charSelectTabStripRect is the fallback home for the Characters/Wardrobe switch: the
// row directly under char_search, sized to the chips' own labels and kept inside the
// canvas.
//
// It is only reached when there is no name column to host the switch. The Wardrobe is
// AsyncAO's own tab, so no theme rect describes it, and it has to stay reachable —
// under the search box is the one place every themed frame is guaranteed to have,
// since char_search is one of the three keys the themed row requires at all. On the
// handful of corpus themes that get here the strip lands over the first cell or two of
// a grid that already covers the whole canvas; that is the cost of those themes
// leaving no room, and it beats losing the tab.
func (a *App) charSelectTabStripRect(lay *charSelectLayoutCache) (sdl.Rect, bool) {
	search, ok := lay.rect(charSelectSearchKey)
	if !ok {
		return sdl.Rect{}, false
	}
	r := sdl.Rect{X: search.X, Y: search.Y + search.H, W: charSelectTabsW(a.ctx), H: charListRowH(a.fontLineH())}
	if r.W < 1 {
		r.W = search.W // no font to measure (headless): fall back to the search width
	}
	if right := lay.area.X + lay.area.W; r.X+r.W > right {
		r.X = right - r.W
	}
	if bottom := lay.area.Y + lay.area.H; r.Y+r.H > bottom {
		r.Y = bottom - r.H
	}
	r = intersectRect(r, lay.clip)
	// minThemedElementPx, not 1: the same floor the layout stage applies to every
	// themed rect. A 2 px sliver of tab strip surviving a Custom pan is not a
	// reachable control, and drawing one would leave the Wardrobe tab looking present
	// but un-clickable — worse than falling back to the client's own header row.
	if r.W < minThemedElementPx || r.H < minThemedElementPx {
		return sdl.Rect{}, false
	}
	return r, true
}

// charSelectTabsW is the natural width of the roster switch: both chips at their label
// widths plus the gap between them.
func charSelectTabsW(c *Ctx) int32 {
	out := int32(0)
	for i, tb := range charSelectTabs {
		if i > 0 {
			out += charListTabGapPx
		}
		out += c.TextWidth(tb.label) + 2*charListTabPadPx
	}
	return out
}

// drawCharSelectControls draws the mapped controls at their theme rects and reports
// whether one of them changed the screen — in which case drawCharSelect must return
// immediately rather than keep laying out a screen that is going away.
//
// Every control here is an EXISTING AsyncAO control moved to a theme rect, not a new
// one: the same search field id (so the query, the focus ring and the undo history all
// survive a theme change), the same Spectate action, the same requestDisconnect
// confirmation, the same Back-to-courtroom rule the classic header uses.
func (a *App) drawCharSelectControls(lay *charSelectLayoutCache, g charGridPlan) bool {
	c := a.ctx
	// The canvas region under the app-chrome band. Under Native, Letterbox and
	// Stretch this is the whole canvas and the clip is free; under Crop and Custom the
	// canvas legitimately runs off every window edge, and this is what keeps a control
	// from painting into — or being clicked inside — the menu bar and the server-tab
	// strip. It is never degenerate here: the caller's gate requires a themed grid,
	// which already requires a viewport at least one cell wide inside this rect.
	prev, had := c.pushClip(lay.clip)
	done := a.drawCharSelectControlsIn(lay, g)
	c.popClip(prev, had)
	return done
}

// drawCharSelectControlsIn is drawCharSelectControls' body, split out so the canvas
// clip is released on EVERY exit — several of these controls return early because they
// have just changed the screen.
func (a *App) drawCharSelectControlsIn(lay *charSelectLayoutCache, g charGridPlan) bool {
	c := a.ctx
	if r, ok := lay.rect(charSelectSearchKey); ok {
		a.charSearch, _ = c.TextField("charsearch", r, a.charSearch, "Search...")
	}
	// char_taken: AO2's filter checkbox, default CHECKED = show taken characters
	// (charselect.cpp:99). The kit's checkbox is a fixed 16 px box plus a label, so it
	// is anchored at the rect's left edge and vertically centred in it rather than
	// stretched — the theme sizes the SLOT, the kit sizes the control.
	if r, ok := lay.rect(charSelectTakenKey); ok {
		// Centred in the slot, but never ABOVE it: rect() only guarantees
		// minThemedElementPx (6), so a theme declaring a slot shorter than the kit's
		// 16 px tick box would otherwise push the box up out of its own rect and into
		// whatever the theme parked above it.
		y := r.Y
		if gap := (r.H - checkboxBoxPx) / 2; gap > 0 {
			y += gap
		}
		was := a.charShowTaken
		a.charShowTaken = c.Checkbox(r.X, y, "Taken", a.charShowTaken)
		if a.charShowTaken != was {
			// The filter changed under both views: a scroll offset measured against
			// the old content height would leave either view parked past its end.
			a.charScroll, a.charListScroll = 0, 0
		}
		c.Tooltip(sdl.Rect{X: r.X, Y: y, W: r.W, H: checkboxBoxPx},
			"Show characters other players are already using")
	}
	// Spectate needs a session to join without a character; this row draws before the
	// handshake completes (so "leave" stays reachable while a join hangs), so the
	// button is simply absent until there is something to spectate.
	if r, ok := lay.rect(charSelectSpectatorKey); ok && a.sess != nil {
		if c.Button(r, "Spectate") {
			if !a.sess.Rehearsal {
				a.sess.PickCharacter(protocol.UnpairedCharID)
			}
			a.enterCourtroom()
			return true
		}
	}
	// back_to_lobby is AO2's "leave the server" button, and AsyncAO's top-right
	// "leave this screen" slot already has exactly that meaning with one refinement
	// worth keeping: while a courtroom is live behind this screen (re-picking a
	// character) the safe action is Back, and Disconnect is deliberately kept out of
	// the slot muscle memory aims at. Disconnect stays one click away in the menu bar
	// either way, so nothing is lost when Back occupies the rect.
	if r, ok := lay.rect(charSelectBackKey); ok {
		if a.room != nil {
			if c.Button(r, "Back") {
				a.screen = ScreenCourtroom
				return true
			}
		} else if c.ButtonCol(r, "Disconnect", ColPanel, ColPanelHi, ColDanger, ColDanger) {
			a.requestDisconnect() // confirm first unless instant-disconnect is set
			return true
		}
	}
	a.drawCharSelectPageArrows(lay, g)
	return false
}

// drawCharSelectPageArrows wires char_select_left / char_select_right.
//
// AO2 PAGES its grid and these are the page buttons. AsyncAO scrolls, and the scroll
// is already quantised to whole rows (charGridScroll), so a page maps cleanly: step by
// as many whole rows as the viewport holds. That gives the arrows AO2's feel — a press
// replaces the visible page — while leaving the scrollbar, the wheel and the search
// working, which paging would have taken away.
func (a *App) drawCharSelectPageArrows(lay *charSelectLayoutCache, g charGridPlan) {
	c := a.ctx
	scroll := a.gridScroll()
	if scroll == nil {
		return
	}
	// rowOffset, not rows*pitchY: on the themed lattice a page has to be a whole
	// number of ARTWORK rows or the next frame's snap silently lands somewhere else.
	rows := int32(1)
	if fit := g.view.H / g.pitchY; fit > 1 {
		rows = fit
	}
	page := g.rowOffset(rows)
	if r, ok := lay.rect(charSelectLeftKey); ok {
		if c.Button(r, "<") {
			*scroll -= page
		}
		c.Tooltip(r, "Previous page")
	}
	if r, ok := lay.rect(charSelectRightKey); ok {
		if c.Button(r, ">") {
			*scroll += page
		}
		c.Tooltip(r, "Next page")
	}
}

// gridScroll is the active tab's grid scroll offset. Both tabs share one plan and one
// set of page arrows, so the arrows need to know which offset they are stepping.
func (a *App) gridScroll() *int32 {
	if a.charTab == charTabWardrobe {
		return &a.iniScroll
	}
	return &a.charScroll
}

// drawCharNameList is AO2's char_list: a scrolling column of character names beside
// the grid, filtered by the same predicate, selecting into the same cells.
//
// ONE SELECTION, TWO VIEWS. Clicking a name selects it, outlines its cell in the grid
// and scrolls the grid to it; a double click picks it, which is exactly AO2's
// itemDoubleClicked wiring. AO2 gets the sync for free because both views are Qt
// widgets over one model — immediate mode has to be told, and being told once
// (charListSel) is why the column is drawn BEFORE the grid: no ordering hazard, no
// frame of lag, and the two never disagree about which character is chosen.
//
// Categories are deliberately absent — see docs/ROADMAP.md. AO2 groups this list by
// each character's char.ini [Options] category, which a streaming client can only
// learn with one fetch per character.
func (a *App) drawCharNameList(lay *charSelectLayoutCache, rows charListRows, query string, showTaken bool, g charGridPlan) {
	c := a.ctx
	r, ok := a.charSelectNameListRect(lay)
	if !ok {
		// No column on this theme — but the roster switch still has to exist, so it
		// falls back to its own strip under the search box.
		if strip, ok := a.charSelectTabStripRect(lay); ok {
			a.drawCharListTabs(strip)
		}
		return
	}
	rowH := charListRowH(a.fontLineH())
	a.drawCharListTabs(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: rowH})
	body := sdl.Rect{X: r.X, Y: r.Y + rowH, W: r.W, H: r.H - rowH}
	matches := rows.matches(query, showTaken)
	contentH := matches * rowH
	scroll := a.charListScroll
	scroll -= c.WheelIn(body) * scrollStepPx
	track := sdl.Rect{X: body.X + body.W - scrollBarW, Y: body.Y, W: scrollBarW, H: body.H}
	scroll = c.VScrollbar("charlist", track, scroll, contentH, body.H)
	a.charListScroll = scroll
	textW := body.W - scrollBarW - scrollBarGap - charListTextInsetPx
	if textW < 1 {
		return
	}
	// pushClip, not SetClipRect: it fences INPUT too, so a row scrolled under the
	// column header is neither drawn nor clickable up there (clipped-grids rule).
	prev, had := c.pushClip(body)
	n := int32(0)
	for i := 0; i < rows.n; i++ {
		if rows.hidden(i, query, showTaken) {
			continue
		}
		y := body.Y + n*rowH - scroll
		ord := n
		n++
		if y+rowH <= body.Y || y >= body.Y+body.H {
			continue // culled: a 4000-name column draws one screenful, not 4000 rows
		}
		row := sdl.Rect{X: body.X, Y: y, W: body.W - scrollBarW, H: rowH}
		switch {
		case i == a.charListSel:
			c.Fill(row, ColAccent) // the same character the grid outlines
		case c.hovering(row):
			c.Fill(row, ColPanelHi)
		}
		// AO2 DISABLES a taken row rather than hiding it while the taken filter is on
		// (charselect.cpp:381-384), so the name stays readable and simply refuses to
		// be picked.
		ink := ColText
		if rows.taken(i) {
			ink = ColTextDim
		}
		c.LabelClipped(row.X+charListTextInsetPx, y+(rowH-a.fontLineH())/2, textW, rows.name(i), ink)
		if !c.hovering(row) {
			continue
		}
		if c.dblClick && rows.chars != nil && !rows.taken(i) {
			// AO2's itemDoubleClicked → char_clicked (charselect.cpp:55, :183).
			c.dblClick = false // consumed: the grid must not also read it
			c.popClip(prev, had)
			a.pickCharacter(i)
			return
		}
		if c.clicked {
			a.charListSel = i
			*a.gridScroll() = revealRowInGrid(g, ord, *a.gridScroll())
		}
	}
	c.popClip(prev, had)
}

// drawCharListTabs is the Characters / Wardrobe switch, drawn as the name column's
// header. The Wardrobe is AsyncAO's own addition with no AO2 counterpart, so it needs
// a home on the canvas that no theme rect describes; the column header is that home,
// because the column is the thing whose CONTENTS the switch changes.
//
// WHICH CHIP IS ACTIVE HAS TO BE VISIBLE. The first version filled the active chip
// with ColAccent and then drew a Button on the IDENTICAL rect — and Button fills
// again, with an opaque ColPanel/ColPanelHi, so the accent was completely overpainted.
// Both chips also take Button's ColAccent border, so Characters and Wardrobe were
// pixel-identical whichever one you were on. The classic header gets away with the
// fill-then-button pattern only because it fills a LARGER rect, leaving a ring; here
// the chips sit in a tight row inside a theme-declared column, so the active one is
// drawn as an accent-BODIED button instead — the same segmented-control idiom the
// content panel's source switch uses (ButtonCol with bg == hoverBg == accent, so it
// reads selected rather than hoverable).
//
// The width fallback matters too: charSelectNameListRect admits columns narrow enough
// that two natural-width chips do not fit, and clipping the second one to whatever was
// left could leave it a pixel wide — an unreachable Wardrobe tab. When the natural
// widths do not fit, the row splits the space evenly instead, so both chips are always
// clickable.
func (a *App) drawCharListTabs(r sdl.Rect) {
	c := a.ctx
	n := int32(len(charSelectTabs))
	even := int32(0)
	if natural := charSelectTabsW(c); natural > r.W {
		even = (r.W - (n-1)*charListTabGapPx) / n
		if even < 1 {
			return // no room for a usable switch at all
		}
	}
	x := r.X
	for _, tb := range charSelectTabs {
		w := even
		if w == 0 {
			w = c.TextWidth(tb.label) + 2*charListTabPadPx
		}
		chip := sdl.Rect{X: x, Y: r.Y, W: w, H: r.H}
		clicked := false
		if a.charTab == tb.id {
			clicked = c.ButtonCol(chip, tb.label, ColAccent, ColAccent, ColAccent, ColBackground)
		} else {
			clicked = c.Button(chip, tb.label)
		}
		if clicked {
			a.selectCharTab(tb.id)
		}
		x += w + charListTabGapPx
	}
}

// charSelectTabs is the roster switch's row, shared by the classic header and the
// themed column header so the two can never offer different tabs.
var charSelectTabs = [...]struct {
	id    int
	label string
}{{charTabServer, "Characters"}, {charTabWardrobe, "Wardrobe"}}

// selectCharTab switches roster. The name column and the grid both index into the tab's
// own slice, so a selection carried across would point at a different character (or
// past the end of a shorter list) — it is dropped, along with the column's scroll.
func (a *App) selectCharTab(id int) {
	if a.charTab == id {
		return
	}
	a.charTab = id
	a.charListSel, a.charListScroll = -1, 0
	if id == charTabWardrobe {
		a.ensureIniList()
	}
}

// revealRowInGrid returns the grid scroll offset that brings the ord-th filtered cell
// into view, moving as little as possible: already-visible cells do not move the grid
// at all, which is what keeps clicking down a column of names from jumping the grid on
// every click. charGridScroll clamps and row-snaps the result on the next frame.
func revealRowInGrid(g charGridPlan, ord, scroll int32) int32 {
	if g.cols < 1 || g.pitchY < 1 {
		return scroll
	}
	// rowOffset, not row*pitchY: on the themed lattice the two differ, and a reveal
	// that landed between rows would be snapped somewhere else on the next frame.
	top := g.rowOffset(ord / g.cols)
	if top < scroll {
		return top
	}
	if bottom := top + g.cell - g.view.H; bottom > scroll {
		return bottom
	}
	return scroll
}
