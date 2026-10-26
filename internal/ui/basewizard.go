package ui

// Settings ▸ Assets ▸ "Use my own AO files…" — the guided way to point AsyncAO
// at a base folder of your own (issue #72).
//
// WHY A WIZARD RATHER THAN THE ROWS UNDER IT. The controls it fronts are all
// still there, and they are correct; they are just not a thing a sprite maker
// can act on. Setting this up used to mean reading three radio labels, deciding
// between them before knowing what any of them would do, typing an absolute path
// into a text field with no picker, pressing Add, and then noticing — or not —
// a yellow line further down saying the folder was configured but not being
// used. Nothing on that page ever told the user what was in the folder they
// named, so a typo, a folder one level too high, and a correct setup all looked
// the same.
//
// So the panel keeps ONE button, and everything else happens here: pick, see
// what was found, choose how it is used, apply. The order matters — the mode
// choice comes AFTER the report, because "never stream" is only a safe answer
// once you can see the folder actually holds a base.
//
// It changes nothing until "Use this folder". Cancel is always a full no-op:
// browsing, scanning and re-picking are all read-only, which is what makes the
// report worth showing before the commit rather than after it.

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

const (
	// baseScanResCap is the wizard's scan result channel. Exactly one scan runs at
	// a time (baseWizardState.scanning), so a cap of 1 can never block the scan
	// goroutine or drop a result — the same bargain demobrowser's browseResCap
	// strikes. Named per rule 4.
	baseScanResCap = 1
	// The modal's geometry. Clamped against the window like the file browser's, so
	// it is still usable on a small one.
	baseWizMaxW = 620
	baseWizMaxH = 520
	baseWizMinW = 340
	baseWizMinH = 260
	// baseWizRowH is the pitch of one wrapped help line, matching settingsDesc.
	baseWizRowH = 16
)

// baseWizardState is package-level like `settings` and `demoBrowser`: the
// Settings screen is single-instance, so a second wizard cannot exist.
type baseWizardState struct {
	open bool
	// path is the folder the user has picked in this run ("" = none yet). It
	// survives close, so re-opening to change the mode does not make the user find
	// their folder again.
	path string
	// scan is the report for path, valid only once scanned. Tracked with a flag
	// rather than inferred from the counts, because an empty folder is a real
	// answer whose counts are all zero.
	scan    baseScan
	scanned bool
	// scanning is the single-flight latch: at most ONE scan goroutine exists
	// (rule 4). A pick made while a scan runs is not lost — pollBaseScan re-kicks
	// when the superseded result lands.
	scanning bool
	res      chan baseScan
	// mode is the asset-source mode "Use this folder" will apply: assetSrcLayered
	// or assetSrcLocal. Never assetSrcStream — this surface exists to turn the
	// user's folder ON, and "stream everything" is the Advanced rows' business.
	mode int
	// note is a refusal shown in place (the mount cap). Kept here rather than on
	// settings.statusLine because the status line draws BEHIND this modal.
	note string
}

var baseWizard = baseWizardState{res: make(chan baseScan, baseScanResCap), mode: assetSrcLayered}

// openBaseWizard opens the modal, seeded from the current setup.
//
// Seeding matters more than it looks: re-opening this to CHANGE something is at
// least as common as a first run, and starting blank would make a user who only
// wants to flip "never stream" go and find their folder on disk again.
func (a *App) openBaseWizard() {
	w := &baseWizard
	w.open = true
	w.note = ""
	if w.mode = a.assetSourceMode(); w.mode == assetSrcStream {
		w.mode = assetSrcLayered
	}
	if _, mounts := a.d.Prefs.LocalAssets(); w.path == "" && len(mounts) > 0 {
		w.setBasePath(mounts[0]) // first-hit-wins order: mount 1 is the one in charge
	}
}

// setBasePath records a pick and starts its scan. The report is dropped
// immediately so a stale one cannot be read as describing the new folder.
func (w *baseWizardState) setBasePath(path string) {
	w.path = strings.TrimSpace(path)
	w.scan, w.scanned, w.note = baseScan{}, false, ""
	w.kickScan()
}

// kickScan starts one bounded scan goroutine for w.path, or does nothing if one
// is already running. The pick is never lost by that refusal: the in-flight
// result is tagged with the path it scanned, and pollBaseScan re-kicks when a
// superseded one lands — the same reconcile-on-landing shape applyMountLayer uses
// for the mount index, and the reason neither needs a queue.
//
// BOTH the path and the channel are passed as arguments, so the goroutine holds
// no reference to w at all and the only shared object left is the channel itself.
// Reaching through w for the channel instead is a data race the moment anything
// reassigns the struct, which is exactly what the race detector caught.
func (w *baseWizardState) kickScan() {
	if w.scanning || w.path == "" {
		return
	}
	w.scanning = true
	go func(p string, res chan baseScan) { res <- scanBaseFolder(p) }(w.path, w.res)
}

