package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	hvalue "github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// volumeWheelStep is how many percent one wheel tick nudges a volume row.
const volumeWheelStep = 2

// volumeRow draws one "<name>  [slider]  NN%" control (0-100); the wheel steps by volumeWheelStep.
func (a *App) volumeRow(y int32, name string, value int) int {
	c := a.ctx
	pad := a.formX
	if c.onRow != nil {
		c.onRow(name, y) // gather-search: "master volume" & friends are now findable/flash-jumpable (#52)
	}
	c.Label(pad, y+4, name+":", ColText)
	track := sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}
	value = int(c.Slider(name, track, int32(value), 100))
	// The label+track band owns the wheel so a hover-scroll tunes the value instead of paging the settings list.
	row := sdl.Rect{X: pad, Y: y, W: track.X + track.W - pad, H: 26}
	if c.hovering(row) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered control owns the wheel — no page scroll
		value += int(c.wheelY) * volumeWheelStep
		value = hvalue.Clamp(value, 0, 100)
	}
	c.Label(track.X+track.W+12, y+4, fmt.Sprintf("%3d%%", value), ColAccent)
	return value
}

// numberRow is volumeRow for arbitrary units, steps and bounds (spinbox-style: -/+ plus wheel).
func (a *App) numberRow(y int32, label string, value, step, min, max int, tip ...string) int {
	c := a.ctx
	pad := a.formX
	if c.onRow != nil {
		c.onRow(label, y) // gather-search: number rows are findable like sliders/checkboxes (#52)
	}
	c.Label(pad, y+4, label+":", ColText)
	if c.Button(sdl.Rect{X: pad + 130, Y: y, W: 24, H: 24}, "-") && value-step >= min {
		value -= step
	}
	c.Label(pad+162, y+4, fmt.Sprintf("%5d", value), ColAccent)
	if c.Button(sdl.Rect{X: pad + 224, Y: y, W: 24, H: 24}, "+") && value+step <= max {
		value += step
	}
	row := sdl.Rect{X: pad, Y: y, W: 252, H: 26}
	if c.hovering(row) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered spinbox owns the wheel — no page scroll
		next := value + int(c.wheelY)*step
		if next >= min && next <= max {
			value = next
		}
	}
	settingTip(c, row, tip) // #19: optional "what's this" hover explanation
	return value
}

// settingTip shows a value row's tooltip when one was passed.
func settingTip(c *Ctx, row sdl.Rect, tip []string) {
	if len(tip) > 0 && tip[0] != "" {
		c.Tooltip(row, tip[0])
	}
}

