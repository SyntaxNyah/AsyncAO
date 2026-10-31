package ui

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// logSearchRowH sizes the search/export row to the chrome font, floored at 24px.
func logSearchRowH(fontH int32) int32 {
	if h := fontH + logSearchRowPadV; h > logSearchRowMinH {
		return h
	}
	return logSearchRowMinH
}

// unreadDividerH is the thickness of the accent line at the first unread IC line.
const unreadDividerH = 2

// friendTintColor is the translucent glow behind a highlighted friend's IC line; alpha is the glow strength.
// A `name=RRGGBB` friends entry overrides the R/G/B per friend.
var friendTintColor = sdl.Color{R: 255, G: 210, B: 90, A: 64}

const (
	// friendGlowMinAlpha is the dim floor the pulsing glow breathes down to (FriendGlowPulse only).
	friendGlowMinAlpha = 20
	// friendGlowPulsePeriod is one full breathe cycle of the friend glow.
	friendGlowPulsePeriod = 1600 * time.Millisecond
)

// logFastZoomMultiple scales the zoom step while the middle (wheel) button is held.
const logFastZoomMultiple = 5

// zoomWheel resizes *pct (clamped to [min,max]) on wheel over rect: Ctrl = one step, middle held = fast.
// Returns true if it consumed the wheel, so the caller skips scrolling.
func (a *App) zoomWheel(rect sdl.Rect, pct *int, min, max int) bool {
	c := a.ctx
	if c.wheelTaken || c.wheelY == 0 || !c.hovering(rect) || (!c.ctrlHeld && !c.middleHeld) {
		return false
	}
	step := config.ScaleStepPercent
	if c.middleHeld {
		step *= logFastZoomMultiple
	}
	*pct = value.Clamp(*pct+int(c.wheelY)*step, min, max)
	a.saveLayout()
	c.wheelTaken = true
	return true
}

// logScrollEaseTauMs is the smooth-scroll time constant: the glide covers ~63% of the remaining distance per tau.
const logScrollEaseTauMs = 50.0

// easeICScroll moves the on-screen offset toward a.icScroll and returns it clamped; snap (scrollbar drag) pins it 1:1.
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

