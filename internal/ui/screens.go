package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

const (
	pad        int32 = 10
	rowH       int32 = 28
	fieldH     int32 = 26
	btnH       int32 = 28
	iconCell   int32 = 64
	iconGap    int32 = 8
	previewMax int32 = 360
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
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
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

	// Server rows.
	listTop := dcY + 40
	a.lobbyScroll -= c.wheelY * scrollStepPx
	if a.lobbyScroll < 0 {
		a.lobbyScroll = 0
	}
	y := listTop - a.lobbyScroll
	legacyHeaderDrawn := false
	for i := range a.servers {
		e := &a.servers[i]
		if !e.Joinable() && !legacyHeaderDrawn {
			if y > listTop-rowH && y < h {
				c.Label(pad, y+4, "— NOT SUPPORTED: "+network.UnsupportedReason, ColDanger)
			}
			y += rowH
			legacyHeaderDrawn = true
		}
		if y > h {
			break
		}
		if y > listTop-rowH {
			a.drawServerRow(e, y, w)
		}
		y += rowH
	}

	// Description of hovered/selected server.
	if a.selectedDesc != "" {
		c.LabelClipped(pad, h-24, w-2*pad, a.selectedDesc, ColTextDim)
	}
}

func (a *App) drawServerRow(e *network.ServerEntry, y, w int32) {
	c := a.ctx
	row := sdl.Rect{X: pad, Y: y, W: w - 2*pad, H: rowH - 2}
	hover := c.hovering(row)
	bg := ColPanel
	if hover {
		bg = ColPanelHi
		a.selectedDesc = e.Description
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

	if e.Joinable() {
		if c.Button(sdl.Rect{X: row.X + row.W - 80, Y: y + 1, W: 76, H: rowH - 4}, "Join") {
			a.Connect(e.Name, e.WebSocketURL())
		}
	}
}

// maybeRequestCharIcon demands a visible icon whose texture isn't resident:
// at most charIconAskPerFrame submissions per frame, one ask per icon per
// charIconRetryInterval. Low priority keeps the render thread non-blocking
// (the high lane can stall producers); the retry cadence self-heals when
// the low lane sheds a burst, since shed jobs are never re-run by the pool.
func (a *App) maybeRequestCharIcon(idx int, base string) {
	if a.iconAskBudget <= 0 || a.sess == nil || idx < 0 || idx >= len(a.sess.Chars) {
		return
	}
	// Char list reloads (SI wipes + SC rebuilds) re-size the stamp table.
	if len(a.iconAsk) != len(a.sess.Chars) {
		a.iconAsk = make([]time.Time, len(a.sess.Chars))
	}
	now := time.Now()
	if now.Sub(a.iconAsk[idx]) < charIconRetryInterval {
		return
	}
	a.iconAsk[idx] = now
	a.iconAskBudget--
	a.d.Manager.Prefetch(base, assets.AssetTypeCharIcon, network.PriorityLow) // AssetType: CharIcon
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
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
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

	a.charSearch, _ = c.TextField("charsearch", sdl.Rect{X: pad, Y: pad + 36, W: 260, H: fieldH}, a.charSearch, "Search characters...")
	if c.Button(sdl.Rect{X: pad + 270, Y: pad + 36, W: 90, H: btnH}, "Spectate") {
		a.sess.PickCharacter(protocol.UnpairedCharID)
		a.enterCourtroom()
		return
	}
	if a.room != nil {
		// Re-picking from the courtroom ("Change character"): allow backing
		// out without dropping the session.
		if c.Button(sdl.Rect{X: pad + 366, Y: pad + 36, W: 90, H: btnH}, "Back") {
			a.screen = ScreenCourtroom
			return
		}
	}
	if a.warnActive() {
		c.LabelClipped(pad+470, pad+42, w-pad-470-130, a.warnLine, ColDanger)
	}

	gridTop := pad + 76
	gridW := w - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (iconCell + iconGap)
	if cols < 1 {
		cols = 1
	}

	query := strings.ToLower(strings.TrimSpace(a.charSearch))
	// Pre-count matches so the scrollbar knows the content height; the
	// draw loop below walks the same list anyway.
	matches := int32(0)
	for i := range a.sess.Chars {
		if query == "" || strings.Contains(strings.ToLower(a.sess.Chars[i].Name), query) {
			matches++
		}
	}
	cellH := iconCell + iconGap + 14
	contentH := (matches + cols - 1) / cols * cellH
	visibleH := h - gridTop - pad

	a.charScroll -= c.wheelY * scrollStepPx
	track := sdl.Rect{X: w - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.charScroll = c.VScrollbar("charscroll", track, a.charScroll, contentH, visibleH)

	col, row := int32(0), int32(0)
	previewRequested := false
	for i := range a.sess.Chars {
		slot := &a.sess.Chars[i]
		if query != "" && !strings.Contains(strings.ToLower(slot.Name), query) {
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
	if page, ok := a.d.Store.Get(base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &cell)
	} else {
		// Not resident: demand it (visible = not speculation) and draw the
		// initials placeholder; the texture pops in live.
		a.maybeRequestCharIcon(idx, base)
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
	if c.hovering(cell) && c.clicked && !slot.Taken {
		a.sess.PickCharacter(idx)
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

	// Viewport: AO 4:3, scaled to fit the left column.
	vpW := w * 2 / 3
	vpH := vpW * 3 / 4
	if vpH > h-220 {
		vpH = h - 220
		vpW = vpH * 4 / 3
	}
	vp := sdl.Rect{X: pad, Y: pad, W: vpW, H: vpH}
	c.Fill(vp, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	a.d.Viewport.Render(c.Ren, &a.room.Scene, vp)
	a.drawChatOverlay(vp)

	// Right column: log + music.
	rx := vp.X + vp.W + pad
	rw := w - rx - pad
	a.drawLogPanel(sdl.Rect{X: rx, Y: pad, W: rw, H: vpH}, vp)

	// Bottom: IC input, emotes, controls.
	a.drawICControls(w, h, vp)
}

func (a *App) drawChatOverlay(vp sdl.Rect) {
	c := a.ctx
	sc := &a.room.Scene
	if sc.MessageText == "" && sc.ShownameText == "" {
		return
	}
	boxH := vp.H / 4
	box := sdl.Rect{X: vp.X, Y: vp.Y + vp.H - boxH, W: vp.W, H: boxH}
	c.Fill(box, sdl.Color{R: 16, G: 16, B: 24, A: 215})
	c.Border(box, ColAccent)
	c.Label(box.X+8, box.Y+4, sc.ShownameText, ColAccent)

	// (Re)rasterize when the message or color changes.
	if a.msRaster == nil || a.rasterText != sc.MessageText || a.rasterColor != sc.TextColor {
		if a.msRaster != nil {
			a.msRaster.Destroy()
			a.msRaster = nil
		}
		if sc.MessageText != "" {
			raster, err := renderRaster(a, sc, box.W-16)
			if err == nil {
				a.msRaster = raster
				a.rasterText = sc.MessageText
				a.rasterColor = sc.TextColor
			}
		}
	}
	if a.msRaster != nil {
		a.msRaster.Draw(c.Ren, sc.VisibleRunes, box.X+8, box.Y+26)
	}
}

func (a *App) drawLogPanel(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanel)
	c.Border(r, ColPanelHi)
	tab := r.W / 3
	if c.Button(sdl.Rect{X: r.X, Y: r.Y, W: tab, H: btnH}, "Log") {
		a.logTab = logTabLog
	}
	if c.Button(sdl.Rect{X: r.X + tab, Y: r.Y, W: tab, H: btnH}, "Music") {
		a.logTab = logTabMusic
	}
	if c.Button(sdl.Rect{X: r.X + 2*tab, Y: r.Y, W: r.W - 2*tab, H: btnH}, "Areas") {
		a.logTab = logTabAreas
	}
	inner := sdl.Rect{X: r.X + 4, Y: r.Y + btnH + 4, W: r.W - 8, H: r.H - btnH - 8}
	switch a.logTab {
	case logTabMusic:
		a.drawMusicList(inner)
		return
	case logTabAreas:
		a.drawAreaList(inner)
		return
	}
	lineH := int32(a.ctx.font.Height()) + 2
	maxLines := int(inner.H / lineH)
	start := 0
	if len(a.icLog) > maxLines {
		start = len(a.icLog) - maxLines
	}
	y := inner.Y
	for _, line := range a.icLog[start:] {
		c.LabelClipped(inner.X, y, inner.W, line, ColText)
		y += lineH
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
	a.areaScroll -= c.wheelY * scrollStepPx
	lineH := rowH
	contentH := int32(len(a.sess.Areas)) * lineH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.areaScroll = c.VScrollbar("areascroll", track, a.areaScroll, contentH, r.H)
	y := r.Y - a.areaScroll
	for _, area := range a.sess.Areas {
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-lineH {
			row := sdl.Rect{X: r.X, Y: y, W: r.W - scrollBarW - scrollBarGap, H: lineH - 4}
			hover := c.hovering(row)
			if hover {
				c.Fill(row, ColPanelHi)
			}
			c.LabelClipped(r.X+4, y+4, row.W-8, area, ColText)
			if hover && c.clicked {
				a.sess.RequestMusic(area) // area transfer rides MC
			}
		}
		y += lineH
	}
}

func (a *App) drawMusicList(r sdl.Rect) {
	c := a.ctx
	a.musicScroll -= c.wheelY * scrollStepPx
	lineH := rowH
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
			c.LabelClipped(r.X+4, y+4, row.W-8, track, ColText)
			if hover && c.clicked {
				a.sess.RequestMusic(track)
			}
		}
		y += lineH
	}
}

func (a *App) drawICControls(w, h int32, vp sdl.Rect) {
	c := a.ctx
	y := vp.Y + vp.H + pad

	// Shout buttons.
	shoutW := int32(96)
	shouts := []struct {
		label string
		mod   int
	}{{"Hold It!", protocol.ShoutHoldIt}, {"Objection!", protocol.ShoutObjection}, {"Take That!", protocol.ShoutTakeThat}}
	x := pad
	var pendingShout int
	for _, s := range shouts {
		if c.Button(sdl.Rect{X: x, Y: y, W: shoutW, H: btnH}, s.label) {
			pendingShout = s.mod
		}
		x += shoutW + 6
	}
	if c.Button(sdl.Rect{X: x, Y: y, W: 70, H: btnH}, "Pair...") {
		a.showPair = !a.showPair
	}
	x += 76
	if c.Button(sdl.Rect{X: x, Y: y, W: 100, H: btnH}, "Character") {
		// Back to char select; the session stays, the server re-picks via
		// CC → PV and EventCharPicked rebuilds the courtroom.
		a.screen = ScreenCharSelect
	}
	x += 106
	if c.Button(sdl.Rect{X: x, Y: y, W: 90, H: btnH}, "Settings") {
		a.prevScreen = ScreenCourtroom
		a.screen = ScreenSettings
	}
	x += 96
	if c.Button(sdl.Rect{X: x, Y: y, W: 80, H: btnH}, "About") {
		a.prevScreen = ScreenCourtroom
		a.screen = ScreenAbout
	}
	x += 86
	if c.Button(sdl.Rect{X: x, Y: y, W: 110, H: btnH}, "Disconnect") {
		a.Disconnect()
		return
	}

	// IC input row.
	icY := y + btnH + 6
	var send bool
	a.icInput, send = c.TextField("ic", sdl.Rect{X: pad, Y: icY, W: vp.W - 0, H: fieldH}, a.icInput, "Say something in character... (/pair <id>, /unpair, /offset <x> [y])")
	if send || pendingShout != 0 {
		a.sendIC(pendingShout)
	}

	// Emote row.
	emoteY := icY + fieldH + 6
	a.drawEmoteRow(sdl.Rect{X: pad, Y: emoteY, W: w - 2*pad, H: h - emoteY - 30}, vp)

	// OOC row at the very bottom.
	oocY := h - fieldH - 4
	nameW := int32(140)
	a.oocName, _ = c.TextField("oocname", sdl.Rect{X: pad, Y: oocY, W: nameW, H: fieldH}, a.oocName, "OOC name")
	var sendOOC bool
	a.oocInput, sendOOC = c.TextField("ooc", sdl.Rect{X: pad + nameW + 6, Y: oocY, W: w/2 - nameW - pad - 12, H: fieldH}, a.oocInput, "OOC chat...")
	if sendOOC && strings.TrimSpace(a.oocInput) != "" {
		a.sess.SendOOC(a.oocName, a.oocInput)
		a.oocInput = ""
	}
	// OOC log line.
	if len(a.oocLog) > 0 {
		c.LabelClipped(pad+w/2, oocY+4, w/2-2*pad, a.oocLog[len(a.oocLog)-1], ColTextDim)
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
	me := a.myCharName()
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
		a.maybeRequestEmoteButton(i, base)
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

// maybeRequestEmoteButton demands missing button art exactly like
// maybeRequestCharIcon: shared per-frame budget, one ask per button per
// charIconRetryInterval, low priority (sheddable; the cadence self-heals).
func (a *App) maybeRequestEmoteButton(idx int, base string) {
	if a.iconAskBudget <= 0 || idx < 0 || idx >= len(a.emotes) {
		return
	}
	if len(a.emoteAsk) != len(a.emotes) {
		a.emoteAsk = make([]time.Time, len(a.emotes))
	}
	now := time.Now()
	if now.Sub(a.emoteAsk[idx]) < charIconRetryInterval {
		return
	}
	a.emoteAsk[idx] = now
	a.iconAskBudget--
	a.d.Manager.Prefetch(base, assets.AssetTypeEmoteButton, network.PriorityLow) // AssetType: EmoteButton
}

func (a *App) drawPairPanel(w, h int32) {
	c := a.ctx
	panel := sdl.Rect{X: w/2 - 180, Y: h/2 - 140, W: 360, H: 280}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+8, "Pairing", ColText)

	y := panel.Y + 44
	c.Label(panel.X+pad, y, fmt.Sprintf("Partner: %s", a.pairLabel()), ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 70, Y: y - 4, W: 28, H: 24}, "<") {
		a.cyclePair(-1)
	}
	if c.Button(sdl.Rect{X: panel.X + panel.W - 38, Y: y - 4, W: 28, H: 24}, ">") {
		a.cyclePair(1)
	}

	y += 34
	c.Label(panel.X+pad, y, fmt.Sprintf("Offset X: %d%%", a.pairOffX), ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 70, Y: y - 4, W: 28, H: 24}, "-") {
		a.pairOffX = clampOffset(a.pairOffX - offsetStep)
		a.persistPairPrefs()
	}
	if c.Button(sdl.Rect{X: panel.X + panel.W - 38, Y: y - 4, W: 28, H: 24}, "+") {
		a.pairOffX = clampOffset(a.pairOffX + offsetStep)
		a.persistPairPrefs()
	}
	y += 34
	c.Label(panel.X+pad, y, fmt.Sprintf("Offset Y: %d%%", a.pairOffY), ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 70, Y: y - 4, W: 28, H: 24}, "-") {
		a.pairOffY = clampOffset(a.pairOffY - offsetStep)
		a.persistPairPrefs()
	}
	if c.Button(sdl.Rect{X: panel.X + panel.W - 38, Y: y - 4, W: 28, H: 24}, "+") {
		a.pairOffY = clampOffset(a.pairOffY + offsetStep)
		a.persistPairPrefs()
	}

	y += 34
	a.pairFlip = c.Checkbox(panel.X+pad, y, "Flip my sprite for the pair", a.pairFlip)
	y += 30
	front := a.pairOrder == protocol.PairSpeakerInFront
	front = c.Checkbox(panel.X+pad, y, "Render me in front", front)
	a.pairOrder = protocol.PairSpeakerInFront
	if !front {
		a.pairOrder = protocol.PairSpeakerBehind
	}

	y += 36
	c.Label(panel.X+pad, y, "Offsets apply from your next message.", ColTextDim)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 90 - pad, Y: panel.Y + panel.H - btnH - pad, W: 90, H: btnH}, "Close") {
		a.showPair = false
	}
}

