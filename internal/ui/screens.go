package ui

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

const (
	pad      int32 = 8 // base unit, rebased ~0.8 toward classic-Windows compactness (was 10)
	rowH     int32 = 22
	fieldH   int32 = 21
	btnH     int32 = 22
	iconCell int32 = 64
	iconGap  int32 = 8
	// previewZoomMax caps the preview magnifier (× the fit scale).
	previewZoomMax = 8
	// Playtest sizing (consistent previews): every character previews at the
	// SAME height — the Settings default (config.PreviewHeightPx; shipped at
	// 384 px, double AO's native 192 stage: "it's really tiny" + re-dragging
	// it each session "is stinky") — regardless of source resolution. The
	// corner grip still resizes per session within [previewMinH, previewMaxH]
	// (bounds shared with the Settings slider via config); previewMaxW /
	// previewMinW keep extreme aspects on screen; the previewCaptionH strip
	// reports "source × shown-scale" AO2-style.
	previewMinH     int32 = config.MinPreviewHeightPx
	previewMaxH     int32 = config.MaxPreviewHeightPx
	previewMaxW     int32 = 960 // fits 4:3 art at the 720 px height cap (was 560; the window clamp still applies)
	previewMinW     int32 = 48
	previewCaptionH int32 = 20
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
	// cleanOOCMinH floors the new-layout OOC box (and the IC log above it) so neither is crushed;
	// cleanGapPx is the breathing room between stacked clean-layout panels.
	cleanOOCMinH int32 = 96
	cleanGapPx   int32 = 6
	cleanHeaderH int32 = 20 // titled-header bar height on clean-layout boxes
)

// --- LOBBY ------------------------------------------------------------------------

