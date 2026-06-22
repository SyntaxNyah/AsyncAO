package ui

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	// previewZoomMax caps the preview magnifier (× the fit scale).
	previewZoomMax = 8
	// emoteBtnCell matches AO2's 40×40 emotions/button<N> art.
	emoteBtnCell int32 = 40
	// emoteTextCellW is the fixed cell width for text-mode emote chips, so
	// they page in the same uniform grid as image chips (labels clip).
	emoteTextCellW int32 = 104
	// emoteGridGap spaces the classic emote grid (both axes).
	emoteGridGap int32 = 6
	// scrollBarW/Gap reserve the scrollbar lane beside scrolling lists.
	scrollBarW   int32 = 12
	scrollBarGap int32 = 6
)

func osHostname() (string, error) { return os.Hostname() }

// --- LOBBY ------------------------------------------------------------------------

func (a *App) drawLobby(w, h int32) {
	a.pollLobbyFetch()
	a.pollPing() // drain connect-time probes (no-op unless a sweep is running)
	c := a.ctx
	a.drawScreenBackdrop(w, h, "lobbybackground")
	c.Heading(pad, pad, "AsyncAO — Server Phone Book & Lobby", ColText)
	c.Label(pad, pad+30, a.lobbyStatus, ColTextDim)
	if a.connErr != "" {
		c.Label(pad+220, pad+30, a.connErr, ColDanger)
		if a.lastConnURL != "" { // one-click Reconnect to the server we dropped from
			label := "Reconnect to " + a.lastConnName
			bw := c.TextWidth(label) + 20
			if c.Button(sdl.Rect{X: pad + 220, Y: pad + 50, W: bw, H: btnH}, label) {
				a.connErr = ""
				a.Connect(a.lastConnName, a.lastConnURL)
			}
		}
	}
	// M2 auto-reconnect status (when a retry is pending): the cached message
	// (rebuilt per attempt, not per frame) + a Stop button to bail out.
	if !a.autoReconnectAt.IsZero() {
		c.Label(pad+220, pad+78, a.autoReconnectMsg, ColAccent)
		sx := pad + 220 + c.TextWidth(a.autoReconnectMsg) + 12
		if c.Button(sdl.Rect{X: sx, Y: pad + 74, W: c.TextWidth("Stop") + 20, H: btnH}, "Stop") {
			a.cancelAutoReconnect()
		}
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
	// Connect-time ("ping") sort: probe joinable servers and sort by RTT. A
	// second press goes back to the player-count sort. Off until pressed.
	pingBtn := sdl.Rect{X: w - 470 - pad, Y: pad, W: 110, H: btnH}
	pingLabel := "Ping"
	if a.pinging {
		pingLabel = "Pinging…"
	} else if a.pingMode {
		pingLabel = "Ping ✓"
	}
	if c.Button(pingBtn, pingLabel) {
		if a.pingMode {
			a.pingMode = false
			a.applyServerSort() // back to player-count order
		} else {
			a.startPinging()
		}
	}
	c.Tooltip(pingBtn, "Sort by connect time (rough TCP latency, not ICMP ping) — press again for player-count order")
	// Phone Book page toggle: a dedicated view of just YOUR saved servers.
	pbBtn := sdl.Rect{X: w - 590 - pad, Y: pad, W: 110, H: btnH}
	pbLabel := "★ Phone Book"
	if a.phoneBookPage {
		pbLabel = "← All servers"
	}
	if c.Button(pbBtn, pbLabel) {
		a.phoneBookPage = !a.phoneBookPage
		a.selServer, a.descLines, a.lobbyScroll = -1, nil, 0
	}
	c.Tooltip(pbBtn, "Your manually-added servers — kept forever (until Wipe everything), exportable")

	dcY := pad + 56
	if a.phoneBookPage {
		a.drawPhoneBookBar(w, dcY)
	} else {
		// Direct connect row.
		c.Label(pad, dcY+4, "Direct connect (ip:port, url:port, ws:// or wss://):", ColText)
		fieldX := pad + c.TextWidth("Direct connect (ip:port, url:port, ws:// or wss://):") + 12
		a.directInput, _ = c.TextField("direct", sdl.Rect{X: fieldX, Y: dcY, W: 280, H: fieldH}, a.directInput, "127.0.0.1:50001")
		a.directSecure = c.Checkbox(fieldX+290, dcY+4, "TLS (wss)", a.directSecure)
		a.directSave = c.Checkbox(fieldX+390, dcY+4, "Save to phone book", a.directSave)
		if c.Button(sdl.Rect{X: fieldX + 560, Y: dcY, W: 100, H: btnH}, "Connect") {
			a.directConnect()
		}
	}

	// Server rows. Click once: expand the full description under the
	// row; click the selected row again: join (Join button still works).
	// Wheel scrolls only over the list itself (never the connect row).
	listTop := dcY + 40
	a.lobbyScroll -= c.WheelIn(sdl.Rect{X: 0, Y: listTop, W: w, H: h - listTop}) * scrollStepPx
	if a.lobbyScroll < 0 {
		a.lobbyScroll = 0
	}
	lineH := int32(c.font.Height()) + 3
	y := listTop - a.lobbyScroll
	legacyHeaderDrawn := false
	for i := range a.servers {
		e := &a.servers[i]
		if a.phoneBookPage && !e.Favorite {
			continue // Phone Book shows only your saved servers
		}
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
			boxH := int32(len(a.descLines)+len(a.descLinks))*lineH + btnH + 16
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
				// Rehearse, spelled out (the row button alone was missed
				// in playtests): offline browse of this server's cached
				// roster, or the one-visit explainer.
				e := &a.servers[i]
				if e.Joinable() {
					if c.Button(sdl.Rect{X: box.X + 6, Y: ly + 4, W: 320, H: btnH}, "Rehearse offline (browse cached characters)") {
						if info := a.d.Prefs.ServerWarmInfoFor(e.WebSocketURL()); info.Origin != "" && len(info.Chars) > 0 {
							a.startRehearsal(e.Name, e.WebSocketURL(), info)
							return
						}
						a.connErr = "Rehearsal needs one visit first: join " + e.Name +
							" once (this build) so its roster gets remembered."
					}
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

	// Right-side label: normally the security tier; in ping mode the connect
	// time takes this slot instead (the verbose tier text would run under the
	// number — the tier COLOUR still reads off the left swatch). "…" probing,
	// "unreachable" on a failed dial.
	if a.pingMode && e.Joinable() {
		label, col := "…", ColTextDim
		if d, ok := a.pings[e.WebSocketURL()]; ok {
			if d < 0 {
				label, col = "unreachable", ColDanger
			} else {
				label, col = fmt.Sprintf("%d ms", int(d/time.Millisecond)), ColText
			}
		}
		c.Label(row.X+row.W-260, y+5, label, col)
	} else {
		c.Label(row.X+row.W-260, y+5, e.Security().String(), tierColor(*e))
	}

	joinHover := false
	if e.Joinable() {
		joinBtn := sdl.Rect{X: row.X + row.W - 80, Y: y + 1, W: 76, H: rowH - 4}
		joinHover = c.hovering(joinBtn)
		if c.Button(joinBtn, "Join") {
			a.Connect(e.Name, e.WebSocketURL())
			return
		}
		// Rehearse (selected row): offline browse of the server's cached
		// assets. Always visible so the feature is discoverable — without
		// a remembered roster (recorded on each join) it explains itself
		// instead of silently not existing.
		if idx == a.selServer {
			info := a.d.Prefs.ServerWarmInfoFor(e.WebSocketURL())
			rehBtn := sdl.Rect{X: joinBtn.X - 92, Y: y + 1, W: 86, H: rowH - 4}
			if c.hovering(rehBtn) {
				joinHover = true // suppress the row's click-to-join
			}
			if c.Button(rehBtn, "Rehearse") {
				if info.Origin != "" && len(info.Chars) > 0 {
					a.startRehearsal(e.Name, e.WebSocketURL(), info)
					return
				}
				a.connErr = "Rehearsal needs one visit first: join " + e.Name +
					" once (this build) so its roster gets remembered, then Rehearse works offline."
			}
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
			c.Label(pad, gridTop+8, "Wardrobe empty — tap the ★ on any character in the Characters tab to save it here (it stays per server).", ColTextDim)
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
	a.iniScroll -= c.WheelIn(sdl.Rect{X: 0, Y: gridTop, W: w, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: w - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.iniScroll = c.VScrollbar("iniscroll", track, a.iniScroll, contentH, visibleH)

	col, row := int32(0), int32(0)
	// Clip to the grid viewport so scrolled cells slide under the fixed top bar
	// (search + tabs + buttons) instead of covering it — same fix as the
	// Characters tab.
	gridClip := sdl.Rect{X: 0, Y: gridTop, W: w, H: visibleH}
	_ = c.Ren.SetClipRect(&gridClip)
	for i := range a.iniList {
		if query != "" && !strings.Contains(a.iniLower[i], query) {
			continue
		}
		x := pad + col*(iconCell+iconGap)
		y := gridTop + row*cellH - a.iniScroll
		if y+iconCell > gridTop && y < gridTop+visibleH {
			// Char-select Wardrobe tab: a favourite SWITCHES to the real character
			// (wardrobeClick picks its free slot), not a blind iniswap.
			a.drawIniswapCell(i, sdl.Rect{X: x, Y: y, W: iconCell, H: iconCell}, cellClickChar)
		}
		col++
		if col >= cols {
			col = 0
			row++
		}
	}
	_ = c.Ren.SetClipRect(nil)
	if a.previewBase != "" {
		a.drawSpritePreview(w, h, false)
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
		a.requestDisconnect() // confirm first unless instant-disconnect is set
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
		if !a.sess.Rehearsal {
			a.sess.PickCharacter(protocol.UnpairedCharID)
		}
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
	query := a.charQ.get(a.charSearch)

	if a.charTab == charTabWardrobe {
		a.drawWardrobeGrid(w, h, gridTop, cols, cellH, visibleH, query)
		return
	}

	a.ensureCharLower()
	a.ensureWardrobeMembers() // star state for the grid; rebuilt only on change
	// Pre-count matches so the scrollbar knows the content height. With no
	// search every slot matches, so skip the scan (it's a per-frame O(n) walk
	// that bites on servers with thousands of characters); the draw loop below
	// still culls to the visible rows either way. Mirrors the bg picker.
	matches := int32(len(a.sess.Chars))
	if query != "" {
		matches = 0
		for i := range a.sess.Chars {
			if strings.Contains(a.charLower[i], query) {
				matches++
			}
		}
	}
	contentH := (matches + cols - 1) / cols * cellH

	a.charScroll -= c.WheelIn(sdl.Rect{X: 0, Y: gridTop, W: w, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: w - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.charScroll = c.VScrollbar("charscroll", track, a.charScroll, contentH, visibleH)

	dlOn := a.d.Prefs.CharDownloaderEnabled() // read once per frame, not per cell
	col, row := int32(0), int32(0)
	previewRequested := false
	// Clip the grid to its own viewport (below the top bar) so scrolled cells
	// slide UNDER the fixed search/tabs/buttons instead of painting over them: the
	// bar is drawn first, so without this the later cell draws covered it as you
	// scrolled down, and search became unreachable. Keeps the bar always usable.
	gridClip := sdl.Rect{X: 0, Y: gridTop, W: w, H: visibleH}
	_ = c.Ren.SetClipRect(&gridClip)
	for i := range a.sess.Chars {
		slot := &a.sess.Chars[i]
		if query != "" && !strings.Contains(a.charLower[i], query) {
			continue
		}
		x := pad + col*(iconCell+iconGap)
		y := gridTop + row*cellH - a.charScroll
		cell := sdl.Rect{X: x, Y: y, W: iconCell, H: iconCell}
		if y+iconCell > gridTop && y < gridTop+visibleH { // only rows touching the viewport (no draw/hover through the bar)
			a.drawCharCell(slot, cell, i, dlOn)
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
	_ = c.Ren.SetClipRect(nil)
	if !previewRequested && !c.rightClicked {
		// keep showing while hovered; HoverPreview clears hoverID on exit
	}
	if a.previewBase != "" {
		a.drawSpritePreview(w, h, false)
		if c.clicked || (a.ctx.hoverID == "" && !previewRequested) {
			a.previewBase = ""
		}
	}
}

func (a *App) drawCharCell(slot *courtroom.CharacterSlot, cell sdl.Rect, idx int, downloaderOn bool) {
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
	// While this character is the active download, mark the cell.
	if a.dl.active && a.dl.target == slot.Name {
		c.Fill(cell, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 70})
		c.Label(cell.X+4, cell.Y+4, downloadGlyph+"…", ColText)
	}
	// Download badge (only with the opt-in downloader on): grabs this
	// character's folder + the sfx/blips its char.ini names, for offline use.
	// Works on taken slots too.
	if downloaderOn && a.drawDownloadBadge(cell, "Press the green down arrow to download this character") {
		a.startCharDownload(slot.Name)
		return
	}
	// ★ Wardrobe star (top-right): one click favourites this character into your
	// Wardrobe (LemmyAO-style), so it appears in the Wardrobe tab on every
	// connect. The click is consumed so it can't also pick the character; works
	// on taken slots too. Membership is the lock-free cached set.
	starred := idx >= 0 && idx < len(a.charLower) && a.wardrobeMembers[a.charLower[idx]]
	starR := sdl.Rect{X: cell.X + cell.W - 20, Y: cell.Y + 2, W: 18, H: 18}
	c.Fill(starR, sdl.Color{R: 0, G: 0, B: 0, A: 130})
	starCol := ColTextDim
	if starred {
		starCol = ColStar
	}
	c.Label(starR.X+3, starR.Y+1, "★", starCol)
	if c.hovering(starR) {
		c.Tooltip(starR, "Star → save to your Wardrobe (favourites)")
		if c.clicked {
			if starred {
				a.d.Prefs.RemoveWardrobe(a.serverKey, slot.Name)
			} else {
				a.d.Prefs.AddWardrobe(a.serverKey, slot.Name)
			}
			c.clicked = false // consumed: don't also pick the character
			return
		}
	}
	if c.hovering(cell) {
		a.warmCharINI(slot.Name) // pick = memory hit, not an RTT
		if c.clicked && !slot.Taken {
			a.pickCharacter(idx) // rehearsal resolves locally
		}
	}
}

// randomChar swaps to a random AVAILABLE character (webAO's /randomchar): a
// uniformly-random untaken slot other than the current one, chosen by reservoir
// sampling so it never allocates. No-op with no free slot. Render thread only.
func (a *App) randomChar() {
	if a.sess == nil || len(a.sess.Chars) == 0 {
		return
	}
	cur := a.sess.MyCharID
	chosen, seen := -1, 0
	for i := range a.sess.Chars {
		if i == cur || a.sess.Chars[i].Taken {
			continue
		}
		seen++
		if rand.IntN(seen) == 0 { // reservoir: keep the i-th candidate w.p. 1/seen
			chosen = i
		}
	}
	if chosen < 0 {
		a.warnLine = clampLine("No free character to switch to.")
		a.warnAt = time.Now()
		return
	}
	a.pickCharacter(chosen)
	a.warnLine = clampLine("Random character: " + a.sess.Chars[chosen].Name)
	a.warnAt = time.Now()
}

// drawSpritePreview shows the previewed sprite in a bottom-right box (the
// "show the entire thing" pop-up for a hovered/right-clicked icon or emote).
// With cycle set (the wardrobe's try-before-wear), it also draws the ‹ › emote
// navigator and keeps the box alive while the next emote streams in, so the
// controls don't blink. Off the hot path — only drawn when a preview is up.
func (a *App) drawSpritePreview(w, h int32, cycle bool) {
	c := a.ctx
	page, ok := a.d.Store.Get(a.previewBase)
	ready := ok && len(page.Frames) > 0
	cycling := cycle && len(a.previewAnims) > 1
	if !ready && !cycling {
		a.previewFrameRect = sdl.Rect{} // no box this frame — no phantom wheel/drag target
		return
	}
	// Size to the art when we have it; otherwise a default box so the box and
	// its ‹ › controls hold their place while the next emote sprite loads.
	pw, ph := previewMax/2, previewMax/2
	if ready {
		// The preview exists to show the sprite — restart its loop per pick so
		// animated idles play instead of freezing on frame 0.
		if a.previewBase != a.previewFor {
			a.previewFor = a.previewBase
			a.previewAt = time.Now()
			a.previewZoom = 1 // a new sprite starts unzoomed
		}
		pw, ph = page.W, page.H
		scale := int32(1)
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
	}
	// Default bottom-right, shifted by the user's drag and clamped on-screen.
	baseX, baseY := w-pw-pad*2, h-ph-pad*2
	hiX, hiY := w-pw-pad, h-ph-pad
	if hiX < pad {
		hiX = pad
	}
	if hiY < pad {
		hiY = pad
	}
	dst := sdl.Rect{X: clampI32(baseX+a.previewOffX, pad, hiX), Y: clampI32(baseY+a.previewOffY, pad, hiY), W: pw, H: ph}
	frame := sdl.Rect{X: dst.X - 4, Y: dst.Y - 4, W: dst.W + 8, H: dst.H + 8}
	a.previewFrameRect = frame // cached for handlePreviewInput (wheel zoom + drag)
	c.Fill(frame, ColPanel)
	c.Border(frame, ColAccent)
	if ready {
		tex := page.Frames[pageFrameLoop(page, a.now().Sub(a.previewAt))]
		if a.previewZoom > 1 {
			// Magnifier: blit a window of the sprite centered on where the
			// cursor sits over the box — move the mouse to pan around it.
			zw, zh := page.W/int32(a.previewZoom), page.H/int32(a.previewZoom)
			if zw < 1 {
				zw = 1
			}
			if zh < 1 {
				zh = 1
			}
			relX := float64(c.mouseX-dst.X) / float64(dst.W)
			relY := float64(c.mouseY-dst.Y) / float64(dst.H)
			relX = clampF64(relX, 0, 1)
			relY = clampF64(relY, 0, 1)
			src := sdl.Rect{X: clampI32(int32(relX*float64(page.W))-zw/2, 0, page.W-zw), Y: clampI32(int32(relY*float64(page.H))-zh/2, 0, page.H-zh), W: zw, H: zh}
			_ = c.Ren.Copy(tex, &src, &dst)
		} else {
			_ = c.Ren.Copy(tex, nil, &dst)
		}
		a.drawPreviewZoom(frame)
	} else {
		c.LabelClipped(dst.X+4, dst.Y+dst.H/2-8, dst.W-8, "loading…", ColTextDim)
	}
	if cycling {
		a.drawPreviewEmoteNav(frame)
	}
}

// previewDragBottomReserve keeps the bottom strip of the preview box (the − / +
// zoom buttons) out of the drag-start region, so pressing a button doesn't also
// grab-drag the box.
const previewDragBottomReserve = 22

// handlePreviewInput drives the sprite-preview box's mouse-wheel zoom and
// left-drag reposition. It runs in App.Frame BEFORE any screen draws, against
// last frame's cached box rect, so it claims the wheel and the mouse press ahead
// of the grid/list scroll and the icon clicks UNDER the box (it overlaps them).
// Off the hot path — a no-op whenever no preview is up.
func (a *App) handlePreviewInput() {
	c := a.ctx
	if a.previewBase == "" {
		a.previewDrag = false
		return
	}
	box := a.previewFrameRect
	if box.W == 0 { // no preview drawn yet
		return
	}
	// Wheel zoom in/out over the box; claim the wheel so the list/grid under it
	// doesn't also scroll (same range as the − / + buttons).
	if c.wheelY != 0 && c.hovering(box) {
		a.previewZoom = int(clampI32(int32(a.previewZoom)+c.wheelY, 1, previewZoomMax))
		c.wheelY = 0
		c.wheelTaken = true
	}
	// Left-drag to reposition. Start only on the body (not the bottom zoom-button
	// strip). Claiming the press here keeps a drag from selecting whatever icon is
	// underneath, and a real move swallows the release so click-to-dismiss/select
	// doesn't fire.
	body := sdl.Rect{X: box.X, Y: box.Y, W: box.W, H: box.H - previewDragBottomReserve}
	if c.mouseDown && !a.previewDrag && c.hovering(body) {
		a.previewDrag = true
		a.previewDragMoved = false
		a.previewDragStart = [2]int32{c.mouseX, c.mouseY}
		a.previewDragBase = [2]int32{a.previewOffX, a.previewOffY}
	}
	if a.previewDrag {
		if c.mouseDown {
			dx, dy := c.mouseX-a.previewDragStart[0], c.mouseY-a.previewDragStart[1]
			if dx != 0 || dy != 0 {
				a.previewDragMoved = true
			}
			a.previewOffX = a.previewDragBase[0] + dx
			a.previewOffY = a.previewDragBase[1] + dy
		} else {
			if a.previewDragMoved {
				c.clicked = false // a completed drag isn't a dismiss/selection
			}
			a.previewDrag = false
		}
	}
}

// drawPreviewZoom draws the magnifier controls (− / level / +) along the bottom
// of the preview box and handles their clicks (consumed, so the caller's
// click-to-dismiss doesn't fire). At >1× the cursor pans the magnified view.
func (a *App) drawPreviewZoom(frame sdl.Rect) {
	c := a.ctx
	const bh, bw int32 = 18, 22
	y := frame.Y + frame.H - bh
	minus := sdl.Rect{X: frame.X + 2, Y: y, W: bw, H: bh}
	plus := sdl.Rect{X: frame.X + frame.W - bw - 2, Y: y, W: bw, H: bh}
	lvl := sdl.Rect{X: frame.X + frame.W/2 - 16, Y: y, W: 32, H: bh}
	for _, b := range []struct {
		r     sdl.Rect
		label string
	}{{minus, "-"}, {lvl, fmt.Sprintf("%dx", a.previewZoom)}, {plus, "+"}} {
		c.Fill(b.r, sdl.Color{R: 0, G: 0, B: 0, A: 205})
		c.LabelClipped(b.r.X+6, b.r.Y+1, b.r.W-8, b.label, ColAccent)
	}
	c.Tooltip(minus, "Zoom out (or scroll the wheel over the box)")
	c.Tooltip(plus, "Zoom in (or scroll the wheel) — at >1× move the mouse to pan. Drag the box to move it.")
	if c.hovering(minus) && c.clicked {
		if a.previewZoom > 1 {
			a.previewZoom--
		}
		c.clicked = false
	}
	if c.hovering(plus) && c.clicked {
		if a.previewZoom < previewZoomMax {
			a.previewZoom++
		}
		c.clicked = false
	}
}

// drawPreviewEmoteNav draws the try-before-wear control bar above the preview
// box — [<] caption [>] — and handles its clicks plus Left/Right keys (only
// when no text field owns the keyboard). Clicks on the arrows are consumed so
// the caller's "click dismisses the preview" check doesn't also fire.
func (a *App) drawPreviewEmoteNav(frame sdl.Rect) {
	c := a.ctx
	const barH = 22
	const navW = 22
	bar := sdl.Rect{X: frame.X, Y: frame.Y - barH - 2, W: frame.W, H: barH}
	c.Fill(bar, sdl.Color{R: 0, G: 0, B: 0, A: 210})
	caption := ""
	if a.previewEmoteIdx < len(a.previewLabels) {
		caption = a.previewLabels[a.previewEmoteIdx]
	}
	caption = fmt.Sprintf("%s (%d/%d)", caption, a.previewEmoteIdx+1, len(a.previewAnims))
	c.LabelClipped(bar.X+navW+4, bar.Y+3, bar.W-2*navW-8, caption, ColAccent)
	if c.Button(sdl.Rect{X: bar.X, Y: bar.Y, W: navW, H: barH}, "<") {
		a.cyclePreviewEmote(-1)
		c.clicked = false
	}
	if c.Button(sdl.Rect{X: bar.X + bar.W - navW, Y: bar.Y, W: navW, H: barH}, ">") {
		a.cyclePreviewEmote(1)
		c.clicked = false
	}
	if c.focusID == "" { // arrow keys cycle too, unless the search field has focus
		switch c.keyPressed {
		case sdl.K_LEFT:
			a.cyclePreviewEmote(-1)
		case sdl.K_RIGHT:
			a.cyclePreviewEmote(1)
		}
	}
}

// --- COURTROOM ----------------------------------------------------------------------

func (a *App) drawCourtroom(w, h int32) {
	c := a.ctx
	// Fence the courtroom under the non-blocking Extras box: while the pointer is
	// over the box, this whole pass runs pointer-blind so a click in the box can't
	// also land on the scene/log. The deferred unfence is a direct method (no
	// closure) and a no-op when the box is closed, so the common frame is alloc-free.
	if a.boxFencesPointer(w, h) {
		c.fencePointer()
	}
	defer c.unfencePointer()
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	a.pollCharINI()
	if a.room == nil || a.sess == nil {
		a.screen = ScreenLobby
		return
	}

	// Theater mode preempts BOTH layouts: stage only, Esc exits.
	if a.theaterOn {
		a.drawTheater(w, h)
		return
	}

	// Theme-driven geometry: when the theme ships courtroom_design.ini
	// (and the toggle is on), the courtroom IS the theme's layout.
	if lay := a.themeLayout(w, h); lay.valid && a.d.Prefs.ThemeLayoutEnabled() {
		a.drawCourtroomThemed(w, h, lay)
		return
	}
	// The editor only exists over themed geometry; release its fence if
	// the theme (or the toggle) went away mid-edit.
	if a.layoutEdit {
		a.stopLayoutEdit()
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
	a.renderViewportZoomed(vp)
	a.drawStageRecordButton(vp)
	a.handleVpDivider(vp, w) // drag the right edge to resize (claims the press BEFORE zoom/sprite-drag)
	// Camera input first (Ctrl+wheel zoom / Ctrl+drag pan); sprite drag
	// only at 1× — zoomed hit rects would lie.
	chatBandH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	a.handleViewportZoom(vp, c.mouseY >= vp.Y+vp.H-chatBandH)
	if a.vpZoom <= 1 {
		a.handleSpriteDrag(vp)
		a.handleSpriteHide(vp) // right-click → hide-sprite confirm (default ON)
	}
	a.handleHotkeys() // Ctrl-chords (shouts, pos, music, screenshot...)
	if a.rehearsal {
		c.Label(vp.X+8, vp.Y+8, rehearsalBadge, ColTierYellow)
	}
	a.drawChatOverlay(vp)
	a.drawCourtOverlays(vp, nil) // HP bars, clocks, badges, splashes

	// Modal popups: the kit has no z-aware input, so the controls
	// underneath simply don't draw (and don't see clicks) — same pattern
	// as the iniswap menu. Shared with the themed path (drawCourtroomModals).
	if a.drawCourtroomModals(w, h) {
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

// drawCourtroomModals draws whichever return-to-top courtroom popup is open and
// reports whether one took the screen — the caller then returns, because the
// kit has no z-aware input so nothing underneath should draw or take clicks.
// SHARED by the classic and themed courtroom so the two lists can't drift: the
// background picker once drew in the classic path but was missing from the
// themed switch, so on a custom theme the Extras → Background button opened a
// picker that never rendered. (showPair is NOT here — it's a non-returning
// overlay drawn on top in both paths.)
func (a *App) drawCourtroomModals(w, h int32) bool {
	switch {
	case a.showIni:
		a.drawIniswapPanel(w, h)
	case a.bgPick.show:
		a.drawBgPanel(w, h)
	case a.showEvid:
		a.drawEvidencePanel(w, h)
	case a.showModcall:
		a.drawModcallDialog(w, h)
	case a.showTimer:
		a.drawTimerPanel(w, h)
	case a.showUICfg:
		a.drawUICfgPanel(w, h)
	case a.showLogin:
		a.drawLoginDialog(w, h)
	case a.pairPopupOpen:
		a.drawPairPopup(w, h)
	default:
		return false
	}
	return true
}

// handleVpDivider lets the user drag the viewport's right edge to resize the
// viewport↔log split (vpPct) — the mouse alternative to the View knob. Called
// BEFORE the zoom/sprite-drag handlers so a grab on the edge wins, and only in
// drag-resize mode (the default; the knob panel covers it when off). It scales
// the whole 4:3 viewport, so past the height clamp the edge stops tracking the
// mouse (the grip then sits "stuck" at the limit — expected, not a bug). Reuses
// the View knob's own clamp so the two never disagree.
func (a *App) handleVpDivider(vp sdl.Rect, w int32) {
	c := a.ctx
	if !a.d.Prefs.DragLayoutOn() || a.vpZoom > 1 || a.courtModalOpen() { // off, zoomed, or a blocking popup (Pair menu, evidence…) is up over the edge
		a.dragVpDivider = false
		a.dividerPrevDwn = c.mouseDown
		return
	}
	handle := sdl.Rect{X: vp.X + vp.W - 4, Y: vp.Y, W: 12, H: vp.H}
	hot := c.hovering(handle)
	grip := ColPanelHi
	if hot || a.dragVpDivider {
		grip = ColAccent
	}
	c.Fill(sdl.Rect{X: vp.X + vp.W - 1, Y: vp.Y + vp.H/2 - 24, W: 3, H: 48}, grip) // a subtle edge grip
	pressed := c.mouseDown && !a.dividerPrevDwn
	a.dividerPrevDwn = c.mouseDown
	if pressed && hot {
		a.dragVpDivider = true
	}
	if !c.mouseDown {
		if a.dragVpDivider {
			a.saveLayout() // persist the new width on release
		}
		a.dragVpDivider = false
		return
	}
	if a.dragVpDivider && w > 0 {
		pct := int(int64(c.mouseX-vp.X) * int64(DefaultScalePct) / int64(w))
		if pct < config.MinViewportPercent {
			pct = config.MinViewportPercent
		}
		if pct > config.MaxViewportPercent {
			pct = config.MaxViewportPercent
		}
		a.vpPct = pct
	}
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
	if a.dragVpDivider || a.courtModalOpen() {
		return // the resize divider owns this press, or a blocking popup fences the stage
	}
	if !a.d.Prefs.SpriteMoveEnabled() {
		return // opt-in (Settings → General); off by default so stray clicks can't nudge sprites
	}
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

	// Right-click a sprite: drop its move override (back to server placement) —
	// but only when right-click-to-hide is OFF, so the two don't both fire.
	if c.rightClicked && !a.d.Prefs.RightClickHideSpriteOn() && len(a.spriteOv) > 0 && c.hovering(vp) && !inChatBox {
		for _, layer := range layers {
			if r, ok := a.spriteHitRect(vp, layer); ok && c.hovering(r) {
				delete(a.spriteOv, strings.ToLower(layer.Name))
				break
			}
		}
	}
}

// requestDisconnect is what the user-facing Disconnect buttons call: it acts at
// once when "instant disconnect" is set, otherwise opens a confirm modal (the
// button is easy to hit by accident). Internal disconnects — a closed tab, a
// dropped connection, auto-reconnect teardown — keep calling Disconnect() direct.
func (a *App) requestDisconnect() {
	if a.d.Prefs.InstantDisconnectOn() {
		a.Disconnect()
		return
	}
	a.confirmDisconnect = true
}

// drawDisconnectConfirm paints the "are you sure" overlay: a dimmed screen + a
// centered modal warning that Yes leaves the server, with Yes / Cancel and a note
// about the instant-disconnect setting. Drawn top-level (Frame) so it works from
// any Disconnect button; the pointer is fenced around it so clicks can't reach the
// screen behind. Off the render hot path (only while open).
func (a *App) drawDisconnectConfirm(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 480, 176
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Disconnect from the server?", ColText)
	c.Label(m.X+pad, m.Y+50, "Yes will disconnect you from the server (you'll return to the lobby).", ColText)
	c.Label(m.X+pad, m.Y+74, "Tip: enable \"Instant disconnect\" in Settings → General to skip this.", ColTextDim)
	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 170, H: btnH}, "Yes, disconnect") {
		a.confirmDisconnect = false
		a.Disconnect()
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.confirmDisconnect = false
	}
}

// handleSpriteHide opens the "hide this sprite?" confirm when the user right-clicks
// a character sprite (default ON; Settings can disable). Hidden sprites are dropped
// from the viewport for the session — for players who'd rather not see certain art.
// Only at 1× zoom (zoomed hit rects lie) and not over the chatbox area.
func (a *App) handleSpriteHide(vp sdl.Rect) {
	c := a.ctx
	if !c.rightClicked || a.room == nil || !a.d.Prefs.RightClickHideSpriteOn() || a.courtModalOpen() {
		return // a blocking popup fences the stage
	}
	boxH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	if !c.hovering(vp) || c.mouseY >= vp.Y+vp.H-boxH {
		return
	}
	sc := &a.room.Scene
	layers := [2]*courtroom.SpriteLayer{&sc.Speaker, &sc.Pair}
	if sc.PairActive && !sc.SpeakerInFront {
		layers[0], layers[1] = &sc.Pair, &sc.Speaker
	}
	for _, layer := range layers {
		if r, ok := a.spriteHitRect(vp, layer); ok && c.hovering(r) && layer.Name != "" {
			a.hidePrompt = layer.Name // open the confirm for this sprite
			break
		}
	}
}

// drawHideSpriteConfirm paints the "hide this sprite?" modal. Yes hides it for the
// session; the pointer is fenced around it (Frame) so the click can't reach the
// courtroom behind. Off the render hot path (only while open).
func (a *App) drawHideSpriteConfirm(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 520, 184
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Hide this sprite?", ColText)
	c.Label(m.X+pad, m.Y+50, "Hide \""+a.hidePrompt+"\" from the viewport for this session?", ColText)
	c.Label(m.X+pad, m.Y+74, "Reshow all with the \"Reshow hidden sprites\" key, or turn this off in Settings → General.", ColTextDim)
	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 150, H: btnH}, "Hide it") {
		if a.hiddenSprites == nil {
			a.hiddenSprites = make(map[string]struct{}, 4)
		}
		a.hiddenSprites[strings.ToLower(a.hidePrompt)] = struct{}{}
		a.hidePrompt = ""
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.hidePrompt = ""
	}
}

// reshowSprites un-hides every sprite hidden this session (the Reshow key + the
// Settings button).
func (a *App) reshowSprites() {
	if len(a.hiddenSprites) == 0 {
		return
	}
	a.hiddenSprites = nil
	a.warnLine = "Hidden sprites are showing again."
	a.warnAt = time.Now()
}

func (a *App) drawChatOverlay(vp sdl.Rect) {
	c := a.ctx
	sc := a.renderScene() // live room, slideshow override, OR the replay scene (M16)
	// Blankpost hides the whole chatbox (frame + showname + text) so only the
	// sprite shows; the second clause is the unchanged idle/no-message case.
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
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
		_ = c.Ren.Copy(a.themeFrame(page), nil, &box)
		skinned = true
	}
	if !skinned {
		// Panel opacity is user-tunable (Settings → see-through chatbox); the
		// border stays solid for legibility. Read once here, off the 0-alloc
		// render gate (that's render.Viewport; this UI overlay already reads prefs).
		alpha := uint8(255 * a.d.Prefs.ChatboxOpacityPct() / 100)
		c.Fill(box, sdl.Color{R: 16, G: 16, B: 24, A: alpha})
		c.Border(box, ColAccent)
	}
	// Theme text colors are designed against the theme's own skin; on the
	// flat fallback panel they can be unreadable (black-on-dark was a real
	// report), so they only apply while the skin actually drew.
	nameCol := ColAccent
	if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	if a.d.Prefs.NameColorsOn() { // per-speaker name colour wins over accent/theme
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.NameColorSat())/100, float64(a.d.Prefs.NameColorVal())/100)
	}
	a.labelEmoji(c.font, c.EmojiFont(DefaultScalePct), box.X+8, box.Y+4, box.W-16, sc.ShownameText, nameCol)

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
	sc := a.renderScene() // matches drawChatOverlay (live / slideshow / replay scene)
	if a.msRaster != nil && a.rasterRaw == sc.MessageRaw && a.rasterText == sc.MessageText && a.rasterColor == sc.TextColor &&
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
	raster, err := renderRaster(a, sc, wrapW, skinned, a.chatPct)
	if err != nil {
		return
	}
	a.msRaster = raster
	a.rasterText = sc.MessageText
	a.rasterRaw = sc.MessageRaw
	a.rasterColor = sc.TextColor
	a.rasterScale = a.chatPct
	a.rasterW = wrapW
	a.rasterSkinned = skinned
}

// chatZoomWheel: Ctrl+wheel over the chatbox zooms the chat text
// (browser convention) — shared by both chatbox flavors.
func (a *App) chatZoomWheel(box sdl.Rect) {
	// Ctrl+wheel (fine) or wheel-button-held (fast) resizes the chatbox text.
	a.zoomWheel(box, &a.chatPct, config.MinChatScalePercent, config.MaxChatScalePercent)
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

func clampI32(v, min, max int32) int32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampF64(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// drawVolumeStrip draws the toggleable on-screen volume row (Master + the three
// channels) at the top of the log panel — quick volume without leaving the chat.
func (a *App) drawVolumeStrip(r sdl.Rect) {
	c := a.ctx
	master := a.d.Prefs.MasterVolume()
	music, sfx, blip := a.d.Prefs.AudioVolumes()
	colW := r.W / 4
	cell := func(i int32, id, label string, val int) int {
		x := r.X + i*colW
		c.Label(x, r.Y+4, label, ColTextDim)
		track := sdl.Rect{X: x + 34, Y: r.Y + 6, W: colW - 42, H: 12}
		return int(c.Slider("volstrip:"+id, track, int32(val), 100))
	}
	if nv := cell(0, "master", "Mas", master); nv != master {
		a.d.Prefs.SetMasterVolume(nv)
		a.applyAudioVolumes()
	}
	if nv := cell(1, "music", "Mus", music); nv != music {
		a.d.Prefs.SetAudioVolumes(nv, sfx, blip)
		a.applyAudioVolumes()
	}
	if nv := cell(2, "sfx", "SFX", sfx); nv != sfx {
		a.d.Prefs.SetAudioVolumes(music, nv, blip)
		a.applyAudioVolumes()
	}
	if nv := cell(3, "blip", "Blp", blip); nv != blip {
		a.d.Prefs.SetAudioVolumes(music, sfx, nv)
		a.applyAudioVolumes()
	}
}

func (a *App) drawLogPanel(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanel)
	c.Border(r, ColPanelHi)
	// A "🔊" toggle at the right end drops a compact volume strip above the panel
	// content — adjust volume while the log stays on screen and you keep chatting
	// (the IC box below is untouched). The tabs share the rest of the row.
	const volBtnW = int32(36)
	const numLogTabs = 7
	tab := (r.W - volBtnW) / numLogTabs
	tabBtn := func(i int32, id int, label string) {
		bw := tab
		if int(i) == numLogTabs-1 {
			bw = (r.W - volBtnW) - (numLogTabs-1)*tab // last tab takes the remainder before the Vol toggle
		}
		if c.Button(sdl.Rect{X: r.X + i*tab, Y: r.Y, W: bw, H: btnH}, label) {
			a.logTab = id
		}
	}
	tabBtn(0, logTabLog, "Log")
	tabBtn(1, logTabMusic, "Music")
	tabBtn(2, logTabAreas, "Areas")
	tabBtn(3, logTabPlayers, "Players")
	tabBtn(4, logTabOOC, "OOC")
	tabBtn(5, logTabNotes, "Notes")
	tabBtn(6, logTabFriends, "Friends")
	volBtn := sdl.Rect{X: r.X + r.W - volBtnW, Y: r.Y, W: volBtnW, H: btnH}
	if c.Button(volBtn, "Vol") {
		a.volStripOn = !a.volStripOn
	}
	if a.volStripOn {
		c.Border(volBtn, ColAccent) // active cue
	}
	c.Tooltip(volBtn, "Show/hide volume sliders on screen (chat stays usable)")
	innerY := r.Y + btnH + 4
	if a.volStripOn {
		const stripH = int32(28)
		a.drawVolumeStrip(sdl.Rect{X: r.X + 4, Y: innerY, W: r.W - 8, H: stripH})
		innerY += stripH + 4
	}
	inner := sdl.Rect{X: r.X + 4, Y: innerY, W: r.W - 8, H: r.Y + r.H - innerY - 4}
	// Ctrl+wheel (fine) or wheel-button-held (fast) anywhere on the panel
	// resizes the log/OOC/list text; plain wheel keeps scrolling the active
	// list. Taking the wheel here stops the inner lists' zoom from double-stepping.
	// The Music tab tunes its OWN scale (musicPct) so you can shrink long track
	// titles without shrinking the IC log.
	scale := &a.logPct
	if a.logTab == logTabMusic {
		scale = &a.musicPct
	}
	a.zoomWheel(r, scale, config.MinLogScalePercent, config.MaxLogScalePercent)
	switch a.logTab {
	case logTabMusic:
		a.drawMusicList(inner)
		return
	case logTabAreas:
		a.drawAreaList(inner)
		return
	case logTabPlayers:
		a.drawPlayerList(inner)
		return
	case logTabOOC:
		a.drawOOCPanel(inner)
		return
	case logTabNotes:
		a.drawNotesTab(inner)
		return
	case logTabFriends:
		a.drawFriendsTab(inner)
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

// unreadDividerH is the thickness of the accent line drawn at the first
// unread IC line (the "last read" boundary the N-new pill jumps to).
const unreadDividerH = 2

// friendTintColor is the warm "glow" behind a highlighted friend's IC line
// (translucent so the text reads through; distinct from the blue selection).
// A `name=RRGGBB` friends entry overrides the R/G/B per friend; the alpha is
// the glow strength (and the pulse ceiling).
var friendTintColor = sdl.Color{R: 255, G: 210, B: 90, A: 64}

const (
	// friendGlowMinAlpha is the dim floor the pulsing glow breathes down to
	// (up to friendTintColor.A and back) — only when FriendGlowPulse is on.
	friendGlowMinAlpha = 20
	// friendGlowPulsePeriod is one full breathe cycle of the friend glow.
	friendGlowPulsePeriod = 1600 * time.Millisecond
)

// logFastZoomMultiple scales the text-zoom step when the middle (wheel) button
// is held — "hold the wheel and scroll to zoom the log fast".
const logFastZoomMultiple = 5

// zoomWheel resizes a text scale (*pct, clamped to [min,max]) when the wheel
// turns over rect with Ctrl (fine, one step) OR the middle button held (fast,
// logFastZoomMultiple steps). Returns true if it consumed the wheel, so the
// caller skips scrolling. Shared by every text-zoom surface (IC/OOC log, the
// right-column panel, the chatbox) so the gesture is identical everywhere.
func (a *App) zoomWheel(rect sdl.Rect, pct *int, min, max int) bool {
	c := a.ctx
	if c.wheelTaken || c.wheelY == 0 || !c.hovering(rect) || (!c.ctrlHeld && !c.middleHeld) {
		return false
	}
	step := config.ScaleStepPercent
	if c.middleHeld {
		step *= logFastZoomMultiple
	}
	*pct = clampInt(*pct+int(c.wheelY)*step, min, max)
	a.saveLayout()
	c.wheelTaken = true
	return true
}

// drawICLogList renders the colored IC scrollback (search-filtered,
// word-wrapped to the list width) into rect — used by the classic Log tab
// and the themed ic_chatlog element.
func (a *App) drawICLogList(list sdl.Rect) {
	c := a.ctx
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	rows := a.icWrapped(wrapW, a.d.Prefs.ICTimestampsOn()) // per-frame pref read, like the IC counter
	contentH := int32(len(rows)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	// Ctrl+wheel (fine) or wheel-button-held (fast) zooms the log text.
	zoomed := a.zoomWheel(list, &a.logPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	maxScroll := contentH - list.H
	if maxScroll < 0 {
		maxScroll = 0
	}
	if !zoomed { // a zoom consumed the wheel — don't also scroll
		if d := c.WheelIn(list); d != 0 {
			a.icScroll -= d * scrollStepPx
			a.icStick = a.icScroll >= maxScroll // bottom re-sticks, up releases
		}
	}
	before := a.icScroll
	a.icScroll = c.VScrollbar("icscroll", track, a.icScroll, contentH, list.H)
	if a.icScroll != before {
		a.icStick = a.icScroll >= maxScroll // bar drag follows the same rule
	}
	// AUTO-SCROLL: while stuck, every new line lands in view no matter
	// how many wrapped rows it added.
	if a.icStick {
		a.icScroll = maxScroll
		a.icReadMark = len(a.icLog) // caught up to the bottom: everything is read
	}
	if a.icReadMark > len(a.icLog) {
		a.icReadMark = len(a.icLog) // log was capped/cleared
	}
	// Drag-select / Ctrl+C (before the loop so a real drag swallows the click).
	a.handleLogSelect(logSelIC, list, a.icScroll, lineH, wrapW)
	// Pin-to-notes can also fire on a configurable Ctrl-chord (default Ctrl+N):
	// it pins the HOVERED line, like right-click. Resolved once (the chord is
	// one key/frame); handleHotkeys leaves an unrecognized chord in c.hotkey.
	pinChord := c.hotkey != 0 && strings.EqualFold(sdl.GetKeyName(c.hotkey), a.hotkeyFor(hotkeyPinNote))
	friendsOn := a.d.Prefs.FriendHighlightOn() // gates the per-line friend glow (read once)
	// Per-speaker name colours (read once): tint each entry's name prefix on its
	// first wrapped row. Short-circuit so the default OFF path adds nothing.
	nameColorsOn := a.d.Prefs.NameColorsOn()
	var nameSat, nameVal float64
	if nameColorsOn {
		nameSat = float64(a.d.Prefs.NameColorSat()) / 100
		nameVal = float64(a.d.Prefs.NameColorVal()) / 100
	}
	// Glow alpha, computed once per frame: static by default, or a slow breathe
	// when FriendGlowPulse is on (suppressed by reduce-motion). Short-circuit
	// keeps the no-friend default path to a single pref read — no trig, no extra
	// locks. Modulo-into-period keeps the sine argument small for precision.
	friendAlpha := friendTintColor.A
	if friendsOn && a.d.Prefs.FriendGlowPulseOn() && !a.d.Prefs.ReduceMotion() {
		period := int64(friendGlowPulsePeriod)
		phase := math.Sin(float64(a.now().UnixNano()%period) / float64(period) * 2 * math.Pi)
		lo, hi := float64(friendGlowMinAlpha), float64(friendTintColor.A)
		friendAlpha = uint8(lo + (hi-lo)*(phase*0.5+0.5)) // [-1,1] -> [floor, full]
	}
	// First wrapped row of the first unread entry — the "jump to last read"
	// target and where the unread divider draws. Scanned only while scrolled
	// up with unread; caught-up frames (icReadMark == len) skip it entirely, so
	// the hot render path pays nothing.
	firstUnreadRow := -1
	if a.icReadMark < len(a.icLog) {
		for ri := range rows {
			if rows[ri].entry >= a.icReadMark {
				firstUnreadRow = ri
				break
			}
		}
	}
	// Scissor the scrollback to the list rect so the partially scrolled
	// top/bottom row can't draw past it onto the tab strip above.
	clipPrev, clipHad := c.pushClip(list)
	y := list.Y - a.icScroll
	for ri := range rows {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			row := &rows[ri]
			col := ColText
			if ecol := a.icLog[row.entry].color; ecol > 0 {
				col = render.TextColor(ecol)
			}
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			font := c.LogFontFor(a.logPct, row.text)
			// A highlighted friend's line glows (warm tint behind it), gated on
			// the master toggle + the entry flag — a no-friend log is byte-
			// identical to before. A `name=hex` entry recolors the glow per
			// friend; the alpha carries the (optional) pulse.
			if friendsOn && a.icLog[row.entry].friend {
				tint := friendTintColor
				if fc := a.icLog[row.entry].friendColor; fc >= 0 {
					tint.R, tint.G, tint.B = uint8(fc>>16), uint8(fc>>8), uint8(fc)
				}
				tint.A = friendAlpha
				c.Fill(rowRect, tint)
			}
			// Selection highlight sits under the text (and the divider).
			a.drawLogSelHighlight(logSelIC, ri, list.X, y, wrapW, lineH, row.text, font)
			// Unread divider: a thin accent rule at the top of the first unread
			// line, so "jump to last read" lands on an obvious boundary.
			if ri == firstUnreadRow {
				c.Fill(sdl.Rect{X: list.X, Y: y, W: wrapW, H: unreadDividerH}, ColAccent)
			}
			// A line carrying a link reads as a link on hover (accent) and
			// opens it on click — the whole message line is the hit target.
			if a.icLog[row.entry].url != "" && c.hovering(rowRect) {
				col = ColAccent
			}
			// Per-speaker name colour: tint the name prefix on an entry's FIRST
			// wrapped row (the shared helper falls back to a plain draw for
			// system/evidence lines or when a long name wrapped off this row).
			lineSpeaker := ""
			if nameColorsOn && (ri == 0 || rows[ri-1].entry != row.entry) {
				lineSpeaker = a.icLog[row.entry].speaker
			}
			a.drawLogLineNamed(font, c.EmojiFont(a.logPct), list.X, y, wrapW, row.text, lineSpeaker, col, nameColorsOn, nameSat, nameVal)
			if u := a.icLog[row.entry].url; u != "" {
				if c.hovering(rowRect) {
					c.Tooltip(rowRect, "Open "+u)
					if c.clicked {
						openBrowser(u)
					}
				}
			}
			// The pin-note chord (default Ctrl+N) pins the WHOLE entry into the case
			// notebook. Right-click now COPIES the log text (see handleLogSelect), so
			// pinning lives on its chord; double-click selects the line; pairing moved
			// to the player list's Pair button.
			if pinChord && c.hovering(sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}) {
				a.pinNote(a.icLog[row.entry].text)
			}
		}
		y += lineH
	}
	// "N new" pill: while scrolled up, show how many messages arrived since you
	// last caught up. Clicking it jumps to the FIRST unread line (read forward
	// from where you left off); the Jump-logs hotkey still snaps to newest.
	if unread := len(a.icLog) - a.icReadMark; unread > 0 && !a.icStick {
		label := fmt.Sprintf("↓ %d new", unread)
		bw := c.TextWidth(label) + 20
		pill := sdl.Rect{X: list.X + (list.W-bw)/2, Y: list.Y + list.H - 26, W: bw, H: 22}
		c.Fill(pill, ColAccent)
		c.Label(pill.X+10, pill.Y+4, label, ColBackground)
		if c.hovering(pill) {
			c.Tooltip(pill, "Left-click: jump to the newest message · right-click: first unread")
			if c.clicked { // left-click → snap straight to the latest message
				a.icScroll, a.icStick = maxScroll, true
			} else if c.rightClicked { // right-click → read forward from the first unread
				if firstUnreadRow >= 0 {
					a.icScroll = clampI32(int32(firstUnreadRow)*lineH, 0, maxScroll)
					a.icStick = a.icScroll >= maxScroll
				} else {
					a.icStick = true // unread rows scrolled off the cap: just go live
				}
			}
		}
	}
	c.popClip(clipPrev, clipHad)
}

// drawOOCLogList renders the OOC scrollback into rect (themed
// server_chatlog element; the classic OOC tab keeps its own copy with the
// identity fields).
func (a *App) drawOOCLogList(list sdl.Rect) {
	c := a.ctx
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	lines := a.oocWrapped(wrapW)             // MOTDs wrap — never truncate
	nameColorsOn := a.d.Prefs.NameColorsOn() // per-speaker OOC name colours (read once)
	var nameSat, nameVal float64
	if nameColorsOn {
		nameSat = float64(a.d.Prefs.NameColorSat()) / 100
		nameVal = float64(a.d.Prefs.NameColorVal()) / 100
	}
	contentH := int32(len(lines)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	// Ctrl+wheel (fine) or wheel-button-held (fast) zooms the OOC text.
	zoomed := a.zoomWheel(list, &a.logPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	maxScroll := contentH - list.H
	if maxScroll < 0 {
		maxScroll = 0
	}
	if !zoomed {
		if d := c.WheelIn(list); d != 0 {
			a.oocScroll -= d * scrollStepPx
			a.oocStick = a.oocScroll >= maxScroll // bottom re-sticks, up releases
		}
	}
	before := a.oocScroll
	a.oocScroll = c.VScrollbar("oocscroll", track, a.oocScroll, contentH, list.H)
	if a.oocScroll != before {
		a.oocStick = a.oocScroll >= maxScroll
	}
	if a.oocStick {
		a.oocScroll = maxScroll
	}
	// Drag-select / Ctrl+C (before the loop so a real drag swallows the click).
	a.handleLogSelect(logSelOOC, list, a.oocScroll, lineH, wrapW)
	clipPrev, clipHad := c.pushClip(list) // top/bottom row clipped to the rect, not the tabs
	y := list.Y - a.oocScroll
	for li, line := range lines {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			col := ColText
			font := c.LogFontFor(a.logPct, line)
			// Selection highlight sits under the text.
			a.drawLogSelHighlight(logSelOOC, li, list.X, y, wrapW, lineH, line, font)
			// Links in OOC are openable (click) and copyable (right-click) —
			// matching the IC log. The link is the entry's, resolved at wrap
			// time (oocWrapURL), so a URL the wrap hard-split still opens whole.
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			if c.hovering(rowRect) && li < len(a.oocWrapURL) && a.oocWrapURL[li] != "" {
				col = ColAccent
				a.oocLinkActions(rowRect, a.oocWrapURL[li])
			}
			sp := ""
			if li < len(a.oocWrapName) {
				sp = a.oocWrapName[li]
			}
			a.drawLogLineNamed(font, c.EmojiFont(a.logPct), list.X, y, wrapW, line, sp, col, nameColorsOn, nameSat, nameVal)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)
}

// oocLinkActions handles the hover interactions for an OOC log line that holds
// a link: a one-click "+ Jukebox" save button at the row's right edge, else
// click-to-open / right-click-to-copy. Shared by the courtroom OOC log and the
// OOC tab. Only ever called for the single hovered line (one extract/frame).
func (a *App) oocLinkActions(rowRect sdl.Rect, url string) {
	c := a.ctx
	if a.juke != nil {
		saveBtn := sdl.Rect{X: rowRect.X + rowRect.W - 96, Y: rowRect.Y + 1, W: 94, H: rowRect.H - 2}
		if c.Button(saveBtn, "+ Jukebox") {
			if a.juke.QuickAdd(jukeboxOOCPlaylist, "", url) {
				a.warnLine = "Saved to Jukebox → " + jukeboxOOCPlaylist
			} else {
				a.warnLine = "Already in your Jukebox (or it's full)"
			}
			a.warnAt = time.Now()
			return
		}
		if c.hovering(saveBtn) {
			return // hovering the button — don't also open/copy the line
		}
	}
	c.Tooltip(rowRect, "Click to open · right-click to copy · or + Jukebox to save: "+url)
	if c.clicked {
		openBrowser(url)
	} else if c.rightClicked {
		_ = sdl.SetClipboardText(url)
		a.warnLine = "Link copied to clipboard"
		a.warnAt = time.Now()
	}
}

// submitOOC sends the OOC input if non-blank (shared classic/themed).
// The name falls back to the sticky AsyncAO<n> — servers reject empty
// OOC names, and commands must always be sendable.
func (a *App) submitOOC() {
	if strings.TrimSpace(a.oocInput) == "" || a.sess == nil {
		return
	}
	a.sess.SendOOC(a.oocNameOrDefault(), a.oocInput)
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
	lines := a.oocWrapped(wrapW)             // MOTDs wrap — never truncate
	nameColorsOn := a.d.Prefs.NameColorsOn() // per-speaker OOC name colours (read once)
	var nameSat, nameVal float64
	if nameColorsOn {
		nameSat = float64(a.d.Prefs.NameColorSat()) / 100
		nameVal = float64(a.d.Prefs.NameColorVal()) / 100
	}
	contentH := int32(len(lines)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	maxScroll := contentH - list.H
	if maxScroll < 0 {
		maxScroll = 0
	}
	if !c.ctrlHeld { // ctrl+wheel resizes text, never scrolls
		if d := c.WheelIn(list); d != 0 {
			a.oocScroll -= d * scrollStepPx
			a.oocStick = a.oocScroll >= maxScroll // bottom re-sticks, up releases
		}
	}
	before := a.oocScroll
	a.oocScroll = c.VScrollbar("oocscroll", track, a.oocScroll, contentH, list.H)
	if a.oocScroll != before {
		a.oocStick = a.oocScroll >= maxScroll
	}
	if a.oocStick {
		a.oocScroll = maxScroll
	}
	// Drag-select / Ctrl+C + links — same as the themed OOC log (this is the
	// classic OOC tab's own scrollback; it must behave identically).
	a.handleLogSelect(logSelOOC, list, a.oocScroll, lineH, wrapW)
	clipPrev, clipHad := c.pushClip(list) // scrollback only; restored before the fields below
	y := list.Y - a.oocScroll
	for li, line := range lines {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			col := ColText
			font := c.LogFontFor(a.logPct, line)
			a.drawLogSelHighlight(logSelOOC, li, list.X, y, wrapW, lineH, line, font)
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			if c.hovering(rowRect) && li < len(a.oocWrapURL) && a.oocWrapURL[li] != "" {
				col = ColAccent
				a.oocLinkActions(rowRect, a.oocWrapURL[li])
			}
			sp := ""
			if li < len(a.oocWrapName) {
				sp = a.oocWrapName[li]
			}
			a.drawLogLineNamed(font, c.EmojiFont(a.logPct), list.X, y, wrapW, line, sp, col, nameColorsOn, nameSat, nameVal)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)

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
	// Recent areas (M3): one-click jump-back chips for the areas you've passed
	// through (newest first; index 0 is the current area, so skip it). One row.
	if len(a.areaHistory) > 1 {
		c.Label(r.X+2, r.Y+5, "Recent:", ColTextDim)
		cx := r.X + 2 + c.TextWidth("Recent:") + 8
		for _, name := range a.areaHistory[1:] {
			w := c.TextWidth(name) + 16
			if cx+w > r.X+r.W-scrollBarW {
				break // keep it to a single row inside the panel
			}
			if c.Button(sdl.Rect{X: cx, Y: r.Y, W: w, H: 22}, name) {
				a.jumpToArea(name)
			}
			cx += w + 4
		}
		r.Y += 28
		r.H -= 28
	}
	if !c.ctrlHeld { // ctrl+wheel resizes text, never scrolls
		a.areaScroll -= c.WheelIn(r) * scrollStepPx
	}
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 10
	contentH := int32(len(a.sess.Areas)) * lineH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.areaScroll = c.VScrollbar("areascroll", track, a.areaScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r) // partial top/bottom row stays inside the panel
	defer c.popClip(clipPrev, clipHad)
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
			if c.ClickedIn(row) { // press+release in-row: a drag-in release must not transfer areas
				a.switchAreaScrollback(area) // per-area IC log (opt-in) — before curArea moves
				a.sess.RequestMusic(area)    // area transfer rides MC
				a.curArea = area             // Rich Presence (best-effort)
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
// wardrobeFolders lists the distinct folder names in the current menu, in
// first-seen order (stable, no per-frame sort) — the folder icons at the
// wardrobe's top level. Membership-derived: a folder exists exactly while a
// character is filed in it (filing the first creates it, emptying it removes
// it), so there's no separate folder table to keep in sync.
func (a *App) wardrobeFolders() []string {
	var out []string
	seen := map[string]struct{}{}
	for _, f := range a.iniFolders {
		if f == "" {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

// folderMenuOpt is one row of the right-click character menu.
type folderMenuOpt struct {
	label, folder string
	remove        bool // remove-from-wardrobe action (not a folder move)
}

const iniFolderMenuW = 200

// iniFolderMenuOpts builds the character menu rows: remove from wardrobe, unfile,
// each existing folder, then a "+ new: X" row when the new-folder field has text.
func (a *App) iniFolderMenuOpts() []folderMenuOpt {
	opts := []folderMenuOpt{{label: "× Remove from wardrobe", remove: true}, {label: "(unsorted)"}}
	for _, f := range a.wardrobeFolders() {
		opts = append(opts, folderMenuOpt{label: f, folder: f})
	}
	if nf := strings.TrimSpace(a.iniNewFold); nf != "" {
		opts = append(opts, folderMenuOpt{label: "+ new: " + nf, folder: nf})
	}
	return opts
}

// iniFolderMenuRect positions the menu at the cursor, clamped on-screen.
func iniFolderMenuRect(at [2]int32, nOpts int, w, h int32) sdl.Rect {
	r := sdl.Rect{X: at[0], Y: at[1], W: iniFolderMenuW, H: int32(nOpts)*iniMenuRowH + 4}
	if r.X+r.W > w {
		r.X = w - r.W
	}
	if r.Y+r.H > h {
		r.Y = h - r.H
	}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	return r
}

const iniMenuRowH = 22

// iniDragThreshold is the cursor travel (logical px) past which a press on a
// wardrobe cell becomes a drag-to-file instead of a wear-click.
const iniDragThreshold = 6

// handleIniFolderMenu resolves a click on the open move-to-folder menu BEFORE
// the grid draws (so the click never leaks through to a cell). A click on a
// row files the character; a click anywhere else closes it; Esc closes it.
// Either way the click is consumed.
func (a *App) handleIniFolderMenu(rect sdl.Rect, opts []folderMenuOpt) {
	c := a.ctx
	if c.escPressed {
		a.iniMenuChar = ""
		return
	}
	if !c.clicked {
		return
	}
	for i, opt := range opts {
		row := sdl.Rect{X: rect.X, Y: rect.Y + 2 + int32(i)*iniMenuRowH, W: rect.W, H: iniMenuRowH}
		if c.hovering(row) {
			if opt.remove {
				a.d.Prefs.RemoveWardrobe(a.serverKey, a.iniMenuChar) // unfavourite (also drops its folder)
			} else {
				a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniMenuChar, opt.folder)
			}
			a.rebuildIniMenu()
			a.iniMenuChar = ""
			c.clicked = false
			return
		}
	}
	a.iniMenuChar = "" // clicked outside → close
	c.clicked = false
}

// drawIniFolderMenu paints the move-to-folder menu on top, highlighting the
// character's current folder.
func (a *App) drawIniFolderMenu(rect sdl.Rect, opts []folderMenuOpt) {
	c := a.ctx
	c.Fill(rect, sdl.Color{R: 18, G: 18, B: 26, A: 245})
	c.Border(rect, ColAccent)
	cur := a.d.Prefs.WardrobeFolderMap(a.serverKey)[strings.ToLower(a.iniMenuChar)]
	for i, opt := range opts {
		row := sdl.Rect{X: rect.X, Y: rect.Y + 2 + int32(i)*iniMenuRowH, W: rect.W, H: iniMenuRowH}
		if c.hovering(row) {
			c.Fill(row, ColPanelHi)
		}
		col := ColText
		if opt.remove {
			col = ColDanger // remove-from-wardrobe
		} else if opt.folder == cur {
			col = ColAccent // the character's current folder
		}
		c.LabelClipped(row.X+8, row.Y+4, row.W-12, opt.label, col)
	}
}

// Wardrobe sections: your curated characters (folders), your favourite
// backgrounds, and a flat browse of the server's full iniswap list (★ to add
// one to your characters).
const (
	wardSectionCharacters = iota
	wardSectionBackgrounds
	wardSectionIniswaps
	wardSectionJukebox
)

// drawIniswapPanel is the wardrobe modal shell. It owns the state SHARED by
// both sections — the drag press-edge and the end-of-frame drag cleanup, which
// must run exactly once per frame so a drag can't arm twice or fail to clear —
// and the section tabs, then dispatches to the active section's body for the
// grid region. A tab is a mouseup, so a section never switches mid-drag.
func (a *App) drawIniswapPanel(w, h int32) {
	c := a.ctx
	a.iniPressed = c.mouseDown && !a.iniPrevDown // mouse went down this frame (drag arm; shared)
	panel := sdl.Rect{X: pad * 3, Y: pad * 3, W: w - pad*6, H: h - pad*6}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+8, "Wardrobe", ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 90 - pad, Y: panel.Y + 8, W: 90, H: btnH}, "Close") {
		a.showIni = false
		a.iniMenuChar = ""
		a.wardDelFolder = ""
		a.iniPrevDown = c.mouseDown
		return
	}
	// Section tabs on the header row.
	tabX := panel.X + 110
	for _, t := range [...]struct {
		id    int
		label string
	}{{wardSectionCharacters, "Characters"}, {wardSectionBackgrounds, "Backgrounds"}, {wardSectionIniswaps, "Iniswaps"}, {wardSectionJukebox, "Jukebox"}} {
		r := sdl.Rect{X: tabX, Y: panel.Y + 8, W: 120, H: btnH}
		bg := ColPanel
		if a.wardSection == t.id {
			bg = ColPanelHi
		}
		c.Fill(r, bg)
		c.Border(r, ColAccent)
		c.Label(r.X+12, r.Y+5, t.label, ColText)
		if c.hovering(r) && c.clicked && a.wardSection != t.id {
			a.wardSection = t.id
			a.previewBase = ""   // each section owns its own preview
			a.iniMenuChar = ""   // close any open character move-to-folder menu
			a.wardDelFolder = "" // close any open folder-delete confirmation
			a.jukeBindFor = ""   // cancel any armed jukebox key-capture
			if t.id == wardSectionBackgrounds {
				a.rebuildBgFav()
			}
		}
		tabX += 126
	}
	// Armed key-capture hint: while a wardrobe bind waits for a key, say so loudly
	// in the header AND that Esc or a click cancels — the per-cell "press..." badge
	// alone was too easy to miss after an accidental arm.
	if a.bindingFor != "" {
		hintX := tabX + 8
		hintW := (panel.X + panel.W - 90 - pad) - hintX - 8
		if hintW > 60 {
			c.LabelClipped(hintX, panel.Y+13, hintW, "Binding "+a.bindingFor+": press a key — or Esc / click to cancel", ColAccent)
		}
	}
	switch a.wardSection {
	case wardSectionBackgrounds:
		a.drawWardrobeBgsBody(panel, w, h)
	case wardSectionIniswaps:
		a.drawWardrobeIniswapsBody(panel, w, h)
	case wardSectionJukebox:
		a.drawWardrobeJukeboxBody(panel, w, h)
	default:
		a.drawWardrobeCharsBody(panel, w, h)
	}
	// Shared drag ghost: a label trailing the cursor shows what's being dragged
	// (a character or a background — whichever section armed the drag).
	if a.iniDragging && a.iniDragChar != "" {
		g := sdl.Rect{X: c.mouseX + 12, Y: c.mouseY + 8, W: c.TextWidth(a.iniDragChar) + 16, H: 22}
		c.Fill(g, sdl.Color{R: 0, G: 0, B: 0, A: 225})
		c.Border(g, ColAccent)
		c.Label(g.X+6, g.Y+3, a.iniDragChar, ColAccent)
	}
	// Shared end-of-frame drag bookkeeping (once per frame, both sections).
	if !c.mouseDown {
		a.iniDragChar = ""
		a.iniDragging = false
	}
	a.iniPrevDown = c.mouseDown
}

// drawWardrobeCharsBody is the Characters section: the navigable wardrobe grid.
func (a *App) drawWardrobeCharsBody(panel sdl.Rect, w, h int32) {
	c := a.ctx
	a.pollIniswap()
	a.pollPreviewEmotes() // try-before-wear: drain a previewed char's emote list
	a.iniHoverChar = ""   // recomputed by the cells this frame (quick-file target)
	// Move-to-folder menu: handle its clicks FIRST so they can't leak to the
	// folder icons/grid underneath (it's painted last, on top).
	if a.iniMenuChar != "" {
		opts := a.iniFolderMenuOpts()
		a.handleIniFolderMenu(iniFolderMenuRect(a.iniMenuAt, len(opts), w, h), opts)
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
	// Default: clicking a favourite SWITCHES you to that character (claims its
	// slot — the CC → PV flow). Tick this to iniswap it instead: wear the look,
	// keep your slot. Per session (resets on restart). Iniswap-by-name now lives
	// on the Iniswaps tab's search box.
	const swapLabel = "Iniswap instead of switching character"
	a.iniSwapMode = c.Checkbox(panel.X+pad, y, swapLabel, a.iniSwapMode)
	c.Tooltip(sdl.Rect{X: panel.X + pad, Y: y, W: 22 + c.TextWidth(swapLabel), H: 16},
		"Off (default): clicking a character switches you to them (takes a slot). On: wears them as an iniswap — no slot taken.")
	y += 32
	a.iniSearch, _ = c.TextField("iniswapsearch", sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH}, a.iniSearch, "Search...")
	// Add any folder name on the current asset base to the wardrobe —
	// no server list required (★ marks saved entries; ★ persists
	// across sessions and servers).
	var addNow bool
	a.iniAdd, addNow = c.TextField("iniswapadd", sdl.Rect{X: panel.X + pad + 240, Y: y, W: 230, H: fieldH}, a.iniAdd, "Add char (or folder/char) to wardrobe...")
	if c.Button(sdl.Rect{X: panel.X + pad + 476, Y: y, W: 60, H: btnH}, "Add") || addNow {
		// "folder/char" files it explicitly; otherwise it joins the folder
		// you're currently viewing ("" at the top level = unfiled).
		raw := strings.TrimSpace(a.iniAdd)
		folder, char := a.iniFolder, raw
		if i := strings.IndexByte(raw, '/'); i >= 0 {
			folder, char = strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[i+1:])
		}
		if a.d.Prefs.AddWardrobe(a.serverKey, char) {
			if folder != "" {
				a.d.Prefs.SetWardrobeFolder(a.serverKey, char, folder)
			}
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
	y += 30
	// One-line orientation — the Wardrobe confused some newcomers. Names the two
	// ways in (★ in Character Select, or the Add box) and the folder/wear flow.
	c.LabelClipped(panel.X+pad, y, panel.W-2*pad,
		"Your saved characters, kept per server. ★ a character in Character Select (or use Add above) to save it; drag a card onto a folder to organise; click a card to wear it.", ColTextDim)
	y += 22

	// --- Folder navigation -----------------------------------------------------
	// Folders are real objects you open. At the top level the grid leads with a
	// folder icon per category; click one to open it (the grid then shows just
	// that folder's characters) and drag a character onto one to file it. Inside
	// a folder, a back button returns to the top (and is itself a drop target
	// that removes a character from the folder). Folders are membership-derived;
	// typing a name below makes a transient folder cell so the FIRST character
	// has something to drop onto. A search spans every folder.
	query := a.iniQ.get(a.iniSearch)
	searching := query != ""

	folderCells := a.wardrobeFolders()
	if nf := strings.TrimSpace(a.iniNewFold); nf != "" { // a just-typed folder is a drop target before it has members
		seen := false
		for _, f := range folderCells {
			if strings.EqualFold(f, nf) {
				seen = true
				break
			}
		}
		if !seen {
			folderCells = append(folderCells, nf)
		}
	}
	charVisible := func(i int) bool {
		// Characters is YOUR wardrobe only — starred or filed into a folder. The
		// full server list lives on the Iniswaps tab (★ there adds it here).
		if !a.iniWardrobe[i] && a.iniFolders[i] == "" {
			return false
		}
		if searching { // search ignores the folder you're in — find anyone
			return strings.Contains(a.iniLower[i], query)
		}
		return strings.EqualFold(a.iniFolders[i], a.iniFolder) // "" = top level = unfiled
	}
	countInFolder := func(name string) int {
		n := 0
		for _, f := range a.iniFolders {
			if strings.EqualFold(f, name) {
				n++
			}
		}
		return n
	}

	if a.wardDelFolder != "" {
		// A folder delete awaits confirmation: the bar replaces the nav row.
		choice := a.drawFolderDeleteConfirm(panel.X+pad, y, panel.W-2*pad, countInFolder(a.wardDelFolder), "characters")
		if choice == folderDeleteWithItems || choice == folderDeleteKeepItems {
			a.d.Prefs.DeleteWardrobeFolder(a.serverKey, a.wardDelFolder, choice == folderDeleteKeepItems)
			if strings.EqualFold(a.iniNewFold, a.wardDelFolder) {
				a.iniNewFold = "" // also drop the transient "new folder" chip if it named this one
			}
			if strings.EqualFold(a.iniFolder, a.wardDelFolder) { // we were inside the deleted folder
				a.iniFolder = ""
				a.iniScroll = 0
			}
			a.wardDelFolder = ""
			a.rebuildIniMenu()
		} else if choice == folderDeleteCancel {
			a.wardDelFolder = ""
		}
	} else {
		switch {
		case searching:
			c.Label(panel.X+pad, y+4, "Search spans every folder — clear it to browse folders", ColTextDim)
		case a.iniFolder == "":
			a.iniNewFold, _ = c.TextField("ininewfold", sdl.Rect{X: panel.X + pad, Y: y, W: 240, H: fieldH}, a.iniNewFold, "New folder name, then drag chars onto it")
			c.LabelClipped(panel.X+pad+250, y+4, panel.X+panel.W-(panel.X+pad+250)-pad, "Drag a character onto a folder to file it · open a folder to see inside · × on a folder deletes it", ColTextDim)
		default:
			back := sdl.Rect{X: panel.X + pad, Y: y, W: 150, H: btnH}
			c.Fill(back, ColPanel)
			if a.iniDragging && c.hovering(back) {
				c.Border(back, ColAccent) // drop here to take the character out of the folder
			} else {
				c.Border(back, ColPanelHi)
			}
			c.Label(back.X+8, back.Y+5, "‹ All folders", ColText)
			c.Tooltip(back, "Back to all folders — or drop a character here to take it out of this folder")
			if c.hovering(back) && c.clicked {
				if a.iniDragging && a.iniDragChar != "" {
					a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniDragChar, "")
					a.rebuildIniMenu()
					c.clicked = false // consume the drop
				} else {
					a.iniFolder = ""
					a.iniScroll = 0
				}
			}
			c.LabelClipped(back.X+back.W+12, y+5, panel.X+panel.W-(back.X+back.W+12)-pad, fmt.Sprintf("%s — %d character(s) · right-click a character to move it", a.iniFolder, countInFolder(a.iniFolder)), ColAccent)
		}
	}
	y += btnH + 8

	// Grid: folder icons (top level only) then character cells. The grid slot
	// drives layout ONLY — character cells always pass their iniList index to
	// drawIniswapCell so the index-keyed cachedPage stays correct (see the
	// cachedPage reorder invariant).
	gridTop := y
	gridW := panel.W - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (iconCell + iconGap)
	if cols < 1 {
		cols = 1
	}
	showFolders := !searching && a.iniFolder == ""
	slots := int32(0)
	if showFolders {
		slots += int32(len(folderCells))
	}
	for i := range a.iniList {
		if charVisible(i) {
			slots++
		}
	}
	cellH := iconCell + iconGap + 14
	contentH := (slots + cols - 1) / cols * cellH
	visibleH := panel.Y + panel.H - gridTop - pad

	a.iniScroll -= c.WheelIn(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.iniScroll = c.VScrollbar("iniscroll", track, a.iniScroll, contentH, visibleH)

	clipPrev, clipHad := c.pushClip(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH})
	slot := int32(0)
	place := func() (sdl.Rect, bool) {
		x := panel.X + pad + (slot%cols)*(iconCell+iconGap)
		yy := gridTop + (slot/cols)*cellH - a.iniScroll
		slot++
		return sdl.Rect{X: x, Y: yy, W: iconCell, H: iconCell}, yy > gridTop-iconCell && yy < panel.Y+panel.H-14
	}
	if showFolders {
		for _, f := range folderCells {
			if cell, vis := place(); vis {
				a.drawIniFolderCell(f, countInFolder(f), cell)
			}
		}
	}
	for i := range a.iniList {
		if !charVisible(i) {
			continue
		}
		if cell, vis := place(); vis {
			a.drawIniswapCell(i, cell, cellClickChar)
		}
	}
	c.popClip(clipPrev, clipHad)

	// Number-key quick-file: hover a character (no field focused, no menu open)
	// and press a digit to file it — 1-9 = that-numbered folder, 0 = unsorted.
	if a.iniHoverChar != "" && c.focusID == "" && a.iniMenuChar == "" {
		switch {
		case c.keyPressed == sdl.K_0:
			a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniHoverChar, "")
			a.rebuildIniMenu()
		case c.keyPressed >= sdl.K_1 && c.keyPressed <= sdl.K_9:
			if folders := a.wardrobeFolders(); int(c.keyPressed-sdl.K_1) < len(folders) {
				a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniHoverChar, folders[c.keyPressed-sdl.K_1])
				a.rebuildIniMenu()
			}
		}
	}

	if a.previewBase != "" {
		a.drawSpritePreview(w, h, true) // wardrobe: try-before-wear emote cycle
		if c.clicked {
			a.previewBase = ""
		}
	}

	// Move-to-folder menu paints last (above the grid + preview). The shared
	// drag ghost and end-of-frame drag cleanup live in the drawIniswapPanel
	// wrapper — one place for both sections.
	if a.iniMenuChar != "" {
		opts := a.iniFolderMenuOpts()
		a.drawIniFolderMenu(iniFolderMenuRect(a.iniMenuAt, len(opts), w, h), opts)
	}
}

// drawWardrobeIniswapsBody is the Iniswaps section: a flat browse of the
// server's full iniswap list. ★ on a cell adds it to your wardrobe (it then
// appears under Characters); clicking the cell wears it to try it on. No folders
// — organizing lives in Characters. Renders from the SAME a.iniList with the
// SAME indices as the Characters grid, so the index-keyed thumbnail cache
// (a.iniPages) stays valid across a tab switch (the cachedPage reorder
// invariant — a different list under the same index keys would paint wrong art).
func (a *App) drawWardrobeIniswapsBody(panel sdl.Rect, w, h int32) {
	c := a.ctx
	a.pollIniswap()     // drain the server list (fetched on open)
	a.iniHoverChar = "" // recomputed by the cells (quick-file target; unused here)

	y := panel.Y + 44
	var iniswapNow bool
	a.iniSearch, iniswapNow = c.TextField("iniswapsearch", sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH}, a.iniSearch, "Iniswap a folder by name, or search the list…")
	c.Tooltip(sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH},
		"Type any character folder on the base and press Enter to iniswap into it — or just type to filter the list below.")
	if iniswapNow { // Enter → legacy iniswap into the typed folder (whether or not it's in the list)
		if name := strings.TrimSpace(a.iniSearch); name != "" {
			a.iniSearch = ""
			a.wearFromMenu(name)
		}
	}
	statusX := panel.X + pad + 250
	switch {
	case a.iniBusy:
		c.Label(statusX, y+4, "Fetching "+iniswapFileName+"...", ColTextDim)
	case len(a.iniServer) == 0:
		// No iniswap.txt on this server (404 or none published): show nothing,
		// calmly — the favourites live on the Characters tab, untouched.
		c.LabelClipped(statusX, y+4, panel.X+panel.W-statusX-pad, "This server doesn't publish an "+iniswapFileName+" list.", ColTextDim)
	default:
		c.Label(statusX, y+4, fmt.Sprintf("%d on this server · ★ adds one to your Characters wardrobe", len(a.iniServer)), ColTextDim)
	}
	y += 36

	// No server list yet → a mini guide for owners (where to put iniswap.txt) and
	// players (they can still wear any folder by name).
	if !a.iniBusy && len(a.iniServer) == 0 {
		base := a.urls.Origin()
		if base == "" {
			base = "https://your-server.com/base/"
		}
		gx, gy := panel.X+pad, y+4
		for _, line := range []string{
			"No iniswap list for this server yet.",
			"",
			"Server owners: drop an " + iniswapFileName + " at your asset base and it'll",
			"be fetched here automatically — one character folder name per line:",
			"",
			base + iniswapFileName,
			"",
			"Players: type any folder name in the search box above and press Enter to",
			"iniswap into it instantly — no published list required.",
		} {
			col := ColTextDim
			if strings.Contains(line, "://") {
				col = ColAccent // the example URL
			}
			c.LabelClipped(gx, gy, panel.W-2*pad, line, col)
			gy += 20
		}
		return
	}

	query := a.iniQ.get(a.iniSearch)
	// The Iniswaps tab is the server's iniswap.txt ONLY — a favourite that isn't
	// a server iniswap (or any favourite on a server with no list) stays out.
	visible := func(i int) bool {
		if i >= len(a.iniServerMem) || !a.iniServerMem[i] {
			return false
		}
		return query == "" || strings.Contains(a.iniLower[i], query)
	}

	gridTop := y
	gridW := panel.W - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (iconCell + iconGap)
	if cols < 1 {
		cols = 1
	}
	slots := int32(0)
	for i := range a.iniList {
		if visible(i) {
			slots++
		}
	}
	cellH := iconCell + iconGap + 14
	contentH := (slots + cols - 1) / cols * cellH
	visibleH := panel.Y + panel.H - gridTop - pad

	a.iniBrowseScroll -= c.WheelIn(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.iniBrowseScroll = c.VScrollbar("inibrowsescroll", track, a.iniBrowseScroll, contentH, visibleH)

	clipPrev, clipHad := c.pushClip(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH})
	slot := int32(0)
	for i := range a.iniList {
		if !visible(i) {
			continue
		}
		x := panel.X + pad + (slot%cols)*(iconCell+iconGap)
		yy := gridTop + (slot/cols)*cellH - a.iniBrowseScroll
		slot++
		if yy > gridTop-iconCell && yy < panel.Y+panel.H-14 {
			a.drawIniswapCell(i, sdl.Rect{X: x, Y: yy, W: iconCell, H: iconCell}, cellClickIniswap)
		}
	}
	c.popClip(clipPrev, clipHad)

	if a.previewBase != "" {
		a.drawSpritePreview(w, h, true) // try-before-wear preview, same as Characters
		if c.clicked {
			a.previewBase = ""
		}
	}
}

// drawFolderShape paints a folder icon — a tab over a body, the member count
// centered, the name beneath — sized to the given cell. The shared VISUAL for
// both wardrobe sections; interaction (open/drop) is the caller's, since
// characters and backgrounds file into different prefs. Pure graphics — no
// cachedPage — so a folder cell is safe to draw at any grid slot (unlike the
// index-keyed character/background cells).
func (a *App) drawFolderShape(cell sdl.Rect, count int, name string, hover, dropTarget bool) {
	c := a.ctx
	tab := sdl.Rect{X: cell.X + 4, Y: cell.Y + 5, W: (cell.W - 8) / 2, H: 9}
	body := sdl.Rect{X: cell.X + 2, Y: cell.Y + 12, W: cell.W - 4, H: cell.H - 14}
	col := ColFolder
	if hover {
		col = ColFolderHi
	}
	c.Fill(tab, col)
	c.Fill(body, col)
	if dropTarget { // a drag is hovering this folder
		c.Border(tab, ColAccent)
		c.Border(body, ColAccent)
	} else {
		c.Border(body, ColPanelHi)
	}
	cnt := fmt.Sprintf("%d", count)
	c.Label(body.X+body.W/2-c.TextWidth(cnt)/2, body.Y+body.H/2-8, cnt, ColText)
	c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, name, ColText)
}

// folderDeleteHit draws the folder's delete (×) button when the icon is hovered
// (and no drag is in flight — during a drag the folder is a drop target) and
// reports a click on it. Shared by both wardrobe sections; the caller opens the
// delete confirmation. The × claims its own click so the folder doesn't open.
func (a *App) folderDeleteHit(cell sdl.Rect, hover bool) bool {
	if !hover || a.iniDragging {
		return false
	}
	c := a.ctx
	x := sdl.Rect{X: cell.X + cell.W - 18, Y: cell.Y + 2, W: 16, H: 16}
	c.Fill(x, sdl.Color{R: 0, G: 0, B: 0, A: 205})
	c.Label(x.X+4, x.Y+1, "×", ColDanger)
	c.Tooltip(x, "Delete this folder")
	return c.hovering(x) && c.clicked
}

// folderDeleteChoice is the outcome of the delete-folder confirmation bar.
type folderDeleteChoice int

const (
	folderDeleteNone folderDeleteChoice = iota
	folderDeleteWithItems
	folderDeleteKeepItems
	folderDeleteCancel
)

// drawFolderDeleteConfirm draws the "delete folder?" bar (replacing the nav row
// while a.wardDelFolder is set) and returns the user's choice this frame. noun
// is "characters"/"backgrounds" for the copy. Esc cancels.
func (a *App) drawFolderDeleteConfirm(x, y, maxW int32, count int, noun string) folderDeleteChoice {
	c := a.ctx
	if c.escPressed {
		return folderDeleteCancel
	}
	label := fmt.Sprintf("Delete folder \"%s\" (%d %s)?", a.wardDelFolder, count, noun)
	c.LabelClipped(x, y+5, 320, label, ColDanger)
	bx := x + 332
	if c.Button(sdl.Rect{X: bx, Y: y, W: 150, H: btnH}, fmt.Sprintf("Delete + %d items", count)) {
		return folderDeleteWithItems
	}
	bx += 156
	if c.Button(sdl.Rect{X: bx, Y: y, W: 130, H: btnH}, "Keep items") {
		return folderDeleteKeepItems
	}
	bx += 136
	if c.Button(sdl.Rect{X: bx, Y: y, W: 80, H: btnH}, "Cancel") {
		return folderDeleteCancel
	}
	return folderDeleteNone
}

// drawIniFolderCell draws one Characters-section folder. Clicking opens it (the
// grid then shows only its characters); dropping a dragged character files it;
// the hover × deletes it.
func (a *App) drawIniFolderCell(name string, count int, cell sdl.Rect) {
	c := a.ctx
	hover := c.hovering(cell)
	a.drawFolderShape(cell, count, name, hover, a.iniDragging && hover)
	if a.folderDeleteHit(cell, hover) {
		a.wardDelFolder = name
		return // the × claimed the click; don't open the folder
	}
	c.Tooltip(cell, "Open the "+name+" folder — or drop a character here to file it")
	if hover && c.clicked {
		if a.iniDragging && a.iniDragChar != "" {
			a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniDragChar, name)
			a.rebuildIniMenu()
			c.clicked = false // consume the drop
		} else {
			a.iniFolder = name
			a.iniScroll = 0
		}
	}
}

// cellClick is what clicking a wardrobe cell does: always iniswap (the Iniswaps
// tab), or run the Characters-tab logic — switch to the character (claim its
// slot), falling back to iniswap when it's taken / not a server char / the box
// is ticked. cellClickChar drives the courtroom Characters tab AND the
// char-select Wardrobe tab, so a favourite switches in both.
type cellClick int

const (
	cellClickIniswap cellClick = iota
	cellClickChar
)

func (a *App) drawIniswapCell(idx int, cell sdl.Rect, click cellClick) {
	c := a.ctx
	name := a.iniList[idx]

	// Hover hint for what a click does here. Registered before the star/key-badge
	// tooltips below, which override it on their own sub-rects (Tooltip = last
	// write wins, and the cursor sits on those rects when it's over them).
	if click == cellClickChar {
		c.Tooltip(cell, "Click to switch to this character (take its slot). Tick \"Iniswap instead\" above to wear it without a slot.")
	} else {
		c.Tooltip(cell, "Click to iniswap into this folder — wear its look, no slot taken.")
	}

	// App-drawer drag: a press on the cell arms this character as the drag
	// candidate; once the cursor travels past iniDragThreshold it becomes a
	// floating ghost that drops onto a folder chip to file it (the chip drop
	// handler and ghost live in drawIniswapPanel). A press that never moves is
	// just a click and still wears/toggles below — the click actions are gated
	// on !a.iniDragging so a drag never also fires them.
	if a.iniPressed && a.iniMenuChar == "" && c.hovering(cell) {
		a.iniDragChar = name
		a.iniDragStart = [2]int32{c.mouseX, c.mouseY}
		a.iniDragging = false
	}
	if a.iniDragChar == name && c.mouseDown {
		dx, dy := c.mouseX-a.iniDragStart[0], c.mouseY-a.iniDragStart[1]
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx+dy > iniDragThreshold {
			a.iniDragging = true
		}
	}

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

	// Folder tag (top-left): the category this character is filed under
	// (right-click the cell to file it into the active folder).
	if idx < len(a.iniFolders) && a.iniFolders[idx] != "" {
		ft := a.iniFolders[idx]
		tw := c.TextWidth(ft) + 6
		if maxW := cell.W - 22; tw > maxW { // leave the top-right for the star
			tw = maxW
		}
		tag := sdl.Rect{X: cell.X + 1, Y: cell.Y + 1, W: tw, H: 15}
		c.Fill(tag, sdl.Color{R: 0, G: 0, B: 0, A: 185})
		c.LabelClipped(tag.X+3, tag.Y+1, tag.W-5, ft, ColAccent)
	}

	// Wardrobe star (top-right of the cell): toggle membership without
	// wearing — the favourites list itself, exactly like lobby stars.
	star := sdl.Rect{X: cell.X + cell.W - 18, Y: cell.Y + 1, W: 17, H: 17}
	starCol := ColTextDim
	if idx < len(a.iniWardrobe) && a.iniWardrobe[idx] {
		starCol = ColStar
	}
	c.Label(star.X+2, star.Y, "★", starCol)
	c.Tooltip(star, "★ add to / remove from your wardrobe (right-click the cell for more)")
	if c.hovering(star) && c.clicked && !a.iniDragging {
		if a.iniWardrobe[idx] {
			a.d.Prefs.RemoveWardrobe(a.serverKey, name)
		} else {
			a.d.Prefs.AddWardrobe(a.serverKey, name)
		}
		a.rebuildIniMenu()
		return // membership toggled; don't also wear it
	}

	// Key badge (bottom-left): the character's bound key on this server,
	// or "+key" on hover. Click arms capture (next plain keypress binds);
	// right-click clears the binding.
	bound := a.charKeyFor(name)
	badgeLabel := bound
	if badgeLabel == "" && c.hovering(cell) {
		badgeLabel = "+key"
	}
	if a.bindingFor == name {
		badgeLabel = "press..."
	}
	if badgeLabel != "" {
		bw := c.TextWidth(badgeLabel) + 8
		badge := sdl.Rect{X: cell.X + 1, Y: cell.Y + cell.H - 17, W: bw, H: 16}
		c.Fill(badge, sdl.Color{R: 0, G: 0, B: 0, A: 190})
		col := ColAccent
		if bound == "" {
			col = ColTextDim
		}
		c.Label(badge.X+4, badge.Y+1, badgeLabel, col)
		if c.hovering(badge) {
			if c.clicked && !a.iniDragging {
				a.bindingFor = name
				c.focusID = "" // capture owns the next keypress outright
				return         // don't also wear it
			}
			if c.rightClicked && bound != "" {
				a.d.Prefs.SetCharKeyBind(a.serverKey, bound, "")
				a.refreshCharKeys()
				return
			}
		}
	}

	// Right-click the cell opens a "move to folder" menu (pick any destination,
	// including a brand-new one). The key badge's own right-click ran first.
	if c.rightClicked && c.hovering(cell) {
		a.iniMenuChar = name
		a.iniMenuAt = [2]int32{c.mouseX, c.mouseY}
		return
	}

	if c.HoverPreview("iniswap:"+name, cell) {
		a.previewBase = a.urls.Emote(name, "normal", courtroom.EmoteIdle)
		a.d.Manager.PrefetchWithFallback(a.previewBase, a.urls.EmoteBare(name, "normal"), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
		a.ensurePreviewEmotes(name)                                                                                                         // try-before-wear: load this character's emotes to cycle
	}
	if c.hovering(cell) {
		a.iniHoverChar = name // number keys 0-9 quick-file the hovered character
		a.warmCharINI(name)   // wearing it = memory hit, not an RTT
		if c.clicked && !a.iniDragging {
			if click == cellClickChar {
				a.wardrobeClick(name) // Characters tab: switch to the char (or iniswap if ticked)
			} else {
				a.wearFromMenu(name) // Iniswaps tab / char-select drawer: iniswap
			}
		}
	}
}

// musicFilterKey memoizes the server-music-list filter so a list of thousands
// isn't re-scanned (and re-lowercased — that allocates) every frame. Keyed by
// the query plus a cheap identity of the list (len + first/last track), which
// changes whenever the server resends SM.
type musicFilterKey struct {
	q           string
	n           int
	first, last string
}

// refreshMusicFilter recomputes the matching track indices for a non-empty
// query, memoized against the query + the list identity. Only this O(N) scan
// (and its ToLower allocs) is gated here; the per-frame call is an O(1) key
// compare and early-return when neither the query nor the list changed.
func (a *App) refreshMusicFilter(query string) {
	n := len(a.sess.Music)
	key := musicFilterKey{q: query, n: n}
	if n > 0 {
		key.first, key.last = a.sess.Music[0], a.sess.Music[n-1]
	}
	if key == a.musicFilterMemo {
		return
	}
	a.musicFilterMemo = key
	a.musicFiltered = a.musicFiltered[:0]
	for i, track := range a.sess.Music {
		if strings.Contains(strings.ToLower(track), query) {
			a.musicFiltered = append(a.musicFiltered, i)
		}
	}
}

// drawMusicVolume is the Music tab's pure volume menu (toggled in place of the
// track list) — Master + the three channels, full sliders + readouts. Reachable
// on a legacy AO2 theme too, since the theme draws drawMusicList directly.
func (a *App) drawMusicVolume(r sdl.Rect) {
	c := a.ctx
	c.LabelClipped(r.X, r.Y, r.W, "Volume — the IC box stays live below, so adjust and keep chatting.", ColTextDim)
	y := r.Y + 30
	master := a.d.Prefs.MasterVolume()
	music, sfx, blip := a.d.Prefs.AudioVolumes()
	row := func(id, label string, val int) int {
		c.Label(r.X, y+6, label, ColText)
		track := sdl.Rect{X: r.X + 80, Y: y + 7, W: r.W - 80 - 56, H: 16}
		nv := int(c.Slider("musicvol:"+id, track, int32(val), 100))
		c.Label(r.X+r.W-48, y+6, strconv.Itoa(nv)+"%", ColTextDim)
		y += 40
		return nv
	}
	if nv := row("master", "Master", master); nv != master {
		a.d.Prefs.SetMasterVolume(nv)
		a.applyAudioVolumes()
	}
	if nv := row("music", "Music", music); nv != music {
		a.d.Prefs.SetAudioVolumes(nv, sfx, blip)
		a.applyAudioVolumes()
	}
	if nv := row("sfx", "SFX", sfx); nv != sfx {
		a.d.Prefs.SetAudioVolumes(music, nv, blip)
		a.applyAudioVolumes()
	}
	if nv := row("blip", "Blip", blip); nv != blip {
		a.d.Prefs.SetAudioVolumes(music, sfx, nv)
		a.applyAudioVolumes()
	}
}

func (a *App) drawMusicList(r sdl.Rect) {
	c := a.ctx
	if a.musicPct < config.MinLogScalePercent { // uninit / stale → match the log
		a.musicPct = a.logPct
	}
	// Ctrl+wheel (or wheel-button) resizes the track text (musicPct). Lives HERE
	// — not just the classic log panel — so it also works on the THEMED courtroom
	// (which draws drawMusicList directly). In classic the panel already took the
	// wheel (wheelTaken), so this no-ops there; no double-step.
	a.zoomWheel(r, &a.musicPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	if c.Button(sdl.Rect{X: r.X, Y: r.Y, W: 90, H: 24}, "Stop music") {
		a.stopMusic()
	}
	// Toggle this panel between the track list and a pure volume-sliders menu.
	// It lives in drawMusicList, which the THEMED courtroom draws directly too, so
	// volume is reachable on a legacy AO2 theme as well (no Extras box needed).
	volLabel := "Volume"
	if a.musicVolMode {
		volLabel = "Track list"
	}
	volRect := sdl.Rect{X: r.X + r.W - 96, Y: r.Y, W: 96, H: 24}
	if c.Button(volRect, volLabel) {
		a.musicVolMode = !a.musicVolMode
	}
	c.Tooltip(volRect, "Swap the track list for volume sliders (Master/Music/SFX/Blip) and back — chat stays live.")
	// Now-Playing indicator: the current track from the server's MC (cleared on
	// stop / area transfer), so you can see and silence what's playing.
	now := ""
	if a.room != nil {
		now = a.room.Scene.MusicTrack
	}
	if now != "" {
		c.LabelClipped(r.X+98, r.Y+6, r.W-98-104, "Now playing: "+musicDisplayName(now), ColAccent)
	} else {
		c.Label(r.X+98, r.Y+6, "Nothing playing", ColTextDim)
	}
	r.Y += 28
	r.H -= 28
	if a.musicVolMode { // pure volume menu in place of the track list
		a.drawMusicVolume(r)
		return
	}

	// Search filter (AO2/webAO parity): type to narrow the server's track list.
	// Memoized so the O(N) scan runs only when the query or the list changes.
	a.musicSearch, _ = c.TextField("musicsearch", sdl.Rect{X: r.X, Y: r.Y, W: r.W - 150, H: fieldH}, a.musicSearch, "Search music…  (Ctrl+wheel resizes)")
	query := strings.ToLower(strings.TrimSpace(a.musicSearch))
	total := len(a.sess.Music)
	shown := total
	if query != "" {
		a.refreshMusicFilter(query)
		shown = len(a.musicFiltered)
	}
	c.Label(r.X+r.W-142, r.Y+5, fmt.Sprintf("%d / %d", shown, total), ColTextDim)
	r.Y += fieldH + 6
	r.H -= fieldH + 6

	// Hover-gated (playtest: the music list scrolled from anywhere).
	if !c.ctrlHeld { // ctrl+wheel resizes text (musicPct), never scrolls
		a.musicScroll -= c.WheelIn(r) * scrollStepPx
	}
	font := c.LogFont(a.musicPct) // independent of the IC log scale
	lineH := int32(font.Height()) + 10
	contentH := int32(shown) * lineH
	bar := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.musicScroll = c.VScrollbar("musicscroll", bar, a.musicScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r) // partial top/bottom row stays inside the panel
	defer c.popClip(clipPrev, clipHad)
	y := r.Y - a.musicScroll
	for vi := 0; vi < shown; vi++ {
		ti := vi
		if query != "" {
			ti = a.musicFiltered[vi]
		}
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-lineH && ti >= 0 && ti < len(a.sess.Music) {
			track := a.sess.Music[ti]
			row := sdl.Rect{X: r.X, Y: y, W: r.W - scrollBarW - scrollBarGap, H: lineH - 4}
			hover := c.hovering(row)
			if hover {
				c.Fill(row, ColPanelHi)
			}
			c.LabelClippedFont(font, r.X+4, y+4, row.W-8, track, ColText)
			if hover {
				c.Tooltip(row, track) // full track name on hover (long titles get clipped)
			}
			if c.ClickedIn(row) { // press+release in-row: a scrollbar-drag release must not play a track
				a.sess.RequestMusic(track)
			}
		}
		y += lineH
	}
	if shown == 0 {
		hint := "Server sent no music list."
		if query != "" {
			hint = "No tracks match your search."
		}
		c.Label(r.X+4, r.Y+6, hint, ColTextDim)
	}
}

// musicDisplayName shortens a track for the Now-Playing line: a streaming URL
// shows its filename (query string stripped), a server track shows as-is.
func musicDisplayName(track string) string {
	if strings.HasPrefix(strings.ToLower(track), "http") {
		if i := strings.IndexByte(track, '?'); i >= 0 {
			track = track[:i]
		}
		if i := strings.LastIndexByte(track, '/'); i >= 0 && i+1 < len(track) {
			track = track[i+1:]
		}
		if track == "" {
			return "streaming link"
		}
	}
	return track
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
		// Only for characters that actually ship one (hasCustomShout).
		if a.sess.Features.Has(protocol.FeatureCustomObjections) && a.hasCustomShout() {
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
	if !a.panelHidden(panelKnobs) && !a.d.Prefs.DragLayoutOn() { // drag-resize mode hides the +/− knobs
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
	// Wardrobe (iniswap): accent + tooltip so it's easy to spot.
	wr := sdl.Rect{X: x, Y: y2, W: 90, H: btnH}
	if c.Button(wr, "Wardrobe") {
		a.openIniswap()
	}
	c.Border(wr, ColAccent)
	c.Tooltip(wr, "Wardrobe / iniswap — swap your character's sprites & emotes")
	x += 96
	if c.Button(sdl.Rect{X: x, Y: y2, W: 100, H: btnH}, "Background") {
		a.openBgPicker()
	}
	x += 106
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
		a.requestDisconnect() // confirm first unless instant-disconnect is set
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
	if c.Button(sdl.Rect{X: x, Y: y2, W: 70, H: btnH}, "Login...") {
		a.openLoginDialog()
	}
	x += 76
	if c.Button(sdl.Rect{X: x, Y: y2, W: 50, H: btnH}, "UI...") {
		a.showUICfg = true
	}
	x += 56
	x = a.drawPosSelect(x, y2, btnH)
	// "Hotkeys" (#96) + "Restyle" (#103/#104) are the trailing convenience buttons,
	// appended after Pos so no existing button shifts. At a narrow window width the
	// long Row 2 would push the PAIR off the right edge, so wrap them down to a
	// fresh row when they wouldn't fit. Everything below keys off y2 (icY = y2 +
	// btnH + …), so bumping it in place drops the IC area and judge strip with it.
	// Constant widths keep this alloc-free.
	const keysW, styleW, btnGap int32 = 90, 84, 6
	if x+keysW+btnGap+styleW > w-pad {
		y2 += btnH + 4
		x = pad
	}
	keysR := sdl.Rect{X: x, Y: y2, W: keysW, H: btnH}
	if c.Button(keysR, "Hotkeys") {
		a.openHotkeyCheatSheet()
	}
	c.Tooltip(keysR, "Show all your hotkeys & custom binds (also F1)")
	x += keysW + btnGap
	styleR := sdl.Rect{X: x, Y: y2, W: styleW, H: btnH}
	if c.Button(styleR, "Restyle") {
		a.openSpriteStyle()
	}
	c.Tooltip(styleR, "Recolour / glow your character on the fly — other AsyncAO players see it")

	// Judge strip (JD grant, or the judge stand when pos-dependent).
	icY := y2 + btnH + 6
	if a.judgeVisible() {
		icY += a.drawJudgeRow(pad, icY)
	}

	// IC input row (height follows the Box knob), led by the AO2 text
	// color selector: a swatch previews the active wire color (MS
	// text_color 0–9), the dropdown names it (AO2's color dropdown). The
	// showname box OVERRIDES the Settings showname for the session.
	fH := a.inputFieldH()
	swatch := sdl.Rect{X: pad, Y: icY, W: 26, H: fH}
	// The selector also offers the extended AsyncAO colours (#98) and the two
	// "fun colour" modes (#79): they sit after the palette so they're picked like
	// any colour instead of being buried in Settings. icColorSelected drives the
	// active row + swatch; applyICColorChoice routes the pick — both shared with
	// the themed row so the two layouts can't drift.
	icSel, sw := a.icColorSelected()
	c.Fill(swatch, sw)
	c.Border(swatch, ColPanelHi)
	if next, changed := c.Dropdown("colordd", sdl.Rect{X: pad + 32, Y: icY, W: colorSelectW, H: fH}, icColorChoices, icSel); changed {
		a.applyICColorChoice(next)
	}
	const shownameBoxW = 140
	nameX := pad + 32 + colorSelectW + 6
	namePlaceholder := a.d.Prefs.SavedShowname()
	if namePlaceholder == "" {
		namePlaceholder = "Showname"
	}
	a.ensureNameOpts()
	snW, snDD := int32(shownameBoxW), int32(0)
	if len(a.nameOpts) > 1 { // a tiny ▾ saved-name picker, fitted INSIDE the box width so nothing downstream shifts
		snDD = 22
		snW -= snDD + 2
	}
	a.shownameOverride, _ = c.TextField("icshownameov", sdl.Rect{X: nameX, Y: icY, W: snW, H: fH}, a.shownameOverride, namePlaceholder)
	if name := a.pickNameDropdown("snpick", sdl.Rect{X: nameX + snW + 2, Y: icY, W: snDD, H: fH}); name != "" {
		a.shownameOverride = name
	}
	// Immediate (AO non-interrupting preanim): the preanim plays without
	// holding back the text. Session toggle; rides the next message via
	// OutgoingMS.Immediate. Vertically centered against the fH-tall inputs.
	const immedW = 70
	immedX := nameX + shownameBoxW + 6
	a.icImmediate = c.Checkbox(immedX, icY+(fH-16)/2, "Immed", a.icImmediate)
	c.Tooltip(sdl.Rect{X: immedX, Y: icY, W: immedW, H: fH}, "Immediate: the preanim plays without holding back the text (non-interrupting preanim)")
	var send bool
	icX := immedX + immedW + 6
	icCounterOn := a.d.Prefs.MessageCounterOn()
	icW := vp.W - (icX - pad)
	if icCounterOn {
		icW -= msgCounterReserve
	}
	icBox := sdl.Rect{X: icX, Y: icY, W: icW, H: fH}
	a.icInput, send = c.TextField("ic", icBox, a.icInput, "Say something in character... (/pair <id>, /unpair, /offset <x> [y], /pos <side>)")
	a.drawMsgCounter(icBox, icCounterOn)
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
		ocW, ocDD := nameW, int32(0)
		if len(a.nameOpts) > 1 { // same tiny ▾ saved-name picker, fitted inside the name box
			ocDD = 22
			ocW -= ocDD + 2
		}
		prevOOC := a.oocName
		a.oocName, _ = c.TextField("oocname", sdl.Rect{X: pad, Y: oocY, W: ocW, H: fH}, a.oocName, "OOC name")
		if name := a.pickNameDropdown("oocpick", sdl.Rect{X: pad + ocW + 2, Y: oocY, W: ocDD, H: fH}); name != "" {
			a.oocName = name
		}
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
	// Missing-asset warning + the DND badge share this row. When DND is on, clip
	// the (left-aligned) toast so it can't run under the right-aligned badge —
	// both stay readable. DND off (the default) draws byte-identical to before.
	toastW := w - 2*pad
	dndMsg := ""
	var dndW int32
	if a.dndOn {
		dndMsg = "● Do Not Disturb — alerts muted (click to undo)"
		dndW = c.TextWidth(dndMsg)
		if gap := dndW + 16; gap < toastW {
			toastW -= gap
		} else {
			toastW = 0
		}
	}
	if a.warnActive() {
		c.LabelClipped(pad, oocY-20, toastW, a.warnLine, ColDanger)
	}
	// Do Not Disturb badge: a persistent reminder while alerts are muted, so a
	// silenced callword never reads as "callwords broken". Click it to turn DND
	// off without opening Settings.
	if a.dndOn {
		bx := w - pad - dndW
		c.Label(bx, oocY-20, dndMsg, ColTierYellow)
		if c.clicked && c.hovering(sdl.Rect{X: bx, Y: oocY - 22, W: dndW, H: 18}) {
			a.setDND(false)
			a.warnLine = "Do Not Disturb off."
			a.warnAt = time.Now()
		}
	}

	if a.showPair {
		a.drawPairPanel(w, h)
	}
}

// previewEmote points the hover preview at an emote: its pre-animation when it
// has one (looped in the preview box — scrub the flourish before sending), else
// the talking sprite (what actually plays). Shared by both emote rows.
func (a *App) previewEmote(char string, e *courtroom.Emote) {
	if e.Preanim != "" && e.Preanim != "-" { // "-" / "" = no preanim (AO convention)
		a.previewBase = a.urls.Emote(char, e.Preanim, courtroom.EmotePreanim)
		a.d.Manager.Prefetch(a.previewBase, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preanim preview)
		return
	}
	a.previewBase = a.urls.Emote(char, e.Anim, courtroom.EmoteTalk)
	a.d.Manager.PrefetchWithFallback(a.previewBase, a.urls.EmoteBare(char, e.Anim), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
}

func (a *App) drawEmoteRow(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	if a.charINIBusy {
		c.Label(r.X, r.Y, "Loading emotes...", ColTextDim)
		return
	}
	me := a.activeCharName() // iniswap override drives emotes + buttons
	useImages := a.d.Prefs.EmoteButtonImagesEnabled()
	a.refreshEmoteView() // build the favourite set + the visible-index list (#77)
	vis := a.emoteVisible

	// Uniform grid so hundreds of emotes page cleanly. The old layout
	// wrapped and `break`ed on overflow, silently dropping every emote past
	// the visible rows (playtest: many-emote characters were unusable). The
	// themed layout already paged (theme_layout.go); this brings the classic
	// row to parity.
	cellW, cellH := emoteBtnCell, emoteBtnCell
	if !useImages {
		cellW, cellH = emoteTextCellW, btnH
	}
	cols := (r.W + emoteGridGap) / (cellW + emoteGridGap)
	if cols < 1 {
		cols = 1
	}
	// Reserve the arrow row ONLY when paging is needed, so normal characters
	// keep the full height. Two-pass and oscillation-free: reserving height
	// only shrinks a page, so a list that needed paging still does.
	gridH := r.H
	if len(vis) > int(cols*gridRows(gridH, cellH)) {
		gridH = r.H - (btnH + 4)
	}
	perPage := int(cols * gridRows(gridH, cellH))
	if perPage < 1 {
		perPage = 1
	}
	a.emotePerPage = perPage // number-key emote select reads this
	pages := (len(vis) + perPage - 1) / perPage
	if pages < 1 {
		pages = 1 // favs-only with no favourites yet: one empty page
	}
	if a.emotePage >= pages {
		a.emotePage = 0
	}
	// Mouse-wheel over the grid pages through emotes (scroll up = previous page,
	// down = next). WheelIn fences the page-level scroll so nothing else moves.
	if pages > 1 {
		if d := c.WheelIn(r); d > 0 && a.emotePage > 0 {
			a.emotePage--
		} else if d < 0 && a.emotePage < pages-1 {
			a.emotePage++
		}
	}
	start := a.emotePage * perPage

	for slot := start; slot < len(vis) && slot < start+perPage; slot++ {
		i := vis[slot] // real index into a.emotes (favs-only filters which show)
		e := &a.emotes[i]
		label := e.Comment
		if label == "" {
			label = e.Anim
		}
		n := int32(slot - start)
		btn := sdl.Rect{
			X: r.X + (n%cols)*(cellW+emoteGridGap),
			Y: r.Y + (n/cols)*(cellH+emoteGridGap),
			W: cellW, H: cellH,
		}
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
		// The favourite ★ is drawn on top and wins the click: a press on the
		// corner star toggles the favourite instead of selecting the emote.
		if a.drawEmoteFavStar(btn, i) {
			// star toggled — swallow this cell's select for the frame
		} else if picked {
			a.selectEmote(i)
		}
		// Full-size preview after a 3 s hover (right-click = instant). If the
		// emote has a pre-animation, scrub THAT (it loops in the preview box) so
		// you can watch the flourish before sending; otherwise the TALKING
		// sprite — what actually plays when this emote is sent.
		if c.HoverPreview("emote:"+e.Anim, btn) {
			a.previewEmote(me, e)
		}
	}
	// Favs-only with nothing starred yet: explain how to get out / add some.
	if len(vis) == 0 && a.d.Prefs.EmoteFavOnlyOn() {
		c.Label(r.X, r.Y+4, "No favourite emotes yet — turn off ★ Favs, then click the ★ on an emote.", ColTextDim)
	}

	// Page arrows + counter (only when more than one page exists). "<"/">"
	// match the Settings theme-cycle buttons.
	if pages > 1 {
		cy := r.Y + r.H - btnH
		if c.Button(sdl.Rect{X: r.X, Y: cy, W: 30, H: btnH}, "<") && a.emotePage > 0 {
			a.emotePage--
		}
		if c.Button(sdl.Rect{X: r.X + 34, Y: cy, W: 30, H: btnH}, ">") && a.emotePage < pages-1 {
			a.emotePage++
		}
		c.Label(r.X+72, cy+6, a.emotePageCounter(a.emotePage+1, pages, len(vis)), ColTextDim)
	}

	// ★ Favs filter toggle (always present, so you can switch it off even with an
	// empty favs-only grid) + Random. Bottom-right, clear of the page arrows.
	a.drawEmoteFavToggle(sdl.Rect{X: r.X + r.W - 158, Y: r.Y + r.H - btnH, W: 64, H: btnH})
	// Swap to a random available character — replaced the old "Random" emote
	// button (redundant with per-send auto-random + the wheel cycling emotes).
	rcb := sdl.Rect{X: r.X + r.W - 92, Y: r.Y + r.H - btnH, W: 92, H: btnH}
	if c.Button(rcb, "Rand char") {
		a.randomChar()
	}

	if a.previewBase != "" {
		a.drawSpritePreview(vp.X+vp.W, vp.Y+vp.H, false)
		if c.clicked {
			a.previewBase = ""
		}
	}
}

// selectEmote picks emote i (shared by click, the Random button, and the
// number-key shortcut): warms its pressed button art + the sprites the next
// message will need, and refocuses the IC input (AO2 focus_ic_input).
// Out-of-range indices are ignored.
func (a *App) selectEmote(i int) {
	if i < 0 || i >= len(a.emotes) {
		return
	}
	a.emoteIdx = i
	me := a.activeCharName()
	// Warm BOTH button states at HIGH: the _on (pressed) art the selected
	// cell wants, AND the _off art it falls back to while _on streams in —
	// so a freshly-pressed cell shows its sprite immediately instead of
	// blanking to the text-clipped fallback (the "button didn't load on
	// press" report).
	a.d.Manager.Prefetch(a.urls.EmoteButton(me, i+1, true), assets.AssetTypeEmoteButton, network.PriorityHigh)  // AssetType: EmoteButton (on)
	a.d.Manager.Prefetch(a.urls.EmoteButton(me, i+1, false), assets.AssetTypeEmoteButton, network.PriorityHigh) // AssetType: EmoteButton (off fallback)
	a.speculateEmote(me, &a.emotes[i])
	a.ctx.FocusField("ic")
}

// emotePageCounter returns the "page x/y · N emotes" label, memoized so the
// per-frame emote-grid draw allocates nothing while paging is stable — the
// fmt.Sprintf runs only when the page, page count, or emote total changes (the
// same memoize-on-change idiom as the generation-cached texture pages).
func (a *App) emotePageCounter(page, pages, n int) string {
	if key := [3]int{page, pages, n}; a.emotePageLabel == "" || key != a.emotePageLabelKey {
		a.emotePageLabel = fmt.Sprintf("page %d/%d · %d emotes", page, pages, n)
		a.emotePageLabelKey = key
	}
	return a.emotePageLabel
}

// nextRandomEmote picks a random emote index for auto-random mode. With more
// than one emote it returns a DIFFERENT index than cur (uniform over the rest,
// via an unbiased skip-current), so every send visibly changes the sprite;
// with 0 or 1 it can't vary and returns cur (the caller no-ops). Pure for tests.
func nextRandomEmote(n, cur int) int {
	if n <= 1 {
		return cur
	}
	if cur < 0 || cur >= n { // nothing valid selected: any emote will do
		return rand.IntN(n)
	}
	i := rand.IntN(n - 1)
	if i >= cur {
		i++ // skip the current index — guarantees a different emote, no bias
	}
	return i
}

// randomEmoteForSend rolls a fresh emote on send for auto-random mode, reusing
// selectEmote so the button art + sprites warm exactly as a manual pick would,
// and scrolls the grid to it. It varies among the VISIBLE emotes, so with the
// favs-only filter on it rolls within your favourites (and with it off — the
// default — `vis` is the whole list, so behaviour is unchanged). No-op with 0 or
// 1 visible emote.
func (a *App) randomEmoteForSend() {
	a.refreshEmoteView()
	vis := a.emoteVisible
	if len(vis) <= 1 {
		return
	}
	cur := -1 // current selection's slot in the visible list (-1 if not visible)
	for k, idx := range vis {
		if idx == a.emoteIdx {
			cur = k
			break
		}
	}
	slot := nextRandomEmote(len(vis), cur)
	i := vis[slot]
	if i == a.emoteIdx {
		return
	}
	a.selectEmote(i)
	if a.emotePerPage > 0 {
		a.emotePage = slot / a.emotePerPage
	}
}

// cycleEmote steps the selection by delta with wrap and scrolls its page into
// view — keyboard-only emote stepping during RP (the Ctrl+emote-cycle hotkey).
// Unlike Random (which jumps anywhere), this walks the list in order. No-op
// with no emotes; selectEmote clamps the index and refocuses the IC input.
const (
	msgCounterReserve = int32(48) // px reserved right of the IC box for the counter
	msgCounterCap     = 256       // typical AO server IC cap — past it, warn in red
)

// drawMsgCounter draws the live IC character count in the gap reserved to the
// right of the input (M5; ON by default, Settings-toggleable). The count string
// is cached and reformatted only when the length changes, so the courtroom frame
// stays 0-alloc; past the typical server cap it turns red (the line may truncate).
func (a *App) drawMsgCounter(input sdl.Rect, on bool) {
	if !on {
		return
	}
	c := a.ctx
	n := utf8.RuneCountInString(a.icInput)
	if n != a.icCountN {
		a.icCountN, a.icCountStr = n, strconv.Itoa(n)
	}
	col := ColTextDim
	if n > msgCounterCap {
		col = ColDanger
	}
	c.Label(input.X+input.W+6, input.Y+(input.H-14)/2, a.icCountStr, col)
}

// randomShowname swaps the active showname to a random saved preset (M6, the
// Ctrl+H hotkey). It sets the in-courtroom override, which effectiveShowname
// reads, so the next message carries it.
func (a *App) randomShowname() {
	presets := a.d.Prefs.ShownameList()
	if len(presets) == 0 {
		a.warnLine = "No showname presets saved — add some in Settings → General"
		a.warnAt = a.now()
		return
	}
	a.shownameOverride = presets[rand.IntN(len(presets))]
	a.warnLine = clampLine("Showname → " + a.shownameOverride)
	a.warnAt = a.now()
}

// cycleShowname swaps the active showname to the NEXT saved preset (Ctrl+B),
// wrapping around — a quick in-courtroom flip through your shownames.
func (a *App) cycleShowname() {
	presets := a.d.Prefs.ShownameList()
	if len(presets) == 0 {
		a.warnLine = "No showname presets saved — add some in Settings → General"
		a.warnAt = a.now()
		return
	}
	cur := a.effectiveShowname()
	next := 0
	for i, p := range presets {
		if strings.EqualFold(p, cur) {
			next = (i + 1) % len(presets)
			break
		}
	}
	a.shownameOverride = presets[next]
	a.warnLine = clampLine("Showname → " + a.shownameOverride)
	a.warnAt = a.now()
}

func (a *App) cycleEmote(delta int) {
	a.refreshEmoteView()
	vis := a.emoteVisible
	n := len(vis)
	if n == 0 {
		return
	}
	// Walk the VISIBLE list in order (so favs-only steps through favourites).
	// Start from the current selection's slot, or -1 ("nothing selected") which
	// the wrap tolerates.
	cur := -1
	for k, idx := range vis {
		if idx == a.emoteIdx {
			cur = k
			break
		}
	}
	slot := ((cur+delta)%n + n) % n
	a.selectEmote(vis[slot])
	if a.emotePerPage > 0 {
		a.emotePage = slot / a.emotePerPage
	}
}

// gridRows is how many cellH-tall rows (plus emoteGridGap) fit in h, ≥ 1.
func gridRows(h, cellH int32) int32 {
	if rows := (h + emoteGridGap) / (cellH + emoteGridGap); rows > 1 {
		return rows
	}
	return 1
}

// drawEmoteImageButton draws one emotions/button<N> cell, preferring the
// state-correct art and falling back to the _off art (selection ring still
// reads) and finally a text chip while textures stream in. Reports clicks.
// Pages resolve through the generation-keyed caches — a full emote grid
// redraw costs zero store locks while nothing was uploaded/evicted (the
// same trick as the char grid; this was the last per-frame LRU storm).
func (a *App) drawEmoteImageButton(btn sdl.Rect, me string, i int, selected bool, label string) bool {
	c := a.ctx
	base := a.urls.EmoteButton(me, i+1, selected)
	cache, gen := &a.emoteBtnOff, &a.emoteBtnOffGen
	if selected {
		cache, gen = &a.emoteBtnOn, &a.emoteBtnOnGen
	}
	page, ok := a.cachedPage(cache, gen, len(a.emotes), i, base)
	if !ok {
		a.demandAsset(&a.emoteAsk, len(a.emotes), i, base, assets.AssetTypeEmoteButton) // AssetType: EmoteButton
		if selected {
			page, ok = a.cachedPage(&a.emoteBtnOff, &a.emoteBtnOffGen, len(a.emotes), i, a.urls.EmoteButton(me, i+1, false))
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
	query := a.pairQ.get(a.pairSearch)
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
	c.Label(rx, ry, "Both sides must pair with each other;", ColTextDim)
	c.Label(rx, ry+18, "applies from your next message.", ColTextDim)
	ry += 42

	// Offset ghost editor: drag your sprite live; partner shows as a
	// translucent ghost at their last-known placement.
	pv := sdl.Rect{X: rx, Y: ry, W: panel.W/2 - 2*pad, H: panel.Y + panel.H - pad - ry}
	if pv.H >= ghostMinHeightPx {
		a.drawPairGhost(pv)
	}
}

// ghostMinHeightPx is the smallest useful ghost stage; below it the panel
// keeps just the numeric controls.
const ghostMinHeightPx = 70

// ghostAlpha is the partner ghost's translucency.
const ghostAlpha = 110

// drawPairGhost renders the live offset editor: a miniature stage, the
// pair partner translucent at THEIR offsets (from the last paired
// message), and your idle sprite at YOUR offsets — drag it to set them
// (the numeric rows above mirror live). Same offset math as the real
// viewport: percent of stage width/height.
func (a *App) drawPairGhost(pv sdl.Rect) {
	c := a.ctx
	c.Fill(pv, sdl.Color{R: 12, G: 12, B: 16, A: 255})
	c.Border(pv, ColPanelHi)

	// Partner ghost first (behind), then me.
	if a.pairWith >= 0 && a.pairWith < len(a.sess.Chars) {
		name := a.sess.Chars[a.pairWith].Name
		// The scene's pair layer knows their REAL offsets once a paired
		// message arrived; before that they stand centered.
		gx, gy := 0, 0
		if sc := &a.room.Scene; sc.PairActive && strings.EqualFold(sc.Pair.Name, name) {
			gx, gy = sc.Pair.OffsetX, sc.Pair.OffsetY
		}
		a.drawGhostSprite(pv, name, gx, gy, false, ghostAlpha)
	}
	if me := a.myCharName(); me != "" {
		a.drawGhostSprite(pv, me, a.pairOffX, a.pairOffY, a.pairFlip, 255)
	}
	c.Label(pv.X+4, pv.Y+2, "drag your sprite to place — arrow keys nudge 1%", ColTextDim)

	// Drag: anywhere in the stage moves YOUR sprite (it is the only
	// thing being edited here — no hit-testing pixel art).
	pressed := c.mouseDown && !a.ghostPrev
	a.ghostPrev = c.mouseDown
	if pressed && c.hovering(pv) {
		a.ghostDrag = true
		a.ghostStart = [2]int32{c.mouseX, c.mouseY}
		a.ghostBase = [2]int{a.pairOffX, a.pairOffY}
	}
	if !c.mouseDown {
		a.ghostDrag = false
	}
	if a.ghostDrag && pv.W > 0 && pv.H > 0 {
		a.pairOffX = clampOffset(a.ghostBase[0] + int(c.mouseX-a.ghostStart[0])*100/int(pv.W))
		a.pairOffY = clampOffset(a.ghostBase[1] + int(c.mouseY-a.ghostStart[1])*100/int(pv.H))
		a.persistPairPrefs()
	}
	// Arrow keys nudge your offset 1% for fine placement — only with no text
	// field focused (otherwise arrows move the text caret). Up = toward the
	// top of the stage (lower Y), matching the drag.
	if c.focusID == "" {
		nudged := true
		switch c.keyPressed {
		case sdl.K_LEFT:
			a.pairOffX = clampOffset(a.pairOffX - 1)
		case sdl.K_RIGHT:
			a.pairOffX = clampOffset(a.pairOffX + 1)
		case sdl.K_UP:
			a.pairOffY = clampOffset(a.pairOffY - 1)
		case sdl.K_DOWN:
			a.pairOffY = clampOffset(a.pairOffY + 1)
		default:
			nudged = false
		}
		if nudged {
			a.persistPairPrefs()
		}
	}
}

// drawGhostSprite draws one character's idle at offset% of the stage,
// sized like the real viewport sizes sprites (full stage height). The
// texture's alpha-mod restores immediately — pages are shared with the
// live viewport.
func (a *App) drawGhostSprite(pv sdl.Rect, name string, offX, offY int, flip bool, alpha uint8) {
	c := a.ctx
	base := a.urls.Emote(name, "normal", courtroom.EmoteIdle)
	page, ok := a.d.Store.Get(base)
	if !ok || len(page.Frames) == 0 || page.H == 0 {
		// Warm it once per (panel, character) — not per frame.
		if a.ghostWarm[name] != base {
			if a.ghostWarm == nil {
				a.ghostWarm = map[string]string{}
			}
			if len(a.ghostWarm) < ghostWarmCap {
				a.ghostWarm[name] = base
				a.d.Manager.PrefetchWithFallback(base, a.urls.EmoteBare(name, "normal"), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (ghost editor)
			}
		}
		c.Label(pv.X+pv.W/2-c.TextWidth(name)/2, pv.Y+pv.H/2, name, ColTextDim)
		return
	}
	dst := sdl.Rect{H: pv.H, W: pv.H * page.W / page.H}
	dst.X = pv.X + (pv.W-dst.W)/2 + int32(offX)*pv.W/100
	dst.Y = pv.Y + int32(offY)*pv.H/100
	tex := page.Frames[pageFrameLoop(page, a.themeElapsed())]
	_ = tex.SetAlphaMod(alpha)
	if flip {
		_ = c.Ren.CopyEx(tex, nil, &dst, 0, nil, sdl.FLIP_HORIZONTAL)
	} else {
		_ = c.Ren.Copy(tex, nil, &dst)
	}
	_ = tex.SetAlphaMod(255)
}

// ghostWarmCap bounds the ghost editor's prefetch dedupe table.
const ghostWarmCap = 16

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
// funColor applies the optional outgoing message-colour modes (M61, both off by
// default): rainbow wraps the text in the \cr inline-colour markup (per-rune
// palette cycle), else random swaps the message's palette colour. Pure (the
// random index is supplied) so the rule is testable; blank/space sends are left
// alone, and rainbow wins if both are set.
func funColor(text string, color, ext int, rainbow, random bool, randIdx int) (string, int) {
	if text == "" || text == " " {
		return text, color
	}
	switch {
	case rainbow:
		return "\\cr" + text, color
	case random:
		return text, randIdx
	case ext >= 0 && ext < render.ExtColorCount():
		// Extended AsyncAO colour (#98): the exact colour rides as inline markup
		// (AsyncAO renders it); the wire text_color is the nearest standard index
		// so stock AO2 clients still see a sensible colour.
		e := render.ExtColorAt(ext)
		return "\\c" + string(e.Code) + text, e.Wire
	}
	return text, color
}

func (a *App) sendIC(shout int) {
	text := strings.TrimSpace(a.icInput)
	if cmdHandled := a.handleChatCommand(text); cmdHandled {
		a.icInput = ""
		return
	}
	// Blankpost: Enter on an empty input sends the AO single-space
	// message — your sprite plays with no text (the RP "just show my
	// character" convention; truly empty messages get server-rejected).
	if text == "" && shout == 0 {
		text = " "
	}
	if a.sess.MyCharID < 0 {
		return
	}
	// AO2-Client chat_ratelimit parity: drop sends inside the window.
	if _, _, rateMs := a.d.Prefs.Timing(); rateMs > 0 &&
		time.Since(a.lastICSend) < time.Duration(rateMs)*time.Millisecond {
		return
	}
	// Auto-random emote (OFF by default): roll a fresh sprite from this char's
	// set on every accepted send — for people who'd rather not click the grid,
	// and to surface emotes they'd never pick. Behaviorally identical to picking
	// that emote by hand (same Anim/Preanim/SFX/Mod ride along below). Placed
	// after the command/blankpost/rate-limit guards so a dropped send won't roll.
	if a.d.Prefs.RandomEmoteOn() {
		a.randomEmoteForSend()
	}
	emote := courtroom.Emote{Anim: "normal", Preanim: "-", DeskMod: protocol.DeskShow}
	if a.emoteIdx >= 0 && a.emoteIdx < len(a.emotes) {
		emote = a.emotes[a.emoteIdx]
	}
	hasPre := emote.Preanim != "" && emote.Preanim != "-"
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
	// M61 fun colour: rainbow (\cr prefix), an extended AsyncAO colour (\c<letter>
	// + nearest-standard wire fallback, #98), or a random palette colour per message.
	text, msgColor := funColor(text, a.icColor, a.icExtColor-1, a.d.Prefs.RainbowMessagesOn(), a.d.Prefs.RandomMessageColorOn(), rand.IntN(render.TextColorCount))
	// Transmitted sprite style (#103): append the invisible zero-width marker at
	// the END of the text — other AsyncAO clients decode + render it on this
	// character; AO2/webAO see nothing. End placement keeps the visible text intact
	// if a server length-limits the message (worst case: the style is dropped).
	if marker := a.mySpriteStyle().EncodeMarker(); marker != "" {
		text += marker
	}
	out := protocol.OutgoingMS{
		DeskMod:    emote.DeskMod, // the emote's char.ini desk_mod (was hardcoded 1, so no-desk emotes never hid the desk)
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
		TextColor: msgColor, // swatch cycler, or M61 random-per-message colour
		Showname:  a.effectiveShowname(),
		PairWith:  a.pairWith,
		PairOrder: a.pairOrder,
		OffsetX:   a.pairOffX,
		OffsetY:   a.pairOffY,
		Flip:      a.pairFlip,
		Immediate: a.icImmediate, // non-interrupting preanim (IC-row toggle)
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
		// applySide lowercases/trims and forwards /pos to the server, so typing it
		// in the IC box now moves you instantly too (matching the OOC-box command).
		a.applySide(strings.TrimPrefix(text, "/pos "))
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
func renderRaster(a *App, sc *courtroom.Scene, wrapW int32, skinned bool, pct int) (*render.MessageRaster, error) {
	// The chat zoom font: rebuilt only when the Text knob changes. The
	// theme's "message" color replaces only AO's DEFAULT color (code 0)
	// — explicit message colors (green/red/...) always win — and only
	// while the theme's own chatbox skin is drawn: theme colors assume
	// their skin (black text on white paper), not our dark flat panel.
	col := render.TextColor(sc.TextColor)
	if skinned && sc.TextColor == 0 && a.themeHasMsg {
		col = a.themeMsgCol
	}
	// Per-message font pick at the given scale (pct): the override chain's first
	// covering font (CJK fallback), the embedded font otherwise. The live chatbox
	// passes a.chatPct; the export passes a size fitted to the capture frame.
	font := a.ctx.ChatFontFor(pct, sc.MessageText)
	// Emoji present → the per-glyph fallback raster: text runes from `font`, emoji
	// runes from the color emoji face, baseline-aligned. Gated on a cheap byte scan
	// so PLAIN messages (the overwhelming common case) never reach here and stay on
	// the untouched fast paths below — zero change for them.
	if render.NeedsEmojiFallback(sc.MessageText) {
		a.ensureEmojiFontLoad() // kick off the one off-thread system-emoji read
		var spans []render.ColorSpan
		if sceneNeedsStyled(sc.MessageStyles) {
			spans = buildColorSpans(sc.MessageStyles, col)
		} else {
			spans = []render.ColorSpan{{Len: len([]rune(sc.MessageText)), Color: col}}
		}
		return render.RasterizeFallback(a.ctx.Ren, font, a.ctx.EmojiFont(pct), sc.MessageText, spans, wrapW)
	}
	// Inline \cN colors → the multi-color span raster; plain messages keep the
	// untouched single-color path (col is their whole-message color).
	if sceneNeedsStyled(sc.MessageStyles) {
		return render.RasterizeStyled(a.ctx.Ren, font, sc.MessageText, buildColorSpans(sc.MessageStyles, col), wrapW)
	}
	return render.Rasterize(a.ctx.Ren, font, sc.MessageText, wrapW, col)
}

// sceneNeedsStyled reports whether any style run has a non-default color or
// bold/italic (so the message needs the multi-span raster). A plain message has
// a single default, non-bold, non-italic run (or none).
func sceneNeedsStyled(styles []courtroom.StyleRun) bool {
	for _, s := range styles {
		if s.Color != courtroom.ColorDefault || s.Bold || s.Italic {
			return true
		}
	}
	return false
}

// icColorChoices is the IC colour dropdown (#79/#98): the wire palette, then the
// extended AsyncAO colours (#98), then the two "fun colour" modes — so every
// colour is picked from one list rather than hidden in Settings. Built once at
// package init — the IC input row reads it every frame, so it must not allocate.
// extColorFirst is where the extended entries start; icColorRainbowIdx /
// icColorRandomIdx are the trailing fun-mode positions (all len-derived so they
// shift correctly if the palettes change).
var (
	extColorFirst     = len(render.TextColorNames())
	icColorRainbowIdx = extColorFirst + render.ExtColorCount()
	icColorRandomIdx  = icColorRainbowIdx + 1
	icColorChoices    = buildICColorChoices()
)

// buildICColorChoices assembles the dropdown label list once at init: standard
// palette names, extended colour names, then Rainbow/Random.
func buildICColorChoices() []string {
	out := append([]string{}, render.TextColorNames()...)
	for i := 0; i < render.ExtColorCount(); i++ {
		out = append(out, render.ExtColorAt(i).Name)
	}
	return append(out, "Rainbow", "Random")
}

// applyICColorChoice routes an IC colour-dropdown selection (shared by the
// classic and themed rows so they can't drift). The four kinds are mutually
// exclusive — picking one clears the others. Critically, only a standard 0..8
// pick touches a.icColor (the wire text_color); extended colours live in
// a.icExtColor and ship as inline markup with a nearest-standard wire fallback,
// so the wire field never leaves 0..8 (#98).
func (a *App) applyICColorChoice(next int) {
	switch {
	case next == icColorRainbowIdx:
		a.d.Prefs.SetRainbowMessages(true)
		a.d.Prefs.SetRandomMessageColor(false)
		a.icExtColor = 0
	case next == icColorRandomIdx:
		a.d.Prefs.SetRandomMessageColor(true)
		a.d.Prefs.SetRainbowMessages(false)
		a.icExtColor = 0
	case next >= extColorFirst && next < extColorFirst+render.ExtColorCount():
		a.icExtColor = next - extColorFirst + 1 // 1-based; 0 = none
		a.d.Prefs.SetRainbowMessages(false)
		a.d.Prefs.SetRandomMessageColor(false)
	default: // standard palette 0..8
		a.icColor = next
		a.icExtColor = 0
		a.d.Prefs.SetRainbowMessages(false)
		a.d.Prefs.SetRandomMessageColor(false)
	}
}

// icColorSelected returns the active dropdown row and its swatch preview colour
// for the current IC colour state, so both layouts highlight + preview the same
// thing. Reads only fields/prefs the IC row already reads each frame (0 alloc).
func (a *App) icColorSelected() (sel int, swatch sdl.Color) {
	switch {
	case a.d.Prefs.RainbowMessagesOn():
		return icColorRainbowIdx, chatRainbow[0]
	case a.d.Prefs.RandomMessageColorOn():
		return icColorRandomIdx, ColTextDim
	case a.icExtColor > 0 && a.icExtColor <= render.ExtColorCount():
		return extColorFirst + a.icExtColor - 1, render.ExtColorAt(a.icExtColor - 1).Color
	default:
		return a.icColor, render.TextColor(a.icColor)
	}
}

// chatRainbow is the palette inline rainbow (\cr) cycles through, per rune.
var chatRainbow = []sdl.Color{
	{R: 255, G: 80, B: 80, A: 255},   // red
	{R: 255, G: 165, B: 0, A: 255},   // orange
	{R: 250, G: 235, B: 60, A: 255},  // yellow
	{R: 90, G: 220, B: 90, A: 255},   // green
	{R: 80, G: 200, B: 255, A: 255},  // blue
	{R: 185, G: 120, B: 255, A: 255}, // violet
}

// buildColorSpans resolves the courtroom style runs into render color spans:
// ColorDefault → the message color, a palette index → that color, and rainbow
// expands into per-rune spans that flow continuously across the message.
func buildColorSpans(styles []courtroom.StyleRun, def sdl.Color) []render.ColorSpan {
	out := make([]render.ColorSpan, 0, len(styles))
	rb := 0
	for _, s := range styles {
		switch {
		case s.Color == courtroom.ColorRainbow:
			for k := 0; k < s.Len; k++ {
				out = append(out, render.ColorSpan{Len: 1, Color: chatRainbow[rb%len(chatRainbow)], Bold: s.Bold, Italic: s.Italic})
				rb++
			}
		case s.Color == courtroom.ColorDefault:
			out = append(out, render.ColorSpan{Len: s.Len, Color: def, Bold: s.Bold, Italic: s.Italic})
		case s.Color >= courtroom.ColorExtBase: // extended AsyncAO color (#98): resolve by inline letter
			col, ok := render.ExtColorByCode(byte(s.Color - courtroom.ColorExtBase))
			if !ok {
				col = def // unknown code → the message's own colour
			}
			out = append(out, render.ColorSpan{Len: s.Len, Color: col, Bold: s.Bold, Italic: s.Italic})
		default:
			out = append(out, render.ColorSpan{Len: s.Len, Color: render.TextColor(s.Color), Bold: s.Bold, Italic: s.Italic})
		}
	}
	return out
}
