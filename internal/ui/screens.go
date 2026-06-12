package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

const (
	pad      int32 = 10
	rowH     int32 = 28
	fieldH   int32 = 26
	btnH     int32 = 28
	iconCell int32 = 64
	iconGap  int32 = 8
	// previewMax bounds the hover-preview pop-up edge; small sprites
	// integer-upscale toward it so pixel art previews stay readable.
	previewMax int32 = 520
	// emoteBtnCell matches AO2's 40×40 emotions/button<N> art.
	emoteBtnCell int32 = 40
	// scrollBarW/Gap reserve the scrollbar lane beside scrolling lists.
	scrollBarW   int32 = 12
	scrollBarGap int32 = 6
)

func osHostname() (string, error) { return os.Hostname() }

// --- LOBBY ------------------------------------------------------------------------

func (a *App) drawLobby(w, h int32) {
	a.pollLobbyFetch()
	c := a.ctx
	a.drawScreenBackdrop(w, h, "lobbybackground")
	c.Heading(pad, pad, "AsyncAO — Server Phone Book & Lobby", ColText)
	c.Label(pad, pad+30, a.lobbyStatus, ColTextDim)
	if a.connErr != "" {
		c.Label(pad+220, pad+30, a.connErr, ColDanger)
	}

	// Top bar buttons.
	if c.Button(sdl.Rect{X: w - 110 - pad, Y: pad, W: 110, H: btnH}, "Refresh") {
		a.RefreshServers()
	}
	if c.Button(sdl.Rect{X: w - 230 - pad, Y: pad, W: 110, H: btnH}, "Settings") {
		a.prevScreen = ScreenLobby
		a.screen = ScreenSettings
	}
	if c.Button(sdl.Rect{X: w - 350 - pad, Y: pad, W: 110, H: btnH}, "About") {
		a.prevScreen = ScreenLobby
		a.screen = ScreenAbout
	}

	// Direct connect row.
	dcY := pad + 56
	c.Label(pad, dcY+4, "Direct connect (ip:port, url:port, ws:// or wss://):", ColText)
	fieldX := pad + c.TextWidth("Direct connect (ip:port, url:port, ws:// or wss://):") + 12
	a.directInput, _ = c.TextField("direct", sdl.Rect{X: fieldX, Y: dcY, W: 280, H: fieldH}, a.directInput, "127.0.0.1:50001")
	a.directSecure = c.Checkbox(fieldX+290, dcY+4, "TLS (wss)", a.directSecure)
	a.directSave = c.Checkbox(fieldX+390, dcY+4, "Save to phone book", a.directSave)
	if c.Button(sdl.Rect{X: fieldX + 560, Y: dcY, W: 100, H: btnH}, "Connect") {
		a.directConnect()
	}

	// Server rows. Click once: expand the full description under the
	// row; click the selected row again: join (Join button still works).
	listTop := dcY + 40
	a.lobbyScroll -= c.wheelY * scrollStepPx
	if a.lobbyScroll < 0 {
		a.lobbyScroll = 0
	}
	lineH := int32(c.font.Height()) + 3
	y := listTop - a.lobbyScroll
	legacyHeaderDrawn := false
	for i := range a.servers {
		e := &a.servers[i]
		if !e.Joinable() && !legacyHeaderDrawn {
			if y > listTop-rowH && y < h {
				msg := "— NOT SUPPORTED: " + network.UnsupportedReason
				c.Label(pad, y+4, msg, ColDanger)
				// Server owner looking at their own black row: the
				// upgrade guide lives one click away.
				bx := pad + c.TextWidth(msg) + 12
				const helpBtnW = 180
				if bx+helpBtnW > w-pad {
					bx = w - pad - helpBtnW
				}
				if c.Button(sdl.Rect{X: bx, Y: y, W: helpBtnW, H: 24}, "For server owners…") {
					a.screen = ScreenServerHelp
				}
			}
			y += rowH
			legacyHeaderDrawn = true
		}
		if y > h {
			break
		}
		if y > listTop-rowH {
			a.drawServerRow(e, i, y, w)
		}
		y += rowH
		if i == a.selServer && len(a.descLines) > 0 {
			boxH := int32(len(a.descLines)+len(a.descLinks))*lineH + 8
			if y > listTop-boxH && y < h {
				box := sdl.Rect{X: pad + 16, Y: y, W: w - 2*pad - 16, H: boxH - 4}
				c.Fill(box, ColPanelHi)
				c.Border(box, ColAccent)
				ly := y + 3
				for _, line := range a.descLines {
					c.LabelClipped(box.X+6, ly, box.W-12, line, ColText)
					ly += lineH
				}
				// URLs from the description: clickable, opens the browser.
				for _, link := range a.descLinks {
					a.linkLabel(box.X+6, ly, box.W-12, link)
					ly += lineH
				}
			}
			y += boxH
		}
	}
}

func (a *App) drawServerRow(e *network.ServerEntry, idx int, y, w int32) {
	c := a.ctx
	row := sdl.Rect{X: pad, Y: y, W: w - 2*pad, H: rowH - 2}
	hover := c.hovering(row)
	bg := ColPanel
	if idx == a.selServer {
		bg = ColPanelHi
	} else if hover {
		bg = ColPanelHi
	}
	c.Fill(row, bg)

	// Tier swatch.
	c.Fill(sdl.Rect{X: row.X + 2, Y: y + 4, W: 14, H: rowH - 10}, tierColor(*e))

	// Star toggle.
	starRect := sdl.Rect{X: row.X + 22, Y: y + 2, W: 22, H: rowH - 6}
	starCol := ColTextDim
	if e.Favorite {
		starCol = ColStar
	}
	c.Label(starRect.X+4, y+4, "★", starCol)
	if c.hovering(starRect) && c.clicked {
		a.toggleFavorite(e)
		return
	}

	name := fmt.Sprintf("%s  (%d players)", e.Name, e.Players)
	if !e.Joinable() {
		name = e.Name + "  — legacy TCP only"
	}
	textCol := ColText
	if !e.Joinable() {
		textCol = ColTextDim
	}
	c.LabelClipped(row.X+52, y+5, row.W-260, name, textCol)

	tierLabel := e.Security().String()
	c.Label(row.X+row.W-260, y+5, tierLabel, tierColor(*e))

	joinHover := false
	if e.Joinable() {
		joinBtn := sdl.Rect{X: row.X + row.W - 80, Y: y + 1, W: 76, H: rowH - 4}
		joinHover = c.hovering(joinBtn)
		if c.Button(joinBtn, "Join") {
			a.Connect(e.Name, e.WebSocketURL())
			return
		}
	}

	// Row body click: select-and-expand first, join on the second click.
	if hover && !joinHover && c.clicked {
		if idx == a.selServer && e.Joinable() {
			a.Connect(e.Name, e.WebSocketURL())
			return
		}
		a.selServer = idx
		desc := e.Description
		if desc == "" {
			desc = "(no description)"
		}
		// Wrapped ONCE per selection — drawing reuses the cached lines;
		// any links in the description become clickable rows below it.
		a.descLines = c.WrapText(desc, w-2*pad-40, maxDescLines)
		a.descLinks = extractURLs(desc, maxDescLinks)
	}
}

// maxDescLinks bounds the clickable-link rows under a description.
const maxDescLinks = 4

