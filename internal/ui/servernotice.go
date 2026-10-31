package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The server notice box shows a server's own words verbatim and copyable, on any screen: KK, KB, BD and BB.
// AO2 uses a modal too (courtroom.cpp call_notice, the QMessageBox in packet_distribution.cpp).
// On App, not sessionState: resetSessionState rebuilds that on disconnect, wiping the reason before the lobby draws.
// Blocking, unlike the panelSlotTable floatWins, because it must work with no session at all.
// No scrollbar: the body is capped, says so, and Copy hands over the whole message.
// A lockdown's code is on the last line.

// serverNoticeKind is what the notice means, not which packet arrived, so a test can draw it with no session.
type serverNoticeKind int

const (
	serverNoticeNone serverNoticeKind = iota
	// serverNoticeRemoved: the session is gone and the caller already tore down; the box only stops showing.
	serverNoticeRemoved
	// serverNoticeInfo: the connection is fine (BB), and dismissing must leave the session alone.
	serverNoticeInfo
)

// Modal geometry; the arithmetic keeps the box inside config.MinWindowH without a scroll region.
const (
	// serverNoticeW is wide enough that a whitelist URL usually stays on one row, since a wrapped link gets mistyped.
	serverNoticeW = 560
	// serverNoticeMinH stops a one-line notice looking like a dismissable tooltip.
	serverNoticeMinH = 180
	// serverNoticeMaxH leaves the window edges visible at the minimum height.
	serverNoticeMaxH = 420
	// serverNoticeMargin is the least gap to the window edge.
	serverNoticeMargin = 24
	// serverNoticeLineH is one wrapped body row, matching drawFontWarnDialog's step.
	serverNoticeLineH = 20
	// serverNoticeMaxRows caps the body; past this the box says it truncated and points at Copy.
	serverNoticeMaxRows = 14
	// serverNoticeHeadH is the heading band, the same as the sibling modals' first body row.
	serverNoticeHeadH = 48
	// serverNoticeMetaH is the row naming the server; with several tabs open, who said it matters.
	serverNoticeMetaH = 22
	// serverNoticeBtnGap keeps the last body row off the buttons.
	serverNoticeBtnGap = 10
	// serverNoticeBtnW is the width of both buttons.
	serverNoticeBtnW = 110
	// serverNoticeTitleRuneCap bounds the heading: c.Heading is unclipped, so a long title would overflow the panel.
	serverNoticeTitleRuneCap = 96
	// serverNoticeTruncMark marks the cut, since wrapToWidthMeasured truncates silently.
	serverNoticeTruncMark = "… more (use Copy for the whole message)"
	// serverNoticeNoReason stands in when the server gave none, so the box is never empty.
	serverNoticeNoReason = "The server did not give a reason."
	// serverNoticeInfoTitle is neutral: BB carries rules, a MOTD or a whitelist procedure alike.
	serverNoticeInfoTitle = "Notice from the server"
)

type serverNoticeDialog struct {
	open bool
	kind serverNoticeKind
	// title is the client-authored headline, clamped at open to serverNoticeTitleRuneCap.
	title string
	// server is the server name shown in the header.
	server string
	// body is the server's text exactly as copied, after courtroom.capServerText; the draw wraps a view of it.
	body string

	// wrap memoizes the laid-out body so a modal redrawing every frame does not rewrap (paraWrapCache).
	wrap paraWrapCache
}

// openServerNotice raises the box; title must be client-authored and short, and it does not navigate.
// A removal replaces anything showing and info never replaces a removal, so a MOTD popup cannot bury a ban reason.
func (a *App) openServerNotice(kind serverNoticeKind, title, server, body string) {
	if kind == serverNoticeNone {
		return
	}
	if a.serverNoticeDlg.open && kind == serverNoticeInfo && a.serverNoticeDlg.kind == serverNoticeRemoved {
		return
	}
	// Whole-struct assignment, so no field, wrap cache included, survives from a previous notice.
	a.serverNoticeDlg = serverNoticeDialog{
		open: true,
		kind: kind,
		// The attribution is not client-authored: a.serverName comes from the master list with no length limit, and
		// LabelClipped rasterizes the whole string before clipping, so a long name draws nothing.
		title:  clampRunes(title, serverNoticeTitleRuneCap),
		server: clampRunes(server, serverNoticeTitleRuneCap),
		body:   body,
	}
}

func (a *App) closeServerNotice() {
	a.serverNoticeDlg = serverNoticeDialog{}
}

// clampRunes cuts s to at most n runes, marking a cut; runes because a byte cap halves Cyrillic or CJK.
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

// serverNoticeRows reports how many body rows fit in a window h tall, by the same arithmetic drawServerNotice uses.
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

// serverNoticeChromeH is the panel's vertical pixels other than body rows.
func serverNoticeChromeH() int32 {
	return serverNoticeHeadH + serverNoticeMetaH + serverNoticeBtnGap + btnH + pad
}

// serverNoticeBody returns the wrapped body and whether it all fit, rebaking only when an input changed.
func (a *App) serverNoticeBody(width int32, rows int) ([]string, bool) {
	d := &a.serverNoticeDlg
	src := d.body
	if src == "" {
		src = serverNoticeNoReason
	}
	return d.wrap.get(a.chromeWrapMeasure(), a.ctx.fontChainGen, src, width, rows, serverNoticeTruncMark)
}

// chromeWrapMeasure measures a row in the face LabelClipped draws it in, not ctx.TextWidth's base chrome font,
// which may not cover the script. It also triggers the off-thread fallback load (noteScript).
// Built once and kept, because a closure allocates and the wrap cache consults this every frame.
func (a *App) chromeWrapMeasure() func(string) int32 {
	if a.chromeMeasure == nil {
		a.chromeMeasure = func(s string) int32 { return fontWidth(a.ctx.chromeFaceFor(s), s) }
	}
	return a.chromeMeasure
}

// drawServerNotice paints the box at the frame tail with the other blocking modals.
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
	// The floor goes after the window clamp: the buttons measure from the bottom, so a short window or a big UI scale
	// would land them on the heading. Overflowing is the lesser evil, and Esc stays the way out.
	if floor := serverNoticeChromeH() + serverNoticeLineH; mh < floor {
		mh = floor
	}
	my := (h - mh) / 2
	if my < 0 {
		my = 0 // taller than the window: keep the heading and the first rows on screen
	}
	// Centring in a narrow viewport gives negative x, putting Copy out of reach; pin to 0.
	mx := (w - serverNoticeW) / 2
	if mx < 0 {
		mx = 0
	}
	m := sdl.Rect{X: mx, Y: my, W: serverNoticeW, H: mh}

	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	c.Fill(m, ColPanel)
	// The danger color says "you are out", not "the server has news".
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
	// Copy the raw body, since a re-flowed code is useless; an empty body copies nothing, not our stand-in sentence.
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