func (a *App) drawLobby(w, h int32) {
	a.pollLobbyFetch()
	a.pollPing() // drain connect-time probes (no-op unless a sweep is running)
	c := a.ctx
	a.drawScreenBackdrop(w, h, "lobbybackground")
	c.Heading(pad, pad, "AsyncAO", ColText)
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
	// "What's New" — the full, always-available version history (the post-update
	// modal only shows the latest release's notes). A green unread dot nags after an
	// app update until you open it (#23).
	wnBtn := sdl.Rect{X: w - 710 - pad, Y: pad, W: 110, H: btnH}
	if c.Button(wnBtn, "What's New") {
		a.prevScreen = ScreenLobby
		a.screen = ScreenChangelog
		a.markChangelogSeen() // opening it clears the unread dot
	}
	if a.changelogUnread() {
		a.drawUnreadDot(wnBtn)
	}
	// Log browser: search your saved transcripts (any server, any session).
	if c.Button(sdl.Rect{X: w - 830 - pad, Y: pad, W: 110, H: btnH}, "Logs") {
		a.prevScreen = ScreenLobby
		a.openLogBrowser()
		a.screen = ScreenLogs
	}
	// Privacy + Glossary: the two things a newcomer most needs — what the server can see,
	// and what the AO jargon means. They sit CENTRE STAGE in the header (larger + accent-
	// styled) instead of buried in the right-hand utility cluster, and replace the old
	// generic "Help" button — each opens its Help-screen tab. Centred in the gap between the
	// title and that cluster; the width shrinks and anchors after the title on a narrow
	// window so the pair never overlaps either side.
	const (
		helpBtnWMax     = int32(132) // prominent width when there's room (fullscreen)
		helpBtnWMin     = int32(96)  // floor so "Glossary" stays readable when squeezed
		helpBtnGap      = int32(12)
		headerTitleZone = int32(180) // reserved width for the "AsyncAO" heading on the left
	)
	titleRight := pad + headerTitleZone
	utilLeft := w - 830 - pad // Logs is the leftmost utility button
	helpBtnW := helpBtnWMax
	if zoneW := utilLeft - titleRight; helpBtnW*2+helpBtnGap > zoneW {
		if helpBtnW = (zoneW - helpBtnGap) / 2; helpBtnW < helpBtnWMin {
			helpBtnW = helpBtnWMin
		}
	}
	pairW := helpBtnW*2 + helpBtnGap
	helpX := (titleRight+utilLeft)/2 - pairW/2
	if helpX < titleRight { // narrow window: anchor just after the title
		helpX = titleRight
	}
	helpY, helpH := pad-2, btnH+4 // a touch taller than the utility buttons
	if c.ButtonCol(sdl.Rect{X: helpX, Y: helpY, W: helpBtnW, H: helpH}, "Privacy", ColPanel, ColPanelHi, ColAccent, ColAccent) {
		a.prevScreen = ScreenLobby
		a.openHelp(1)
	}
	if c.ButtonCol(sdl.Rect{X: helpX + helpBtnW + helpBtnGap, Y: helpY, W: helpBtnW, H: helpH}, "Glossary", ColPanel, ColPanelHi, ColAccent, ColAccent) {
		a.prevScreen = ScreenLobby
		a.openHelp(0)
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

	// Keyboard navigation (#18): arrows move the selection through joinable servers, Enter joins —
	// when no text field is focused. The selection drives the same expand + scroll as a click.
	if c.focusID == "" {
		switch c.keyPressed {
		case sdl.K_DOWN:
			if nx := a.nextJoinableServer(a.selServer, 1); nx >= 0 {
				a.selectServerRow(nx, w)
				a.scrollServerIntoView(nx, listTop, h)
			}
			c.keyPressed = 0
		case sdl.K_UP:
			if nx := a.nextJoinableServer(a.selServer, -1); nx >= 0 {
				a.selectServerRow(nx, w)
				a.scrollServerIntoView(nx, listTop, h)
			}
			c.keyPressed = 0
		case sdl.K_RETURN, sdl.K_KP_ENTER:
			if a.selServer >= 0 && a.selServer < len(a.servers) && a.servers[a.selServer].Joinable() {
				e := &a.servers[a.selServer]
				a.Connect(e.Name, e.WebSocketURL())
				return
			}
		}
	}

	// Transient warning banner (bottom of the lobby): the app starts here, so
	// startup notices — e.g. the corrupt-prefs quarantine (#3) — must draw on
	// the lobby too, not only the courtroom/char-select screens.
	if a.warnActive() {
		c.LabelClipped(pad, h-24, w-2*pad, a.warnLine, ColDanger)
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
	if hover { // #17: hover shows the URL + description ("MOTD") without clicking to expand
		a.serverRowTooltip(row, e)
	}

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
		a.selectServerRow(idx, w) // select + (re)build the cached description lines/links
	}
}

// maxDescLinks bounds the clickable-link rows under a description.
const maxDescLinks = 4

// selectServerRow selects a lobby row and (re)builds its cached description lines + links — shared
// by a row click and keyboard navigation (#18) so both show the same expanded detail.
func (a *App) selectServerRow(idx int, w int32) {
	if idx < 0 || idx >= len(a.servers) {
		return
	}
	a.selServer = idx
	desc := a.servers[idx].Description
	if desc == "" {
		desc = "(no description)"
	}
	a.descLines = a.ctx.WrapText(desc, w-2*pad-40, maxDescLines)
	a.descLinks = extractURLs(desc, maxDescLinks)
}

// serverRowTooltip shows a lobby row's details on hover (#17): the connect URL and the server's
// description ("MOTD"), which otherwise only appear once you click the row to expand it. (Real,
// live MOTD arrives after the handshake; this is what the master list advertises pre-connect.)
func (a *App) serverRowTooltip(row sdl.Rect, e *network.ServerEntry) {
	tip := e.WebSocketURL()
	if d := strings.TrimSpace(e.Description); d != "" {
		tip = d + "   —   " + tip
	}
	a.ctx.Tooltip(row, tip)
}

// nextJoinableServer returns the index of the next joinable server from `from` in direction dir
// (+1 down / -1 up), respecting the Phone Book filter; -1 when there's none that way (no wrap).
func (a *App) nextJoinableServer(from, dir int) int {
	n := len(a.servers)
	for i := from + dir; i >= 0 && i < n; i += dir {
		e := &a.servers[i]
		if a.phoneBookPage && !e.Favorite {
			continue
		}
		if e.Joinable() {
			return i
		}
	}
	return -1
}

// scrollServerIntoView adjusts lobbyScroll so the row at idx is fully visible — accounting for the
// "NOT SUPPORTED" legacy header and the Phone Book filter, so keyboard nav never selects off-screen.
func (a *App) scrollServerIntoView(idx int, listTop, h int32) {
	top := int32(0) // content-space offset (pre-scroll) of row idx's top
	legacyDrawn := false
	for i := range a.servers {
		e := &a.servers[i]
		if a.phoneBookPage && !e.Favorite {
			continue
		}
		if !e.Joinable() && !legacyDrawn {
			top += rowH // the legacy-servers header row
			legacyDrawn = true
		}
		if i == idx {
			break
		}
		top += rowH
	}
	view := h - listTop
	if top < a.lobbyScroll {
		a.lobbyScroll = top
	} else if top+rowH > a.lobbyScroll+view {
		a.lobbyScroll = top + rowH - view
	}
	if a.lobbyScroll < 0 {
		a.lobbyScroll = 0
	}
}

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
	// Characters tab. pushClip (not raw SetClipRect) also sets the INPUT clip, so a
	// cell scrolled half under the bar isn't clickable up there (hovering() honours
	// clipRect) — else clicking the search/tabs picks the hidden character.
	gridClip := sdl.Rect{X: 0, Y: gridTop, W: w, H: visibleH}
	prev, had := c.pushClip(gridClip)
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
	c.popClip(prev, had)
	if a.previewBase != "" {
		a.drawSpritePreview(w, h, false)
		a.closeSpritePreviewOnLeave()
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

	// Top-right is the consistent "leave this screen" slot (matches Settings/About/
	// Help/etc.) so muscle memory doesn't land on Disconnect (playtest: "in char select
	// the top-right is Disconnect, not Back — I keep almost pressing it"). Re-picking a
	// character from the courtroom puts a safe Back there; Disconnect is ALWAYS danger-
	// tinted (red outline + label) and kept out of that spot. Buttons lay out R→L.
	rightX := w - pad
	if a.room != nil {
		backW := int32(90)
		rightX -= backW
		if c.Button(sdl.Rect{X: rightX, Y: pad, W: backW, H: btnH}, "Back") {
			a.screen = ScreenCourtroom
			return
		}
		rightX -= 8
	}
	dcW := int32(120)
	rightX -= dcW
	if c.ButtonCol(sdl.Rect{X: rightX, Y: pad, W: dcW, H: btnH}, "Disconnect", ColPanel, ColPanelHi, ColDanger, ColDanger) {
		a.requestDisconnect() // confirm first unless instant-disconnect is set
		return
	}
	rightX -= 8
	// Privacy: opens the Help screen's Privacy tab so "what can this server see about
	// me?" is one click away before you commit to playing here.
	privW := int32(120)
	rightX -= privW
	if c.Button(sdl.Rect{X: rightX, Y: pad, W: privW, H: btnH}, "Privacy") {
		a.prevScreen = ScreenCharSelect
		a.openHelp(1)
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
	// (Re-pick "Back" → courtroom now lives in the top-right header slot above, so it
	// matches every other screen instead of sitting mid-row.)
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
	// Clip the grid to its own viewport (below the top bar) so scrolled cells
	// slide UNDER the fixed search/tabs/buttons instead of painting over them: the
	// bar is drawn first, so without this the later cell draws covered it as you
	// scrolled down, and search became unreachable. Keeps the bar always usable.
	// pushClip (not raw SetClipRect) also sets the INPUT clip, so a cell scrolled
	// half under the bar can't be clicked/hovered up there — hovering() honours
	// clipRect, so the pick (drawCharCell), the ★ star, and the hover-preview all
	// stop at the viewport edge instead of firing under the search/tabs bar.
	gridClip := sdl.Rect{X: 0, Y: gridTop, W: w, H: visibleH}
	prev, had := c.pushClip(gridClip)
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
				a.d.Manager.PrefetchChain(a.previewBase, a.urls.EmoteAlts(slot.Name, "normal", courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
			}
		}
		col++
		if col >= cols {
			col = 0
			row++
		}
	}
	c.popClip(prev, had)
	if a.previewBase != "" {
		a.drawSpritePreview(w, h, false)
		a.closeSpritePreviewOnLeave()
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
	if ready && len(page.Frames) > 1 {
		// The box loops on the wall clock (pageFrameLoop below): report through
		// the animated-chrome census so both loop modes keep frames coming WHILE
		// the box is actually drawn. SkipFrame deliberately has no previewBase
		// state check — an orphaned preview (its screen switched away) must never
		// hold the pace (the active-cap latch report). A single-frame box is
		// static: its pop-in rides store-generation damage, zoom/drag ride input.
		a.NoteAnimating()
	}
	// Playtest sizing: every character previews at the SAME height — the
	// Settings default (shipped at 384 px; the old fixed 192 read tiny), or
	// the user's grip-resized height this session — with width following the
	// art's aspect, instead of a per-character size driven by source
	// resolution. The caption strip below reports source resolution + scale.
	boxH := a.previewUserH
	if boxH == 0 {
		boxH = int32(a.d.Prefs.PreviewHeightPx())
	}
	pw, ph := boxH, boxH // placeholder box while the next sprite streams in
	if ready {
		// The preview exists to show the sprite — restart its loop per pick so
		// animated idles play instead of freezing on frame 0.
		if a.previewBase != a.previewFor {
			a.previewFor = a.previewBase
			a.previewAt = time.Now()
			a.previewZoom = 1 // a new sprite starts unzoomed
		}
		srcW, srcH := page.W, page.H
		if srcW < 1 {
			srcW = 1
		}
		if srcH < 1 {
			srcH = 1
		}
		ph = boxH
		pw = srcW * ph / srcH
		if pw > previewMaxW { // ultra-wide art: cap the width, let the height follow
			pw = previewMaxW
			ph = srcH * pw / srcW
		}
		if maxW := w - 4*pad; maxW > previewMinW && pw > maxW {
			pw = maxW // a big default (up to 720 tall now) must still fit a small window
			ph = srcH * pw / srcW
		}
		if pw < previewMinW {
			pw = previewMinW
		}
	}
	// Default CENTERED-RIGHT, shifted by the user's drag and clamped on-screen
	// (the whole window — the box is free to leave the stage). Centre-right
	// instead of the old bottom-right: down there it layered over the IC bar
	// and the bottom controls (playtest ask — spawn it higher up).
	capH := ph + previewCaptionH
	baseX, baseY := w-pw-pad*2, (h-capH)/2
	hiX, hiY := w-pw-pad, h-capH-pad
	if hiX < pad {
		hiX = pad
	}
	if hiY < pad {
		hiY = pad
	}
	dst := sdl.Rect{X: clampI32(baseX+a.previewOffX, pad, hiX), Y: clampI32(baseY+a.previewOffY, pad, hiY), W: pw, H: ph}
	frame := sdl.Rect{X: dst.X - 4, Y: dst.Y - 4, W: dst.W + 8, H: dst.H + 8 + previewCaptionH}
	a.previewFrameRect = frame // cached for handlePreviewInput (wheel zoom + drag + resize)
	c.Fill(frame, ColPanel)
	c.Border(frame, ColAccent)
	if a.previewPinned { // close button for the pinned box (click handled in handlePreviewInput)
		xb := sdl.Rect{X: frame.X + frame.W - 20, Y: frame.Y + 2, W: 18, H: 18}
		c.Fill(xb, ColPanelHi)
		c.Label(xb.X+5, xb.Y+1, "x", ColText)
	}
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
		a.drawPreviewZoom(frame, page.W, page.H, dst.H)
		// Resize grip (◢) at the image's bottom-right, just above the caption
		// strip — drag it to change the box height (handlePreviewInput).
		gr := previewGripRect(frame)
		for i := int32(0); i < 3; i++ {
			c.Fill(sdl.Rect{X: gr.X + 3 + i*3, Y: gr.Y + gr.H - 3 - i*3, W: 2, H: 2}, ColAccent)
			c.Fill(sdl.Rect{X: gr.X + gr.W - 3 - i*3, Y: gr.Y + 3 + i*3, W: 2, H: 2}, ColAccent)
		}
		c.Tooltip(gr, "Drag to resize the preview")
	} else {
		c.LabelClipped(dst.X+4, dst.Y+dst.H/2-8, dst.W-8, "loading…", ColTextDim)
	}
	if cycling {
		a.drawPreviewEmoteNav(frame)
	}
}

// previewGripRect is the resize grip's hit rect: the image corner above the
// caption strip. Shared by the draw and the input handler.
func previewGripRect(frame sdl.Rect) sdl.Rect {
	return sdl.Rect{X: frame.X + frame.W - 16, Y: frame.Y + frame.H - previewCaptionH - 16, W: 16, H: 16}
}

// closeSpritePreviewOnLeave dismisses the hover sprite-preview box. The box opens on a
// hovered cell but lives in the bottom-right corner, so the cursor has to TRAVEL from
// the cell across a gap to reach the box — and an "over neither" close would kill the box
// mid-travel (beta report: "the preview disappears the moment you move the mouse"). So
// while the cursor hasn't reached the box yet, the box stays up over the cell, the box,
// OR the corridor spanning the two; it closes the instant the cursor strays off that path
// (so it still vanishes when you simply move away — the other half of the feedback). Once
// the cursor has reached the box, it closes as soon as the cursor leaves the box (or goes
// back onto a trigger). Any click also dismisses (a selection commits). Pure per-frame
// state check, called only while a preview is up — off the hot path.
func (a *App) closeSpritePreviewOnLeave() {
	c := a.ctx
	if a.previewPinned {
		return // pinned: the box stays open until its x is clicked (handlePreviewInput)
	}
	if c.clicked {
		a.closeSpritePreview()
		return
	}
	overBox := c.hovering(a.previewFrameRect)
	// A trigger counts only while the pointer is genuinely ON it. hoverID is
	// cleared by the trigger's own HoverPreview call, so when the trigger stops
	// being drawn (drawer closed, emote page flipped, panel hidden, screen
	// switched) the id goes stale — trusting it bare pinned "over trigger" true
	// forever, and the box could never leave-close again (only a click on a
	// frame that runs this very check could, part of the cap-latch report).
	overTrigger := c.hoverID != "" && c.hovering(c.hoverRect)
	if overTrigger && !overBox {
		a.previewEntered = false           // back on a cell → (re)enter the travel phase
		a.previewTriggerRect = c.hoverRect // remember it so the corridor spans cell→box
	}
	if overBox {
		a.previewEntered = true
	}
	switch {
	case a.previewEntered:
		if !overBox && !overTrigger {
			a.closeSpritePreview() // used the box, then left it → close
		}
	case overTrigger || overBox || c.hovering(unionRect(a.previewTriggerRect, a.previewFrameRect)):
		// still on the cell, the box, or the corridor between them → keep it up to travel
	default:
		a.closeSpritePreview() // strayed off the travel path → close
	}
}

// closeSpritePreview tears down the hover-preview box and resets its travel state.
func (a *App) closeSpritePreview() {
	a.previewBase = ""
	a.previewEntered = false
	a.previewPinned = false
	// Disarm the dwell too: closing with the pointer still resting on the
	// trigger (a click-commit) otherwise re-opens NEXT frame — the elapsed
	// hoverSince satisfies the dwell instantly. That silent re-arm is how a
	// char pick carried a "closed" preview into the courtroom (PV lands a few
	// frames after the click) and latched the frame pacer at the active cap.
	// A fresh hover restarts the full dwell, which is also the right feel:
	// a selection commits, the pop-up shouldn't bounce straight back.
	a.ctx.hoverID = ""
}

// noteScreenTransition runs once per Frame, before handlePreviewInput: on a
// screen switch it drops every piece of hover-preview state tied to the old
// screen. The preview's draw + close-on-leave + pinned-× all live in per-screen
// draw tails, so a preview (pinned or not) that survives a switch — the owning
// click was pointer-fenced by a confirm modal, the switch came from a hotkey /
// the server (PV, disconnect), or the old screen's tail simply never ran
// (charINIBusy) — is unreachable: an invisible box whose stale rect eats
// wheel/press and whose stale trigger id pins close-on-leave open. SkipFrame no
// longer keys on previewBase (the draw-site census owns pacing), so this is
// input/lifecycle hygiene rather than the pacing fix itself.
func (a *App) noteScreenTransition() {
	if a.screen == a.drawnScreen {
		return
	}
	a.drawnScreen = a.screen
	a.closeSpritePreview() // also clears the trigger id — no cross-screen dwell carry-over
	a.previewFrameRect = sdl.Rect{}
	a.previewTriggerRect = sdl.Rect{}
}

// unionRect is the smallest rect covering both a and b — the hover-preview "travel
// corridor" from the trigger cell to the box. A zero-area rect contributes nothing.
func unionRect(a, b sdl.Rect) sdl.Rect {
	if a.W <= 0 || a.H <= 0 {
		return b
	}
	if b.W <= 0 || b.H <= 0 {
		return a
	}
	x0, y0 := min(a.X, b.X), min(a.Y, b.Y)
	x1, y1 := max(a.X+a.W, b.X+b.W), max(a.Y+a.H, b.Y+b.H)
	return sdl.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
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
	if a.previewPinned { // pinned box: its x claims the click before the drag-start below
		xb := sdl.Rect{X: box.X + box.W - 20, Y: box.Y + 2, W: 18, H: 18}
		if c.clicked && c.hovering(xb) {
			a.closeSpritePreview()
			c.clicked = false
			return
		}
	}
	// Wheel zoom in/out over the box; claim the wheel so the list/grid under it
	// doesn't also scroll (same range as the − / + buttons).
	if c.wheelY != 0 && c.hovering(box) {
		a.previewZoom = int(clampI32(int32(a.previewZoom)+c.wheelY, 1, previewZoomMax))
		c.wheelY = 0
		c.wheelTaken = true
	}
	// Corner-grip resize (playtest: "consistently sized… but resizable"): drag
	// the ◢ vertically to change the box height; width follows the art's aspect
	// on the next draw. Claims the press before the body move-drag below.
	grip := previewGripRect(box)
	if c.mouseDown && !a.previewResize && !a.previewDrag && c.hovering(grip) {
		a.previewResize = true
		a.previewResizeFrom = c.mouseY
		a.previewResizeBase = a.previewUserH
		if a.previewResizeBase == 0 {
			a.previewResizeBase = int32(a.d.Prefs.PreviewHeightPx()) // grip starts from the Settings default
		}
	}
	if a.previewResize {
		if c.mouseDown {
			a.previewUserH = clampI32(a.previewResizeBase+(c.mouseY-a.previewResizeFrom), previewMinH, previewMaxH)
		} else {
			a.previewResize = false
			c.clicked = false // a finished resize isn't a dismiss/selection
		}
		return // resizing owns the pointer — no move-drag underneath
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

// drawPreviewZoom draws the caption strip along the preview's bottom — the
// source resolution + the scale it's shown at (the playtest's "resolution scale
// line, just like existing AO") — flanked by the − / + magnifier buttons, whose
// clicks are consumed so the caller's click-to-dismiss doesn't fire. At >1× the
// cursor pans the magnified view.
func (a *App) drawPreviewZoom(frame sdl.Rect, srcW, srcH, shownH int32) {
	c := a.ctx
	const bh, bw int32 = 18, 22
	y := frame.Y + frame.H - bh - 1
	minus := sdl.Rect{X: frame.X + 2, Y: y, W: bw, H: bh}
	plus := sdl.Rect{X: frame.X + frame.W - bw - 2, Y: y, W: bw, H: bh}
	pct := int32(100)
	if srcH > 0 {
		pct = shownH * 100 / srcH
	}
	caption := fmt.Sprintf("%d×%d · %d%%", srcW, srcH, pct)
	if a.previewZoom > 1 {
		caption += fmt.Sprintf(" · %dx", a.previewZoom)
	}
	capW := c.TextWidth(caption) + 12
	lvl := sdl.Rect{X: frame.X + (frame.W-capW)/2, Y: y, W: capW, H: bh}
	for _, b := range []struct {
		r     sdl.Rect
		label string
	}{{minus, "-"}, {lvl, caption}, {plus, "+"}} {
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
	// v1.50.5 (Nightingale: "disabled buttons still show up in editor view"):
	// the slot registry is rebuilt from THIS frame's draws while editing, so a
	// piece hidden mid-session stops ghosting handles the moment it stops
	// drawing — stale registrations from when it was visible used to live in
	// the map forever. Edit-mode only (the registry isn't read otherwise).
	if a.classicEdit {
		clear(a.slotReg)
	}
	// Load the slot overrides and re-tear last session's torn-off Extras widgets
	// BEFORE the fence below reads them: boxFencesPointer walks a.classicOv (torn
	// tabs) and a.extrasDetached (torn widgets), so both must be populated first or
	// frame one fences a stale/empty set (the same statelessness torn tabs already
	// enjoy). Neither call needs room/sess; both are latched, so this stays
	// alloc-free after the first courtroom frame.
	a.ensureClassicOv()
	a.reconstructTornWidgets(w, h)
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
		if a.classicEdit {
			a.stopClassicEdit() // session dropped mid-edit — release the fence so the lobby isn't stuck
		}
		a.screen = ScreenLobby
		return
	}

	// Theater mode preempts BOTH layouts: stage only, Esc exits.
	if a.theaterOn {
		if a.classicEdit {
			a.stopClassicEdit() // can't edit the layout from theater mode — release the fence
		}
		a.drawTheater(w, h)
		return
	}

	// Theme-driven geometry: when the theme ships courtroom_design.ini
	// (and the toggle is on), the courtroom IS the theme's layout.
	if lay := a.themeLayout(w, h); lay.valid && a.d.Prefs.ThemeLayoutEnabled() {
		if a.classicEdit {
			a.stopClassicEdit() // a themed layout took over; classic edit is default-only
		}
		a.drawCourtroomThemed(w, h, lay)
		return
	}
	// The themed editor only exists over themed geometry; release its fence if
	// the theme (or the toggle) went away mid-edit.
	if a.layoutEdit {
		a.stopLayoutEdit()
	}
	// Classic slot editor (default layout only): fence the pointer BEFORE any
	// widget draws so its clicks reach the editor, not the courtroom; the overlay
	// itself draws LAST (after drawICControls).
	a.classicEditFence()

	// Viewport: AO 4:3 at the user's width percent (View −/+ buttons).
	vpW := w * int32(a.vpPct) / DefaultScalePct
	vpH := vpW * 3 / 4
	if vpH > h-220 {
		vpH = h - 220
		vpW = vpH * 4 / 3
	}
	// Precise (exact-px) sizing overrides the % knob: the native stage art is
	// 256×192, so an exact width that's an integer multiple stays crisp where the
	// %-of-window size lands between multiples and blurs. If the chosen size doesn't
	// fit the window, snap DOWN to the largest 256×192 multiple that does — clamping
	// to raw px would re-introduce the very blur this control removes.
	if ew := int32(a.d.Prefs.ViewportExactWidth()); ew > 0 {
		vpW, vpH = ew, ew*config.ViewportArtH/config.ViewportArtW
		if vpW > w-2*pad || vpH > h-220 {
			fitM := (h - 220) / config.ViewportArtH
			if wM := (w - 2*pad) / config.ViewportArtW; wM < fitM {
				fitM = wM
			}
			if fitM < 1 {
				fitM = 1
			}
			vpW, vpH = fitM*config.ViewportArtW, fitM*config.ViewportArtH
		}
	}
	vpDef := sdl.Rect{X: pad, Y: pad, W: vpW, H: vpH}
	// Movable + resizable stage. With no override the View knob / divider own its
	// 4:3 size (vpDef); once you drag a viewport handle in the editor, that override
	// wins (free position + size; the scene fills it) until you reset the box.
	vp := a.slotRect(slotViewport, vpDef, w, h)
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
	a.drawChatOverlay(vp, true, w, h) // classic layout: the chatbox is a movable slot
	a.drawCourtOverlays(vp, nil)      // HP bars, clocks, badges, splashes
	a.drawReactionFloats(vp)          // #2: emoji reactions rising over the stage (0-alloc when none)

	// Modal popups: the kit has no z-aware input, so the controls
	// underneath simply don't draw (and don't see clicks) — same pattern
	// as the iniswap menu. Shared with the themed path (drawCourtroomModals).
	if !a.classicEdit && a.drawCourtroomModals(w, h) {
		return // skipped while editing: modals are closed on entry, so the editor is never stranded
	}

	// Right column: log + music. In the new (default) layout the OOC gets its OWN always-visible
	// box under the IC log; the Legacy Developer theme keeps OOC as a tab (its old handling).
	// Anchored to the DEFAULT stage spot (vpDef), not the moved viewport — each box is placed
	// independently, so dragging the stage doesn't drag the column with it. The whole column is a
	// slot ("rightcol"), draggable/resizable on BOTH the default and Legacy themes.
	rx := vpDef.X + vpDef.W + pad
	rw := w - rx - pad
	a.dockLeftX = w // log hidden ⇒ no dock strip; the server-tab strip falls back to window-centre
	if !a.panelHidden(panelLog) {
		rcolDef := sdl.Rect{X: rx, Y: pad, W: rw, H: vpDef.H}
		rcol := a.slotRect(slotRightCol, rcolDef, w, h)
		a.dockLeftX = rcol.X // keep the floating server-tab strip LEFT of the dock tabs (issue #2)
		// OOC lives in its own box by default; the Legacy theme — or the opt-in
		// "OOC in the log tab" toggle (the old layout, set in the layout editor) —
		// instead routes the whole column to the tabbed log with an OOC tab.
		if a.d.Prefs.LegacyDevThemeOn() || a.d.Prefs.OOCInLogTabOn() {
			a.drawLogPanel(rcol, vpDef)
		} else {
			a.drawCleanRightColumn(rcol, vpDef, w, h)
		}
	}

	// Bottom: IC input, emotes, controls — anchored to the default stage spot so moving the
	// viewport doesn't drag them (they become their own slots in a later slice).
	a.drawICControls(w, h, vpDef)

	// Compact hover toolbox (#27): slim bottom-right grip → Theater / Edit / Hide-UI
	// chips on hover. Normal play only — the editor shows its own full chip strip.
	if !a.classicEdit {
		a.drawCompactToolbox(w, h)
	}

	// Live slot editor overlay (default layout only) — drawn LAST so it sits over every widget.
	// Torn-off tab panels draw HERE while editing (before the editor) so slotRect registers them
	// and the editor hands them drag/resize handles this same frame; their content shows (inert)
	// so you see what you're arranging. In normal play they draw post-courtroom (app.go) where the
	// content is interactive and the pointer is fenced over them. (torntabs.go)
	if a.classicEdit {
		a.drawTornTabs(w, h)
		a.drawMessagesSlotGhost(w, h) // Group Chat panel: inert placeholder so the editor gives it handles
		a.drawTabBar(w, h)            // the server-tab strip paints UNDER the editor while editing (app.go skips its usual over-everything paint)
		a.drawClassicEditor(w, h)
	}
}

// drawCleanRightColumn is the new-default right column: the IC log on top and the OOC as its own
// bordered, always-visible box below (instead of Legacy's OOC-in-a-tab). The split keeps the OOC
// roughly a third of the height with a sane floor so its input never gets crushed on short windows.
func (a *App) drawCleanRightColumn(rcol sdl.Rect, vp sdl.Rect, w, h int32) {
	c := a.ctx
	oocH := rcol.H * 32 / 100
	if oocH < cleanOOCMinH {
		oocH = cleanOOCMinH
	}
	if oocH > rcol.H-cleanOOCMinH { // never starve the IC log either
		oocH = rcol.H - cleanOOCMinH
	}
	logH := rcol.H - oocH - cleanGapPx
	boxDef := sdl.Rect{X: rcol.X, Y: rcol.Y + logH + cleanGapPx, W: rcol.W, H: oocH}
	// If the OOC box is hidden, or has been dragged to its own spot (a slot override), it
	// no longer sits under the log — so the log reclaims the FULL column instead of leaving
	// a dead gap where the OOC used to be (playtest: "unpin OOC and the IC log is stuck
	// with empty space that never goes away").
	_, oocMoved := a.classicOv[slotOOC]
	oocHidden := a.panelHidden(panelOOC)
	if oocMoved || oocHidden {
		logH = rcol.H
	}
	a.drawLogPanel(sdl.Rect{X: rcol.X, Y: rcol.Y, W: rcol.W, H: logH}, vp)
	if oocHidden {
		return // the log fills the column; there's no OOC box to draw
	}
	// The OOC box is its OWN slot — drag it anywhere / resize it independently of the log.
	box := a.slotRect(slotOOC, boxDef, w, h)
	// Guard against a stale slot override (saved at a different window size / UI scale)
	// dropping the OOC box onto the log's tab strip — the "OOC overlapping the menu
	// bars" report. While it sits over the right column, keep its top below the tab row
	// so Log / Music / Areas / … stay visible and clickable.
	if box.X < rcol.X+rcol.W && box.X+box.W > rcol.X {
		if top := rcol.Y + btnH + 4; box.Y < top {
			box.H -= top - box.Y
			box.Y = top
			if box.H < cleanOOCMinH {
				box.H = cleanOOCMinH
			}
		}
	}
	c.Fill(box, a.partPanelOr(partOOC, ColPanel)) // per-part tint (v1.52.0)
	c.Border(box, ColAccent)
	// Titled header bar so it reads as a clean, distinct box (brighter label = legible at a glance).
	hdr := sdl.Rect{X: box.X + 1, Y: box.Y + 1, W: box.W - 2, H: cleanHeaderH}
	c.Fill(hdr, ColPanelHi)
	c.Label(hdr.X+7, hdr.Y+3, "OOC", ColText)
	a.drawOOCPanel(sdl.Rect{X: box.X + 5, Y: box.Y + cleanHeaderH + 4, W: box.W - 10, H: box.H - cleanHeaderH - 8}, true)
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
	case a.showTimer:
		a.drawTimerPanel(w, h)
	case a.showUICfg:
		a.drawUICfgPanel(w, h)
	case a.showLogin:
		a.drawLoginDialog(w, h)
	case a.pairPopupOpen:
		a.drawPairPopup(w, h)
	case a.showSfxBrowser:
		a.drawSfxBrowser(w, h) // #12 SFX Browser modal (preview + favourites)
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
	if !a.d.Prefs.DragLayoutOn() || a.vpZoom > 1 || a.courtModalOpen() || a.viewportOverridden() { // off, zoomed, a blocking popup, or the stage has an editor size override (the divider would change the now-shadowed vpPct)
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
		// Grabbing the edge is a deliberate "size it freely" gesture, so it takes
		// back control from an exact-px pin (else the drag would silently no-op
		// against the shadowing fixed size).
		if a.d.Prefs.ViewportExactWidth() != 0 {
			a.d.Prefs.SetViewportExactWidth(0)
		}
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
		a.deliberateClose = true // user chose to leave — the drop paths must not auto-reconnect (#1)
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
	// "Don't ask again" reuses the Quit-modal pattern: it ticks the existing
	// Instant-disconnect pref, so future Disconnects — the button AND Esc, both
	// routed through requestDisconnect — skip straight through. Untickable here too.
	inst := a.d.Prefs.InstantDisconnectOn()
	if next := c.Checkbox(m.X+pad, m.Y+78, "Don't ask again (disconnect instantly from now on)", inst); next != inst {
		a.d.Prefs.SetInstantDisconnect(next)
	}
	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 170, H: btnH}, "Yes, disconnect") {
		a.confirmDisconnect = false
		a.deliberateClose = true // user confirmed leaving — suppress auto-reconnect on this teardown (#1)
		a.Disconnect()
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.confirmDisconnect = false
	}
}

// drawCloseTabConfirm paints the "close this tab?" overlay: the sibling of
// drawDisconnectConfirm for a manual background-tab ✕ (requestCloseTab). Same
// layout, fence discipline (drawn top-level, pointer restored just before it, one
// modal at a time), and "Don't ask again" → Instant-disconnect pref — but its own
// copy naming the server, and Yes closes just THIS tab (confirmPendingCloseTab)
// instead of the live session. You stay on whatever screen you're on, so the copy
// deliberately does NOT say "return to the lobby" (unlike the disconnect modal).
// Off the render hot path (only while a close is pending).
func (a *App) drawCloseTabConfirm(w, h int32) {
	c := a.ctx
	t := a.pendingCloseTab
	if t == nil {
		return // defensive: the outer guard only draws this while pending
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 480, 176
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	// The server name is variable-length and drawn on a fixed-width panel, so clip
	// it (LabelClipped) — an over-long name must not spill past the border onto the
	// scene behind (the §3.4 class of bug that just landed one commit upstream).
	name := t.state.serverName
	if name == "" {
		name = "this server"
	}
	c.Heading(m.X+pad, m.Y+pad, "Close this tab?", ColText)
	c.LabelClipped(m.X+pad, m.Y+50, mw-2*pad, "Close and disconnect from "+name+"? You'll stay where you are.", ColText)
	// "Don't ask again" reuses the disconnect modal's pattern: it ticks the same
	// Instant-disconnect pref, so future tab-✕ clicks (AND the Disconnect button /
	// Esc) skip straight through. Untickable here too.
	inst := a.d.Prefs.InstantDisconnectOn()
	if next := c.Checkbox(m.X+pad, m.Y+78, "Don't ask again (close/disconnect instantly from now on)", inst); next != inst {
		a.d.Prefs.SetInstantDisconnect(next)
	}
	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 170, H: btnH}, "Yes, close tab") {
		a.confirmPendingCloseTab() // revalidates the pointer's current index, then closeParkedTab
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.pendingCloseTab = nil
	}
}

// requestQuit asks to close AsyncAO: straight to quit if the user ticked "don't
// ask again", otherwise the confirm dialog. The Esc-in-lobby escape hatch (you
// can't always reach the window's X in fullscreen) routes through here.
func (a *App) requestQuit() {
	if a.d.Prefs.QuitConfirmSkipOn() {
		a.doQuit()
		return
	}
	a.showQuitConfirm = true
}

// doQuit pushes an SDL quit event, which the main loop drains to shut down
// cleanly (prefs flush, etc.) — same path as the window's close button.
func (a *App) doQuit() {
	_, _ = sdl.PushEvent(&sdl.QuitEvent{Type: sdl.QUIT})
}

// drawQuitConfirm is the "Quit AsyncAO?" modal (Esc in the lobby): Quit / Cancel
// plus a "Don't ask again" tick. Drawn top-level so it fences the screen behind.
func (a *App) drawQuitConfirm(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 460, 184
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Quit AsyncAO?", ColText)
	c.Label(m.X+pad, m.Y+50, "Close the AsyncAO window?", ColText)
	skip := a.d.Prefs.QuitConfirmSkipOn()
	if next := c.Checkbox(m.X+pad, m.Y+82, "Don't ask again", skip); next != skip {
		a.d.Prefs.SetQuitConfirmSkip(next)
	}
	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 120, H: btnH}, "Quit") {
		a.showQuitConfirm = false
		a.doQuit()
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.showQuitConfirm = false
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

// chatBoxTopStrip is the showname strip above the message text (text draws at box.Y+chatBoxTopStrip);
// chatBoxBottomPad leaves a little air under the last line. Used to grow the box to fit its message.
const (
	chatBoxTopStrip  = int32(26)
	chatBoxBottomPad = int32(8)
)

// grownChatBoxH is the chatbox height needed to show `lines` lines of `lineH`-tall text in full
// (plus the showname strip + bottom pad) — never below baseH (the MsgBox-knob band) and never above
// 3/5 of the stage height vpH. Pins the "grow to fit so a resized/long message isn't cut off at the
// bottom" rule; drawChatOverlay applies it to the flat fallback panel (a theme's own skin keeps its
// authored size). Pure + unit-tested.
func grownChatBoxH(baseH, vpH, lines, lineH int32) int32 {
	needH := chatBoxTopStrip + lines*lineH + chatBoxBottomPad
	if maxH := vpH * 3 / 5; needH > maxH {
		needH = maxH
	}
	if needH < baseH {
		return baseH
	}
	return needH
}

// drawChatOverlay paints the message box (showname + spoken text) over the stage.
// movableBox (classic layout only) routes the box rect through slotRect so it can
// be dragged off the sprites / out of the stage in the layout editor; w,h give the
// override its window-fraction frame. The themed and replay paths pass false (the
// theme owns its chatbox geometry there).
func (a *App) drawChatOverlay(vp sdl.Rect, movableBox bool, w, h int32) {
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
	// Movable chatbox (classic layout): its default sits at the stage bottom, but a
	// layout-editor override lifts it anywhere — off the sprites or out of the stage
	// entirely. Everything below lays out relative to box, so move + resize follow.
	if movableBox {
		box = a.slotRect(slotChatbox, box, w, h)
	}
	// Per-character chatbox skin (char.ini chat=<misc>, playtest ask): the
	// SPEAKER's own box art wins over the theme skin — AO2's get_chat priority.
	// Drawn only once the misc texture is resident; until then the theme/flat
	// box covers the fetch gap, so nothing ever flashes empty.
	var charSkin *sdl.Texture
	if sc.ChatSkinBase != "" {
		if page, ok := a.d.Store.Get(sc.ChatSkinBase); ok && len(page.Frames) > 0 {
			charSkin = page.Frames[0]
		}
	}
	// Grow the FLAT fallback panel to fit the whole wrapped message so long text isn't cut off
	// at the bottom — notably after a viewport/box resize wraps it to more lines (reported bug).
	// Authored skin art (theme OR per-character) keeps its box (stretching looks wrong). Grows
	// UPWARD from the current bottom edge, capped inside grownChatBoxH.
	if _, hasSkin := a.themePage(themeStemChatbox); !hasSkin && charSkin == nil {
		lineH := int32(c.ChatFontFor(a.chatPct, sc.MessageText).Height())
		if g := grownChatBoxH(box.H, vp.H, int32(a.chatMsgLines(box.W-16, sc)), lineH); g > box.H {
			box.Y -= g - box.H
			box.H = g
		}
	}
	// Skin priority: the speaker's own art, else the theme's, else the flat
	// translucent panel (themePage self-heals T1 eviction). Skin blits go via
	// the Ctx scratch rect: &box into cgo would heap-allocate box at its
	// CREATION above, every frame, even when neither skin branch runs.
	skinned, themeSkinned := false, false
	if charSkin != nil {
		c.cgoRect = box
		_ = c.Ren.Copy(charSkin, nil, &c.cgoRect)
		skinned = true
	}
	if !skinned {
		if page, ok := a.themePage(themeStemChatbox); ok {
			c.cgoRect = box
			_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
			skinned, themeSkinned = true, true
		}
	}
	if !skinned {
		// Panel opacity is user-tunable (Settings → see-through chatbox); the
		// border stays solid for legibility. Read once here, off the 0-alloc
		// render gate (that's render.Viewport; this UI overlay already reads prefs).
		alpha := uint8(255 * a.d.Prefs.ChatboxOpacityPct() / 100)
		bg := sdl.Color{R: 16, G: 16, B: 24, A: alpha}
		if col, ok := a.partPanel(partChatbox); ok { // per-part tint (v1.52.0): custom base, opacity kept
			bg = sdl.Color{R: col.R, G: col.G, B: col.B, A: alpha}
		}
		if a.d.Prefs.ChatboxTintOn() && sc.ShownameText != "" { // #14 per-character tint
			bg = chatboxTintFor(sc.ShownameText, bg)
		}
		c.Fill(box, bg)
		c.Border(box, ColAccent)
	}
	// Theme text colors are designed against the theme's own skin; on the
	// flat fallback panel (or a per-character skin) they can be unreadable
	// (black-on-dark was a real report), so they only apply while the THEME
	// skin actually drew.
	nameCol := ColAccent
	if themeSkinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	if a.d.Prefs.NameColorsOn() { // per-speaker name colour wins over accent/theme
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.NameColorSat())/100, float64(a.d.Prefs.NameColorVal())/100)
	}
	// Pick a covering face for the showname (a Tifinagh / Cyrillic NAME would tofu on
	// the fixed chrome font); ChatFontFor returns the chrome font for ASCII, so plain
	// names are unchanged. DefaultScalePct matches the emoji face size below.
	if a.d.Prefs.BoldNamesOn() { // faux-bold the showname (1px-shifted second pass) for readability — default on
		a.labelEmoji(c.ChatFontFor(DefaultScalePct, sc.ShownameText), c.EmojiFont(DefaultScalePct), box.X+9, box.Y+4, box.W-16, sc.ShownameText, nameCol)
	}
	a.labelEmoji(c.ChatFontFor(DefaultScalePct, sc.ShownameText), c.EmojiFont(DefaultScalePct), box.X+8, box.Y+4, box.W-16, sc.ShownameText, nameCol)

	wrapW := box.W - 16
	a.ensureChatRaster(wrapW, themeSkinned) // theme ink only with the THEME's skin; char skins keep our readable text
	// Drag the message to highlight it, Ctrl+C / right-click to copy (webAO-style).
	textRect := sdl.Rect{X: box.X + 8, Y: box.Y + 26, W: wrapW, H: box.Y + box.H - (box.Y + 26)}
	a.handleChatSelect(textRect, sc)
	if a.msAnim != nil || a.msRaster != nil {
		// Clip to the box: oversized Text settings stay INSIDE it. Via the Ctx
		// scratch rect — &box into cgo would heap-allocate box at its creation
		// every frame (SDL copies the rect, so later scratch reuse is safe).
		c.cgoRect = box
		_ = c.Ren.SetClipRect(&c.cgoRect)
		if a.chatSelActive { // selection highlight, UNDER the text so it reads through
			a.drawChatSelHighlight(textRect.X, textRect.Y, wrapW, sc)
		}
		if a.msAnim != nil { // #M5 animated message (shake/wave/rainbow spans)
			reduce := a.d.Prefs.ReduceMotion()
			if a.msAnim.Animates(reduce) {
				// Clock-driven text FX on screen: keep frames coming through the
				// static skip (idle=0 froze/stuttered them — the FX-at-idle
				// report). Draw-site census, so it self-clears the moment the
				// message leaves the box; gradient-only / reduce-motion render
				// static and deliberately don't hold frames. Minimized still
				// draws nothing (no Frame at all), and unfocused with the
				// background cap off still parks (that gate outranks this).
				a.NoteAnimating()
			}
			a.msAnim.Draw(c.Ren, a.glyphCache, a.msAnimFont, a.d.Viewport.AnimClock(), sc.VisibleRunes, box.X+8, box.Y+26, reduce)
		} else {
			a.msRaster.Draw(c.Ren, sc.VisibleRunes, box.X+8, box.Y+26)
		}
		_ = c.Ren.SetClipRect(nil)
	}

	a.chatZoomWheel(box)
}