// drawWardrobeGrid is the char-select grid over the wardrobe menu: same
// cells, same demand pipeline, same search box. Picking claims the first
// free slot and wears the custom on PV (wearFromMenu).
func (a *App) drawWardrobeGrid(w, h, gridTop int32, cols, cellH, visibleH int32, query string) {
	c := a.ctx
	if len(a.iniList) == 0 {
		switch {
		case a.iniBusy:
			c.Label(pad, gridTop+8, "Fetching "+iniswapFileName+"...", ColTextDim)
		case a.iniListErr != "":
			c.LabelClipped(pad, gridTop+8, w-2*pad, a.iniListErr, ColTextDim)
		default:
			c.Label(pad, gridTop+8, "Wardrobe empty — star characters or add folders from the courtroom Wardrobe menu.", ColTextDim)
		}
		return
	}

	matches := int32(0)
	for i := range a.iniList {
		if query == "" || strings.Contains(a.iniLower[i], query) {
			matches++
		}
	}
	contentH := (matches + cols - 1) / cols * cellH
	a.iniScroll -= c.wheelY * scrollStepPx
	track := sdl.Rect{X: w - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.iniScroll = c.VScrollbar("iniscroll", track, a.iniScroll, contentH, visibleH)

	col, row := int32(0), int32(0)
	for i := range a.iniList {
		if query != "" && !strings.Contains(a.iniLower[i], query) {
			continue
		}
		x := pad + col*(iconCell+iconGap)
		y := gridTop + row*cellH - a.iniScroll
		if y > -iconCell && y < h {
			a.drawIniswapCell(i, sdl.Rect{X: x, Y: y, W: iconCell, H: iconCell})
		}
		col++
		if col >= cols {
			col = 0
			row++
		}
	}
	if a.previewBase != "" {
		a.drawSpritePreview(w, h)
		if c.clicked {
			a.previewBase = ""
		}
	}
}

func (a *App) toggleFavorite(e *network.ServerEntry) {
	url := e.WebSocketURL()
	if url == "" {
		return
	}
	if e.Favorite {
		a.d.Prefs.RemoveFavorite(url)
	} else {
		a.d.Prefs.AddFavorite(e.Name, url, e.Description)
	}
	a.servers = a.mergedFavorites()
	a.selServer, a.descLines = -1, nil // the list just reordered
}

func (a *App) directConnect() {
	url, err := network.ParseDirectAddress(a.directInput, a.directSecure)
	if err != nil {
		a.connErr = err.Error()
		return
	}
	if a.directSave {
		a.d.Prefs.AddFavorite(a.directInput, url, "")
	}
	a.Connect(a.directInput, url)
}

// --- CHARACTER SELECT ---------------------------------------------------------------

func (a *App) drawCharSelect(w, h int32) {
	c := a.ctx
	a.drawScreenBackdrop(w, h, "charselect_bg")
	title := "Choose a character"
	if a.serverName != "" {
		title += " — " + a.serverName
	}
	c.Heading(pad, pad, title, ColText)

	if c.Button(sdl.Rect{X: w - 120 - pad, Y: pad, W: 120, H: btnH}, "Disconnect") {
		a.Disconnect()
		return
	}
	if a.sess == nil {
		c.Label(pad, pad+40, "Loading...", ColTextDim)
		return
	}
	if a.sess.Phase() != courtroom.PhaseReady {
		c.Label(pad, pad+40, "Handshaking with server...", ColTextDim)
		return
	}

	a.charSearch, _ = c.TextField("charsearch", sdl.Rect{X: pad, Y: pad + 36, W: 230, H: fieldH}, a.charSearch, "Search...")

	// Grid tabs right of the search: the same grid swaps between the
	// server's list and your wardrobe (favourites + server customs), so
	// joining AS an iniswap is one click from the door.
	tabX := pad + 240
	tabs := [...]struct {
		id    int
		label string
	}{{charTabServer, "Characters"}, {charTabWardrobe, "Wardrobe"}}
	for _, tb := range tabs {
		bw := c.TextWidth(tb.label) + 20
		if a.charTab == tb.id {
			c.Fill(sdl.Rect{X: tabX - 2, Y: pad + 34, W: bw + 4, H: btnH + 4}, ColAccent)
		}
		if c.Button(sdl.Rect{X: tabX, Y: pad + 36, W: bw, H: btnH}, tb.label) {
			a.charTab = tb.id
			if tb.id == charTabWardrobe {
				a.ensureIniList()
			}
		}
		tabX += bw + 6
	}
	specX := tabX + 8
	if c.Button(sdl.Rect{X: specX, Y: pad + 36, W: 90, H: btnH}, "Spectate") {
		a.sess.PickCharacter(protocol.UnpairedCharID)
		a.enterCourtroom()
		return
	}
	if a.room != nil {
		// Re-picking from the courtroom ("Change character"): allow backing
		// out without dropping the session.
		if c.Button(sdl.Rect{X: specX + 96, Y: pad + 36, W: 90, H: btnH}, "Back") {
			a.screen = ScreenCourtroom
			return
		}
	}
	if a.warnActive() {
		c.LabelClipped(specX+200, pad+42, w-specX-200-pad, a.warnLine, ColDanger)
	}

	gridTop := pad + 76
	gridW := w - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (iconCell + iconGap)
	if cols < 1 {
		cols = 1
	}
	cellH := iconCell + iconGap + 14
	visibleH := h - gridTop - pad
	query := strings.ToLower(strings.TrimSpace(a.charSearch))

	if a.charTab == charTabWardrobe {
		a.drawWardrobeGrid(w, h, gridTop, cols, cellH, visibleH, query)
		return
	}

	a.ensureCharLower()
	// Pre-count matches so the scrollbar knows the content height; the
	// draw loop below walks the same list anyway.
	matches := int32(0)
	for i := range a.sess.Chars {
		if query == "" || strings.Contains(a.charLower[i], query) {
			matches++
		}
	}
	contentH := (matches + cols - 1) / cols * cellH

	a.charScroll -= c.wheelY * scrollStepPx
	track := sdl.Rect{X: w - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.charScroll = c.VScrollbar("charscroll", track, a.charScroll, contentH, visibleH)

	col, row := int32(0), int32(0)
	previewRequested := false
	for i := range a.sess.Chars {
		slot := &a.sess.Chars[i]
		if query != "" && !strings.Contains(a.charLower[i], query) {
			continue
		}
		x := pad + col*(iconCell+iconGap)
		y := gridTop + row*cellH - a.charScroll
		cell := sdl.Rect{X: x, Y: y, W: iconCell, H: iconCell}
		if y > -iconCell && y < h {
			a.drawCharCell(slot, cell, i)
			if c.HoverPreview("char:"+slot.Name, cell) {
				a.previewBase = a.urls.Emote(slot.Name, "normal", courtroom.EmoteIdle)
				a.d.Manager.PrefetchWithFallback(a.previewBase, a.urls.EmoteBare(slot.Name, "normal"), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
				previewRequested = true
			}
		}
		col++
		if col >= cols {
			col = 0
			row++
		}
	}
	if !previewRequested && !c.rightClicked {
		// keep showing while hovered; HoverPreview clears hoverID on exit
	}
	if a.previewBase != "" {
		a.drawSpritePreview(w, h)
		if c.clicked || (a.ctx.hoverID == "" && !previewRequested) {
			a.previewBase = ""
		}
	}
}

func (a *App) drawCharCell(slot *courtroom.CharacterSlot, cell sdl.Rect, idx int) {
	c := a.ctx
	c.Fill(cell, ColPanel)
	base := a.urls.CharIcon(slot.Name)
	if page, ok := a.cachedPage(&a.iconPages, &a.iconPagesGen, len(a.sess.Chars), idx, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &cell)
	} else {
		// Not resident: demand it (visible = not speculation) and draw the
		// initials placeholder; the texture pops in live.
		a.demandAsset(&a.iconAsk, len(a.sess.Chars), idx, base, assets.AssetTypeCharIcon) // AssetType: CharIcon
		initial := slot.Name
		if len(initial) > 2 {
			initial = initial[:2]
		}
		c.Label(cell.X+iconCell/2-8, cell.Y+iconCell/2-8, initial, ColTextDim)
	}
	if slot.Taken {
		c.Fill(sdl.Rect{X: cell.X, Y: cell.Y, W: cell.W, H: cell.H}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
		c.Label(cell.X+6, cell.Y+iconCell/2-8, "taken", ColDanger)
	}
	c.LabelClipped(cell.X, cell.Y+iconCell+1, iconCell, slot.Name, ColTextDim)
	if c.hovering(cell) {
		a.warmCharINI(slot.Name) // pick = memory hit, not an RTT
		if c.clicked && !slot.Taken {
			a.sess.PickCharacter(idx)
		}
	}
}

// drawSpritePreview shows the full-size idle sprite for the hovered or
// right-clicked icon/emote (the "show the entire thing" pop-up).
func (a *App) drawSpritePreview(w, h int32) {
	c := a.ctx
	page, ok := a.d.Store.Get(a.previewBase)
	if !ok || len(page.Frames) == 0 {
		return
	}
	scale := int32(1)
	pw, ph := page.W, page.H
	for (pw > previewMax || ph > previewMax) && scale < 8 {
		pw /= 2
		ph /= 2
		scale *= 2
	}
	// Small art doubles up toward the box (integer scale keeps pixels crisp).
	for pw*2 <= previewMax && ph*2 <= previewMax {
		pw *= 2
		ph *= 2
	}
	dst := sdl.Rect{X: w - pw - pad*2, Y: h - ph - pad*2, W: pw, H: ph}
	frame := sdl.Rect{X: dst.X - 4, Y: dst.Y - 4, W: dst.W + 8, H: dst.H + 8}
	c.Fill(frame, ColPanel)
	c.Border(frame, ColAccent)
	_ = c.Ren.Copy(page.Frames[0], nil, &dst)
}

// --- COURTROOM ----------------------------------------------------------------------

func (a *App) drawCourtroom(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	a.pollCharINI()
	if a.room == nil || a.sess == nil {
		a.screen = ScreenLobby
		return
	}

	// Theme-driven geometry: when the theme ships courtroom_design.ini
	// (and the toggle is on), the courtroom IS the theme's layout.
	if lay := a.themeLayout(w, h); lay.valid && a.d.Prefs.ThemeLayoutEnabled() {
		a.drawCourtroomThemed(w, h, lay)
		return
	}

	// Viewport: AO 4:3 at the user's width percent (View −/+ buttons).
	vpW := w * int32(a.vpPct) / DefaultScalePct
	vpH := vpW * 3 / 4
	if vpH > h-220 {
		vpH = h - 220
		vpW = vpH * 4 / 3
	}
	vp := sdl.Rect{X: pad, Y: pad, W: vpW, H: vpH}
	c.Fill(vp, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	a.d.Viewport.Render(c.Ren, &a.room.Scene, vp)
	a.handleSpriteDrag(vp)
	a.handleHotkeys() // Ctrl-chords (shouts, pos, music, screenshot...)
	a.drawChatOverlay(vp)
	a.drawCourtOverlays(vp, nil) // HP bars, clocks, badges, splashes

	// Modal popups: the kit has no z-aware input, so the controls
	// underneath simply don't draw (and don't see clicks) — same pattern
	// as the iniswap menu.
	switch {
	case a.showIni:
		a.drawIniswapPanel(w, h)
		return
	case a.showEvid:
		a.drawEvidencePanel(w, h)
		return
	case a.showModcall:
		a.drawModcallDialog(w, h)
		return
	case a.showUICfg:
		a.drawUICfgPanel(w, h)
		return
	}

	// Right column: log + music.
	rx := vp.X + vp.W + pad
	rw := w - rx - pad
	if !a.panelHidden(panelLog) {
		a.drawLogPanel(sdl.Rect{X: rx, Y: pad, W: rw, H: vpH}, vp)
	}

	// Bottom: IC input, emotes, controls.
	a.drawICControls(w, h, vp)
}

// spriteHitRect mirrors Viewport.drawSprite's placement math so drags
// hit-test exactly what's drawn. Runs only on press/right-click edges.
func (a *App) spriteHitRect(vp sdl.Rect, layer *courtroom.SpriteLayer) (sdl.Rect, bool) {
	if !layer.Visible || layer.Active == "" {
		return sdl.Rect{}, false
	}
	page, ok := a.d.Store.Get(layer.Active)
	if !ok || page.H == 0 {
		return sdl.Rect{}, false
	}
	scaledW := vp.H * page.W / page.H
	return sdl.Rect{
		X: vp.X + (vp.W-scaledW)/2 + vp.W*int32(layer.OffsetX)/100,
		Y: vp.Y + vp.H*int32(layer.OffsetY)/100,
		W: scaledW,
		H: vp.H,
	}, true
}

// handleSpriteDrag implements client-side sprite repositioning: grab any
// character in the viewport and put them wherever you want — the server
// keeps setting positions per message, the override (keyed by character)
// wins afterwards. Right-click a sprite to reset it. All math runs on
// press/drag edges only; idle frames cost one bool check.
func (a *App) handleSpriteDrag(vp sdl.Rect) {
	c := a.ctx
	sc := &a.room.Scene
	pressed := c.mouseDown && !a.prevDown
	a.prevDown = c.mouseDown
	if !c.mouseDown {
		a.dragName = "" // release keeps the override, ends the drag
	}

	// Front-most first, matching render z-order; the chat box area is
	// the text's, not the sprites'.
	boxH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	inChatBox := c.mouseY >= vp.Y+vp.H-boxH
	layers := [2]*courtroom.SpriteLayer{&sc.Speaker, &sc.Pair}
	if sc.PairActive && !sc.SpeakerInFront {
		layers[0], layers[1] = &sc.Pair, &sc.Speaker
	}

	if pressed && c.hovering(vp) && !inChatBox && a.dragName == "" {
		for _, layer := range layers {
			if r, ok := a.spriteHitRect(vp, layer); ok && c.hovering(r) && layer.Name != "" {
				if _, have := a.spriteOv[strings.ToLower(layer.Name)]; !have && len(a.spriteOv) >= spriteOvCap {
					break // table full; never unbounded
				}
				a.dragName = strings.ToLower(layer.Name)
				a.dragStart = [2]int32{c.mouseX, c.mouseY}
				a.dragBase = [2]int{layer.OffsetX, layer.OffsetY}
				break
			}
		}
	}

	if a.dragName != "" && c.mouseDown && vp.W > 0 && vp.H > 0 {
		dx := int(c.mouseX-a.dragStart[0]) * 100 / int(vp.W)
		dy := int(c.mouseY-a.dragStart[1]) * 100 / int(vp.H)
		a.spriteOv[a.dragName] = [2]int{
			clampOffset(a.dragBase[0] + dx),
			clampOffset(a.dragBase[1] + dy),
		}
	}

	// Right-click a sprite: drop its override (back to server placement).
	if c.rightClicked && len(a.spriteOv) > 0 && c.hovering(vp) && !inChatBox {
		for _, layer := range layers {
			if r, ok := a.spriteHitRect(vp, layer); ok && c.hovering(r) {
				delete(a.spriteOv, strings.ToLower(layer.Name))
				break
			}
		}
	}
}

func (a *App) drawChatOverlay(vp sdl.Rect) {
	c := a.ctx
	sc := &a.room.Scene
	if sc.MessageText == "" && sc.ShownameText == "" {
		return
	}
	// Box height follows the MsgBox knob ONLY; text size (the Text knob)
	// lives inside it and clips at the box edge — big text never inflates
	// the box, small text never shrinks it.
	boxH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	if max := vp.H * 3 / 5; boxH > max {
		boxH = max
	}
	box := sdl.Rect{X: vp.X, Y: vp.Y + vp.H - boxH, W: vp.W, H: boxH}
	// Theme chatbox skin when the theme ships one; flat translucent
	// panel otherwise (themePage self-heals T1 eviction).
	skinned := false
	if page, ok := a.themePage(themeStemChatbox); ok {
		_ = c.Ren.Copy(page.Frames[0], nil, &box)
		skinned = true
	}
	if !skinned {
		c.Fill(box, sdl.Color{R: 16, G: 16, B: 24, A: 215})
		c.Border(box, ColAccent)
	}
	// Theme text colors are designed against the theme's own skin; on the
	// flat fallback panel they can be unreadable (black-on-dark was a real
	// report), so they only apply while the skin actually drew.
	nameCol := ColAccent
	if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	c.Label(box.X+8, box.Y+4, sc.ShownameText, nameCol)

	wrapW := box.W - 16
	a.ensureChatRaster(wrapW, skinned)
	if a.msRaster != nil {
		// Clip to the box: oversized Text settings stay INSIDE it.
		_ = c.Ren.SetClipRect(&box)
		a.msRaster.Draw(c.Ren, sc.VisibleRunes, box.X+8, box.Y+26)
		_ = c.Ren.SetClipRect(nil)
	}

	a.chatZoomWheel(box)
}

// ensureChatRaster (re)rasterizes the current message when the text,
// color, zoom, wrap width, or skin presence changed — shared by the
// classic overlay and the themed chatbox.
func (a *App) ensureChatRaster(wrapW int32, skinned bool) {
	sc := &a.room.Scene
	if a.msRaster != nil && a.rasterText == sc.MessageText && a.rasterColor == sc.TextColor &&
		a.rasterScale == a.chatPct && a.rasterW == wrapW && a.rasterSkinned == skinned {
		return
	}
	if a.msRaster != nil {
		a.msRaster.Destroy()
		a.msRaster = nil
	}
	if sc.MessageText == "" {
		return
	}
	raster, err := renderRaster(a, sc, wrapW, skinned)
	if err != nil {
		return
	}
	a.msRaster = raster
	a.rasterText = sc.MessageText
	a.rasterColor = sc.TextColor
	a.rasterScale = a.chatPct
	a.rasterW = wrapW
	a.rasterSkinned = skinned
}

// chatZoomWheel: Ctrl+wheel over the chatbox zooms the chat text
// (browser convention) — shared by both chatbox flavors.
func (a *App) chatZoomWheel(box sdl.Rect) {
	c := a.ctx
	if c.ctrlHeld && c.wheelY != 0 && c.hovering(box) {
		a.chatPct = clampInt(a.chatPct+int(c.wheelY)*config.ScaleStepPercent,
			config.MinChatScalePercent, config.MaxChatScalePercent)
		a.saveLayout()
	}
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func (a *App) drawLogPanel(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanel)
	c.Border(r, ColPanelHi)
	tab := r.W / 4
	if c.Button(sdl.Rect{X: r.X, Y: r.Y, W: tab, H: btnH}, "Log") {
		a.logTab = logTabLog
	}
	if c.Button(sdl.Rect{X: r.X + tab, Y: r.Y, W: tab, H: btnH}, "Music") {
		a.logTab = logTabMusic
	}
	if c.Button(sdl.Rect{X: r.X + 2*tab, Y: r.Y, W: tab, H: btnH}, "Areas") {
		a.logTab = logTabAreas
	}
	if c.Button(sdl.Rect{X: r.X + 3*tab, Y: r.Y, W: r.W - 3*tab, H: btnH}, "OOC") {
		a.logTab = logTabOOC
	}
	inner := sdl.Rect{X: r.X + 4, Y: r.Y + btnH + 4, W: r.W - 8, H: r.H - btnH - 8}
	// Ctrl+wheel anywhere on the panel resizes the log/OOC/list text;
	// plain wheel keeps scrolling the active list.
	if c.ctrlHeld && c.wheelY != 0 && c.hovering(r) {
		a.logPct = clampInt(a.logPct+int(c.wheelY)*config.ScaleStepPercent,
			config.MinLogScalePercent, config.MaxLogScalePercent)
		a.saveLayout()
	}
	switch a.logTab {
	case logTabMusic:
		a.drawMusicList(inner)
		return
	case logTabAreas:
		a.drawAreaList(inner)
		return
	case logTabOOC:
		a.drawOOCPanel(inner)
		return
	}
	// IC log tab: search box + Copy/TXT/HTML row, then the colored
	// scrollback (AO text colors preserved per entry).
	rowY := inner.Y
	searchW := inner.W - 3*52 - 12
	a.logSearch, _ = c.TextField("logsearch", sdl.Rect{X: inner.X, Y: rowY, W: searchW, H: 24}, a.logSearch, "Search log...")
	bx := inner.X + searchW + 4
	if c.Button(sdl.Rect{X: bx, Y: rowY, W: 50, H: 24}, "Copy") {
		a.copyICLog()
	}
	if c.Button(sdl.Rect{X: bx + 52, Y: rowY, W: 50, H: 24}, "TXT") {
		a.exportICLog(false)
	}
	if c.Button(sdl.Rect{X: bx + 104, Y: rowY, W: 50, H: 24}, "HTML") {
		a.exportICLog(true)
	}
	a.drawICLogList(sdl.Rect{X: inner.X, Y: rowY + 28, W: inner.W, H: inner.H - 28})
}

// drawICLogList renders the colored IC scrollback (search-filtered) into
// rect — used by the classic Log tab and the themed ic_chatlog element.
func (a *App) drawICLogList(list sdl.Rect) {
	c := a.ctx
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 2
	idx := a.icLogFiltered()
	contentH := int32(len(idx)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	if !c.ctrlHeld && c.hovering(list) { // ctrl+wheel resizes text
		a.icScroll -= c.wheelY * scrollStepPx
	}
	// Follow the tail unless the user scrolled back (one line of slack).
	if maxScroll := contentH - list.H; maxScroll > 0 && a.icScroll >= maxScroll-lineH {
		a.icScroll = maxScroll
	}
	a.icScroll = c.VScrollbar("icscroll", track, a.icScroll, contentH, list.H)
	y := list.Y - a.icScroll
	for _, i := range idx {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			e := &a.icLog[i]
			col := ColText
			if e.color > 0 {
				col = render.TextColor(e.color)
			}
			c.LabelClippedFont(font, list.X, y, list.W-scrollBarW-scrollBarGap, e.text, col)
		}
		y += lineH
	}
}

// drawOOCLogList renders the OOC scrollback into rect (themed
// server_chatlog element; the classic OOC tab keeps its own copy with the
// identity fields).
func (a *App) drawOOCLogList(list sdl.Rect) {
	c := a.ctx
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	lines := a.oocWrapped(wrapW) // MOTDs wrap — never truncate
	contentH := int32(len(lines)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	if !c.ctrlHeld && c.hovering(list) {
		a.oocScroll -= c.wheelY * scrollStepPx
	}
	if maxScroll := contentH - list.H; maxScroll > 0 && a.oocScroll >= maxScroll-lineH {
		a.oocScroll = maxScroll
	}
	a.oocScroll = c.VScrollbar("oocscroll", track, a.oocScroll, contentH, list.H)
	y := list.Y - a.oocScroll
	for _, line := range lines {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			c.LabelClippedFont(font, list.X, y, wrapW, line, ColText)
		}
		y += lineH
	}
}

// submitOOC sends the OOC input if non-blank (shared classic/themed).
func (a *App) submitOOC() {
	if strings.TrimSpace(a.oocInput) == "" || a.sess == nil {
		return
	}
	a.sess.SendOOC(a.oocName, a.oocInput)
	a.oocInput = ""
}

// drawOOCPanel is the actual OOC box: full scrollable history plus the
// identity fields — IC showname (live; outgoing messages read it per
// send) and the permanent OOC name. Both persist via the debounced saver.
func (a *App) drawOOCPanel(r sdl.Rect) {
	c := a.ctx
	fH := a.inputFieldH()
	fieldsH := 2*(fH+6) + 4
	list := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - fieldsH}

	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	lines := a.oocWrapped(wrapW) // MOTDs wrap — never truncate
	contentH := int32(len(lines)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	if !c.ctrlHeld { // ctrl+wheel resizes text, never scrolls
		a.oocScroll -= c.wheelY * scrollStepPx
	}
	// Follow the tail unless the user scrolled back (within one line of
	// the bottom counts as "at the bottom").
	maxScroll := contentH - list.H
	if maxScroll > 0 && a.oocScroll >= maxScroll-lineH {
		a.oocScroll = maxScroll
	}
	a.oocScroll = c.VScrollbar("oocscroll", track, a.oocScroll, contentH, list.H)
	y := list.Y - a.oocScroll
	for _, line := range lines {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			c.LabelClippedFont(font, list.X, y, wrapW, line, ColText)
		}
		y += lineH
	}

	// Identity fields: full width (side labels squished the boxes in the
	// narrow right column) — the placeholders carry the labels.
	fy := r.Y + r.H - fieldsH + 4
	shown := a.d.Prefs.SavedShowname()
	if next, _ := c.TextField("icshowname", sdl.Rect{X: r.X, Y: fy, W: r.W - 4, H: fH}, shown, "IC showname (blank = character name)"); next != shown {
		a.d.Prefs.SetShowname(next)
	}
	fy += fH + 6
	prev := a.oocName
	a.oocName, _ = c.TextField("oocname2", sdl.Rect{X: r.X, Y: fy, W: r.W - 4, H: fH}, a.oocName, "Permanent OOC name")
	if a.oocName != prev {
		a.d.Prefs.SetOOCName(a.oocName)
	}
}

// drawAreaList lists the server's areas; clicking one requests the room
// swap. AO area transfers ride the MC packet with the area name in place
// of a track (AO2-Client sends areas from the same list — the courtroom's
// isAreaTransfer filter keeps them out of the audio path client-side).
func (a *App) drawAreaList(r sdl.Rect) {
	c := a.ctx
	if len(a.sess.Areas) == 0 {
		c.Label(r.X+4, r.Y+4, "Server reported no areas.", ColTextDim)
		return
	}
	if !c.ctrlHeld { // ctrl+wheel resizes text, never scrolls
		a.areaScroll -= c.wheelY * scrollStepPx
	}
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 10
	contentH := int32(len(a.sess.Areas)) * lineH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.areaScroll = c.VScrollbar("areascroll", track, a.areaScroll, contentH, r.H)
	y := r.Y - a.areaScroll
	for i, area := range a.sess.Areas {
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-lineH {
			row := sdl.Rect{X: r.X, Y: y, W: r.W - scrollBarW - scrollBarGap, H: lineH - 4}
			hover := c.hovering(row)
			if hover {
				c.Fill(row, ColPanelHi)
			}
			// ARUP columns: "name [players] (STATUS) [lock] CM: x",
			// row color keyed by status — the live "which area is
			// active" signal (courtroom.cpp list_areas).
			line, col := area, ColText
			if i < len(a.sess.AreaInfo) {
				info := &a.sess.AreaInfo[i]
				if info.Players >= 0 {
					line += fmt.Sprintf("  [%d]", info.Players)
				}
				if info.Status != "" {
					line += " " + info.Status
					col = areaStatusColor(info.Status)
				}
				switch strings.ToUpper(info.Lock) {
				case "LOCKED":
					line += " [locked]"
					col = ColDanger
				case "SPECTATABLE":
					line += " [spec]"
				}
				if info.CM != "" && !strings.EqualFold(info.CM, "FREE") {
					line += " CM: " + info.CM
				}
			}
			c.LabelClippedFont(font, r.X+4, y+4, row.W-8, line, col)
			if hover && c.clicked {
				a.sess.RequestMusic(area) // area transfer rides MC
				a.curArea = area          // Rich Presence (best-effort)
				a.updatePresence()
			}
		}
		y += lineH
	}
}

// drawIniswapPanel is the custom character menu: every name from the
// server's iniswap.txt as a char-select-grade grid — same demand pipeline
// (paced asks, 64 px thumbnail icons, 404 cache), same search, same
// scrollbar, same 3 s hover preview. Picking one iniswaps outgoing
// messages into that folder; the server slot is untouched.
func (a *App) drawIniswapPanel(w, h int32) {
	c := a.ctx
	a.pollIniswap()
	panel := sdl.Rect{X: pad * 3, Y: pad * 3, W: w - pad*6, H: h - pad*6}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+8, "Wardrobe — your characters, any server", ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 90 - pad, Y: panel.Y + 8, W: 90, H: btnH}, "Close") {
		a.showIni = false
		return
	}

	y := panel.Y + 44
	active := "none (using " + a.myCharName() + ")"
	if a.iniChar != "" {
		active = a.iniChar
	}
	c.LabelClipped(panel.X+pad, y+4, 340, "Wearing: "+active, ColAccent)
	if a.iniChar != "" {
		if c.Button(sdl.Rect{X: panel.X + 360, Y: y, W: 130, H: btnH}, "Take off") {
			a.setIniswap("")
		}
	}
	y += 32
	a.iniSearch, _ = c.TextField("iniswapsearch", sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH}, a.iniSearch, "Search...")
	// Add any folder name on the current asset base to the wardrobe —
	// no server list required (★ marks saved entries; ★ persists
	// across sessions and servers).
	var addNow bool
	a.iniAdd, addNow = c.TextField("iniswapadd", sdl.Rect{X: panel.X + pad + 240, Y: y, W: 230, H: fieldH}, a.iniAdd, "Add folder to wardrobe...")
	if c.Button(sdl.Rect{X: panel.X + pad + 476, Y: y, W: 60, H: btnH}, "Add") || addNow {
		if a.d.Prefs.AddWardrobe(a.iniAdd) {
			a.iniAdd = ""
			a.rebuildIniMenu()
		}
	}
	statusX := panel.X + pad + 550
	switch {
	case a.iniBusy:
		c.Label(statusX, y+4, "Fetching "+iniswapFileName+"...", ColTextDim)
	case a.iniListErr != "":
		c.LabelClipped(statusX, y+4, panel.X+panel.W-statusX-pad, a.iniListErr, ColTextDim)
	default:
		c.Label(statusX, y+4, fmt.Sprintf("%d entries", len(a.iniList)), ColTextDim)
	}
	y += 36

	// Grid: clone of the char-select layout over the iniswap list.
	gridTop := y
	gridW := panel.W - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (iconCell + iconGap)
	if cols < 1 {
		cols = 1
	}
	query := strings.ToLower(strings.TrimSpace(a.iniSearch))
	matches := int32(0)
	for i := range a.iniList {
		if query == "" || strings.Contains(a.iniLower[i], query) {
			matches++
		}
	}
	cellH := iconCell + iconGap + 14
	contentH := (matches + cols - 1) / cols * cellH
	visibleH := panel.Y + panel.H - gridTop - pad

	a.iniScroll -= c.wheelY * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.iniScroll = c.VScrollbar("iniscroll", track, a.iniScroll, contentH, visibleH)

	col, row := int32(0), int32(0)
	for i := range a.iniList {
		if query != "" && !strings.Contains(a.iniLower[i], query) {
			continue
		}
		x := panel.X + pad + col*(iconCell+iconGap)
		yy := gridTop + row*cellH - a.iniScroll
		if yy > gridTop-iconCell && yy < panel.Y+panel.H-14 {
			a.drawIniswapCell(i, sdl.Rect{X: x, Y: yy, W: iconCell, H: iconCell})
		}
		col++
		if col >= cols {
			col = 0
			row++
		}
	}

	if a.previewBase != "" {
		a.drawSpritePreview(w, h)
		if c.clicked {
			a.previewBase = ""
		}
	}
}

