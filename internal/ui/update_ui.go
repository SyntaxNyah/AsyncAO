package ui

// M13 self-update, UI side: the one-shot launch check, the persistent
// "update available" chip, and the What's New patch-notes modal. The actual
// download + self-replace is a separate, isolated step (see internal/update);
// until it lands, "Get the update" opens the release page in the browser.
//
// Input is handled in handleUpdateInput BEFORE the screens draw (the kit has
// one per-frame click bool, so a modal must consume it first — same pattern as
// the tab strip); drawUpdateAvailable only paints, in the overlay phase, using
// the same rects.

import (
	"context"
	"os"
	"runtime"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/update"
)

const (
	// updateChipH is the height of the persistent reopen chip (top-right).
	updateChipH int32 = 22
	// updateModalW/H size the What's New panel.
	updateModalW int32 = 560
	updateModalH int32 = 420
	// updateNotesLineH is the patch-notes line pitch (matches the kit's body
	// font density, like the hotkey sheet's rows).
	updateNotesLineH int32 = 18
	// updateNotesMaxLines bounds the wrapped patch notes the modal builds
	// (rule §17.4: a huge release body can't grow the draw unbounded — the
	// overflow is reachable via "Get the update" → the full release page).
	updateNotesMaxLines = 400
)

// maybeKickUpdateCheck fires the update probe EXACTLY ONCE, on the first frame
// (the window is already up), so the check never touches the boot path. A
// disabled pref short-circuits it, and Check itself skips an unstamped dev
// build, so neither hits the network.
func (a *App) maybeKickUpdateCheck() {
	if a.updateChecked {
		return
	}
	a.updateChecked = true
	// Clear a leftover .old backup from a previous self-update — off the render
	// thread (disk I/O) and off the boot path (frame 1), regardless of the
	// check pref (you want the stale binary gone even with checks disabled).
	go func() {
		if exe, err := os.Executable(); err == nil {
			update.CleanupOldVersion(exe)
		}
	}()
	if !a.d.Prefs.UpdateCheckEnabled() {
		return
	}
	res := a.updateRes
	go func() {
		// assetMatch = GOOS so the (future) self-replace step grabs this
		// platform's release asset. A failed or already-current check is
		// silent — the updater never nags.
		rel, err := update.Check(context.Background(), "", update.Version, runtime.GOOS)
		if err != nil || rel == nil {
			return
		}
		res <- rel
	}()
}

// pollUpdate drains a found release: it stores it, builds the chip label once
// (so the per-frame draw never allocates), and auto-opens the modal the first
// time so the patch notes are surfaced without the user hunting for them.
func (a *App) pollUpdate() {
	select {
	case rel := <-a.updateRes:
		if rel == nil {
			return
		}
		a.updateRel = rel
		a.updateChipLabel = "Update " + rel.Version + " available"
		a.updateShow = true
	default:
	}
}

// updateChipRect is the persistent reopen chip, top-right and clear of the
// centre tab strip.
func (a *App) updateChipRect(w int32) sdl.Rect {
	cw := a.ctx.TextWidth(a.updateChipLabel) + 18
	return sdl.Rect{X: w - cw - pad, Y: tabBarH + 4, W: cw, H: updateChipH}
}

// updateModalRects lays out the What's New panel and its hit targets (shared by
// the pre-screen input pass and the overlay draw so they can't drift).
func updateModalRects(w, h int32) (panel, notes, getBtn, laterBtn sdl.Rect) {
	panel = sdl.Rect{X: (w - updateModalW) / 2, Y: (h - updateModalH) / 2, W: updateModalW, H: updateModalH}
	notes = sdl.Rect{X: panel.X + pad, Y: panel.Y + 60, W: panel.W - 2*pad, H: panel.H - 60 - 48}
	btnY := panel.Y + panel.H - 38
	getBtn = sdl.Rect{X: panel.X + pad, Y: btnY, W: 220, H: btnH}
	laterBtn = sdl.Rect{X: panel.X + panel.W - pad - 100, Y: btnY, W: 100, H: btnH}
	return panel, notes, getBtn, laterBtn
}

