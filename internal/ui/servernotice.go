package ui

import (
	"github.com/veandco/go-sdl2/sdl"
)

// The server notice box: the one modal that shows a server's own words to the
// user, verbatim and copyable, on any screen.
//
// WHY IT EXISTS. Four packets are a server talking directly to the person at the
// keyboard — KK (kick), KB (ban), BD (ban with reason, which is what a lockdown
// sends) and BB (a popup notice). AO2 puts all four in a real modal
// (courtroom.cpp call_notice / the QMessageBox in packet_distribution.cpp). We
// had three different fates for them instead:
//
//   - BB became one OOC line and a toast, so it scrolled away;
//   - KK/KB/BD opened the disconnect dialog ONLY if a room already existed,
//     because handleInvoluntaryDrop gates on a.screen == ScreenCourtroom;
//   - a drop before the courtroom (the common case: the server refuses during
//     the handshake) fell through to a single unclipped label on the lobby.
//
// That last one is the reported bug. A server that is locked down, or that
// whitelists by IP, sends the instructions for getting in — a Discord invite, an
// access code — in that string, and a lockdown message puts the code on the LAST
// line (../Nyathena/internal/athena/lockdown_passkey.go). One label cannot show a
// second line, so the code was structurally unreachable. Worse, LabelClipped is
// not a safety net: it rasterizes the WHOLE string and only clips the blit, so
// past some length the label drew nothing at all.
//
// WHY IT IS ON App, NOT sessionState. resetSessionState rebuilds that struct
// wholesale (tabs.go), on connect AND on disconnect, so a removal reason stored
// there is wiped by the very teardown that produced it — before the lobby draws.
// fontWarnDlg is the existing App-level precedent and says the same thing.
//
// WHY IT BLOCKS. It joins the disconnectDlg / pendingCloseTab family (dimmed
// backdrop, pointer fence, Esc closes), not the panelSlotTable floatWins: those
// are deliberately non-blocking with a live courtroom behind them
// (blockingCourtPopup, floatbox.go), and this has to work with no session at all.
//
// WHY NO SCROLLBAR. The body is capped at serverNoticeMaxRows and says so when it
// cuts. A scroll region here would need WheelIn, which is single-consumer, and a
// clipped click region, which needs pushClip rather than a raw SetClipRect — both
// avoidable, because Copy already hands over the entire uncapped message and that
// is what someone with a code to type actually wants.

// serverNoticeKind is what the notice MEANS, which is the only thing the box
// varies on. It deliberately does not encode which packet arrived: the box is not
// a protocol consumer and must stay drawable from a test with no session.
type serverNoticeKind int

const (
	// serverNoticeNone is the zero value: closed.
	serverNoticeNone serverNoticeKind = iota
	// serverNoticeRemoved means the session is already gone (kick, ban, lockdown,
	// a refused dial). Dismissing it cannot restore anything, so the caller has
	// already torn down and routed; the box only stops showing.
	serverNoticeRemoved
	// serverNoticeInfo means the connection is FINE and the server just said
	// something (BB). Dismissing it must leave the session completely alone.
	serverNoticeInfo
)