func (a *App) drawIniswapCell(idx int, cell sdl.Rect) {
	c := a.ctx
	name := a.iniList[idx]
	c.Fill(cell, ColBackground)
	base := a.urls.CharIcon(name)
	if page, ok := a.cachedPage(&a.iniPages, &a.iniPagesGen, len(a.iniList), idx, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &cell)
	} else {
		a.demandAsset(&a.iniAsk, len(a.iniList), idx, base, assets.AssetTypeCharIcon) // AssetType: CharIcon (wardrobe)
		initial := name
		if len(initial) > 2 {
			initial = initial[:2]
		}
		c.Label(cell.X+iconCell/2-8, cell.Y+iconCell/2-8, initial, ColTextDim)
	}
	c.LabelClipped(cell.X, cell.Y+iconCell+1, iconCell, name, ColTextDim)

	// Wardrobe star (top-right of the cell): toggle membership without
	// wearing — the favourites list itself, exactly like lobby stars.
	star := sdl.Rect{X: cell.X + cell.W - 18, Y: cell.Y + 1, W: 17, H: 17}
	starCol := ColTextDim
	if idx < len(a.iniWardrobe) && a.iniWardrobe[idx] {
		starCol = ColStar
	}
	c.Label(star.X+2, star.Y, "★", starCol)
	if c.hovering(star) && c.clicked {
		if a.iniWardrobe[idx] {
			a.d.Prefs.RemoveWardrobe(name)
		} else {
			a.d.Prefs.AddWardrobe(name)
		}
		a.rebuildIniMenu()
		return // membership toggled; don't also wear it
	}

	if c.HoverPreview("iniswap:"+name, cell) {
		a.previewBase = a.urls.Emote(name, "normal", courtroom.EmoteIdle)
		a.d.Manager.PrefetchWithFallback(a.previewBase, a.urls.EmoteBare(name, "normal"), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
	}
	if c.hovering(cell) {
		a.warmCharINI(name) // wearing it = memory hit, not an RTT
		if c.clicked {
			a.wearFromMenu(name) // courtroom: instant swap; char select: claim a slot first
		}
	}
}

