package ui

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// directRowLayout is one frame's direct-connect geometry: where each control goes and
// which caption survived. All absolute X in window space.
type directRowLayout struct {
	label          string // "" when even the short caption had to go
	labelX, fieldX int32
	fieldW         int32
	tlsX, saveX    int32
	connX          int32
}

// lobbyDirectRow places the row inside w, shedding width in the order that costs the
// user least: the long caption first, then the input, then the caption entirely. The
// controls' own widths are measured by the caller and passed in, so this stays pure
// and alloc-free — which is what makes the "nothing leaves the window" property a
// unit test rather than a screenshot.
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
	// Hard guarantee for an absurdly narrow window (below MinWindowW the shrink above
	// can still run out of room): pull the tail back onto the window and let the
	// controls overlap rather than disappear — visible beats invisible, the same rule
	// lobbyUtilStop's clamp applies to the cluster.
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

// lobbyUtilWidth is the width every button of the cluster shares at window width w:
// the comfortable default when the whole row fits, otherwise the room that exists
// split evenly, floored at lobbyUtilBtnMinW. Pure + alloc-free, so the "nothing
// leaves the window" property below is unit-pinnable.
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

// lobbyUtilStop is the X of the i-th cluster button counting from the RIGHT edge
// (0 = rightmost, lobbyUtilBtnCount-1 = leftmost). The clamp is the hard guarantee
// that no button can leave the window: the shared-width shrink already makes the
// cluster fit at config.MinWindowW, and this floors the cursor for anything narrower
// still (the width floor means an absurd window overlaps rather than disappears —
// visible beats invisible).
func lobbyUtilStop(w, i int32) int32 {
	btnW := lobbyUtilWidth(w)
	x := w - pad - btnW - i*(btnW+lobbyUtilBtnGap)
	if x < pad {
		return pad
	}
	return x
}

// lobbyDirectRowY and lobbyListTop derive the lobby's two lower rows from the
// chrome band, so drawLobby and its callers read ONE source. Re-deriving the
// list's top as a literal `(topChromeH+pad+56)+40` broke the moment the list's
// action strip pushed it down — a duplicated layout constant fails for the wrong reason.
func lobbyDirectRowY(topChrome int32) int32 { return topChrome + pad + lobbyDirectRowDropY }

func lobbyListTop(topChrome int32) int32 {
	return lobbyDirectRowY(topChrome) + lobbyListActionsDropY + btnH + lobbyListActionsGapY
}

// connErrLabel is the lobby's one-line view of connErr, and it is a VIEW: the full
// reason is never truncated in connErr itself, and a server-authored one now also
// goes to the notice box (servernotice.go), which is where the whole thing —
// newlines, access code, appeal link — is readable and copyable.
// Memoized against connErrSrc because the clamp allocates and this is called from
// the per-frame lobby draw.
func (a *App) connErrLabel() string {
	if a.conn.connErrSrc != a.conn.connErr {
		a.conn.connErrSrc = a.conn.connErr
		s := a.conn.connErr
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			// Mark the cut: a reason that silently stops at its first line reads as
			// the whole message, and the part that matters is usually below it.
			s = strings.TrimRight(s[:i], " \t\r") + " …"
		}
		a.conn.connErrLine = clampLine(s) // logLineMax runes, the shared single-line UI cap
	}
	return a.conn.connErrLine
}