// drawICLogList renders the colored IC scrollback into rect (classic Log tab and themed ic_chatlog element).
// canvasInk is true only for the themed ic_chatlog element, drawn inside the theme's design canvas (themeink.go).
func (a *App) drawICLogList(list sdl.Rect, canvasInk bool) {
	c := a.ctx
	// See drawPlayerList: the user's pick also dresses this panel's chrome.
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemICChatlog, a.logPct)))
	// ic_chatlog_color is the log's default ink; resolved once per frame.
	icInk := a.elemInkOr(elemICChatlog, canvasInk, ColText)
	// The theme's ic_chatlog size/family/bold folded with the log zoom; wrap key, row pick and emoji face must use
	// the same icPct or wrap and draw disagree.
	icPct := a.elemPct(elemICChatlog, a.logPct)
	font := a.elemFont(elemICChatlog, a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	// Wrap against the indented width so continuation rows (drawn at +logWrapIndentPx) cannot overflow.
	rows := a.icWrapped(wrapW-logWrapIndentPx, a.d.Prefs.Load().ICTimestamps) // per-frame pref read, like the IC counter
	contentH := int32(len(rows)) * lineH
	track := sdl.Rect{X: list.X + list.W - scrollBarW, Y: list.Y, W: scrollBarW, H: list.H}
	// Ctrl+wheel (fine) or wheel-button-held (fast) zooms the log text.
	zoomed := a.zoomWheel(list, &a.logPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	maxScroll := contentH - list.H
	if maxScroll < 0 {
		maxScroll = 0
	}
	// Offset as found this frame: moves before the sticky clamp are the user's own gesture, moves by the clamp are
	// the follow. They ease differently (see the instant-follow branch).
	scrollAtEntry := a.icScroll
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
	// Auto-scroll: while stuck, every new line lands in view.
	if a.icStick {
		// AO2 parity: the follow jump is instant (AO2-Client's log pins the scrollbar to maximum() on append) unless
		// Smooth log scrolling is on. Only when the log grew this frame and the user did not move the offset first.
		if a.icScroll != maxScroll && a.icScroll == scrollAtEntry && !a.d.Prefs.Load().SmoothLogScroll {
			a.icScrollVis = float64(maxScroll)
		}
		a.icScroll = maxScroll
		a.icReadMark = len(a.icLog) // caught up to the bottom: everything is read
	}
	if a.icReadMark > len(a.icLog) {
		a.icReadMark = len(a.icLog) // log was capped/cleared
	}
	// Smooth scrolling: the on-screen offset eases toward the target; a scrollbar drag snaps 1:1.
	// Selection hit-math uses the same visual offset.
	visScroll := a.easeICScroll(maxScroll, c.dragID == "icscroll")
	// Drag-select / Ctrl+C (before the loop so a real drag swallows the click).
	a.handleLogSelect(logSelIC, list, visScroll, lineH, wrapW)
	// Pin-to-notes chord (default Ctrl+N) pins the hovered line; resolved once, handleHotkeys leaves an
	// unrecognized chord in c.hotkey.
	pinChord := c.hotkey != 0 && strings.EqualFold(sdl.GetKeyName(c.hotkey), a.hotkeyFor(hotkeyPinNote))
	friendsOn := a.d.Prefs.Load().FriendHighlight // gates the per-line friend glow (read once)
	// Per-speaker name colours, read once: tint the name prefix on an entry's first wrapped row.
	nameColorsOn := a.d.Prefs.Load().NameColors
	boldNames := a.logRowBoldPref(logSelIC) // read once per frame, passed per line (#39: ic_chatlog_bold)
	var nameSat, nameVal float64
	if nameColorsOn {
		nameSat = float64(a.d.Prefs.Load().NameSat) / 100
		nameVal = float64(a.d.Prefs.Load().NameVal) / 100
	}
	// Glow alpha, computed once per frame: static, or a slow breathe with FriendGlowPulse (off under reduce-motion).
	friendAlpha := friendTintColor.A
	pulseOn := friendsOn && a.d.Prefs.Load().FriendGlowPulse && !a.d.Prefs.Load().ReduceMotionOn
	if pulseOn {
		period := int64(friendGlowPulsePeriod)
		phase := math.Sin(float64(a.now().UnixNano()%period) / float64(period) * 2 * math.Pi)
		lo, hi := float64(friendGlowMinAlpha), float64(friendTintColor.A)
		friendAlpha = uint8(lo + (hi-lo)*(phase*0.5+0.5)) // [-1,1] -> [floor, full]
	}
	// First wrapped row of the first unread entry (jump target and divider); scanned only while unread.
	firstUnreadRow := -1
	if a.icReadMark < len(a.icLog) {
		for ri := range rows {
			if rows[ri].entry >= a.icReadMark {
				firstUnreadRow = ri
				break
			}
		}
	}
	// Ghost text (ghosttext.go): the log runs ahead of the crawl, so rows not yet spoken draw faint.
	// Resolved once per frame; the zero value ghosts nothing.
	ghost := a.ghostLogSpan(rows)
	// Link hover spans the whole message: hovering any wrapped row tints every row of that entry.
	hoverLinkEntry := -1
	if c.hovering(list) && c.mouseX < list.X+wrapW {
		if li := int((c.mouseY - list.Y + visScroll) / lineH); li >= 0 && li < len(rows) {
			if a.icLog[rows[li].entry].url != "" {
				hoverLinkEntry = rows[li].entry
			}
		}
	}
	// Scissor to the list rect so a partially scrolled row cannot draw onto the tab strip above.
	clipPrev, clipHad := c.pushClip(list)
	y := list.Y - visScroll
	for ri := range rows {
		if y > list.Y+list.H-lineH {
			break
		}
		if y >= list.Y-lineH {
			row := &rows[ri]
			// The theme ink replaces only the default colour; an explicit \cN wins (AO2 bakes codes before substituting
			// ic_chatlog_color into the leftover "$c0", courtroom.cpp:3929/:3972).
			col := icInk
			if ecol := a.icLog[row.entry].color; ecol > 0 {
				col = render.TextColor(ecol)
			}
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			font := a.elemFontFor(elemICChatlog, a.logPct, row.text)
			// A highlighted friend's line glows; a `name=hex` entry recolors it per friend; alpha carries the pulse.
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
			// Find highlight (Ctrl+F): a band behind each matched word.
			a.drawLogSearchHighlight(logSelIC, ri, list.X, y, wrapW, lineH, row.text, font)
			// Unread divider: an accent rule at the top of the first unread line.
			if ri == firstUnreadRow {
				c.Fill(sdl.Rect{X: list.X, Y: y, W: wrapW, H: unreadDividerH}, ColAccent)
			}
			// A message with a link tints on hover (every wrapped row) and opens on click.
			if row.entry == hoverLinkEntry {
				col = ColAccent
			}
			// Per-speaker name colour on an entry's first wrapped row (the helper falls back to a plain draw for
			// system/evidence lines or when a long name wrapped off this row).
			lineSpeaker := ""
			if nameColorsOn && a.icRowIsEntryStart(ri) {
				lineSpeaker = a.icLog[row.entry].speaker
			}
			// Not said yet -> faint. After the link-hover override so a hovered unspoken link stays faint;
			// the name tint is dropped with it.
			ghostedRow := ghost.ghosted(ri, row.entry)
			if ghostedRow {
				col = ghostInk(col, ColPanel)
				lineSpeaker = ""
			}
			indent := a.logRowIndent(logSelIC, ri) // continuation rows hang right of their first row
			// Inline-colour body: a styled entry draws in its authored colours; ghosted or link-hovered rows use the
			// single-colour path.
			if styles := a.icLog[row.entry].styles; len(styles) > 0 && row.entry != hoverLinkEntry && !ghostedRow {
				bodyLen := utf8.RuneCountInString(row.text) - row.headRunes
				spans := inlineSpanAt(styles, row.headRunes, row.bodyOff, bodyLen, col)
				a.drawColoredLogRow(font, c.EmojiFont(icPct), list.X+indent, y, wrapW-indent, row.text, spans)
			} else {
				a.drawLogLineNamed(font, c.EmojiFont(icPct), list.X+indent, y, wrapW-indent, row.text, lineSpeaker, col, nameColorsOn, nameSat, nameVal, boldNames)
			}
			if u := a.icLog[row.entry].url; u != "" {
				if c.hovering(rowRect) {
					c.Tooltip(rowRect, "Open "+u)
					if c.clicked {
						a.openBrowser(schemeForOpen(u)) // bare "www." link → https:// at open time
					}
				}
			}
			// The pin-note chord pins the whole entry into the case notebook; right-click copies the text.
			if pinChord && c.hovering(sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}) {
				a.pinNote(a.icLog[row.entry].text)
			}
		}
		y += lineH
	}
	// "N new" pill: while scrolled up, shows messages since you caught up; click jumps to the first unread line.
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
					a.icScroll = value.Clamp(int32(firstUnreadRow)*lineH, 0, maxScroll)
					a.icStick = a.icScroll >= maxScroll
				} else {
					a.icStick = true // unread rows scrolled off the cap: just go live
				}
			}
		}
	} else if !a.icStick && maxScroll > 0 {
		// Scrolled up with nothing new: offer a plain "jump to latest" button, subtler than the unread pill.
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

// drawOOCLogList renders the OOC scrollback into rect (themed server_chatlog element).
// canvasInk has the same contract as drawICLogList; drawOOCPanel, its chrome twin, passes ColText.
func (a *App) drawOOCLogList(list sdl.Rect, canvasInk bool) {
	c := a.ctx
	// See drawPlayerList.
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemServerChatlog, a.oocPct)))
	// server_chatlog_color is the default foreground (courtroom.cpp:1199 -> :1300); the per-sender tint is a
	// separate key and not handled here.
	oocInk := a.elemInkOr(elemServerChatlog, canvasInk, ColText)
	// The theme's server_chatlog size/family/bold (AO2 folds ms_chatlog in), folded with the OOC zoom.
	font := a.elemFont(elemServerChatlog, a.oocPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	// Wrap against the indented width so continuation rows (drawn at +logWrapIndentPx) cannot overflow.
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
	// Hovering a link tints only that link, across the rows its wrap split it into.
	hoverSpan, hoverID := a.oocHoverSpan(list, a.oocScroll, lineH, wrapW, len(lines))
	clipPrev, clipHad := c.pushClip(list) // top/bottom row clipped to the rect, not the tabs
	y := list.Y - a.oocScroll
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
			// OOC links open on click and copy on right-click; the URL is resolved at wrap time.
			rowRect := sdl.Rect{X: list.X, Y: y, W: wrapW, H: lineH}
			a.oocRowLinkActions(hoverSpan, li, rowRect, list.X+indent, y, lineH)
			a.drawOOCLogRow(list, li, font, list.X+indent, y, wrapW-indent, line, sp, hoverID, nameColorsOn, nameSat, nameVal, boldNames, oocInk)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)
}