// handleUpdateInput consumes the chip / modal interaction before the screens
// see this frame's click. No-op until the one-shot check found a release.
func (a *App) handleUpdateInput(w, h int32) {
	if a.updateRel == nil {
		return
	}
	c := a.ctx
	if !a.updateShow {
		if chip := a.updateChipRect(w); c.hovering(chip) && c.clicked {
			a.updateShow = true
			c.clicked = false
		}
		return
	}
	panel, notes, getBtn, laterBtn := updateModalRects(w, h)
	if d := c.WheelIn(notes); d != 0 {
		a.updateScroll -= d * scrollStepPx
		if a.updateScroll < 0 {
			a.updateScroll = 0
		}
		c.wheelTaken = true
	}
	if c.keyPressed == sdl.K_ESCAPE {
		a.updateShow = false
		c.keyPressed = 0
	}
	if !c.clicked {
		return
	}
	switch {
	case c.hovering(getBtn):
		if a.updateRel.PageURL != "" {
			openBrowser(a.updateRel.PageURL)
		}
	case c.hovering(laterBtn):
		a.updateShow = false
	case !c.hovering(panel):
		a.updateShow = false // click off the panel dismisses
	}
	c.clicked = false // the modal owns this frame's click
}

// drawUpdateAvailable paints the M13 affordances over every screen: the
// persistent reopen chip when the modal is closed, else the modal itself.
func (a *App) drawUpdateAvailable(w, h int32) {
	if a.updateRel == nil {
		return
	}
	c := a.ctx
	if !a.updateShow {
		chip := a.updateChipRect(w)
		c.Fill(chip, ColAccent)
		c.Label(chip.X+9, chip.Y+4, a.updateChipLabel, ColBackground)
		return
	}

	panel, notes, getBtn, laterBtn := updateModalRects(w, h)
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{A: 150}) // scrim
	c.Fill(panel, sdl.Color{R: 12, G: 12, B: 18, A: 245})
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+10, "Update "+a.updateRel.Version+" available", ColText)
	c.Label(panel.X+pad, panel.Y+38, "What's new:", ColAccent)

	// Patch notes: split on newlines FIRST (WrapText collapses whitespace and
	// would flatten the bullet structure), then wrap each line to the panel.
	lines := a.updateNotesLines(notes.W)
	contentH := int32(len(lines)) * updateNotesLineH
	maxScroll := contentH - notes.H
	if maxScroll < 0 {
		maxScroll = 0
	}
	if a.updateScroll > maxScroll {
		a.updateScroll = maxScroll
	}
	clipPrev, clipHad := c.pushClip(notes)
	y := notes.Y - a.updateScroll
	for _, ln := range lines {
		if y > notes.Y+notes.H {
			break
		}
		if y >= notes.Y-updateNotesLineH && ln != "" {
			c.LabelClipped(notes.X, y, notes.W, ln, ColText)
		}
		y += updateNotesLineH
	}
	c.popClip(clipPrev, clipHad)

	a.drawUpdateButton(getBtn, "Get the update")
	a.drawUpdateButton(laterBtn, "Later")
	if maxScroll > 0 {
		c.Label(panel.X+pad, getBtn.Y+4, "(scroll for more)", ColTextDim)
	}
}

// updateNotesLines wraps the release body for the modal, preserving its line
// breaks and bounding the total (rule §17.4).
func (a *App) updateNotesLines(wrapW int32) []string {
	c := a.ctx
	lines := make([]string, 0, 32)
	for _, raw := range strings.Split(a.updateRel.Notes, "\n") {
		raw = strings.TrimRight(raw, "\r")
		if strings.TrimSpace(raw) == "" {
			lines = append(lines, "") // preserve blank spacers between sections
		} else {
			lines = append(lines, c.WrapText(raw, wrapW, updateNotesMaxLines)...)
		}
		if len(lines) >= updateNotesMaxLines {
			break
		}
	}
	return lines
}

// drawUpdateButton paints one modal button (visual only — the click is handled
// pre-screen in handleUpdateInput).
func (a *App) drawUpdateButton(r sdl.Rect, label string) {
	c := a.ctx
	bg := ColPanelHi
	if c.hovering(r) {
		bg = ColAccent
	}
	c.Fill(r, bg)
	c.Border(r, ColAccent)
	c.Label(r.X+10, r.Y+(r.H-16)/2, label, ColText)
}
