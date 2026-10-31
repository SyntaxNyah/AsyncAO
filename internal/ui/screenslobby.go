package ui

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// directRowLayout is one frame's direct-connect geometry, in absolute window X.
type directRowLayout struct {
	label          string // "" when even the short caption had to go
	labelX, fieldX int32
	fieldW         int32
	tlsX, saveX    int32
	connX          int32
}

// lobbyDirectRow places the row inside w, shedding the long caption first, then input width, then the caption entirely.
func lobbyDirectRow(w, fullW, shortW, tlsW, saveW int32) directRowLayout {
	// The tail is fixed: two checkboxes and the button, all measured, none droppable.
	tail := tlsW + dcGapPx + saveW + dcGapPx + dcConnectW
	room := w - 2*pad - tail - dcGapPx // what is left for caption + input
	lay := directRowLayout{label: dcLabelFull, fieldW: dcFieldWMax}
	labelW := fullW
	if labelW+dcGapPx+lay.fieldW > room {
		lay.label, labelW = dcLabelShort, shortW // step 1: the short caption
	}
	if labelW+dcGapPx+lay.fieldW > room {
		lay.fieldW = room - labelW - dcGapPx // step 2: squeeze the input
	}
	if lay.fieldW < dcFieldWMin { // step 3: drop the caption and give the input its floor
		lay.label, labelW = "", -dcGapPx // -gap cancels the gap a caption would have needed
		if lay.fieldW = room; lay.fieldW > dcFieldWMax {
			lay.fieldW = dcFieldWMax
		}
		if lay.fieldW < dcFieldWMin {
			lay.fieldW = dcFieldWMin
		}
	}
	lay.labelX = pad
	lay.fieldX = pad + labelW + dcGapPx
	lay.tlsX = lay.fieldX + lay.fieldW + dcGapPx
	lay.saveX = lay.tlsX + tlsW + dcGapPx
	lay.connX = lay.saveX + saveW + dcGapPx
	// Below MinWindowW the shrink can run out of room: pull the tail back and let controls overlap rather than
	// disappear.
	if over := lay.connX + dcConnectW - (w - pad); over > 0 {
		lay.connX -= over
		if lay.saveX -= over; lay.saveX < pad {
			lay.saveX = pad
		}
		if lay.tlsX -= over; lay.tlsX < pad {
			lay.tlsX = pad
		}
		if lay.connX < pad {
			lay.connX = pad
		}
	}
	return lay
}

// lobbyUtilWidth is the cluster's shared button width: the default if the row fits, else an even split, floored.
func lobbyUtilWidth(w int32) int32 {
	bw := lobbyUtilBtnW
	room := w - 2*pad
	if lobbyUtilBtnCount*(bw+lobbyUtilBtnGap)-lobbyUtilBtnGap <= room {
		return bw
	}
	if bw = (room - (lobbyUtilBtnCount-1)*lobbyUtilBtnGap) / lobbyUtilBtnCount; bw < lobbyUtilBtnMinW {
		bw = lobbyUtilBtnMinW
	}
	return bw
}

// lobbyUtilStop is the X of the i-th cluster button from the right; the clamp lets an absurd window overlap, not lose
// them.
func lobbyUtilStop(w, i int32) int32 {
	btnW := lobbyUtilWidth(w)
	x := w - pad - btnW - i*(btnW+lobbyUtilBtnGap)
	if x < pad {
		return pad
	}
	return x
}

// lobbyDirectRowY and lobbyListTop derive the lobby's two lower rows from the chrome band, so callers share one source.
func lobbyDirectRowY(topChrome int32) int32 { return topChrome + pad + lobbyDirectRowDropY }

func lobbyListTop(topChrome int32) int32 {
	return lobbyDirectRowY(topChrome) + lobbyListActionsDropY + btnH + lobbyListActionsGapY
}

// connErrLabel is the one-line view of connErr, memoized because the clamp allocates and the lobby draws per frame.
// Raw connErr fails invisibly: SDL_ttf does not break lines, and a long enough string fails at the texture, clipped or
// not.
func (a *App) connErrLabel() string {
	if a.connErrSrc != a.connErr {
		a.connErrSrc = a.connErr
		s := a.connErr
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			// Mark the cut: a reason stopping at its first line reads as whole, and what matters is usually below it.
			s = strings.TrimRight(s[:i], " \t\r") + " …"
		}
		a.connErrLine = clampLine(s) // logLineMax runes, the shared single-line UI cap
	}
	return a.connErrLine
}

