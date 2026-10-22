package ui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/winexec"
)

// volumeWheelStep is how many percent one wheel tick nudges a volume row (#52):
// fine enough for precise tuning, coarse enough that a flick crosses the 0–100
// range in a handful of ticks. It's the volume analog of sliderRow's per-call
// step; a value row without an explicit step needs a named one here.
const volumeWheelStep = 2

// volumeRow draws one "<name>  [====slider====] NN%" control and returns the
// value. A draggable slider (click/drag anywhere on the track) instead of
// +/- buttons — far nicer to set than stepping a button. Continuous 0–100.
// Mousewheel over the row steps by volumeWheelStep (numberRow/sliderRow parity,
// #52) and the row joins the settings gather-search index like every other row.
func (a *App) volumeRow(y int32, name string, value int) int {
	c := a.ctx
	pad := a.formX
	if c.onRow != nil {
		c.onRow(name, y) // gather-search: "master volume" & friends are now findable/flash-jumpable (#52)
	}
	c.Label(pad, y+4, name+":", ColText)
	track := sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}
	value = int(c.Slider(name, track, int32(value), 100))
	// Wheel-over-row stepping (#52): the whole label+track band owns the wheel so
	// a hover-scroll fine-tunes instead of paging the settings list.
	row := sdl.Rect{X: pad, Y: y, W: track.X + track.W - pad, H: 26}
	if c.hovering(row) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered control owns the wheel — no page scroll
		value += int(c.wheelY) * volumeWheelStep
		if value < 0 {
			value = 0
		}
		if value > 100 {
			value = 100
		}
	}
	c.Label(track.X+track.W+12, y+4, fmt.Sprintf("%3d%%", value), ColAccent)
	return value
}

// numberRow is volumeRow for arbitrary units/steps/bounds (spinbox-style:
// −/+ plus mousewheel over the row).
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

// settingTip shows a value row's "what's this" tooltip (#19) when one was passed — a hover
// explanation for the terse slider / number controls that can't carry a full inline label like the
// (already self-documenting) checkboxes do.
func settingTip(c *Ctx, row sdl.Rect, tip []string) {
	if len(tip) > 0 && tip[0] != "" {
		c.Tooltip(row, tip[0])
	}
}

// sliderRow is numberRow drawn as a draggable slider (same signature, so it's
// a drop-in): label, a slider track, then the live value — all left of the
// pad+270 help text the settings rows use. Drag for coarse; mousewheel over
// the row still fine-tunes by ±step (numberRow parity). The result snaps to
// the step grid and clamps to [min, max].
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
	if value < min {
		value = min
	}
	if value > max {
		value = max
	}
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

// uiScaleSliderID is the manual UI-scale slider's drag id (== the label passed to
// sliderRow), used to detect the drag for commit-on-release.
const uiScaleSliderID = "UI scale %"