const offsetStep = 5

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

func (a *App) cyclePair(dir int) {
	if a.sess == nil || len(a.sess.Chars) == 0 {
		return
	}
	next := a.pairWith + dir
	if next < protocol.UnpairedCharID {
		next = len(a.sess.Chars) - 1
	}
	if next >= len(a.sess.Chars) {
		next = protocol.UnpairedCharID
	}
	a.pairWith = next
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
	emote := courtroom.Emote{Anim: "normal", Preanim: "-"}
	if a.emoteIdx >= 0 && a.emoteIdx < len(a.emotes) {
		emote = a.emotes[a.emoteIdx]
	}
	deskMod := 1
	out := protocol.OutgoingMS{
		DeskMod:   deskMod,
		PreEmote:  emote.Preanim,
		CharName:  a.myCharName(),
		Emote:     emote.Anim,
		Message:   text,
		Side:      a.mySide(),
		EmoteMod:  emote.Mod,
		CharID:    a.sess.MyCharID,
		Objection: shout,
		Showname:  a.d.Prefs.SavedShowname(),
		PairWith:  a.pairWith,
		PairOrder: a.pairOrder,
		OffsetX:   a.pairOffX,
		OffsetY:   a.pairOffY,
		Flip:      a.pairFlip,
	}
	a.sess.SendChat(out)
	a.icInput = ""
}

func (a *App) mySide() string {
	if a.room != nil && a.room.Scene.Position != "" {
		return a.room.Scene.Position
	}
	return "wit"
}

// handleChatCommand implements /pair <id>, /unpair, /offset <x> [y].
func (a *App) handleChatCommand(text string) bool {
	switch {
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
func renderRaster(a *App, sc *courtroom.Scene, wrapW int32) (*render.MessageRaster, error) {
	return render.Rasterize(a.ctx.Ren, a.ctx.Font(), sc.MessageText, wrapW, render.TextColor(sc.TextColor))
}