func (a *App) drawMusicList(r sdl.Rect) {
	c := a.ctx
	if c.Button(sdl.Rect{X: r.X, Y: r.Y, W: 90, H: 24}, "Stop music") {
		a.stopMusic()
	}
	r.Y += 28
	r.H -= 28
	if !c.ctrlHeld { // ctrl+wheel resizes text, never scrolls
		a.musicScroll -= c.wheelY * scrollStepPx
	}
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 10
	contentH := int32(len(a.sess.Music)) * lineH
	bar := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.musicScroll = c.VScrollbar("musicscroll", bar, a.musicScroll, contentH, r.H)
	y := r.Y - a.musicScroll
	for _, track := range a.sess.Music {
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-lineH {
			row := sdl.Rect{X: r.X, Y: y, W: r.W - scrollBarW - scrollBarGap, H: lineH - 4}
			hover := c.hovering(row)
			if hover {
				c.Fill(row, ColPanelHi)
			}
			c.LabelClippedFont(font, r.X+4, y+4, row.W-8, track, ColText)
			if hover && c.clicked {
				a.sess.RequestMusic(track)
			}
		}
		y += lineH
	}
}

// scaleControl draws one "Name − +" layout knob; steps mutate *value
// within [min, max] and persist the layout. Returns the next x.
func (a *App) scaleControl(x, y int32, name string, value *int, step, min, max int) int32 {
	c := a.ctx
	x += c.Label(x, y+6, name, ColTextDim) + 4
	if c.Button(sdl.Rect{X: x, Y: y, W: 22, H: btnH}, "-") && *value-step >= min {
		*value -= step
		a.saveLayout()
	}
	x += 24
	if c.Button(sdl.Rect{X: x, Y: y, W: 22, H: btnH}, "+") && *value+step <= max {
		*value += step
		a.saveLayout()
	}
	return x + 30
}