// handleChatSelect runs the chatbox's text selection + Ctrl+C / right-click
// copy: drag across the message to select a RANGE of it (per-rune, snapped
// to boundaries via the raster's own glyph advances — "so people can copy one
// word instead of the whole line"), double-click selects the word under the
// cursor, triple-click the whole message; a plain click clears. The animated
// path (msAnim — per-glyph motion, no static raster) keeps the old
// whole-message selection. Its own press edge so it's independent of the log
// selection's; activating either clears the other so they never fight over a
// Ctrl+C. Shared by the classic overlay and the themed chatbox.
func (a *App) handleChatSelect(textRect sdl.Rect, sc *courtroom.Scene) {
	c := a.ctx
	pressed := c.mouseDown && !a.chatSelPrevDown
	a.chatSelPrevDown = c.mouseDown
	inText := c.hovering(textRect)
	if pressed {
		if inText {
			a.chatSelDragging = true
			a.chatSelDownX, a.chatSelDownY = c.mouseX, c.mouseY
			if a.msRaster != nil { // anchor the range at the pressed boundary
				a.chatSelA = a.msRaster.RuneAt(c.mouseX-textRect.X, c.mouseY-textRect.Y)
				a.chatSelB = a.chatSelA
			}
		} else {
			a.chatSelActive = false // a press elsewhere clears the highlight
		}
	}
	if a.chatSelDragging {
		if c.mouseDown {
			if a.msRaster != nil { // the held drag moves the range's head
				a.chatSelB = a.msRaster.RuneAt(c.mouseX-textRect.X, c.mouseY-textRect.Y)
			}
			if absInt(int(c.mouseX-a.chatSelDownX))+absInt(int(c.mouseY-a.chatSelDownY)) > 3 {
				if !a.chatSelActive {
					a.chatSelActive = true // moved enough → a selection, not a click
					a.logSelActive = false // …and it owns the highlight now
				}
			}
		} else {
			a.chatSelDragging = false
			switch {
			case a.chatSelActive && a.msRaster != nil && a.chatSelA == a.chatSelB:
				a.chatSelActive = false // wobbled past the slop but landed on one boundary — not a selection
			case a.chatSelActive:
				c.clicked = false // a real drag isn't a click on whatever's under it
				c.focusID = ""    // unfocus so Ctrl+C copies the selection, not a still-focused field
			}
		}
	}
	// Double-click: the word under the cursor; triple-click: the whole
	// message (native text gestures — mirrors the fields and the logs).
	if a.msRaster != nil && inText && (c.dblClick || c.tripleClick) && sc.MessageText != "" {
		runes := []rune(sc.MessageText)
		if c.tripleClick {
			a.chatSelA, a.chatSelB = 0, len(runes)
			c.tripleClick = false
		} else {
			idx := a.msRaster.RuneAt(c.mouseX-textRect.X, c.mouseY-textRect.Y)
			a.chatSelA, a.chatSelB = wordBoundsAt(runes, idx)
			c.dblClick = false
		}
		if a.chatSelB > a.chatSelA {
			a.chatSelActive = true
			a.logSelActive = false
			c.clicked = false
			c.focusID = ""
		}
	}
	if a.chatSelActive && sc.MessageText != "" {
		if (c.copyReq && c.focusID == "") || (c.rightClicked && inText) {
			_ = sdl.SetClipboardText(a.chatSelText(sc))
			a.warnLine = "Copied selection to clipboard"
			a.warnAt = time.Now()
			c.copyReq = false
			if inText {
				c.rightClicked = false
			}
		}
	}
}

// chatSelText is the text the chatbox selection copies: the selected rune
// range, or the whole message on the animated path / a degenerate range.
func (a *App) chatSelText(sc *courtroom.Scene) string {
	if a.msRaster == nil {
		return sc.MessageText
	}
	runes := []rune(sc.MessageText)
	lo, hi := a.chatSelA, a.chatSelB
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 0 {
		lo = 0
	}
	if hi > len(runes) {
		hi = len(runes)
	}
	if lo >= hi {
		return sc.MessageText
	}
	return string(runes[lo:hi])
}

// drawChatSelHighlight fills the selected range behind the message, one band
// per wrapped line: partial first/last lines measure their end runes through
// the raster's own advances, interior lines span their drawn text, and the
// typewriter clamp keeps the band off glyphs that haven't revealed yet. The
// animated path has no per-rune raster — the whole visible block highlights,
// exactly the old behavior. Measurement only (no allocations): fine per frame
// while a selection is up.
func (a *App) drawChatSelHighlight(x, y, wrapW int32, sc *courtroom.Scene) {
	c := a.ctx
	m := a.msRaster
	if m == nil { // animated message (msAnim): whole-block highlight
		lineH := int32(c.ChatFontFor(a.chatPct, sc.MessageText).Height())
		c.Fill(sdl.Rect{X: x, Y: y, W: wrapW, H: int32(a.chatMsgLines(wrapW, sc)) * lineH}, a.highlightFill())
		return
	}
	lo, hi := a.chatSelA, a.chatSelB
	if lo > hi {
		lo, hi = hi, lo
	}
	if hi > sc.VisibleRunes {
		hi = sc.VisibleRunes // typewriter: never highlight unrevealed glyphs
	}
	fill := a.highlightFill()
	for i := 0; i < m.Lines(); i++ {
		if x0, x1, ok := m.LineSpanX(i, lo, hi); ok && x1 > x0 {
			c.Fill(sdl.Rect{X: x + x0, Y: y + int32(i)*m.LineH(), W: x1 - x0, H: m.LineH()}, fill)
		}
	}
}

// sfxAutoLabel is the SFX picker's "no override" entry: use the selected emote's own
// char.ini sound (the default behaviour).
const sfxAutoLabel = "SFX: auto"

// ensureSFXChoices (re)builds the IC-bar SFX picker list when the character or its emote
// list changes: "SFX: auto" (the emote's own sound) then this character's DISTINCT emote
// sounds (char.ini [SoundN]; "0"/"1"/empty are AO silence, skipped). The picked sound
// overrides the emote's on every send until set back to auto. Off the per-frame path
// (rebuild only on a char/emote-count change) so the IC bar stays alloc-free.
func (a *App) ensureSFXChoices() {
	// Plain-field guard, not a concatenated key: this runs every frame, and
	// building a "<char>:<n>" string just to compare it allocated per frame.
	name := a.activeCharName()
	if a.sfxChoicesForName == name && a.sfxChoicesForCount == len(a.emotes) {
		return
	}
	a.sfxChoicesForName, a.sfxChoicesForCount = name, len(a.emotes)
	a.sfxChoices = append(a.sfxChoices[:0], sfxAutoLabel)
	for i := range a.emotes {
		s := a.emotes[i].SFXName
		if s == "" || s == "0" || s == "1" {
			continue
		}
		dup := false
		for _, ex := range a.sfxChoices {
			if ex == s {
				dup = true
				break
			}
		}
		if !dup {
			a.sfxChoices = append(a.sfxChoices, s)
		}
	}
	if a.sfxChoiceIdx >= len(a.sfxChoices) {
		a.sfxChoiceIdx = 0
	}
}

// chatMsgLines is the chatbox message's wrapped-line count (the raster's own count when
// present, else an approximate re-wrap) — sizes the whole-message selection block.
func (a *App) chatMsgLines(wrapW int32, sc *courtroom.Scene) int {
	if a.msRaster != nil {
		if n := a.msRaster.Lines(); n > 0 {
			return n
		}
	}
	if n := len(a.ctx.WrapText(sc.MessageText, wrapW, 0)); n > 0 {
		return n
	}
	return 1
}

// ensureChatRaster (re)rasterizes the current message when the text,
// color, zoom, wrap width, or skin presence changed — shared by the
// classic overlay and the themed chatbox.
func (a *App) ensureChatRaster(wrapW int32, skinned bool) {
	sc := a.renderScene() // matches drawChatOverlay (live / slideshow / replay scene)
	effSig := effectsSig(sc.MessageEffects)
	if (a.msRaster != nil || a.msAnim != nil) && a.rasterRaw == sc.MessageRaw && a.rasterText == sc.MessageText && a.rasterColor == sc.TextColor &&
		a.rasterScale == a.chatPct && a.rasterW == wrapW && a.rasterSkinned == skinned && a.rasterEffSig == effSig && a.rasterCentered == sc.Centered &&
		a.rasterDevPct == a.ctx.textDevPct { // #77: a UI-scale change re-rasterizes at the new device size
		return
	}
	if a.rasterRaw != sc.MessageRaw {
		a.chatSelActive = false // a new message — drop the stale chatbox highlight/selection
	}
	if a.msRaster != nil {
		a.msRaster.Destroy()
		a.msRaster = nil
	}
	a.msAnim = nil // AnimatedText owns no textures (the glyph cache does); just drop it
	a.msAnimFont = nil
	if sc.MessageText == "" {
		return
	}
	// #M5: a message with effect spans takes the per-glyph animated path; every other
	// message keeps the untouched MessageRaster fast paths (zero change for the common case).
	if len(sc.MessageEffects) > 0 {
		if a.glyphCache == nil {
			a.glyphCache = render.NewGlyphCache(glyphCacheCap)
		}
		anim, font := renderAnimated(a, sc, wrapW, skinned, a.chatPct)
		anim.Warm(a.ctx.Ren, a.glyphCache, font) // render all glyphs up front → 0-alloc draws
		a.msAnim = anim
		a.msAnimFont = font
	} else {
		raster, err := renderRaster(a, sc, wrapW, skinned, a.chatPct, false)
		if err != nil {
			return
		}
		a.msRaster = raster
	}
	a.rasterText = sc.MessageText
	a.rasterRaw = sc.MessageRaw
	a.rasterColor = sc.TextColor
	a.rasterScale = a.chatPct
	a.rasterW = wrapW
	a.rasterSkinned = skinned
	a.rasterEffSig = effSig
	a.rasterCentered = sc.Centered
	a.rasterDevPct = a.ctx.textDevPct // #77
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
	// FIX #9 (ZeitHeld): drive the EFFECTIVE (per-server when connected) volumes, exactly
	// like the Settings and Extras sliders. The old code read/wrote the GLOBAL prefs, which
	// a connected server's per-server audio profile overrides — so once you touched volume
	// anywhere else (Settings/Extras both write per-server), these sidebar sliders moved
	// nothing audible. Plus a "Rate" column for the blip cadence (the user's ask).
	master, music, sfx, blip := a.effectiveVolumes()
	rate, onSpaces := a.d.Prefs.BlipTyping()
	const cols = 5
	colW := r.W / cols
	// Playtest ("kinda bare bones"): two rows per column — "label NN%" (the
	// label is still the click-to-mute) above a full-width slider, so the
	// actual level is always READABLE, not just a thumb position. The sliders
	// also take the wheel now (kit-wide), so fine steps don't need a drag.
	cell := func(i int32, id, label, valText, tip string, val, lo, hi int32, mute *bool) int32 {
		x := r.X + i*colW
		labelCol := ColTextDim
		if mute != nil { // #10: the channel label is a click-to-mute toggle (red = muted)
			lbl := sdl.Rect{X: x, Y: r.Y, W: colW - 6, H: 16}
			if c.ClickedIn(lbl) {
				*mute = !*mute
				a.applyAudioVolumes() // the muted channel goes silent; the slider level is kept
			}
			if *mute {
				labelCol = ColDanger
				valText = "muted"
			}
			c.Tooltip(lbl, "Click to mute / unmute (the slider level is kept)")
		}
		c.Label(x, r.Y+2, label, labelCol)
		c.LabelClipped(x+c.TextWidth(label)+6, r.Y+2, colW-c.TextWidth(label)-12, valText, ColAccent)
		track := sdl.Rect{X: x, Y: r.Y + 22, W: colW - 8, H: 14}
		v := clampI32(c.Slider("volstrip:"+id, track, val, hi), lo, hi)
		if tip != "" {
			c.Tooltip(track, tip)
		}
		return v
	}
	if nv := cell(0, "master", "Master", fmt.Sprintf("%d%%", master), "", int32(master), 0, 100, &a.masterMuted); int(nv) != master {
		a.setEffectiveVolumes(int(nv), music, sfx, blip)
	}
	if nv := cell(1, "music", "Music", fmt.Sprintf("%d%%", music), "", int32(music), 0, 100, &a.musicMuted); int(nv) != music {
		a.setEffectiveVolumes(master, int(nv), sfx, blip)
	}
	if nv := cell(2, "sfx", "SFX", fmt.Sprintf("%d%%", sfx), "", int32(sfx), 0, 100, &a.sfxMuted); int(nv) != sfx {
		a.setEffectiveVolumes(master, music, int(nv), blip)
	}
	if nv := cell(3, "blip", "Blips", fmt.Sprintf("%d%%", blip), "", int32(blip), 0, 100, &a.blipMuted); int(nv) != blip {
		a.setEffectiveVolumes(master, music, sfx, int(nv))
	}
	// Blip cadence (1 blip / N letters; left = faster). Mirrors Settings → Blips. No mute.
	if nv := cell(4, "rate", "Rate", fmt.Sprintf("1/%d", rate), "Blip cadence: 1 blip every N letters (left = faster)", int32(rate), int32(config.MinBlipRate), int32(config.MaxBlipRate), nil); int(nv) != rate {
		a.d.Prefs.SetBlipTyping(int(nv), onSpaces)
		a.applyTimingToRoom()
	}
}

