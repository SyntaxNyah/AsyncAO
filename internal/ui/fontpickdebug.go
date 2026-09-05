package ui

// Font-pick diagnostic (v1.93.0 "funky fonts" report): a themed IC log
// rendered two short ASCII rows in a stray face, both dimmed, one of them
// monospace, while every other row on screen stayed normal. Two static-
// reading passes could not reproduce a live trigger for it — the census that
// ships beside this file names the two structurally real, unconfirmed
// candidates: a per-glyph raster path (labelEmojiWeight → coverRunes) that
// reverse-resolves a font pointer to its owning fontSet by IDENTITY
// (setOf/setIndexOf, ui.go), which is ambiguous the moment two sets share a
// pointer (buildSet aliases the embedded last-resort face across every
// 100%-scale set with no custom chrome font — ui.go's buildSet, the
// "chromeShare" branch); and a single-slot coverage-hint cache
// (Ctx.coverHintFont/Text) that only stays valid because nothing currently
// runs between the pick and the read for the SAME text.
//
// This file does not fix either candidate — the recon was explicit that a
// speculative fix here would be worse than nothing, because it could close
// the report while the real defect stays. It gives the NEXT report a way to
// be diagnosed from a screenshot: the Debug panel's Fonts tab resolves the
// SAME font and colour a live IC log row would, through the SAME calls
// drawICLogList makes (elemFontFor, coversFace, the ghost/colour pipeline),
// so a user need only open it, reproduce the funky row, and send a
// screenshot of THIS panel instead of the client.
//
// Read only from drawDebugFonts (debugpanel.go), which runs only while the
// panel is open on its Fonts tab. The normal IC/OOC draw calls nothing in
// this file — TestICLogDrawAllocationIsUnaffectedByTheFontDebugReadout pins
// that the real per-frame call site (drawICLogList) is untouched.

import (
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// debugFontRowCap bounds how many of the most recent IC log entries the
	// Fonts tab inspects. A screenshot of a live bug only ever needs the
	// tail of the log — the panel has a fixed height and rows past what it
	// can show would scroll off unread — so this is a small, named cap
	// rather than the unbounded log itself (hard rule 4).
	debugFontRowCap = 24
	// debugFontTextCap bounds how much of a row's text the tab PRINTS. The
	// resolved font and colour never depend on the untruncated tail of a
	// long message, only the on-screen label does — a message longer than
	// this still gets the real pick, just a shortened readout.
	debugFontTextCap = 48
)

// fontPickRow is one Fonts-tab line: exactly what the real draw resolved for
// one recent IC log row, nothing re-derived.
type fontPickRow struct {
	text string // the row text as drawn (stamp + speaker + message), truncated to debugFontTextCap

	// setName / slot / slotOf are setIndexOf's answer for the resolved font —
	// the SAME ambiguous reverse pointer lookup coverRunes and emojiPctFor use
	// on the real per-glyph raster path. slot/slotOf are -1/0 when the font
	// belongs to no scaled set (chrome-only / headless).
	setName string
	slot    int
	slotOf  int
	// shared marks the resolved font as literally Ctx.font or Ctx.fontDev —
	// the pointer buildSet aliases across EVERY 100%-scale set with no custom
	// chrome font (ui.go). A themed ic_chatlog row reporting shared==true
	// while covered==false is the exact shape of the funky-fonts recon's
	// unconfirmed candidate #2 firing for real.
	shared bool
	// covered is coversFace(font, text): the same gate logRowSplit and
	// labelEmojiWeight read to decide between the plain single-font blit and
	// the per-glyph raster. false means THIS row draws through the raster,
	// not the fast path every other row on a normal screen takes.
	covered bool

	color    sdl.Color
	colorSrc string // which branch of the real colour pipeline produced color
}

// icFontPickRows drives elemFontFor and coversFace — the exact calls
// drawICLogList's row loop makes (screens.go) at the same scale (a.logPct) —
// for the most recent debugFontRowCap IC log entries, folding in the same
// colour pipeline the real draw reads (explicit \cN, friend glow, ghost
// dimming, off the SAME a.icLog/ghostLogSpan state).
//
// It reconstructs the stamp-prefixed row text the same way icWrapped does
// (qol.go: stamp + "  " + text) — that's a plain string concatenation, not a
// font or colour decision, so replicating it here isn't re-implementing the
// seam this exists to catch. It does NOT reproduce icWrapped's word-wrap: a
// message long enough to wrap into more than one on-screen row is inspected
// here as the single unwrapped string, not per continuation row. See the
// phase report's RISKS section for why that's an accepted, documented gap
// rather than a silent one.
func (a *App) icFontPickRows(out []fontPickRow) []fontPickRow {
	out = out[:0]
	if len(a.icLog) == 0 {
		return out
	}
	c := a.ctx
	showStamps := a.d.Prefs != nil && a.d.Prefs.ICTimestampsOn()
	// canvasInk mirrors drawICLogList's own parameter: true only while the log
	// draws INSIDE a theme's design canvas (theme_layout.go), which is the one
	// case elemInkOr honours a theme's own ic_chatlog_color. a.themeLay.valid
	// is the same flag drawCourtroom itself reads to choose the themed vs
	// classic draw path, so this tracks the real dispatch rather than guessing.
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
			text:     truncateRunes(text, debugFontTextCap),
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

// fontSetLabel names which fontSet setIndexOf's reverse pointer lookup
// resolves font to. It is a pure NAMING step over setIndexOf's real answer —
// it makes no pick decision of its own, so a divergence between this label
// and "ic_chatlog" for a row drawn through elemICChatlog is setIndexOf
// actually being ambiguous, not a bug in this function.
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