func (a *App) drawICControls(w, h int32, vp sdl.Rect) {
	c := a.ctx
	y := vp.Y + vp.H + pad

	// Row 1: shouts, pairing, and the live layout knobs (both hideable).
	x := pad
	var pendingShout int
	if !a.panelHidden(panelShouts) {
		shoutW := int32(96)
		shouts := []struct {
			label string
			mod   int
		}{{"Hold It!", protocol.ShoutHoldIt}, {"Objection!", protocol.ShoutObjection}, {"Take That!", protocol.ShoutTakeThat}}
		for _, s := range shouts {
			if c.Button(sdl.Rect{X: x, Y: y, W: shoutW, H: btnH}, s.label) {
				pendingShout = s.mod
			}
			x += shoutW + 6
		}
		// Custom interjection (2.10): the button fires the active pick;
		// the ▾ cycler steps base custom → each named [Shouts] entry.
		if a.sess.Features.Has(protocol.FeatureCustomObjections) {
			label := a.customShoutLabel()
			bw := c.TextWidth(label) + 16
			if c.Button(sdl.Rect{X: x, Y: y, W: bw, H: btnH}, label) {
				pendingShout = protocol.ShoutCustom
			}
			x += bw + 4
			if len(a.customShouts) > 0 {
				if c.Button(sdl.Rect{X: x, Y: y, W: 26, H: btnH}, "▾") {
					// −1 (base) → 0 → … → len−1 → −1
					a.customIdx++
					if a.customIdx >= len(a.customShouts) {
						a.customIdx = -1
					}
				}
				x += 32
			}
		}
	}
	if c.Button(sdl.Rect{X: x, Y: y, W: 70, H: btnH}, "Pair...") {
		a.showPair = !a.showPair
	}
	x += 80
	if !a.panelHidden(panelKnobs) {
		x = a.scaleControl(x, y, "View", &a.vpPct, config.ViewportStepPercent, config.MinViewportPercent, config.MaxViewportPercent)
		x = a.scaleControl(x, y, "Text", &a.chatPct, config.ScaleStepPercent, config.MinChatScalePercent, config.MaxChatScalePercent)
		x = a.scaleControl(x, y, "MsgBox", &a.boxPct, config.ScaleStepPercent, config.MinChatBoxPercent, config.MaxChatBoxPercent)
		x = a.scaleControl(x, y, "Log", &a.logPct, config.ScaleStepPercent, config.MinLogScalePercent, config.MaxLogScalePercent)
		_ = a.scaleControl(x, y, "Input", &a.inputPct, config.ScaleStepPercent, config.MinInputPercent, config.MaxInputPercent)
	}

	// Row 2: utility buttons (their own row so nothing overlaps at any
	// viewport scale or window width).
	y2 := y + btnH + 4
	x = pad
	if c.Button(sdl.Rect{X: x, Y: y2, W: 100, H: btnH}, "Character") {
		// Back to char select; the session stays, the server re-picks via
		// CC → PV and EventCharPicked rebuilds the courtroom.
		a.screen = ScreenCharSelect
	}
	x += 106
	if c.Button(sdl.Rect{X: x, Y: y2, W: 90, H: btnH}, "Wardrobe") {
		a.openIniswap()
	}
	x += 96
	if c.Button(sdl.Rect{X: x, Y: y2, W: 90, H: btnH}, "Settings") {
		a.prevScreen = ScreenCourtroom
		a.screen = ScreenSettings
	}
	x += 96
	if c.Button(sdl.Rect{X: x, Y: y2, W: 80, H: btnH}, "About") {
		a.prevScreen = ScreenCourtroom
		a.screen = ScreenAbout
	}
	x += 86
	if c.Button(sdl.Rect{X: x, Y: y2, W: 110, H: btnH}, "Disconnect") {
		a.Disconnect()
		return
	}
	x += 116
	evLabel := "Evidence"
	if a.evidPresent {
		evLabel = "Evidence ●" // armed: next IC message presents it
	}
	if c.Button(sdl.Rect{X: x, Y: y2, W: 100, H: btnH}, evLabel) {
		a.showEvid = true
	}
	x += 106
	if c.Button(sdl.Rect{X: x, Y: y2, W: 80, H: btnH}, "Mods...") {
		a.showModcall = true
	}
	x += 86
	if c.Button(sdl.Rect{X: x, Y: y2, W: 50, H: btnH}, "UI...") {
		a.showUICfg = true
	}
	x += 56
	_ = a.drawPosCycler(x, y2)

	// Judge strip (JD grant, or the judge stand when pos-dependent).
	icY := y2 + btnH + 6
	if a.judgeVisible() {
		icY += a.drawJudgeRow(pad, icY)
	}

	// IC input row (height follows the Box knob), led by the AO2 text
	// color cycler: the swatch shows the active wire color (MS text_color
	// 0–9); left-click next, right-click previous. The showname box
	// OVERRIDES the Settings showname for the session (blank = saved).
	fH := a.inputFieldH()
	swatch := sdl.Rect{X: pad, Y: icY, W: 26, H: fH}
	c.Fill(swatch, render.TextColor(a.icColor))
	c.Border(swatch, ColPanelHi)
	if c.hovering(swatch) {
		if c.clicked {
			a.icColor = (a.icColor + 1) % render.TextColorCount
		} else if c.rightClicked {
			a.icColor = (a.icColor + render.TextColorCount - 1) % render.TextColorCount
		}
	}
	const shownameBoxW = 140
	namePlaceholder := a.d.Prefs.SavedShowname()
	if namePlaceholder == "" {
		namePlaceholder = "Showname"
	}
	a.shownameOverride, _ = c.TextField("icshownameov", sdl.Rect{X: pad + 32, Y: icY, W: shownameBoxW, H: fH}, a.shownameOverride, namePlaceholder)
	var send bool
	a.icInput, send = c.TextField("ic", sdl.Rect{X: pad + 32 + shownameBoxW + 6, Y: icY, W: vp.W - 32 - shownameBoxW - 6, H: fH}, a.icInput, "Say something in character... (/pair <id>, /unpair, /offset <x> [y], /pos <side>)")
	if send || pendingShout != 0 {
		a.sendIC(pendingShout)
	}

	// Emote row.
	emoteY := icY + fH + 6
	if !a.panelHidden(panelEmotes) {
		a.drawEmoteRow(sdl.Rect{X: pad, Y: emoteY, W: w - 2*pad, H: h - emoteY - 30}, vp)
	}

	// OOC row at the very bottom: name + a FULL-width input (the squished
	// half-width box is gone — history lives in the OOC tab now).
	oocY := h - fH - 4
	if !a.panelHidden(panelOOC) {
		nameW := int32(120)
		prevOOC := a.oocName
		a.oocName, _ = c.TextField("oocname", sdl.Rect{X: pad, Y: oocY, W: nameW, H: fH}, a.oocName, "OOC name")
		if a.oocName != prevOOC {
			a.d.Prefs.SetOOCName(a.oocName) // permanent OOC name
		}
		var sendOOC bool
		a.oocInput, sendOOC = c.TextField("ooc", sdl.Rect{X: pad + nameW + 6, Y: oocY, W: w - nameW - 3*pad - 6, H: fH}, a.oocInput, "OOC chat... (full history in the OOC tab)")
		if sendOOC && strings.TrimSpace(a.oocInput) != "" {
			a.sess.SendOOC(a.oocName, a.oocInput)
			a.oocInput = ""
		}
		// Ctrl+wheel over the OOC row: same log/OOC text-size shortcut.
		if c.ctrlHeld && c.wheelY != 0 && c.hovering(sdl.Rect{X: pad, Y: oocY, W: w - 2*pad, H: fH}) {
			a.logPct = clampInt(a.logPct+int(c.wheelY)*config.ScaleStepPercent,
				config.MinLogScalePercent, config.MaxLogScalePercent)
			a.saveLayout()
		}
	}
	// Missing-asset warning (spec §4: visible in-client, names what was
	// tried so "enable fallbacks" is an informed fix, not a guess).
	if a.warnActive() {
		c.LabelClipped(pad, oocY-20, w-2*pad, a.warnLine, ColDanger)
	}

	if a.showPair {
		a.drawPairPanel(w, h)
	}
}