// oocHoverSpan resolves the cursor to one OOC link span: its index into a.oocWrapLink and the link id
// (-1, -1 = not on a link). The hit test is against the span's pixel range measured at wrap time.
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
	// Spans are relative to the row's text origin, so take the wrap-continuation indent off the cursor.
	rel := c.mouseX - (list.X + a.logRowIndent(logSelOOC, li))
	for k := range spans {
		if rel >= spans[k].x0 && rel < spans[k].x1 {
			return int(a.oocWrapLinkAt[li]) + k, spans[k].link
		}
	}
	return -1, -1
}

// oocRowLinkActions runs the hover interactions when the hovered span is on display row li; no-op otherwise.
// textX is the row's text origin; the click/copy target is the link's rect, the Jukebox button anchors to the row.
func (a *App) oocRowLinkActions(hoverSpan, li int, rowRect sdl.Rect, textX, y, lineH int32) {
	if hoverSpan < 0 || hoverSpan >= len(a.oocWrapLink) {
		return
	}
	s := a.oocWrapLink[hoverSpan]
	if int(s.row) != li {
		return
	}
	a.oocLinkActions(rowRect, sdl.Rect{X: textX + s.x0, Y: y, W: s.x1 - s.x0, H: lineH}, s.url)
}

// drawOOCLogRow draws one wrapped OOC row, tinting only the hovered link (hoverID < 0 = none).
// The row is drawn whole per pass and the clip picks the slice, so tinted and plain parts share one glyph layout.
// The clip keeps the list's vertical bounds so a partly scrolled row still cuts at the rect edge.
// base is the plain-text colour: server_chatlog_color on the canvas panel, ColText in the chrome OOC box.
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

