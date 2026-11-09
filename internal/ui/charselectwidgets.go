package ui

// Char-select widgets: the controls around the grid, mapped onto AsyncAO controls at the theme's rects.
//
// The name column is needed because some themes paint a ten-column grid while their INI restricts char_buttons
// to seven; char_list covers the other three, and omitting it leaves empty slot frames in the art.
// AO2-Client charselect.cpp:
//   - char_taken is a filter checkbox, default checked (:99); unchecking hides taken characters (:360-364).
//   - char_passworded is dead upstream (:355-358), so it is not drawn.
//   - char_list rows are disabled while taken (:381-384); a double click picks (:55, :168-183).
//   - char_select_left/right step whole pages (:116-117, courtroom.cpp:6523-6533).
//   - back_to_lobby leaves the server (courtroom.cpp:6517-6521).
// char_password is not drawn: the client has no PW packet, so the box would drop a secret.

import (
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// charListRowPadPx is the leading added to the font's line height to get one name row.
	charListRowPadPx int32 = 4
	// charListRowMinH is the row height when there is no font to measure; checkboxBoxPx is the floor.
	charListRowMinH = checkboxBoxPx
	// charListTextInsetPx is the left margin inside a name row.
	charListTextInsetPx int32 = 4
)

// charListRows is the roster the name column shows this frame, and the one filter the column and grid share.
//
// The column's row order is the grid's cell order, so a clicked name maps to its cell. Built per frame; it holds
// only slice headers and must not reach the heap.
type charListRows struct {
	chars []courtroom.CharacterSlot
	// lower is the index-aligned lowercased name cache the search filter reads.
	lower []string
}

// name is row i's display text.
func (s charListRows) name(i int) string { return s.chars[i].Name }

// taken reports whether row i's slot is occupied.
func (s charListRows) taken(i int) bool { return s.chars[i].Taken }

// hidden is AO2's filter_character_list (charselect.cpp:347-372): the taken checkbox, then the search text.
//
// showTaken is passed, not read off App: the classic screen has no checkbox and passes true.
func (s charListRows) hidden(i int, query string, showTaken bool) bool {
	if !showTaken && s.taken(i) {
		return true
	}
	return query != "" && i < len(s.lower) && !strings.Contains(s.lower[i], query)
}

// matches counts the rows that survive the filter; the unfiltered case skips the O(n) walk.
func (s charListRows) matches(query string, showTaken bool) int32 {
	if query == "" && showTaken {
		return int32(len(s.chars))
	}
	out := int32(0)
	for i := range s.chars {
		if !s.hidden(i, query, showTaken) {
			out++
		}
	}
	return out
}

// charListRowH is one name row's height for a font line height.
func charListRowH(fontH int32) int32 {
	if h := fontH + charListRowPadPx; h > charListRowMinH {
		return h
	}
	return charListRowMinH
}

// fontLineH is the client font's line height, or 0 when there is no font yet.
func (a *App) fontLineH() int32 {
	if a.ctx == nil || a.ctx.font == nil {
		return 0
	}
	return int32(a.ctx.font.Height())
}

// charSelectWidgetsThemed reports whether the theme's canvas can host the char-select controls, so this screen
// may drop its own header row. Half a themed header is worse than either, so it is all or nothing.
//
// Requires a themed grid, and char_search, spectator and back_to_lobby all resolved and on screen. Resolving only
// checks the size floor (charSelectLayoutIn drops rects under minThemedElementPx); char-select rects are not
// clamped, so under ThemeFitCrop a rect can resolve at a good size off screen. Each essential rect must keep
// minThemedElementPx of itself inside lay.clip.
//
// char_list is not required: CCWidescreen declares it 0x0 and Mobile parks it at x=99999, both meaning no
// column, and AAI-style themes give the whole canvas to char_buttons.
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

// charSelectEssentialKeys are the controls the screen cannot be finished without.
var charSelectEssentialKeys = [...]string{charSelectSearchKey, charSelectSpectatorKey, charSelectBackKey}

// charSelectShowTaken is AO2's char_taken filter for this frame, honoured only where its checkbox is drawn.
//
// char_taken is not an essential key, so the themed row can be taken on a frame where the box does not resolve
// (theme hid the key via AO2's 11037 convention, or it scaled under minThemedElementPx); the filter must not
// keep hiding taken characters with no control to undo it. It reads the same rect the draw uses.
func (a *App) charSelectShowTaken(themedUI bool, lay *charSelectLayoutCache) bool {
	if !themedUI {
		return true
	}
	if _, ok := lay.rect(charSelectTakenKey); !ok {
		return true
	}
	return a.charShowTaken
}

// charSelectNameListRect resolves where AO2's char_list column goes this frame, or false when there is no room.
//
// No room when: the theme hides or zeroes the key; the column cannot hold one name beside its scrollbar; or it overlaps char_buttons (AAI, alter ego (mobi)). Stock AO2 draws both overlapped, which is a
// stacking artefact not worth reproducing.
func (a *App) charSelectNameListRect(lay *charSelectLayoutCache) (sdl.Rect, bool) {
	r, ok := lay.rect(charSelectListKey)
	if !ok {
		return sdl.Rect{}, false
	}
	// Under Crop or a Custom pan the column can extend past the window; trim the rect (not a clip, which would
	// leave a half row clickable) to the canvas region under the app-chrome band.
	r = intersectRect(r, lay.clip)
	rowH := charListRowH(a.fontLineH())
	// One name, and one line height of text beside the scrollbar.
	if r.H < rowH || r.W-scrollBarW-scrollBarGap-charListTextInsetPx < rowH {
		return sdl.Rect{}, false
	}
	if buttons, ok := lay.rect(charSelectButtonsKey); ok {
		if ov := intersectRect(r, buttons); ov.W > 0 && ov.H > 0 {
			return sdl.Rect{}, false
		}
	}
	return r, true
}