func (a *App) drawEmoteRow(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	if a.charINIBusy {
		c.Label(r.X, r.Y, "Loading emotes...", ColTextDim)
		return
	}
	x, y := r.X, r.Y
	me := a.activeCharName() // iniswap override drives emotes + buttons
	useImages := a.d.Prefs.EmoteButtonImagesEnabled()
	for i := range a.emotes {
		e := &a.emotes[i]
		label := e.Comment
		if label == "" {
			label = e.Anim
		}
		bw, bh := emoteBtnCell, emoteBtnCell
		if !useImages {
			bw, bh = c.TextWidth(label)+18, btnH
		}
		if x+bw > r.X+r.W {
			x = r.X
			y += bh + 4
			if y > r.Y+r.H-bh {
				break
			}
		}
		btn := sdl.Rect{X: x, Y: y, W: bw, H: bh}
		selected := i == a.emoteIdx
		if selected {
			c.Fill(sdl.Rect{X: btn.X - 2, Y: btn.Y - 2, W: btn.W + 4, H: btn.H + 4}, ColAccent)
		}
		var picked bool
		if useImages {
			picked = a.drawEmoteImageButton(btn, me, i, selected, label)
		} else {
			picked = c.Button(btn, label)
		}
		if picked {
			a.emoteIdx = i
			// Pressed art for the new selection, before next frame draws it.
			a.d.Manager.Prefetch(a.urls.EmoteButton(me, i+1, true), assets.AssetTypeEmoteButton, network.PriorityHigh) // AssetType: EmoteButton (selected)
			// Typing-driven speculation: the pick is the strongest signal
			// of what the next outgoing message needs — warm it all now.
			a.speculateEmote(me, e)
		}
		// Full-size preview after a 3 s hover (right-click = instant): the
		// TALKING sprite — what actually plays when this emote is sent.
		if c.HoverPreview("emote:"+e.Anim, btn) {
			a.previewBase = a.urls.Emote(me, e.Anim, courtroom.EmoteTalk)
			a.d.Manager.PrefetchWithFallback(a.previewBase, a.urls.EmoteBare(me, e.Anim), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
		}
		x += bw + 6
	}
	if a.previewBase != "" {
		a.drawSpritePreview(vp.X+vp.W, vp.Y+vp.H)
		if c.clicked {
			a.previewBase = ""
		}
	}
}

// drawEmoteImageButton draws one emotions/button<N> cell, preferring the
// state-correct art and falling back to the _off art (selection ring still
// reads) and finally a text chip while textures stream in. Reports clicks.
func (a *App) drawEmoteImageButton(btn sdl.Rect, me string, i int, selected bool, label string) bool {
	c := a.ctx
	base := a.urls.EmoteButton(me, i+1, selected)
	page, ok := a.d.Store.Get(base)
	if !ok {
		a.demandAsset(&a.emoteAsk, len(a.emotes), i, base, assets.AssetTypeEmoteButton) // AssetType: EmoteButton
		if selected {
			page, ok = a.d.Store.Get(a.urls.EmoteButton(me, i+1, false))
		}
	}
	if ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &btn)
	} else {
		c.Fill(btn, ColPanel)
		c.Border(btn, ColPanelHi)
		c.LabelClipped(btn.X+3, btn.Y+btn.H/2-8, btn.W-6, label, ColTextDim)
	}
	return c.hovering(btn) && c.clicked
}