// Modal geometry. All named per rule §17.9; the arithmetic below is what keeps
// the box inside the smallest window we allow (config.MinWindowH) without a
// scroll region.
const (
	// serverNoticeW matches the fonts modal's width. Wide enough that a URL in a
	// whitelist notice usually survives on one row, which matters because a
	// wrapped link is a link people mistype.
	serverNoticeW = 560
	// serverNoticeMinH stops a one-line notice from rendering as a tooltip-sized
	// box that reads as dismissable chrome. This IS the message.
	serverNoticeMinH = 180
	// serverNoticeMaxH is the tallest we grow, chosen to leave the window edges
	// visible at the minimum window height so the box never looks like a screen.
	serverNoticeMaxH = 420
	// serverNoticeMargin is the least gap kept to the window edge at any size.
	serverNoticeMargin = 24
	// serverNoticeLineH is one wrapped body row, matching drawFontWarnDialog's
	// step so the two modals have the same rhythm.
	serverNoticeLineH = 20
	// serverNoticeMaxRows caps the body. Past this the box says it truncated and
	// points at Copy.
	serverNoticeMaxRows = 14
	// serverNoticeHeadH is the heading band: the same m.Y+48 the sibling modals
	// use for their first body row.
	serverNoticeHeadH = 48
	// serverNoticeMetaH is the attribution row that names the server. With more
	// than one tab open, "who said this" is half the message.
	serverNoticeMetaH = 22
	// serverNoticeBtnGap keeps the last body row off the buttons.
	serverNoticeBtnGap = 10
	// serverNoticeBtnW is the width of both buttons. Named because the layout gates
	// have to speak in it: "a click 63 px from the window's left edge lands on Copy"
	// is only meaningful as pad + serverNoticeBtnW/2, and the X-clamp gate turns on
	// that point falling inside the button when the panel is pinned to 0 and outside
	// it when the panel is allowed to centre off-screen.
	serverNoticeBtnW = 110
	// serverNoticeTitleRuneCap bounds the HEADING, which draws through c.Heading
	// and is therefore unclipped. The title is client-authored by contract; this
	// is the structural guarantee that a caller who ignores that contract gets a
	// clamped heading rather than a panel-wide overflow.
	serverNoticeTitleRuneCap = 96
	// serverNoticeTruncMark is the visible cut marker. wrapToWidthMeasured
	// truncates SILENTLY, and silence is the bug: the tail of these payloads is
	// the part you need.
	serverNoticeTruncMark = "… more (use Copy for the whole message)"
	// serverNoticeNoReason stands in when the server closed us out and said
	// nothing, so the box is never an empty panel with a button.
	serverNoticeNoReason = "The server did not give a reason."
	// serverNoticeInfoTitle heads a BB popup notice. Deliberately neutral: BB is
	// how servers send rules, a MOTD and a whitelist procedure alike, and we have
	// no way to tell which, so the box must not editorialise.
	serverNoticeInfoTitle = "Notice from the server"
)

// serverNoticeDialog is the modal's state. The zero value is closed.
type serverNoticeDialog struct {
	open bool
	kind serverNoticeKind
	// title is the client-authored headline ("The server banned you."). Clamped at
	// open time to serverNoticeTitleRuneCap.
	title string
	// server is who said it, for the attribution row.
	server string
	// body is the server's text EXACTLY as it will be copied — post-cap
	// (courtroom.capServerText) but otherwise untouched, newlines and all. The
	// draw wraps a view of this; Copy sends this.
	body string

	// wrap memoizes the laid-out body; see paraWrapCache (qol.go) for why a modal
	// that redraws every frame must not rewrap every frame.
	wrap paraWrapCache
}

// openServerNotice raises the box for one server-authored message.
//
// title must be CLIENT-authored and short; body is the server's text and may be
// any length and any script. Only body is ever copied, and only body is wrapped.
//
// The box does NOT navigate. A serverNoticeRemoved notice arrives after the
// caller has already torn the session down and routed to the lobby, so dismissing
// it has nothing to undo; keeping the routing out here is what lets the whole
// modal be driven headlessly by a test with no session, and keeps internal/ui's
// screen machine out of the box's dependencies.
//
// PRECEDENCE: a removal always replaces whatever is showing, and an info notice
// never replaces a removal. A server that spams a MOTD popup as it closes the
// socket must not be able to bury the ban reason underneath it — that reason is
// the only copy of the appeal instructions the user will get.
func (a *App) openServerNotice(kind serverNoticeKind, title, server, body string) {
	if kind == serverNoticeNone {
		return
	}
	if a.serverNoticeDlg.open && kind == serverNoticeInfo && a.serverNoticeDlg.kind == serverNoticeRemoved {
		return
	}
	// Whole-struct assignment, so no field of a previous notice can survive into
	// this one — the wrap cache included. A stale wrapped body under a new title
	// would be a server putting words in another server's mouth.
	a.serverNoticeDlg = serverNoticeDialog{
		open: true,
		kind: kind,
		// Both the title and the attribution are clamped, and the attribution is the
		// one that is NOT client-authored: it is a.serverName, which for a lobby entry
		// came out of the master list's JSON with no per-field length of its own. It
		// draws through LabelClipped, which rasterizes the whole string before clipping
		// the blit — so a long enough server name reproduces the draws-nothing failure
		// on the attribution row of the box built to end it.
		title:  clampRunes(title, serverNoticeTitleRuneCap),
		server: clampRunes(server, serverNoticeTitleRuneCap),
		body:   body,
	}
}