// drawCharSelectControls draws the mapped controls at their theme rects and reports whether one changed the
// screen, in which case drawCharSelect must return immediately.
//
// Each is an existing control moved to a theme rect (same search field id, Spectate action, disconnect
// confirmation and Back rule as the classic header).
func (a *App) drawCharSelectControls(lay *charSelectLayoutCache, g charGridPlan) bool {
	c := a.ctx
	// The canvas region under the app-chrome band: under Crop and Custom the canvas runs off the window edges, and
	// this keeps controls out of the menu bar and server-tab strip.
	prev, had := c.pushClip(lay.clip)
	done := a.drawCharSelectControlsIn(lay, g)
	c.popClip(prev, had)
	return done
}

// drawCharSelectControlsIn is the body of drawCharSelectControls; the canvas clip is released on every exit.
func (a *App) drawCharSelectControlsIn(lay *charSelectLayoutCache, g charGridPlan) bool {
	c := a.ctx
	if r, ok := lay.rect(charSelectSearchKey); ok {
		a.charSearch, _ = c.TextField("charsearch", r, a.charSearch, "Search...")
	}
	// char_taken: AO2's filter checkbox, default checked (charselect.cpp:99); the theme sizes the slot, not the box.
	if r, ok := lay.rect(charSelectTakenKey); ok {
		// Centred in the slot but never above it: a slot shorter than the 16 px tick box would push it out of its rect.
		y := r.Y
		if gap := (r.H - checkboxBoxPx) / 2; gap > 0 {
			y += gap
		}
		was := a.charShowTaken
		a.charShowTaken = c.Checkbox(r.X, y, "Taken", a.charShowTaken)
		if a.charShowTaken != was {
			// The filter changed under both views; a stale scroll offset could park either past its end.
			a.charScroll, a.charListScroll = 0, 0
		}
		c.Tooltip(sdl.Rect{X: r.X, Y: y, W: r.W, H: checkboxBoxPx},
			"Show characters other players are already using")
	}
	// Spectate needs a session; this row draws before the handshake completes, so the button is absent until then.
	if r, ok := lay.rect(charSelectSpectatorKey); ok && a.sess != nil {
		if c.Button(r, "Spectate") {
			if !a.sess.Rehearsal {
				a.sess.PickCharacter(protocol.UnpairedCharID)
			}
			a.enterCourtroom()
			return true
		}
	}
	// back_to_lobby is AO2's leave-the-server button. While a courtroom is live behind this screen the safe action
	// is Back, and Disconnect stays in the menu bar.
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
// AO2 pages its grid; AsyncAO scrolls in whole rows, so a page is as many whole rows as the viewport holds.
func (a *App) drawCharSelectPageArrows(lay *charSelectLayoutCache, g charGridPlan) {
	c := a.ctx
	scroll := &a.charScroll
	// rowOffset, not rows*pitchY: on the themed lattice a page must be whole artwork rows or the snap lands elsewhere.
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

// drawCharNameList is AO2's char_list: a scrolling column of names beside the grid, sharing its filter and cells.
//
// Clicking a name selects it, outlines its cell and scrolls the grid to it; a double click picks it
// (itemDoubleClicked). The column draws before the grid so both see the same charListSel.
// Categories are absent: they need one char.ini fetch per character (docs/ROADMAP.md).
func (a *App) drawCharNameList(lay *charSelectLayoutCache, rows charListRows, query string, showTaken bool, g charGridPlan) {
	c := a.ctx
	body, ok := a.charSelectNameListRect(lay)
	if !ok {
		return
	}
	rowH := charListRowH(a.fontLineH())
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
	// pushClip, not SetClipRect: it fences input too, so a row scrolled under the header is not clickable.
	prev, had := c.pushClip(body)
	n := int32(0)
	for i := range rows.chars {
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
		// AO2 disables a taken row rather than hiding it while the taken filter is on (charselect.cpp:381-384).
		ink := ColText
		if rows.taken(i) {
			ink = ColTextDim
		}
		c.LabelClipped(row.X+charListTextInsetPx, y+(rowH-a.fontLineH())/2, textW, rows.name(i), ink)
		if !c.hovering(row) {
			continue
		}
		if c.dblClick && !rows.taken(i) {
			// AO2's itemDoubleClicked → char_clicked (charselect.cpp:55, :183).
			c.dblClick = false // consumed: the grid must not also read it
			c.popClip(prev, had)
			a.pickCharacter(i)
			return
		}
		if c.clicked {
			a.charListSel = i
			a.charScroll = revealRowInGrid(g, ord, a.charScroll)
		}
	}
	c.popClip(prev, had)
}

// revealRowInGrid returns the grid scroll offset that brings the ord-th filtered cell into view with the least
// movement; charGridScroll clamps and row-snaps it next frame.
func revealRowInGrid(g charGridPlan, ord, scroll int32) int32 {
	if g.cols < 1 || g.pitchY < 1 {
		return scroll
	}
	// rowOffset, not row*pitchY: on the themed lattice the two differ and the snap would land elsewhere.
	top := g.rowOffset(ord / g.cols)
	if top < scroll {
		return top
	}
	if bottom := top + g.cell - g.view.H; bottom > scroll {
		return bottom
	}
	return scroll
}
