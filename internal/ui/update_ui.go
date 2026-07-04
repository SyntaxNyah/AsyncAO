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
	"os/exec"
	"runtime"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/update"
	"github.com/SyntaxNyah/AsyncAO/internal/winexec"
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
		// assetMatch identifies THIS platform's ONE swappable default binary
		// (see update.SelfUpdateAssetMatch — a bare GOOS match could grab a
		// .zip bundle and brick the self-replace). A failed or already-current
		// check is silent — the updater never nags.
		rel, err := update.Check(context.Background(), "", update.Version, update.SelfUpdateAssetMatch(runtime.GOOS))
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
	select {
	case err := <-a.updateApplyRes:
		a.updateBusy = false
		if err != nil {
			a.updateErr = "Update failed: " + err.Error()
		} else {
			a.updateStaged = true // new binary in place; restart to apply
		}
	default:
	}
}

// startSelfUpdate downloads the release asset next to the running exe, verifies
// it, and stages the swap — all off-thread, reporting on updateApplyRes. It
// degrades to the release page when there's no downloadable asset or the
// install dir is read-only (Program Files without elevation). No-op while a
// previous run is in flight or already staged.
func (a *App) startSelfUpdate() {
	if a.updateBusy || a.updateStaged || a.updateRel == nil {
		return
	}
	if a.updateRel.AssetURL == "" {
		if a.updateRel.PageURL != "" {
			openBrowser(a.updateRel.PageURL)
		}
		a.updateErr = "This release has no downloadable build for your platform — opened the release page."
		return
	}
	exe, err := os.Executable()
	if err != nil || !update.TargetWritable(exe) {
		if a.updateRel.PageURL != "" {
			openBrowser(a.updateRel.PageURL)
		}
		a.updateErr = "Can't self-update here (read-only install) — opened the release page."
		return
	}
	a.updateBusy = true
	a.updateErr = ""
	url := a.updateRel.AssetURL
	res := a.updateApplyRes
	go func() {
		staged := update.StagedPath(exe)
		if _, err := update.Download(context.Background(), url, staged); err != nil {
			res <- err
			return
		}
		// Integrity check is plumbed (update.VerifyChecksum); no published
		// checksum source yet, so it's skipped until releases ship one.
		if err := update.StageReplace(staged, exe, update.BackupPath(exe)); err != nil {
			_ = os.Remove(staged)
			res <- err
			return
		}
		// Uninstall the old binary now. On Unix the renamed-away old exe unlinks
		// immediately (the running process keeps its open inode); on Windows it is
		// still locked, so this no-ops and the next-launch CleanupOldVersion
		// finishes the job — a running .exe can't delete itself.
		update.CleanupOldVersion(exe)
		res <- nil
	}()
}

// requestRelaunch is the "Restart to apply" action: flag a relaunch and quit
// cleanly via SDL_QUIT. The new binary starts AFTER the main loop exits and
// prefs/tabs are flushed (see MaybeRelaunch), so the two instances never fight
// over the prefs file or the window.
func (a *App) requestRelaunch() {
	a.relaunchOnExit = true
	_, _ = sdl.PushEvent(&sdl.QuitEvent{Type: sdl.QUIT})
}

// MaybeRelaunch starts the freshly-installed binary if "Restart to apply" was
// clicked. Called by main AFTER the run loop exits and state is saved.
func (a *App) MaybeRelaunch() {
	if !a.relaunchOnExit {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe)
	winexec.Hide(cmd) // dev builds are console-subsystem; no console flash on relaunch
	_ = cmd.Start()
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
// While the modal is up it hit-tests with raw pointIn, NOT hovering():
// updateModalFence holds the kit's modal fence (modalOn) so everything
// underneath is pointer-blind on every screen — hovering() is false for the
// modal itself too, by design (the same discipline as the emoji picker).
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
	// Raw wheel consume: scroll the notes AND zero the frame's wheel, so the
	// list behind the scrim can't also scroll ("scrolling the changelog
	// scrolled the server list") — WheelIn is hovering()-based and modal-fenced.
	if c.wheelY != 0 && pointIn(c.mouseX, c.mouseY, notes) {
		a.updateScroll -= c.wheelY * scrollStepPx
		if a.updateScroll < 0 {
			a.updateScroll = 0
		}
		c.wheelY = 0
		c.wheelTaken = true
	}
	// (Esc closes via closeTopOverlay — the app-level escPressed handler.)
	if !c.clicked {
		return
	}
	switch {
	case pointIn(c.mouseX, c.mouseY, getBtn):
		if a.updateStaged {
			a.requestRelaunch() // "Restart to apply": relaunch into the new binary
		} else if !a.updateBusy {
			a.startSelfUpdate() // download, verify, staged swap (or degrade)
		}
	case pointIn(c.mouseX, c.mouseY, laterBtn):
		a.updateShow = false
	case !pointIn(c.mouseX, c.mouseY, panel):
		a.updateShow = false // click off the panel dismisses
	}
	c.clicked = false // the modal owns this frame's click
}

// updateModalFence holds the kit's modal fence while the What's New modal is
// up: hovering() reads false for EVERY widget on every screen, so nothing
// under the scrim reacts — no wheel scroll (WheelIn), no hover highlights, no
// scrollbar/slider drags. Released the frame after the modal closes, exactly
// like emojiPickerFence (an un-released modalOn would freeze the whole UI).
// The modal's own interaction runs on raw pointIn (handleUpdateInput /
// drawUpdateButton), so the fence never blinds it.
func (a *App) updateModalFence(c *Ctx) {
	if a.updateRel != nil && a.updateShow {
		c.modalOn = true
		a.updateFenceOn = true
	} else if a.updateFenceOn {
		c.modalOn = false // modal just closed → release the persistent fence
		a.updateFenceOn = false
	}
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

	// Status line above the buttons reflects the apply flow.
	statusY := getBtn.Y - 20
	switch {
	case a.updateErr != "":
		c.LabelClipped(panel.X+pad, statusY, panel.W-2*pad, a.updateErr, ColDanger)
	case a.updateStaged:
		c.LabelClipped(panel.X+pad, statusY, panel.W-2*pad, "Installed - restart AsyncAO to finish updating.", ColAccent)
	case a.updateBusy:
		c.Label(panel.X+pad, statusY, "Downloading and installing...", ColTextDim)
	}
	getLabel := "Get the update"
	switch {
	case a.updateBusy:
		getLabel = "Downloading..."
	case a.updateStaged:
		getLabel = "Restart to apply"
	}
	a.drawUpdateButton(getBtn, getLabel)
	a.drawUpdateButton(laterBtn, "Close")
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
// pre-screen in handleUpdateInput). Raw pointIn for the hover tint: the modal
// holds the modalOn fence, under which hovering() is false everywhere.
func (a *App) drawUpdateButton(r sdl.Rect, label string) {
	c := a.ctx
	bg := ColPanelHi
	if pointIn(c.mouseX, c.mouseY, r) {
		bg = ColAccent
	}
	c.Fill(r, bg)
	c.Border(r, ColAccent)
	c.Label(r.X+10, r.Y+(r.H-16)/2, label, ColText)
}