// oocLinkActions handles OOC link hover: a "+ Jukebox" button at the row's right edge, while open/copy is
// gated on linkRect (the link's own characters). Only called for the single hovered link.
func (a *App) oocLinkActions(rowRect, linkRect sdl.Rect, url string) {
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
	c.Tooltip(linkRect, "Click to open · right-click to copy · or + Jukebox to save: "+url)
	if c.clicked {
		a.openBrowser(schemeForOpen(url)) // bare "www." link → https:// at open time (copy stays bare, as displayed)
	} else if c.rightClicked {
		_ = sdl.SetClipboardText(url)
		a.warnLine = "Link copied to clipboard"
		a.warnAt = time.Now()
	}
}

// submitOOC sends the OOC input if non-blank; an empty name falls back to the sticky AsyncAO<n> (servers reject it).
func (a *App) submitOOC() {
	if strings.TrimSpace(a.oocInput) == "" || a.sess == nil {
		return
	}
	a.sess.SendOOC(a.oocNameOrDefault(), a.oocInput)
	a.recordSentOOC(a.oocInput) // remember the raw line for Up-arrow recall (mirrors IC #8)
	a.oocInput = ""             // clears at send (no echo protocol) — the field's undo history catches it (Ctrl+Z)
}

// drawOOCPanel is the OOC box: scrollable history plus the permanent OOC name field, persisted via the saver.
func (a *App) drawOOCPanel(r sdl.Rect, withInput bool) {
	c := a.ctx
	fH := a.inputFieldH()
	nFields := int32(1) // permanent OOC name (OOC is only an OOC name + OOC chat — no IC showname here)
	if withInput {
		nFields = 2 // + the OOC message input, so the box is a complete OOC chat (one thing)
	}
	fieldsH := nFields*(fH+6) + 4
	list := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - fieldsH}

	// Same server_chatlog resolution as drawOOCLogList (this is the other copy of the OOC scrollback).
	font := a.elemFont(elemServerChatlog, a.oocPct)
	lineH := int32(font.Height()) + 2
	wrapW := list.W - scrollBarW - scrollBarGap
	// Wrap against the indented width so continuation rows (drawn at +logWrapIndentPx) cannot overflow.
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
	// Ctrl+wheel / wheel-button zooms the OOC text, independent of the IC log. As a docked tab the right-column
	// zoom already took the wheel (wheelTaken).
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
	// Drag-select / Ctrl+C + links, same as the themed OOC log.
	a.handleLogSelect(logSelOOC, list, a.oocScroll, lineH, wrapW)
	// Hovering a link tints only that link, as in the themed log.
	hoverSpan, hoverID := a.oocHoverSpan(list, a.oocScroll, lineH, wrapW, len(lines))
	clipPrev, clipHad := c.pushClip(list) // scrollback only; restored before the fields below
	y := list.Y - a.oocScroll
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
			// ColText, not server_chatlog_color: this box is chrome outside any theme canvas, on its own ColPanel.
			a.drawOOCLogRow(list, li, font, list.X+indent, y, wrapW-indent, line, sp, hoverID, nameColorsOn, nameSat, nameVal, boldNames, ColText)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)

	// Identity fields: full width; the placeholders carry the labels.
	fy := r.Y + r.H - fieldsH + 4
	if withInput {
		// The OOC message bar lives inside the box.
		var sent bool
		oocPrimary, oocEmoji := a.icFieldFonts(a.oocInput)
		a.oocInput, sent = c.TextFieldEmoji("oocmsg", sdl.Rect{X: r.X, Y: fy, W: r.W - 4, H: fH}, a.oocInput, "OOC chat… (Enter to send)", oocPrimary, oocEmoji)
		if sent {
			a.submitOOC()
		}
		a.recallOOC() // Up/Down recall recently-sent OOC lines when this field is focused
		fy += fH + 6
	}
	// OOC carries only an OOC name and the OOC chat. The field is tab-local; the permanent default is in Settings.
	// A CJK OOC name renders via the IC input's font fallback; plain ASCII keeps the single-font path.
	onPrimary, onEmoji := a.icFieldFonts(a.oocName)
	a.oocName, _ = c.TextFieldEmoji("oocname2", sdl.Rect{X: r.X, Y: fy, W: r.W - 4, H: fH}, a.oocName, "OOC name (this tab)", onPrimary, onEmoji)
}

