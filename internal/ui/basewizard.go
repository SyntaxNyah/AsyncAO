package ui

// Settings > Assets > "Use my own AO files": pick a folder, see what was found, choose how it is used, apply.
// The mode choice follows the report because "never stream" is only safe once the folder is seen to hold a base.
// Nothing changes until "Use this folder"; browsing, scanning and re-picking are read-only.

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// baseScanResCap: one scan runs at a time (baseWizardState.scanning), so 1 can never block or drop a result.
	baseScanResCap = 1
	// The modal's geometry, clamped against the window like the file browser's.
	baseWizMaxW = 620
	baseWizMaxH = 520
	baseWizMinW = 340
	baseWizMinH = 260
	// baseWizRowH is the pitch of one wrapped help line, matching settingsDesc.
	baseWizRowH = 16
)

// baseWizardState is package-level because the Settings screen is single-instance.
type baseWizardState struct {
	open bool
	// path is the picked folder ("" = none yet); it survives close so re-opening to change the mode keeps it.
	path string
	// scan is the report for path, valid only once scanned: an empty folder's counts are a real all-zero answer.
	scan    baseScan
	scanned bool
	// scanning allows at most one scan goroutine (hard rule 4); pollBaseScan rescans if the path changed meanwhile.
	scanning bool
	res      chan baseScan
	// mode is what "Use this folder" applies: assetSrcLayered or assetSrcLocal, never assetSrcStream.
	mode int
	// note is a refusal shown in place (the mount cap), because the status line draws behind this modal.
	note string
}

var baseWizard = baseWizardState{res: make(chan baseScan, baseScanResCap), mode: assetSrcLayered}

// openBaseWizard seeds from the current setup, so re-opening to change something does not mean finding the folder
// again.
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

// setBasePath drops the report at once so a stale one cannot be read as describing the new folder.
func (w *baseWizardState) setBasePath(path string) {
	w.path = strings.TrimSpace(path)
	w.scan, w.scanned, w.note = baseScan{}, false, ""
	w.kickScan()
}

// kickScan starts one scan for w.path, or nothing if one is running; pollBaseScan rescans when a stale result lands.
func (w *baseWizardState) kickScan() {
	if w.scanning || w.path == "" {
		return
	}
	w.scanning = true
	go func(p string, res chan baseScan) { res <- scanBaseFolder(p) }(w.path, w.res)
}

// pollBaseScan lands a finished scan; never blocks and never touches the disk.
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

// baseWizardTakesDrop reports whether the open wizard consumed a dropped path; unconsumed, it would fall through and
// repoint the theme root. Global claims win (.aotheme, recordings); anything unclaimed is a pick, including a .zip.
func (a *App) baseWizardTakesDrop(path string) bool {
	if !baseWizard.open || claimDroppedFile(path, settings.importArmed) != dropClaimNone {
		return false
	}
	resolveDroppedBasePath(path)
	return true
}

// resolveDroppedBasePath turns a drop into a pick, off-thread because a stat on a network share must not stall the
// frame. A dropped file means its folder, except a .zip, which is a mountable pack itself.
func resolveDroppedBasePath(path string) {
	w := &baseWizard
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		w.setBasePath(path)
		return
	}
	// scanBaseFolder reports whatever that directory turns out to be.
	if !isDir(path) {
		path = filepath.Dir(path)
	}
	w.setBasePath(path)
}

// applyBaseWizard commits the pick; the only function in this file that writes. The folder goes first because mounts
// are first-hit-wins; an exact re-pick moves it to the front instead of adding a duplicate.
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
func baseWizardApplied(path string, mode int) string {
	if mode == assetSrcLocal {
		return "Using " + path + " only. Nothing will stream from the server."
	}
	return "Using " + path + " first, then streaming anything it doesn't have."
}

// drawBaseWizard draws last in drawSettings (topmost) with the page's modal blocking released, like the file browser.
func (a *App) drawBaseWizard(w, h int32) {
	s := &baseWizard
	if !s.open {
		return
	}
	c := a.ctx
	// The file browser opens on top of this panel, so widgets here must ignore clicks meanwhile, or a click on a
	// browser row also fires the control beneath it. Restored by defer so the browser's own draw is unblocked.
	fenced := c.modalOn
	c.modalOn = fenced || demoBrowser.open
	defer func() { c.modalOn = fenced }()
	s.pollBaseScan()

	pw := value.Clamp(w-2*browseModalMargin, baseWizMinW, baseWizMaxW)
	ph := value.Clamp(h-2*browseModalMargin, baseWizMinH, baseWizMaxH)
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
		// Return value dropped: the buttons below are pinned to the panel, so nothing flows after this.
		a.baseWizText(inX, y+4, inW, s.note, ColDanger)
	}

	// Pinned to the bottom edge so the buttons don't move as the report grows and shrinks.
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

func (a *App) baseWizStepPick(x, y, w int32) int32 {
	c := a.ctx
	s := &baseWizard
	c.Label(x, y+4, "1. Pick your base folder", ColText)
	y += 22
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
	// A button because both mistakes it catches (the install root, the characters/ folder itself) are one click from
	// right.
	if s.scanned && s.scan.suggest != "" {
		if c.Button(sdl.Rect{X: x + 16, Y: y, W: 150, H: btnH}, "Use that one instead") {
			s.setBasePath(s.scan.suggest)
		}
		c.LabelClipped(x+172, y+4, w-172, s.scan.suggest, ColTierYellow)
		y += btnH + 6
	}
	return y + 4
}

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

// baseWizStepMode is step 3: how the folder is used, with radio behaviour over checkbox rows.
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
	// "Never stream" is a promise about everything the folder does not hold, so warn.
	if s.mode == assetSrcLocal {
		return a.baseWizText(x+16, y+2, w-16,
			"Anything this folder is missing will not load at all — no sprite, no background, no music. "+
				"Pick this only for servers that provide no asset URL of their own.", ColTierYellow) + 6
	}
	return a.baseWizText(x+16, y+2, w-16,
		"Files you have replace the server's at the same path; everything else still streams. "+
			"This is the safe choice for testing your own work on a normal server.", ColTextDim) + 6
}

// baseWizText wraps to this panel's width; settingsDesc wraps to the settings card's.
func (a *App) baseWizText(x, y, w int32, text string, col sdl.Color) int32 {
	for _, ln := range a.ctx.WrapText(text, w, 0) {
		a.ctx.Label(x, y, ln, col)
		y += baseWizRowH
	}
	return y
}

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

// baseScanINILine always says how many folders were looked at: a folder scan samples (baseScanINISample).
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

// baseScanWarnings lists what the user must be told before committing; empty for a folder that is fine.
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
	// A base with no char.ini still loads sprites, so it looks fine, while emotes, showname, blips and chatbox skin
	// keep coming from the server's copy of that character.
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