func (a *App) drawLogPanel(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	c.Fill(r, a.partPanelOr(partLog, ColPanel)) // per-part tint (v1.52.0): the log column can carry its own colour
	c.Border(r, ColAccent)                      // match the rest of the UI (buttons/OOC box/panels all border in the accent) — playtest: the log panel was the lone grey outline
	// A "🔊" toggle at the right end drops a compact volume strip above the panel
	// content — adjust volume while the log stays on screen and you keep chatting
	// (the IC box below is untouched). The tabs share the rest of the row.
	const volBtnW = int32(36)
	// By default OOC is its OWN box, so the OOC tab is dropped (and the rest reindex). The Legacy theme
	// — or the opt-in "OOC in the log tab" toggle — instead keeps OOC in the strip. If the active tab
	// was OOC while OOC is a box, or it has been torn into its own floating panel (torntabs.go), fall
	// back to the IC log so the docked area still shows something.
	oocAsTab := a.d.Prefs.LegacyDevThemeOn() || a.d.Prefs.OOCInLogTabOn()
	if (!oocAsTab && a.logTab == logTabOOC) || a.tabTorn(a.logTab) || a.tabHidden(a.logTab) {
		a.logTab = logTabLog
	}
	// The docked strip skips any torn-off tab and compacts the rest (0-alloc stack array).
	docked, numLogTabs := a.dockedLogTabs(oocAsTab)
	tab := (r.W - volBtnW) / numLogTabs
	for i := int32(0); i < numLogTabs; i++ {
		bw := tab
		if i == numLogTabs-1 {
			bw = (r.W - volBtnW) - (numLogTabs-1)*tab // last tab takes the remainder before the Vol toggle
		}
		if c.Button(sdl.Rect{X: r.X + i*tab, Y: r.Y, W: bw, H: btnH}, docked[i].label) {
			a.logTab = docked[i].id
		}
	}
	volBtn := sdl.Rect{X: r.X + r.W - volBtnW, Y: r.Y, W: volBtnW, H: btnH}
	if c.Button(volBtn, "Vol") {
		a.volStripOn = !a.volStripOn
		a.d.Prefs.SetVolStripShown(a.volStripOn) // persist so it survives a restart
	}
	if a.volStripOn {
		c.Border(volBtn, ColAccent) // active cue
	}
	c.Tooltip(volBtn, "Show/hide volume sliders on screen (chat stays usable)")
	innerY := r.Y + btnH + 4
	if a.volStripOn {
		const stripH = int32(44) // two rows: "label NN%" (click = mute) over a full-width slider
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
	switch a.logTab {
	case logTabMusic:
		scale = &a.musicPct // the Music tab tunes its own scale
	case logTabOOC:
		scale = &a.oocPct // the OOC tab resizes OOC text, independent of the IC log
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
		// The OOC tab is a COMPLETE OOC chat: scrollback + its own input + the OOC name. The
		// bottom OOC bar is a SECOND, always-visible input (no name) for a hybrid setup — type
		// from either; both share the same OOC draft (a.oocInput).
		a.drawOOCPanel(inner, true)
		return
	case logTabNotes:
		a.drawNotesTab(inner)
		return
	case logTabFriends:
		a.drawFriendsTab(inner)
		return
	}
	// IC log tab: search box + Copy/TXT/HTML row, then the colored
	// scrollback (AO text colors preserved per entry). The row is sized to
	// the CHROME font: textField vertically centres its text, so a custom
	// whole-UI font taller than the old fixed 24px box spilled its glyphs
	// BELOW the field, straight onto the first log line 4px under it
	// (playtest, Tifera: "the logs overlap with the search bar"). With the
	// stock font the max() keeps the row at exactly 24px — byte-identical.
	rowY := inner.Y
	rowH := logSearchRowH(int32(c.font.Height()))
	searchW := inner.W - 3*logExportBtnPitch - 12
	// A layout-edited slot can be too narrow for the export buttons: give the
	// search field the whole row instead of drawing it at a degenerate width
	// (the placeholder text has no box to live in at W <= 0).
	showExports := searchW >= logSearchMinW
	if !showExports {
		searchW = inner.W
	}
	a.logSearch, _ = c.TextField("logsearch", sdl.Rect{X: inner.X, Y: rowY, W: searchW, H: rowH}, a.logSearch, "Search log...")
	if showExports {
		bx := inner.X + searchW + 4
		if c.Button(sdl.Rect{X: bx, Y: rowY, W: logExportBtnW, H: rowH}, "Copy") {
			a.copyICLog()
		}
		if c.Button(sdl.Rect{X: bx + logExportBtnPitch, Y: rowY, W: logExportBtnW, H: rowH}, "TXT") {
			a.exportICLog(false)
		}
		if c.Button(sdl.Rect{X: bx + 2*logExportBtnPitch, Y: rowY, W: logExportBtnW, H: rowH}, "HTML") {
			a.exportICLog(true)
		}
	}
	a.drawICLogList(sdl.Rect{X: inner.X, Y: rowY + rowH + logSearchRowGap, W: inner.W, H: inner.H - rowH - logSearchRowGap})
}

const (
	// logSearchRowMinH is the search/export row's historical height — the
	// floor, so the stock chrome font keeps the exact old geometry.
	logSearchRowMinH = int32(24)
	// logSearchRowPadV is the vertical breathing room around the chrome
	// font inside the search field (glyphs must never leave the box).
	logSearchRowPadV = int32(8)
	// logSearchRowGap separates the row from the log list below it.
	logSearchRowGap = int32(4)
	// logSearchMinW is the narrowest useful search field; below it the
	// export buttons yield the row (a layout-edited skinny log slot).
	logSearchMinW = int32(60)
	// logExportBtnW/Pitch are the Copy/TXT/HTML button width and spacing.
	logExportBtnW     = int32(50)
	logExportBtnPitch = int32(52)
)

// logSearchRowH sizes the log panel's search/export row to the chrome font,
// floored at the historical 24px so the stock font is byte-identical.
func logSearchRowH(fontH int32) int32 {
	if h := fontH + logSearchRowPadV; h > logSearchRowMinH {
		return h
	}
	return logSearchRowMinH
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

// logScrollEaseTauMs is the smooth-scroll time constant (#22): the glide covers
// ~63% of the remaining distance per τ, visually settled in ~4τ (≈0.2 s) —
// well inside the input full-rate grace, so the pacer keeps it fluid.
const logScrollEaseTauMs = 50.0

// easeICScroll advances the eased on-screen offset toward the a.icScroll
// target and returns it clamped. snap (a scrollbar drag) pins it 1:1 — a drag
// must track the hand exactly. Frame-rate independent via frameDtMs.
func (a *App) easeICScroll(maxScroll int32, snap bool) int32 {
	target := float64(a.icScroll)
	if snap || math.Abs(target-a.icScrollVis) < 0.5 {
		a.icScrollVis = target
	} else {
		dt := float64(a.frameDtMs)
		if dt <= 0 {
			dt = 16
		}
		a.icScrollVis += (target - a.icScrollVis) * (1 - math.Exp(-dt/logScrollEaseTauMs))
		a.NoteAnimating() // still gliding toward the target: keep frames coming so the ease doesn't freeze mid-scroll at idle=0
	}
	if a.icScrollVis < 0 {
		a.icScrollVis = 0
	}
	if m := float64(maxScroll); a.icScrollVis > m {
		a.icScrollVis = m
	}
	return int32(a.icScrollVis)
}

// drawICLogList renders the colored IC scrollback (search-filtered,
// word-wrapped to the list width) into rect — used by the classic Log tab
// and the themed ic_chatlog element.
func (a *App) drawICLogList(list sdl.Rect) {
	c := a.ctx
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	// Wrap against the indented width so a continuation row (drawn at
	// +logWrapIndentPx) can never overflow the column.
	rows := a.icWrapped(wrapW-logWrapIndentPx, a.d.Prefs.ICTimestampsOn()) // per-frame pref read, like the IC counter
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
	// #22 smooth scrolling: the on-screen offset EASES toward the target so
	// wheel steps and sticky-bottom jumps glide instead of teleporting
	// ("messages just instantly appear"). A scrollbar drag snaps 1:1 (dragging
	// must track the hand), and everything below — selection hit-math included
	// — uses the SAME visual offset, so clicks always land on what's shown.
	visScroll := a.easeICScroll(maxScroll, c.dragID == "icscroll")
	// Drag-select / Ctrl+C (before the loop so a real drag swallows the click).
	a.handleLogSelect(logSelIC, list, visScroll, lineH, wrapW)
	// Pin-to-notes can also fire on a configurable Ctrl-chord (default Ctrl+N):
	// it pins the HOVERED line, like right-click. Resolved once (the chord is
	// one key/frame); handleHotkeys leaves an unrecognized chord in c.hotkey.
	pinChord := c.hotkey != 0 && strings.EqualFold(sdl.GetKeyName(c.hotkey), a.hotkeyFor(hotkeyPinNote))
	friendsOn := a.d.Prefs.FriendHighlightOn() // gates the per-line friend glow (read once)
	// Per-speaker name colours (read once): tint each entry's name prefix on its
	// first wrapped row. Short-circuit so the default OFF path adds nothing.
	nameColorsOn := a.d.Prefs.NameColorsOn()
	boldNames := a.d.Prefs.BoldNamesOn() // read once per frame, passed per line
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
	pulseOn := friendsOn && a.d.Prefs.FriendGlowPulseOn() && !a.d.Prefs.ReduceMotion()
	if pulseOn {
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
	// Link hover spans the WHOLE message: hovering any wrapped row of a linked
	// entry tints every one of its rows (playtest: only the hovered row lit up,
	// so a wrapped link read as one highlighted line among plain ones). O(1):
	// the hovered row indexes straight into rows; the loop compares entry ids.
	hoverLinkEntry := -1
	if c.hovering(list) && c.mouseX < list.X+wrapW {
		if li := int((c.mouseY - list.Y + visScroll) / lineH); li >= 0 && li < len(rows) {
			if a.icLog[rows[li].entry].url != "" {
				hoverLinkEntry = rows[li].entry
			}
		}
	}
	// Scissor the scrollback to the list rect so the partially scrolled
	// top/bottom row can't draw past it onto the tab strip above.
	clipPrev, clipHad := c.pushClip(list)
	y := list.Y - visScroll
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
				if pulseOn {
					a.NoteAnimating() // a visible friend glow is breathing: keep frames coming so the pulse animates at idle=0
				}
			}
			// Selection highlight sits under the text (and the divider).
			a.drawLogSelHighlight(logSelIC, ri, list.X, y, wrapW, lineH, row.text, font)
			// Unread divider: a thin accent rule at the top of the first unread
			// line, so "jump to last read" lands on an obvious boundary.
			if ri == firstUnreadRow {
				c.Fill(sdl.Rect{X: list.X, Y: y, W: wrapW, H: unreadDividerH}, ColAccent)
			}
			// A message carrying a link reads as a link on hover (accent, every
			// wrapped row of it) and opens on click — the whole message is the
			// hit target.
			if row.entry == hoverLinkEntry {
				col = ColAccent
			}
			// Per-speaker name colour: tint the name prefix on an entry's FIRST
			// wrapped row (the shared helper falls back to a plain draw for
			// system/evidence lines or when a long name wrapped off this row).
			lineSpeaker := ""
			if nameColorsOn && (ri == 0 || rows[ri-1].entry != row.entry) {
				lineSpeaker = a.icLog[row.entry].speaker
			}
			indent := a.logRowIndent(logSelIC, ri) // continuation rows hang right of their first row
			a.drawLogLineNamed(font, c.EmojiFont(a.logPct), list.X+indent, y, wrapW-indent, row.text, lineSpeaker, col, nameColorsOn, nameSat, nameVal, boldNames)
			if u := a.icLog[row.entry].url; u != "" {
				if c.hovering(rowRect) {
					c.Tooltip(rowRect, "Open "+u)
					if c.clicked {
						openBrowser(schemeForOpen(u)) // bare "www." link → https:// at open time
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
	} else if !a.icStick && maxScroll > 0 {
		// #217 scroll-to-bottom: scrolled up with nothing new to announce, so the
		// accent "N new" pill above doesn't fire — offer a plain "jump to latest"
		// button (Discord-style) so you don't have to drag the scrollbar all the
		// way down after re-reading. Subtler colour than the unread pill because
		// there's nothing new pulling the eye. Anchored bottom-right by the bar.
		label := "↓ Latest"
		bw := c.TextWidth(label) + 20
		pill := sdl.Rect{X: list.X + list.W - scrollBarW - scrollBarGap - bw, Y: list.Y + list.H - 26, W: bw, H: 22}
		c.Fill(pill, ColPanelHi)
		c.Border(pill, ColAccent)
		c.Label(pill.X+10, pill.Y+4, label, ColText)
		if c.hovering(pill) {
			c.Tooltip(pill, "Jump to the newest message")
			if c.clicked {
				a.icScroll, a.icStick = maxScroll, true
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
	font := c.LogFont(a.oocPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	// Wrap against the indented width so a continuation row (drawn at
	// +logWrapIndentPx) can never overflow the column.
	lines := a.oocWrapped(wrapW - logWrapIndentPx) // MOTDs wrap — never truncate
	nameColorsOn := a.d.Prefs.NameColorsOn()       // per-speaker OOC name colours (read once)
	boldNames := a.d.Prefs.BoldNamesOn()           // read once per frame, passed per line
	var nameSat, nameVal float64
	if nameColorsOn {
		nameSat = float64(a.d.Prefs.NameColorSat()) / 100
		nameVal = float64(a.d.Prefs.NameColorVal()) / 100
	}
	contentH := int32(len(lines)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	// Ctrl+wheel (fine) or wheel-button-held (fast) zooms the OOC text (independent of the IC log).
	zoomed := a.zoomWheel(list, &a.oocPct, config.MinLogScalePercent, config.MaxLogScalePercent)
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
	// Hovering any wrapped row of a linked message tints the whole run of it.
	linkLo, linkHi := a.oocHoverLinkRun(list, a.oocScroll, lineH, wrapW, len(lines))
	clipPrev, clipHad := c.pushClip(list) // top/bottom row clipped to the rect, not the tabs
	y := list.Y - a.oocScroll
	for li, line := range lines {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			col := ColText
			font := c.LogFontFor(a.oocPct, line)
			// Selection highlight sits under the text.
			a.drawLogSelHighlight(logSelOOC, li, list.X, y, wrapW, lineH, line, font)
			// Links in OOC are openable (click) and copyable (right-click) —
			// matching the IC log. The link is the entry's, resolved at wrap
			// time (oocWrapURL), so a URL the wrap hard-split still opens whole.
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			if li >= linkLo && li <= linkHi {
				col = ColAccent
			}
			if c.hovering(rowRect) && li < len(a.oocWrapURL) && a.oocWrapURL[li] != "" {
				a.oocLinkActions(rowRect, a.oocWrapURL[li])
			}
			sp := ""
			if li < len(a.oocWrapName) {
				sp = a.oocWrapName[li]
			}
			indent := a.logRowIndent(logSelOOC, li) // continuation rows hang right of their first row
			a.drawLogLineNamed(font, c.EmojiFont(a.oocPct), list.X+indent, y, wrapW-indent, line, sp, col, nameColorsOn, nameSat, nameVal, boldNames)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)
}

// oocHoverLinkRun resolves the OOC row under the cursor to the contiguous run
// of rows sharing its link — the wrapped rows of one linked paragraph — so the
// hover tint covers the WHOLE message (playtest: only the hovered row lit up,
// so a wrapped link read as one highlighted line among plain ones). Returns
// lo=0, hi=-1 (an empty range) when the cursor isn't on a linked row. The
// outward walks are bounded by the per-entry wrap cap: a same-URL run longer
// than that is several paragraphs, and one paragraph is the unit we tint.
//
// The walk requires the SAME url AND the same source oocLog entry
// (oocWrapSrc) — this keys OOC the way IC keys off icWrapLine.entry, so two
// ADJACENT DISTINCT messages that happen to carry the same URL no longer merge
// into one tinted run (the URL boundary alone used to). The url boundary is
// kept too: within ONE multi-paragraph entry, a per-paragraph link (each line
// its own URL — TestOOCWrapURLPerParagraph) still tints only its own
// paragraph, not the whole entry.
func (a *App) oocHoverLinkRun(list sdl.Rect, scroll, lineH, wrapW int32, n int) (lo, hi int) {
	c := a.ctx
	lo, hi = 0, -1
	if lineH <= 0 || !c.hovering(list) || c.mouseX >= list.X+wrapW {
		return
	}
	li := int((c.mouseY - list.Y + scroll) / lineH)
	if li < 0 || li >= n || li >= len(a.oocWrapURL) || a.oocWrapURL[li] == "" {
		return
	}
	url := a.oocWrapURL[li]
	// A run row must match the hovered row's URL AND its source oocLog entry. The
	// source parallel is always present in-app; a bare unit test may set only
	// oocWrapURL, so a missing/short oocWrapSrc degrades to url-only equality (the
	// old behavior) rather than misbehaving. The check is inlined into both walks
	// (no closure) so this stays allocation-free on the per-frame draw path.
	src, hasSrc := -1, li < len(a.oocWrapSrc)
	if hasSrc {
		src = a.oocWrapSrc[li]
	}
	lo, hi = li, li
	for steps := 0; lo > 0 && a.oocWrapURL[lo-1] == url &&
		(!hasSrc || (lo-1 < len(a.oocWrapSrc) && a.oocWrapSrc[lo-1] == src)) &&
		steps < oocWrapMaxLinesPerEntry; steps++ {
		lo--
	}
	for steps := 0; hi+1 < len(a.oocWrapURL) && a.oocWrapURL[hi+1] == url &&
		(!hasSrc || (hi+1 < len(a.oocWrapSrc) && a.oocWrapSrc[hi+1] == src)) &&
		steps < oocWrapMaxLinesPerEntry; steps++ {
		hi++
	}
	return
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
		openBrowser(schemeForOpen(url)) // bare "www." link → https:// at open time (copy stays bare, as displayed)
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
	a.recordSentOOC(a.oocInput) // remember the raw line for Up-arrow recall (mirrors IC #8)
	a.oocInput = ""             // clears at send (no echo protocol) — the field's undo history catches it (Ctrl+Z)
}

// drawOOCPanel is the actual OOC box: full scrollable history plus the
// identity fields — IC showname (live; outgoing messages read it per
// send) and the permanent OOC name. Both persist via the debounced saver.
func (a *App) drawOOCPanel(r sdl.Rect, withInput bool) {
	c := a.ctx
	fH := a.inputFieldH()
	nFields := int32(1) // permanent OOC name (OOC is only an OOC name + OOC chat — no IC showname here)
	if withInput {
		nFields = 2 // + the OOC message input, so the box is a complete OOC chat (one thing)
	}
	fieldsH := nFields*(fH+6) + 4
	list := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - fieldsH}

	font := c.LogFont(a.oocPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	// Wrap against the indented width so a continuation row (drawn at
	// +logWrapIndentPx) can never overflow the column.
	lines := a.oocWrapped(wrapW - logWrapIndentPx) // MOTDs wrap — never truncate
	nameColorsOn := a.d.Prefs.NameColorsOn()       // per-speaker OOC name colours (read once)
	boldNames := a.d.Prefs.BoldNamesOn()           // read once per frame, passed per line
	var nameSat, nameVal float64
	if nameColorsOn {
		nameSat = float64(a.d.Prefs.NameColorSat()) / 100
		nameVal = float64(a.d.Prefs.NameColorVal()) / 100
	}
	contentH := int32(len(lines)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	// Ctrl+wheel (fine) / wheel-button (fast) zooms the OOC text, INDEPENDENT of the IC
	// log. As a docked tab the right-column zoom already consumed the wheel (wheelTaken);
	// as the standalone default OOC box this is the only handler.
	zoomed := a.zoomWheel(list, &a.oocPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	maxScroll := contentH - list.H
	if maxScroll < 0 {
		maxScroll = 0
	}
	if !zoomed { // a zoom consumed the wheel — don't also scroll
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
	// Hovering any wrapped row of a linked message tints the whole run of it.
	linkLo, linkHi := a.oocHoverLinkRun(list, a.oocScroll, lineH, wrapW, len(lines))
	clipPrev, clipHad := c.pushClip(list) // scrollback only; restored before the fields below
	y := list.Y - a.oocScroll
	for li, line := range lines {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			col := ColText
			font := c.LogFontFor(a.oocPct, line)
			a.drawLogSelHighlight(logSelOOC, li, list.X, y, wrapW, lineH, line, font)
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			if li >= linkLo && li <= linkHi {
				col = ColAccent
			}
			if c.hovering(rowRect) && li < len(a.oocWrapURL) && a.oocWrapURL[li] != "" {
				a.oocLinkActions(rowRect, a.oocWrapURL[li])
			}
			sp := ""
			if li < len(a.oocWrapName) {
				sp = a.oocWrapName[li]
			}
			indent := a.logRowIndent(logSelOOC, li) // continuation rows hang right of their first row
			a.drawLogLineNamed(font, c.EmojiFont(a.oocPct), list.X+indent, y, wrapW-indent, line, sp, col, nameColorsOn, nameSat, nameVal, boldNames)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)

	// Identity fields: full width (side labels squished the boxes in the
	// narrow right column) — the placeholders carry the labels.
	fy := r.Y + r.H - fieldsH + 4
	if withInput {
		// The OOC message bar lives INSIDE the box now (one unified OOC chat), not a separate bottom row.
		var sent bool
		oocPrimary, oocEmoji := a.icFieldFonts(a.oocInput)
		a.oocInput, sent = c.TextFieldEmoji("oocmsg", sdl.Rect{X: r.X, Y: fy, W: r.W - 4, H: fH}, a.oocInput, "OOC chat… (Enter to send)", oocPrimary, oocEmoji)
		if sent {
			a.submitOOC()
		}
		a.recallOOC() // Up/Down recall recently-sent OOC lines when this field is focused
		fy += fH + 6
	}
	// OOC carries only an OOC name + the OOC chat — the IC showname is set on the IC bar
	// and in Settings, so it has no business in the OOC box (beta feedback).
	// TAB-LOCAL: this field used to write the saved default too, so a name
	// typed here followed you into every other tab (playtest). The permanent
	// default lives in Settings → Identity.
	a.oocName, _ = c.TextField("oocname2", sdl.Rect{X: r.X, Y: fy, W: r.W - 4, H: fH}, a.oocName, "OOC name (this tab)")
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
	// Search filter (#20 — parity with the Music tab beside it): type to narrow
	// a hub server's hundreds of areas. Memoized so the O(N) scan runs only when
	// the query or the Areas list changes (refreshAreaFilter).
	// Inset the search field 2px to match the area cards' left edge below (the
	// cards draw at X: r.X+2). The bordered-card look insets the rows, but the
	// search box was ported verbatim from drawMusicList (whose rows start at
	// r.X, no inset), so it sat 2px left of its column and clipped out. Shrinking
	// width by 2 keeps the right edge exactly where it was (r.X+r.W-150),
	// preserving the "shown / total" counter-label clearance drawn just past it.
	a.areaSearch, _ = c.TextField("areasearch", sdl.Rect{X: r.X + 2, Y: r.Y, W: r.W - 152, H: fieldH}, a.areaSearch, "Search areas…")
	query := strings.ToLower(strings.TrimSpace(a.areaSearch))
	total := len(a.sess.Areas)
	shown := total
	if query != "" {
		a.refreshAreaFilter(query)
		shown = len(a.areaFiltered)
	}
	c.Label(r.X+r.W-142, r.Y+5, fmt.Sprintf("%d / %d", shown, total), ColTextDim)
	r.Y += fieldH + 6
	r.H -= fieldH + 6
	if !c.ctrlHeld { // ctrl+wheel resizes text, never scrolls
		a.areaScroll -= c.WheelIn(r) * scrollStepPx
	}
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height())*2 + 11 // two-row entries: name + an indented detail line
	contentH := int32(shown) * lineH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.areaScroll = c.VScrollbar("areascroll", track, a.areaScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r) // partial top/bottom row stays inside the panel
	defer c.popClip(clipPrev, clipHad)
	y := r.Y - a.areaScroll
	for vi := 0; vi < shown; vi++ {
		// i is the ORIGINAL index into a.sess.Areas (and the parallel AreaInfo);
		// with a query active it comes from the filtered index list.
		i := vi
		if query != "" {
			i = a.areaFiltered[vi]
		}
		if i < 0 || i >= len(a.sess.Areas) {
			continue
		}
		area := a.sess.Areas[i]
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-lineH {
			card := sdl.Rect{X: r.X + 2, Y: y + 2, W: r.W - scrollBarW - scrollBarGap - 4, H: lineH - areaCardGap}
			hover := c.hovering(card)
			current := area == a.curArea
			// Two-row entry: the name (population-coloured) on top, an indented
			// detail line (count / status / lock / CM) under it. A locked room tints
			// the whole row red, the area you're IN is accent. Fixes "it's all gray,
			// I can't tell what's busy or where I am / every area looked the same".
			nameCol := ColTextDim
			detail := ""
			locked := false
			if i < len(a.sess.AreaInfo) {
				info := &a.sess.AreaInfo[i]
				if info.Players > 0 {
					nameCol = ColText // populated reads at full strength
				}
				if info.Players >= 0 {
					unit := "users"
					if info.Players == 1 {
						unit = "user"
					}
					detail = fmt.Sprintf("%d %s", info.Players, unit)
				}
				if info.Status != "" {
					detail += "  ·  " + info.Status
					if strings.ToUpper(info.Status) != "IDLE" {
						nameCol = areaStatusColor(info.Status) // a real status colours it; IDLE keeps the population colour
					}
				}
				switch strings.ToUpper(info.Lock) {
				case "LOCKED":
					detail += "  ·  locked"
					locked = true
				case "SPECTATABLE":
					detail += "  ·  spectatable"
				}
				if info.CM != "" && !strings.EqualFold(info.CM, "FREE") {
					detail += "  ·  CM: " + info.CM
				}
			}
			fill, border := ColPanel, ColPanelHi // resting card = a plain button (bordered + filled = visually separate)
			switch {
			case locked:
				fill, border, nameCol = areaLockedBg, areaLockedBorder, ColText
			case current:
				fill, border, nameCol = areaCurrentBg, ColAccent, ColAccent
			case hover:
				fill, border = ColPanelHi, ColAccent
			}
			c.Fill(card, fill)
			c.Border(card, border)
			subH := int32(font.Height())
			c.LabelClippedFont(font, card.X+6, card.Y+3, card.W-12, area, nameCol)
			if detail != "" {
				c.LabelClippedFont(font, card.X+18, card.Y+5+subH, card.W-24, detail, ColTextDim)
			}
			if c.ClickedIn(card) { // press+release in-card: a drag-in release must not transfer areas
				a.jumpToArea(area) // consolidated: scrollback swap + curArea + presence + MC + warn line
			}
		}
		y += lineH
	}
	if shown == 0 && query != "" {
		c.Label(r.X+4, r.Y+6, "No areas match your search.", ColTextDim)
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
	a.applyPendingFav() // a ★ toggle a cell deferred: rebuild AFTER the grid loop, never during it
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
		if i >= len(a.iniWardrobe) || i >= len(a.iniFolders) || i >= len(a.iniLower) {
			return false // never index past the parallel slices if they ever shrink mid-frame
		}
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
		// DEFER the toggle: rebuilding the wardrobe here shrinks the iniList/
		// iniWardrobe/iniFolders slices the grid loop is currently ranging, which
		// panicked on a REMOVE (the reported crash). drawIniswapPanel applies it
		// after the loop instead.
		a.iniFavPending = name
		a.iniFavPendingAdd = idx >= len(a.iniWardrobe) || !a.iniWardrobe[idx]
		c.clicked = false // consumed; don't also wear it
		return
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
		a.d.Manager.PrefetchChain(a.previewBase, a.urls.EmoteAlts(name, "normal", courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
		a.ensurePreviewEmotes(name)                                                                                                                       // try-before-wear: load this character's emotes to cycle
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

// areaFilterKey memoizes the areas-list filter (#20), mirroring musicFilterKey.
// Keyed by the query plus a cheap identity of the Areas list (len +
// first/last name), which changes whenever the server resends the area list.
// It deliberately does NOT key on AreaInfo (player counts / status / lock):
// those change on every area packet, and the name filter doesn't depend on
// them, so including them would defeat the memo and re-scan constantly.
type areaFilterKey struct {
	q           string
	n           int
	first, last string
}

// refreshAreaFilter recomputes the matching area indices for a non-empty query,
// memoized against the query + the list identity — same O(1)-per-frame guard as
// refreshMusicFilter. The stored indices are ORIGINAL indices into a.sess.Areas,
// which parallels a.sess.AreaInfo, so a caller uses ti (not the visible index)
// to look up either.
func (a *App) refreshAreaFilter(query string) {
	n := len(a.sess.Areas)
	key := areaFilterKey{q: query, n: n}
	if n > 0 {
		key.first, key.last = a.sess.Areas[0], a.sess.Areas[n-1]
	}
	if key == a.areaFilterMemo {
		return
	}
	a.areaFilterMemo = key
	a.areaFiltered = a.areaFiltered[:0]
	for i, area := range a.sess.Areas {
		if strings.Contains(strings.ToLower(area), query) {
			a.areaFiltered = append(a.areaFiltered, i)
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
	// FIX #9 parity (playtest: "Volume button in Music tab does not work"): drive the
	// EFFECTIVE (per-server when connected) volumes, exactly like the strip and the
	// Settings/Extras sliders. This menu used to read/write the GLOBAL prefs, which a
	// connected server's per-server audio profile overrides — so as soon as volume had
	// been touched anywhere else (those all write per-server), these sliders moved
	// values nothing was listening to.
	master, music, sfx, blip := a.effectiveVolumes()
	row := func(id, label string, val int) int {
		c.Label(r.X, y+6, label, ColText)
		track := sdl.Rect{X: r.X + 80, Y: y + 7, W: r.W - 80 - 56, H: 16}
		nv := int(c.Slider("musicvol:"+id, track, int32(val), 100))
		c.Label(r.X+r.W-48, y+6, strconv.Itoa(nv)+"%", ColTextDim)
		y += 40
		return nv
	}
	if nv := row("master", "Master", master); nv != master {
		a.setEffectiveVolumes(nv, music, sfx, blip)
	}
	if nv := row("music", "Music", music); nv != music {
		a.setEffectiveVolumes(master, nv, sfx, blip)
	}
	if nv := row("sfx", "SFX", sfx); nv != sfx {
		a.setEffectiveVolumes(master, music, nv, blip)
	}
	if nv := row("blip", "Blip", blip); nv != blip {
		a.setEffectiveVolumes(master, music, sfx, nv)
	}
	// Blip rate, right under the Blip volume — the same cadence control as the volume
	// strip's "Rate" column and Settings → Blips, mirrored here so it's adjustable straight
	// from the Music menu's volume view. Range is clamped like the strip (left = faster).
	rate, onSpaces := a.d.Prefs.BlipTyping()
	c.Label(r.X, y+6, "Blip rate", ColText)
	rtrack := sdl.Rect{X: r.X + 80, Y: y + 7, W: r.W - 80 - 56, H: 16}
	nr := clampI32(c.Slider("musicvol:rate", rtrack, int32(rate), int32(config.MaxBlipRate)), int32(config.MinBlipRate), int32(config.MaxBlipRate))
	c.Tooltip(rtrack, "Blip cadence: 1 blip every N letters (left = faster)")
	c.Label(r.X+r.W-48, y+6, "1/"+strconv.Itoa(int(nr)), ColTextDim)
	if int(nr) != rate {
		a.d.Prefs.SetBlipTyping(int(nr), onSpaces)
		a.applyTimingToRoom()
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
		a.d.Prefs.SetMusicVolMode(a.musicVolMode) // persist so the volume view survives a restart
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

// icBarUnderStage is the #8 default layout: the IC input bar sits DIRECTLY under the
// stage (icBarTop, the classic AO spot so it's obvious where you talk IC), and the
// control-button block sits fH + a small gap below it (defY). Pure, so the "input is
// under the viewport, above the controls" invariant is unit-pinnable.
func icBarUnderStage(vp sdl.Rect, fH int32) (icBarTop, defY int32) {
	icBarTop = vp.Y + vp.H + pad
	defY = icBarTop + fH + 8 // gap below the input row
	return
}

func (a *App) drawICControls(w, h int32, vp sdl.Rect) {
	c := a.ctx
	// #8 (ZeitHeld): the IC INPUT BAR sits DIRECTLY under the stage — the classic AO
	// spot — so it's obvious where you talk IC; the control buttons, judge strip and
	// emote grid follow BELOW it. fH (the input-row height) is needed up here to place
	// the control block below the bar.
	fH := a.inputFieldH()
	icBarTop, defY := icBarUnderStage(vp, fH) // IC bar under the stage; control block below it

	// Control-button slot ("controls"): the two button rows — shouts / pair / layout
	// knobs (row 1) and the utility buttons (row 2, which wraps) — move together as one
	// block. Un-edited, the content width is w-2*pad, so the wrap structure — and
	// therefore the block's row count and height — matches the classic layout exactly
	// (clusterX == pad, y == defY, dy == 0 ⇒ every rect, every wrap edge identical:
	// the un-edited courtroom stays pixel-identical). An override translates the block
	// and, since v1.52.0 (Tifera), its WIDTH drives the wrap edge — narrowing re-wraps
	// the buttons into a taller stack, and the judge strip / emote grid below anchor to
	// the re-wrapped bottom (y2 - dy) so they keep clear of it. Height is always
	// content-driven. The override is read lock-free; off the edit path this is one map
	// lookup, no alloc. controlsBlockOrigin is the pure core (unit-pinned:
	// TestControlsBlockOrigin).
	ctrlOv, ctrlEdited := a.classicOv[slotControls]
	var ctrlRect sdl.Rect
	if ctrlEdited {
		ctrlRect = a.anchoredRect(slotControls, ctrlOv, w, h) // window pin honoured
	}
	clusterX, y, dy, clusterRight := controlsBlockOrigin(ctrlRect, ctrlEdited, w, defY)

	// Row 1: shouts, pairing, and the live layout knobs (both hideable).
	// Extracted to drawICShoutRow (rects by value, no closures — the row stays
	// alloc-free) so the send decision below reads pendingShout as before.
	pendingShout := a.drawICShoutRow(clusterX, y, w, h)

	// Row 2: utility buttons (their own row so nothing overlaps at any
	// viewport scale or window width). Split into the legacy-dev-theme and the
	// grouped-default variants; each returns the (wrapped) row bottom y2 and a
	// `done` flag set when a Disconnect click already fired requestDisconnect —
	// in which case the frame short-circuits exactly as the inline `return` did.
	y2 := y + btnH + 4
	var done bool
	if a.d.Prefs.LegacyDevThemeOn() {
		y2, done = a.drawICUtilityRowLegacy(clusterX, y2, clusterRight, w, h)
	} else {
		y2, done = a.drawICUtilityRowGrouped(clusterX, y2, clusterRight, w, h)
	}
	if done {
		return // Disconnect requested — skip the rest of this frame (was an inline return)
	}

	// The control-button block ends here. Register it for the editor (its height is
	// content-driven, so measure it now). The grab box spans the LIVE content width —
	// the wrap edge the rows actually used — so the side handles sit where the
	// buttons end after a width resize. def keeps the default full width; its height
	// reflects the live row count (recomputing the default wrap would mean laying the
	// rows out twice — reset just recomputes for real next frame).
	if a.classicEdit {
		a.regSlot(slotControls,
			sdl.Rect{X: clusterX, Y: y, W: clusterRight - clusterX, H: y2 + btnH - y},
			sdl.Rect{X: pad, Y: defY, W: w - 2*pad, H: y2 - dy + btnH - defY})
	}

	// Judge strip (JD grant, or the judge stand when pos-dependent) + the emote grid now
	// follow the control block, which sits BELOW the IC bar (#8). They anchor to the
	// block's UN-MOVED bottom (y2 - dy). The IC bar itself anchors to icBarTop (under the
	// stage), independent of this chain, so the judge strip never pushes the input around.
	postY := y2 - dy + btnH + 6
	if a.judgeVisible() {
		postY += a.drawJudgeRow(pad, postY)
	}

	// IC input row (height follows the Box knob), led by the AO2 text
	// color selector: a swatch previews the active wire color (MS
	// text_color 0–9), the dropdown names it (AO2's color dropdown). The
	// showname box OVERRIDES the Settings showname for the session.
	// The whole IC bar is a movable + resizable slot ("icbar"). Its default now spans the
	// stage width (vp.W) DIRECTLY under the stage (icBarTop, #8) — the first thing below
	// the viewport. Everything inside lays out relative to the slot (icBar.X / .W) and the
	// fH-tall row is centred within the slot height, so a drag moves the bar and a width
	// resize widens / narrows the text input (still floored at minICInputW). The emoji /
	// FX / React buttons store their live rects as they draw, so their pop-ups follow the
	// bar wherever it lands. slotRect is alloc-free off the edit path. (fH is computed at
	// the top now, to place the control block below this bar.)
	// v1.50.5 (Nightingale): the whole-bar "IC input bar" PANEL slot is gone —
	// every element below is its own independent movable+resizable slot, so the
	// bar is just the DEFAULT row geometry they lay out from. (An old "icbar"
	// override in prefs is simply ignored; Reset all clears it.)
	icBar := sdl.Rect{X: pad, Y: icBarTop, W: vp.W, H: fH}
	rowY := icBar.Y
	// Colour swatch + dropdown — an individually movable slot (#4a). The selector also
	// offers the extended AsyncAO colours (#98) and the two "fun colour" modes (#79): they
	// sit after the palette so they're picked like any colour instead of being buried in
	// Settings. icColorSelected drives the active row + swatch; applyICColorPick routes
	// the pick — both shared with the themed row so the two layouts can't drift.
	colorBox := a.slotRect(slotICColor, sdl.Rect{X: icBar.X, Y: rowY, W: 32 + colorSelectW, H: fH}, w, h)
	a.drawICColorStrip(colorBox) // swatch + colour dropdown (rect by value — alloc-free)
	// The rest of the IC bar (showname → Immediate → Additive → SFX → emoji → FX →
	// text input → muted chip) is one sequential cursor chain, kept together in
	// drawICInputRow. It returns whether Enter was pressed so the send decision
	// stays visible here alongside the shout row's pendingShout. icBar is passed by
	// value (alloc-free); the chain flows from the DEFAULT positions, never an
	// override, so freeing one slot never cascades the rest (unchanged behaviour).
	send := a.drawICInputRow(icBar, rowY, w, h, fH)
	if send || pendingShout != 0 {
		a.sendIC(pendingShout)
	}

	// Emote row — a movable/resizable slot (the grid pages within whatever rect it
	// gets, so drag/resize is free). slotRect stays INSIDE the hidden guard so the
	// editor never registers a handle for a grid that isn't drawn. The default is
	// byte-identical to before, so an un-edited courtroom is pixel-identical.
	emoteY := postY // #8: the emote grid follows the control block + judge strip, below the IC bar
	if !a.panelHidden(panelEmotes) {
		emoteDef := sdl.Rect{X: pad, Y: emoteY, W: w - 2*pad, H: h - emoteY - 30}
		a.drawEmoteRow(a.slotRect(slotEmotes, emoteDef, w, h), vp)
	}

	// OOC row at the very bottom: a FULL-width OOC chat INPUT (no name box — the OOC name
	// lives in the OOC box/tab now; it used to be duplicated here, which is the redundancy
	// a tester flagged). Shown whenever OOC is a tab (Legacy theme, or the opt-in "OOC in the
	// log tab" toggle), as a SECOND always-visible input alongside the tab's own — the hybrid.
	// In the new-default OOC BOX the input lives inside the box, so this bar is dropped.
	oocY := h - fH - 4
	if !a.panelHidden(panelOOC) && (a.d.Prefs.LegacyDevThemeOn() || a.d.Prefs.OOCInLogTabOn()) {
		// The bottom OOC bar is its own movable + resizable slot ("oocbar"): its default spans
		// the bottom row, the row centred within the slot height. Registered only while it
		// actually draws, so the editor offers a handle for it only when OOC is a tab.
		oocBarDef := sdl.Rect{X: pad, Y: oocY, W: w - 3*pad, H: fH}
		oocBar := a.slotRect(slotOOCBar, oocBarDef, w, h)
		oocRowY := oocBar.Y
		if oocBar.H > fH {
			oocRowY = oocBar.Y + (oocBar.H-fH)/2
		}
		oocPrimary, oocEmoji := a.icFieldFonts(a.oocInput) // #M5: emoji/unicode in the OOC bar too
		var sendOOC bool
		a.oocInput, sendOOC = c.TextFieldEmoji("ooc", sdl.Rect{X: oocBar.X, Y: oocRowY, W: oocBar.W, H: fH}, a.oocInput, "OOC chat…  (set your OOC name in the OOC tab)", oocPrimary, oocEmoji)
		if sendOOC {
			a.submitOOC() // shared OOC send: uses your OOC name (or the server default) + clears
		}
		a.recallOOC() // Up/Down recall recently-sent OOC lines when the bar is focused
		// Ctrl+wheel over the OOC row resizes the OOC text (independent of the IC log).
		if c.ctrlHeld && c.wheelY != 0 && c.hovering(sdl.Rect{X: oocBar.X, Y: oocRowY, W: oocBar.W, H: fH}) {
			a.oocPct = clampInt(a.oocPct+int(c.wheelY)*config.ScaleStepPercent,
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

}

// drawICShoutRow draws control-block Row 1 — the shout buttons (Hold It / Objection
// / Take That + the 2.10 custom interjection), the Pair toggle and the live layout
// knobs — from the block origin (clusterX, y). It returns the shout modifier the
// user clicked (0 = none), which the caller folds into its send decision. clusterX
// / y arrive by value and the local x cursor never escapes, so the row stays
// allocation-free (moved verbatim from drawICControls).
func (a *App) drawICShoutRow(clusterX, y, w, h int32) (pendingShout int) {
	c := a.ctx
	x := clusterX
	if !a.panelHidden(panelShouts) {
		shoutW := int32(96)
		// Each shout is individually movable in the layout editor (literal slot keys
		// keep the row alloc-free; the struct is range-only so it stays on the stack).
		shouts := []struct {
			key   string
			label string
			mod   int
		}{
			{"ctrl.holdit", "Hold It!", protocol.ShoutHoldIt},
			{"ctrl.objection", "Objection!", protocol.ShoutObjection},
			{"ctrl.takethat", "Take That!", protocol.ShoutTakeThat},
		}
		for _, s := range shouts {
			if a.panelHidden(s.key) {
				continue // individually hideable (the row compacts); the group toggle still hides all
			}
			if a.movableButton(s.key, sdl.Rect{X: x, Y: y, W: shoutW, H: btnH}, s.label, w, h) {
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
	if !a.panelHidden("ctrl.pair") { // hideable (v1.50.5): hiding compacts, like ctrlSlot
		if a.movableButton("ctrl.pair", sdl.Rect{X: x, Y: y, W: 70, H: btnH}, "Pair...", w, h) {
			a.showPair = !a.showPair
		}
		x += 80
	}
	if !a.panelHidden(panelKnobs) && !a.d.Prefs.DragLayoutOn() { // drag-resize mode hides the +/− knobs
		if a.d.Prefs.ViewportExactWidth() == 0 { // exact-px sizing shadows vpPct, so the View knob would no-op — hide it (set the size in Settings instead)
			x = a.scaleControl(x, y, "View", &a.vpPct, config.ViewportStepPercent, config.MinViewportPercent, config.MaxViewportPercent)
		}
		x = a.scaleControl(x, y, "Text", &a.chatPct, config.ScaleStepPercent, config.MinChatScalePercent, config.MaxChatScalePercent)
		x = a.scaleControl(x, y, "MsgBox", &a.boxPct, config.ScaleStepPercent, config.MinChatBoxPercent, config.MaxChatBoxPercent)
		x = a.scaleControl(x, y, "Log", &a.logPct, config.ScaleStepPercent, config.MinLogScalePercent, config.MaxLogScalePercent)
		_ = a.scaleControl(x, y, "Input", &a.inputPct, config.ScaleStepPercent, config.MinInputPercent, config.MaxInputPercent)
	}
	return pendingShout
}

// drawICUtilityRowLegacy draws control-block Row 2 in the legacy-dev-theme layout
// (the flat, ungrouped button run). It threads the row cursor y2 (which wraps to a
// fresh row when the trailing buttons wouldn't fit) in and out by value, and returns
// done=true when a Disconnect click already fired requestDisconnect so the caller
// short-circuits the frame exactly as the old inline `return` did (moved verbatim).
func (a *App) drawICUtilityRowLegacy(clusterX, y2, clusterRight, w, h int32) (int32, bool) {
	c := a.ctx
	x := clusterX
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
		return y2, true
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
	if x+keysW+btnGap+styleW > clusterRight {
		y2 += btnH + 4
		x = clusterX
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
	x += styleW + btnGap

	// Edit Layout (front door to the live layout editor) + Mod / CM launchers, in the button row so
	// they never float over the emote grid. Wrap to a fresh row as a group if they wouldn't fit.
	editW := int32(94)
	modW, cmW := int32(0), int32(0)
	if a.amIMod() {
		modW = 54
	}
	if a.amICMNow {
		cmW = 46
	}
	if x+editW+modW+cmW+btnGap*2 > clusterRight {
		y2 += btnH + 4
		x = clusterX
	}
	edR := sdl.Rect{X: x, Y: y2, W: editW, H: btnH}
	if c.Button(edR, "Edit Layout") {
		a.openLayoutEditor()
	}
	c.Tooltip(edR, "Live layout editor — drag & resize every box: the stage, log & OOC. Works on any theme; saved across sessions.")
	x += editW + btnGap
	if modW > 0 {
		mR := sdl.Rect{X: x, Y: y2, W: modW, H: btnH}
		if c.Button(mR, "Mod") {
			a.toggleModDash()
		}
		c.Border(mR, ColDanger)
		c.Tooltip(mR, "Moderation tools — server-aware ban / kick")
		x += modW + btnGap
	}
	if cmW > 0 {
		cR := sdl.Rect{X: x, Y: y2, W: cmW, H: btnH}
		if c.Button(cR, "CM") {
			a.toggleCMPanel()
		}
		c.Border(cR, chipCMColor)
		c.Tooltip(cR, "CM area controls — lock / kick-from-area")
		x += cmW + btnGap
	}
	// Group Chat / DMs launcher — a MAIN button (default ON; Settings → Chat to hide).
	// Appended last so it can't shift the buttons before it; skipped (no x advance) off.
	if a.d.Prefs.GroupChatButtonOn() && !a.panelHidden("ctrl.groupchat") {
		const gcW = int32(96)
		if x+gcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		gcR := sdl.Rect{X: x, Y: y2, W: gcW, H: btnH}
		if c.Button(gcR, "Group Chat") {
			a.toggleMessages()
		}
		c.Border(gcR, ColAccent)
		c.Tooltip(gcR, "Private DMs & group chats with other AsyncAO players (also Extras → Group Chat)")
		x += gcW + btnGap
	}
	// Voice button — only in a voice-enabled area (VS_CAPS / Nyathena); appears
	// when you enter a VC room, hidden otherwise. Appended; no x advance when off.
	if a.voiceOfferable() && !a.panelHidden("ctrl.voice") {
		const vcW = int32(80)
		if x+vcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		vcLabel, vcBord := a.voiceButtonState()
		vcR := sdl.Rect{X: x, Y: y2, W: vcW, H: btnH}
		if c.Button(vcR, vcLabel) {
			a.toggleVoice()
		}
		c.Border(vcR, vcBord)
		c.Tooltip(vcR, "Voice chat — this area supports it. Join to talk (Nyathena). Red dot = your mic is live.")
		x += vcW + btnGap
	}
	return y2, false
}

// drawICUtilityRowGrouped draws control-block Row 2 in the grouped-default layout
// (buttons clustered by purpose — Character · Scene · Moderation · System — with a
// wider gap between groups and Disconnect set apart at the end). Like the legacy
// variant it threads the wrapping row cursor y2 by value and returns done=true when
// a Disconnect click already fired requestDisconnect (moved verbatim).
func (a *App) drawICUtilityRowGrouped(clusterX, y2, clusterRight, w, h int32) (int32, bool) {
	c := a.ctx
	x := clusterX
	// New default: the same buttons grouped by purpose (Character · Scene · Moderation · System),
	// a wider gap between groups, the leave button set apart at the end. Inline (no closures) so
	// the row stays alloc-free.
	const gGap = int32(16) // gap between groups
	evLabel := "Evidence"
	if a.evidPresent {
		evLabel = "Evidence ●"
	}
	// — Character — (each button hideable via UI… → Buttons; hidden ones compact away)
	if r, ok := a.ctrlSlot(&x, y2, 100, 106, w, h, "ctrl.character"); ok {
		if c.Button(r, "Character") {
			a.screen = ScreenCharSelect
		}
	}
	if r, ok := a.ctrlSlot(&x, y2, 90, 96, w, h, "ctrl.wardrobe"); ok {
		if c.Button(r, "Wardrobe") {
			a.openIniswap()
		}
		c.Border(r, ColAccent)
		c.Tooltip(r, "Wardrobe / iniswap — swap your character's sprites & emotes")
	}
	if r, ok := a.ctrlSlot(&x, y2, 84, 84+gGap, w, h, "ctrl.restyle"); ok {
		if c.Button(r, "Restyle") {
			a.openSpriteStyle()
		}
		c.Tooltip(r, "Recolour / glow your character on the fly — other AsyncAO players see it")
	}
	// — Scene —
	if r, ok := a.ctrlSlot(&x, y2, 100, 106, w, h, "ctrl.background"); ok {
		if c.Button(r, "Background") {
			a.openBgPicker()
		}
	}
	if r, ok := a.ctrlSlot(&x, y2, 100, 106, w, h, "ctrl.evidence"); ok {
		if c.Button(r, evLabel) {
			a.showEvid = true
		}
	}
	x = a.drawPosSelect(x, y2, btnH)
	x += gGap
	// — Moderation —
	if r, ok := a.ctrlSlot(&x, y2, 80, 86, w, h, "ctrl.mods"); ok {
		if c.Button(r, "Mods...") {
			a.showModcall = true
		}
	}
	if a.amIMod() {
		mR := a.slotRect("ctrl.mod", sdl.Rect{X: x, Y: y2, W: 54, H: btnH}, w, h)
		if c.Button(mR, "Mod") {
			a.toggleModDash()
		}
		c.Border(mR, ColDanger)
		c.Tooltip(mR, "Moderation tools — server-aware ban / kick")
		x += 60
	}
	if a.amICMNow {
		cR := a.slotRect("ctrl.cm", sdl.Rect{X: x, Y: y2, W: 46, H: btnH}, w, h)
		if c.Button(cR, "CM") {
			a.toggleCMPanel()
		}
		c.Border(cR, chipCMColor)
		c.Tooltip(cR, "CM area controls — lock / kick-from-area")
		x += 52
	}
	x += gGap
	// — System — wrap to a fresh row as a block if it would run off the right edge.
	if x+90+56+100+96+86+76 > clusterRight {
		y2 += btnH + 4
		x = clusterX
	}
	if r, ok := a.ctrlSlot(&x, y2, 90, 96, w, h, "ctrl.settings"); ok {
		if c.Button(r, "Settings") {
			a.prevScreen = ScreenCourtroom
			a.screen = ScreenSettings
		}
	}
	// UI… is the access point to the button-customise list, so it's never hidden.
	if a.movableButton("ctrl.ui", sdl.Rect{X: x, Y: y2, W: 50, H: btnH}, "UI...", w, h) {
		a.showUICfg = true
	}
	x += 56
	if r, ok := a.ctrlSlot(&x, y2, 94, 100, w, h, "ctrl.editlayout"); ok {
		if c.Button(r, "Edit Layout") {
			a.openLayoutEditor()
		}
		c.Tooltip(r, "Live layout editor — drag & resize every box: the stage, log & OOC. Works on any theme; saved across sessions.")
	}
	if r, ok := a.ctrlSlot(&x, y2, 90, 96, w, h, "ctrl.hotkeys"); ok {
		if c.Button(r, "Hotkeys") {
			a.openHotkeyCheatSheet()
		}
		c.Tooltip(r, "Show all your hotkeys & custom binds (also F1)")
	}
	if r, ok := a.ctrlSlot(&x, y2, 80, 86, w, h, "ctrl.about"); ok {
		if c.Button(r, "About") {
			a.prevScreen = ScreenCourtroom
			a.screen = ScreenAbout
		}
	}
	// Login / account button: once you've saved an account for this server it shows
	// your username (left-click views your profile via /account); otherwise it's the
	// plain "Login..." that opens the dialog. Right-click always opens the dialog (log
	// in / switch / re-login). Account name is per-server (a.serverKey), so it's
	// correct across multi-server tabs.
	acct := strings.TrimSpace(a.d.Prefs.ServerWarmInfoFor(a.serverKey).LoginUser)
	loginLabel, loginW := "Login...", int32(70)
	if acct != "" {
		loginLabel, loginW = acct, c.TextWidth(acct)+18
	}
	if loginR, ok := a.ctrlSlot(&x, y2, loginW, loginW+6, w, h, "ctrl.login"); ok {
		if c.Button(loginR, loginLabel) {
			if acct != "" {
				a.sess.SendOOC(a.oocNameOrDefault(), "/account") // view your profile
			} else {
				a.openLoginDialog()
			}
		}
		if acct != "" {
			if c.rightClicked && c.hovering(loginR) {
				a.openLoginDialog() // re-login / switch account
			} else if c.hovering(loginR) { // build the hint string only on hover (0-alloc otherwise)
				c.Tooltip(loginR, "Logged in as "+acct+" — click: view profile (/account) · right-click: log in / switch")
			}
		}
	}
	// Group Chat / DMs launcher — a MAIN button (default ON; Settings → Chat to hide).
	// APPENDED at the end of the functional buttons so it can never shift the ones
	// before it; when off it's skipped entirely (no x advance), so the row is
	// byte-identical to before. A movable slot, so Edit Layout can reposition it.
	if a.d.Prefs.GroupChatButtonOn() && !a.panelHidden("ctrl.groupchat") {
		const gcW = int32(96)
		if x+gcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		gcR := a.slotRect("ctrl.groupchat", sdl.Rect{X: x, Y: y2, W: gcW, H: btnH}, w, h)
		if c.Button(gcR, "Group Chat") {
			a.toggleMessages()
		}
		c.Border(gcR, ColAccent)
		c.Tooltip(gcR, "Private DMs & group chats with other AsyncAO players (also Extras → Group Chat)")
		x += gcW + 6
	}
	// Voice button — appears ONLY in an area that advertises voice (VS_CAPS, i.e.
	// Nyathena), so it shows up when you move into a VC-supported room and hides
	// elsewhere. Appended like the others; skipped (no x advance) when unavailable.
	if a.voiceOfferable() && !a.panelHidden("ctrl.voice") {
		const vcW = int32(80)
		if x+vcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		vcLabel, vcBord := a.voiceButtonState()
		vcR := a.slotRect("ctrl.voice", sdl.Rect{X: x, Y: y2, W: vcW, H: btnH}, w, h)
		if c.Button(vcR, vcLabel) {
			a.toggleVoice()
		}
		c.Border(vcR, vcBord)
		c.Tooltip(vcR, "Voice chat — this area supports it. Join to talk (Nyathena). Red dot = your mic is live.")
		x += vcW + 6
	}
	// — Leave — set apart at the end.
	x += gGap
	if x+110 > clusterRight {
		y2 += btnH + 4
		x = clusterX
	}
	if !a.panelHidden("ctrl.disconnect") { // hideable (v1.50.5): Esc still leaves the server
		if a.movableButton("ctrl.disconnect", sdl.Rect{X: x, Y: y2, W: 110, H: btnH}, "Disconnect", w, h) {
			a.requestDisconnect()
			return y2, true
		}
		x += 116
	}
	return y2, false
}

// drawICColorStrip draws the IC text-colour selector (the swatch previewing the
// active wire colour + the naming dropdown) inside its resolved slot rect. colorBox
// arrives by value; icColorSelected drives the active row + swatch and
// applyICColorPick routes the pick — both shared with the themed row so the two
// layouts can't drift (moved verbatim from drawICControls).
func (a *App) drawICColorStrip(colorBox sdl.Rect) {
	c := a.ctx
	// The pieces fill the SLOT rect (v1.50.5): a resize genuinely widens the
	// dropdown / grows the row height instead of being silently ignored.
	swatch := sdl.Rect{X: colorBox.X, Y: colorBox.Y, W: 26, H: colorBox.H}
	icSel, sw := a.icColorSelected()
	c.Fill(swatch, sw)
	c.Border(swatch, ColPanelHi)
	a.icSwatchRect = swatch // the free-hex wheel anchors here (v1.52.0)
	if a.icCustomOn && c.clicked && c.hovering(swatch) {
		a.showICColorWheel = !a.showICColorWheel // re-open to adjust (re-picking "Custom…" in the dropdown doesn't fire changed)
	}
	colorDDW := colorBox.W - 32
	if colorDDW < 60 {
		colorDDW = 60 // floor: the dropdown stays clickable however small the slot is dragged
	}
	// Freeze the IC field's selection BEFORE the dropdown so a picked colour can
	// wrap it (§3.8 select-and-colour, folded into the dropdown to match AO2's own
	// on_text_color_changed). The open-click unfocuses the field later this frame
	// (the strip draws before the field), so by the pick frame the live selection
	// is gone — captureICColorSel snapshots it while it still exists.
	a.captureICColorSel()
	if next, changed := c.Dropdown(icColorDDID, sdl.Rect{X: colorBox.X + 32, Y: colorBox.Y, W: colorDDW, H: colorBox.H}, icColorChoices, icSel); changed {
		a.applyICColorPick(next)
	}
}

// drawICInputRow draws the IC bar's input chain — showname box (+ saved-name picker),
// the Immediate and Additive toggles, the SFX picker, the emoji and Text-FX buttons,
// the IC text field itself and the muted chip — flowing left-to-right from the DEFAULT
// positions so freeing one slot in the editor never cascades the rest. icBar arrives
// by value; it returns whether Enter was pressed so the caller keeps the send decision
// visible (moved verbatim from drawICControls).
func (a *App) drawICInputRow(icBar sdl.Rect, rowY, w, h, fH int32) (send bool) {
	c := a.ctx
	const shownameBoxW = 140
	nameX := icBar.X + 32 + colorSelectW + 6 // DEFAULT spot; downstream (immedX) flows from here
	namePlaceholder := a.d.Prefs.SavedShowname()
	if namePlaceholder == "" {
		namePlaceholder = "Showname"
	}
	a.ensureNameOpts()
	// Showname box (+ saved-name picker) — an individually movable slot (#4a). Draws at
	// the slot; the row cursor below flows from the DEFAULT nameX so moving it never
	// cascades the rest.
	nameBox := a.slotRect(slotICShowname, sdl.Rect{X: nameX, Y: rowY, W: shownameBoxW, H: fH}, w, h)
	// v1.50.5 fix ("the showname box is not resizable — it has the ui for it but
	// it does nothing"): the field fills the SLOT'S live width/height, not the
	// fixed default, so the editor's resize handles actually take effect.
	snW, snDD := nameBox.W, int32(0)
	if len(a.nameOpts) > 1 { // a tiny ▾ saved-name picker, fitted INSIDE the box width so nothing downstream shifts
		snDD = 22
		snW -= snDD + 2
	}
	if snW < 40 {
		snW = 40 // floor: never collapse the field into an unclickable sliver
	}
	a.shownameOverride, _ = c.TextField("icshownameov", sdl.Rect{X: nameBox.X, Y: nameBox.Y, W: snW, H: nameBox.H}, a.shownameOverride, namePlaceholder)
	if name := a.pickNameDropdown("snpick", sdl.Rect{X: nameBox.X + snW + 2, Y: nameBox.Y, W: snDD, H: nameBox.H}); name != "" {
		a.shownameOverride = name
	}
	// Immediate (AO non-interrupting preanim): the preanim plays without
	// holding back the text. Session toggle; rides the next message via
	// OutgoingMS.Immediate. Vertically centered against the fH-tall inputs.
	const immedW = 96 // fits the full "Immediate" label without truncating to "Immed"
	immedX := nameX + shownameBoxW + 6
	// Slice 1 of the movable IC bar: the Immediate toggle can be pulled out into its
	// own spot in the classic editor. Its default rides the bar (un-edited / whole-bar
	// move stays pixel-identical), and everything downstream keeps flowing from the
	// DEFAULT immedX below — never the override — so freeing Immediate doesn't cascade
	// the rest of the row. slotRect is alloc-free off the edit path.
	immedDef := sdl.Rect{X: immedX, Y: rowY, W: immedW, H: fH}
	immedBox := a.slotRect(slotICImmediate, immedDef, w, h)
	a.icImmediate = c.Checkbox(immedBox.X, immedBox.Y+(immedBox.H-16)/2, "Immediate", a.icImmediate)
	c.Tooltip(immedBox, "Immediate: the preanim plays without holding back the text (non-interrupting preanim)")
	icX := immedX + immedW + 6 // downstream flows from the DEFAULT position, not the override
	// Additive (#14, 2.8): the next message APPENDS to your last one (narration RP).
	// Shown only when the server advertises additive AND the master pref is on (AO2
	// shows ui_additive whenever the server advertises it, courtroom.cpp:1638-1644).
	// When hidden the toggle is forced off so a stale check can't ride a message.
	if a.sess != nil && a.sess.Features.Has(protocol.FeatureAdditive) && a.d.Prefs.AdditiveTextOn() {
		const addW = 84 // fits "Additive"
		// Movable slot (#4a), same wrap-not-extract rule as Immediate above: draws through
		// slotRect so the editor can pull it out into its own spot, but the row cursor still
		// advances by the DEFAULT width below — so an un-edited bar is pixel-identical and
		// moving Additive never cascades the rest. slotRect is alloc-free off the edit path.
		addDef := sdl.Rect{X: icX, Y: rowY, W: addW, H: fH}
		addBox := a.slotRect(slotICAdditive, addDef, w, h)
		a.icAdditive = c.Checkbox(addBox.X, addBox.Y+(addBox.H-16)/2, "Additive", a.icAdditive)
		c.Tooltip(addBox, "Additive: this message adds to your last one instead of replacing it (2.8 narration-style RP).")
		icX += addW + 6 // downstream flows from the DEFAULT position, not the override
	} else {
		a.icAdditive = false
	}
	icCounterOn := a.d.Prefs.MessageCounterOn()
	// The IC input must always keep at least minICInputW (plus the counter's reserve when it's
	// on). Each IC-bar button is placed ONLY if it still leaves that room, so a narrow bar
	// drops them right-to-left — React, then FX, then emoji — instead of pushing the text input
	// off the edge. Regression guard: adding the React button collapsed the input to zero width
	// for users with a narrower stage ("the IC bar disappeared"). Widths key off the slot
	// (icBar.W), so the same guard holds after the bar is moved/resized. Inlined (no closure) to
	// keep this per-frame row allocation-free.
	const minICInputW = 150
	tailReserve := int32(minICInputW)
	if icCounterOn {
		tailReserve += msgCounterReserve
	}
	// SFX picker (AO2-style): pick a sound to ride your NEXT message — overrides the
	// emote's own sound until set back to "auto". Picking one previews it. Placed first
	// so on a narrow bar it survives longest (the buttons drop right-to-left).
	const sfxDDW = 92
	a.ensureSFXChoices()
	if icBar.W-(icX-icBar.X)-(sfxDDW+4) >= tailReserve {
		sfxRect := a.slotRect(slotICSFX, sdl.Rect{X: icX, Y: rowY, W: sfxDDW, H: fH}, w, h) // movable (#4a)
		if next, changed := c.Dropdown("sfxdd", sfxRect, a.sfxChoices, a.sfxChoiceIdx); changed {
			a.sfxChoiceIdx = next
			if next > 0 && next < len(a.sfxChoices) {
				a.d.Audio.PlaySFX(a.urls.SFX(a.sfxChoices[next]), 0) // preview the picked sound
			}
		}
		c.TooltipAfter("sfxdd-tip", sfxRect, "Sound for your NEXT message — 'auto' uses the emote's own sound, or pick one to override. Picking previews it. Extras → SFX Browser for favourites & any sound by name.")
		icX += sfxDDW + 4
	}
	// #M2 S1: emoji picker button on the IC bar's left edge — movable (#4a) and
	// hideable (playtest: some players don't want it at all).
	if icBar.W-(icX-icBar.X)-(fH+4) >= tailReserve && !a.panelHidden(slotICEmoji) {
		if a.drawEmojiBarButton(a.slotRect(slotICEmoji, sdl.Rect{X: icX, Y: rowY, W: fH, H: fH}, w, h)) {
			a.showEmojiPicker = !a.showEmojiPicker
		}
		icX += fH + 4
	}
	// #M5: dedicated Text FX cycle button (Off → Shake → Wave → Rainbow) — movable (#4a).
	if icBar.W-(icX-icBar.X)-(fxBtnW+4) >= tailReserve {
		a.fxButton(a.slotRect(slotICFx, sdl.Rect{X: icX, Y: rowY, W: fxBtnW, H: fH}, w, h))
		icX += fxBtnW + 4
	}
	// The #2 React BUTTON was removed by request (playtest: unused) — incoming
	// reaction floats still render (and Hide-reactions still hides them), the
	// IC bar just doesn't spend a slot on sending one.
	icW := icBar.W - (icX - icBar.X)
	if icCounterOn {
		icW -= msgCounterReserve
	}
	if icW < minICInputW {
		icW = minICInputW // defensive floor: never collapse the input, even on a tiny stage
	}
	// The IC text input itself is a movable + resizable slot (#4a) — default fills the rest
	// of the bar; an override repositions/resizes it and the message counter rides along.
	icBox := a.slotRect(slotICInput, sdl.Rect{X: icX, Y: rowY, W: icW, H: fH}, w, h)
	icPrimary, icEmoji := a.icFieldFonts(a.icInput) // #M5: show typed emoji/unicode, not tofu
	a.icInput, send = c.TextFieldEmoji(icFieldID, icBox, a.icInput, "Talk in-character here…  (/pair <id>, /unpair, /offset <x> [y], /pos <side>)", icPrimary, icEmoji)
	a.recallIC() // #8: Up/Down recall recently-sent lines when the IC field is focused
	a.drawMsgCounter(icBox, icCounterOn)
	// Muted (#13): the server disabled our IC speech (MU). Draw a persistent chip
	// over the input's right edge so a muted player sees WHY their sends do
	// nothing; sendIC refuses the send and keeps the typed line intact.
	if a.sess != nil && a.sess.Muted {
		const mutedChip = "🔇 muted"
		chipW := c.TextWidth(mutedChip) + 12
		if chipW < icBox.W { // only when it fits inside the field
			chip := sdl.Rect{X: icBox.X + icBox.W - chipW - 2, Y: icBox.Y + 2, W: chipW, H: icBox.H - 4}
			c.Fill(chip, sdl.Color{R: 120, G: 30, B: 30, A: 210})
			c.Label(chip.X+6, chip.Y+(chip.H-14)/2, mutedChip, ColDanger)
		}
	}
	return send
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
	a.d.Manager.PrefetchChain(a.previewBase, a.urls.EmoteAlts(char, e.Anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
}

func (a *App) drawEmoteRow(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	// Per-part tint (v1.52.0): the grid has NO backing panel by default, so a
	// set colour ADDS one — un-set stays exactly as before (no fill at all).
	if col, ok := a.partPanel(partEmotes); ok {
		c.Fill(r, col)
	}
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
		if c.rightClicked && c.hovering(btn) {
			a.previewPinned = true // right-click pins the preview open until you close it
		}
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
	// empty favs-only grid) + Random. Their DEFAULT is the grid's bottom-right
	// corner, but each is its own movable slot now (v1.50.5 — "the random char
	// button is always stuck in the emote grid corner") and hideable like any
	// control button.
	if !a.panelHidden("ctrl.favsfilter") {
		a.drawEmoteFavToggle(a.slotRect("ctrl.favsfilter", sdl.Rect{X: r.X + r.W - 158, Y: r.Y + r.H - btnH, W: 64, H: btnH}, a.winW, a.winH))
	}
	// Swap to a random available character — replaced the old "Random" emote
	// button (redundant with per-send auto-random + the wheel cycling emotes).
	if !a.panelHidden("ctrl.randchar") {
		rcb := a.slotRect("ctrl.randchar", sdl.Rect{X: r.X + r.W - 92, Y: r.Y + r.H - btnH, W: 92, H: btnH}, a.winW, a.winH)
		if c.Button(rcb, "Rand char") {
			a.randomChar()
		}
	}

	if a.previewBase != "" {
		// Clamp to the WINDOW, not the stage — the box is draggable anywhere
		// (playtest: "you can't move the preview out of the viewport, wth").
		a.drawSpritePreview(a.winW, a.winH, false)
		a.closeSpritePreviewOnLeave()
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
		// No emotions/button<N> art (404 / still streaming): fall back to the character
		// ICON — a face beats a bare grey box (players disliked the grey). The emote label
		// rides a strip at the bottom so emotes stay distinguishable even when every cell
		// shows the same icon. Grey only remains until even the icon streams in. One small
		// icon per character (NOT the full sprite), so no streaming storm / budget hit.
		iconBase := a.urls.CharIcon(me)
		if ip, iok := a.cachedPage(&a.emoteIconPages, &a.emoteIconGen, 1, 0, iconBase); iok && len(ip.Frames) > 0 {
			_ = c.Ren.Copy(ip.Frames[0], nil, &btn)
		} else {
			a.demandAsset(&a.emoteIconAsk, 1, 0, iconBase, assets.AssetTypeCharIcon) // AssetType: CharIcon
			c.Fill(btn, ColPanel)
			c.Border(btn, ColPanelHi)
			a.frameDemandPending = true // blank cell (no art, no icon yet): keep the demand pump alive at idle
		}
		// Emote-name caption: opt-in (default OFF). Off ⇒ the fallback shows a clean
		// icon with no text overlay, which is what most players want; on ⇒ the name
		// strip returns for telling otherwise-identical fallback cells apart.
		if a.d.Prefs.EmoteCaptionsOn() {
			strip := sdl.Rect{X: btn.X, Y: btn.Y + btn.H - 15, W: btn.W, H: 15}
			c.Fill(strip, sdl.Color{R: 0, G: 0, B: 0, A: 150})
			c.LabelClipped(btn.X+3, strip.Y+1, btn.W-6, label, ColText)
		}
	}
	return c.hovering(btn) && c.clicked
}

// drawPairPanel: partner picking is a searchable click-to-pick list (the
// old one-by-one </> cycle was unusable on 4000-char servers); offsets,
// flip and z-order live in the right column.
func (a *App) drawPairPanel(w, h int32, pressed *bool) {
	c := a.ctx
	wasActive := a.pairWin.dragging || a.pairWin.resizing // detect the drag/resize-end frame for slot persistence
	r := a.pairPanelRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	// Title bar / drag handle + close + bottom-right resize grip — a non-blocking
	// floating box (drawn in the box pass), so the courtroom behind stays live:
	// you can keep chatting with the pair menu open, and drag/resize it freely.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	c.Heading(r.X+pad, r.Y+6, "Pairing", ColText)
	if c.Button(sdl.Rect{X: r.X + r.W - 84 - pad, Y: r.Y + 3, W: 84, H: btnH}, "Close") {
		a.showPair = false
		return
	}
	a.floatWinDrag(&a.pairWin, sdl.Rect{X: r.X, Y: r.Y, W: r.W - 94 - pad, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.pairWin, grip, r, pairPanelMinW, pairPanelMinH, pressed)
	a.drawResizeGrip(grip)
	if wasActive && !a.pairWin.dragging && !a.pairWin.resizing { // drag/resize just ended → remember where
		a.persistPanelSlot(slotPanelPair, r, w, h)
	}

	contentTop := r.Y + floatTitleH + 10

	// Left: searchable partner list.
	listW := r.W/2 - pad*2
	y := contentTop
	c.LabelClipped(r.X+pad, y, listW, "Partner: "+a.pairLabel(), ColAccent)
	y += 24
	a.pairSearch, _ = c.TextField("pairsearch", sdl.Rect{X: r.X + pad, Y: y, W: listW - 80, H: fieldH}, a.pairSearch, "Search...")
	if c.Button(sdl.Rect{X: r.X + pad + listW - 74, Y: y, W: 74, H: btnH}, "Unpair") {
		a.pairWith = protocol.UnpairedCharID
	}
	y += fieldH + 8

	a.ensureCharLower()
	query := a.pairQ.get(a.pairSearch)
	lineH := int32(22)
	listTop := y
	listH := r.Y + r.H - listTop - pad
	matches := int32(0)
	for i := range a.sess.Chars {
		if i != a.sess.MyCharID && (query == "" || strings.Contains(a.charLower[i], query)) {
			matches++
		}
	}
	if c.hovering(sdl.Rect{X: r.X + pad, Y: listTop, W: listW, H: listH}) {
		a.pairScroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: r.X + pad + listW - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
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
			row := sdl.Rect{X: r.X + pad, Y: rowY, W: listW - scrollBarW - scrollBarGap, H: lineH - 2}
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
	rx := r.X + r.W/2 + pad
	ry := contentTop
	a.pairOffX = a.offsetControl("pairoffx", rx, ry, "Offset X %", a.pairOffX, &a.pairOffXText)
	ry += 34
	a.pairOffY = a.offsetControl("pairoffy", rx, ry, "Offset Y %", a.pairOffY, &a.pairOffYText)
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
	pv := sdl.Rect{X: rx, Y: ry, W: r.W/2 - 2*pad, H: r.Y + r.H - pad - ry}
	if pv.H >= ghostMinHeightPx {
		a.drawPairGhost(pv)
	}
}

// pairPanelRect is the Pair menu's floating-window rect (floatwin.go): movable +
// resizable, clamped on-screen, with a window-height-responsive default size.
func (a *App) pairPanelRect(w, h int32) sdl.Rect {
	defH := clampI32(h-120, pairPanelMinH, 560)
	if r, ok := a.seedPanelFromSlot(&a.pairWin, slotPanelPair, pairPanelDefW, defH, pairPanelMinW, pairPanelMinH, w, h); ok {
		return r
	}
	return a.pairWin.rect(pairPanelDefW, defH, pairPanelMinW, pairPanelMinH, w, h)
}

const (
	pairPanelDefW = 580 // default width
	pairPanelMinW = 440 // floor: the two columns need room
	pairPanelMinH = 320 // floor: list + controls
)

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
	// Real background behind the ghosts, so you place your sprite against the
	// actual stage instead of a black void ("otherwise what's the point"). Uses
	// YOUR position's bg; a flat fill stands in until it streams in.
	bgPart, _ := courtroom.PositionScene(a.mySide())
	base := a.urls.Background(a.sess.Background, bgPart)
	if base != a.pairBgKey {
		a.pairBgPages, a.pairBgKey = nil, base
	}
	if page, ok := a.cachedPage(&a.pairBgPages, &a.pairBgGen, 1, 0, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &pv)
	} else {
		c.Fill(pv, sdl.Color{R: 12, G: 12, B: 16, A: 255})
		if a.sess.Background != "" {
			a.d.Manager.Prefetch(base, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background (pair preview)
		}
	}
	c.Border(pv, ColPanelHi)

	// Partner ghost first (behind), then me.
	if a.pairWith >= 0 && a.pairWith < len(a.sess.Chars) {
		name := a.sess.Chars[a.pairWith].Name
		// The scene's pair layer knows their REAL offsets — and their real idle
		// sprite — once a paired message arrived; before that they stand
		// centered on the "normal" guess.
		gx, gy := 0, 0
		base := a.urls.Emote(name, ghostFallbackEmote, courtroom.EmoteIdle)
		alts := a.urls.EmoteAlts(name, ghostFallbackEmote, courtroom.EmoteIdle)
		if sc := &a.room.Scene; sc.PairActive && strings.EqualFold(sc.Pair.Name, name) {
			gx, gy = sc.Pair.OffsetX, sc.Pair.OffsetY
			if sc.Pair.IdleBase != "" {
				base, alts = sc.Pair.IdleBase, nil // the exact sprite on stage — already resolved
			}
		}
		a.drawGhostSprite(pv, name, base, alts, gx, gy, false, ghostAlpha)
	}
	if me := a.activeCharName(); me != "" { // iniswap-aware: preview the folder that actually renders
		// YOUR ghost is the SELECTED emote's idle — the sprite the next message
		// really shows. The old hardcoded "normal" drew nothing for the many
		// packs without a normal.* sprite (playtest: "the sprite doesn't
		// display at all") while the live viewport worked fine.
		anim := ghostFallbackEmote
		if a.emoteIdx >= 0 && a.emoteIdx < len(a.emotes) {
			anim = a.emotes[a.emoteIdx].Anim
		}
		base := a.urls.Emote(me, anim, courtroom.EmoteIdle)
		alts := a.urls.EmoteAlts(me, anim, courtroom.EmoteIdle)
		a.drawGhostSprite(pv, me, base, alts, a.pairOffX, a.pairOffY, a.pairFlip, 255)
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
		a.refreshPairOffsetBufs() // a focused numeric row must follow the drag live
	}
	// Arrow keys nudge your offset 1% for fine placement — only with no text
	// field focused (otherwise arrows move the text caret). Up = toward the
	// top of the stage (lower Y), matching the drag.
	if c.focusID == "" {
		switch c.keyPressed {
		case sdl.K_LEFT:
			a.pairOffX = clampOffset(a.pairOffX - 1)
		case sdl.K_RIGHT:
			a.pairOffX = clampOffset(a.pairOffX + 1)
		case sdl.K_UP:
			a.pairOffY = clampOffset(a.pairOffY - 1)
		case sdl.K_DOWN:
			a.pairOffY = clampOffset(a.pairOffY + 1)
		}
	}
}

// ghostFallbackEmote is the idle guessed for a character whose real sprite
// isn't known yet (no emote list / no paired message) — AO's conventional
// default idle.
const ghostFallbackEmote = "normal"

// drawGhostSprite draws one character's sprite (base + its spelling alts)
// at offset% of the stage, sized like the real viewport sizes sprites
// (full stage height). The texture's alpha-mod restores immediately —
// pages are shared with the live viewport.
func (a *App) drawGhostSprite(pv sdl.Rect, name, base string, alts []string, offX, offY int, flip bool, alpha uint8) {
	c := a.ctx
	page, ok := a.d.Store.Get(base)
	if !ok || len(page.Frames) == 0 || page.H == 0 {
		// Warm once per (character, base) — not per frame. Re-warms when the
		// base CHANGES (emote pick, pair message) even with the table full:
		// only brand-new characters count against the cap.
		if a.ghostWarm[name] != base {
			if a.ghostWarm == nil {
				a.ghostWarm = map[string]string{}
			}
			_, known := a.ghostWarm[name]
			if known || len(a.ghostWarm) < ghostWarmCap {
				a.ghostWarm[name] = base
				a.d.Manager.PrefetchChain(base, alts, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (ghost editor)
			}
		}
		c.Label(pv.X+pv.W/2-c.TextWidth(name)/2, pv.Y+pv.H/2, name, ColTextDim)
		return
	}
	dst := sdl.Rect{H: pv.H, W: pv.H * page.W / page.H}
	dst.X = pv.X + (pv.W-dst.W)/2 + int32(offX)*pv.W/100
	dst.Y = pv.Y + int32(offY)*pv.H/100
	if len(page.Frames) > 1 {
		a.NoteAnimating() // the editor ghost loops: keep frames coming through the static skip
	}
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
	typed := val // the value as of the typed commit — any change below is a mouse edit
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
	// A mouse edit (−/+ button, wheel) while the field is FOCUSED: the field
	// displays the edit buffer, not val, so refresh it — wheeling never blurs,
	// and the row froze at its old number until a click-off (playtest: "they
	// react to the buttons and the wheel but stay at 0 until I click off").
	// Typing keeps its partial-input buffer: this only fires on mouse edits.
	if val != typed && c.focusID == id {
		*buf = strconv.Itoa(val) // textField clamps the caret to the new length
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

// refreshPairOffsetBufs re-mirrors a FOCUSED offset field's edit buffer after
// the value changed somewhere else (stage drag, arrow nudge) — while focused
// the field displays the buffer, so without this the number froze until blur.
func (a *App) refreshPairOffsetBufs() {
	switch a.ctx.focusID {
	case "pairoffx":
		a.pairOffXText = strconv.Itoa(a.pairOffX)
	case "pairoffy":
		a.pairOffYText = strconv.Itoa(a.pairOffY)
	}
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
// palette cycle), else random swaps the message's palette colour. customRGB ≥ 0
// is the free hex pick (v1.52.0, Tifera): the exact colour rides as `\c#RRGGBB`
// markup for AsyncAO clients, the wire text_color falls back to the nearest
// standard index. Pure (the random index is supplied) so the rule is testable;
// blank/space sends are left alone, and rainbow wins if several are set.
func funColor(text string, color, ext, customRGB int, rainbow, random bool, randIdx int) (string, int) {
	if text == "" || text == " " {
		return text, color
	}
	switch {
	case rainbow:
		return "\\cr" + text, color
	case random:
		return text, randIdx
	case customRGB >= 0:
		return fmt.Sprintf("\\c#%06x%s", customRGB&0xFFFFFF, text),
			render.NearestTextColorIndex(uint8(customRGB>>16), uint8(customRGB>>8), uint8(customRGB))
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
		a.icInput = "" // commands clear instantly — the field's undo history catches it (Ctrl+Z)
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
	// Muted (MU): the server disabled our IC speech (AO2 set_mute disables
	// ui_ic_chat_message; on_chat_return_pressed returns early when is_muted). Refuse
	// the send but KEEP the typed line — keep-until-echo never clears it (there's no
	// echo for a swallowed send), so the text stays in the field for when we're
	// unmuted. Surface it so a muted player isn't left typing into a void.
	if a.sess.Muted {
		a.pushOOC("[SERVER] You're muted — that message wasn't sent.", "")
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
	if a.sfxChoiceIdx > 0 && a.sfxChoiceIdx < len(a.sfxChoices) {
		sfxName = a.sfxChoices[a.sfxChoiceIdx] // IC-bar SFX picker override (until set back to auto)
	}
	if sfxName == "" {
		sfxName = "1"
	}
	// Per-emote blip override (2.9.1 custom_blips), else the character's.
	blip := emote.Blip
	if blip == "" {
		blip = a.charBlips
	}
	// #M5 animated text: the sticky FX button wraps the whole message first (no-op when off
	// / blankpost / you already typed inline markup), then ParseTextEffects pulls all the
	// [shake]/[wave]/[rainbow] markup out into the wire text (tags removed, \cN colour kept)
	// plus the display-indexed effect spans. The spans ride an invisible frame appended
	// below, so AO2/webAO render the plain message; the spans align with the receiver's
	// MessageText because the two strips commute. 0-alloc when the input has no '['. A
	// markup-only message (no visible text left) falls back to a blankpost.
	text = a.applyStickyEffect(text)
	text, effectSpans := courtroom.ParseTextEffects(text)
	if strings.TrimSpace(text) == "" && shout == 0 {
		text, effectSpans = " ", nil
	}
	// #M1 auto-status: a trigger word you typed (e.g. "brb") flips your status on the raw
	// text, BEFORE markup/markers, so the status change rides this very message.
	a.applyAutoStatus(text)
	// M61 fun colour: rainbow (\cr prefix), an extended AsyncAO colour (\c<letter>
	// + nearest-standard wire fallback, #98), or a random palette colour per message.
	customRGB := -1
	if a.icCustomOn {
		customRGB = a.icCustomRGB
	}
	text, msgColor := funColor(text, a.icColor, a.icExtColor-1, customRGB, a.d.Prefs.RainbowMessagesOn(), a.d.Prefs.RandomMessageColorOn(), rand.IntN(render.TextColorCount))
	// Transmitted sprite style (#103): append the invisible zero-width marker at
	// the END of the text — other AsyncAO clients decode + render it on this
	// character; AO2/webAO see nothing. End placement keeps the visible text intact
	// if a server length-limits the message (worst case: the style is dropped).
	// Send-on-CHANGE: the marker rides only the messages where YOUR style changed
	// (and a clear when you turn it off), not every line — other clients typewriter
	// the invisible run and blip on each character, so a marker on every message was
	// audible spam. lastSentStyle tracks what we last transmitted this session.
	curStyle := a.mySpriteStyle()
	if marker := curStyle.EncodeChangeMarker(a.lastSentStyle); marker != "" {
		text += marker
	}
	a.lastSentStyle = curStyle
	// Transmitted character profile (#101 slice 2): the same invisible channel, also
	// send-on-CHANGE, appended AFTER the style marker (an older AsyncAO client reads only
	// the first zero-width run, so the style must stay first). Only pronouns + tagline
	// ride; the receiver shows them on this character's player-list card.
	curProfile := a.myWireProfile()
	if pm := curProfile.EncodeChangeMarker(a.lastSentProfile); pm != "" {
		text += pm
	}
	a.lastSentProfile = curProfile
	// Cross-client presence status (#M1): same invisible channel, send-on-change.
	if sm := courtroom.EncodeStatusChangeMarker(a.myStatus, a.lastSentStatus); sm != "" {
		text += sm
	}
	a.lastSentStatus = a.myStatus
	// Animated-text spans (#M5): same invisible channel, appended LAST. Unlike the
	// send-on-change markers above, the effects frame is per-message CONTENT, so it rides
	// every message that carries [shake]/[wave]/[rainbow] markup.
	if marker := courtroom.EncodeEffectsMarker(effectSpans); marker != "" {
		text += marker
	}
	// #2 reactions: a queued reaction (you clicked React + picked an emoji) rides THIS
	// message as an invisible frame referencing the message you reacted to, then clears —
	// per-message content like the effects frame. AsyncAO viewers float the emoji; AO2/webAO
	// see clean text. When your own message echoes back you'll see it float too (matched
	// against your log), so there's no separate local echo.
	if a.pendingReactSet {
		if marker := a.pendingReact.EncodeMarker(); marker != "" {
			text += marker
		}
		a.pendingReactSet = false
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
		// #17 networked frame-synced effects: the char.ini [<emote>_Frame*]
		// sections, pre-assembled at parse into AO2's wire format. Empty for the
		// (vast majority of) emotes with no frame data — KFOCompat still fills the
		// template for KFO-Server. An IC-bar SFX-picker override doesn't rewrite the
		// per-frame SFX map (it targets the single main SFX), matching AO2.
		FrameShake:   emote.FrameShake,
		FrameRealize: emote.FrameRealize,
		FrameSFX:     emote.FrameSFX,
		Blipname:     blip,
		Side:         a.mySide(),
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
		Immediate: a.icImmediate,      // non-interrupting preanim (IC-row toggle)
		Additive:  a.icAdditive,       // #14 2.8: this message appends to your last (gated to the additive server + pref in the IC row)
		KFOCompat: a.sess.KFOCompat(), // KFO-Server only: fill empty frame/effect fields (its MS validator rejects them)
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
	a.lastICSend = time.Now()
	a.recordSentIC(a.icInput) // #8: remember the raw typed line for Up-arrow recall
	// Keep-until-echo (AO2-Client parity, courtroom.cpp handle_chatmessage):
	// the input is NOT cleared here — noteOwnICEcho clears it when the server
	// echoes our message back. tsuserver-family servers silently swallow an MS
	// that lands inside another message's area-wide delay window
	// (area.set_next_msg_delay), so an optimistic clear cost the whole typed
	// line whenever two people sent at once and the other side won the race.
	// evidPresent stays armed for the same reason (AO2 resets it on the echo):
	// a swallowed send must not disarm the evidence you presented.
	a.icPendingSent = a.icInput
}

// noteOwnICEcho lands the server's echo of OUR OWN IC message (CHAR_ID ==
// MyCharID) — the AO2-Client own-echo block in handle_chatmessage. Clear the
// input only if it still holds exactly what we sent (typing that raced the
// echo survives), and consume the one-shot evidence-present state. A send the
// server swallowed never echoes, so everything stays put for an immediate
// re-Enter.
func (s *sessionState) noteOwnICEcho() {
	if s.icInput == s.icPendingSent {
		// The echo consumes the line. The IC field's undo history records the
		// clear at its next draw (fieldhistory.go's out-of-band detector), so
		// Ctrl+Z still brings the sent line back — with real redo on top.
		s.icInput = ""
	}
	s.icPendingSent = ""
	s.evidPresent = false // presenting is one-shot: consumed by the message that displayed
}

// sentHistCap bounds a per-tab message recall ring (#8: IC, and the OOC ring that mirrors it).
const sentHistCap = 30

// recordSentLine pushes a just-sent line onto a recall ring (newest last), skipping blankposts and
// consecutive duplicates, capping at sentHistCap, and resetting the cursor (*idx) to the live draft.
// Shared by IC and OOC; off the render path (called once per send), so the growth never hits a frame.
func recordSentLine(raw string, hist *[]string, idx *int) {
	*idx = -1 // any send returns you to a fresh draft
	t := strings.TrimSpace(raw)
	if t == "" { // blankpost / whitespace-only — nothing to recall
		return
	}
	if n := len(*hist); n > 0 && (*hist)[n-1] == t {
		return // don't stack the same line twice in a row
	}
	*hist = append(*hist, t)
	if len(*hist) > sentHistCap {
		*hist = (*hist)[len(*hist)-sentHistCap:]
	}
}

// recallLine walks a recall ring (shell-style) when its field is focused and Up/Down is pressed: Up
// goes older, Down newer and back to the stashed live draft. Consumes the key so it can't double-act
// (also dedups when two fields sharing one input both call it in a frame). A few comparisons, no alloc.
func recallLine(c *Ctx, focused bool, input *string, hist []string, idx *int, draft *string) {
	if !focused || len(hist) == 0 {
		return
	}
	switch c.keyPressed {
	case sdl.K_UP:
		if *idx == -1 { // entering history: stash the live draft first
			*draft = *input
			*idx = len(hist)
		}
		if *idx > 0 {
			*idx--
			*input = hist[*idx]
		}
		c.keyPressed = 0
	case sdl.K_DOWN:
		if *idx >= 0 {
			*idx++
			if *idx >= len(hist) {
				*input, *idx = *draft, -1 // back to the draft
			} else {
				*input = hist[*idx]
			}
		}
		c.keyPressed = 0
	}
}

// recordSentIC / recallIC: the IC recall ring (#8).
func (a *App) recordSentIC(raw string) { recordSentLine(raw, &a.icSentHist, &a.icRecallIdx) }
func (a *App) recallIC() {
	recallLine(a.ctx, a.ctx.focusID == "ic", &a.icInput, a.icSentHist, &a.icRecallIdx, &a.icRecallDraft)
}

// recordSentOOC / recallOOC: the OOC recall ring — the same Up/Down history, fired from any of the
// OOC inputs (the OOC box "oocmsg", the bottom bar "ooc", the themed "ooc"), all sharing oocInput.
func (a *App) recordSentOOC(raw string) { recordSentLine(raw, &a.oocSentHist, &a.oocRecallIdx) }
func (a *App) recallOOC() {
	f := a.ctx.focusID
	recallLine(a.ctx, f == "ooc" || f == "oocmsg", &a.oocInput, a.oocSentHist, &a.oocRecallIdx, &a.oocRecallDraft)
}

// areaLockedBg / areaCurrentBg fill a locked area's card dark red and the area
// you're in a dim accent (light text stays readable on top); areaLockedBorder is
// the brighter red outline that makes a locked card pop as its own button.
var (
	areaLockedBg     = sdl.Color{R: 96, G: 32, B: 32, A: 255}
	areaLockedBorder = sdl.Color{R: 170, G: 64, B: 64, A: 255}
	areaCurrentBg    = sdl.Color{R: 40, G: 56, B: 96, A: 255}
)

// areaCardGap is the vertical space left between area cards so each reads as a
// separate button rather than one continuous striped list (playtest request).
const areaCardGap = 5

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
		a.refreshPairOffsetBufs() // pair panel rows mirror the command
		return true
	}
	return false
}

// renderRaster rasterizes the current message with its AO color. comicInk forces the
// DEFAULT colour to dark "ink" for the comic export's paper speech bubble (explicit
// \cN colours still win), so light default text isn't invisible on the white bubble.
func renderRaster(a *App, sc *courtroom.Scene, wrapW int32, skinned bool, pct int, comicInk bool) (*render.MessageRaster, error) {
	// The chat zoom font: rebuilt only when the Text knob changes.
	col := chatBaseColor(a, sc, skinned, comicInk)
	// Per-message font pick at the given scale (pct): the override chain's first
	// covering font (CJK fallback), the embedded font otherwise. The live chatbox
	// passes a.chatPct; the export passes a size fitted to the capture frame.
	logFont := a.ctx.ChatFontFor(pct, sc.MessageText)
	// #77: rasterize with the DEVICE-scaled sibling at the ambient device scale so
	// the LIVE chatbox is crisp at UI scale. Exports run with textDevPct BRACKETED
	// to 100 for the whole session (see beginExportScaleBracket), so this ambient
	// read is already 100 there — native offscreen resolution, the live UI scale
	// can never leak into export pixels.
	dev := a.ctx.textDevPct
	font := a.ctx.deviceFontFor(logFont, pct)
	// Per-glyph fallback raster when the message has emoji OR mixes scripts no single
	// face covers (covers() reads the pick just made above — no rescan): each rune
	// routes to the color-emoji face or its covering text face, baseline-aligned.
	// Gated cheaply so PLAIN single-script messages (the overwhelming common case)
	// never reach here and stay on the untouched fast paths below — zero change.
	needEmoji := render.NeedsEmojiFallback(sc.MessageText)
	if needEmoji || !a.ctx.covers(sc.MessageText) {
		if needEmoji {
			a.ensureEmojiFontLoad() // kick off the one off-thread system-emoji read
		}
		var spans []render.ColorSpan
		if sceneNeedsStyled(sc.MessageStyles) {
			spans = buildColorSpans(sc.MessageStyles, col)
		} else {
			spans = []render.ColorSpan{{Len: len([]rune(sc.MessageText)), Color: col}}
		}
		textFonts := a.ctx.deviceCoverRunes(logFont, pct, []rune(sc.MessageText))
		m, err := render.RasterizeFallback(a.ctx.Ren, textFonts, a.ctx.emojiDeviceFont(pct), sc.MessageText, spans, wrapW, dev)
		return centerRaster(m, err, sc, wrapW)
	}
	// Inline \cN colors → the multi-color span raster; plain messages keep the
	// untouched single-color path (col is their whole-message color).
	if sceneNeedsStyled(sc.MessageStyles) {
		m, err := render.RasterizeStyled(a.ctx.Ren, font, sc.MessageText, buildColorSpans(sc.MessageStyles, col), wrapW, dev)
		return centerRaster(m, err, sc, wrapW)
	}
	m, err := render.Rasterize(a.ctx.Ren, font, sc.MessageText, wrapW, col, dev)
	return centerRaster(m, err, sc, wrapW)
}

// centerRaster applies the webAO "~~" centre alignment to a freshly built raster when
// the scene asked for it (Scene.Centered). Wraps the (raster, err) return so the three
// build paths stay one-liners; a no-op for the common left-aligned case and on error.
func centerRaster(m *render.MessageRaster, err error, sc *courtroom.Scene, wrapW int32) (*render.MessageRaster, error) {
	if err == nil && m != nil && sc.Centered {
		m.Center(wrapW)
	}
	return m, err
}

// chatBaseColor resolves a message's DEFAULT text colour (used as the whole-message colour
// and as the base for the animated-text path). The theme's "message" colour replaces only
// AO's default code 0 — explicit \cN colours always win — and only while the theme's own
// chatbox skin is drawn (theme colours assume their skin: black on paper, not our dark
// panel). comicInk forces dark ink for the comic export's white bubble.
func chatBaseColor(a *App, sc *courtroom.Scene, skinned, comicInk bool) sdl.Color {
	col := render.TextColor(sc.TextColor)
	switch {
	case sc.TextColor != 0:
		// an explicit message colour always wins (reads on either background)
	case comicInk:
		col = comicBubbleInk
	case skinned && a.themeHasMsg:
		col = a.themeMsgCol
	}
	return col
}

// glyphCacheCap bounds the shared #M5 white-glyph cache. One chatbox message is ≤ the AO
// ~256-char limit, so this comfortably holds a whole message's distinct glyphs across a
// couple of font sizes without evicting its own glyphs mid-reveal (NewGlyphCache floors it).
const glyphCacheCap = 512

// renderAnimated builds the #M5 animated-text layout for a message that carries effect spans
// — the per-glyph path that can shake/wave/displace and rainbow-recolour. Single base colour
// (the message default) with rainbow overriding per glyph; inline \cN colour + emoji/CJK
// per-glyph fallback don't compose with effects yet (a documented follow-up). Returns the
// layout and the face it used (the glyph cache keys on it).
func renderAnimated(a *App, sc *courtroom.Scene, wrapW int32, skinned bool, pct int) (*render.AnimatedText, *ttf.Font) {
	col := chatBaseColor(a, sc, skinned, false)
	// #77 Part A PUNT: the animated per-glyph path (AnimatedText.Draw + GlyphCache)
	// stays on the LOGICAL face — its per-glyph pen positions and pixel-amplitude
	// effect offsets aren't scale-folded yet, so it keeps drawing 1:1 and the
	// renderer's SetScale bilinearly stretches it (correctly-sized, still soft at
	// >100%). Static / emoji / plain messages ARE crisp; an EFFECTS message stays
	// soft until the follow-up. Clean seam: msAnim XOR msRaster, one per message.
	font := a.ctx.ChatFontFor(pct, sc.MessageText)
	return render.RasterizeAnimated(font, sc.MessageText, toRenderEffectSpans(sc.MessageEffects), animColors(sc, col), wrapW), font
}

// animColors flattens the message's inline-colour runs into a per-rune colour slice so #M5
// effects compose with \cN colours (a red shaking word; the rainbow EFFECT still overrides
// per glyph at draw time). A message with no inline styling returns a single-element slice —
// RasterizeAnimated clamps every rune to that base colour. Bold/italic don't compose with
// effects yet (the animated path is single-font); only colour does.
func animColors(sc *courtroom.Scene, base sdl.Color) []sdl.Color {
	if !sceneNeedsStyled(sc.MessageStyles) {
		return []sdl.Color{base}
	}
	spans := buildColorSpans(sc.MessageStyles, base)
	n := 0
	for _, s := range spans {
		n += s.Len
	}
	out := make([]sdl.Color, 0, n)
	for _, s := range spans {
		for k := 0; k < s.Len; k++ {
			out = append(out, s.Color)
		}
	}
	return out
}

// toRenderEffectSpans maps the courtroom (SDL-free) spans to the render package's spans. The
// effect ids are pinned equal by TestEffectIDsMatchRender, so the cast is a straight copy.
// Allocates once per effects message (build time), never per frame.
func toRenderEffectSpans(spans []courtroom.TextEffectSpan) []render.EffectSpan {
	if len(spans) == 0 {
		return nil
	}
	out := make([]render.EffectSpan, len(spans))
	for i, s := range spans {
		out[i] = render.EffectSpan{Start: s.Start, Len: s.Len, Effect: s.Effect}
	}
	return out
}

// effectsSig folds the effect spans into a cache key so ensureChatRaster rebuilds when they
// change even if the visible text didn't (the zero-width effects frame is stripped out of
// MessageRaw, so two messages with the same text but different effects share a raw key).
func effectsSig(spans []courtroom.TextEffectSpan) uint64 {
	const fnvOffset, fnvPrime = 1469598103934665603, 1099511628211
	h := uint64(fnvOffset)
	for _, s := range spans {
		h = (h ^ uint64(uint32(s.Start))) * fnvPrime
		h = (h ^ uint64(uint32(s.Len))) * fnvPrime
		h = (h ^ uint64(s.Effect)) * fnvPrime
	}
	return h
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
	icColorCustomIdx  = icColorRandomIdx + 1
	icColorChoices    = buildICColorChoices()
)

// buildICColorChoices assembles the dropdown label list once at init: standard
// palette names, extended colour names, then Rainbow/Random/Custom.
func buildICColorChoices() []string {
	out := append([]string{}, render.TextColorNames()...)
	for i := 0; i < render.ExtColorCount(); i++ {
		out = append(out, render.ExtColorAt(i).Name)
	}
	return append(out, "Rainbow", "Random", "Custom…")
}

// applyICColorChoice routes an IC colour-dropdown selection (shared by the
// classic and themed rows so they can't drift). The four kinds are mutually
// exclusive — picking one clears the others. Critically, only a standard 0..8
// pick touches a.icColor (the wire text_color); extended colours live in
// a.icExtColor and ship as inline markup with a nearest-standard wire fallback,
// so the wire field never leaves 0..8 (#98).
func (a *App) applyICColorChoice(next int) {
	a.icCustomOn = false // every non-Custom pick turns the free hex off (mutual exclusion)
	switch {
	case next == icColorCustomIdx:
		// Free hex pick (v1.52.0, Tifera): seed from the last persisted custom
		// colour and open the wheel. Selecting Custom… again just reopens it.
		a.icCustomOn = true
		a.icCustomRGB = a.d.Prefs.ICCustomColorRGB()
		a.showICColorWheel = true
		a.d.Prefs.SetRainbowMessages(false)
		a.d.Prefs.SetRandomMessageColor(false)
		a.icExtColor = 0
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
	case a.icCustomOn:
		return icColorCustomIdx, sdl.Color{R: uint8(a.icCustomRGB >> 16), G: uint8(a.icCustomRGB >> 8), B: uint8(a.icCustomRGB), A: 255}
	case a.icExtColor > 0 && a.icExtColor <= render.ExtColorCount():
		return extColorFirst + a.icExtColor - 1, render.ExtColorAt(a.icExtColor - 1).Color
	default:
		return a.icColor, render.TextColor(a.icColor)
	}
}

// icFieldID is the immediate-mode id of the main IC text field (both the classic
// and themed rows draw it under this id). captureICColorSel reads the field's
// selection by this id (only the focused field's selection is meaningful).
const icFieldID = "ic"

// icColorDDID is the immediate-mode id of the IC colour dropdown (shared by the
// classic and themed rows). captureICColorSel keeps its frozen selection alive
// ONLY while this dropdown is the open one — any other unfocus means the
// snapshot is stale and must not wrap on a later pick.
const icColorDDID = "colordd"

// icColorSel is a frozen snapshot of the IC field's [lo,hi) RUNE selection, taken
// while the field is still focused so the colour dropdown can wrap it even though
// the open-click has since unfocused the field. active is false when there was no
// selection to wrap. A plain value (no pointer) — the App holds one directly.
type icColorSel struct {
	lo, hi int
	active bool
}

// captureICColorSel freezes the IC field's live selection into a.icColorSel so a
// colour picked from the dropdown can wrap it (§3.8 select-and-colour, folded into
// the colour dropdown to match AO2's own on_text_color_changed). It MUST be called
// every frame right before c.Dropdown("colordd", …) in BOTH the classic and themed
// rows: the colour strip draws BEFORE the IC field, so at this point the field is
// still focused and its selAnchor/caret are intact — but the dropdown's open-click
// (dropdownEx doesn't consume it) reaches the later-drawn IC field and click-away
// unfocuses it, dropping the live selection before the pick lands next frame. So we
// snapshot here while it exists; once the dropdown is open the field is unfocused,
// selectionFor returns ok=false, and we leave the frozen snapshot untouched.
//
// Alloc-free: selectionFor → fieldSel(utf8.RuneCountInString) is int-only, so the
// per-frame IC-row alloc gate stays green (the splice runs only on the pick).
func (a *App) captureICColorSel() {
	c := a.ctx
	if lo, hi, ok := c.selectionFor(icFieldID, a.icInput); ok {
		a.icColorSel = icColorSel{lo: lo, hi: hi, active: true} // focused + non-empty: track live
		return
	}
	if c.focusID == icFieldID || c.ddOpen != icColorDDID {
		// Focused with no selection, OR unfocused without the colour dropdown
		// being the open one: any snapshot is stale. The second case is the
		// review-caught hazard — a click on some OTHER widget also arms the
		// snapshot on its way to unfocusing the field, and a much-later
		// dropdown pick must not wrap that long-gone selection (possibly at
		// stale rune indices into edited text). The frozen snapshot is only
		// meaningful inside the open colour dropdown's own focus-stealing
		// window, so that is the only unfocused state that preserves it.
		a.icColorSel.active = false
		return
	}
	// Unfocused while the colour dropdown is open: keep the frozen pre-open selection.
}

// applyICColorPick routes an IC colour-dropdown selection, wrapping a frozen
// selection when the pick is a standard palette colour that carries AO2 markup —
// exactly what AO2's Courtroom::on_text_color_changed does when the IC field has a
// selection (src/courtroom.cpp:6364-6390): it inserts the colour's markdown_start
// before the selection and markdown_end after it (leaving the whole-message
// text_color untouched), and does NOTHING for a colour with no markup characters
// (c0/default; :6370-6375 qWarning+return). With no selection it sets the
// whole-message colour (:6391-6401), which is the existing applyICColorChoice.
//
// Only standard palette entries (dropdown index 0..extColorFirst) map to an AO2
// colour; the AsyncAO-native extras (extended/rainbow/random/custom hex) have no
// AO2 wire markup, so they always fall through to applyICColorChoice — the
// no-selection behaviour — rather than inventing new wire markup.
func (a *App) applyICColorPick(next int) {
	sel := a.icColorSel
	a.icColorSel.active = false // consume the snapshot: the next open re-captures
	if sel.active && next >= 0 && next < extColorFirst {
		// A standard palette pick with a live selection. AO2's on_text_color_changed
		// wraps the selection in the colour's markdown chars — EXCEPT for a colour
		// with none (the stock c0/Default has empty _start; :6370-6375 qWarning +
		// return, doing nothing). AO2MarkupFor mirrors that: ok is true only for the
		// eight colours (1..8) that carry delimiters, false for palette 0 — so both
		// the wrap and the "no-markup → do nothing" case fall out of this one branch.
		if start, end, ok := courtroom.AO2MarkupFor(next); ok {
			a.wrapICSelection(sel.lo, sel.hi, start, end)
		}
		return // AO2 leaves the whole-message text_color untouched on a selection.
	}
	// No frozen selection (or an AsyncAO-only extra: extended/rainbow/random/custom
	// hex, which have no AO2 wire markup): set the whole-message colour instead.
	a.applyICColorChoice(next)
}

// wrapICSelection splices the AO2 delimiters start/end around the [lo,hi) RUNE
// range of a.icInput, so a real AO2/webAO peer renders the wrapped span in colour
// (the characters survive to the wire). Rune-indexed (emoji/CJK selections splice
// on rune boundaries, never mid-codepoint). The splice lands in a.icInput directly,
// so the field's out-of-band change detector records the prior value on the next
// draw and Ctrl+Z undoes the wrap (fieldhistory.go). The build runs only on a pick
// (a rare event), so a plain slice build is fine — it is off the per-frame path.
func (a *App) wrapICSelection(lo, hi int, start, end rune) {
	rs := []rune(a.icInput)
	if lo < 0 || hi > len(rs) || lo > hi { // stale snapshot vs a value edited since: ignore
		return
	}
	out := make([]rune, 0, len(rs)+2)
	out = append(out, rs[:lo]...)
	out = append(out, start)
	out = append(out, rs[lo:hi]...)
	out = append(out, end)
	out = append(out, rs[hi:]...)
	a.icInput = string(out)
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
		case s.Color >= courtroom.ColorHexBase: // exact transmitted hex (v1.52.0): unpack 0xRRGGBB
			rgb := s.Color - courtroom.ColorHexBase
			col := sdl.Color{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 255}
			out = append(out, render.ColorSpan{Len: s.Len, Color: col, Bold: s.Bold, Italic: s.Italic})
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