// drawPairPanel: partner picking is a searchable click-to-pick list (the
// old one-by-one </> cycle was unusable on 4000-char servers); offsets,
// flip and z-order live in the right column.
func (a *App) drawPairPanel(w, h int32) {
	c := a.ctx
	ph := h - 120
	if ph > 540 {
		ph = 540
	}
	if ph < 320 {
		ph = 320
	}
	panel := sdl.Rect{X: w/2 - 290, Y: h/2 - ph/2, W: 580, H: ph}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+8, "Pairing", ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 90 - pad, Y: panel.Y + 8, W: 90, H: btnH}, "Close") {
		a.showPair = false
		return
	}

	// Left: searchable partner list.
	listW := panel.W/2 - pad*2
	y := panel.Y + 44
	c.LabelClipped(panel.X+pad, y, listW, "Partner: "+a.pairLabel(), ColAccent)
	y += 24
	a.pairSearch, _ = c.TextField("pairsearch", sdl.Rect{X: panel.X + pad, Y: y, W: listW - 80, H: fieldH}, a.pairSearch, "Search...")
	if c.Button(sdl.Rect{X: panel.X + pad + listW - 74, Y: y, W: 74, H: btnH}, "Unpair") {
		a.pairWith = protocol.UnpairedCharID
	}
	y += fieldH + 8

	a.ensureCharLower()
	query := strings.ToLower(strings.TrimSpace(a.pairSearch))
	lineH := int32(22)
	listTop := y
	listH := panel.Y + panel.H - listTop - pad
	matches := int32(0)
	for i := range a.sess.Chars {
		if i != a.sess.MyCharID && (query == "" || strings.Contains(a.charLower[i], query)) {
			matches++
		}
	}
	if c.hovering(sdl.Rect{X: panel.X + pad, Y: listTop, W: listW, H: listH}) {
		a.pairScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: panel.X + pad + listW - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
	a.pairScroll = c.VScrollbar("pairscroll", track, a.pairScroll, matches*lineH, listH)
	rowY := listTop - a.pairScroll
	for i := range a.sess.Chars {
		if i == a.sess.MyCharID { // can't pair with yourself
			continue
		}
		if query != "" && !strings.Contains(a.charLower[i], query) {
			continue
		}
		if rowY > listTop+listH-lineH {
			break
		}
		if rowY >= listTop-lineH {
			row := sdl.Rect{X: panel.X + pad, Y: rowY, W: listW - scrollBarW - scrollBarGap, H: lineH - 2}
			hover := c.hovering(row)
			if i == a.pairWith {
				c.Fill(row, ColAccent)
			} else if hover {
				c.Fill(row, ColPanelHi)
			}
			c.LabelClipped(row.X+4, rowY+3, row.W-8, a.sess.Chars[i].Name, ColText)
			if hover && c.clicked {
				a.pairWith = i
			}
		}
		rowY += lineH
	}

	// Right: placement controls (type the number, nudge with −/+, or
	// mousewheel over the row — all three work).
	rx := panel.X + panel.W/2 + pad
	ry := panel.Y + 44
	if next := a.offsetControl("pairoffx", rx, ry, "Offset X %", a.pairOffX, &a.pairOffXText); next != a.pairOffX {
		a.pairOffX = next
		a.persistPairPrefs()
	}
	ry += 34
	if next := a.offsetControl("pairoffy", rx, ry, "Offset Y %", a.pairOffY, &a.pairOffYText); next != a.pairOffY {
		a.pairOffY = next
		a.persistPairPrefs()
	}
	ry += 34
	a.pairFlip = c.Checkbox(rx, ry, "Flip my sprite", a.pairFlip)
	ry += 28
	// Explicit two-state order toggle — an unchecked box read as "???";
	// the button always names the CURRENT state, click to flip.
	orderLabel := "Order: In front"
	if a.pairOrder != protocol.PairSpeakerInFront {
		orderLabel = "Order: Behind"
	}
	if c.Button(sdl.Rect{X: rx, Y: ry, W: 170, H: btnH}, orderLabel) {
		if a.pairOrder == protocol.PairSpeakerInFront {
			a.pairOrder = protocol.PairSpeakerBehind
		} else {
			a.pairOrder = protocol.PairSpeakerInFront
		}
	}
	ry += 36
	c.Label(rx, ry, "Both sides must pair with", ColTextDim)
	c.Label(rx, ry+18, "each other; applies from", ColTextDim)
	c.Label(rx, ry+36, "your next message.", ColTextDim)
}