// closeServerNotice dismisses the box and drops the cached wrap with it.
func (a *App) closeServerNotice() {
	a.serverNoticeDlg = serverNoticeDialog{}
}

// clampRunes cuts s to at most n runes, marking a cut. RUNES, not bytes: a
// Cyrillic or CJK heading would lose half its characters to a byte cap.
func clampRunes(s string, n int) string {
	if len(s) <= n { // bytes >= runes, so this clears every short string without decoding
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// serverNoticeRows reports how many body rows fit in a window h tall. Derived
// from the same arithmetic drawServerNotice lays out with, so the wrap budget and
// the drawn area cannot drift apart.
func serverNoticeRows(h int32) int {
	avail := int32(serverNoticeMaxH)
	if lim := h - 2*serverNoticeMargin; lim < avail {
		avail = lim
	}
	rows := int((avail - serverNoticeChromeH()) / serverNoticeLineH)
	if rows > serverNoticeMaxRows {
		rows = serverNoticeMaxRows
	}
	if rows < 1 {
		rows = 1 // a window this small cannot happen above config.MinWindowH; never wrap to nothing
	}
	return rows
}

// serverNoticeChromeH is every vertical pixel the panel spends on something other
// than body rows.
func serverNoticeChromeH() int32 {
	return serverNoticeHeadH + serverNoticeMetaH + serverNoticeBtnGap + btnH + pad
}

// serverNoticeBody returns the wrapped body, whether it all fit, and rebakes the
// cache only when the text, the width, the row budget or the font chain changed.
//
// Measured through chromeFaceFor — the face LabelClipped will actually draw each
// row in — not through the bare ctx font. Measuring in a face that does not cover
// the script and drawing in one that does is the #42 bug class, and it is not
// hypothetical here: the reported notice is Russian.
func (a *App) serverNoticeBody(width int32, rows int) ([]string, bool) {
	d := &a.serverNoticeDlg
	src := d.body
	if src == "" {
		src = serverNoticeNoReason
	}
	return d.wrap.get(a.chromeWrapMeasure(), a.ctx.fontChainGen, src, width, rows, serverNoticeTruncMark)
}

// chromeWrapMeasure is the metric every modal body wrap breaks rows against: the
// width of the row in the face LabelClipped will ACTUALLY draw it in.
//
// Not ctx.TextWidth. That measures the base chrome font, and chromeFaceFor may hand
// the draw a different face entirely for non-Latin text — measuring in a face that
// does not cover the script and drawing in one that does is the #42 bug class, and
// it is not hypothetical here: the notice this was built for is Russian. Going
// through chromeFaceFor also latches the off-thread fallback load (noteScript) for
// free, so the first frame that needs Cyrillic is the frame that asks for it.
//
// Built ONCE and kept. A closure — or a bound method value — allocates, and this is
// handed to a cache that is consulted on every frame a modal is open.
func (a *App) chromeWrapMeasure() func(string) int32 {
	if a.chromeMeasure == nil {
		a.chromeMeasure = func(s string) int32 { return fontWidth(a.ctx.chromeFaceFor(s), s) }
	}
	return a.chromeMeasure
}

// drawServerNotice paints the box. Drawn at the frame tail with the rest of the
// blocking modal family so nothing behind it can be clicked. Off the render hot
// path — only while open.
func (a *App) drawServerNotice(w, h int32) {
	if !a.serverNoticeDlg.open {
		return
	}
	c := a.ctx
	d := &a.serverNoticeDlg

	textW := int32(serverNoticeW) - 2*pad
	rows := serverNoticeRows(h)
	lines, _ := a.serverNoticeBody(textW, rows)

	mh := serverNoticeChromeH() + int32(len(lines))*serverNoticeLineH
	if mh < serverNoticeMinH {
		mh = serverNoticeMinH
	}
	if lim := h - 2*serverNoticeMargin; mh > lim {
		mh = lim
	}
	// THE FLOOR IS ABSOLUTE, and it goes after the window clamp on purpose. The window
	// is resizable and the logical height is the physical one divided by the UI scale, so
	// nothing guarantees the "cannot happen above config.MinWindowH" comment on
	// serverNoticeRows: drag the window short enough (or scale up enough) and the clamp
	// above would shrink the panel below its own chrome, putting the buttons — whose y is
	// measured from the panel's BOTTOM — on top of the heading. Overflowing a window this
	// small is the lesser evil, so the box keeps its shape and Esc stays the way out.
	if floor := serverNoticeChromeH() + serverNoticeLineH; mh < floor {
		mh = floor
	}
	my := (h - mh) / 2
	if my < 0 {
		my = 0 // taller than the window: keep the heading and the first rows on screen
	}
	// X degrades the same way, and for a sharper reason than tidiness: centring a 560 px
	// panel in a narrower logical viewport gives a NEGATIVE x, which puts Copy — the
	// left-anchored button, the one that exists to rescue an access code — off the left
	// edge where no click can reach it. Pinned to 0 the left button always lands, and
	// only the right edge of the panel spills.
	mx := (w - serverNoticeW) / 2
	if mx < 0 {
		mx = 0
	}
	m := sdl.Rect{X: mx, Y: my, W: serverNoticeW, H: mh}

	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	c.Fill(m, ColPanel)
	// A removal is bordered in the danger ink: the one glance-level cue that this
	// is "you are out" and not "the server has news".
	border := ColAccent
	head := ColText
	if d.kind == serverNoticeRemoved {
		border, head = ColDanger, ColDanger
	}
	c.Border(m, border)
	c.Heading(m.X+pad, m.Y+pad, d.title, head)

	if d.server != "" {
		c.LabelClipped(m.X+pad, m.Y+serverNoticeHeadH, textW, d.server, ColAccent)
	}

	y := m.Y + serverNoticeHeadH + serverNoticeMetaH
	bodyLimit := m.Y + mh - btnH - pad - serverNoticeBtnGap
	for _, line := range lines {
		if y+serverNoticeLineH > bodyLimit {
			break // the height clamp above already told the wrap its budget; belt and braces
		}
		c.LabelClipped(m.X+pad, y, textW, line, ColTextDim)
		y += serverNoticeLineH
	}

	by := m.Y + mh - btnH - pad
	// Copy sends the RAW body, not the wrapped view: the whole point is that a
	// truncated or re-flowed access code is useless. An empty body copies nothing
	// rather than copying our own stand-in sentence back at the user.
	if c.Button(sdl.Rect{X: m.X + pad, Y: by, W: serverNoticeBtnW, H: btnH}, "Copy") {
		if d.body != "" {
			_ = sdl.SetClipboardText(d.body)
			a.warnLine = clampLine("Copied the server's message.")
			a.warnAt = a.now()
		}
	}
	label := "Got it"
	if d.kind == serverNoticeRemoved {
		label = "Close"
	}
	if c.Button(sdl.Rect{X: m.X + serverNoticeW - pad - serverNoticeBtnW, Y: by, W: serverNoticeBtnW, H: btnH}, label) {
		a.closeServerNotice()
	}
}
