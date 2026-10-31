package ui

import (
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The missing-theme-fonts modal. A theme names its fonts and usually doesn't include them (in AO2 they live in the
// base's fonts/ folder, which Qt registers globally at startup), so a theme shared alone arrives with families named
// and no files. AsyncAO renders those strings in its own face and says nothing, which reads as a wrong font.
//
// A modal rather than the warning toast, because this needs an action in a file manager. It is in the same family as
// drawDisconnectDialog.
//
// Once per theme: a theme with missing fonts has them missing on every apply, so warning every time would make the
// client unusable for anyone who won't fix it. The set of themes already warned about is persisted.

// fontWarnDialog is the modal's state. The zero value is closed.
type fontWarnDialog struct {
	open bool
	// theme is the theme this warning is about, so dismissing marks the right one even if another apply lands
	// meanwhile.
	theme string
	// families and body are the resolved content, snapshotted at open so a later apply can't rewrite the text under the
	// user.
	families []string
	body     string
	// dir is AsyncAO's font drop folder, "" when it could not be resolved (a read-only config location); the Open
	// button hides then.
	dir string
}

// maybeWarnMissingFonts opens the modal for a theme whose fonts did not resolve, unless already warned about. Called on
// theme apply; never for a theme that resolved everything.
func (a *App) maybeWarnMissingFonts(themeName string, families []string, body, dir string) bool {
	if len(families) == 0 || body == "" {
		return false
	}
	if a.d.Prefs.Load().FontWarnShownFor(themeName) {
		return false
	}
	// Marked shown at open, not at dismiss: a user who quits with the modal up has still seen it.
	a.d.Prefs.MarkFontWarnShown(themeName)
	a.fontWarnDlg = fontWarnDialog{open: true, theme: themeName, families: families, body: body, dir: dir}
	return true
}

// Modal geometry; taller than the disconnect box because the body is a paragraph plus a font list, both wrapped.
const (
	fontWarnW = 560
	fontWarnH = 260
)

// drawFontWarnDialog paints the missing-fonts modal at the frame tail with the rest of the modal family so clicks can't
// reach the screen behind it.
func (a *App) drawFontWarnDialog(w, h int32) {
	c := a.ctx
	if !a.fontWarnDlg.open {
		return
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	m := sdl.Rect{X: (w - fontWarnW) / 2, Y: (h - fontWarnH) / 2, W: fontWarnW, H: fontWarnH}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "This theme's fonts are missing", ColText)

	// The families first, in the normal ink: they are what the user has to go and find.
	y := m.Y + 48
	c.LabelClipped(m.X+pad, y, fontWarnW-2*pad, strings.Join(a.fontWarnDlg.families, ", "), ColAccent)
	y += 26

	// Then the explanation, wrapped to the panel so a long font list can't spill past the border.
	for _, line := range a.wrapDialogBody(a.fontWarnDlg.body, fontWarnW-2*pad) {
		if y > m.Y+fontWarnH-btnH-pad-24 {
			break
		}
		c.LabelClipped(m.X+pad, y, fontWarnW-2*pad, line, ColTextDim)
		y += 20
	}

	by := m.Y + fontWarnH - btnH - pad
	if a.fontWarnDlg.dir != "" {
		if c.Button(sdl.Rect{X: m.X + pad, Y: by, W: 170, H: btnH}, "Open fonts folder") {
			a.openInFileManager(a.fontWarnDlg.dir)
		}
		if c.Button(sdl.Rect{X: m.X + pad + 178, Y: by, W: 140, H: btnH}, "Reload theme") {
			a.fontWarnDlg = fontWarnDialog{}
			a.applyThemeAsync()
			return
		}
	}
	if c.Button(sdl.Rect{X: m.X + fontWarnW - pad - 110, Y: by, W: 110, H: btnH}, "Got it") {
		a.fontWarnDlg = fontWarnDialog{}
	}
}

// fontWarnBodyLines is how many wrapped lines fit between the font list and the buttons; wrapToWidthMeasured takes the
// cap itself.
const fontWarnBodyLines = 6

// wrapDialogBody splits the explanation to fit the panel, measured in the chrome
// font the modal actually draws in.
func (a *App) wrapDialogBody(s string, width int32) []string {
	return wrapToWidthMeasured(a.ctx.TextWidth, s, width, fontWarnBodyLines)
}