func (a *App) drawLobby(w, h int32) {
	a.pollLobbyFetch()
	a.pollPing() // drain connect-time probes (no-op unless a sweep is running)
	c := a.ctx
	a.drawScreenBackdrop(w, h, "lobbybackground")
	// The lobby's own content starts below the app-chrome band (#14, chrometop.go);
	// hdrY is this screen's header row, and every offset below is relative to it.
	hdrY := a.topChromeH() + pad
	c.Heading(pad, hdrY, "AsyncAO", ColText)
	c.Label(pad, hdrY+30, a.conn.lobby.lobbyStatus, ColTextDim)
	if a.conn.connErr != "" {
		c.Label(pad+220, hdrY+30, a.connErrLabel(), ColDanger)
		if a.conn.lastConnURL != "" { // one-click Reconnect to the server we dropped from
			label := "Reconnect to " + a.conn.lastConnName
			bw := c.TextWidth(label) + 20
			if c.Button(sdl.Rect{X: pad + 220, Y: hdrY + 50, W: bw, H: btnH}, label) {
				a.conn.connErr = ""
				a.Connect(a.conn.lastConnName, a.conn.lastConnURL)
			}
		}
	}
	// M2 auto-reconnect status (when a retry is pending): the cached message
	// (rebuilt per attempt, not per frame) + a Stop button to bail out.
	if !a.conn.autoReconnectAt.IsZero() {
		c.Label(pad+220, hdrY+78, a.conn.autoReconnectMsg, ColAccent)
		sx := pad + 220 + c.TextWidth(a.conn.autoReconnectMsg) + 12
		if c.Button(sdl.Rect{X: sx, Y: hdrY + 74, W: c.TextWidth("Stop") + 20, H: btnH}, "Stop") {
			a.cancelAutoReconnect()
		}
	}

	// Top bar buttons, laid out RIGHT TO LEFT by lobbyUtilStop (stop 0 = rightmost).
	// They used to be pinned by raw `w - <literal> - pad` rects
	// (110/230/350/470/590/710/830); at config.MinWindowW those literals put "What's
	// New" at x=-78 and "Logs" at x=-198, i.e. clean off the left edge of the window.
	utilW := lobbyUtilWidth(w)
	pbBtn := sdl.Rect{X: lobbyUtilStop(w, 0), Y: hdrY, W: utilW, H: btnH}
	// The leftmost of the cluster: its X is what the centred Privacy/Glossary pair
	// below measures its room against.
	utilLeft := lobbyUtilStop(w, lobbyUtilBtnCount-1)
	// Privacy + Glossary + What's New: the three things worth reading before you play —
	// what the server can see, what the AO jargon means, and what changed in this build.
	// They sit CENTRE STAGE in the header (larger + accent-styled) instead of buried in
	// the right-hand utility cluster. Centred in the gap between the title and that
	// cluster; the width shrinks and anchors after the title on a narrow window so the
	// row never overlaps either side.
	// What's New is here rather than in the menu bar because release notes are a LOBBY
	// concern: you read them between sessions, not mid-trial. It previously lived in the
	// Help menu with a green unread dot riding the Help title, which overlapped the
	const (
		helpBtnWMax     = int32(132) // prominent width when there's room (fullscreen)
		helpBtnWMin     = int32(96)  // floor so "What's New" stays readable when squeezed
		helpBtnGap      = int32(12)
		helpBtnCount    = int32(3)
		headerTitleZone = int32(180) // reserved width for the "AsyncAO" heading on the left
	)
	titleRight := pad + headerTitleZone
	// SHED from the right when the zone cannot hold the row even at the minimum
	// width. The width clamp alone is not enough: at the 96 px floor three buttons
	// plus their gaps need 312 px, and a 150% DPI user on a 768 px window has a
	// 206 px logical zone — the row would paint and CLICK over the Phone Book
	// button beside it. Two fitted before the third button was added, which is why
	// this only became reachable now. Same shed-order discipline as the direct-
	// connect row: drop the least essential first.
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
		a.docs.openHelp(1)
	}
	if r, ok := helpSlot(1); ok && c.ButtonCol(r, "Glossary", ColPanel, ColPanelHi, ColAccent, ColAccent) {
		a.prevScreen = ScreenLobby
		a.docs.openHelp(0)
	}
	// What's New sheds FIRST: Privacy and Glossary are what a newcomer needs before
	// they play, and the release notes are also reachable from the changelog the
	// updater offers. It stays in the Extras menu's reach either way.
	if r, ok := helpSlot(2); ok && c.ButtonCol(r, "What's New", ColPanel, ColPanelHi, ColAccent, ColAccent) {
		a.prevScreen = ScreenLobby
		a.screen = ScreenChangelog
		a.markChangelogSeen() // opening the notes is what stamps this build as read
	}
	// Phone Book page toggle: a dedicated view of just YOUR saved servers. Kept on
	// screen (see the cluster comment) because "← All servers" is this page's only
	// way back; the Servers menu carries the same toggle through the same setter.
	pbLabel := "★ Phone Book"
	if a.conn.lobby.phoneBookPage {
		pbLabel = "← All servers"
	}
	if c.Button(pbBtn, pbLabel) {
		a.setPhoneBookPage(!a.conn.lobby.phoneBookPage)
	}
	c.Tooltip(pbBtn, "Your manually-added servers — kept forever (until Wipe everything), exportable")

	dcY := lobbyDirectRowY(a.topChromeH())
	if a.conn.lobby.phoneBookPage {
		a.drawPhoneBookBar(w, dcY)
	} else {
		// Direct connect row, laid out by lobbyDirectRow so nothing walks off the right
		// edge of a config.MinWindowW window (the old `fieldX + 290/390/560` literals
		// put both checkboxes and the whole Connect button outside it).
		const tlsLabel, saveLabel = "TLS (wss)", "Save to phone book"
		row := lobbyDirectRow(w, c.TextWidth(dcLabelFull), c.TextWidth(dcLabelShort),
			c.CheckboxWidth(tlsLabel), c.CheckboxWidth(saveLabel))
		if row.label != "" {
			c.Label(row.labelX, dcY+4, row.label, ColText)
		}
		a.conn.lobby.directInput, _ = c.TextField(directConnectFieldID, sdl.Rect{X: row.fieldX, Y: dcY, W: row.fieldW, H: fieldH}, a.conn.lobby.directInput, "127.0.0.1:50001")
		a.conn.lobby.directSecure = c.Checkbox(row.tlsX, dcY+4, tlsLabel, a.conn.lobby.directSecure)
		a.conn.lobby.directSave = c.Checkbox(row.saveX, dcY+4, saveLabel, a.conn.lobby.directSave)
		if c.Button(sdl.Rect{X: row.connX, Y: dcY, W: dcConnectW, H: btnH}, "Connect") {
			a.directConnect()
		}
	}

	// The server list's own action strip. Refresh and the connect-time sort came
	// back out of the Servers menu by request: they act on the LIST, so a menu
	// carrying them is dead weight the moment you are in a server, and a refresh
	// button belongs beside the thing it refreshes.
	// They sit HERE rather than in the header's utility cluster because that
	// cluster shares its row with the Privacy/Glossary/What's New trio; three
	// buttons there squeezed the trio to nothing at config.MinWindowW, and What's
	listActY := dcY + lobbyListActionsDropY
	refreshLabel := "Refresh"
	if a.conn.lobby.lobbyFetching {
		refreshLabel = "Refreshing…"
	}
	refreshBtn := sdl.Rect{X: pad, Y: listActY, W: lobbyListActionW, H: btnH}
	if c.Button(refreshBtn, refreshLabel) && !a.conn.lobby.lobbyFetching {
		a.RefreshServers()
	}
	c.Tooltip(refreshBtn, "Re-fetch the master server list")

	sortLabel := "Sort: name"
	if a.conn.lobby.pingMode {
		sortLabel = "Sort: speed"
	}
	if a.conn.lobby.pinging {
		sortLabel = "Pinging…"
	}
	sortBtn := sdl.Rect{X: pad + lobbyListActionW + lobbyUtilBtnGap, Y: listActY, W: lobbyListActionW, H: btnH}
	if c.Button(sortBtn, sortLabel) && !a.conn.lobby.pinging {
		a.toggleConnectTimeSort()
	}
	c.Tooltip(sortBtn, "Order the list by how fast each server answers, instead of by name")

	// Server rows. Click once: expand the full description under the
	// row; click the selected row again: join (Join button still works).
	// Wheel scrolls only over the list itself (never the connect row).
	listTop := lobbyListTop(a.topChromeH())
	a.conn.lobby.lobbyScroll -= c.WheelIn(sdl.Rect{X: 0, Y: listTop, W: w, H: h - listTop}) * scrollStepPx
	if a.conn.lobby.lobbyScroll < 0 {
		a.conn.lobby.lobbyScroll = 0
	}
	lineH := int32(c.font.Height()) + 3
	y := listTop - a.conn.lobby.lobbyScroll
	legacyHeaderDrawn := false
	// "Phone Fanat" chip (Phone Book page only): claim its click BEFORE the rows
	// draw — the chip sits over the bottom-right corner where a server row can be,
	// and rows act on c.clicked immediately, so consuming the click here (like
	// classicEditFence) stops an accidental select/connect from a chip press. The
	// chip's visual draws later, on top, in drawPhoneFanatChip.
	if a.conn.lobby.phoneBookPage {
		a.claimPhoneFanatClick(w, h)
	}
	// Snapshot the slice for the whole loop: a ★-click removal (drawServerRow →
	// toggleFavorite) or a phone-book Save (savePhoneBookEdit) re-assigns
	// a.servers = a.mergedFavorites() mid-frame, and a removal SHRINKS it — a
	// bare a.servers[i] on the next iteration would then index past the new
	// length and panic. Ranging the snapshot keeps i valid against the old
	// backing array for the rest of this frame; the stale rows draw once more
	// (standard immediate-mode) and the next frame ranges the fresh list.
	servers := a.conn.lobby.servers
	for i := range servers {
		e := &servers[i]
		if a.conn.lobby.phoneBookPage && !e.Favorite {
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
		if i == a.conn.lobby.selServer && len(a.conn.lobby.descLines) > 0 {
			boxH := int32(len(a.conn.lobby.descLines)+len(a.conn.lobby.descLinks))*lineH + btnH + 16
			if y > listTop-boxH && y < h {
				box := sdl.Rect{X: pad + 16, Y: y, W: w - 2*pad - 16, H: boxH - 4}
				c.Fill(box, ColPanelHi)
				c.Border(box, ColAccent)
				ly := y + 3
				for _, line := range a.conn.lobby.descLines {
					c.LabelClipped(box.X+6, ly, box.W-12, line, ColText)
					ly += lineH
				}
				// URLs from the description: clickable, opens the browser.
				for _, link := range a.conn.lobby.descLinks {
					a.linkLabel(box.X+6, ly, box.W-12, link)
					ly += lineH
				}
				// Rehearse, spelled out (the row button alone was missed
				// in playtests): offline browse of this server's cached
				// roster, or the one-visit explainer. Read the snapshot, not
				// a.servers — a ★-remove earlier this frame may already have
				// shrunk the field slice (see the snapshot note at loop top).
				e := &servers[i]
				if e.Joinable() {
					if c.Button(sdl.Rect{X: box.X + 6, Y: ly + 4, W: 320, H: btnH}, "Rehearse offline (browse cached characters)") {
						if info := a.d.Prefs.Load().ServerWarmInfoFor(e.WebSocketURL()); info.Origin != "" && len(info.Chars) > 0 {
							a.startRehearsal(e.Name, e.WebSocketURL(), info)
							return
						}
						a.conn.connErr = "Rehearsal needs one visit first: join " + e.Name +
							" once (this build) so its roster gets remembered."
					}
				}
			}
			y += boxH
		}
	}

	// Keyboard navigation (#18): arrows move the selection through joinable servers, Enter joins —
	// when no text field is focused. The selection drives the same expand + scroll as a click.
	// NOT while the server-notice box is up. A refused dial lands here with the box open
	// and a.selServer still pointing at the server that just refused us, and the pointer
	// fence (App.Frame) only blanks the MOUSE — fencePointer leaves the keyboard alone.
	// Enter would redial the server whose ban message is the reason the box is on screen,
	// which is the same hazard the quick-connect key is gated for.
	if c.focusID == "" && !a.font.serverNoticeDlg.open {
		// Enter reads c.enter, NOT c.keyPressed, and that is a fix rather than a style
		// choice: HandleEvent answers K_RETURN/K_KP_ENTER with `c.enter = true` and only
		// reaches `c.keyPressed = e.Keysym.Sym` in its DEFAULT arm (ui.go), so keyPressed
		// can never hold Return and the `case sdl.K_RETURN` this replaces was unreachable
		// from a real keystroke. #18's "Enter joins" has been dead since it shipped.
		if c.enter {
			c.enter = false
			if a.conn.lobby.selServer >= 0 && a.conn.lobby.selServer < len(a.conn.lobby.servers) && a.conn.lobby.servers[a.conn.lobby.selServer].Joinable() {
				e := &a.conn.lobby.servers[a.conn.lobby.selServer]
				a.Connect(e.Name, e.WebSocketURL())
				return
			}
		}
		switch c.keyPressed {
		case sdl.K_DOWN:
			if nx := a.conn.lobby.nextJoinableServer(a.conn.lobby.selServer, 1); nx >= 0 {
				a.conn.lobby.selectServerRow(&a.env, nx, w)
				a.conn.lobby.scrollServerIntoView(nx, listTop, h)
			}
			c.keyPressed = 0
		case sdl.K_UP:
			if nx := a.conn.lobby.nextJoinableServer(a.conn.lobby.selServer, -1); nx >= 0 {
				a.conn.lobby.selectServerRow(&a.env, nx, w)
				a.conn.lobby.scrollServerIntoView(nx, listTop, h)
			}
			c.keyPressed = 0
		}
	}

	// "Phone Fanat" chip: a small, dim, always-visible click-to-quip button in the
	// bottom-right of the Phone Book page only. Drawing strictly inside this branch
	// keeps it off the plain all-servers lobby and out of the rest of the app.
	if a.conn.lobby.phoneBookPage {
		a.drawPhoneFanatChip(w, h)
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
	if idx == a.conn.lobby.selServer {
		bg = ColPanelHi
	} else if hover {
		bg = ColPanelHi
	}
	c.Fill(row, bg)

	// Inline edit (phone book only): while this row is being edited, it draws
	// name/address fields + Save/Cancel INSTEAD of the normal row, and returns
	// early — that early return is what suppresses star-remove, click-to-connect
	// and the tooltip for the row under edit. Identity is the stored Favorites
	// URL (== WebSocketURL()); guarded by phoneBookPage && Favorite so it can
	// never trigger on a plain master-list row.
	editing := a.conn.lobby.phoneBookPage && e.Favorite && a.conn.pbEditURL != "" && a.conn.pbEditURL == e.WebSocketURL()
	if editing {
		fy := y + (rowH-2-fieldH)/2
		a.conn.pbEditName, _ = c.TextField("pbedit_name", sdl.Rect{X: row.X + 6, Y: fy, W: 150, H: fieldH}, a.conn.pbEditName, "name")
		a.conn.pbEditAddr, _ = c.TextField("pbedit_url", sdl.Rect{X: row.X + 164, Y: fy, W: 250, H: fieldH}, a.conn.pbEditAddr, "host:port, ws:// or wss://")
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

	// Phone-book edit affordance: a small "Edit" button beside the star, only on
	// the user's own saved rows on the Phone Book page (never on plain lobby
	// rows). It sits between the star and the name, so the name's start-x shifts
	// right in this branch only — the normal lobby row is byte-for-byte unchanged.
	nameX := row.X + 52
	if a.conn.lobby.phoneBookPage && e.Favorite {
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

	// Right-side label: normally the security tier; in ping mode the connect
	// time takes this slot instead (the verbose tier text would run under the
	// number — the tier COLOUR still reads off the left swatch). "…" probing,
	// "unreachable" on a failed dial.
	if a.conn.lobby.pingMode && e.Joinable() {
		label, col := "…", ColTextDim
		if d, ok := a.conn.lobby.pings[e.WebSocketURL()]; ok {
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
		if idx == a.conn.lobby.selServer {
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
				a.conn.connErr = "Rehearsal needs one visit first: join " + e.Name +
					" once (this build) so its roster gets remembered, then Rehearse works offline."
			}
		}
	}

	// Row body click: select-and-expand first, join on the second click.
	if hover && !joinHover && c.clicked {
		if idx == a.conn.lobby.selServer && e.Joinable() {
			a.Connect(e.Name, e.WebSocketURL())
			return
		}
		a.conn.lobby.selectServerRow(&a.env, idx, w) // select + (re)build the cached description lines/links
	}
}

// maxDescLinks bounds the clickable-link rows under a description.
const maxDescLinks = 4

// selectServerRow selects a lobby row and (re)builds its cached description lines + links — shared
// by a row click and keyboard navigation (#18) so both show the same expanded detail.
func (lobby1 *lobbyState) selectServerRow(e *env, idx int, w int32) {
	if idx < 0 || idx >= len(lobby1.servers) {
		return
	}
	lobby1.selServer = idx
	desc := lobby1.servers[idx].Description
	if desc == "" {
		desc = "(no description)"
	}
	lobby1.descLines = e.ctx.WrapText(desc, w-2*pad-40, maxDescLines)
	lobby1.descLinks = extractURLs(desc, maxDescLinks)
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
func (lobby1 *lobbyState) nextJoinableServer(from, dir int) int {
	n := len(lobby1.servers)
	for i := from + dir; i >= 0 && i < n; i += dir {
		e := &lobby1.servers[i]
		if lobby1.phoneBookPage && !e.Favorite {
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
func (lobby1 *lobbyState) scrollServerIntoView(idx int, listTop, h int32) {
	top := int32(0) // content-space offset (pre-scroll) of row idx's top
	legacyDrawn := false
	for i := range lobby1.servers {
		e := &lobby1.servers[i]
		if lobby1.phoneBookPage && !e.Favorite {
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
	if top < lobby1.lobbyScroll {
		lobby1.lobbyScroll = top
	} else if top+rowH > lobby1.lobbyScroll+view {
		lobby1.lobbyScroll = top + rowH - view
	}
	if lobby1.lobbyScroll < 0 {
		lobby1.lobbyScroll = 0
	}
}

// drawWardrobeGrid is the char-select grid over the wardrobe menu: same
// cells, same demand pipeline, same search box. Picking claims the first
// free slot and wears the custom on PV (wearFromMenu).
// It takes the SAME charGridPlan the Characters tab lays out on (#20). The two tabs
// It also takes the same charListRows, so the wardrobe's cell order matches the name
// column's row order exactly — that is what lets a clicked name reveal the right cell.
func (a *App) drawWardrobeGrid(w, h int32, g charGridPlan, rows charListRows, query string) {
	// The open move-to-folder menu resolves its clicks BEFORE the grid draws, so a
	// click on it can never leak through to a cell underneath (drawWardrobeCharsBody's
	// own ordering rule; the menu is painted last, on top).
	if a.inibrowse.iniMenuChar != "" {
		opts := a.iniFolderMenuOpts()
		a.handleIniFolderMenu(iniFolderMenuRect(a.inibrowse.iniMenuAt, len(opts), w, h), opts)
	}
	a.drawWardrobeGridCells(w, h, g, rows, query)
	// A ★ toggle a cell deferred: it rebuilds the slices the loop just ranged, so it
	// runs after the loop, never during it.
	a.applyPendingFav()
	// ...and the menu paints last, above the grid and any sprite preview.
	if a.inibrowse.iniMenuChar != "" {
		opts := a.iniFolderMenuOpts()
		a.drawIniFolderMenu(iniFolderMenuRect(a.inibrowse.iniMenuAt, len(opts), w, h), opts)
	}
}

// drawWardrobeGridCells is drawWardrobeGrid's body: the empty state, the scroll
// bookkeeping and the cell loop. Split out so the host wrapper's after-the-loop work
// runs on EVERY exit — the empty-state arm returns early, and a ★ toggle that emptied
// the list has to be applied on exactly that frame.
func (a *App) drawWardrobeGridCells(w, h int32, g charGridPlan, rows charListRows, query string) {
	c := a.ctx
	if len(a.inibrowse.iniList) == 0 {
		// Anchored on the GRID's viewport, not on the window: under a theme the grid
		// starts wherever char_buttons is (x=353 at 1:1 on the stock table), so an
		// empty-state line drawn from x=pad landed out in the left-hand name column.
		// Classic keeps its old position exactly — there g.view.X is 0 and the width is
		// the whole window.
		msgX, msgW := g.view.X+pad, g.view.W-2*pad
		if !g.themed {
			msgX, msgW = pad, w-2*pad
		}
		switch {
		case a.inibrowse.iniBusy:
			c.LabelClipped(msgX, g.view.Y+8, msgW, "Fetching "+iniswapFileName+"...", ColTextDim)
		case a.inibrowse.iniListErr != "":
			c.LabelClipped(msgX, g.view.Y+8, msgW, a.inibrowse.iniListErr, ColTextDim)
		default:
			c.LabelClipped(msgX, g.view.Y+8, msgW,
				"Wardrobe empty — tap the ★ on any character in the Characters tab to save it here (it stays per server).", ColTextDim)
		}
		return
	}

	// The wardrobe has no taken slots (a folder cannot be occupied by another
	// player), so the shared filter runs with the taken arm permanently open.
	a.inibrowse.iniScroll = a.charGridScroll(w, "iniscroll", g, a.inibrowse.iniScroll, rows.matches(query, true))

	n := int32(0)
	// The theme's hover selector, remembered here and painted after the loop for the
	// same reason the Characters grid does it: AO2 raises char_selector ABOVE every
	// button (aocharbutton.cpp), and immediate mode has no z-order, so a halo drawn
	// inline is overpainted by the next cell whenever the theme's spacing is narrower
	// than the overhang. Both tabs draw on one lattice over one backdrop; leaving the
	// halo off this one made the same cell behave differently depending on which tab
	// happened to be showing.
	hoverCell := sdl.Rect{}
	// Clip to the grid viewport so scrolled cells slide under the fixed top bar
	// (search + tabs + buttons) instead of covering it — same fix as the
	// Characters tab. pushClip (not raw SetClipRect) also sets the INPUT clip, so a
	// cell scrolled half under the bar isn't clickable up there (hovering() honours
	// clipRect) — else clicking the search/tabs picks the hidden character.
	prev, had := c.pushClip(g.view)
	for i := range a.inibrowse.iniList {
		if rows.hidden(i, query, true) {
			continue
		}
		cell := g.cellAt(n, a.inibrowse.iniScroll)
		n++
		if g.visible(cell) {
			if c.hovering(cell) {
				hoverCell = cell
			}
			// Char-select Wardrobe tab: a favourite SWITCHES to the real character
			// (wardrobeClick picks its free slot), not a blind iniswap.
			a.drawIniswapCell(i, cell, cellClickChar, g.caption)
			if i == a.charsel.charListSel {
				c.Border(cell, ColStar) // the name column's selection (charselectwidgets.go)
			}
		}
	}
	if g.themed && hoverCell.W > 0 {
		a.drawCharSelectorHalo(hoverCell, g.haloPx)
	}
	c.popClip(prev, had)
	if a.budget.previewBase != "" {
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
	a.conn.lobby.servers = a.conn.lobby.mergedFavorites(&a.env)
	a.conn.lobby.selServer, a.conn.lobby.descLines = -1, nil // the list just reordered
}

func (a *App) directConnect() {
	url, err := network.ParseDirectAddress(a.conn.lobby.directInput, a.conn.lobby.directSecure)
	if err != nil {
		a.conn.connErr = err.Error()
		return
	}
	if a.conn.lobby.directSave {
		a.d.Prefs.AddFavorite(a.conn.lobby.directInput, url, "")
	}
	a.Connect(a.conn.lobby.directInput, url)
}

// --- CHARACTER SELECT ---------------------------------------------------------------

func (a *App) drawCharSelect(w, h int32) {
	c := a.ctx
	// #20: under a theme this screen is AO2's SECOND fixed-size canvas
	// (charselectlayout.go), so the backdrop is blitted into that canvas rather
	// than stretched across the window — the art and everything laid out on it then
	// share one transform and cannot desync. Off a theme, lay is invalid and this is
	// the pre-#20 full-window backdrop, byte for byte.
	csLay := a.charSelectLayout(w, h)
	a.drawCharSelectBackdrop(w, h, csLay)
	// Both tabs here open sprite previews backed by a char.ini parse (the grid's
	// portrait, the Wardrobe tab's try-before-wear cell), so this screen owns the
	// drain: without it the parse goroutine parks on the one-slot result channel
	// and the portrait never learns the pack has no "normal" emote.
	a.pollPreviewEmotes()
	// Content starts below the app-chrome band (#14, chrometop.go).
	hdrY := a.topChromeH() + pad

	// WHOSE CONTROL ROW IS THIS FRAME'S? (#20, C2.) Under a theme whose canvas can
	// carry them, char select's controls sit at the theme's own rects and AsyncAO
	// draws no header of its own — that is the AO2 screen. When the canvas cannot
	// (no themed grid, or the rects scaled below a usable size, or the theme gives the
	// whole area to char_buttons and leaves nowhere for the name column), the screen
	// keeps the header row it has always had, on top of the theme's backdrop.
	themedTop := a.topChromeH()
	themedUI := csLay.valid && a.charSelectWidgetsThemed(csLay, a.charSelectGridPlan(w, h, themedTop, csLay))
	gridTop := themedTop
	if !themedUI {
		gridTop = hdrY + charSelectHeaderH
	}
	// #20: under a theme this is AO2's grid inside the theme's char_buttons rect, on
	// AO2's own arithmetic (charselectgrid.go); off a theme it is the classic
	// full-window grid, unchanged. BOTH tabs of this screen lay out on this one plan.
	g := a.charSelectGridPlan(w, h, gridTop, csLay)

	// Status text follows this frame's header: under the client row on the classic
	// screen, at the top-left of the grid area when the theme is hosting.
	statusX, statusY := pad, hdrY+40
	if themedUI {
		statusX, statusY = g.view.X, g.view.Y
	}

	if themedUI {
		// The theme's own control row. Drawn ABOVE the session checks below for the
		// same reason the classic Disconnect is: a client stuck handshaking must
		// still be able to leave.
		if a.drawCharSelectControls(csLay, g) {
			return
		}
	} else {
		c.Heading(pad, hdrY, a.charSelectTitle(), ColText)

		// Top-right is the consistent "leave this screen" slot (matches Settings/About/
		// Help/etc.) so muscle memory doesn't land on Disconnect (playtest: char select put
		// Disconnect where Back lives everywhere else, a near-miss waiting to happen). Re-picking a
		// character from the courtroom puts a safe Back there; Disconnect is ALWAYS danger-
		// tinted (red outline + label) and kept out of that spot. Buttons lay out R→L.
		rightX := w - pad
		if a.link.room != nil {
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
		// Privacy: opens the Help screen's Privacy tab so "what can this server see about
		// me?" is one click away before you commit to playing here.
		privW := int32(120)
		rightX -= privW
		if c.Button(sdl.Rect{X: rightX, Y: hdrY, W: privW, H: btnH}, "Privacy") {
			a.prevScreen = ScreenCharSelect
			a.docs.openHelp(1)
		}
	}
	if a.link.sess == nil {
		c.Label(statusX, statusY, "Loading...", ColTextDim)
		return
	}
	if a.link.sess.Phase() != courtroom.PhaseReady {
		c.Label(statusX, statusY, "Handshaking with server...", ColTextDim)
		return
	}

	if !themedUI {
		a.charsel.charSearch, _ = c.TextField("charsearch", sdl.Rect{X: pad, Y: hdrY + 36, W: 230, H: fieldH}, a.charsel.charSearch, "Search...")

		// Grid tabs right of the search: the same grid swaps between the
		// server's list and your wardrobe (favourites + server customs), so
		// joining AS an iniswap is one click from the door.
		tabX := pad + 240
		for _, tb := range charSelectTabs {
			bw := c.TextWidth(tb.label) + 20
			if a.charsel.charTab == tb.id {
				c.Fill(sdl.Rect{X: tabX - 2, Y: hdrY + 34, W: bw + 4, H: btnH + 4}, ColAccent)
			}
			if c.Button(sdl.Rect{X: tabX, Y: hdrY + 36, W: bw, H: btnH}, tb.label) {
				a.selectCharTab(tb.id)
			}
			tabX += bw + 6
		}
		specX := tabX + 8
		if c.Button(sdl.Rect{X: specX, Y: hdrY + 36, W: 90, H: btnH}, "Spectate") {
			if !a.link.sess.Rehearsal {
				a.link.sess.PickCharacter(protocol.UnpairedCharID)
			}
			a.enterCourtroom()
			return
		}
		// (Re-pick "Back" → courtroom now lives in the top-right header slot above, so it
		// matches every other screen instead of sitting mid-row.)
		if a.warnActive() {
			c.LabelClipped(specX+200, hdrY+42, w-specX-200-pad, a.warnLine, ColDanger)
		}
	} else if a.warnActive() {
		// No client header row to hang the warning off: put it along the bottom of the
		// canvas, where no theme in the reference corpus paints a control. Off clip,
		// not area — under Crop or a Custom pan the canvas legitimately runs off the
		// window, and a warning drawn past the bottom edge is a warning nobody sees.
		c.LabelClipped(csLay.clip.X+pad, csLay.clip.Y+csLay.clip.H-charListRowH(a.fontLineH())-pad,
			csLay.clip.W-2*pad, a.warnLine, ColDanger)
	}

	query := a.charsel.charQ.get(a.charsel.charSearch)
	showTaken := a.charSelectShowTaken(themedUI, csLay)

	if a.charsel.charTab == charTabWardrobe {
		rows := charListRows{n: len(a.inibrowse.iniList), names: a.inibrowse.iniList, lower: a.inibrowse.iniLower}
		if themedUI {
			a.drawCharNameList(csLay, rows, query, showTaken, g)
		}
		a.drawWardrobeGrid(w, h, g, rows, query)
		return
	}

	a.ensureCharLower()
	a.ensureWardrobeMembers() // star state for the grid; rebuilt only on change
	rows := charListRows{n: len(a.link.sess.Chars), chars: a.link.sess.Chars, lower: a.charsel.charLower}
	// The name column draws BEFORE the grid: the two never overlap (the widget gate
	// rejects a theme where they would), so this is purely about the selection being
	// settled before the cells are painted with it.
	if themedUI {
		a.drawCharNameList(csLay, rows, query, showTaken, g)
	}
	// Pre-count matches so the scrollbar knows the content height. With no
	// search every slot matches, so skip the scan (it's a per-frame O(n) walk
	// that bites on servers with thousands of characters); the draw loop below
	// still culls to the visible rows either way. Mirrors the bg picker.
	matches := rows.matches(query, showTaken)
	a.charsel.charScroll = a.charGridScroll(w, "charscroll", g, a.charsel.charScroll, matches)

	dlOn := a.d.Prefs.Load().CharDownloaderOn // read once per frame, not per cell
	n := int32(0)
	// AO2 raises char_selector ABOVE every button on hover (aocharbutton.cpp), so the
	// hovered cell is remembered here and its halo painted after the loop — immediate
	// mode has no z-order, and a halo drawn inline would be overpainted by the next
	// cell whenever a theme declares a spacing narrower than the 1 px overhang.
	hoverCell := sdl.Rect{}
	// Clip the grid to its own viewport (below the top bar) so scrolled cells
	// slide UNDER the fixed search/tabs/buttons instead of painting over them: the
	// bar is drawn first, so without this the later cell draws covered it as you
	// scrolled down, and search became unreachable. Keeps the bar always usable.
	prev, had := c.pushClip(g.view)
	for i := range a.link.sess.Chars {
		slot := &a.link.sess.Chars[i]
		if rows.hidden(i, query, showTaken) {
			continue
		}
		cell := g.cellAt(n, a.charsel.charScroll)
		n++
		if g.visible(cell) { // only rows touching the viewport (no draw/hover through the bar)
			if c.hovering(cell) {
				hoverCell = cell
			}
			a.drawCharCell(slot, cell, i, dlOn, g)
			if i == a.charsel.charListSel {
				// The name column's selection, shown where the user is looking. One
				// selection, two views (charselectwidgets.go).
				c.Border(cell, ColStar)
			}
			if c.HoverPreview(a.charHoverID(i), cell) {
				// Parse the char.ini behind the portrait (once per character): "normal"
				// is only a convention, and a pack that spells its poses SNormal/SCry
				// left this box permanently empty without it. previewPortraitAnim picks
				// the first real emote for those packs and keeps "normal" — already
				// probed — for everyone else. // AssetType: CharSprite (preview)
				a.ensurePreviewEmotes(slot.Name, previewEmotePortrait)
				a.setCharPortraitPreview(slot.Name)
			}
		}
	}
	if g.themed && hoverCell.W > 0 {
		a.drawCharSelectorHalo(hoverCell, g.haloPx)
	}
	c.popClip(prev, had)
	if a.budget.previewBase != "" {
		a.drawSpritePreview(w, h, false, "") // character preview: no emote-name caption
		a.closeSpritePreviewOnLeave()
	}
}

func (a *App) drawCharCell(slot *courtroom.CharacterSlot, cell sdl.Rect, idx int, downloaderOn bool, g charGridPlan) {
	c := a.ctx
	c.Fill(cell, ColPanel)
	base := a.link.urls.CharIcon(slot.Name)
	if page, ok := a.cachedPage(&a.rcache.iconPages, &a.rcache.iconPagesGen, len(a.link.sess.Chars), idx, base); ok && len(page.Frames) > 0 {
		// &cell into cgo would heap-allocate the PARAMETER once per VISIBLE CELL per
		// frame — a 7×9 themed grid is 63 of them, and escape analysis is static so
		// the placeholder branch pays it too. Shared scratch (ui.go cgoRect contract:
		// SDL copies the rect during the call and never retains the pointer); the
		// cachedPage lookup above is done, so nothing can interleave.
		c.cgoRect = cell
		_ = c.Ren.Copy(page.Frames[0], nil, &c.cgoRect)
	} else {
		// Not resident: demand it (visible = not speculation) and draw the
		// initials placeholder; the texture pops in live.
		a.demandAsset(&a.rcache.iconAsk, len(a.link.sess.Chars), idx, base, assets.AssetTypeCharIcon) // AssetType: CharIcon
		// AO2 does the same thing when a character ships no char_icon: it clears the
		// stylesheet's border-image and setText()s the character's name into the button
		// (AO2-Client aocharbutton.cpp:55-58). Centred off the CELL, not off iconCell,
		// so a themed cell of any size gets it in the middle.
		initial := slot.Name
		if len(initial) > 2 {
			initial = initial[:2]
		}
		c.Label(cell.X+cell.W/2-8, cell.Y+cell.H/2-8, initial, ColTextDim)
	}
	if slot.Taken {
		// AO2's char_taken is a full-cover 60x60 child of the button, shown at (0,0)
		// (aocharbutton.cpp:14-17, :31-32) — so where the theme ships the art it IS the
		// taken treatment, at the cell's own size. Themes that don't (34 of the 74 in
		// the reference corpus) keep AsyncAO's scrim + label.
		if !g.themed || !a.drawCharOverlayArt(themeStemCharTaken, cell) {
			c.Fill(sdl.Rect{X: cell.X, Y: cell.Y, W: cell.W, H: cell.H}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
			c.Label(cell.X+6, cell.Y+cell.H/2-8, "taken", ColDanger)
		}
	}
	if g.caption {
		c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, slot.Name, ColTextDim)
	} else {
		// On the theme's grid the gutter between rows is the theme's (7 px on the stock
		// table), so a caption would print across the next row's slot frame. AO2 has no
		// caption either — it puts the name on the button as a TOOLTIP
		// (charselect.cpp:304). Registered before the badge/star tooltips below, which
		// override it on their own sub-rects (last write wins).
		c.Tooltip(cell, slot.Name)
	}
	// While this character is the active download, mark the cell.
	if a.audio.dl.active && a.audio.dl.target == slot.Name {
		c.Fill(cell, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 70})
		c.Label(cell.X+4, cell.Y+4, downloadGlyph+"…", ColText)
	}
	// FIXED-SIZE CORNER CHROME, all of it behind one gate (cellFitsCornerChrome).
	// The download badge and the ★ are both AsyncAO affordances drawn at a constant
	// pixel size, so on a downscaled theme cell they stop being corner badges and
	// become most of the character art — and, because each one arms its own
	// hover+click, a click aimed at the character starts a download or favourites it
	// instead. Below the floor they BOTH go: a partial set is how the two ended up
	// disagreeing in the first place. The cell still names itself (the tooltip
	// registered above), and both are one click away at any usable cell size.
	if cellFitsCornerChrome(cell) {
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
		starred := idx >= 0 && idx < len(a.charsel.charLower) && a.charsel.wardrobeMembers[a.charsel.charLower[idx]]
		starR := sdl.Rect{X: cell.X + cell.W - charCellStarPx - charCellStarInsetPx,
			Y: cell.Y + charCellStarInsetPx, W: charCellStarPx, H: charCellStarPx}
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
					a.d.Prefs.RemoveWardrobe(a.srvMeta.serverKey, slot.Name)
				} else {
					a.d.Prefs.AddWardrobe(a.srvMeta.serverKey, slot.Name)
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

// randomChar swaps to a random AVAILABLE character (webAO's /randomchar): a
// uniformly-random untaken slot other than the current one, chosen by reservoir
// sampling so it never allocates. No-op with no free slot. Render thread only.
func (a *App) randomChar() {
	if a.link.sess == nil || len(a.link.sess.Chars) == 0 {
		return
	}
	cur := a.link.sess.MyCharID
	chosen, seen := -1, 0
	for i := range a.link.sess.Chars {
		if i == cur || a.link.sess.Chars[i].Taken {
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
	a.warnLine = clampLine("Random character: " + a.link.sess.Chars[chosen].Name)
	a.warnAt = time.Now()
}

// drawSpritePreview shows the previewed sprite in a bottom-right box (the
// "show the entire thing" pop-up for a hovered/right-clicked icon or emote).
// With cycle set (the wardrobe's try-before-wear), it also draws the ‹ › emote
// navigator and keeps the box alive while the next emote streams in, so the
// controls don't blink. Off the hot path — only drawn when a preview is up.
func (a *App) drawSpritePreview(w, h int32, cycle bool, name string) {
	c := a.ctx
	// Detached (preview-window-wire, requirement 2): the in-app box
	// does not exist AT ALL while the sprite lives in its own OS window —
	// drawing both would show the same pick twice, and this box's own
	// clamp-to-window math (baseX/hiX/baseY/hiY below) is exactly the thing
	// the detached path must never inherit. previewFrameRect is zeroed so
	// handlePreviewInput's wheel/drag hit-testing (which gates on box.W==0)
	if a.previewIsDetached() {
		a.previewFrameRect = sdl.Rect{}
		return
	}
	// The box is an OCCLUDER: fenceSpritePreview published its footprint at the top
	// of the courtroom pass (#37), and that fence would blank the box's OWN − / +
	// zoom buttons and pinned × too. Suspend it for our own widgets, mark-scoped so
	// anything published AFTER us — the compact toolbox strip, which paints above the
	// box — still occludes us (overlayfence.go).
	prevOwner := c.pushOverlayOwner(a.previewOwnerMark())
	defer c.popOverlayOwner(prevOwner)
	page, ok := a.d.Store.Get(a.budget.previewBase)
	ready := ok && len(page.Frames) > 0
	cycling := cycle && len(a.budget.previewAnims) > 1
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
		boxH = int32(a.d.Prefs.Load().PreviewHeightPx())
	}
	pw, ph := boxH, boxH // placeholder box while the next sprite streams in
	if ready {
		// The preview exists to show the sprite — restart its loop per pick so
		// animated idles play instead of freezing on frame 0.
		if a.budget.previewBase != a.budget.previewFor {
			a.budget.previewFor = a.budget.previewBase
			a.budget.previewAt = time.Now()
			a.budget.previewZoom = 1 // a new sprite starts unzoomed
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
	if ready {
		tex := page.Frames[pageFrameLoop(page, a.now().Sub(a.budget.previewAt))]
		if a.budget.previewZoom > 1 {
			// Magnifier: blit a window of the sprite centered on where the
			// cursor sits over the box — move the mouse to pan around it.
			zw, zh := page.W/int32(a.budget.previewZoom), page.H/int32(a.budget.previewZoom)
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
	// The top-right chrome is painted AFTER the sprite blit, not before it.
	// The blit covers `dst` — the frame inset by 4 — which swallows all but a
	// 2 px sliver of both 18 px buttons, and the surfaces that show FULL-BLEED
	// opaque art (the background pickers, bgpicker.go / wardrobe_bg.go) are
	// exactly the ones where a preview is hardest to get rid of. Chrome that is
	// invisible but still clickable is the lie assetmisschip.go's rule forbids,
	// so draw order follows hit order here. Hit-testing lives in
	// handlePreviewInput off previewFrameRect and is unaffected by this move.
	// Emote-name caption (ARM 2): the emote-grid / fav-box preview box names the
	// emote it's showing, so the preview surface reports it too — not just the
	if name != "" && !cycle {
		strip := sdl.Rect{X: frame.X, Y: frame.Y, W: frame.W, H: previewNameStripH}
		c.Fill(strip, sdl.Color{R: 0, G: 0, B: 0, A: 205})
		// The chrome reserve is UNCONDITIONAL now: the pin button is always there,
		// so a name that fit yesterday must not run under it when the box is pinned.
		c.LabelClipped(strip.X+4, strip.Y+3, frame.W-8-previewChromeW, name, ColAccent)
	}
	// Pop-out toggle (preview-window-wire, requirement 1): detaches the
	// sprite into its own OS window, freely movable off AsyncAO's own borders —
	// "somewhere out of the way", the user's own stated workflow. Always drawn
	// plain (never a lit "active" state): this whole box stops drawing the
	// instant it IS detached (the early return above), so a click here always
	// means "detach", never "toggle off" — re-attaching is the OS window's own
	// titlebar X (previewDetachRect's doc).
	detachB := previewDetachRect(frame)
	c.Fill(detachB, ColPanelHi)
	drawToolIcon(c, iconPopOut, detachB, ColText)
	c.Tooltip(detachB, "Pop out into its own window — drag it anywhere, even a second monitor. Close that window to bring it back.")
	// Pin toggle — the discoverable half of the feature. Until now the ONLY way to
	// pin was right-clicking a CLASSIC emote-grid cell, which no AO2 theme even
	// draws. Lit accent while pinned, exactly the compact toolbox's pin-chip idiom.
	pinB := previewPinRect(frame)
	pinBg := ColPanelHi
	if a.previewIsPinned() {
		pinBg = ColAccent
	}
	c.Fill(pinB, pinBg)
	drawToolIcon(c, iconPin, pinB, ColText)
	c.Tooltip(pinB, "Pin the preview open. This also sets it to stay pinned by default — unpin to turn that off.")
	// Close — drawn always, not only when pinned. Unpinned it does exactly what a
	// body click already did, so nothing changes there; pinned it is the only exit.
	closeB := previewCloseRect(frame)
	c.Fill(closeB, ColPanelHi)
	c.Label(closeB.X+5, closeB.Y+1, "x", ColText)
	c.Tooltip(closeB, "Close the preview")
	if cycling {
		a.drawPreviewEmoteNav(frame)
	}
}

// previewGripRect is the resize grip's hit rect: the image corner above the
// caption strip. Shared by the draw and the input handler.
func previewGripRect(frame sdl.Rect) sdl.Rect {
	return sdl.Rect{X: frame.X + frame.W - 16, Y: frame.Y + frame.H - previewCaptionH - 16, W: 16, H: 16}
}

// The preview box's top-right chrome: a pop-out toggle, a pin toggle, and a
// close button. Rects live here rather than at their call sites — the close
// button's rect used to be written literally in the draw AND again in the
// input handler, which is one edit away from a control whose pixels and hit
// box disagree.
const (
	previewChromeBtn   = int32(18) // square px per chrome button
	previewChromeGap   = int32(2)  // inset from the frame edge, and between buttons
	previewChromeSlots = int32(3)  // pop-out + pin + close (preview-window-wire)
	// previewChromeW is the width the name strip must keep clear of them.
	previewChromeW = previewChromeSlots * (previewChromeBtn + previewChromeGap)
)

// previewCloseRect is the right-most chrome slot.
func previewCloseRect(frame sdl.Rect) sdl.Rect {
	return sdl.Rect{X: frame.X + frame.W - previewChromeBtn - previewChromeGap, Y: frame.Y + previewChromeGap, W: previewChromeBtn, H: previewChromeBtn}
}

// previewPinRect is the slot immediately left of the close button.
func previewPinRect(frame sdl.Rect) sdl.Rect {
	r := previewCloseRect(frame)
	r.X -= previewChromeBtn + previewChromeGap
	return r
}

// previewDetachRect is the slot immediately left of the pin button — the
// pop-out-to-its-own-window toggle (preview-window-wire, requirement
// 1). Only ever clickable while ATTACHED: this whole box does not draw at
// all once detached (drawSpritePreview's early return), so there is no
// in-app affordance to re-attach — closing the OS window (its own titlebar
// X) IS the re-attach gesture, and toggleDetachPreview/syncPreviewWindow
// both treat that uniformly with the button's own click.
func previewDetachRect(frame sdl.Rect) sdl.Rect {
	r := previewPinRect(frame)
	r.X -= previewChromeBtn + previewChromeGap
	return r
}

// previewIsPinned is the box's "stays open" state: the per-session latch (set by
// a right-click open, or by this box's own pin button), the sticky preference,
// OR the sprite currently being detached into its own OS window.
func (a *App) previewIsPinned() bool {
	return a.previewPinned || a.d.Prefs.Load().PreviewPinned || a.previewIsDetached()
}

// togglePreviewPin flips BOTH the latch and the preference, so unpinning a box
// that was pinned by the PREF actually unpins it — clearing the latch alone would
// leave the pref holding it up and the button would look broken.
func (a *App) togglePreviewPin() {
	on := !a.previewIsPinned()
	a.previewPinned = on
	a.d.Prefs.Update(func(sn *config.Settings) { sn.PreviewPinned = on })
}

// dismissPreviewOnClick is the click-dismiss shared by the preview surfaces that
// are NOT on the hover corridor (the background pickers, the wardrobe grids, the
// AO2 themed emote grid). They each open with a raw `a.previewBase = ""`, which
// ignored the pin outright AND skipped closeSpritePreview — so the trigger's dwell
// id was never disarmed and the already-elapsed hover re-opened the box on the very
// next frame, the exact trap closeSpritePreview documents.
func (a *App) dismissPreviewOnClick() {
	if a.ctx.clicked && !a.previewIsPinned() {
		a.closeSpritePreview()
	}
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
	if a.previewIsPinned() {
		return // pinned: the box stays open until its x is clicked (handlePreviewInput)
	}
	// A drag/resize in flight OWNS the box, and while one runs the courtroom pass is
	// pointer-blind (boxFencesPointer, #36) — so every hovering() test below reads
	// false and the "used the box, then left it" arm would close the box out from
	// under the gesture on its very first frame. The gesture is itself proof the
	// cursor is on the box; handlePreviewInput resolves its own release.
	if a.previewDrag || a.previewResize {
		a.previewEntered = true
		return
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
	a.budget.previewBase = ""
	a.previewEntered = false
	a.previewPinned = false
	// Disarm the dwell too: closing with the pointer still resting on the
	// trigger (a click-commit) otherwise re-opens NEXT frame — the elapsed
	// hoverSince satisfies the dwell instantly. That silent re-arm is how a
	// char pick carried a "closed" preview into the courtroom (PV lands a few
	// frames after the click) and latched the frame pacer at the active cap.
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
// input/lifecycle hygiene rather than the pacing fix itself. It also owns the
// lobby-entry master-list auto-refresh: this is the one place every screen
// switch funnels through, so the edge fires below.
func (a *App) noteScreenTransition() {
	if a.screen == a.frame.drawnScreen {
		return // settled frame: one int compare, nothing below runs (the lobby draw is 0-alloc-gated)
	}
	prev := a.frame.drawnScreen
	a.frame.drawnScreen = a.screen
	a.closeSpritePreview() // also clears the trigger id — no cross-screen dwell carry-over
	a.previewFrameRect = sdl.Rect{}
	a.previewTriggerRect = sdl.Rect{}
	// Leaving the lobby mid-sweep: the done sentinel is sent non-blocking (a
	// full pingRes drops it) and pollPing only runs from drawLobby — so an
	// undrained sweep would leave a.pinging latched true for the session (dead
	// Ping button, expScreenSkippable stuck false = idle lobby at full rate).
	if prev == ScreenLobby && a.conn.lobby.pinging {
		a.conn.lobby.pinging = false
		a.conn.lobby.pingGen++
	}
	// Lobby-entry auto-refresh: past the early-return we KNOW this frame is a
	// screen switch, so landing on ScreenLobby is the entry edge. Detecting it
	// here — not at the half-dozen `a.screen = ScreenLobby` sites (disconnect,
	// tab close, settings/about/serverhelp back-nav, …) — catches every current
	// AND future entry path with one hook. Purely edge-triggered: no request
	// off-lobby, no polling while sitting on it, and the min-interval cap keeps
	// menu bouncing from hammering the master server (RefreshServers itself
	// dedups an in-flight fetch and re-arms the cap stamp). Boot is not an edge
	// (drawnScreen's zero value IS ScreenLobby): the constructor's
	// RefreshServers covers launch and arms the cap.
	if a.screen == ScreenLobby {
		// Drain any parked fetch result BEFORE the due-check: drawLobby is the
		// only steady-state drain, so leaving the lobby mid-fetch (joining a
		// server, opening Settings) parks the result in the 1-buffer channel
		// with lobbyFetching latched true. Un-drained, a due entry edge would
		// hit RefreshServers' in-flight dedup gate and silently no-op — the
		// whole visit then shows the stale parked list, breaking the cap
		// comment's "past the cap the refresh fires immediately on entry"
		// promise. One empty-channel select when nothing is parked.
		a.pollLobbyFetch()
		if lobbyAutoRefreshDue(a.conn.lobby.lastLobbyRefreshAt, a.now()) {
			a.RefreshServers()
		}
	}
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

// previewFenceLatch is the sprite-preview box's per-frame overlay-fence answer:
// the registry depth taken just BEFORE its footprint was published, handed to
// pushOverlayOwner so the box's own − / + zoom buttons and pinned × stay live
// while everything painted UNDER the box goes inert (overlayfence.go).
type previewFenceLatch struct {
	mark int  // registry depth before our publication (0 when nothing was published)
	on   bool // we published — so the draw must suspend it for its own widgets
}

// fenceSpritePreview publishes the sprite-preview box's painted footprint as an
// overlay fence — issue #37. The box floats over the right-hand column, so with
// no fence a click on it ALSO fired the music row underneath: the box won the
// pixels, the row won the click, and a song started playing. The row draws far
// earlier in the same single pass and has no way to know the box is coming.
func (a *App) fenceSpritePreview() {
	c := a.ctx
	a.previewFence = previewFenceLatch{mark: c.overlayFenceMark()}
	// No preview up, or none drawn last frame (drawSpritePreview zeroes the rect
	// when the sprite isn't ready) — nothing is painted there, and fencing pixels
	// nothing painted is the mirror image of the leak this fixes.
	if a.budget.previewBase == "" || a.previewFrameRect.W <= 0 || a.previewFrameRect.H <= 0 {
		return
	}
	c.fenceOverlay(a.previewFrameRect)
	a.previewFence.on = true
}