// pollBaseScan lands a finished scan. Render thread, drained from the wizard's
// own draw; never blocks and never touches the disk itself.
func (w *baseWizardState) pollBaseScan() {
	select {
	case s := <-w.res:
		w.scanning = false
		if s.path != w.path {
			w.kickScan() // the user picked again while this scan ran
			return
		}
		w.scan, w.scanned = s, true
	default:
	}
}

// baseWizardTakesDrop gives the OPEN wizard first refusal on a dropped path,
// reporting whether it consumed the drop.
//
// A modal that says "or drop a folder here" has to mean it. Without this the
// drop falls through to the Settings screen's default arm, which points the
// user's THEME ROOT at the dropped folder — the exact silent-wrong-action class
// dropclaim.go was written to end, arriving by a different door.
//
// Global claims still win: a .aotheme or a recording dropped on the open wizard
// belongs to HandleFileDrop, and stealing it here would resurrect the two-owners
// bug from the other side. Anything unclaimed is a folder pick, including a .zip,
// which is a mountable pack.
func (a *App) baseWizardTakesDrop(path string) bool {
	if !baseWizard.open || claimDroppedFile(path, settings.importArmed) != dropClaimNone {
		return false
	}
	resolveDroppedBasePath(path)
	return true
}

// resolveDroppedBasePath turns an SDL drop into a pick, off-thread.
//
// A dropped FILE means its folder, matching resolveDroppedFolder and the rest of
// the app — except for a .zip, which is a mountable pack in its own right and
// whose parent folder is emphatically not what the user meant. The stat is why
// this is a goroutine: a drop from a network share must not stall the frame.
func resolveDroppedBasePath(path string) {
	w := &baseWizard
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		w.setBasePath(path)
		return
	}
	// Reuses the scan goroutine's own single-flight slot: scanBaseFolder already
	// answers "that is a file, not a folder", so a dropped file resolves to its
	// directory here and the scan reports whatever that directory turns out to be.
	if !isDir(path) {
		path = filepath.Dir(path)
	}
	w.setBasePath(path)
}

// applyBaseWizard commits the pick: the folder becomes mount 1 and the chosen
// mode is applied. The ONLY function in this file that writes anything.
//
// FIRST in the list, because mounts are searched first-hit-wins — the folder the
// user just pointed at and inspected is the one that should win a tie. An exact
// re-pick moves it to the front instead of adding a duplicate.
func (a *App) applyBaseWizard() {
	w := &baseWizard
	if w.path == "" {
		w.note = "Pick a folder first."
		return
	}
	_, mounts := a.d.Prefs.LocalAssets()
	next := make([]string, 0, len(mounts)+1)
	next = append(next, w.path)
	for _, m := range mounts {
		if m != w.path {
			next = append(next, m)
		}
	}
	if len(next) > config.LocalMountCap {
		w.note = "That would be " + strconv.Itoa(len(next)) + " folders and the limit is " +
			strconv.Itoa(config.LocalMountCap) + ". Remove one under Advanced first."
		return
	}
	a.setAssetSourceMode(w.mode, next)
	w.open = false
	settings.statusLine = baseWizardApplied(w.path, w.mode)
}

// baseWizardApplied is the line the Settings page shows after the modal closes.
// A named function so the two modes cannot end up describing each other, and so
// the wording is testable without a window.
func baseWizardApplied(path string, mode int) string {
	if mode == assetSrcLocal {
		return "Using " + path + " only. Nothing will stream from the server."
	}
	return "Using " + path + " first, then streaming anything it doesn't have."
}