// areaWrapMaxLines caps how many lines an area name or detail line wraps to, bounding row height.
const areaWrapMaxLines = 3

// The area list can be drawn twice in one frame under different queries (themed copy via music_search, torn-off
// Areas panel via its own box), so each query source gets its own memo; a shared one would be evicted every frame.
// Two is the exact count: a torn-off tab leaves the docked strip, and classic and themed courtrooms are exclusive.
const (
	areaQuerySlotOwn    = iota // the panel's own field (a.areaSearch)
	areaQuerySlotThemed        // AO2's music_search driving the canvas copy (a.musicSearch)
	areaQuerySlots
)

// areaWrapCache memoizes areaWrapped: the rows plus their inputs; rows == nil means never filled.
type areaWrapCache struct {
	rows  []areaWrapRow
	seq   uint64 // a.areaInfoSeq at build time
	query string
	cardW int32
	pct   int
	gen   int // a.ctx.fontChainGen
}

// areaWrapRow is one ARUP row's word-wrapped display text, cached so scroll/resize does not re-wrap each frame.
type areaWrapRow struct {
	idx         int // ORIGINAL index into a.sess.Areas / a.sess.AreaInfo
	name        string
	detail      string
	nameLines   []string
	detailLines []string
}

// areaWrapLines returns a row's visual line count: wrapped name lines plus a detail block of at least one line.
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

