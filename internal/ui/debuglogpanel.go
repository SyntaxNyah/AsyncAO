package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// AO2's ooc_toggle swaps the server chat panel for the debug log (on_ooc_toggle_clicked, courtroom.cpp:5197).
// The debug log lives at ms_chatlog (:831), so that key is not a server_chatlog alias.
// The label names what is on screen, not what clicking does (AO2 sets "Server" at :1014 while server chat is up).

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

// drawThemedDebugLog paints the bounded failure ring into a theme's rect, plainer than drawOOCLogList: stamped lines
// only.
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
	// debug_log, not server_chatlog: AO2 dresses this widget from its own identifier (courtroom.cpp:1198),
	// and a theme can style them differently, as aceattorney2x does.
	font := a.elemFont(elemDebugLog, a.oocPct)
	bold := a.elemBold(elemDebugLog)
	// This panel only draws inside the theme canvas, so canvasInk is true. AO2's debug_log_error_color family goes
	// through get_color rather than set_font, and this ring has no severity anyway.
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
	a.debugOOCScroll = value.Clamp(a.debugOOCScroll, 0, maxScroll)

	if len(lines) == 0 {
		// LabelClippedFont: the row pitch above is measured in the debug_log face.
		c.LabelClippedFont(font, body.X, body.Y, wrapW, "No failures logged this session.", ColTextDim)
		return
	}
	// Clip so partly scrolled rows stop at the panel edge.
	clipPrev, clipHad := c.pushClip(body)
	y := body.Y - a.debugOOCScroll
	for _, e := range lines {
		ln := e.text
		if y+lineH > body.Y && y < body.Y+body.H { // skip rows scrolled out of view
			// One weighted draw: a 1 px logical offset scales with the UI, landing on another sub-pixel phase so the
			// row reads doubled.
			c.LabelClippedFontWeight(font, body.X, y, wrapW, ln, ink, bold)
		}
		y += lineH
	}
	c.popClip(clipPrev, clipHad)
}