// drawManualUIScale draws the manual UI-scale control. Applying the scale LIVE
// rescales the whole UI — this very slider included — so a live drag chases the
// cursor (the "super hard to use" report). Fix: the slider COMMITS ON RELEASE
// (preview the number while dragging, apply on mouse-up), and a row of preset
// chips gives a one-click path that never fights the cursor. Returns the new y.
func (a *App) drawManualUIScale(y int32) int32 {
	c := a.ctx
	pad := a.formX
	// While a drag is armed, feed the PENDING value back in: on the release
	// frame the Slider no longer tracks the mouse and echoes whatever it was
	// passed, so passing uiScalePct made the thumb (and worse, `next`) revert.
	shown := a.uiScalePct
	if a.uiScaleDragging {
		shown = a.uiScalePending
	}
	next := a.sliderRow(y, uiScaleSliderID, shown, config.UIScaleStepPercent,
		config.MinUIScalePercent, config.MaxUIScalePercent,
		"Zooms the WHOLE interface. Drag to preview, release to apply (applying mid-drag would rescale this slider under your cursor); or click a preset below.")
	switch {
	// The mouseDown guard is load-bearing: BeginFrame clears dragID only on a
	// frame that STARTS with the button up, so on the release frame itself
	// dragID still names this slider while mouseDown is already false. Without
	// the guard this case fired one extra frame and clobbered uiScalePending
	// with the un-applied base value — the release then committed a no-op and
	// the slider "snapped back to 100%" (the playtest report).
	case c.dragID == uiScaleSliderID && c.mouseDown:
		// Dragging: remember the value but DON'T rescale yet (that's the feedback loop).
		a.uiScaleDragging = true
		a.uiScalePending = next
	case a.uiScaleDragging:
		// Released → apply once, on the release frame.
		a.uiScaleDragging = false
		a.applyManualUIScale(a.uiScalePending)
	case next != a.uiScalePct:
		// Wheel or click-to-set (no drag) — discrete, no feedback loop.
		a.applyManualUIScale(next)
	}
	y += 30

	// Preset chips: a no-drag, one-click path (the easy way to land 150/175% on a
	// big screen). A click is instantaneous, so it can't chase the cursor.
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

// applyManualUIScale commits a manual UI scale: clamp, store, push to the kit
// (mouse unprojection) and persist. The single chokepoint for the slider's
// release, the wheel, and the preset chips.
func (a *App) applyManualUIScale(pct int) {
	pct = clampInt(pct, config.MinUIScalePercent, config.MaxUIScalePercent)
	if pct == a.uiScalePct {
		return
	}
	a.uiScalePct = pct
	a.ctx.SetUIScale(a.UIScale())
	a.d.Prefs.SetUIScale(pct)
}

// drawViewportSizeRow is the precise stage-size control. The AO stage art is
// 256×192, so the stage stays sharp at integer multiples and softens at the
// %-of-window sizes that fall between them. Preset chips pick the crisp
// multiples, a slider / mouse-wheel step through them, and an entry box takes an
// exact px width for power users; 0 px = "Fit" (size by the View % knob / edge
// drag, the prior behaviour). The entry buffer is written only on edit / commit
// / chip / slider — never reseeded per frame — so it stays freely editable.
// Settings screen only; off the courtroom hot path.
func (a *App) drawViewportSizeRow(y int32) int32 {
	c := a.ctx
	pad := a.formX
	cur := a.d.Prefs.Load().ViewportExactW
	set := func(px int) { // apply + resync the entry buffer to the clamped result
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ViewportExactW = px })
		cur = a.d.Prefs.Load().ViewportExactW
		if cur == 0 {
			a.client.vpExactBuf = ""
		} else {
			a.client.vpExactBuf = strconv.Itoa(cur)
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

	// Slider + wheel step in art-multiples (so every slider stop is a crisp size);
	// 0 = the leftmost stop = Fit.
	maxM := config.MaxViewportExactPx / config.ViewportArtW
	curM := cur / config.ViewportArtW
	track := sdl.Rect{X: pad + 130, Y: y + 5, W: 160, H: 16}
	newM := int(c.Slider("vpexact", track, int32(curM), int32(maxM)))
	if c.hovering(sdl.Rect{X: track.X - 6, Y: y, W: track.W + 12, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // hovered control owns the wheel — no page scroll
		newM = curM + int(c.wheelY)
	}
	if newM < 0 {
		newM = 0
	}
	if newM > maxM {
		newM = maxM
	}
	if newM != curM {
		set(newM * config.ViewportArtW) // 0 ⇒ Fit
	}

	// Exact-px entry for "specific numbers" (commit on Enter; mirrors the
	// window-size inputs — no live filtering, Atoi just ignores junk on submit).
	box := sdl.Rect{X: track.X + track.W + 14, Y: y, W: 64, H: fieldH}
	next, submitted := c.TextField("vpexactpx", box, a.client.vpExactBuf, "px")
	a.client.vpExactBuf = next
	if submitted {
		if v, err := strconv.Atoi(strings.TrimSpace(a.client.vpExactBuf)); err == nil {
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

// delayRow draws a hover-dwell slider with a seconds readout and returns the
// picked ms, clamped to [minMs, maxMs] and snapped to the half-second grid.
// Shared by previewDelayRow and emoteNameDelayRow, which differ only in the row
// label, widget id and upper bound (the config setters are authoritative).
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
	if ms < minMs {
		ms = minMs
	}
	if ms > maxMs {
		ms = maxMs
	}
	ms = ((ms-minMs+stepMs/2)/stepMs)*stepMs + minMs // snap to the grid
	if ms > maxMs {
		ms = maxMs
	}
	c.Label(track.X+track.W+8, y+4, fmt.Sprintf("%.1f s", float64(ms)/1000), ColAccent)
	return ms
}

// previewDelayRow draws the sprite-preview hover dwell as a draggable slider
// with a seconds readout (the value is stored in milliseconds; a raw "5000"
// would be opaque). Bounds mirror the config clamp — SetPreviewHoverMs is
// authoritative — and the result snaps to the half-second grid.
func (a *App) previewDelayRow(y int32, ms int) int {
	return a.delayRow(y, ms, "Preview after hovering:", "previewdelay", 15000) // == config.maxPreviewHoverMs
}

// emoteNameDelayRow draws the emote-name hover dwell as a draggable slider with
// a seconds readout (mirrors previewDelayRow; the value is stored in ms). Bounds
// mirror the config clamp — SetEmoteHoverNamesMs is authoritative — and the
// result snaps to the half-second grid.
func (a *App) emoteNameDelayRow(y int32, ms int) int {
	return a.delayRow(y, ms, "Name after hovering:", "emotenamedelay", 10000) // == config.maxEmoteHoverNamesMs
}

// emoteGridGapRow draws the emote-grid icon-spacing slider and returns the picked
// gap in LOGICAL px. Same shape as emoteNameDelayRow (slider + wheel-over-the-row
// nudge + a live readout), and it shadows pad from a.formX like every settings
// draw helper must — without that shadow the row renders under the sidebar.
//
// The range starts at 0 on purpose (icons flush against each other) and the value
// is NOT snapped to a step, unlike the half-second grid on the delay row above:
// this setting is specified in pixels, so every pixel in range must be reachable.
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
	if gap < config.MinEmoteGridGapPx {
		gap = config.MinEmoteGridGapPx
	}
	if gap > config.MaxEmoteGridGapPx {
		gap = config.MaxEmoteGridGapPx
	}
	c.Label(track.X+track.W+8, y+4, strconv.Itoa(gap)+" px", ColAccent)
	return gap
}

// browseForFolder shells the native Windows THEME-folder picker on a goroutine;
// the chosen path lands on folderRes (empty = cancelled, dropped). Returns
// false when a dialog is already open (busy) so the caller can say so. This is
// the last native shell-out picker: it's a FOLDER picker the in-app file browser
// (demobrowser.go) doesn't replace today. If this one ever fails foreground live
// the same way the old .demo file dialog did, the in-app browser is the proven
// escalation path (extend it to pick directories).
func browseForFolder() bool {
	if settings.browseBusy {
		return false
	}
	settings.browseBusy = true
	go func() {
		// A shown-and-activated TopMost owner form: a merely-CREATED owner proved
		// insufficient live, so it's Show()n (invisible: Opacity 0, no taskbar) and
		// Activate()d first so the modal dialog inherits real foreground z-order.
		const dialog = `Add-Type -AssemblyName System.Windows.Forms; ` +
			`$d = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
			`$d.Description = 'Pick the folder that CONTAINS themes\<name>'; ` +
			`$o = New-Object System.Windows.Forms.Form; $o.TopMost = $true; $o.ShowInTaskbar = $false; $o.Opacity = 0; $o.Show(); $o.Activate(); ` +
			`if ($d.ShowDialog($o) -eq 'OK') { Write-Output $d.SelectedPath }`
		cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", dialog)
		winexec.Hide(cmd) // GUI-subsystem build: no empty PowerShell window (the dialog still shows)
		// Start → donate foreground rights → Wait: a CREATE_NO_WINDOW child can't
		// activate its own dialog, so we hand it OUR foreground-activation rights.
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		if err := cmd.Start(); err != nil {
			settings.folderRes <- pickFailed
			return
		}
		winexec.AllowSetForeground(cmd.Process.Pid)
		err := cmd.Wait()
		path := strings.TrimSpace(outBuf.String())
		if err != nil {
			settings.folderRes <- pickFailed // failure ≠ cancel
			return
		}
		if path == "" {
			settings.folderRes <- ""
			return
		}
		settings.folderRes <- path
	}()
	return true
}

// resolveDroppedFolder turns an SDL drop path into a directory off-thread
// (a dropped file means "its folder") and feeds the same channel as Browse.
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
		// Trigger 4: an import just landed. Unconditional — the floor exists to
		// stop idle polling, not to make a user wait after their own action.
		a.requestThemeCatalog(themeScanAfterDrop)
		settings.statusLine = "Theme folder set: " + path
	default:
	}
}

// pickFailed marks a native-picker shell-out FAILURE on folderRes, distinct from
// "" (user cancelled). NUL can never be a real path, so it can't collide. Used by
// browseForFolder (the THEME folder picker); the .demo picker is in-app
// (demobrowser.go), so this is the theme picker's alone.
const pickFailed = "\x00"

// themePickerCap bounds the theme dropdown's list (hard rule 4). It is derived
// from themeCatalogCap, not a second number: the picker and the browser answer
// the same question about the same directories, and a name in one but not the
// other is the class of bug where a theme resolves yet cannot be picked.
// Past it the list stops and the row says so — the themes are still on disk and
// still load by name, which is what the truncation note tells the user.
//
// The +1 is the seed, and it is what makes "same question" true. scanThemeDirs
// starts the list with the built-in "default" NAME before it reads a single
// directory (theme.Load falls back to it even when no themes/ folder exists),
// while the catalog counts directories only. At a plain themeCatalogCap the seed
// would spend a directory's slot: a machine with exactly themeCatalogCap real
// themes got 128 catalog entries and 127 picker rows — one theme the browser
// lists and the dropdown cannot reach, i.e. precisely the bug above.
const themePickerCap = themeCatalogCap + 1 // +1: the "default" seed is a name, not a directory

// scanThemeDirs collects theme names across roots, "default" always first
// (the built-in fallback theme.Load uses even when no folder exists). BLOCKING
// (os.ReadDir per root) — goroutine only, never a draw path.
//
// pick is the bare-folder import normalizeThemeRoot just resolved (#21). It has
// no themes/ parent, so the ReadDir below can never find it; without appending
// it the theme applies once and then vanishes from the dropdown, and stepping
// the ◀ ▶ arrows would walk off it with no way back. It is appended even at the
// cap — a theme the user just picked must never be the one entry the bound drops,
// or the pick becomes unreachable the moment it is made.
//
// Dedup keys on the FOLDED name, matching the catalog scan: on Windows and macOS
// "Nocturne" and "nocturne" are one directory, and keying case-sensitively listed
// it twice in the dropdown while the catalog held one entry for it. The name kept
// is the first spelling seen, which is the one the first root actually holds.
//
// On a case-SENSITIVE filesystem (Linux) those are two real directories, and
// folding costs the second SPELLING ENCOUNTERED its dropdown row (root order,
// then directory order, decides which). That is deliberate, not an oversight:
// the catalog scan folds too
// (themecatalog.go), so the picker and the browser stay one list, and the price
// is paid by a collection that keeps two themes whose names differ only in case.
// The hidden one is not lost — theme.Load takes the name as spelled, so a
// preferences file or a drag-and-drop import still resolves it.
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

// appendThemePick adds the bare-folder import to the list unless a root already
// produced it. Split out so the cap's early return and the normal end of the walk
// cannot forget it — the #21 vanishing-theme bug is exactly that omission.
func appendThemePick(names []string, seen map[string]bool, pick string) []string {
	if pick != "" && !seen[strings.ToLower(pick)] {
		names = append(names, pick)
	}
	return names
}

// themeListTruncated reports whether the picker list may be a prefix of what is
// on disk. Derived rather than returned alongside the list, because the list is
// carried across a channel to the render thread and one slice is the whole
// message; a list at the cap is reported as possibly-truncated, which overstates
// by one collection-size exactly (a machine with EXACTLY themePickerCap themes),
// always in the honest direction.
func themeListTruncated(names []string) bool { return len(names) >= themePickerCap }

// themeFoundText is the "(N found)" line beside the picker, memoized on the count
// it was built from: it draws every frame the Theme tab is up, and a per-frame
// fmt.Sprintf on a settings row is the allocation the settings-text doctrine
// exists to keep out. Render thread only (settingsState).
func themeFoundText(names []string) string {
	n := len(names)
	if settings.themeCountFor == n && settings.themeCountText != "" {
		return settings.themeCountText
	}
	settings.themeCountFor = n
	if themeListTruncated(names) {
		// At the bound the list IS a prefix: say so rather than implying these are
		// all the themes there are. They still load by name — only the dropdown
		// stops here.
		settings.themeCountText = fmt.Sprintf("(first %d — more are on disk and still load by name)", themePickerCap)
	} else {
		settings.themeCountText = fmt.Sprintf("(%d found)", n)
	}
	return settings.themeCountText
}

// Per-character sprite filtering (#21 label 15). Indices MUST match
// config.SpriteScaling* — the pref is mirrored straight into the dropdown row.
const (
	spriteScaleDDX = 110 // dropdown x, past the "Sprite filtering:" label
	spriteScaleDDW = 230 // wide enough for the longest label below
)

var spriteScalingLabels = [...]string{
	config.SpriteScalingAuto:   "Auto (recommended)",
	config.SpriteScalingSmooth: "Always smooth",
	config.SpriteScalingPixel:  "Always crisp pixels",
}

// One line of plain why-would-I-pick-this under the row. Short and factual —
// settings text stays descriptive, never chatty.
var spriteScalingHints = [...]string{
	config.SpriteScalingAuto:   "Follows each character's own char.ini, and keeps small pixel-art sprites crisp when they're enlarged.",
	config.SpriteScalingSmooth: "Blurs enlarged pixel art. Ignores what characters ask for.",
	config.SpriteScalingPixel:  "Crisp everywhere, including art that was drawn to be smooth.",
}
