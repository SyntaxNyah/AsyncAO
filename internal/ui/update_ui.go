package ui

// Self-update UI: the launch check, the update-available chip and the What's New modal. handleUpdateInput runs before
// the screens draw because the kit has one per-frame click bool a modal must consume first.

import (
	"context"
	"os"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/update"
)

const (
	// updateChipH is the height of the persistent reopen chip (top-right).
	updateChipH int32 = 22
	// updateModalW/H size the What's New panel.
	updateModalW int32 = 560
	updateModalH int32 = 420
	// updateNotesLineH is the patch-notes line pitch.
	updateNotesLineH int32 = 18
	// updateNotesMaxLines bounds the wrapped patch notes; the overflow is on the release page.
	updateNotesMaxLines = 400
)

// maybeKickUpdateCheck fires the update probe once, on the first frame, to keep it off the boot path.
func (a *App) maybeKickUpdateCheck() {
	if a.updateChecked {
		return
	}
	a.updateChecked = true
	// Clear a leftover .old backup off the render thread, even when update checks are disabled.
	go func() {
		if exe, err := os.Executable(); err == nil {
			update.CleanupOldBundle(exe)
		}
	}()
	if !a.d.Prefs.Load().UpdateCheck {
		return
	}
	res := a.updateRes
	experimental := a.d.Prefs.Load().UpdateExperimental // read on the render thread; the goroutine only captures the bool
	go func() {
		// assetMatch picks this platform's one swappable binary; a bare GOOS match could grab a .zip bundle.
		// Failures are silent. The experimental channel follows the prerelease feed and may offer downgrades.
		var rel *update.Release
		var err error
		if experimental {
			rel, err = update.CheckExperimental(context.Background(), "", update.Version, update.SelfUpdateAssetMatch(platform.SystemProbe{}.GOOS()))
		} else {
			rel, err = update.Check(context.Background(), "", update.Version, update.SelfUpdateAssetMatch(platform.SystemProbe{}.GOOS()))
		}
		if err != nil || rel == nil {
			return
		}
		res <- rel
		PushWake() // wake the loop so pollUpdate surfaces the update chip at idle=0
	}()
}

// pollUpdate drains a found release, builds the chip label once, and opens the modal the first time.
func (a *App) pollUpdate() {
	select {
	case rel := <-a.updateRes:
		if rel == nil {
			return
		}
		a.updateRel = rel
		a.updateChipLabel = "Update " + rel.Version + " available"
		if rel.Prerelease {
			a.updateChipLabel = "Test build " + rel.Version + " available" // experimental channel: say what it is
		}
		a.updateShow = true
		a.uiDirty = true // the update chip/modal just appeared: force a redraw at idle=0
	default:
	}
	select {
	case err := <-a.updateApplyRes:
		a.updateBusy = false
		a.uiDirty = true // download/stage finished ("Restart to apply" or an error): force a redraw so the button updates at idle=0 instead of staying "Downloading…"
		if err != nil {
			a.updateErr = "Update failed: " + err.Error()
		} else {
			a.updateStaged = true // new binary in place; restart to apply
		}
	default:
	}
}

// startSelfUpdate downloads the release asset, verifies it against SHA256SUMS when published, and stages the swap
// off-thread, reporting on updateApplyRes. A bad checksum aborts; with no asset or a read-only install it opens the
// release page.
func (a *App) startSelfUpdate() {
	if a.updateBusy || a.updateStaged || a.updateRel == nil {
		return
	}
	if a.updateRel.AssetURL == "" {
		if a.updateRel.PageURL != "" {
			a.openBrowser(a.updateRel.PageURL)
		}
		a.updateErr = "This release has no downloadable build for your platform — opened the release page."
		return
	}
	exe, err := os.Executable()
	if err != nil || !update.TargetWritable(exe) {
		if a.updateRel.PageURL != "" {
			a.openBrowser(a.updateRel.PageURL)
		}
		a.updateErr = "Can't self-update here (read-only install) — opened the release page."
		return
	}
	a.updateBusy = true
	a.updateErr = ""
	url := a.updateRel.AssetURL
	sumsURL := a.updateRel.SumsURL
	assetName := a.updateRel.AssetName
	res := a.updateApplyRes
	go func() {
		defer PushWake() // wake the event-driven loop so pollUpdate surfaces the result (Restart-to-apply / error) at idle=0
		layout := platform.UpdateLayoutFor(platform.CurrentOS())
		if layout.Bundle {
			res <- applyBundleUpdate(url, sumsURL, assetName, exe, layout.LibSubdir)
			return
		}
		res <- applyBinaryUpdate(url, sumsURL, assetName, exe)
	}()
}

// applyBinaryUpdate is the single-binary path (Linux AppImage): download, verify, stage-replace.
func applyBinaryUpdate(url, sumsURL, assetName, exe string) error {
	staged := update.StagedPath(exe)
	if _, err := update.Download(context.Background(), url, staged); err != nil {
		return err
	}
	// A release without a checksum manifest (SumsURL == "") is not verified; one that ships sums must match,
	// otherwise the partial download is deleted and the error shown.
	if sumsURL != "" {
		wantHex, err := update.FetchSums(context.Background(), sumsURL, assetName)
		if err != nil {
			_ = os.Remove(staged)
			return err
		}
		if err := update.VerifyChecksum(staged, wantHex); err != nil {
			_ = os.Remove(staged)
			return err
		}
	}
	if err := update.StageReplace(staged, exe, update.BackupPath(exe)); err != nil {
		_ = os.Remove(staged)
		return err
	}
	// Unix unlinks the old exe at once; on Windows it stays locked, so the next launch's CleanupOldBundle finishes.
	update.CleanupOldVersion(exe)
	return nil
}

