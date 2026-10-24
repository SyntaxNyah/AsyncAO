package ui

// M13 self-update, UI side: the one-shot launch check, the persistent
// "update available" chip, and the What's New patch-notes modal. "Get the
// update" runs the full self-replace in-app (startSelfUpdate, wired to the
// modal button): download the platform asset next to the running exe → verify
// it against the release's SHA256SUMS manifest when published → stage-replace
// the binary (rename-with-rollback) → relaunch into the new build on the next
// quit (requestRelaunch → MaybeRelaunch). It degrades to opening the release
// page in the browser only when the release has no matching asset or the
// install dir is read-only (the low-level pieces live in internal/update).
// Input is handled in handleUpdateInput BEFORE the screens draw (the kit has
// one per-frame click bool, so a modal must consume it first — same pattern as
// the tab strip); drawUpdateAvailable only paints, in the overlay phase, using

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

// selfUpdate is the M13 self-update component: the one-shot launch check, the
// persistent "update available" chip, and the What's New patch-notes modal.
// Its env pointer is wired to &App.env once in NewApp, so the component reaches
// the shared deps/ctx/notice/navigation through one pointer.
// Field names are deliberately short and unprefixed: INSIDE the component the
// receiver already says whose state it is (s.rel, s.busy). From App the access
// is spelled a.updates.rel — one hop, no promotion, so a reader can tell
type selfUpdate struct {
	env *env

	res            chan *update.Release // the one-shot check's result channel
	checked        bool                 // the check has been kicked (fires exactly once)
	rel            *update.Release      // the release the check found (nil = none)
	show           bool                 // the What's New modal is open
	fenceOn        bool                 // WE hold the modal fence for the open modal (released on close — see updateModalFence)
	scroll         int32                // patch-notes scroll offset (px)
	chipLabel      string               // the persistent chip's text, built once so the draw never allocates
	applyRes       chan error           // the download/verify/stage run's result
	busy           bool                 // a download/verify/stage run is in flight
	staged         bool                 // the new binary is in place; restart to apply
	lastErr        string               // the last failure to surface under the buttons ("" = none)
	relaunchOnExit bool                 // "Restart to apply": relaunch the new binary after a clean shutdown
}

// fenceHeld reports whether the update modal holds the app-wide modal fence.
// App-side modal accounting asks this instead of reading the field, so the
// component owns the bit (and the pairing count stays at zero).
func (s *selfUpdate) fenceHeld() bool { return s.fenceOn }

// visible reports whether the What's New modal is open (and therefore owns Esc).
func (s *selfUpdate) visible() bool { return s.show }

// closeModal closes the What's New modal; the Esc chain calls it by behaviour.
func (s *selfUpdate) closeModal() { s.show = false }

// resetForChannel drops a stale offer after the update channel changed: re-arm
// the one-shot launch check and close the modal, so the other channel's build
// cannot be pushed at the user.
func (s *selfUpdate) resetForChannel() {
	s.checked = false
	s.rel = nil
	s.show = false
}

// wireComponents points every component's env at App's own env, and gives that
// env its host capabilities (a redraw request and the chrome band), so a
// component reaches shared state through one pointer. NewApp calls it once
// (and headless rigs call it directly); nothing is reassigned afterwards.
func (a *App) wireComponents() {
	a.env.h = a
	a.wireComponentEnvs()
}

// wireComponentEnvs re-points the components' env. resetSessionState installs a
// fresh sessionState, which zeroes every component parked inside it, so the
// pointers have to be restored after each park/restore as well as at boot.
func (a *App) wireComponentEnvs() {
	a.updates.env = &a.env
	a.classic.env = &a.env
	a.lobby.env = &a.env
	a.palette.env = &a.env
	a.client.env = &a.env
	a.sceneWarm.env = &a.env
	a.activeWarm.env = &a.env
	a.toolbox.env = &a.env
	a.banBox.env = &a.env
	a.msgs.env = &a.env
	a.overlays.env = &a.env
	a.docs.env = &a.env
	a.jukebox.env = &a.env
	a.classic.env = &a.env
}