func (a *App) drawLobby(w, h int32) {
	a.pollLobbyFetch()
	a.pollPing() // drain connect-time probes (no-op unless a sweep is running)
	c := a.ctx
	a.drawScreenBackdrop(w, h, "lobbybackground")
	// hdrY is this screen's header row, below the app-chrome band; offsets below are relative to it.
	hdrY := a.topChromeH() + pad
	c.Heading(pad, hdrY, "AsyncAO", ColText)
	c.Label(pad, hdrY+30, a.lobbyStatus, ColTextDim)
	if a.connErr != "" {
		c.Label(pad+220, hdrY+30, a.connErrLabel(), ColDanger)
		if a.lastConnURL != "" { // one-click Reconnect to the server we dropped from
			label := "Reconnect to " + a.lastConnName
			bw := c.TextWidth(label) + 20
			if c.Button(sdl.Rect{X: pad + 220, Y: hdrY + 50, W: bw, H: btnH}, label) {
				a.connErr = ""
				a.Connect(a.lastConnName, a.lastConnURL)
			}
		}
	}
	// Auto-reconnect status while a retry is pending, with Stop to bail out; the message is rebuilt per attempt.
	if !a.autoReconnectAt.IsZero() {
		c.Label(pad+220, hdrY+78, a.autoReconnectMsg, ColAccent)
		sx := pad + 220 + c.TextWidth(a.autoReconnectMsg) + 12
		if c.Button(sdl.Rect{X: sx, Y: hdrY + 74, W: c.TextWidth("Stop") + 20, H: btnH}, "Stop") {
			a.cancelAutoReconnect()
		}
	}

	// Top bar buttons are laid out right to left by lobbyUtilStop; the Phone Book toggle takes its rect here but draws
	// below.
	utilW := lobbyUtilWidth(w)
	pbBtn := sdl.Rect{X: lobbyUtilStop(w, 0), Y: hdrY, W: utilW, H: btnH}
	// The cluster's leftmost X is what the centred Privacy/Glossary row measures its room against.
	utilLeft := lobbyUtilStop(w, lobbyUtilBtnCount-1)
	// Privacy, Glossary and What's New sit centred between the title and the cluster, shrinking on a narrow window.
	const (
		helpBtnWMax     = int32(132) // prominent width when there's room (fullscreen)
		helpBtnWMin     = int32(96)  // floor so "What's New" stays readable when squeezed
		helpBtnGap      = int32(12)
		helpBtnCount    = int32(3)
		headerTitleZone = int32(180) // reserved width for the "AsyncAO" heading on the left
	)
	titleRight := pad + headerTitleZone
	// Shed from the right when the zone cannot hold the row at minimum width, or it would paint and click over Phone
	// Book.
	shown := helpBtnCount
	helpBtnW, helpX, helpPitch := helpBtnWMax, titleRight, int32(0)
	for ; shown > 0; shown-- {
		gaps := helpBtnGap * (shown - 1)
		helpBtnW = helpBtnWMax
		if zoneW := utilLeft - titleRight; helpBtnW*shown+gaps > zoneW {
			if helpBtnW = (zoneW - gaps) / shown; helpBtnW < helpBtnWMin {
				helpBtnW = helpBtnWMin
			}
		}
		rowW := helpBtnW*shown + gaps
		if helpX = (titleRight+utilLeft)/2 - rowW/2; helpX < titleRight {
			helpX = titleRight
		}
		if helpX+rowW <= utilLeft {
			break // fits without touching the utility cluster
		}
	}
	helpPitch = helpBtnW + helpBtnGap
	helpY, helpH := hdrY-2, btnH+4 // a touch taller than the utility buttons
	// helpSlot places button i, or reports false once the row has shed that far.
	helpSlot := func(i int32) (sdl.Rect, bool) {
		if i >= shown {
			return sdl.Rect{}, false
		}
		return sdl.Rect{X: helpX + i*helpPitch, Y: helpY, W: helpBtnW, H: helpH}, true
	}
	if r, ok := helpSlot(0); ok && c.ButtonCol(r, "Privacy", ColPanel, ColPanelHi, ColAccent, ColAccent) {
		a.prevScreen = ScreenLobby
		a.openHelp(1)
	}
	if r, ok := helpSlot(1); ok && c.ButtonCol(r, "Glossary", ColPanel, ColPanelHi, ColAccent, ColAccent) {
		a.prevScreen = ScreenLobby
		a.openHelp(0)
	}
	// What's New sheds first: Privacy and Glossary are what a newcomer needs before playing.
	if r, ok := helpSlot(2); ok && c.ButtonCol(r, "What's New", ColPanel, ColPanelHi, ColAccent, ColAccent) {
		a.prevScreen = ScreenLobby
		a.screen = ScreenChangelog
		a.markChangelogSeen() // opening the notes is what stamps this build as read
	}
	// The Phone Book toggle stays on screen because "← All servers" is this page's only way back.
	pbLabel := "★ Phone Book"
	if a.phoneBookPage {
		pbLabel = "← All servers"
	}
	if c.Button(pbBtn, pbLabel) {
		a.setPhoneBookPage(!a.phoneBookPage)
	}
	c.Tooltip(pbBtn, "Your manually-added servers — kept forever (until Wipe everything), exportable")

	dcY := lobbyDirectRowY(a.topChromeH())
	if a.phoneBookPage {
		a.drawPhoneBookBar(w, dcY)
	} else {
		// Laid out by lobbyDirectRow so nothing walks off the right edge of a config.MinWindowW window.
		const tlsLabel, saveLabel = "TLS (wss)", "Save to phone book"
		row := lobbyDirectRow(w, c.TextWidth(dcLabelFull), c.TextWidth(dcLabelShort),
			c.CheckboxWidth(tlsLabel), c.CheckboxWidth(saveLabel))
		if row.label != "" {
			c.Label(row.labelX, dcY+4, row.label, ColText)
		}
		a.directInput, _ = c.TextField(directConnectFieldID, sdl.Rect{X: row.fieldX, Y: dcY, W: row.fieldW, H: fieldH}, a.directInput, "127.0.0.1:50001")
		a.directSecure = c.Checkbox(row.tlsX, dcY+4, tlsLabel, a.directSecure)
		a.directSave = c.Checkbox(row.saveX, dcY+4, saveLabel, a.directSave)
		if c.Button(sdl.Rect{X: row.connX, Y: dcY, W: dcConnectW, H: btnH}, "Connect") {
			a.directConnect()
		}
	}

	// The list's action strip; here because three buttons in the header cluster squeezed out the help row at
	// MinWindowW.
	// The Servers menu shares these setters. The label carries in-flight state so the control stays under the cursor.
	listActY := dcY + lobbyListActionsDropY
	refreshLabel := "Refresh"
	if a.lobbyFetching {
		refreshLabel = "Refreshing…"
	}
	refreshBtn := sdl.Rect{X: pad, Y: listActY, W: lobbyListActionW, H: btnH}
	if c.Button(refreshBtn, refreshLabel) && !a.lobbyFetching {
		a.RefreshServers()
	}
	c.Tooltip(refreshBtn, "Re-fetch the master server list")

	sortLabel := "Sort: name"
	if a.pingMode {
		sortLabel = "Sort: speed"
	}
	if a.pinging {
		sortLabel = "Pinging…"
	}
	sortBtn := sdl.Rect{X: pad + lobbyListActionW + lobbyUtilBtnGap, Y: listActY, W: lobbyListActionW, H: btnH}
	if c.Button(sortBtn, sortLabel) && !a.pinging {
		a.toggleConnectTimeSort()
	}
	c.Tooltip(sortBtn, "Order the list by how fast each server answers, instead of by name")

	// Server rows: one click expands the description, a second joins. The wheel scrolls only over the list.
	listTop := lobbyListTop(a.topChromeH())
	a.lobbyScroll -= c.WheelIn(sdl.Rect{X: 0, Y: listTop, W: w, H: h - listTop}) * scrollStepPx
	if a.lobbyScroll < 0 {
		a.lobbyScroll = 0
	}
	lineH := int32(c.font.Height()) + 3
	y := listTop - a.lobbyScroll
	legacyHeaderDrawn := false
	// Claim the chip's click before rows draw: it overlaps a row corner, and rows act on c.clicked immediately.
	if a.phoneBookPage {
		a.claimPhoneFanatClick(w, h)
	}
	// Range a snapshot: a ★ removal or phone-book Save re-assigns a.servers mid-frame, and a shrunk slice would panic.
	servers := a.servers
	for i := range servers {
		e := &servers[i]
		if a.phoneBookPage && !e.Favorite {
			continue // Phone Book shows only your saved servers
		}
		if !e.Joinable() && !legacyHeaderDrawn {
			if y > listTop-rowH && y < h {
				msg := "— NOT SUPPORTED: " + network.UnsupportedReason
				c.Label(pad, y+4, msg, ColDanger)
				// The upgrade guide for a server owner seeing their own black row.
				bx := pad + c.TextWidth(msg) + 12
				const helpBtnW = 180
				if bx+helpBtnW > w-pad {
					bx = w - pad - helpBtnW
				}
				if c.Button(sdl.Rect{X: bx, Y: y, W: helpBtnW, H: 24}, "For server owners…") {
					a.prevScreen = ScreenLobby // the guide's Back / Esc both return here
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
				// Rehearse: offline browse of the cached roster. Read the snapshot: a ★ removal this frame may have
				// shrunk a.servers.
				e := &servers[i]
				if e.Joinable() {
					if c.Button(sdl.Rect{X: box.X + 6, Y: ly + 4, W: 320, H: btnH}, "Rehearse offline (browse cached characters)") {
						if info := a.d.Prefs.Load().ServerWarmInfoFor(e.WebSocketURL()); info.Origin != "" && len(info.Chars) > 0 {
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

	// Arrows move through joinable servers and Enter joins, when no text field is focused.
	// Not while the notice box is up: the pointer block leaves the keyboard alone, and Enter would reconnect to the
	// refuser.
	if c.focusID == "" && !a.serverNoticeDlg.open {
		// Enter reads c.enter: HandleEvent never puts Return in c.keyPressed. Consumed so one press joins once.
		if c.enter {
			c.enter = false
			if a.selServer >= 0 && a.selServer < len(a.servers) && a.servers[a.selServer].Joinable() {
				e := &a.servers[a.selServer]
				a.Connect(e.Name, e.WebSocketURL())
				return
			}
		}
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
		}
	}

	// Phone Book page only, so the chip stays out of the all-servers lobby.
	if a.phoneBookPage {
		a.drawPhoneFanatChip(w, h)
	}

	// The app starts here, so startup notices like the corrupt-prefs quarantine must draw on the lobby too.
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

	// Inline edit replaces the row; the early return suppresses star-remove, click-to-connect and the tooltip.
	// Identity is the stored Favorites URL, WebSocketURL().
	editing := a.phoneBookPage && e.Favorite && a.pbEditURL != "" && a.pbEditURL == e.WebSocketURL()
	if editing {
		fy := y + (rowH-2-fieldH)/2
		a.pbEditName, _ = c.TextField("pbedit_name", sdl.Rect{X: row.X + 6, Y: fy, W: 150, H: fieldH}, a.pbEditName, "name")
		a.pbEditAddr, _ = c.TextField("pbedit_url", sdl.Rect{X: row.X + 164, Y: fy, W: 250, H: fieldH}, a.pbEditAddr, "host:port, ws:// or wss://")
		if c.Button(sdl.Rect{X: row.X + 422, Y: fy, W: 64, H: btnH}, "Save") {
			a.savePhoneBookEdit(e.WebSocketURL(), e.Description)
		}
		if c.Button(sdl.Rect{X: row.X + 492, Y: fy, W: 72, H: btnH}, "Cancel") {
			a.cancelPhoneBookEdit()
		}
		return
	}

	if hover { // #17: hover shows the URL + description ("MOTD") without clicking to expand
		a.serverRowTooltip(row, e)
	}

	c.Fill(sdl.Rect{X: row.X + 2, Y: y + 4, W: 14, H: rowH - 10}, tierColor(*e))

	starRect := sdl.Rect{X: row.X + 22, Y: y + 2, W: 22, H: rowH - 6}
	starCol := ColTextDim
	if e.Favorite {
		starCol = ColStar
	}
	c.IconLabel(starRect, "★", starCol)
	if c.hovering(starRect) && c.clicked {
		a.toggleFavorite(e)
		return
	}

	// Edit button on saved rows on the Phone Book page only; it shifts the name right in this branch.
	nameX := row.X + 52
	if a.phoneBookPage && e.Favorite {
		const editBtnW = 40
		editBtn := sdl.Rect{X: starRect.X + 22, Y: y + 1, W: editBtnW, H: rowH - 4}
		if c.Button(editBtn, "Edit") {
			a.beginPhoneBookEdit(e.WebSocketURL(), e.Name, e.WebSocketURL())
			return
		}
		nameX = editBtn.X + editBtnW + 8
	}

	name := fmt.Sprintf("%s  (%d players)", e.Name, e.Players)
	if !e.Joinable() {
		name = e.Name + "  — legacy TCP only"
	}
	textCol := ColText
	if !e.Joinable() {
		textCol = ColTextDim
	}
	c.LabelClipped(nameX, y+5, row.W-260-(nameX-(row.X+52)), name, textCol)

	// Right-side label: security tier, or in ping mode the connect time; the tier colour still reads off the swatch.
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
		// Rehearse always shows on the selected row; with no remembered roster it explains itself.
		if idx == a.selServer {
			info := a.d.Prefs.Load().ServerWarmInfoFor(e.WebSocketURL())
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

// selectServerRow selects a row and rebuilds its cached description lines and links.
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

// serverRowTooltip shows a row's connect URL and advertised description on hover.
func (a *App) serverRowTooltip(row sdl.Rect, e *network.ServerEntry) {
	tip := e.WebSocketURL()
	if d := strings.TrimSpace(e.Description); d != "" {
		tip = d + "   —   " + tip
	}
	a.ctx.Tooltip(row, tip)
}

// nextJoinableServer is the next joinable index from `from` in direction dir, honouring the Phone Book filter; -1 if
// none.
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

// scrollServerIntoView makes row idx fully visible, counting the legacy header and the Phone Book filter.
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

// drawWardrobeGrid is the wardrobe's char-select grid, sharing the Characters tab's charGridPlan and charListRows.
// drawIniswapCell defers ★ toggles and folder menus because both rebuild slices the loop ranges; this services them.
func (a *App) drawWardrobeGrid(w, h int32, g charGridPlan, rows charListRows, query string) {
	// The open folder menu resolves clicks before the grid draws, so they cannot leak to a cell underneath.
	if a.iniMenuChar != "" {
		opts := a.iniFolderMenuOpts()
		a.handleIniFolderMenu(iniFolderMenuRect(a.iniMenuAt, len(opts), w, h), opts)
	}
	a.drawWardrobeGridCells(w, h, g, rows, query)
	// A deferred ★ toggle rebuilds the slices the loop ranged, so it runs after the loop.
	a.applyPendingFav()
	// The menu paints last, above the grid and any sprite preview.
	if a.iniMenuChar != "" {
		opts := a.iniFolderMenuOpts()
		a.drawIniFolderMenu(iniFolderMenuRect(a.iniMenuAt, len(opts), w, h), opts)
	}
}

// drawWardrobeGridCells is the grid body, split so the wrapper's after-loop work also runs on the empty-state return.
func (a *App) drawWardrobeGridCells(w, h int32, g charGridPlan, rows charListRows, query string) {
	c := a.ctx
	if len(a.iniList) == 0 {
		// Anchored on the grid's viewport: under a theme the grid starts at char_buttons, not x=pad.
		msgX, msgW := g.view.X+pad, g.view.W-2*pad
		if !g.themed {
			msgX, msgW = pad, w-2*pad
		}
		switch {
		case a.iniBusy:
			c.LabelClipped(msgX, g.view.Y+8, msgW, "Fetching "+iniswapFileName+"...", ColTextDim)
		case a.iniListErr != "":
			c.LabelClipped(msgX, g.view.Y+8, msgW, a.iniListErr, ColTextDim)
		default:
			c.LabelClipped(msgX, g.view.Y+8, msgW,
				"Wardrobe empty — tap the ★ on any character in the Characters tab to save it here (it stays per server).", ColTextDim)
		}
		return
	}

	// A wardrobe folder cannot be occupied, so the shared filter runs with taken always shown.
	a.iniScroll = a.charGridScroll(w, "iniscroll", g, a.iniScroll, rows.matches(query, true))

	n := int32(0)
	// AO2 raises char_selector above every button (aocharbutton.cpp); paint it after the loop or the next cell covers
	// it.
	hoverCell := sdl.Rect{}
	// pushClip, not raw SetClipRect, so a cell scrolled under the top bar is neither drawn nor clickable there.
	prev, had := c.pushClip(g.view)
	for i := range a.iniList {
		if rows.hidden(i, query, true) {
			continue
		}
		cell := g.cellAt(n, a.iniScroll)
		n++
		if g.visible(cell) {
			if c.hovering(cell) {
				hoverCell = cell
			}
			// A favourite switches to the real character (wardrobeClick), not a blind iniswap.
			a.drawIniswapCell(i, cell, cellClickChar, g.caption)
			if i == a.charListSel {
				c.Border(cell, ColStar) // the name column's selection (charselectwidgets.go)
			}
		}
	}
	if g.themed && hoverCell.W > 0 {
		a.drawCharSelectorHalo(hoverCell, g.haloPx)
	}
	c.popClip(prev, had)
	if a.previewBase != "" {
		a.drawSpritePreview(w, h, false, "") // character preview: no emote-name caption
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

func (a *App) drawCharSelect(w, h int32) {
	c := a.ctx
	// Under a theme the backdrop is blitted into AO2's fixed canvas so art and layout share one transform.
	csLay := a.charSelectLayout(w, h)
	a.drawCharSelectBackdrop(w, h, csLay)
	// Drain preview parses here, or the goroutine blocks on its result channel and the portrait stays empty.
	a.pollPreviewEmotes()
	// Content starts below the app-chrome band.
	hdrY := a.topChromeH() + pad

	// If the theme's canvas can carry the controls they sit at its rects with no AsyncAO header; otherwise the header
	// stays.
	// The predicate needs a grid plan, so the themed plan is built first; charSelectGridPlan is pure and alloc-free.
	themedTop := a.topChromeH()
	themedUI := csLay.valid && a.charSelectWidgetsThemed(csLay, a.charSelectGridPlan(w, h, themedTop, csLay))
	gridTop := themedTop
	if !themedUI {
		gridTop = hdrY + charSelectHeaderH
	}
	// AO2's grid inside char_buttons under a theme (charselectgrid.go), else the full-window grid; both tabs use this
	// plan.
	g := a.charSelectGridPlan(w, h, gridTop, csLay)

	// Status sits under the client row when classic, at the top-left of the grid when themed.
	statusX, statusY := pad, hdrY+40
	if themedUI {
		statusX, statusY = g.view.X, g.view.Y
	}

	if themedUI {
		// Drawn above the session checks: a client stuck handshaking must still be able to leave.
		if a.drawCharSelectControls(csLay, g) {
			return
		}
	} else {
		c.Heading(pad, hdrY, a.charSelectTitle(), ColText)

		// Back takes the consistent top-right spot; Disconnect stays out of it and is danger-tinted.
		// Buttons lay out right to left.
		rightX := w - pad
		if a.room != nil {
			backW := int32(90)
			rightX -= backW
			if c.Button(sdl.Rect{X: rightX, Y: hdrY, W: backW, H: btnH}, "Back") {
				a.screen = ScreenCourtroom
				return
			}
			rightX -= 8
		}
		dcW := int32(120)
		rightX -= dcW
		if c.ButtonCol(sdl.Rect{X: rightX, Y: hdrY, W: dcW, H: btnH}, "Disconnect", ColPanel, ColPanelHi, ColDanger, ColDanger) {
			a.requestDisconnect() // confirm first unless instant-disconnect is set
			return
		}
		rightX -= 8
		// Opens Help's Privacy tab.
		privW := int32(120)
		rightX -= privW
		if c.Button(sdl.Rect{X: rightX, Y: hdrY, W: privW, H: btnH}, "Privacy") {
			a.prevScreen = ScreenCharSelect
			a.openHelp(1)
		}
	}
	if a.sess == nil {
		c.Label(statusX, statusY, "Loading...", ColTextDim)
		return
	}
	if a.sess.Phase() != courtroom.PhaseReady {
		c.Label(statusX, statusY, "Handshaking with server...", ColTextDim)
		return
	}

	if !themedUI {
		a.charSearch, _ = c.TextField("charsearch", sdl.Rect{X: pad, Y: hdrY + 36, W: 230, H: fieldH}, a.charSearch, "Search...")

		// Tabs swap the same grid between the server's list and your wardrobe.
		tabX := pad + 240
		for _, tb := range charSelectTabs {
			bw := c.TextWidth(tb.label) + 20
			if a.charTab == tb.id {
				c.Fill(sdl.Rect{X: tabX - 2, Y: hdrY + 34, W: bw + 4, H: btnH + 4}, ColAccent)
			}
			if c.Button(sdl.Rect{X: tabX, Y: hdrY + 36, W: bw, H: btnH}, tb.label) {
				a.selectCharTab(tb.id)
			}
			tabX += bw + 6
		}
		specX := tabX + 8
		if c.Button(sdl.Rect{X: specX, Y: hdrY + 36, W: 90, H: btnH}, "Spectate") {
			if !a.sess.Rehearsal {
				a.sess.PickCharacter(protocol.UnpairedCharID)
			}
			a.enterCourtroom()
			return
		}
		if a.warnActive() {
			c.LabelClipped(specX+200, hdrY+42, w-specX-200-pad, a.warnLine, ColDanger)
		}
	} else if a.warnActive() {
		// No header row to hang it from, so along the canvas bottom, off clip rather than area: Crop can run the canvas
		// off-window.
		c.LabelClipped(csLay.clip.X+pad, csLay.clip.Y+csLay.clip.H-charListRowH(a.fontLineH())-pad,
			csLay.clip.W-2*pad, a.warnLine, ColDanger)
	}

	query := a.charQ.get(a.charSearch)
	showTaken := a.charSelectShowTaken(themedUI, csLay)

	if a.charTab == charTabWardrobe {
		rows := charListRows{n: len(a.iniList), names: a.iniList, lower: a.iniLower}
		if themedUI {
			a.drawCharNameList(csLay, rows, query, showTaken, g)
		}
		a.drawWardrobeGrid(w, h, g, rows, query)
		return
	}

	a.ensureCharLower()
	a.ensureWardrobeMembers() // star state for the grid; rebuilt only on change
	rows := charListRows{n: len(a.sess.Chars), chars: a.sess.Chars, lower: a.charLower}
	// The name column draws first so the selection is settled before the cells paint with it.
	if themedUI {
		a.drawCharNameList(csLay, rows, query, showTaken, g)
	}
	// Pre-count matches so the scrollbar knows the content height.
	matches := rows.matches(query, showTaken)
	a.charScroll = a.charGridScroll(w, "charscroll", g, a.charScroll, matches)

	dlOn := a.d.Prefs.Load().CharDownloaderOn // read once per frame, not per cell
	n := int32(0)
	// AO2 raises char_selector above every button (aocharbutton.cpp); paint it after the loop or the next cell covers
	// it.
	hoverCell := sdl.Rect{}
	// pushClip, not raw SetClipRect, so scrolled cells slide under the top bar and cannot be clicked or hovered there.
	prev, had := c.pushClip(g.view)
	for i := range a.sess.Chars {
		slot := &a.sess.Chars[i]
		if rows.hidden(i, query, showTaken) {
			continue
		}
		cell := g.cellAt(n, a.charScroll)
		n++
		if g.visible(cell) { // only rows touching the viewport (no draw/hover through the bar)
			if c.hovering(cell) {
				hoverCell = cell
			}
			a.drawCharCell(slot, cell, i, dlOn, g)
			if i == a.charListSel {
				// The name column's selection, shown on the grid too (charselectwidgets.go).
				c.Border(cell, ColStar)
			}
			if c.HoverPreview(a.charHoverID(i), cell) {
				// Parse the char.ini behind the portrait once per character: "normal" is only a convention, and packs
				// differ.
				// AssetType: CharSprite (preview)
				a.ensurePreviewEmotes(slot.Name, previewEmotePortrait)
				a.setCharPortraitPreview(slot.Name)
			}
		}
	}
	if g.themed && hoverCell.W > 0 {
		a.drawCharSelectorHalo(hoverCell, g.haloPx)
	}
	c.popClip(prev, had)
	if a.previewBase != "" {
		a.drawSpritePreview(w, h, false, "") // character preview: no emote-name caption
		a.closeSpritePreviewOnLeave()
	}
}

func (a *App) drawCharCell(slot *courtroom.CharacterSlot, cell sdl.Rect, idx int, downloaderOn bool, g charGridPlan) {
	c := a.ctx
	c.Fill(cell, ColPanel)
	base := a.urls.CharIcon(slot.Name)
	if page, ok := a.cachedPage(&a.iconPages, &a.iconPagesGen, len(a.sess.Chars), idx, base); ok && len(page.Frames) > 0 {
		// Shared scratch, since &cell into cgo would heap-allocate per visible cell.
		// SDL copies the rect and never retains it.
		c.cgoRect = cell
		_ = c.Ren.Copy(page.Frames[0], nil, &c.cgoRect)
	} else {
		// Not resident: demand it (visible, not speculation) and draw the initials placeholder.
		a.demandAsset(&a.iconAsk, len(a.sess.Chars), idx, base, assets.AssetTypeCharIcon) // AssetType: CharIcon
		// AO2 also sets the character's name on the button with no char_icon (aocharbutton.cpp:55-58).
		// Centred off the cell.
		initial := slot.Name
		if len(initial) > 2 {
			initial = initial[:2]
		}
		c.Label(cell.X+cell.W/2-8, cell.Y+cell.H/2-8, initial, ColTextDim)
	}
	if slot.Taken {
		// AO2's char_taken is a full-cover child of the button (aocharbutton.cpp:14-17, :31-32); no art gets a scrim.
		if !g.themed || !a.drawCharOverlayArt(themeStemCharTaken, cell) {
			c.Fill(sdl.Rect{X: cell.X, Y: cell.Y, W: cell.W, H: cell.H}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
			c.Label(cell.X+6, cell.Y+cell.H/2-8, "taken", ColDanger)
		}
	}
	if g.caption {
		c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, slot.Name, ColTextDim)
	} else {
		// No caption on a theme's grid: it would print across the next row. AO2 uses a tooltip (charselect.cpp:304).
		// Registered before the badge and star tooltips, which override it on their rects (last write wins).
		c.Tooltip(cell, slot.Name)
	}
	// While this character is the active download, mark the cell.
	if a.dl.active && a.dl.target == slot.Name {
		c.Fill(cell, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 70})
		c.Label(cell.X+4, cell.Y+4, downloadGlyph+"…", ColText)
	}
	// Fixed-size badge and star would cover a small cell and steal its clicks, so below the floor both go.
	if cellFitsCornerChrome(cell) {
		// Download badge, opt-in: grabs the folder and the sfx/blips its char.ini names. Works on taken characters too.
		if downloaderOn && a.drawDownloadBadge(cell, "Press the green down arrow to download this character") {
			a.startCharDownload(slot.Name)
			return
		}
		// The star saves to the Wardrobe; the click is consumed so it cannot also pick the character.
		// Works on taken too.
		starred := idx >= 0 && idx < len(a.charLower) && a.wardrobeMembers[a.charLower[idx]]
		starR := sdl.Rect{X: cell.X + cell.W - charCellStarPx - charCellStarInsetPx,
			Y: cell.Y + charCellStarInsetPx, W: charCellStarPx, H: charCellStarPx}
		c.Fill(starR, sdl.Color{R: 0, G: 0, B: 0, A: 130})
		starCol := ColTextDim
		if starred {
			starCol = ColStar
		}
		c.IconLabel(starR, "★", starCol)
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
	}
	if c.hovering(cell) {
		a.warmCharINI(slot.Name) // pick = memory hit, not an RTT
		if c.clicked && !slot.Taken {
			a.pickCharacter(idx) // rehearsal resolves locally
		}
	}
}

// randomChar swaps to a uniformly random untaken character other than the current one; reservoir sampling never
// allocates.
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

// drawSpritePreview shows the previewed sprite; with cycle set it also draws the emote navigator and keeps the box
// alive.
func (a *App) drawSpritePreview(w, h int32, cycle bool, name string) {
	c := a.ctx
	// Detached: the sprite lives in its own OS window. previewFrameRect is zeroed so handlePreviewInput stands down
	// too.
	if a.previewIsDetached() {
		a.previewFrameRect = sdl.Rect{}
		return
	}
	// fenceSpritePreview would also block this box's own buttons, so suspend it for them.
	// Mark-scoped, so anything published after us, like the compact toolbox, still occludes us.
	prevOwner := c.pushOverlayOwner(a.previewOwnerMark())
	defer c.popOverlayOwner(prevOwner)
	page, ok := a.d.Store.Get(a.previewBase)
	ready := ok && len(page.Frames) > 0
	cycling := cycle && len(a.previewAnims) > 1
	if !ready && !cycling {
		a.previewFrameRect = sdl.Rect{} // no box this frame — no phantom wheel/drag target
		return
	}
	if ready && len(page.Frames) > 1 {
		// The box loops on the wall clock, so report animating only while drawn; an orphaned preview must not hold the
		// pace.
		a.NoteAnimating()
	}
	// Every character previews at the same height, the setting or this session's resized value; width follows the
	// aspect.
	boxH := a.previewUserH
	if boxH == 0 {
		boxH = int32(a.d.Prefs.Load().PreviewHeightPx())
	}
	pw, ph := boxH, boxH // placeholder box while the next sprite streams in
	if ready {
		// Restart the loop per pick so animated idles play instead of freezing on frame 0.
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
	// Default centred right, shifted by drag and clamped to the window; bottom-right would cover the IC bar.
	capH := ph + previewCaptionH
	baseX, baseY := w-pw-pad*2, (h-capH)/2
	hiX, hiY := w-pw-pad, h-capH-pad
	if hiX < pad {
		hiX = pad
	}
	if hiY < pad {
		hiY = pad
	}
	dst := sdl.Rect{X: value.Clamp(baseX+a.previewOffX, pad, hiX), Y: value.Clamp(baseY+a.previewOffY, pad, hiY), W: pw, H: ph}
	frame := sdl.Rect{X: dst.X - 4, Y: dst.Y - 4, W: dst.W + 8, H: dst.H + 8 + previewCaptionH}
	a.previewFrameRect = frame // cached for handlePreviewInput (wheel zoom + drag + resize)
	c.Fill(frame, ColPanel)
	c.Border(frame, ColAccent)
	if ready {
		tex := page.Frames[pageFrameLoop(page, a.now().Sub(a.previewAt))]
		if a.previewZoom > 1 {
			// Magnifier: blit a window of the sprite centred on the cursor, so moving the mouse pans.
			zw, zh := page.W/int32(a.previewZoom), page.H/int32(a.previewZoom)
			if zw < 1 {
				zw = 1
			}
			if zh < 1 {
				zh = 1
			}
			relX := float64(c.mouseX-dst.X) / float64(dst.W)
			relY := float64(c.mouseY-dst.Y) / float64(dst.H)
			relX = value.Clamp(relX, 0, 1)
			relY = value.Clamp(relY, 0, 1)
			src := sdl.Rect{X: value.Clamp(int32(relX*float64(page.W))-zw/2, 0, page.W-zw), Y: value.Clamp(int32(relY*float64(page.H))-zh/2, 0, page.H-zh), W: zw, H: zh}
			_ = c.Ren.Copy(tex, &src, &dst)
		} else {
			_ = c.Ren.Copy(tex, nil, &dst)
		}
		a.drawPreviewZoom(frame, page.W, page.H, dst.H)
		// Resize grip at the image's bottom-right, above the caption; handlePreviewInput does the drag.
		gr := previewGripRect(frame)
		for i := int32(0); i < 3; i++ {
			c.Fill(sdl.Rect{X: gr.X + 3 + i*3, Y: gr.Y + gr.H - 3 - i*3, W: 2, H: 2}, ColAccent)
			c.Fill(sdl.Rect{X: gr.X + gr.W - 3 - i*3, Y: gr.Y + 3 + i*3, W: 2, H: 2}, ColAccent)
		}
		c.Tooltip(gr, "Drag to resize the preview")
	} else {
		c.LabelClipped(dst.X+4, dst.Y+dst.H/2-8, dst.W-8, "loading…", ColTextDim)
	}
	// The chrome paints after the blit, or the sprite would cover buttons that remain clickable.
	// Emote-name strip: skipped when cycling (the nav bar captions it) or with no name (character previews).
	if name != "" && !cycle {
		strip := sdl.Rect{X: frame.X, Y: frame.Y, W: frame.W, H: previewNameStripH}
		c.Fill(strip, sdl.Color{R: 0, G: 0, B: 0, A: 205})
		// Always reserve the chrome width: the pin button is always there.
		c.LabelClipped(strip.X+4, strip.Y+3, frame.W-8-previewChromeW, name, ColAccent)
	}
	// Pop-out always means detach: the box stops drawing once detached, and closing the OS window re-attaches.
	detachB := previewDetachRect(frame)
	c.Fill(detachB, ColPanelHi)
	drawToolIcon(c, iconPopOut, detachB, ColText)
	c.Tooltip(detachB, "Pop out into its own window — drag it anywhere, even a second monitor. Close that window to bring it back.")
	// Pin toggle, lit accent while pinned.
	pinB := previewPinRect(frame)
	pinBg := ColPanelHi
	if a.previewIsPinned() {
		pinBg = ColAccent
	}
	c.Fill(pinB, pinBg)
	drawToolIcon(c, iconPin, pinB, ColText)
	c.Tooltip(pinB, "Pin the preview open. This also sets it to stay pinned by default — unpin to turn that off.")
	// Close always draws: unpinned it matches a body click, pinned it is the only exit.
	closeB := previewCloseRect(frame)
	c.Fill(closeB, ColPanelHi)
	c.Label(closeB.X+5, closeB.Y+1, "x", ColText)
	c.Tooltip(closeB, "Close the preview")
	if cycling {
		a.drawPreviewEmoteNav(frame)
	}
}

// previewGripRect is the resize grip's hit rect, shared by the draw and the input handler.
func previewGripRect(frame sdl.Rect) sdl.Rect {
	return sdl.Rect{X: frame.X + frame.W - 16, Y: frame.Y + frame.H - previewCaptionH - 16, W: 16, H: 16}
}

// The preview box's top-right buttons: pop-out, pin and close, with rects shared by the draw and the input handler.
const (
	previewChromeBtn   = int32(18) // square px per chrome button
	previewChromeGap   = int32(2)  // inset from the frame edge, and between buttons
	previewChromeSlots = int32(3)  // pop-out + pin + close (preview-window-wire)
	// previewChromeW is the width the name strip must keep clear of them.
	previewChromeW = previewChromeSlots * (previewChromeBtn + previewChromeGap)
)

// previewCloseRect is the right-most chrome button.
func previewCloseRect(frame sdl.Rect) sdl.Rect {
	return sdl.Rect{X: frame.X + frame.W - previewChromeBtn - previewChromeGap, Y: frame.Y + previewChromeGap, W: previewChromeBtn, H: previewChromeBtn}
}

// previewPinRect is immediately left of the close button.
func previewPinRect(frame sdl.Rect) sdl.Rect {
	r := previewCloseRect(frame)
	r.X -= previewChromeBtn + previewChromeGap
	return r
}

// previewDetachRect is left of the pin button; only clickable attached, since closing the OS window is how to
// re-attach.
func previewDetachRect(frame sdl.Rect) sdl.Rect {
	r := previewPinRect(frame)
	r.X -= previewChromeBtn + previewChromeGap
	return r
}

// previewIsPinned is the box's stays-open state: the session flag, the preference, or detached.
// The detached arm lets followPinnedPreview's three callers retarget the detached window.
func (a *App) previewIsPinned() bool {
	return a.previewPinned || a.d.Prefs.Load().PreviewPinned || a.previewIsDetached()
}

// togglePreviewPin flips both flag and preference; clearing the flag alone would leave a pref-pinned box up.
func (a *App) togglePreviewPin() {
	on := !a.previewIsPinned()
	a.previewPinned = on
	a.d.Prefs.Update(func(sn *config.Settings) { sn.PreviewPinned = on })
}

// dismissPreviewOnClick is the click dismiss for surfaces off the hover corridor; it honours the pin and disarms the
// dwell.
func (a *App) dismissPreviewOnClick() {
	if a.ctx.clicked && !a.previewIsPinned() {
		a.closeSpritePreview()
	}
}

// closeSpritePreviewOnLeave dismisses the hover preview, which opens on a cell but lives elsewhere on screen.
// Until the cursor reaches the box it survives over the cell, box or corridor between; after, leaving it closes it.
// Any click also dismisses.
func (a *App) closeSpritePreviewOnLeave() {
	c := a.ctx
	if a.previewIsPinned() {
		return // pinned: the box stays open until its x is clicked (handlePreviewInput)
	}
	// A gesture owns the box: hovering() reads false while the pointer is blocked, which would close it on the first
	// frame.
	if a.previewDrag || a.previewResize {
		a.previewEntered = true
		return
	}
	if c.clicked {
		a.closeSpritePreview()
		return
	}
	overBox := c.hovering(a.previewFrameRect)
	// hoverID goes stale when the trigger stops drawing, so a bare id would hold close-on-leave open forever.
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
	// Disarm the dwell: with the pointer still on the trigger, the elapsed hoverSince would reopen it next frame.
	a.ctx.hoverID = ""
}

// noteScreenTransition runs once per Frame before handlePreviewInput and, on a screen switch, drops old hover-preview
// state.
// A surviving preview would be an invisible box whose stale rect eats input.
// It also owns lobby-entry auto-refresh below.
func (a *App) noteScreenTransition() {
	if a.screen == a.drawnScreen {
		return // settled frame: one int compare, nothing below runs (the lobby draw is 0-alloc-gated)
	}
	prev := a.drawnScreen
	a.drawnScreen = a.screen
	a.closeSpritePreview() // also clears the trigger id — no cross-screen dwell carry-over
	a.previewFrameRect = sdl.Rect{}
	a.previewTriggerRect = sdl.Rect{}
	// pollPing only runs from drawLobby, so leaving mid-sweep would leave a.pinging stuck true: clear it here, and bump
	// the generation so the sweep's stragglers and late done sentinel are discarded, as connectWith does.
	if prev == ScreenLobby && a.pinging {
		a.pinging = false
		a.pingGen++
	}
	// Entry edge for auto-refresh, catching every path to the lobby here rather than at each a.screen = ScreenLobby.
	// Boot is not an edge (drawnScreen's zero value is ScreenLobby); the constructor's RefreshServers covers launch.
	if a.screen == ScreenLobby {
		// Drain a parked fetch first: otherwise RefreshServers' in-flight check would silently no-op and show the stale
		// list.
		a.pollLobbyFetch()
		if lobbyAutoRefreshDue(a.lastLobbyRefreshAt, a.now()) {
			a.RefreshServers()
		}
	}
}

// unionRect is the smallest rect covering both; a zero-area rect contributes nothing.
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

// previewDragBottomReserve keeps the zoom buttons out of the drag-start region.
const previewDragBottomReserve = 22

// previewFenceLatch is the preview box's per-frame overlay block: the registry depth before publishing its footprint.
// Stored because it is published at the top of the courtroom pass, while the box paints near the bottom.
type previewFenceLatch struct {
	mark int  // registry depth before our publication (0 when nothing was published)
	on   bool // we published — so the draw must suspend it for its own widgets
}

// fenceSpritePreview publishes the box's footprint as an overlay block, so a click on it does not also fire rows
// beneath.
// Call after fenceCompactToolbox: the toolbox paints above the preview, so its mark-scoped suspend must cover this too.
// The footprint is last frame's rect; the box appears after a hover dwell, and a drag blocks the whole pointer instead.
func (a *App) fenceSpritePreview() {
	c := a.ctx
	a.previewFence = previewFenceLatch{mark: c.overlayFenceMark()}
	// No preview up or drawn last frame: nothing is painted there to block.
	if a.previewBase == "" || a.previewFrameRect.W <= 0 || a.previewFrameRect.H <= 0 {
		return
	}
	c.fenceOverlay(a.previewFrameRect)
	a.previewFence.on = true
}