// areaWrapped returns the word-wrapped rows for the shown (search-filtered) areas, rebuilt only when the ARUP
// state, query, card width, zoom or font chain changes (mirrors icWrapped in qol.go).
// query is passed in, lowered and trimmed, because the themed copy filters by music_search, not a.areaSearch;
// slot is the matching memo (areaQuerySlotOwn/Themed).
func (a *App) areaWrapped(font *ttf.Font, cardW int32, query string, slot int) []areaWrapRow {
	m := &a.areaWrap[slot]
	// Key on the resolved scale (theme area_list size x area zoom) so a theme swap that changes only size re-wraps.
	pct := a.elemPct(elemAreaList, a.areaPct)
	if m.rows != nil && m.seq == a.areaInfoSeq && m.query == query &&
		m.cardW == cardW && m.pct == pct && m.gen == a.ctx.fontChainGen {
		return m.rows
	}
	shown := len(a.sess.Areas)
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
		if i < 0 || i >= len(a.sess.Areas) {
			continue
		}
		area := a.sess.Areas[i]
		detail := ""
		if i < len(a.sess.AreaInfo) {
			info := &a.sess.AreaInfo[i]
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
	m.rows, m.seq, m.query, m.cardW, m.pct, m.gen = out, a.areaInfoSeq, query, cardW, pct, a.ctx.fontChainGen
	return m.rows
}

// arupStatusText renders an area's ARUP status/lock/CM fields (courtroom.AreaInfo, AO2-Client arup_modify) as a
// "  ·  STATUS  ·  locked  ·  CM: Phoenix"-style suffix, in ARUP field order; "" if nothing is reported.
// The leading separator is deliberate: callers glue it onto a headline; drawCurrentAreaStatusStrip trims it.
// Status colours (areaStatusColor) stay simpler than AO2's five-way split (courtroom.cpp:1858-1877).
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

// areaCountRowH / areaCountRowLabelOffY size the bare "shown / total" line that replaces the search row when
// the theme's music_search field drives the filter.
const (
	areaCountRowH         = int32(20)
	areaCountRowLabelOffY = int32(3)
)

// themedListSearchDrew reports whether the themed courtroom painted AO2's music_search field this frame.
// Re-derived rather than passed in: drawAreaList has three callers and only the themed music_list one is in the
// canvas. a.themeLay is the cache drawCourtroomThemed lays out from, so the two cannot disagree.
func (a *App) themedListSearchDrew() bool {
	if a.panelHidden(panelLog) {
		return false
	}
	_, ok := a.themeLay.rect("music_search")
	return ok
}

// areaRowHeaderPad is the fixed slack on top of an area card's two text rows (top inset, name/detail gap, bottom).
const areaRowHeaderPad = int32(11)

// areaRowLineHN is the area row height for an arbitrary visual line count.
func areaRowLineHN(fontH, lines int32) int32 {
	return fontH*lines + areaRowHeaderPad
}

// drawAreaList lists the server's areas; a click requests the swap via MC with the area name (isAreaTransfer
// keeps it out of the audio path). canvasInk is true only for the Areas view of the themed music_list panel.
func (a *App) drawAreaList(r sdl.Rect, canvasInk bool) {
	c := a.ctx
	if a.areaPct < config.MinLogScalePercent { // uninit / stale → match the log
		a.areaPct = a.logPct
	}
	// See drawPlayerList. After the zoom is settled, so the face opens at the scale drawn.
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemAreaList, a.areaPct)))
	// area_list_color replaces only the neutral row inks (population pair, detail block); status, locked, current and
	// hover colours stay. AO2 paints status as the row background brush (courtroom.cpp:1851-1882) and has one
	// foreground key, with population as a column; our detail block carries the count.
	areaIdleCol := a.elemInkOr(elemAreaList, canvasInk, ColTextDim)
	areaLiveCol := a.elemInkOr(elemAreaList, canvasInk, ColText)
	// Ctrl+wheel (or wheel-button) resizes the area text (areaPct). Here so torn and themed callers get it too;
	// in the classic panel zoomWheel already took the wheel (wheelTaken).
	a.zoomWheel(r, &a.areaPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	if len(a.sess.Areas) == 0 {
		c.Label(r.X+4, r.Y+4, "Server reported no areas.", ColTextDim)
		return
	}
	// Recent areas: one-click jump-back chips, newest first, skipping index 0 (the current area). One row.
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
	// Search filter, memoized so the O(N) scan runs only when the query or Areas list changes (refreshAreaFilter).
	// Inside the theme canvas AO2's own music_search field already drives the area filter, so this row shrinks to
	// the match count alone; elsewhere the field is drawn here.
	searchExternal := canvasInk && a.themedListSearchDrew()
	// The header row is split (areamenu.go): the menu button sits at the right end and the field and counter are
	// measured back from its left edge. Only this row is split. canvasInk is the answer rosterMenuThemed derives.
	head := splitAreaHeaderRow(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: fieldH})
	a.drawAreaMenuButton(head.button, canvasInk)
	var query string
	// Both memos are indexed by query source, so this drawer and a torn-off Areas panel can share a frame.
	slot := areaQuerySlotOwn
	if searchExternal {
		slot = areaQuerySlotThemed
		query = strings.ToLower(strings.TrimSpace(a.musicSearch))
	} else {
		// Use head.field, not a rect built here: splitAreaHeaderRow keeps the field, counter and menu button apart.
		a.areaSearch, _ = c.TextField("areasearch", head.field, a.areaSearch, "Search areas…")
		query = strings.ToLower(strings.TrimSpace(a.areaSearch))
	}
	total := len(a.sess.Areas)
	shown := total
	if query != "" {
		shown = len(a.refreshAreaFilter(query, slot))
	}
	if searchExternal {
		// The count keeps its own short line, left-aligned on the card column.
		c.Label(r.X+2, r.Y+areaCountRowLabelOffY, a.areaCount.text(shown, total), ColTextDim)
		r.Y += areaCountRowH
		r.H -= areaCountRowH
	} else {
		// Same split as the field above.
		c.Label(head.count.X, head.count.Y, a.areaCount.text(shown, total), ColTextDim)
		r.Y += fieldH + 6
		r.H -= fieldH + 6
	}
	if !c.ctrlHeld { // ctrl+wheel resizes text, never scrolls
		a.areaScroll -= c.WheelIn(r) * scrollStepPx
	}
	// The theme's area_list size/family, folded with the area zoom.
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
	a.areaScroll = c.VScrollbar("areascroll", track, a.areaScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r) // partial top/bottom row stays inside the panel
	defer c.popClip(clipPrev, clipHad)
	y := r.Y - a.areaScroll
	for _, row := range rows {
		rowH := areaRowLineHN(subH, row.lineCount())
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-rowH {
			card := sdl.Rect{X: r.X + 2, Y: y + 2, W: cardW, H: rowH - areaCardGap}
			hover := c.hovering(card)
			current := row.name == a.curArea
			// Two-block entry: population-coloured name on top, indented detail block under it, each word-wrapped.
			// A locked room tints the row red; the area you are in is accent.
			nameCol := areaIdleCol
			locked := false
			if row.idx < len(a.sess.AreaInfo) {
				info := &a.sess.AreaInfo[row.idx]
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

// wardrobeFolders lists the distinct folder names in the current menu in first-seen order (the wardrobe's
// top-level folder icons); a folder exists exactly while a character is filed in it.
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