// maybeKickUpdateCheck fires the update probe EXACTLY ONCE, on the first frame
// (the window is already up), so the check never touches the boot path. A
// disabled pref short-circuits it, and Check itself skips an unstamped dev
// build, so neither hits the network.
func (s *selfUpdate) maybeKickUpdateCheck() {
	if s.checked {
		return
	}
	s.checked = true
	// Clear a leftover .old backup from a previous self-update — off the render
	// thread (disk I/O) and off the boot path (frame 1), regardless of the
	// check pref (you want the stale binary gone even with checks disabled).
	go func() {
		if exe, err := os.Executable(); err == nil {
			update.CleanupOldBundle(exe)
		}
	}()
	if !s.env.d.Prefs.Load().UpdateCheck {
		return
	}
	res := s.res
	experimental := s.env.d.Prefs.Load().UpdateExperimental // read on the render thread; the goroutine only captures the bool
	go func() {
		// assetMatch identifies THIS platform's ONE swappable default binary
		// (see update.SelfUpdateAssetMatch — a bare GOOS match could grab a
		// .zip bundle and brick the self-replace). A failed or already-current
		// check is silent — the updater never nags. The experimental channel
		// (Power user) follows the prerelease/test-branch feed instead, and
		// may offer sideways/downgrade builds — that's how you hop back off.
		var rel *update.Release
		var err error
		if experimental {
			rel, err = update.CheckExperimental(context.Background(), "", update.Version, update.SelfUpdateAssetMatch(runtime.GOOS))
		} else {
			rel, err = update.Check(context.Background(), "", update.Version, update.SelfUpdateAssetMatch(runtime.GOOS))
		}
		if err != nil || rel == nil {
			return
		}
		res <- rel
		PushWake() // wake the loop so pollUpdate surfaces the update chip at idle=0
	}()
}

// pollUpdate drains a found release: it stores it, builds the chip label once
// (so the per-frame draw never allocates), and auto-opens the modal the first
// time so the patch notes are surfaced without the user hunting for them.
func (s *selfUpdate) pollUpdate() {
	select {
	case rel := <-s.res:
		if rel == nil {
			return
		}
		s.rel = rel
		s.chipLabel = "Update " + rel.Version + " available"
		if rel.Prerelease {
			s.chipLabel = "Test build " + rel.Version + " available" // experimental channel: say what it is
		}
		s.show = true
		s.env.dirty() // the update chip/modal just appeared: force a redraw at idle=0
	default:
	}
	select {
	case err := <-s.applyRes:
		s.busy = false
		s.env.dirty() // download/stage finished ("Restart to apply" or an error): force a redraw so the button updates at idle=0 instead of staying "Downloading…"
		if err != nil {
			s.lastErr = "Update failed: " + err.Error()
		} else {
			s.staged = true // new binary in place; restart to apply
		}
	default:
	}
}

// startSelfUpdate downloads the release asset next to the running exe, verifies
// it against the release's SHA256SUMS manifest WHEN one was published (releases
// cut before checksums shipped have none and proceed unverified but
// integrity-capped), and stages the swap — all off-thread, reporting on
// applyRes. A checksum mismatch (or a failure to fetch/find the entry on a
// release that ships sums) aborts the update and surfaces an error rather than
// installing an unverified binary. It degrades to the release page when there's
// no downloadable asset or the install dir is read-only (Program Files without
// elevation). No-op while a previous run is in flight or already staged.
func (s *selfUpdate) startSelfUpdate() {
	if s.busy || s.staged || s.rel == nil {
		return
	}
	if s.rel.AssetURL == "" {
		if s.rel.PageURL != "" {
			openBrowser(s.rel.PageURL)
		}
		s.lastErr = "This release has no downloadable build for your platform — opened the release page."
		return
	}
	exe, err := os.Executable()
	if err != nil || !update.TargetWritable(exe) {
		if s.rel.PageURL != "" {
			openBrowser(s.rel.PageURL)
		}
		s.lastErr = "Can't self-update here (read-only install) — opened the release page."
		return
	}
	s.busy = true
	s.lastErr = ""
	url := s.rel.AssetURL
	sumsURL := s.rel.SumsURL
	assetName := s.rel.AssetName
	res := s.applyRes
	go func() {
		defer PushWake() // wake the event-driven loop so pollUpdate surfaces the result (Restart-to-apply / error) at idle=0
		var err error
		switch runtime.GOOS {
		case "windows":
			// Windows self-updates via the DLL bundle (exe + runtime DLLs beside
			// it), so a new engine ships with its libraries.
			err = applyBundleUpdate(url, sumsURL, assetName, exe, "")
		case "darwin":
			// macOS self-updates via the bundle tarball (binary + lib/), so a
			// SONAME bump no longer strands a bare binary against a stale lib/.
			err = applyBundleUpdate(url, sumsURL, assetName, exe, "lib")
		default:
			// Linux ships a self-contained AppImage — a single-file swap is the
			// whole update.
			err = applyBinaryUpdate(url, sumsURL, assetName, exe)
		}
		res <- err
	}()
}