// applyBundleUpdate is the bundle path (Windows DLL zip, macOS tarball): download, verify, extract, stage-replace the
// binary and every runtime library. libSubdir is "" on Windows and "lib" on macOS.
func applyBundleUpdate(url, sumsURL, assetName, exe, libSubdir string) error {
	archivePath := update.StagedPath(exe) + ".archive"
	defer os.Remove(archivePath)
	if _, err := update.Download(context.Background(), url, archivePath); err != nil {
		return err
	}
	if sumsURL != "" {
		wantHex, err := update.FetchSums(context.Background(), sumsURL, assetName)
		if err != nil {
			return err
		}
		if err := update.VerifyChecksum(archivePath, wantHex); err != nil {
			return err
		}
	}
	stagingDir := update.StagedPath(exe) + ".d"
	_ = os.RemoveAll(stagingDir)
	defer os.RemoveAll(stagingDir)
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return err
	}
	if strings.HasSuffix(strings.ToLower(assetName), ".tar.gz") {
		if err := update.ExtractTarGz(archivePath, stagingDir); err != nil {
			return err
		}
	} else {
		if err := update.ExtractZip(archivePath, stagingDir); err != nil {
			return err
		}
	}
	if err := update.StageBundle(stagingDir, exe, libSubdir); err != nil {
		return err
	}
	update.CleanupOldBundle(exe)
	return nil
}

// requestRelaunch flags a relaunch and quits via SDL_QUIT; the new binary starts after the main loop exits
// and prefs are flushed, so the two instances never fight over the prefs file.
func (a *App) requestRelaunch() { a.RequestRelaunch(nil, false) }

// RequestRelaunch quits cleanly and restarts with args after state is saved.
// keepTabs saves open tabs even with restore-on-launch off, so a GPU-loss relaunch keeps the user's servers.
func (a *App) RequestRelaunch(args []string, keepTabs bool) {
	a.relaunchOnExit, a.relaunchArgs, a.relaunchKeepTabs = true, args, keepTabs
	_, _ = sdl.PushEvent(&sdl.QuitEvent{Type: sdl.QUIT})
}

// RelaunchRequest says whether the client should start again after it exits, and with which arguments.
func (a *App) RelaunchRequest() ([]string, bool) { return a.relaunchArgs, a.relaunchOnExit }

// updateChipRect is the reopen chip, top-right under the app-chrome band.
func (a *App) updateChipRect(w int32) sdl.Rect {
	cw := a.ctx.TextWidth(a.updateChipLabel) + 18
	return sdl.Rect{X: w - cw - pad, Y: a.topChromeH() + 4, W: cw, H: updateChipH}
}

// updateModalRects lays out the What's New panel, shared by the input pass and the overlay draw.
func updateModalRects(w, h int32) (panel, notes, getBtn, laterBtn sdl.Rect) {
	panel = sdl.Rect{X: (w - updateModalW) / 2, Y: (h - updateModalH) / 2, W: updateModalW, H: updateModalH}
	notes = sdl.Rect{X: panel.X + pad, Y: panel.Y + 60, W: panel.W - 2*pad, H: panel.H - 60 - 48}
	btnY := panel.Y + panel.H - 38
	getBtn = sdl.Rect{X: panel.X + pad, Y: btnY, W: 220, H: btnH}
	laterBtn = sdl.Rect{X: panel.X + panel.W - pad - 100, Y: btnY, W: 100, H: btnH}
	return panel, notes, getBtn, laterBtn
}

// handleUpdateInput consumes chip and modal clicks before the screens see them.
// The modal hit-tests with raw pointIn because updateModalFence makes hovering() false everywhere, itself included.
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
	// Consume the wheel so the list behind the scrim doesn't also scroll; WheelIn is hovering()-based and fenced.
	if c.wheelY != 0 && pointIn(c.mouseX, c.mouseY, notes) {
		a.updateScroll -= c.wheelY * scrollStepPx
		if a.updateScroll < 0 {
			a.updateScroll = 0
		}
		c.wheelY = 0
		c.wheelTaken = true
	}
	// Esc closes via closeTopOverlay.
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

// updateModalFence sets the kit's modal fence while the modal is up so nothing under the scrim reacts.
// It is released the frame after the modal closes; a stuck modalOn would freeze the whole UI.
func (a *App) updateModalFence(c *Ctx) {
	if a.updateRel != nil && a.updateShow {
		c.modalOn = true
		a.updateFenceOn = true
	} else if a.updateFenceOn {
		c.modalOn = false // modal just closed → release the persistent fence
		a.updateFenceOn = false
	}
}

// drawUpdateAvailable paints the reopen chip, or the modal when it is open.
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
	kind := "Update "
	if a.updateRel.Prerelease {
		kind = "Test build " // experimental channel — never dress a prerelease up as a stable update
	}
	c.Heading(panel.X+pad, panel.Y+10, kind+a.updateRel.Version+" available", ColText)
	c.Label(panel.X+pad, panel.Y+38, "What's new:", ColAccent)

	// Split on newlines first: WrapText collapses whitespace and would flatten the bullets.
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

// updateNotesLines wraps the release body for the modal, keeping its line breaks, capped at updateNotesMaxLines.
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

// drawUpdateButton paints one modal button; raw pointIn because hovering() is false under the modal fence.
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