// drawBaseWizard draws the modal LAST in drawSettings (topmost) with the page's
// modal fence released, like the file browser. No-op when closed.
func (a *App) drawBaseWizard(w, h int32) {
	s := &baseWizard
	if !s.open {
		return
	}
	c := a.ctx
	// The in-app file browser opens ON TOP of this panel (step 1's Browse…), so
	// while it is up every widget here must go click-dead — otherwise the click
	// that picks a browser row would also fire whatever wizard control happens to
	// sit under it. Restored on the way out so the browser's own draw is unfenced:
	// the app.go release-and-restore idiom, deferred because this function returns
	// from three places.
	fenced := c.modalOn
	c.modalOn = fenced || demoBrowser.open
	defer func() { c.modalOn = fenced }()
	s.pollBaseScan()

	pw := clampI32(w-2*browseModalMargin, baseWizMinW, baseWizMaxW)
	ph := clampI32(h-2*browseModalMargin, baseWizMinH, baseWizMaxH)
	px, py := (w-pw)/2, (h-ph)/2
	panel := sdl.Rect{X: px, Y: py, W: pw, H: ph}
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)

	inX, inW := px+14, pw-28
	y := py + 12

	c.Label(inX, y, "Use your own AO files", ColText)
	if c.Button(sdl.Rect{X: px + pw - 30, Y: py + 8, W: 22, H: 22}, "✕") {
		s.open = false
		return
	}
	y += 24
	y = a.baseWizText(inX, y, inW,
		"Point AsyncAO at a copy of an AO base folder on this computer. Your files are read straight off "+
			"the disk, are never uploaded anywhere, and are never written into the streaming cache.", ColTextDim)
	y += 8

	y = a.baseWizStepPick(inX, y, inW)
	y = a.baseWizStepFound(inX, y, inW)
	y = a.baseWizStepMode(inX, y, inW)

	if s.note != "" {
		// Return value dropped on purpose: the buttons below are pinned to the
		// panel, so this is the last thing that flows with the content.
		a.baseWizText(inX, y+4, inW, s.note, ColDanger)
	}

	// Buttons pinned to the panel's bottom edge, so they do not walk up and down
	// as the report above them grows and shrinks.
	by := py + ph - btnH - 12
	if c.Button(sdl.Rect{X: px + pw - 250, Y: by, W: 100, H: btnH}, "Cancel") {
		s.open = false
		s.note = ""
		return
	}
	if c.Button(sdl.Rect{X: px + pw - 140, Y: by, W: 128, H: btnH}, "Use this folder") {
		a.applyBaseWizard()
	}
}

// baseWizStepPick is step 1: browse, drop, or fix a near-miss.
func (a *App) baseWizStepPick(x, y, w int32) int32 {
	c := a.ctx
	s := &baseWizard
	c.Label(x, y+4, "1. Pick your base folder", ColText)
	y += 22
	// The IN-APP browser, not a native dialog. The native folder picker is a
	// PowerShell shell-out that only exists on Windows and has failed to win
	// foreground live before (settings.go's browseForFolder says so in its own
	// comment, and names this as the escalation path).
	if c.Button(sdl.Rect{X: x + 16, Y: y, W: 110, H: btnH}, "Browse…") {
		a.openDemoBrowserFor(purposeBaseFolder)
	}
	c.Label(x+134, y+4, "or drag the folder onto this window", ColTextDim)
	y += btnH + 6
	if s.path == "" {
		c.Label(x+16, y, "No folder picked yet.", ColTextDim)
		return y + baseWizRowH + 6
	}
	c.LabelClipped(x+16, y, w-16, s.path, ColAccent)
	y += baseWizRowH + 4
	// The near-miss correction. It is a BUTTON rather than a sentence because the
	// two mistakes it catches (the install root, the characters/ folder itself)
	// are both one click from right, and a user who has just been told their pick
	// is wrong should not have to go back through the browser to act on it.
	if s.scanned && s.scan.suggest != "" {
		if c.Button(sdl.Rect{X: x + 16, Y: y, W: 150, H: btnH}, "Use that one instead") {
			s.setBasePath(s.scan.suggest)
		}
		c.LabelClipped(x+172, y+4, w-172, s.scan.suggest, ColTierYellow)
		y += btnH + 6
	}
	return y + 4
}

// baseWizStepFound is step 2: what the scan actually found, and every warning
// that follows from it.
func (a *App) baseWizStepFound(x, y, w int32) int32 {
	c := a.ctx
	s := &baseWizard
	c.Label(x, y+4, "2. What we found", ColText)
	y += 22
	switch {
	case s.path == "":
		c.Label(x+16, y, "Pick a folder and this will fill in.", ColTextDim)
		return y + baseWizRowH + 8
	case s.scanning || !s.scanned:
		c.Label(x+16, y, "Looking…", ColTextDim)
		return y + baseWizRowH + 8
	case s.scan.err != "":
		y = a.baseWizText(x+16, y, w-16, "Can't read this: "+s.scan.err, ColDanger)
		return y + 8
	}
	for _, ln := range baseScanReport(s.scan) {
		c.LabelClipped(x+16, y, w-16, ln, ColTextDim)
		y += baseWizRowH
	}
	for _, warn := range baseScanWarnings(s.scan) {
		y = a.baseWizText(x+16, y+2, w-16, warn, ColTierYellow)
	}
	return y + 8
}