// applyBinaryUpdate is the single-binary self-update path (Linux AppImage,
// macOS bare binary): download the one asset, verify it against the release's
// SHA256SUMS manifest when published, and stage-replace the running binary.
func applyBinaryUpdate(url, sumsURL, assetName, exe string) error {
	staged := update.StagedPath(exe)
	if _, err := update.Download(context.Background(), url, staged); err != nil {
		return err
	}
	// Verify the download against the release's SHA256SUMS manifest when one
	// was published. Releases cut before checksums shipped carry no manifest
	// (SumsURL == ""): we skip verification and proceed exactly as before — the
	// download is still integrity-capped, just not sum-checked. A release that
	// DOES ship sums gets enforced: any fetch/lookup/mismatch failure aborts,
	// deletes the partial download, and surfaces the error in the modal.
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
	// Uninstall the old binary now. On Unix the renamed-away old exe unlinks
	// immediately (the running process keeps its open inode); on Windows it is
	// still locked, so this no-ops and the next-launch CleanupOldBundle
	// finishes the job — a running .exe can't delete itself.
	update.CleanupOldVersion(exe)
	return nil
}

// applyBundleUpdate is the bundle self-update (Windows DLL zip, macOS binary +
// lib/ tarball): download the archive, verify it, extract it, and stage-replace
// the binary plus every runtime library (see update.StageBundle). libSubdir is
// "" on Windows (DLLs beside the exe) and "lib" on macOS (dylibs). This fixes
// the v1.95.1 class of breakage, where a bare-binary swap updated the executable
// but left stale engine libraries beside it so the new build couldn't launch.
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

// requestRelaunch is the "Restart to apply" action: flag a relaunch and quit
// cleanly via SDL_QUIT. The new binary starts AFTER the main loop exits and
// prefs/tabs are flushed (see MaybeRelaunch), so the two instances never fight
// over the prefs file or the window.
func (s *selfUpdate) requestRelaunch() {
	s.relaunchOnExit = true
	_, _ = sdl.PushEvent(&sdl.QuitEvent{Type: sdl.QUIT})
}

