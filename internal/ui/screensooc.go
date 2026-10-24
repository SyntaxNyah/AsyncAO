package ui

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
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
	target := float64(a.chatlog.icScroll)
	if snap || math.Abs(target-a.chatlog.icScrollVis) < 0.5 {
		a.chatlog.icScrollVis = target
	} else {
		dt := float64(a.chatlog.frameDtMs)
		if dt <= 0 {
			dt = 16
		}
		a.chatlog.icScrollVis += (target - a.chatlog.icScrollVis) * (1 - math.Exp(-dt/logScrollEaseTauMs))
		a.NoteAnimating() // still gliding toward the target: keep frames coming so the ease doesn't freeze mid-scroll at idle=0
	}
	if a.chatlog.icScrollVis < 0 {
		a.chatlog.icScrollVis = 0
	}
	if m := float64(maxScroll); a.chatlog.icScrollVis > m {
		a.chatlog.icScrollVis = m
	}
	return int32(a.chatlog.icScrollVis)
}

// drawICLogList renders the colored IC scrollback (search-filtered,
// word-wrapped to the list width) into rect — used by the classic Log tab
// and the themed ic_chatlog element.
//
// canvasInk is true only when this IS the themed ic_chatlog element, i.e. drawn
// inside the theme's design canvas; see themeink.go for why the flag travels as
// a parameter rather than being read off App.
func (a *App) drawICLogList(list sdl.Rect, canvasInk bool) {
	c := a.ctx
	// See drawPlayerList: the user's pick dresses this panel's chrome too — its
	// search row, its export buttons — not just the scrollback rows.
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemICChatlog, a.logPct)))
	// #21 label 16: "ic_chatlog_color" is the log's DEFAULT ink. Resolved once per
	// frame, outside the row loop.
	icInk := a.elemInkOr(elemICChatlog, canvasInk, ColText)
	// #39: the theme's "ic_chatlog" size/family/bold, folded with the log zoom.
	// icPct is the RESOLVED scale — everything below (the wrap cache key, the
	// per-row pick, the emoji face) must use the same one or the wrap and the draw
	// disagree (the #42 class of bug).
	icPct := a.elemPct(elemICChatlog, a.logPct)
	font := a.elemFont(elemICChatlog, a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	// Wrap against the indented width so a continuation row (drawn at
	// +logWrapIndentPx) can never overflow the column.
	rows := a.icWrapped(wrapW-logWrapIndentPx, a.d.Prefs.Load().ICTimestamps) // per-frame pref read, like the IC counter
	contentH := int32(len(rows)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	// Ctrl+wheel (fine) or wheel-button-held (fast) zooms the log text.
	zoomed := a.zoomWheel(list, &a.logPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	maxScroll := contentH - list.H
	if maxScroll < 0 {
		maxScroll = 0
	}
	// The scroll offset as this frame FOUND it. Anything that moves it before the
	// sticky clamp below is the USER's own gesture (a wheel step, a scrollbar drag or
	// VScrollbar's clamp of a shrunken log); anything the clamp itself moves is the
	// FOLLOW. The two get different easing, so they have to be told apart — see the
	// instant-follow branch below.
	scrollAtEntry := a.chatlog.icScroll
	if !zoomed { // a zoom consumed the wheel — don't also scroll
		if d := c.WheelIn(list); d != 0 {
			a.chatlog.icScroll -= d * scrollStepPx
			a.compose.icStick = a.chatlog.icScroll >= maxScroll // bottom re-sticks, up releases
		}
	}
	before := a.chatlog.icScroll
	a.chatlog.icScroll = c.VScrollbar("icscroll", track, a.chatlog.icScroll, contentH, list.H)
	if a.chatlog.icScroll != before {
		a.compose.icStick = a.chatlog.icScroll >= maxScroll // bar drag follows the same rule
	}
	// AUTO-SCROLL: while stuck, every new line lands in view no matter
	// how many wrapped rows it added.
	if a.compose.icStick {
		// AO2 PARITY, and the default: the follow jump is INSTANT. AO2-Client's log is
		// a QTextEdit whose scrollbar is slammed to maximum() the moment a line is
		// appended — there is no glide — and a message that has already finished
		// playing being eased into view reads as lag. So unless the user opted into
		// "Smooth log scrolling", pin the VISUAL offset to the new bottom here, which
		// makes easeICScroll's "already there" branch take over (no NoteAnimating, so
		// the follow also costs no extra frames at a low idle cap).
		if a.chatlog.icScroll != maxScroll && a.chatlog.icScroll == scrollAtEntry && !a.d.Prefs.Load().SmoothLogScroll {
			a.chatlog.icScrollVis = float64(maxScroll)
		}
		a.chatlog.icScroll = maxScroll
		a.compose.icReadMark = len(a.chatlog.icLog) // caught up to the bottom: everything is read
	}
	if a.compose.icReadMark > len(a.chatlog.icLog) {
		a.compose.icReadMark = len(a.chatlog.icLog) // log was capped/cleared
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
	friendsOn := a.d.Prefs.Load().FriendHighlight // gates the per-line friend glow (read once)
	// Per-speaker name colours (read once): tint each entry's name prefix on its
	// first wrapped row. Short-circuit so the default OFF path adds nothing.
	nameColorsOn := a.d.Prefs.Load().NameColors
	boldNames := a.logRowBoldPref(logSelIC) // read once per frame, passed per line (#39: ic_chatlog_bold)
	var nameSat, nameVal float64
	if nameColorsOn {
		nameSat = float64(a.d.Prefs.Load().NameSat) / 100
		nameVal = float64(a.d.Prefs.Load().NameVal) / 100
	}
	// Glow alpha, computed once per frame: static by default, or a slow breathe
	// when FriendGlowPulse is on (suppressed by reduce-motion). Short-circuit
	// keeps the no-friend default path to a single pref read — no trig, no extra
	// locks. Modulo-into-period keeps the sine argument small for precision.
	friendAlpha := friendTintColor.A
	pulseOn := friendsOn && a.d.Prefs.Load().FriendGlowPulse && !a.d.Prefs.Load().ReduceMotionOn
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
	if a.compose.icReadMark < len(a.chatlog.icLog) {
		for ri := range rows {
			if rows[ri].entry >= a.compose.icReadMark {
				firstUnreadRow = ri
				break
			}
		}
	}
	// Ghost text (ghosttext.go, ON by default): the log is written when a packet
	// ARRIVES and the courtroom says it later, so the log runs ahead of the crawl —
	// rows nobody has spoken yet draw faint instead of spoiling the line. Resolved
	// ONCE per frame, outside the row loop; the zero value ghosts nothing, so with
	// the option off the whole feature costs the draw one comparison.
	ghost := a.ghostLogSpan(rows)
	// Link hover spans the WHOLE message: hovering any wrapped row of a linked
	// entry tints every one of its rows (playtest: only the hovered row lit up,
	// so a wrapped link read as one highlighted line among plain ones). O(1):
	// the hovered row indexes straight into rows; the loop compares entry ids.
	hoverLinkEntry := -1
	if c.hovering(list) && c.mouseX < list.X+wrapW {
		if li := int((c.mouseY - list.Y + visScroll) / lineH); li >= 0 && li < len(rows) {
			if a.chatlog.icLog[rows[li].entry].url != "" {
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
			// The theme's ink substitutes the DEFAULT colour only — an explicit \cN
			// still wins. That is AO2's own order: filter_ic_text bakes every explicit
			// code before the log line is built, and ic_chatlog_color is substituted
			// into the leftover "$c0" placeholder (courtroom.cpp:3929/:3972), so it can
			// never overwrite a colour the message actually asked for.
			col := icInk
			if ecol := a.chatlog.icLog[row.entry].color; ecol > 0 {
				col = render.TextColor(ecol)
			}
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			font := a.elemFontFor(elemICChatlog, a.logPct, row.text)
			// A highlighted friend's line glows (warm tint behind it), gated on
			// the master toggle + the entry flag — a no-friend log is byte-
			// identical to before. A `name=hex` entry recolors the glow per
			// friend; the alpha carries the (optional) pulse.
			if friendsOn && a.chatlog.icLog[row.entry].friend {
				tint := friendTintColor
				if fc := a.chatlog.icLog[row.entry].friendColor; fc >= 0 {
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
			// Find highlight (Ctrl+F): a band behind each matched word, under the
			// text, so a surviving (filtered) row shows WHERE the query matched.
			a.drawLogSearchHighlight(logSelIC, ri, list.X, y, wrapW, lineH, row.text, font)
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
			if nameColorsOn && a.icRowIsEntryStart(ri) {
				lineSpeaker = a.chatlog.icLog[row.entry].speaker
			}
			// Not said yet → faint. AFTER the link-hover override deliberately: a
			// hovered link on an unspoken line should still read as unspoken, and the
			// accent would otherwise shout louder than the lines that HAVE been said.
			// The per-speaker name tint is dropped with it for the same reason — a
			// bright name over ghosted words is the loudest thing on the panel.
			ghostedRow := ghost.ghosted(ri, row.entry)
			if ghostedRow {
				col = ghostInk(col, ColPanel)
				lineSpeaker = ""
			}
			indent := a.logRowIndent(logSelIC, ri) // continuation rows hang right of their first row
			// Inline-colour body (#123): a styled entry draws its body in the
			// authored colours; a ghosted or link-hovered row keeps the single-colour
			// path (its override colour is a transient UI state, not the message).
			if styles := a.chatlog.icLog[row.entry].styles; len(styles) > 0 && row.entry != hoverLinkEntry && !ghostedRow {
				bodyLen := utf8.RuneCountInString(row.text) - row.headRunes
				spans := inlineSpanAt(styles, row.headRunes, row.bodyOff, bodyLen, col)
				a.drawColoredLogRow(font, c.EmojiFont(icPct), list.X+indent, y, wrapW-indent, row.text, spans)
			} else {
				a.drawLogLineNamed(font, c.EmojiFont(icPct), list.X+indent, y, wrapW-indent, row.text, lineSpeaker, col, nameColorsOn, nameSat, nameVal, boldNames)
			}
			if u := a.chatlog.icLog[row.entry].url; u != "" {
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
				a.pinNote(a.chatlog.icLog[row.entry].text)
			}
		}
		y += lineH
	}
	// "N new" pill: while scrolled up, show how many messages arrived since you
	// last caught up. Clicking it jumps to the FIRST unread line (read forward
	// from where you left off); the Jump-logs hotkey still snaps to newest.
	if unread := len(a.chatlog.icLog) - a.compose.icReadMark; unread > 0 && !a.compose.icStick {
		label := fmt.Sprintf("↓ %d new", unread)
		bw := c.TextWidth(label) + 20
		pill := sdl.Rect{X: list.X + (list.W-bw)/2, Y: list.Y + list.H - 26, W: bw, H: 22}
		c.Fill(pill, ColAccent)
		c.Label(pill.X+10, pill.Y+4, label, ColBackground)
		if c.hovering(pill) {
			c.Tooltip(pill, "Left-click: jump to the newest message · right-click: first unread")
			if c.clicked { // left-click → snap straight to the latest message
				a.chatlog.icScroll, a.compose.icStick = maxScroll, true
			} else if c.rightClicked { // right-click → read forward from the first unread
				if firstUnreadRow >= 0 {
					a.chatlog.icScroll = clampI32(int32(firstUnreadRow)*lineH, 0, maxScroll)
					a.compose.icStick = a.chatlog.icScroll >= maxScroll
				} else {
					a.compose.icStick = true // unread rows scrolled off the cap: just go live
				}
			}
		}
	} else if !a.compose.icStick && maxScroll > 0 {
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
				a.chatlog.icScroll, a.compose.icStick = maxScroll, true
			}
		}
	}
	c.popClip(clipPrev, clipHad)
}

// drawOOCLogList renders the OOC scrollback into rect (themed
// server_chatlog element; the classic OOC tab keeps its own copy with the
// identity fields).
func (a *App) drawOOCLogList(list sdl.Rect, canvasInk bool) {
	c := a.ctx
	// See drawPlayerList.
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemServerChatlog, a.oocPct)))
	// #21 label 16: "server_chatlog_color" is this panel's default foreground. AO2
	// reads it through set_font's own key (courtroom.cpp:1199 → :1300); the
	// per-sender tint is a SEPARATE server_chatlog_sender_color read through
	// AOApplication::get_color, and is not in scope here.
	oocInk := a.elemInkOr(elemServerChatlog, canvasInk, ColText)
	// #39: the theme's "server_chatlog" size/family/bold (AO2 folds ms_chatlog in
	// here), folded with the OOC zoom.
	font := a.elemFont(elemServerChatlog, a.oocPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	// Wrap against the indented width so a continuation row (drawn at
	// +logWrapIndentPx) can never overflow the column.
	lines := a.oocWrapped(wrapW - logWrapIndentPx) // MOTDs wrap — never truncate
	nameColorsOn := a.d.Prefs.Load().NameColors    // per-speaker OOC name colours (read once)
	boldNames := a.logRowBoldPref(logSelOOC)       // read once per frame, passed per line
	var nameSat, nameVal float64
	if nameColorsOn {
		nameSat = float64(a.d.Prefs.Load().NameSat) / 100
		nameVal = float64(a.d.Prefs.Load().NameVal) / 100
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
			a.chatlog.oocScroll -= d * scrollStepPx
			a.compose.oocStick = a.chatlog.oocScroll >= maxScroll // bottom re-sticks, up releases
		}
	}
	before := a.chatlog.oocScroll
	a.chatlog.oocScroll = c.VScrollbar("oocscroll", track, a.chatlog.oocScroll, contentH, list.H)
	if a.chatlog.oocScroll != before {
		a.compose.oocStick = a.chatlog.oocScroll >= maxScroll
	}
	if a.compose.oocStick {
		a.chatlog.oocScroll = maxScroll
	}
	// Drag-select / Ctrl+C (before the loop so a real drag swallows the click).
	a.handleLogSelect(logSelOOC, list, a.chatlog.oocScroll, lineH, wrapW)
	// Hovering a link tints THAT link (all of it, across the rows the wrap split
	// it into) — not the whole message, and not its neighbours (#38).
	hoverSpan, hoverID := a.oocHoverSpan(list, a.chatlog.oocScroll, lineH, wrapW, len(lines))
	clipPrev, clipHad := c.pushClip(list) // top/bottom row clipped to the rect, not the tabs
	y := list.Y - a.chatlog.oocScroll
	for li, line := range lines {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			font := a.elemFontFor(elemServerChatlog, a.oocPct, line)
			// Selection highlight sits under the text.
			a.drawLogSelHighlight(logSelOOC, li, list.X, y, wrapW, lineH, line, font)
			sp := ""
			if li < len(a.oocWrapName) {
				sp = a.oocWrapName[li]
			}
			indent := a.logRowIndent(logSelOOC, li) // continuation rows hang right of their first row
			// Links in OOC are openable (click) and copyable (right-click) —
			// matching the IC log. The URL was resolved at wrap time, so one the
			// wrap hard-split still opens whole.
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			a.oocRowLinkActions(hoverSpan, li, rowRect, list.X+indent, y, lineH)
			a.drawOOCLogRow(list, li, font, list.X+indent, y, wrapW-indent, line, sp, hoverID, nameColorsOn, nameSat, nameVal, boldNames, oocInk)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)
}

// oocHoverSpan resolves the cursor to ONE link span of the OOC scrollback: its
// index into a.oocWrapLink and that link's id (-1, -1 = the cursor is not on a
// link). The id is what the draw tints by, so the wrapped pieces of ONE
// hard-split link light up together while a different link on the same row stays
// plain.
// #38: this used to resolve to a RUN OF ROWS sharing the row's single URL, which
// tinted the whole message and made only a paragraph's first link clickable. The
// hit test is now horizontal too — against the span's pixel range, measured at
func (a *App) oocHoverSpan(list sdl.Rect, scroll, lineH, wrapW int32, n int) (int, int32) {
	c := a.ctx
	if lineH <= 0 || !c.hovering(list) || c.mouseX >= list.X+wrapW {
		return -1, -1
	}
	li := int((c.mouseY - list.Y + scroll) / lineH)
	if li < 0 || li >= n {
		return -1, -1
	}
	spans := a.oocRowLinks(li)
	if len(spans) == 0 {
		return -1, -1
	}
	// Spans are stored relative to the row's TEXT origin, so the hanging indent
	// of a wrap-continuation row has to come off the cursor exactly the way the
	// draw adds it — otherwise a continuation row's link would be off by
	// logWrapIndentPx.
	rel := c.mouseX - (list.X + a.logRowIndent(logSelOOC, li))
	for k := range spans {
		if rel >= spans[k].x0 && rel < spans[k].x1 {
			return int(a.oocWrapLinkAt[li]) + k, spans[k].link
		}
	}
	return -1, -1
}

// oocRowLinkActions runs the hover interactions when the hovered span sits on
// display row li: the click/copy target is the LINK's rect (textX is the row's
// text origin — the indent is already in it), while the Jukebox button still
// anchors to the row. A no-op for every other row, so the draw loop can call it
// unconditionally.
func (a *App) oocRowLinkActions(hoverSpan, li int, rowRect sdl.Rect, textX, y, lineH int32) {
	if hoverSpan < 0 || hoverSpan >= len(a.oocWrapLink) {
		return
	}
	s := a.oocWrapLink[hoverSpan]
	if int(s.row) != li {
		return
	}
	a.jukebox.oocLinkActions(rowRect, sdl.Rect{X: textX + s.x0, Y: y, W: s.x1 - s.x0, H: lineH}, s.url)
}

// drawOOCLogRow draws one wrapped OOC row, tinting ONLY the hovered link
// (hoverID < 0 = none) instead of the whole row. The row is drawn whole in each
// pass and the CLIP decides which slice of it lands, so the tinted and plain
// parts share one glyph layout — no re-measure, no seam, and the speaker
// name-split / emoji raster inside drawLogLineNamed keep working untouched. The
// clip keeps the list's vertical bounds so a partially scrolled top/bottom row
// still cuts at the rect edge.
func (a *App) drawOOCLogRow(list sdl.Rect, li int, font *ttf.Font, x, y, textW int32, line, sp string, hoverID int32, nameOn bool, sat, val float64, bold bool, base sdl.Color) {
	c := a.ctx
	ef := a.elemEmoji(elemServerChatlog, a.oocPct) // #39: baseline must match the row's resolved text scale
	spans := a.oocRowLinks(li)
	if hoverID < 0 || len(spans) == 0 {
		a.drawLogLineNamed(font, ef, x, y, textW, line, sp, base, nameOn, sat, val, bold)
		return
	}
	seg := func(lo, hi int32, col sdl.Color) {
		if hi <= lo {
			return
		}
		prev, had := c.pushClip(sdl.Rect{X: x + lo, Y: list.Y, W: hi - lo, H: list.H})
		a.drawLogLineNamed(font, ef, x, y, textW, line, sp, col, nameOn, sat, val, bold)
		c.popClip(prev, had)
	}
	drawn := int32(0)
	for k := range spans {
		if spans[k].link != hoverID {
			continue
		}
		seg(drawn, spans[k].x0, base) // plain text before the link
		seg(spans[k].x0, spans[k].x1, ColAccent)
		drawn = spans[k].x1
	}
	if drawn == 0 { // the hovered link isn't on this row after all
		a.drawLogLineNamed(font, ef, x, y, textW, line, sp, base, nameOn, sat, val, bold)
		return
	}
	seg(drawn, textW, base) // the remainder of the row
}

// oocLinkActions handles the hover interactions for an OOC link: a one-click
// "+ Jukebox" save button at the ROW's right edge (it needs the room), while
// opening / copying is gated on linkRect — the link's own characters — so a
// click on the plain text beside it does nothing (#38). Shared by the courtroom
// OOC log and the OOC tab. Only ever called for the single hovered link.
func (jukebox1 *jukeboxState) oocLinkActions(rowRect, linkRect sdl.Rect, url string) {
	c := jukebox1.env.ctx
	if jukebox1.juke != nil {
		saveBtn := sdl.Rect{X: rowRect.X + rowRect.W - 96, Y: rowRect.Y + 1, W: 94, H: rowRect.H - 2}
		if c.Button(saveBtn, "+ Jukebox") {
			if jukebox1.juke.QuickAdd(jukeboxOOCPlaylist, "", url) {
				jukebox1.env.warnLine = "Saved to Jukebox → " + jukeboxOOCPlaylist
			} else {
				jukebox1.env.warnLine = "Already in your Jukebox (or it's full)"
			}
			jukebox1.env.warnAt = time.Now()
			return
		}
		if c.hovering(saveBtn) {
			return // hovering the button — don't also open/copy the line
		}
	}
	c.Tooltip(linkRect, "Click to open · right-click to copy · or + Jukebox to save: "+url)
	if c.clicked {
		openBrowser(schemeForOpen(url)) // bare "www." link → https:// at open time (copy stays bare, as displayed)
	} else if c.rightClicked {
		_ = sdl.SetClipboardText(url)
		jukebox1.env.warnLine = "Link copied to clipboard"
		jukebox1.env.warnAt = time.Now()
	}
}

// submitOOC sends the OOC input if non-blank (shared classic/themed).
// The name falls back to the sticky AsyncAO<n> — servers reject empty
// OOC names, and commands must always be sendable.
func (a *App) submitOOC() {
	if strings.TrimSpace(a.compose.oocInput) == "" || a.link.sess == nil {
		return
	}
	a.link.sess.SendOOC(a.oocNameOrDefault(), a.compose.oocInput)
	a.recordSentOOC(a.compose.oocInput) // remember the raw line for Up-arrow recall (mirrors IC #8)
	a.compose.oocInput = ""             // clears at send (no echo protocol) — the field's undo history catches it (Ctrl+Z)
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

	// #39: same "server_chatlog" resolution as drawOOCLogList — this is the OTHER
	// copy of the OOC scrollback (the clean right-column box / docked OOC tab), and
	// a fix that lands in only one of the two is a silent miss.
	font := a.elemFont(elemServerChatlog, a.oocPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	// Wrap against the indented width so a continuation row (drawn at
	// +logWrapIndentPx) can never overflow the column.
	lines := a.oocWrapped(wrapW - logWrapIndentPx) // MOTDs wrap — never truncate
	nameColorsOn := a.d.Prefs.Load().NameColors    // per-speaker OOC name colours (read once)
	boldNames := a.logRowBoldPref(logSelOOC)       // read once per frame, passed per line
	var nameSat, nameVal float64
	if nameColorsOn {
		nameSat = float64(a.d.Prefs.Load().NameSat) / 100
		nameVal = float64(a.d.Prefs.Load().NameVal) / 100
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
			a.chatlog.oocScroll -= d * scrollStepPx
			a.compose.oocStick = a.chatlog.oocScroll >= maxScroll // bottom re-sticks, up releases
		}
	}
	before := a.chatlog.oocScroll
	a.chatlog.oocScroll = c.VScrollbar("oocscroll", track, a.chatlog.oocScroll, contentH, list.H)
	if a.chatlog.oocScroll != before {
		a.compose.oocStick = a.chatlog.oocScroll >= maxScroll
	}
	if a.compose.oocStick {
		a.chatlog.oocScroll = maxScroll
	}
	// Drag-select / Ctrl+C + links — same as the themed OOC log (this is the
	// classic OOC tab's own scrollback; it must behave identically).
	a.handleLogSelect(logSelOOC, list, a.chatlog.oocScroll, lineH, wrapW)
	// Hovering a link tints THAT link only — same contract as the themed log (#38).
	hoverSpan, hoverID := a.oocHoverSpan(list, a.chatlog.oocScroll, lineH, wrapW, len(lines))
	clipPrev, clipHad := c.pushClip(list) // scrollback only; restored before the fields below
	y := list.Y - a.chatlog.oocScroll
	for li, line := range lines {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			font := a.elemFontFor(elemServerChatlog, a.oocPct, line)
			a.drawLogSelHighlight(logSelOOC, li, list.X, y, wrapW, lineH, line, font)
			sp := ""
			if li < len(a.oocWrapName) {
				sp = a.oocWrapName[li]
			}
			indent := a.logRowIndent(logSelOOC, li) // continuation rows hang right of their first row
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			a.oocRowLinkActions(hoverSpan, li, rowRect, list.X+indent, y, lineH)
			// ColText, not the theme's server_chatlog_color: this box is the classic
			// docked OOC tab / the Legacy bottom panel, i.e. AsyncAO chrome OUTSIDE any
			// theme's design canvas, and it fills its own ColPanel background rather than
			// drawing over the theme art that colour was authored against (#21 label 16).
			a.drawOOCLogRow(list, li, font, list.X+indent, y, wrapW-indent, line, sp, hoverID, nameColorsOn, nameSat, nameVal, boldNames, ColText)
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
		oocPrimary, oocEmoji := a.icFieldFonts(a.compose.oocInput)
		a.compose.oocInput, sent = c.TextFieldEmoji("oocmsg", sdl.Rect{X: r.X, Y: fy, W: r.W - 4, H: fH}, a.compose.oocInput, "OOC chat… (Enter to send)", oocPrimary, oocEmoji)
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
	// A CJK OOC name must render as real glyphs while typed (same fallback route as
	// the IC input); plain ASCII keeps the single-font fast path (icFieldFonts is nil).
	onPrimary, onEmoji := a.icFieldFonts(a.compose.oocName)
	a.compose.oocName, _ = c.TextFieldEmoji("oocname2", sdl.Rect{X: r.X, Y: fy, W: r.W - 4, H: fH}, a.compose.oocName, "OOC name (this tab)", onPrimary, onEmoji)
}

// areaWrapMaxLines bounds how many lines a single area name or its detail
// line (Issue #22) wraps to before the wrap helper truncates — a named cap
// so one runaway server-supplied area name can't blow a row's height out
// without limit (CLAUDE.md rule: no unbounded growth).
const areaWrapMaxLines = 3

// The area list can be drawn TWICE in one frame, under two different queries:
// the themed canvas copy is filtered by AO2's own music_search field (the
// duplicate-box rule in drawAreaList) while a torn-off Areas panel keeps its own
// "Search areas…" box. Both memos below are therefore indexed by which box the
// query came from. One shared slot would be worse than none: each drawer would
// evict the other's entry every frame, so the O(N) filter scan AND the whole-list
// re-wrap would run twice per frame forever, and the re-wrap allocates.
const (
	areaQuerySlotOwn    = iota // the panel's own field (a.areaSearch)
	areaQuerySlotThemed        // AO2's music_search driving the canvas copy (a.musicSearch)
	areaQuerySlots
)

// areaWrapCache is one memo slot for areaWrapped: the rows plus every input they
// were built from. rows == nil means "never filled" (the [:0] reuse keeps a
// non-nil empty slice once it has been).
type areaWrapCache struct {
	rows  []areaWrapRow
	seq   uint64 // a.areaInfoSeq at build time
	query string
	cardW int32
	pct   int
	gen   int // a.ctx.fontChainGen
}

// areaWrapRow is one ARUP row's word-wrapped display text, cached by
// areaWrapped below so scrolling/resizing the panel doesn't re-wrap every
// area every frame.
type areaWrapRow struct {
	idx         int // ORIGINAL index into a.sess.Areas / a.sess.AreaInfo
	name        string
	detail      string
	nameLines   []string
	detailLines []string
}

// areaWrapLines returns the number of visual lines a row occupies: the
// wrapped name lines plus a detail block that reserves at least one line
// even when empty (matching the pre-#22 fixed two-line row so areas with no
// ARUP detail yet don't visually shrink).
func (row areaWrapRow) lineCount() int32 {
	nameN := int32(len(row.nameLines))
	if nameN < 1 {
		nameN = 1
	}
	detailN := int32(len(row.detailLines))
	if detailN < 1 {
		detailN = 1
	}
	return nameN + detailN
}

// areaWrapped returns the word-wrapped rows for the currently shown
// (possibly search-filtered) areas, rebuilding only when the ARUP state
// (areaInfoSeq), search query, card width, zoom, or font chain changed —
// never per frame (mirrors icWrapped in qol.go).
func (a *App) areaWrapped(font *ttf.Font, cardW int32, query string, slot int) []areaWrapRow {
	m := &a.chatlog.areaWrap[slot]
	// #39: key on the RESOLVED scale (theme "area_list" size × the area zoom), not
	// the raw zoom — a theme swap that only changes the point size must re-wrap.
	pct := a.elemPct(elemAreaList, a.areaPct)
	if m.rows != nil && m.seq == a.areaTab.areaInfoSeq && m.query == query &&
		m.cardW == cardW && m.pct == pct && m.gen == a.ctx.fontChainGen {
		return m.rows
	}
	shown := len(a.link.sess.Areas)
	var filtered []int
	if query != "" {
		filtered = a.refreshAreaFilter(query, slot)
		shown = len(filtered)
	}
	nameW, detailW := cardW-12, cardW-24
	out := m.rows[:0]
	for vi := 0; vi < shown; vi++ {
		i := vi
		if query != "" {
			i = filtered[vi]
		}
		if i < 0 || i >= len(a.link.sess.Areas) {
			continue
		}
		area := a.link.sess.Areas[i]
		detail := ""
		if i < len(a.link.sess.AreaInfo) {
			info := &a.link.sess.AreaInfo[i]
			if info.Players >= 0 {
				unit := "users"
				if info.Players == 1 {
					unit = "user"
				}
				detail = fmt.Sprintf("%d %s", info.Players, unit)
			}
			detail += arupStatusText(info) // status/lock/CM — shared with the Players tab, see arupStatusText
		}
		out = append(out, areaWrapRow{
			idx:         i,
			name:        area,
			detail:      detail,
			nameLines:   wrapToWidth(constFont(font), area, nameW, areaWrapMaxLines),
			detailLines: wrapToWidth(constFont(font), detail, detailW, areaWrapMaxLines),
		})
	}
	m.rows, m.seq, m.query, m.cardW, m.pct, m.gen = out, a.areaTab.areaInfoSeq, query, cardW, pct, a.ctx.fontChainGen
	return m.rows
}

// arupStatusText renders one area's live ARUP status/lock/CM fields
// (courtroom.AreaInfo, filled field-by-field 0=players/1=status/2=CM/3=lock by
// the ARUP case in session.go, matching AO2-Client's arup_modify) as a
// "  ·  STATUS  ·  locked  ·  CM: Phoenix"-style suffix: one "  ·  "-joined
// segment per present field, in ARUP field order, and "" (not even a stray
// separator) when nothing has been reported at all — info's fields all sit at
// their "unknown" zero value, or info itself is nil (the area index is out of
// range, e.g. a partial snapshot mid-connect).
// LFP/CASING/RP/GAMING/RECESS 5-way split (courtroom.cpp:1858-1877) as a
func arupStatusText(info *courtroom.AreaInfo) string {
	if info == nil {
		return ""
	}
	detail := ""
	if info.Status != "" {
		detail += "  ·  " + info.Status
	}
	switch strings.ToUpper(info.Lock) {
	case "LOCKED":
		detail += "  ·  locked"
	case "SPECTATABLE":
		detail += "  ·  spectatable"
	}
	if info.CM != "" && !strings.EqualFold(info.CM, "FREE") {
		detail += "  ·  CM: " + info.CM
	}
	return detail
}

// areaCountRowH / areaCountRowLabelOffY size the bare "shown / total" line that
// replaces the whole search row when the theme's own music_search field is driving
// the filter (drawAreaList). One dim text line plus a hair of breathing space, and
// the label sits at its optical centre — the rest of the old row's height goes back
// to the list, which is the point of dropping the duplicate box.
const (
	areaCountRowH         = int32(20)
	areaCountRowLabelOffY = int32(3)
)

// themedListSearchDrew reports whether the themed courtroom painted AO2's own
// `music_search` field this frame, i.e. whether the list panel already has a search
// box above it.
func (a *App) themedListSearchDrew() bool {
	if a.classic.panelHidden(panelLog) {
		return false
	}
	_, ok := a.themeLay.rect("music_search")
	return ok
}

// areaRowHeaderPad is the fixed slack added on top of the two text rows of an
// area card (the top inset + the gap between the name and detail line + the
// bottom margin). Named so the row-height math is a single, testable formula
// that stays in lock-step with the label offsets inside drawAreaList.
const areaRowHeaderPad = int32(11)

// areaRowLineHN is the area row height generalized to an arbitrary visual line
// count, for rows whose name and/or detail text wrapped to more than one
// line each (Issue #22 — ARUP area names/details previously never wrapped
// and just clipped off the panel edge).
func areaRowLineHN(fontH, lines int32) int32 {
	return fontH*lines + areaRowHeaderPad
}

// drawAreaList lists the server's areas; clicking one requests the room
// swap. AO area transfers ride the MC packet with the area name in place
// of a track (AO2-Client sends areas from the same list — the courtroom's
// isAreaTransfer filter keeps them out of the audio path client-side).
// canvasInk is true only inside the design canvas — the Areas view of the themed
// music_list panel (theme_layout.go), the one call site that draws on the theme's
// own art. The classic panel and a torn-off Areas tab pass false (themeink.go).
func (a *App) drawAreaList(r sdl.Rect, canvasInk bool) {
	c := a.ctx
	if a.areaPct < config.MinLogScalePercent { // uninit / stale → match the log
		a.areaPct = a.logPct
	}
	// See drawPlayerList. AFTER the zoom is settled, so the face is opened at the
	// scale this frame actually draws at.
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemAreaList, a.areaPct)))
	// #21 label 16: "area_list_color" replaces only the NEUTRAL row inks — the
	// population dim/full pair and the detail block. AsyncAO's status, locked,
	// current and hover colours stay: they are this client's status affordance, and
	// AO2's own analogue of them is the row BACKGROUND brush
	// (courtroom.cpp:1851-1882), not the text colour, so the theme's single
	// foreground key has no claim on them.
	areaIdleCol := a.elemInkOr(elemAreaList, canvasInk, ColTextDim)
	areaLiveCol := a.elemInkOr(elemAreaList, canvasInk, ColText)
	// Ctrl+wheel (or wheel-button) resizes the area text (areaPct). Lives HERE
	// — not just the classic log panel — so it also works on the TORN tab and the
	// THEMED courtroom, both of which draw drawAreaList directly. In the classic
	// panel the outer zoomWheel already took the wheel (wheelTaken), so this
	// no-ops there; no double-step.
	a.zoomWheel(r, &a.areaPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	if len(a.link.sess.Areas) == 0 {
		c.Label(r.X+4, r.Y+4, "Server reported no areas.", ColTextDim)
		return
	}
	// Recent areas (M3): one-click jump-back chips for the areas you've passed
	// through (newest first; index 0 is the current area, so skip it). One row.
	if len(a.roster.areaHistory) > 1 {
		c.Label(r.X+2, r.Y+5, "Recent:", ColTextDim)
		cx := r.X + 2 + c.TextWidth("Recent:") + 8
		for _, name := range a.roster.areaHistory[1:] {
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
	searchExternal := canvasInk && a.themedListSearchDrew()
	// The header row is SPLIT (areamenu.go), not laid out offset by offset: the Areas
	// view's ⋮ goes at the right end and the search field and the counter are measured
	// back from its left edge, so they shrink to fit it instead of drawing underneath
	// it (the overlap class the roster toolbar was rebuilt to stop, on a theme's 212 px
	// panel). Only THIS ROW is split — the cards and the scrollbar below keep the full
	// panel. `canvasInk` is the same answer rosterMenuThemed derives: this drawer is
	// inside the theme's design canvas exactly when it is true.
	head := splitAreaHeaderRow(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: fieldH})
	a.drawAreaMenuButton(head.button, canvasInk)
	var query string
	// Both memos below are indexed by which box the query came from, so this
	// drawer and a torn-off Areas panel filtered by the other box can share a
	// frame without evicting each other (areaQuerySlots).
	slot := areaQuerySlotOwn
	if searchExternal {
		slot = areaQuerySlotThemed
		query = strings.ToLower(strings.TrimSpace(a.musicTab.musicSearch))
	} else {
		// head.field, never a rect built here: the inset, the counter's room and the
		// clearance from the ⋮ are one split (splitAreaHeaderRow), so the box cannot
		// drift out from under the rule that keeps this row's three occupants apart.
		a.areaTab.areaSearch, _ = c.TextField("areasearch", head.field, a.areaTab.areaSearch, "Search areas…")
		query = strings.ToLower(strings.TrimSpace(a.areaTab.areaSearch))
	}
	total := len(a.link.sess.Areas)
	shown := total
	if query != "" {
		shown = len(a.refreshAreaFilter(query, slot))
	}
	if searchExternal {
		// The count keeps its own (much shorter) line, left-aligned on the card column:
		// it is the only thing left on this row, and right-aligning a lone "3 / 3"
		// against a panel edge reads as a stray number.
		c.Label(r.X+2, r.Y+areaCountRowLabelOffY, a.areaTab.areaCount.text(shown, total), ColTextDim)
		r.Y += areaCountRowH
		r.H -= areaCountRowH
	} else {
		// Same split as the field above — the counter sits in the room the ⋮ left it.
		c.Label(head.count.X, head.count.Y, a.areaTab.areaCount.text(shown, total), ColTextDim)
		r.Y += fieldH + 6
		r.H -= fieldH + 6
	}
	if !c.ctrlHeld { // ctrl+wheel resizes text, never scrolls
		a.areaTab.areaScroll -= c.WheelIn(r) * scrollStepPx
	}
	// #39: the theme's "area_list" size/family, folded with the area zoom.
	font := a.elemFont(elemAreaList, a.areaPct) // area-list zoom, independent of the IC log
	areaBold := a.elemBold(elemAreaList)        // area_list_bold → the row's real bold cut
	subH := int32(font.Height())
	cardW := r.W - scrollBarW - scrollBarGap - 4
	rows := a.areaWrapped(font, cardW, query, slot) // word-wrapped name/detail text (Issue #22), cached
	contentH := int32(0)
	for _, row := range rows {
		contentH += areaRowLineHN(subH, row.lineCount())
	}
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.areaTab.areaScroll = c.VScrollbar("areascroll", track, a.areaTab.areaScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r) // partial top/bottom row stays inside the panel
	defer c.popClip(clipPrev, clipHad)
	y := r.Y - a.areaTab.areaScroll
	for _, row := range rows {
		rowH := areaRowLineHN(subH, row.lineCount())
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-rowH {
			card := sdl.Rect{X: r.X + 2, Y: y + 2, W: cardW, H: rowH - areaCardGap}
			hover := c.hovering(card)
			current := row.name == a.srvMeta.curArea
			// Two-block entry: the name (population-coloured) on top, an indented
			// detail block (count / status / lock / CM) under it, each independently
			// word-wrapped (Issue #22). A locked room tints the whole row red, the
			// area you're IN is accent. Fixes "it's all gray, I can't tell what's
			// busy or where I am / every area looked the same".
			nameCol := areaIdleCol
			locked := false
			if row.idx < len(a.link.sess.AreaInfo) {
				info := &a.link.sess.AreaInfo[row.idx]
				if info.Players > 0 {
					nameCol = areaLiveCol // populated reads at full strength
				}
				if info.Status != "" && strings.ToUpper(info.Status) != "IDLE" {
					nameCol = areaStatusColor(info.Status) // a real status colours it; IDLE keeps the population colour
				}
				locked = strings.EqualFold(info.Lock, "LOCKED")
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
			ny := card.Y + 3
			for _, line := range row.nameLines {
				c.LabelClippedFontWeight(font, card.X+6, ny, card.W-12, line, nameCol, areaBold)
				ny += subH
			}
			if len(row.nameLines) == 0 { // empty area name (shouldn't happen, but keep the block reserved)
				ny += subH
			}
			dy := ny + 2
			for _, line := range row.detailLines {
				c.LabelClippedFontWeight(font, card.X+18, dy, card.W-24, line, areaIdleCol, areaBold)
				dy += subH
			}
			if c.ClickedIn(card) { // press+release in-card: a drag-in release must not transfer areas
				a.jumpToArea(row.name) // consolidated: scrollback swap + curArea + presence + MC + warn line
			}
		}
		y += rowH
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
func (a *App) wardrobeFolders() []string {
	var out []string
	seen := map[string]struct{}{}
	for _, f := range a.inibrowse.iniFolders {
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