// sliderRow is numberRow drawn as a draggable slider; the result snaps to the step grid and clamps to [min, max].
func (a *App) sliderRow(y int32, label string, value, step, min, max int, tip ...string) int {
	c := a.ctx
	pad := a.formX
	if c.onRow != nil {
		c.onRow(label, y) // gather-search: slider rows are findable like checkboxes
	}
	c.Label(pad, y+4, label+":", ColText)
	track := sdl.Rect{X: pad + 130, Y: y + 5, W: 90, H: 16}
	if span := max - min; span > 0 {
		value = min + int(c.Slider(label, track, int32(value-min), int32(span)))
	}
	if c.hovering(sdl.Rect{X: pad, Y: y, W: 252, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered control owns the wheel — no page scroll
		value += int(c.wheelY) * step
	}
	value = hvalue.Clamp(value, min, max)
	if step > 1 { // snap to the step grid for clean values, then re-clamp
		value = ((value-min+step/2)/step)*step + min
		if value > max {
			value = max
		}
	}
	c.Label(track.X+track.W+8, y+4, fmt.Sprintf("%d", value), ColAccent)
	settingTip(c, sdl.Rect{X: pad, Y: y, W: 252, H: 26}, tip) // #19: optional "what's this" hover explanation
	return value
}

// uiScaleSliderID is the manual UI-scale slider's drag id (the label passed to sliderRow), used to commit on release.
const uiScaleSliderID = "UI scale %"

// drawManualUIScale draws the manual UI-scale control. Applying live rescales this slider too, so it commits on
// release; preset chips give a one-click path. Returns the new y.
func (a *App) drawManualUIScale(y int32) int32 {
	c := a.ctx
	pad := a.formX
	// While a drag is armed, feed the pending value back: on the release frame the Slider echoes what it was passed.
	shown := a.uiScalePct
	if a.uiScaleDragging {
		shown = a.uiScalePending
	}
	next := a.sliderRow(y, uiScaleSliderID, shown, config.UIScaleStepPercent,
		config.MinUIScalePercent, config.MaxUIScalePercent,
		"Zooms the WHOLE interface. Drag to preview, release to apply (applying mid-drag would rescale this slider under your cursor); or click a preset below.")
	switch {
	// The mouseDown guard is load-bearing: dragID is cleared only on a frame that starts with the button up, so on the
	// release frame it still names this slider; without the guard uiScalePending is clobbered with the un-applied base.
	case c.dragID == uiScaleSliderID && c.mouseDown:
		// Dragging: remember the value but do not rescale yet (that would be a feedback loop).
		a.uiScaleDragging = true
		a.uiScalePending = next
	case a.uiScaleDragging:
		// Released: apply once, on the release frame.
		a.uiScaleDragging = false
		a.applyManualUIScale(a.uiScalePending)
	case next != a.uiScalePct:
		// Wheel or click-to-set (no drag) is discrete, so no feedback loop.
		a.applyManualUIScale(next)
	}
	y += 30

	// Preset chips: a one-click path that cannot chase the cursor.
	cx := pad + 130
	for _, p := range []int{100, 125, 150, 175, 200} {
		r := sdl.Rect{X: cx, Y: y, W: 46, H: btnH}
		if c.Button(r, fmt.Sprintf("%d%%", p)) {
			a.applyManualUIScale(p)
		}
		if a.uiScalePct == p {
			c.Border(r, ColAccent) // mark the active preset
		}
		cx += 52
	}
	return y + 34
}

// applyManualUIScale commits a manual UI scale: clamp, store, push to the kit (mouse unprojection) and persist.
func (a *App) applyManualUIScale(pct int) {
	pct = hvalue.Clamp(pct, config.MinUIScalePercent, config.MaxUIScalePercent)
	if pct == a.uiScalePct {
		return
	}
	a.uiScalePct = pct
	a.ctx.SetUIScale(a.UIScale())
	a.d.Prefs.SetUIScale(pct)
}

// drawViewportSizeRow is the stage-size control: chips pick crisp integer multiples of the 256x192 art, a slider
// or wheel steps through them, and an entry box takes an exact px width; 0 px is "Fit". The entry buffer is written
// only on edit, commit, chip or slider so it stays editable.
func (a *App) drawViewportSizeRow(y int32) int32 {
	c := a.ctx
	pad := a.formX
	cur := a.d.Prefs.Load().ViewportExactW
	set := func(px int) { // apply + resync the entry buffer to the clamped result
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ViewportExactW = px })
		cur = a.d.Prefs.Load().ViewportExactW
		if cur == 0 {
			a.vpExactBuf = ""
		} else {
			a.vpExactBuf = strconv.Itoa(cur)
		}
	}

	c.Label(pad, y+4, "Stage size:", ColText)
	x := pad + 130
	chip := func(label string, px int) {
		bw := c.TextWidth(label) + 16
		r := sdl.Rect{X: x, Y: y, W: bw, H: btnH}
		if c.Button(r, label) {
			set(px)
		}
		if cur == px {
			c.Border(r, ColAccent) // the active size
		}
		x += bw + 5
	}
	chip("Fit", 0)
	for m := 1; m <= 4; m++ {
		chip(fmt.Sprintf("%d×", m), m*config.ViewportArtW)
	}
	y += 30

	// Slider and wheel step in art multiples, so every stop is a crisp size; 0 (leftmost) is Fit.
	maxM := config.MaxViewportExactPx / config.ViewportArtW
	curM := cur / config.ViewportArtW
	track := sdl.Rect{X: pad + 130, Y: y + 5, W: 160, H: 16}
	newM := int(c.Slider("vpexact", track, int32(curM), int32(maxM)))
	if c.hovering(sdl.Rect{X: track.X - 6, Y: y, W: track.W + 12, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // hovered control owns the wheel — no page scroll
		newM = curM + int(c.wheelY)
	}
	newM = hvalue.Clamp(newM, 0, maxM)
	if newM != curM {
		set(newM * config.ViewportArtW) // 0 ⇒ Fit
	}

	// Exact-px entry, committed on Enter; Atoi ignores junk on submit.
	box := sdl.Rect{X: track.X + track.W + 14, Y: y, W: 64, H: fieldH}
	next, submitted := c.TextField("vpexactpx", box, a.vpExactBuf, "px")
	a.vpExactBuf = next
	if submitted {
		if v, err := strconv.Atoi(strings.TrimSpace(a.vpExactBuf)); err == nil {
			set(v)
		}
	}

	rx := box.X + box.W + 12
	if cur == 0 {
		c.LabelClipped(rx, y+4, a.formX+a.formW-rx, "Fit window (sized by the View % knob / edge drag)", ColTextDim)
	} else {
		hh := cur * config.ViewportArtH / config.ViewportArtW
		if cur%config.ViewportArtW == 0 {
			c.Label(rx, y+4, fmt.Sprintf("%d×%d  ·  %d× — sharp", cur, hh, cur/config.ViewportArtW), ColAccent)
		} else {
			c.Label(rx, y+4, fmt.Sprintf("%d×%d  ·  off-grid — may blur", cur, hh), ColTierYellow)
		}
	}
	y += 28
	c.LabelClipped(pad, y, a.formW, "Integer multiples of 256×192 stay sharp; for pixel-perfect scaling also turn Smooth texture scaling off.", ColTextDim)
	return y + 20
}