// baseWizStepMode is step 3: how the folder is used. Radio semantics over
// checkbox rows, matching the Advanced rows this fronts.
func (a *App) baseWizStepMode(x, y, w int32) int32 {
	c := a.ctx
	s := &baseWizard
	c.Label(x, y+4, "3. How to use it", ColText)
	y += 22
	for _, opt := range []struct {
		mode  int
		label string
	}{
		{assetSrcLayered, "Mine first, then stream the rest"},
		{assetSrcLocal, "Mine only, never stream"},
	} {
		on := s.mode == opt.mode
		if next := c.Checkbox(x+16, y, opt.label, on); next != on && !on {
			s.mode = opt.mode // a radio group has no "none" state: only selecting moves it
		}
		y += 24
	}
	// The warning, and it is the reason the mode choice sits below the report:
	// "never stream" is a promise about everything the folder does NOT hold.
	if s.mode == assetSrcLocal {
		return a.baseWizText(x+16, y+2, w-16,
			"Anything this folder is missing will not load at all — no sprite, no background, no music. "+
				"Pick this only for servers that provide no asset URL of their own.", ColTierYellow) + 6
	}
	return a.baseWizText(x+16, y+2, w-16,
		"Files you have replace the server's at the same path; everything else still streams. "+
			"This is the safe choice for testing your own work on a normal server.", ColTextDim) + 6
}

// baseWizText draws a wrapped paragraph inside the modal and returns the y past
// it. settingsDesc wraps to the settings CARD's width, which is not this panel's.
func (a *App) baseWizText(x, y, w int32, text string, col sdl.Color) int32 {
	for _, ln := range a.ctx.WrapText(text, w, 0) {
		a.ctx.Label(x, y, ln, col)
		y += baseWizRowH
	}
	return y
}

// baseScanReport is the neutral part of step 2: what is in the folder. Pure over
// the scan so the wording is tested without a window.
func baseScanReport(s baseScan) []string {
	kind := "folder"
	if s.isZip {
		kind = ".zip pack"
	}
	more := ""
	if s.capped {
		more = " or more" // the listing hit its cap: these counts are a floor
	}
	out := []string{
		"This " + kind + " holds " + strconv.Itoa(s.chars) + more + " character(s) and " +
			strconv.Itoa(s.bgs) + more + " background(s).",
		baseScanINILine(s),
	}
	var extra []string
	for _, d := range []struct {
		have bool
		name string
	}{
		{s.hasSounds, baseDirSounds},
		{s.hasEvidence, baseDirEvidence},
		{s.hasMisc, baseDirMisc},
	} {
		if d.have {
			extra = append(extra, d.name+"/")
		}
	}
	if len(extra) > 0 {
		out = append(out, "Also here: "+strings.Join(extra, ", "))
	}
	return out
}

// baseScanINILine reports the char.ini finding — the whole of issue #72 in one
// line. It always says how many folders were LOOKED AT, because a folder scan
// samples (baseScanINISample) and reporting a sample as a total would be the
// same kind of quiet wrongness this screen exists to remove.
func baseScanINILine(s baseScan) string {
	switch {
	case s.iniOf == 0:
		return "char.ini: no characters here to check."
	case s.iniOK == 0:
		return "char.ini: none in the " + strconv.Itoa(s.iniOf) + " character(s) checked."
	case s.iniOK == s.iniOf:
		return "char.ini: yes, in all " + strconv.Itoa(s.iniOf) + " character(s) checked."
	default:
		return "char.ini: in " + strconv.Itoa(s.iniOK) + " of the " + strconv.Itoa(s.iniOf) + " character(s) checked."
	}
}

// baseScanWarnings is everything the user needs told about this folder BEFORE
// they commit to it. Empty for a folder that is simply fine.
//
// Pure over the scan, and separate from baseScanReport, because these are the
// lines with consequences: each one names something that would otherwise be
// discovered as "I set it up and my character still looks wrong".
func baseScanWarnings(s baseScan) []string {
	var out []string
	if !s.looksLikeBase() {
		line := "This doesn't look like an AO base: there is no " + baseDirCharacters + "/ or " +
			baseDirBackground + "/ folder in it."
		if s.suggest != "" {
			line += " " + s.suggest + " looks more like one."
		}
		out = append(out, line)
	}
	// The #72 warning proper. A base whose characters ship no char.ini still loads
	// their sprites, so it looks like it worked — while every emote list, showname,
	// blip set and chatbox skin keeps coming from the server's copy of that
	// character. That is a difference no amount of staring at the screen explains.
	if s.chars > 0 && s.iniOf > 0 && s.iniOK == 0 {
		out = append(out, "None of the characters checked ship a char.ini, so their emote lists, shownames "+
			"and blips will still come from the server.")
	}
	if s.capped {
		out = append(out, "This is a very large "+map[bool]string{true: "pack", false: "folder"}[s.isZip]+
			" and only part of it was counted. It will still be read in full.")
	}
	return out
}
