package ui

import (
	"github.com/veandco/go-sdl2/sdl"
)

// AO2's ooc_toggle: one button that swaps the server chat panel for the debug
// log (issue #21 label 10, "server/debug?").
//
// AO2 wires ui_ooc_toggle to on_ooc_toggle_clicked (../AO2-Client/src/courtroom.cpp:5197):
// it shows ui_debug_log, hides ui_server_chatlog and relabels itself "Debug",
// then swaps back. The debug log lives at the ms_chatlog rect — set_size_and_pos(
// ui_debug_log, "ms_chatlog") at :831, an old key name AO2 keeps for theme
// compatibility — which is why ms_chatlog is NOT a server_chatlog alias.
//
// The button's label names what is CURRENTLY on screen, not what clicking does
// (AO2 sets "Server" at :1014 while server chat is up). Easy to invert.

const (
	// debugPanelLineGap matches the log lists' row spacing.
	debugPanelLineGap = 2
	// debugPanelPad insets the text from the theme's panel art, same as the logs.
	debugPanelPad = 4
)

// oocToggleLabel is what AO2 writes on the button: the panel now showing.
func (s *sessionState) oocToggleLabel() string {
	if s.debugOOC {
		return "Debug"
	}
	return "Server"
}

// drawThemedDebugLog paints the bounded failure ring into a theme's rect.
//
// Deliberately plainer than drawOOCLogList: the debug ring is diagnostics, so it
// has no name colouring, no link spans and no drag-select — just stamped lines,
// wrapped and scrollable. It shares the log font so it matches the panel it
// replaces.
func (a *App) drawThemedDebugLog(list sdl.Rect) {
	c := a.ctx
	if list.W <= 2*debugPanelPad || list.H <= 0 {
		return
	}
	body := sdl.Rect{
		X: list.X + debugPanelPad,
		Y: list.Y + debugPanelPad,
		W: list.W - 2*debugPanelPad,
		H: list.H - 2*debugPanelPad,
	}
	// debug_log, NOT server_chatlog. The two panels swap places so borrowing the
	// neighbour's font looks harmless, but AO2 dresses this widget from its own
	// identifier — set_font(ui_debug_log, "", "debug_log", p_char),
	// courtroom.cpp:1198 — and a theme can size or colour the two differently.
	// aceattorney2x is exactly that case: it declares server_chatlog_sharp = 1 and
	// no debug_log_sharp at all, so sharing the key would render this panel with a
	// treatment its author asked for somewhere else.
	font := a.elemFont(elemDebugLog, a.oocPct)
	bold := a.elemBold(elemDebugLog)
	// #21 label 16: "debug_log_color". This panel only ever draws inside the
	// theme's design canvas (it IS the ms_chatlog element), so canvasInk is
	// unconditionally true here — unlike the log lists, which have chrome twins.
	//
	// AO2's debug_log_error_color / _warning_color etc. are a DIFFERENT family: they
	// go through AOApplication::get_color rather than set_font's per-element key,
	// and are out of scope (this ring is not severity-tagged).
	ink := a.elemInkOr(elemDebugLog, true, ColTextDim)
	lineH := int32(font.Height()) + debugPanelLineGap
	wrapW := body.W - scrollBarW - scrollBarGap
	if wrapW <= 0 {
		return
	}

	lines := a.debugLog
	contentH := int32(len(lines)) * lineH
	maxScroll := contentH - body.H
	if maxScroll < 0 {
		maxScroll = 0
	}
	if d := c.WheelIn(body); d != 0 {
		a.debugOOCScroll -= d * scrollStepPx
	}
	track := sdl.Rect{X: body.X + body.W - scrollBarW, Y: body.Y, W: scrollBarW, H: body.H}
	a.debugOOCScroll = c.VScrollbar("debugoocscroll", track, a.debugOOCScroll, contentH, body.H)
	if a.debugOOCScroll > maxScroll {
		a.debugOOCScroll = maxScroll
	}
	if a.debugOOCScroll < 0 {
		a.debugOOCScroll = 0
	}

	if len(lines) == 0 {
		// LabelClippedFont, not LabelClipped: the row pitch above is measured in the
		// theme's debug_log face, so drawing in the client's chrome face instead
		// would space the panel for one font and fill it with another.
		c.LabelClippedFont(font, body.X, body.Y, wrapW, "No failures logged this session.", ColTextDim)
		return
	}
	// Clip so the partially-scrolled top and bottom rows stop at the panel edge
	// rather than bleeding over the theme's art.
	clipPrev, clipHad := c.pushClip(body)
	y := body.Y - a.debugOOCScroll
	for _, e := range lines {
		ln := e.text
		if y+lineH > body.Y && y < body.Y+body.H { // skip rows scrolled out of view
			// One weighted draw, not a plain draw plus a 1 px-offset second pass: a
			// LOGICAL pixel offset is multiplied by the UI scale, so the two copies land
			// on different sub-pixel phases and the row reads doubled rather than bold
			// (F1b — see Ctx.textTextureBold).
			c.LabelClippedFontWeight(font, body.X, y, wrapW, ln, ink, bold)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)
}