// delayRow draws a hover-dwell slider with a seconds readout and returns the picked ms, clamped to [minMs, maxMs]
// and snapped to the half-second grid. Callers differ in label, widget id and upper bound.
func (a *App) delayRow(y int32, ms int, label, id string, maxMs int) int {
	c := a.ctx
	pad := a.formX
	const minMs, stepMs = 500, 500 // half-second dwell grid
	c.Label(pad, y+4, label, ColText)
	track := sdl.Rect{X: pad + 170, Y: y + 5, W: 120, H: 16}
	if span := maxMs - minMs; span > 0 {
		ms = minMs + int(c.Slider(id, track, int32(ms-minMs), int32(span)))
	}
	if c.hovering(sdl.Rect{X: pad, Y: y, W: 300, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered control owns the wheel — no page scroll
		ms += int(c.wheelY) * stepMs
	}
	ms = hvalue.Clamp(ms, minMs, maxMs)
	ms = ((ms-minMs+stepMs/2)/stepMs)*stepMs + minMs // snap to the grid
	if ms > maxMs {
		ms = maxMs
	}
	c.Label(track.X+track.W+8, y+4, fmt.Sprintf("%.1f s", float64(ms)/1000), ColAccent)
	return ms
}

// previewDelayRow draws the sprite-preview hover dwell slider; bounds mirror the config clamp.
func (a *App) previewDelayRow(y int32, ms int) int {
	return a.delayRow(y, ms, "Preview after hovering:", "previewdelay", 15000) // == config.maxPreviewHoverMs
}

// emoteNameDelayRow draws the emote-name hover dwell slider; bounds mirror the config clamp.
func (a *App) emoteNameDelayRow(y int32, ms int) int {
	return a.delayRow(y, ms, "Name after hovering:", "emotenamedelay", 10000) // == config.maxEmoteHoverNamesMs
}

// emoteGridGapRow draws the emote-grid icon-spacing slider and returns the gap in logical px. It shadows pad from
// a.formX like every settings draw helper, or the row renders under the sidebar. The range starts at 0 (icons flush)
// and the value is not snapped, because every pixel in range must be reachable.
func (a *App) emoteGridGapRow(y int32, gap int) int {
	c := a.ctx
	pad := a.formX
	c.Label(pad, y+4, "Emote icon spacing:", ColText)
	track := sdl.Rect{X: pad + 170, Y: y + 5, W: 120, H: 16}
	if span := config.MaxEmoteGridGapPx - config.MinEmoteGridGapPx; span > 0 {
		gap = config.MinEmoteGridGapPx + int(c.Slider("emotegridgap", track,
			int32(gap-config.MinEmoteGridGapPx), int32(span)))
	}
	if c.hovering(sdl.Rect{X: pad, Y: y, W: 300, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered control owns the wheel — no page scroll
		gap += int(c.wheelY)
	}
	gap = hvalue.Clamp(gap, config.MinEmoteGridGapPx, config.MaxEmoteGridGapPx)
	c.Label(track.X+track.W+8, y+4, strconv.Itoa(gap)+" px", ColAccent)
	return gap
}

// browseForFolder shows the theme-folder picker; the result lands on folderRes. Returns false when one is already open.
func (a *App) browseForFolder() bool {
	if settings.browseBusy {
		return false
	}
	settings.browseBusy = true
	a.d.Desktop.PickFolders(sdl.DialogOptions{
		Window:   a.ctx.win,
		Title:    "Pick the folder that CONTAINS themes/<name>",
		Location: settings.themeDir,
	}, func(paths []string, err error) {
		// SDL's callback must not block, and a dropped folder may hold folderRes's one place.
		go func() {
			settings.folderRes <- folderPickResult(paths, err)
			PushWake()
		}()
	})
	return true
}

// folderPickResult is a single-folder dialog's outcome as folderRes carries it: a path, "" when cancelled, or pickFailed.
func folderPickResult(paths []string, err error) string {
	switch {
	case err != nil:
		return pickFailed
	case len(paths) == 0:
		return ""
	default:
		return paths[0]
	}
}

// resolveDroppedFolder turns an SDL drop path into a directory off-thread (a dropped file means its folder).
func resolveDroppedFolder(path string) {
	go func() {
		st, err := os.Stat(path)
		if err != nil {
			settings.folderRes <- ""
			return
		}
		if !st.IsDir() {
			path = filepath.Dir(path)
		}
		settings.folderRes <- path
	}()
}

func (a *App) pollFolderPick() {
	select {
	case path := <-settings.folderRes:
		settings.browseBusy = false
		if path == "" {
			return // user cancelled — silence is correct
		}
		if path == pickFailed {
			settings.statusLine = "Couldn't open the folder picker — drop the folder onto this window instead."
			return
		}
		settings.themeDir = path
		a.d.Prefs.SetTheme(settings.themeName, path)
		a.scanThemes(true) // the user picked/dropped this folder — a real import (#35)
		// An import just landed: unconditional, since the rescan floor only stops idle polling.
		a.requestThemeCatalog(themeScanAfterDrop)
		settings.statusLine = "Theme folder set: " + path
	default:
	}
}

// pickFailed marks a native-picker failure on folderRes, distinct from "" (cancelled); NUL can never be a real path.
const pickFailed = "\x00"

// themePickerCap bounds the theme dropdown's list (hard rule 4), derived from themeCatalogCap so picker and browser
// agree. The +1 is the built-in "default" name scanThemeDirs seeds before reading any directory; the catalog counts
// directories only. Past the cap the row says the list is truncated; the themes still load by name.
const themePickerCap = themeCatalogCap + 1 // +1: the "default" seed is a name, not a directory

// scanThemeDirs collects theme names across roots, "default" first (theme.Load's built-in fallback). BLOCKING
// (os.ReadDir per root): goroutine only.
//
// pick is the bare-folder import normalizeThemeRoot resolved; it has no themes/ parent, so ReadDir cannot find it.
// It is appended even at the cap so a theme the user just picked never drops out of the dropdown.
//
// Dedup keys on the folded name, matching the catalog scan (themecatalog.go): on Windows and macOS "Nocturne" and
// "nocturne" are one directory. On case-sensitive filesystems the later spelling loses its row; theme.Load still
// resolves it by the spelled name.
func scanThemeDirs(roots []string, pick string) []string {
	names := []string{theme.DefaultThemeName}
	seen := map[string]bool{strings.ToLower(theme.DefaultThemeName): true}
	for _, root := range roots {
		entries, err := os.ReadDir(filepath.Join(root, theme.ThemesDirName))
		if err != nil {
			continue // missing themes/ dir is normal
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			key := strings.ToLower(e.Name())
			if seen[key] {
				continue
			}
			if len(names) >= themePickerCap {
				return appendThemePick(names, seen, pick)
			}
			seen[key] = true
			names = append(names, e.Name())
		}
	}
	return appendThemePick(names, seen, pick)
}

// appendThemePick adds the bare-folder import to the list unless a root already produced it.
func appendThemePick(names []string, seen map[string]bool, pick string) []string {
	if pick != "" && !seen[strings.ToLower(pick)] {
		names = append(names, pick)
	}
	return names
}

// themeListTruncated reports whether the picker list may be a prefix of what is on disk. A list exactly at the cap
// counts as truncated, which errs in the honest direction.
func themeListTruncated(names []string) bool { return len(names) >= themePickerCap }

// themeFoundText is the "(N found)" line beside the picker, memoized on the count so it does not allocate per frame.
// Render thread only.
func themeFoundText(names []string) string {
	n := len(names)
	if settings.themeCountFor == n && settings.themeCountText != "" {
		return settings.themeCountText
	}
	settings.themeCountFor = n
	if themeListTruncated(names) {
		// At the bound the list is a prefix: say so. The themes still load by name.
		settings.themeCountText = fmt.Sprintf("(first %d — more are on disk and still load by name)", themePickerCap)
	} else {
		settings.themeCountText = fmt.Sprintf("(%d found)", n)
	}
	return settings.themeCountText
}

// Per-character sprite filtering. Indices MUST match config.SpriteScaling*; the pref is mirrored into the dropdown row.
const (
	spriteScaleDDX = 110 // dropdown x, past the "Sprite filtering:" label
	spriteScaleDDW = 230 // wide enough for the longest label below
)

var spriteScalingLabels = [...]string{
	config.SpriteScalingAuto:   "Auto (recommended)",
	config.SpriteScalingSmooth: "Always smooth",
	config.SpriteScalingPixel:  "Always crisp pixels",
}

// One factual line under the row saying why you would pick it.
var spriteScalingHints = [...]string{
	config.SpriteScalingAuto:   "Follows each character's own char.ini, and keeps small pixel-art sprites crisp when they're enlarged.",
	config.SpriteScalingSmooth: "Blurs enlarged pixel art. Ignores what characters ask for.",
	config.SpriteScalingPixel:  "Crisp everywhere, including art that was drawn to be smooth.",
}
