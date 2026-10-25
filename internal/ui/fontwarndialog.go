package ui

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// The missing-theme-fonts modal.
// A MODAL rather than the warning toast, because the toast is gone in a few
// seconds and this needs an action taken in a file manager. It is a member of the
// same family as drawDisconnectDialog: dimmed backdrop, panel, heading, body, and
// ONCE PER THEME, and that is the whole design constraint. A theme with missing
// fonts has them missing on every single apply, so warning every time would make
// the client unusable for anyone who does not intend to fix it — and the second

// fontWarnDialog is the modal's state. The zero value is closed.
type fontWarnDialog struct {
	open bool
	// theme is the theme this warning is ABOUT, remembered so dismissing marks
	// the right one even if another apply lands while the modal is up.
	theme string
	// families and body are the resolved content, snapshotted at open time. A
	// later apply must not rewrite the text under the user's eyes.
	families []string
	body     string
	// dir is AsyncAO's font drop folder, "" when it could not be resolved (a
	// read-only config location) — the Open button hides rather than misleads.
	dir string
}

// maybeWarnMissingFonts opens the modal for a theme whose fonts did not resolve,
// unless this theme has already been warned about.
// Called on theme apply. Deliberately NOT called for a theme that resolved
// everything: this must never be the reason someone sees a dialog.
func (a *App) maybeWarnMissingFonts(themeName string, families []string, body, dir string) bool {
	if len(families) == 0 || body == "" {
		return false
	}
	if a.d.Prefs.Load().FontWarnShownFor(themeName) {
		return false
	}
	// Marked as shown at OPEN, not at dismiss. A user who closes the client with
	// the modal up has still seen it, and re-showing it on next launch would be
	// the nagging this is designed to avoid.
	a.d.Prefs.MarkFontWarnShown(themeName)
	a.font.fontWarnDlg = fontWarnDialog{open: true, theme: themeName, families: families, body: body, dir: dir}
	return true
}

// Modal geometry. Taller than the disconnect box because the body is a real
// paragraph plus a font list, and both wrap.
const (
	fontWarnW = 560
	fontWarnH = 260
)

// drawFontWarnDialog paints the missing-fonts modal. Drawn at the frame tail with
// the rest of the modal family so clicks cannot reach the screen behind it. Off
// the render hot path — only while open.
func (a *App) drawFontWarnDialog(w, h int32) {
	c := a.ctx
	if !a.font.fontWarnDlg.open {
		return
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	m := sdl.Rect{X: (w - fontWarnW) / 2, Y: (h - fontWarnH) / 2, W: fontWarnW, H: fontWarnH}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "This theme's fonts are missing", ColText)

	// The families first and in the normal ink — they are the specific thing the
	// user has to go and find, and burying them inside the explanation makes them
	// hard to act on.
	y := m.Y + 48
	c.LabelClipped(m.X+pad, y, fontWarnW-2*pad, strings.Join(a.font.fontWarnDlg.families, ", "), ColAccent)
	y += 26

	// Then the explanation, wrapped to the panel. wrapToWidth is the same helper
	// the logs use, so a long font list cannot spill past the border (the §3.4
	// class of bug the disconnect dialog's comment names).
	for _, line := range a.wrapDialogBody(a.font.fontWarnDlg.body, fontWarnW-2*pad) {
		if y > m.Y+fontWarnH-btnH-pad-24 {
			break
		}
		c.LabelClipped(m.X+pad, y, fontWarnW-2*pad, line, ColTextDim)
		y += 20
	}

	by := m.Y + fontWarnH - btnH - pad
	if a.font.fontWarnDlg.dir != "" {
		if c.Button(sdl.Rect{X: m.X + pad, Y: by, W: 170, H: btnH}, "Open fonts folder") {
			openInFileManager(a.font.fontWarnDlg.dir)
		}
		if c.Button(sdl.Rect{X: m.X + pad + 178, Y: by, W: 140, H: btnH}, "Reload theme") {
			a.font.fontWarnDlg = fontWarnDialog{}
			a.applyThemeAsync()
			return
		}
	}
	if c.Button(sdl.Rect{X: m.X + fontWarnW - pad - 110, Y: by, W: 110, H: btnH}, "Got it") {
		a.font.fontWarnDlg = fontWarnDialog{}
	}
}

// fontWarnBodyLines is how many wrapped lines the panel has room for between the
// font list and the buttons. wrapToWidthMeasured takes the cap itself, so the
// modal cannot be handed more text than it can draw.
const fontWarnBodyLines = 6

// wrapDialogBody splits the explanation to fit the panel, measured in the chrome
// font the modal actually draws in.
func (a *App) wrapDialogBody(s string, width int32) []string {
	return wrapToWidthMeasured(a.ctx.TextWidth, s, width, fontWarnBodyLines)
}
