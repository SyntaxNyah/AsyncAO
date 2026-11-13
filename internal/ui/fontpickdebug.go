package ui

// Font-pick readout for the Debug panel's Fonts tab: it resolves the font and colour a live IC log row would,
// through the same calls drawICLogList makes, so a wrong-font row can be diagnosed from a screenshot.
// Only drawDebugFonts reads this; the normal IC and OOC draw calls nothing here.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

const (
	// debugFontRowCap bounds how many recent IC log entries the tab inspects (hard rule 4).
	debugFontRowCap = 24
	// debugFontTextCap bounds how much of a row's text the tab prints; the resolved font and colour are unaffected.
	debugFontTextCap = 48
)

// fontPickRow is one Fonts-tab line: what the real draw resolved for one recent IC log row.
type fontPickRow struct {
	text string // the row text as drawn (stamp + speaker + message), truncated to debugFontTextCap

	// setName, slot and slotOf are setIndexOf's answer for the resolved font;
	// slot and slotOf are -1 and 0 when it belongs to no scaled set.
	setName string
	slot    int
	slotOf  int
	// shared marks the font as literally Ctx.font or Ctx.fontDev, which buildSet aliases across
	// every 100%-scale set with no custom chrome font, so setIndexOf is ambiguous for it.
	shared bool
	// covered is coversFace(font, text); false means this row draws through the per-glyph raster, not the fast path.
	covered bool

	color    sdl.Color
	colorSrc string // which branch of the real colour pipeline produced color
}

// icFontPickRows runs elemFontFor and coversFace, as drawICLogList's row loop does, on the latest IC log entries.
// It does not reproduce icWrapped's word-wrap: a wrapped message is inspected as the single unwrapped string.
func (a *App) icFontPickRows(out []fontPickRow) []fontPickRow {
	out = out[:0]
	if len(a.icLog) == 0 {
		return out
	}
	c := a.ctx
	showStamps := a.d.Prefs != nil && a.d.Prefs.Load().ICTimestamps
	// True only inside a theme's design canvas, the one case elemInkOr honours the theme's ic_chatlog_color.
	icInk := a.elemInkOr(elemICChatlog, a.themeLay.valid, ColText)
	ghost := a.ghostLogSpan(nil) // the rows argument is unused (ghosttext.go) — nil is what a real caller passes too
	lo := 0
	if len(a.icLog) > debugFontRowCap {
		lo = len(a.icLog) - debugFontRowCap
	}
	for i := lo; i < len(a.icLog); i++ {
		e := &a.icLog[i]
		text := e.text
		if showStamps && e.stamp != "" {
			text = e.stamp + "  " + text
		}
		font := a.elemFontFor(elemICChatlog, a.logPct, text) // SAME call as screens.go's IC row loop
		covered := c.coversFace(font, text)                  // SAME gate logRowSplit/labelEmojiWeight read

		col, src := icInk, "theme ink / default"
		if e.color > 0 {
			col, src = render.TextColor(e.color), "explicit \\cN"
		}
		if e.friend {
			src += " + friend glow"
		}
		if ghost.ghosted(0, i) {
			col, src = ghostInk(col, ColPanel), "ghosted (queued, not yet spoken)"
		}
		setName, slot, slotOf := fontSetLabel(c, font)
		out = append(out, fontPickRow{
			text:     clampRunes(text, debugFontTextCap),
			setName:  setName,
			slot:     slot,
			slotOf:   slotOf,
			shared:   font != nil && (font == c.font || font == c.fontDev),
			covered:  covered,
			color:    col,
			colorSrc: src,
		})
	}
	return out
}

// fontSetLabel names the fontSet setIndexOf resolves font to; it makes no pick of its own.
func fontSetLabel(c *Ctx, font *ttf.Font) (name string, slot, slotOf int) {
	s, _, idx := c.setIndexOf(font)
	if s == nil {
		return "(none - chrome/headless)", -1, 0
	}
	switch s {
	case &c.chatSet:
		return "chat", idx, len(s.fonts)
	case &c.logSet:
		return "log", idx, len(s.fonts)
	case &c.chromeSet:
		return "chrome", idx, len(s.fonts)
	}
	for i := range c.themeElemSets {
		if s == &c.themeElemSets[i] {
			return theme.FontElements[i], idx, len(s.fonts)
		}
	}
	return "(unknown set)", idx, len(s.fonts)
}