// MaybeRelaunch starts the freshly-installed binary if "Restart to apply" was
// clicked. Called by main AFTER the run loop exits and state is saved.
func (s *selfUpdate) MaybeRelaunch() {
	if !s.relaunchOnExit {
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

// MaybeRelaunch is the exported entry point cmd/asyncao calls AFTER the run
// loop exits and state is saved: a one-line delegation so the self-update
// component stays unexported while App keeps the public surface main needs.
func (a *App) MaybeRelaunch() { a.updates.MaybeRelaunch() }

// updateChipRect is the persistent reopen chip, top-right and just under the
// app-chrome band (#14, chrometop.go) — it used to be a bare tabBarH+4, which the
// docked server-tab strip now sits on top of.
func (s *selfUpdate) updateChipRect(w int32) sdl.Rect {
	cw := s.env.ctx.TextWidth(s.chipLabel) + 18
	return sdl.Rect{X: w - cw - pad, Y: s.env.chromeTop() + 4, W: cw, H: updateChipH}
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

// pumpInput is the component's per-frame front half, in the ONE order that is
// correct: kick the one-shot check (frame 1 only), drain a result, then let the
// chip/modal consume this frame's click BEFORE the screens see it. App calls
// this once per frame instead of knowing each phase — the order is the
// component's business, not the frame loop's.
func (s *selfUpdate) pumpInput(w, h int32) {
	s.maybeKickUpdateCheck() // one-shot, off the boot path (fires on frame 1)
	s.pollUpdate()           // drain a found release
	s.handleUpdateInput(w, h)
}

// handleUpdateInput consumes the chip / modal interaction before the screens
// see this frame's click. No-op until the one-shot check found a release.
// While the modal is up it hit-tests with raw pointIn, NOT hovering():
// updateModalFence holds the kit's modal fence (modalOn) so everything
// underneath is pointer-blind on every screen — hovering() is false for the
// modal itself too, by design (the same discipline as the emoji picker).
func (s *selfUpdate) handleUpdateInput(w, h int32) {
	if s.rel == nil {
		return
	}
	c := s.env.ctx
	if !s.show {
		if chip := s.updateChipRect(w); c.hovering(chip) && c.clicked {
			s.show = true
			c.clicked = false
		}
		return
	}
	panel, notes, getBtn, laterBtn := updateModalRects(w, h)
	// Raw wheel consume: scroll the notes AND zero the frame's wheel, so the
	// list behind the scrim can't also scroll ("scrolling the changelog
	// scrolled the server list") — WheelIn is hovering()-based and modal-fenced.
	if c.wheelY != 0 && pointIn(c.mouseX, c.mouseY, notes) {
		s.scroll -= c.wheelY * scrollStepPx
		if s.scroll < 0 {
			s.scroll = 0
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
		if s.staged {
			s.requestRelaunch() // "Restart to apply": relaunch into the new binary
		} else if !s.busy {
			s.startSelfUpdate() // download, verify, staged swap (or degrade)
		}
	case pointIn(c.mouseX, c.mouseY, laterBtn):
		s.show = false
	case !pointIn(c.mouseX, c.mouseY, panel):
		s.show = false // click off the panel dismisses
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
func (s *selfUpdate) updateModalFence(c *Ctx) {
	if s.rel != nil && s.show {
		c.modalOn = true
		s.fenceOn = true
	} else if s.fenceOn {
		c.modalOn = false // modal just closed → release the persistent fence
		s.fenceOn = false
	}
}

// drawUpdateAvailable paints the M13 affordances over every screen: the
// persistent reopen chip when the modal is closed, else the modal itself.
func (s *selfUpdate) drawUpdateAvailable(w, h int32) {
	if s.rel == nil {
		return
	}
	c := s.env.ctx
	if !s.show {
		chip := s.updateChipRect(w)
		c.Fill(chip, ColAccent)
		c.Label(chip.X+9, chip.Y+4, s.chipLabel, ColBackground)
		return
	}

	panel, notes, getBtn, laterBtn := updateModalRects(w, h)
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{A: 150}) // scrim
	c.Fill(panel, sdl.Color{R: 12, G: 12, B: 18, A: 245})
	c.Border(panel, ColAccent)
	kind := "Update "
	if s.rel.Prerelease {
		kind = "Test build " // experimental channel — never dress a prerelease up as a stable update
	}
	c.Heading(panel.X+pad, panel.Y+10, kind+s.rel.Version+" available", ColText)
	c.Label(panel.X+pad, panel.Y+38, "What's new:", ColAccent)

	// Patch notes: split on newlines FIRST (WrapText collapses whitespace and
	// would flatten the bullet structure), then wrap each line to the panel.
	lines := s.updateNotesLines(notes.W)
	contentH := int32(len(lines)) * updateNotesLineH
	maxScroll := contentH - notes.H
	if maxScroll < 0 {
		maxScroll = 0
	}
	if s.scroll > maxScroll {
		s.scroll = maxScroll
	}
	clipPrev, clipHad := c.pushClip(notes)
	y := notes.Y - s.scroll
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
	case s.lastErr != "":
		c.LabelClipped(panel.X+pad, statusY, panel.W-2*pad, s.lastErr, ColDanger)
	case s.staged:
		c.LabelClipped(panel.X+pad, statusY, panel.W-2*pad, "Installed - restart AsyncAO to finish updating.", ColAccent)
	case s.busy:
		c.Label(panel.X+pad, statusY, "Downloading and installing...", ColTextDim)
	}
	getLabel := "Get the update"
	switch {
	case s.busy:
		getLabel = "Downloading..."
	case s.staged:
		getLabel = "Restart to apply"
	}
	s.drawUpdateButton(getBtn, getLabel)
	s.drawUpdateButton(laterBtn, "Close")
}

// updateNotesLines wraps the release body for the modal, preserving its line
// breaks and bounding the total (rule §17.4).
func (s *selfUpdate) updateNotesLines(wrapW int32) []string {
	c := s.env.ctx
	lines := make([]string, 0, 32)
	for _, raw := range strings.Split(s.rel.Notes, "\n") {
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
func (s *selfUpdate) drawUpdateButton(r sdl.Rect, label string) {
	c := s.env.ctx
	bg := ColPanelHi
	if pointIn(c.mouseX, c.mouseY, r) {
		bg = ColAccent
	}
	c.Fill(r, bg)
	c.Border(r, ColAccent)
	c.Label(r.X+10, r.Y+(r.H-16)/2, label, ColText)
}