const offsetStep = 5

// offsetControl draws one pair-offset row: a typed field (text buffer
// commits only on a valid parse, so partial input like "-" survives
// typing), −/+ nudge buttons, and mousewheel over the row. Returns the
// possibly-updated value; the caller persists on change.
func (a *App) offsetControl(id string, x, y int32, label string, val int, buf *string) int {
	c := a.ctx
	c.Label(x, y+4, label, ColText)
	const fieldW = 56
	fx := x + 86
	if c.focusID != id {
		*buf = strconv.Itoa(val) // not editing: mirror the canonical value
	}
	next, _ := c.TextField(id, sdl.Rect{X: fx, Y: y, W: fieldW, H: fieldH}, *buf, "0")
	if next != *buf {
		*buf = next
		if n, err := strconv.Atoi(strings.TrimSpace(next)); err == nil {
			val = clampOffset(n)
		}
	}
	bx := fx + fieldW + 6
	if c.Button(sdl.Rect{X: bx, Y: y, W: 24, H: 24}, "-") {
		val = clampOffset(val - offsetStep)
	}
	if c.Button(sdl.Rect{X: bx + 28, Y: y, W: 24, H: 24}, "+") {
		val = clampOffset(val + offsetStep)
	}
	row := sdl.Rect{X: x, Y: y, W: bx + 52 - x, H: 26}
	if c.hovering(row) && c.wheelY != 0 {
		val = clampOffset(val + int(c.wheelY)*offsetStep)
	}
	return val
}

func clampOffset(v int) int {
	if v < -100 {
		return -100
	}
	if v > 100 {
		return 100
	}
	return v
}

func (a *App) persistPairPrefs() {
	a.d.Prefs.SetPairOffsets(a.pairOffX, a.pairOffY)
	a.d.Prefs.SetPairFlipped(a.pairFlip)
}

func (a *App) pairLabel() string {
	if a.pairWith <= protocol.UnpairedCharID || a.sess == nil || a.pairWith >= len(a.sess.Chars) {
		return "(unpaired)"
	}
	return fmt.Sprintf("%d — %s", a.pairWith, a.sess.Chars[a.pairWith].Name)
}

// sendIC builds and sends the outgoing MS message (chat commands handled
// first: /pair, /unpair, /offset — AO2-Client parity).
func (a *App) sendIC(shout int) {
	text := strings.TrimSpace(a.icInput)
	if cmdHandled := a.handleChatCommand(text); cmdHandled {
		a.icInput = ""
		return
	}
	if text == "" && shout == 0 {
		return
	}
	if a.sess.MyCharID < 0 {
		return
	}
	// AO2-Client chat_ratelimit parity: drop sends inside the window.
	if _, _, rateMs := a.d.Prefs.Timing(); rateMs > 0 &&
		time.Since(a.lastICSend) < time.Duration(rateMs)*time.Millisecond {
		return
	}
	emote := courtroom.Emote{Anim: "normal", Preanim: "-"}
	if a.emoteIdx >= 0 && a.emoteIdx < len(a.emotes) {
		emote = a.emotes[a.emoteIdx]
	}
	hasPre := emote.Preanim != "" && emote.Preanim != "-"
	deskMod := 1
	// Per-emote audio from char.ini ([SoundN]/[SoundT]/[SoundL]); "1" is
	// the AO wire value for silence (get_sfx_name's empty default).
	sfxName := emote.SFXName
	if sfxName == "" {
		sfxName = "1"
	}
	// Per-emote blip override (2.9.1 custom_blips), else the character's.
	blip := emote.Blip
	if blip == "" {
		blip = a.charBlips
	}
	out := protocol.OutgoingMS{
		DeskMod:    deskMod,
		PreEmote:   emote.Preanim,
		CharName:   a.activeCharName(), // iniswap: the wire carries the custom folder
		Emote:      emote.Anim,
		Message:    text,
		SFXName:    sfxName,
		SFXDelay:   emote.SFXDelay,
		LoopingSFX: emote.SFXLoop,
		Blipname:   blip,
		Side:       a.mySide(),
		// Never ship raw char.ini emote mods: legacy 2/3/4 values make
		// schema-strict clients drop the whole message.
		EmoteMod:  protocol.NormalizeOutgoingEmoteMod(emote.Mod, hasPre, false, a.sess.Features),
		CharID:    a.sess.MyCharID,
		Objection: shout,
		TextColor: a.icColor, // the swatch cycler (AO2 color dropdown parity)
		Showname:  a.effectiveShowname(),
		PairWith:  a.pairWith,
		PairOrder: a.pairOrder,
		OffsetX:   a.pairOffX,
		OffsetY:   a.pairOffY,
		Flip:      a.pairFlip,
	}
	// Named custom interjection (2.10): the wire carries "4&<stem>"
	// (formatObjection assembles it; courtroom.cpp:2142).
	if shout == protocol.ShoutCustom && a.customIdx >= 0 && a.customIdx < len(a.customShouts) {
		out.CustomShout = a.customShouts[a.customIdx].File
	}
	// Armed evidence rides this message; the wire shifts ids by 1 because
	// 0 means "no evidence" (legacy standard, courtroom.cpp:2160).
	if a.evidPresent && a.evidIdx >= 0 && a.evidIdx < len(a.sess.Evidence) {
		out.EvidenceID = a.evidIdx + 1
	}
	a.sess.SendChat(out)
	a.evidPresent = false // presenting is one-shot
	a.lastICSend = time.Now()
	a.icInput = ""
}

// areaStatusColor keys the area row color to the tsuserver status set.
func areaStatusColor(status string) sdl.Color {
	switch strings.ToUpper(status) {
	case "LOOKING-FOR-PLAYERS", "LFP":
		return sdl.Color{R: 110, G: 230, B: 110, A: 255} // recruiting: green
	case "CASING", "RP", "GAMING":
		return sdl.Color{R: 250, G: 190, B: 80, A: 255} // busy: amber
	case "RECESS":
		return sdl.Color{R: 120, G: 180, B: 255, A: 255} // paused: blue
	case "IDLE":
		return ColTextDim
	default:
		return ColText
	}
}

// mySide is OUR position: the char.ini side (or /pos override) — never
// the last speaker's position. Inheriting Scene.Position teleported us
// to whoever spoke last AND leaked custom side strings that strict
// receivers (LemmyAO's MS schema enumerates the eight standard sides)
// reject wholesale.
func (a *App) mySide() string {
	if a.sidePref != "" {
		return a.sidePref
	}
	return "wit" // the AO default
}

// handleChatCommand implements /pair <id>, /unpair, /offset <x> [y],
// /pos <side>.
func (a *App) handleChatCommand(text string) bool {
	switch {
	case strings.HasPrefix(text, "/pos "):
		if side := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(text, "/pos "))); side != "" {
			a.sidePref = side
		}
		return true
	case strings.HasPrefix(text, "/pair "):
		if id, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(text, "/pair "))); err == nil {
			a.pairWith = id
		}
		return true
	case text == "/unpair":
		a.pairWith = protocol.UnpairedCharID
		return true
	case strings.HasPrefix(text, "/offset "):
		parts := strings.Fields(strings.TrimPrefix(text, "/offset "))
		if len(parts) >= 1 {
			if x, err := strconv.Atoi(parts[0]); err == nil {
				a.pairOffX = clampOffset(x)
			}
		}
		if len(parts) >= 2 {
			if y, err := strconv.Atoi(parts[1]); err == nil {
				a.pairOffY = clampOffset(y)
			}
		}
		a.persistPairPrefs()
		return true
	}
	return false
}

// renderRaster rasterizes the current message with its AO color.
func renderRaster(a *App, sc *courtroom.Scene, wrapW int32, skinned bool) (*render.MessageRaster, error) {
	// The chat zoom font: rebuilt only when the Text knob changes. The
	// theme's "message" color replaces only AO's DEFAULT color (code 0)
	// — explicit message colors (green/red/...) always win — and only
	// while the theme's own chatbox skin is drawn: theme colors assume
	// their skin (black text on white paper), not our dark flat panel.
	col := render.TextColor(sc.TextColor)
	if skinned && sc.TextColor == 0 && a.themeHasMsg {
		col = a.themeMsgCol
	}
	return render.Rasterize(a.ctx.Ren, a.ctx.ChatFont(a.chatPct), sc.MessageText, wrapW, col)
}
