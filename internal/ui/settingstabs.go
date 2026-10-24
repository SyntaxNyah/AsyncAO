package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// drawSettingsStudio: the "Studio" tab — a ".demo → video" call-out, scene
// recording, the recordings library/replay picker, and GIF/WebP/Video export.
// See replay.go.
func (a *App) drawSettingsStudio(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.drawDemoToVideoCallout(y, w)
	y = a.drawContentToolsCallout(y, w)
	y = a.settingsSection(y, w, "Scene recording")
	c.Label(pad, y, "Record the courtroom to a tiny .aorec replay file — it stores the scene EVENTS (who spoke,", ColTextDim)
	y += 18
	c.Label(pad, y, "emote, text, timing), not video — so it's near-free and replays at perfect quality. All off by default.", ColTextDim)
	y += 18
	c.Label(pad, y, "Record: Ctrl+"+strings.ToUpper(a.hotkeyFor(hotkeyRecordScene))+"   ·   Replay last: Ctrl+"+strings.ToUpper(a.hotkeyFor(hotkeyReplayLast))+"   (rebind in Controls). Files save under recordings\\.", ColTextDim)
	y += 26
	srb := a.d.Prefs.Load().ShowRecordButton
	if next := c.Checkbox(pad, y, "Show a small ● Record button on the courtroom stage (OFF by default)", srb); next != srb {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ShowRecordButton = next })
	}
	y += 28
	// Recordings keep their assets (ON by default): on stop, auto-package the just-
	// recorded scene's already-warm assets into a self-contained bundle beside the
	// flat .aorec, so the recording survives its server's CDN. Additive — the flat
	// .aorec still saves either way.
	rka := a.d.Prefs.Load().RecordingsKeepAssets
	if next := c.Checkbox(pad, y, "Recordings keep their assets (ON): on stop, also save a self-contained bundle so the replay survives its server", rka); next != rka {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RecordingsKeepAssets = next })
	}
	y += 32

	y = a.settingsSection(y, w, "Instant replay (clip what just happened)")
	c.Label(pad, y, "Optionally keep a rolling buffer of the recent conversation, so the clip key can save the last minute or", ColTextDim)
	y += 18
	c.Label(pad, y, "two as a .aorec — WITHOUT starting a recording first. Off by default; nothing is kept until you tick it.", ColTextDim)
	y += 24
	ir := a.d.Prefs.Load().InstantReplay
	if next := c.Checkbox(pad, y, "Pre-record recent conversation (Instant Replay)", ir); next != ir {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				InstantReplay =

				next

		})

	}
	y += 30
	if ir {
		secs := a.d.Prefs.Load().InstantReplaySecondsValue()
		c.Label(pad+16, y+4, "Capture window:", ColTextDim)
		track := sdl.Rect{X: pad + 140, Y: y + 5, W: 240, H: 16}
		lo, hi := config.InstantReplayMinSeconds, config.InstantReplayMaxSeconds
		if nv := int(c.Slider("ir_window", track, int32(secs-lo), int32(hi-lo))) + lo; nv != secs {
			a.d.Prefs.SetInstantReplaySeconds(nv)
			secs = nv
		}
		c.Label(track.X+track.W+10, y+4, formatReplayWindow(time.Duration(secs)*time.Second)+"  (10s … 1 hour)", ColAccent)
		y += 26
		c.Label(pad+16, y, "Clip the last window: Ctrl+"+strings.ToUpper(a.hotkeyFor(hotkeyClipReplay))+"  (rebind in Controls). Saves to recordings\\ — open it in the Scene Maker to trim/export.", ColTextDim)
		y += 22
	}
	y += 6

	y = a.settingsSection(y, w, "Scene maker")
	c.Label(pad, y, "Build a scene from scratch — or edit a recording — line by line: pick the character, emote, text,", ColTextDim)
	y += 18
	c.Label(pad, y, "background and music, set the Origin/CDN the assets load from, then Preview and Save a .aorec.", ColTextDim)
	y += 18
	c.Label(pad, y, ".aorec files are plain text (JSON) — you can also open one in any text editor to tweak it by hand.", ColTextDim)
	y += 18
	c.Label(pad, y, "The Scene Maker's ⇄ .demo button writes scenes AO2's own demo player can watch (import a .demo above).", ColTextDim)
	y += 26
	if c.Button(sdl.Rect{X: pad, Y: y, W: 150, H: btnH}, "🎬 New scene") {
		a.newScene()
	}
	c.Label(pad+162, y+5, "opens the in-app Scene Maker (works offline — no server needed)", ColTextDim)
	y += 36

	y = a.settingsSection(y, w, "Recordings")
	if c.Button(sdl.Rect{X: pad, Y: y, W: 150, H: btnH}, "📁 Open folder") {
		a.openRecordingsFolder()
	}
	c.Label(pad+162, y+5, "the default recordings\\ folder (next to AsyncAO) where .aorec files are saved", ColTextDim)
	y += 32
	// listRecordings does a dir read, but this is the Settings menu (never the
	// courtroom render path), so it stays fresh with no caching.
	recs := listRecordings()
	if len(recs) == 0 {
		c.Label(pad, y+4, "No recordings yet — press the Record key (or the on-stage button) during a scene.", ColTextDim)
		y += 26
	} else {
		c.Label(pad, y+4, "Newest first — Play watches it back (it plays over the screen; ■ Stop to end):", ColText)
		y += 26
		const maxShow = 12
		for i, r := range recs {
			if i >= maxShow {
				c.Label(pad+16, y+4, "… and "+strconv.Itoa(len(recs)-maxShow)+" more in the recordings\\ folder.", ColTextDim)
				y += 24
				break
			}
			if c.Button(sdl.Rect{X: pad + 16, Y: y, W: 64, H: btnH}, "▶ Play") {
				a.replayFromPath(r.path)
			}
			if c.Button(sdl.Rect{X: pad + 84, Y: y, W: 58, H: btnH}, "✎ Edit") {
				a.editRecordingInMaker(r.path)
			}
			if c.Button(sdl.Rect{X: pad + 146, Y: y, W: 54, H: btnH}, "🎞 GIF") {
				a.sceneExportFromPath(r.path, exportGIF)
			}
			if c.Button(sdl.Rect{X: pad + 204, Y: y, W: 74, H: btnH}, "🎬 WebP") {
				a.sceneExportFromPath(r.path, exportWebP) // higher-quality animated WebP
			}
			if c.Button(sdl.Rect{X: pad + 282, Y: y, W: 78, H: btnH}, "🎥 Video") {
				a.sceneExportFromPath(r.path, exportVideo) // MP4/WebM via ffmpeg (Export-options format)
			}
			if c.Button(sdl.Rect{X: pad + 364, Y: y, W: 82, H: btnH}, "🖼 Comic") {
				a.sceneExportFromPath(r.path, exportComic) // single PNG storyboard page (pure Go)
			}
			c.LabelClipped(pad+450, y+4, w-pad-450-scrollBarW, r.name, ColText)
			y += 28
		}
	}
	y += 10

	y = a.settingsSection(y, w, "Replay playback")
	c.Label(pad, y, "How fast a replay plays. Lower = slower, so the whole message types out and lingers long enough", ColTextDim)
	y += 18
	c.Label(pad, y, "to read; higher = a quick recap. 100% is the readable default — adjusts live while a replay runs.", ColTextDim)
	y += 26
	// Bounds mirror the config clamp; SetReplaySpeed is authoritative.
	const (
		minReplayPct  = 25
		maxReplayPct  = 200
		replayPctStep = 5
	)
	rspd := a.d.Prefs.Load().ReplayPlaybackSpeed
	if next := a.sliderRow(y, "  Playback speed %", rspd, replayPctStep, minReplayPct, maxReplayPct); next != rspd {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ReplayPlaybackSpeed = next })
	}
	y += 32

	y = a.settingsSection(y, w, "Export to GIF / WebP / Video")
	c.Label(pad, y, "Turn a recording into a shareable animation. Use 🎞 GIF (works everywhere) or 🎬 WebP (true-colour, smaller)", ColTextDim)
	y += 18
	c.Label(pad, y, "on a recording above, or build one in the Scene Maker. These settings apply to every export:", ColTextDim)
	y += 26
	y = a.drawExportOptions(a.formX, y, false) // speed lives in the Replay-playback section above
	return y
}

// themeFitOption pairs one row of the "Theme fit:" dropdown with the mode
// constant it selects.
type themeFitOption struct {
	mode  int
	label string
}

var themeFitOptions = [...]themeFitOption{
	{config.ThemeFitNative, "Native — 1:1, stock AO2 size"},
	{config.ThemeFitStretch, "Stretch — fill, no bars"},
	{config.ThemeFitLetterbox, "Letterbox — keep shape (bars)"},
	{config.ThemeFitCrop, "Crop — fill, trim edges"},
	{config.ThemeFitCustom, "Custom — zoom + pan"},
}

// themeFitLabels is the dropdown's row text, derived from themeFitOptions once
// at package init so drawing the settings row never rebuilds a []string.
var themeFitLabels = func() []string {
	out := make([]string, len(themeFitOptions))
	for i, o := range themeFitOptions {
		out[i] = o.label
	}
	return out
}()

// themeFitRow maps a stored mode to its dropdown row. An unknown mode (a prefs
// file from a NEWER build, downgraded) falls back to row 0 — the default mode —
// rather than to a negative index the dropdown would render blank.
func themeFitRow(mode int) int {
	for i, o := range themeFitOptions {
		if o.mode == mode {
			return i
		}
	}
	return 0
}

// themeFitModeAt maps a dropdown row back to its mode constant.
func themeFitModeAt(row int) int {
	if row < 0 || row >= len(themeFitOptions) {
		return themeFitOptions[0].mode
	}
	return themeFitOptions[row].mode
}

// drawSettingsTheme: theme picker/folder, layout toggle, live preview, bind.
func (a *App) drawSettingsTheme(y, w, h int32) int32 {
	c := a.ctx
	pad := a.formX
	winW := w // real window width, kept for the aspect-true fit preview
	w = a.formW2()
	y = a.settingsSection(y, w, "Theme")
	c.Label(pad, y+4, "Theme:", ColText)
	// Direct-jump dropdown so a big theme collection is one click + scroll away,
	// not dozens of < > presses (#86). The < > buttons stay for fine stepping.
	selIdx := 0
	for i, n := range settings.themeList {
		if n == settings.themeName {
			selIdx = i
			break
		}
	}
	// Trigger 2 of four (§Q6): the picker OPENING asks for a fresh catalog. On the
	// rising EDGE, not while open: the themeScanMinInterval floor alone would let a
	// list held open walk the disk every 5 s forever, and "never poll the disk" is
	// the whole reason this feature has no filesystem watcher (themecatalog.go's
	// header). The floor still applies on top, so flicking the picker open twice in
	// a second is one scan. The latch is updated OUTSIDE the list-non-empty arm so a
	// dropdown that stops being drawn while open cannot strand it set.
	themeDDOpen := c.ddOpen == themeDropdownID
	if themeDDOpen && !settings.themeDDWasOpen {
		a.requestThemeCatalog(themeScanOnOpen)
	}
	settings.themeDDWasOpen = themeDDOpen
	if len(settings.themeList) > 0 {
		if next, changed := c.Dropdown(themeDropdownID, sdl.Rect{X: pad + 60, Y: y, W: 240, H: btnH}, settings.themeList, selIdx); changed {
			settings.themeName = settings.themeList[next]
			a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
			// #35: a deliberate pick may snap the window to the new canvas. Arm the
			// gen this very apply allocated, so a heal already in flight (themePage
			// heals from any theme-art draw, this screen's live preview included)
			// cannot land first and spend the arming on the OLD theme's canvas.
			a.themeResizeArmGen = a.applyThemeAsync()
		}
	} else {
		c.Label(pad+60, y+6, settings.themeName, ColAccent)
	}
	if c.Button(sdl.Rect{X: pad + 308, Y: y, W: 26, H: btnH}, "<") {
		a.cycleTheme(-1)
	}
	if c.Button(sdl.Rect{X: pad + 338, Y: y, W: 26, H: btnH}, ">") {
		a.cycleTheme(1)
	}
	if settings.themeBusy {
		c.Label(pad+372, y+6, "scanning...", ColTextDim)
	} else {
		c.Label(pad+372, y+6, themeFoundText(settings.themeList), ColTextDim)
	}
	y += 32
	c.Label(pad, y+4, "Theme folder:", ColText)
	settings.themeDir, _ = c.TextField("themedir", sdl.Rect{X: pad + 130, Y: y, W: 320, H: fieldH}, settings.themeDir, `optional root holding themes\<name> — or drop a folder anywhere`)
	if c.Button(sdl.Rect{X: pad + 460, Y: y, W: 130, H: btnH}, "Apply & rescan") {
		a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
		a.scanThemes(true) // user-driven: the scan landing may arm the #35 auto-resize
		a.applyThemeAsync()
	}
	if runtime.GOOS == "windows" {
		if c.Button(sdl.Rect{X: pad + 600, Y: y, W: 90, H: btnH}, "Browse...") {
			if !browseForFolder() {
				settings.statusLine = "The folder picker is already open — finish or close it first (Alt-Tab if you don't see it)."
			}
		}
	}
	y += 36
	y = a.drawThemeCatalogRows(y, w)
	y = a.drawThemeArtBudgetRow(y, w)

	y = a.settingsSection(y, w, "Layout & fit")
	// Emote-grid icon spacing lives HERE rather than with the other emote controls
	// on General: it is a layout measurement, and an AO2 theme overrides it — so
	// this is where someone looks for it. It shipped on General with a changelog
	// path naming a tab that does not exist ("Interface") and no search keyword at
	// all, which made it effectively unreachable.
	gap := a.d.Prefs.EmoteGridGap()
	if next := a.emoteGridGapRow(y, gap); next != gap {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				EmoteGridGapPx =

				next

		})

	}
	c.Label(pad+340, y+4, "gap between emote icons (default 6 px; 0 butts them together). AO2 themes keep their own spacing.", ColTextDim)
	y += 30
	// Built-in look: the new optimal layout (OOC as its own box, etc.) is the main theme; ticking
	// "Legacy Developer" reverts to the old developer layout exactly (OOC back in a tab).
	legacy := a.d.Prefs.Load().LegacyDevTheme
	if next := c.Checkbox(pad, y, "Legacy Developer theme (revert to the old built-in look — OOC back in a tab; off = the new optimal layout)", legacy); next != legacy {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				LegacyDevTheme =

				next

		})

	}
	y += 28
	// Theme-driven courtroom geometry (courtroom_design.ini).
	tlay := a.d.Prefs.Load().ThemeLayoutOn
	if next := c.Checkbox(pad, y, "Use the theme's courtroom layout (courtroom_design.ini positions every widget; off = classic layout)", tlay); next != tlay {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ThemeLayoutOn = next })
		a.invalidateThemeCanvases()
	}
	y += 28
	// #39: per-element courtroom_fonts.ini parity. Re-applies the theme so the
	// table is rebuilt (and the faces read) off-thread, exactly like a theme swap.
	tfonts := a.d.Prefs.Load().ThemeFonts
	if next := c.Checkbox(pad, y, "Use the theme's fonts (courtroom_fonts.ini — per-element family, point size and bold)", tfonts); next != tfonts {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ThemeFonts = next })
		a.applyThemeAsync()
	}
	y += 28
	// The user's own per-panel font and size, which override the checkbox above
	// for the panels they name. Placed directly under it because that is the
	// question this answers: "yes, use the theme's — except here."
	y = a.drawPanelFontSettings(y, w)

	// Theme fit: how the theme's FIXED design size fills your (differently
	// shaped) window — the cause of those borders.
	c.Label(pad, y+4, "Theme fit:", ColText)
	fit := a.d.Prefs.Load().ThemeFit
	if next, changed := c.Dropdown("themefit", sdl.Rect{X: pad + 90, Y: y, W: 230, H: fieldH}, themeFitLabels, themeFitRow(fit)); changed {
		fit = themeFitModeAt(next) // rows are ordered for the user, modes for the disk
		a.d.Prefs.SetThemeFit(fit)
		a.invalidateThemeCanvases() // both canvases re-fit under the new mode
	}
	c.LabelClipped(pad+330, y+4, w-pad-330-scrollBarW, "applies under a theme that drives the courtroom layout (above)", ColTextDim)
	y += 30
	if fit == config.ThemeFitCustom {
		// Big interactive preview (window-shaped): drag to pan, scroll to zoom.
		boxW := int32(560)
		if avail := a.formW; boxW > avail {
			boxW = avail
		}
		boxH := boxW * h / winW // match the real window aspect so the crop is true
		if boxH > 340 {
			boxH, boxW = 340, 340*winW/h
		}
		if boxH < 160 {
			boxH = 160
		}
		a.drawThemeFitPreview(sdl.Rect{X: pad, Y: y, W: boxW, H: boxH})
		y += boxH + 10
		zoom := a.d.Prefs.Load().ThemeZoom()
		if next := a.sliderRow(y, "  Zoom %", zoom, 5, config.MinThemeZoom, config.MaxThemeZoom); next != zoom {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.ThemeFitZoom = next })
			a.invalidateThemeCanvases()
		}
		y += 28
		px, py := a.d.Prefs.Load().ThemePan()
		if next := a.sliderRow(y, "  Pan X %", px, 1, -config.MaxThemePan, config.MaxThemePan); next != px {
			a.d.Prefs.SetThemeFitPan(next, py)
			a.invalidateThemeCanvases()
		}
		y += 28
		if next := a.sliderRow(y, "  Pan Y %", py, 1, -config.MaxThemePan, config.MaxThemePan); next != py {
			a.d.Prefs.SetThemeFitPan(px, next)
			a.invalidateThemeCanvases()
		}
		y += 30
	}

	// Layout presets (#34): save the whole default-courtroom arrangement under a name
	// and flip between setups. Presets are window fractions, so they travel across sizes.
	// applyLayoutPreset/applyStagePreset (layoutpresets.go) take effect the same frame.
	y = a.settingsSection(y, w, "Layout profiles")
	for _, ln := range c.WrapText("Save the WHOLE courtroom layout (the bottom-right toolbox's Edit chip — or Ctrl+Space → Edit Layout — lets you drag every box) under a name and switch between arrangements — a big stage for watching, a wide log for moderating. A profile captures the box positions, their corner anchors, their rotations, which chrome you hid, and the snap-grid step, so loading one restores everything at once. Stored as window fractions, so it looks right at any window size. Profiles cover the default courtroom layout; an AO2 theme's own layout, tilts and the chrome shape are saved separately (per-theme overrides / the chrome shape picker).", a.formW-8, 0) {
		c.Label(pad, y, ln, ColTextDim)
		y += 16
	}
	y += 8
	// Quick stage premades — overlay just the stage; every other box keeps its spot.
	c.Label(pad, y+4, "Quick stage:", ColText)
	bx := pad + c.TextWidth("Quick stage:") + 12
	for _, pm := range []struct {
		label string
		vp    [4]float64
	}{
		{"Theater 4:3", a.theaterStageFrac(winW, h)},
		{"Wide stage", [4]float64{0, 0, 0.74, 0.66}},
		{"Compact", [4]float64{0, 0, 0.50, 0.46}},
	} {
		bw := c.TextWidth(pm.label) + 16
		if c.Button(sdl.Rect{X: bx, Y: y, W: bw, H: btnH}, pm.label) {
			a.classic.applyStagePreset(pm.vp)
			a.debug.push("layout: stage preset applied")
		}
		bx += bw + 6
	}
	y += btnH + 8
	if c.Button(sdl.Rect{X: pad, Y: y, W: 150, H: btnH}, "Reset to stock") {
		a.classic.applyLayoutPreset(nil)
		a.debug.push("layout: reset to stock")
	}
	// Mouse-reachable chrome recovery (A6 no-strand): un-hides every piece at
	// once. The pieces panel refuses to hide BOTH the toolbox and the Settings
	// button precisely so this button stays reachable by mouse. Drawn only
	// while something is actually hidden.
	if len(a.classic.hidden) > 0 {
		lbl := "Show all hidden UI pieces"
		if c.Button(sdl.Rect{X: pad + 158, Y: y, W: c.TextWidth(lbl) + 16, H: btnH}, lbl) {
			a.classic.hidden = map[string]bool{}
			a.d.Prefs.SetHiddenPanels(nil)
			a.debug.push("layout: all hidden UI pieces restored")
		}
	}
	y += btnH + 12

	// Save the CURRENT arrangement under a name.
	c.Label(pad, y+4, "Save current as:", ColText)
	nameX := pad + c.TextWidth("Save current as:") + 10
	saveW := int32(64)
	fieldW := int32(200)
	if maxW := w - nameX - saveW - 10; fieldW > maxW {
		fieldW = maxW
	}
	if fieldW < 80 {
		fieldW = 80
	}
	a.layoutPresetName, _ = c.TextField("layoutpreset", sdl.Rect{X: nameX, Y: y, W: fieldW, H: fieldH}, a.layoutPresetName, "profile name")
	if c.Button(sdl.Rect{X: nameX + fieldW + 8, Y: y, W: saveW, H: fieldH}, "Save") {
		if name := strings.TrimSpace(a.layoutPresetName); name != "" {
			a.d.Prefs.SaveLayoutProfile(name, config.LayoutProfile{
				Classic:   a.d.Prefs.Load().ClassicLayoutOverrides(),
				Anchors:   a.d.Prefs.Load().ClassicAnchorSnapshot(),
				Hidden:    a.d.Prefs.Load().HiddenPanels(),
				GridPx:    a.d.Prefs.Load().LayoutGridSize(),
				Rotations: a.d.Prefs.Load().ClassicRotationSnapshot(), // A4: classic rotations travel with the profile
			})
			a.layoutPresetName = ""
		}
	}
	y += fieldH + 10

	// Saved profiles: load / delete each.
	if names := a.d.Prefs.Load().LayoutProfileNames(); len(names) == 0 {
		c.Label(pad, y, "No saved profiles yet — arrange your layout, then Save it above.", ColTextDim)
		y += 20
	} else {
		sort.Strings(names)
		for _, name := range names {
			if c.Button(sdl.Rect{X: pad, Y: y, W: 60, H: btnH}, "Load") {
				a.applyProfile(a.d.Prefs.Load().LayoutProfile(name))
				a.debug.push("layout: profile loaded")
			}
			if c.Button(sdl.Rect{X: pad + 66, Y: y, W: 64, H: btnH}, "Delete") {
				a.d.Prefs.DeleteLayoutProfile(name)
			}
			c.LabelClipped(pad+140, y+4, w-(pad+140)-8, name, ColText)
			y += btnH + 6
		}
	}
	y += 4

	y = a.settingsSection(y, w, "Lobby")
	// Plain lobby: the server list keeps the readable client backdrop instead of
	// the theme's lobbybackground (which is built for AO2's own list and often
	// makes ours unreadable). The courtroom still uses the theme either way.
	plain := a.d.Prefs.Load().PlainLobby
	if next := c.Checkbox(pad, y, "Plain lobby — keep my readable server-list backdrop, ignore the theme's lobby image (ON by default; the courtroom still uses the theme)", plain); next != plain {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.PlainLobby = next })
	}
	y += 28

	y = a.settingsSection(y, w, "Preview & binding")
	// Live preview of the applied chatbox skin + theme text colors.
	y = a.drawThemePreview(y)
	// Per-server theme binding: "this server always uses that theme".
	y = a.drawThemeBindRow(y, w)
	y = a.drawSettingsStageFX(y)
	return y
}

// drawSettingsStageFX: every render-side stage/viewport look — the sprite
// colour washes, motion FX, the retro post-process overlays (CRT / vignette /
// scanlines / grain), chatbox skins, the stage frame and the ambient extras
// (spotlight, breathing, reflection, weather). These edit how the VIEWPORT
// looks, which is theme territory.
func (a *App) drawSettingsStageFX(y int32) int32 {
	c := a.ctx
	pad := a.formX // rebase every pad-relative box into the content card
	w := a.formW2()
	y = a.settingsSection(y, w, "Stage & viewport effects")
	// The master off-switch leads the section: one tick box for players who want
	// none of this. It doesn't rewrite the rows below — it overrules them — so
	// unticking it hands back the exact set-up they had. The rows stay visible but
	// muted while it's on, which is what tells them what it's actually suppressing.
	noFX := a.d.Prefs.Load().DisableEffects
	if next := c.Checkbox(pad, y, "Disable all visual effects (OFF by default): one switch for a calm screen — stops other players' spinning / glowing / moving sprite styles, screen shake and flash, animated text, entrance slides, sprite washes and the post-processing overlays. Your settings below are remembered, just overruled while this is ticked.", noFX); next != noFX {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DisableEffects =

				next

		})

		noFX = next
		if a.link.room != nil { // apply to the live room immediately, like the rows below do
			a.link.room.ReduceMotion = a.d.Prefs.Load().ReduceMotionOn || noFX
			a.link.room.ScreenEffects = a.d.Prefs.Load().ScreenEffects && !noFX
			a.link.room.HideSpriteStyles = a.d.Prefs.Load().HideSpriteStyles || noFX
		}
	}
	y += 26
	// Mute only the rows the master actually overrules. The per-character chatbox
	// skin and the stage frame further down are NOT effects — they're AO2 parity
	// and decor — so the bracket is reopened around them below.
	unmute := c.pushMuted(noFX)
	// Screen effects (AO2 parity, ON by default): the \s screenshake and \f flash
	// codes you can type into a message, plus the field-based shake / realization.
	// Off keeps your screen still; "Reduce motion" (accessibility) also suppresses.
	seff := a.d.Prefs.Load().ScreenEffects
	if next := c.Checkbox(pad, y, "Enable screen effects (ON by default): screenshake (\\s) and flash (\\f) typed into messages, plus authored shake / realization. Turn off to keep your screen still.", seff); next != seff {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ScreenEffects = next })
		if a.link.room != nil {
			a.link.room.ScreenEffects = next // apply to the live room immediately
		}
	}
	y += 26
	// Sprite colour FX (all OFF by default): a render-side wash over the
	// on-stage characters. Local eye-candy only — nothing on the wire, nobody
	// else sees it, and zero render cost when everything's off.
	rbs := a.d.Prefs.Load().RainbowSprites
	if next := c.Checkbox(pad, y, "Rainbow character sprites (OFF by default): washes every on-stage sprite through a hue cycle — local eye-candy only, nobody else sees it, zero render cost when off", rbs); next != rbs {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowSprites = next })
	}
	y += 26
	if rbs {
		sp := a.d.Prefs.Load().RainbowSpriteSpeed
		c.Label(pad+16, y+4, "Speed:", ColTextDim)
		if next := int(c.Slider("rbspeed", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(sp), 100)); next != sp {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowSpriteSpeed = next })
			sp = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", sp), ColAccent)
		y += 26
		vv := a.d.Prefs.Load().RainbowSpriteVividness
		c.Label(pad+16, y+4, "Vividness:", ColTextDim)
		if next := int(c.Slider("rbvivid", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(vv), 100)); next != vv {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowSpriteVividness = next })
			vv = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", vv), ColAccent)
		y += 26
		dsy := a.d.Prefs.Load().RainbowPairDesync
		if next := c.Checkbox(pad+16, y, "Desync the pair's colour (the two characters show different hues at once)", dsy); next != dsy {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowPairDesync = next })
		}
		y += 26
		pch := a.d.Prefs.Load().RainbowPerChar
		if next := c.Checkbox(pad+16, y, "Different hue per character (each on-stage character cycles to its own colour)", pch); next != pch {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowPerChar = next })
		}
		y += 26
	}
	sst := a.d.Prefs.Load().SpriteSolidTint
	if next := c.Checkbox(pad, y, "Solid colour sprite tint (OFF by default): wash sprites in one fixed colour instead of a cycle (rainbow takes priority if both are on)", sst); next != sst {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteSolidTint = next })
	}
	y += 26
	if sst {
		c.Label(pad+16, y+4, "Tint colour:", ColTextDim)
		cur := a.d.Prefs.Load().SpriteTintColorRGB()
		sw := sdl.Rect{X: pad + 130, Y: y, W: 28, H: fieldH}
		c.Fill(sw, sdl.Color{R: uint8(cur >> 16 & 0xFF), G: uint8(cur >> 8 & 0xFF), B: uint8(cur & 0xFF), A: 255})
		c.Border(sw, ColPanelHi)
		if c.focusID != "spritetinthex" {
			a.sel.spriteTintHex = fmt.Sprintf("%06x", cur) // reflect the pref when not typing
		}
		if next, _ := c.TextField("spritetinthex", sdl.Rect{X: pad + 166, Y: y, W: 100, H: fieldH}, a.sel.spriteTintHex, "RRGGBB"); next != a.sel.spriteTintHex {
			a.sel.spriteTintHex = next
			if rgb, ok := parseHex6(next); ok {
				a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteTintColor = rgb })
			}
		}
		y += 28
	}
	if rbs || sst { // glow applies to whichever wash is on
		glw := a.d.Prefs.Load().RainbowSpriteGlow
		if next := c.Checkbox(pad+16, y, "Neon glow (additive): the tint adds light so sprites glow — they become translucent neon ghosts (you can see the room through them)", glw); next != glw {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.RainbowSpriteGlow = next })
		}
		y += 26
	}
	// Motion FX (independent of the colour wash, OFF by default).
	wob := a.d.Prefs.Load().SpriteWobble
	if next := c.Checkbox(pad, y, "Wobble sprites (OFF by default): a gentle, continuous sway", wob); next != wob {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteWobble = next })
	}
	y += 26
	spn := a.d.Prefs.Load().SpriteSpin
	if next := c.Checkbox(pad, y, "Spin sprites (OFF by default): the on-stage characters rotate slowly — maximum chaos", spn); next != spn {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteSpin = next })
	}
	y += 26
	punch := a.d.Prefs.Load().ShoutPunch
	if next := c.Checkbox(pad, y, "Shout punch (OFF by default): a quick zoom-pop of the whole stage when an objection/shout appears", punch); next != punch {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ShoutPunch = next })
	}
	y += 26
	// Post-processing overlays (#10): retro looks blended over the whole stage. All OFF.
	// #77 CRT preset leads the group: one toggle for the whole old-TV look.
	crt := a.d.Prefs.Load().PostCRT
	if next := c.Checkbox(pad, y, "CRT / retro TV (OFF by default): scanlines + an RGB phosphor grille + vignette — the whole old-TV look in one toggle", crt); next != crt {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.PostCRT = next })
	}
	y += 26
	vig := a.d.Prefs.Load().PostVignette
	if next := c.Checkbox(pad, y, "Vignette (OFF by default): darken the stage edges for a cinematic frame", vig); next != vig {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.PostVignette = next })
	}
	y += 26
	scan := a.d.Prefs.Load().PostScanlines
	if next := c.Checkbox(pad, y, "Scanlines (OFF by default): faint CRT scan lines over the stage", scan); next != scan {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.PostScanlines = next })
	}
	y += 26
	grain := a.d.Prefs.Load().PostGrain
	if next := c.Checkbox(pad, y, "Film grain (OFF by default): subtle animated noise over the stage", grain); next != grain {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.PostGrain = next })
	}
	y += 26
	c.popMuted(unmute) // the next two rows are parity/decor, not effects — keep them live
	// Per-character chatbox skins (char.ini chat=<misc>): canonical AO2/webAO
	// behaviour, default ON; off also stops the misc art fetches.
	ccb := a.d.Prefs.Load().CharChatbox
	if next := c.Checkbox(pad, y, "Per-character chatbox skins (ON by default): a speaker with their own chatbox art (char.ini chat=) shows it, like AO2/webAO", ccb); next != ccb {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				CharChatbox =

				next

		})

	}
	y += 26
	// #56 stage frame: a decorative border around the viewport (Off by default).
	c.Label(pad, y+4, "Stage frame:", ColText)
	sf := a.d.Prefs.StageFrame()
	if next, changed := c.Dropdown("stageframe", sdl.Rect{X: pad + 130, Y: y, W: 170, H: fieldH}, stageFrameNames, sf); changed {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.StageFrameKind = next })
	}
	c.Label(pad+312, y+4, "a decorative border on the stage — pure looks, zero cost when Off", ColTextDim)
	y += 30
	defer c.popMuted(c.pushMuted(noFX)) // back under the master for the rest of the section
	ent := a.d.Prefs.Load().AnimateEntrances
	if next := c.Checkbox(pad, y, "Animate entrances (OFF by default): a new speaker slides in when they take the stage", ent); next != ent {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AnimateEntrances =

				next

		})

	}
	y += 26
	dof := a.d.Prefs.Load().DepthOfField
	if next := c.Checkbox(pad, y, "Depth of field (OFF by default): soft-focus + dim the background behind the speaker", dof); next != dof {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DepthOfField =

				next

		})

	}
	y += 26
	// #121 speaker spotlight: dim the non-speaker characters + the desk. The Dim slider only
	// shows while it's on (like the rainbow knobs above).
	spot := a.d.Prefs.Load().Spotlight
	if next := c.Checkbox(pad, y, "Speaker spotlight (OFF by default): dim the other characters + the desk so the talking character pops", spot); next != spot {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.Spotlight = next })
		spot = next
	}
	y += 26
	if spot {
		lv := a.d.Prefs.Load().SpotlightLevel()
		c.Label(pad+16, y+4, "Dim:", ColTextDim)
		if next := int(c.Slider("spotdim", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(lv), 100)); next != lv {
			a.d.Prefs.SetSpotlightLevel(next)
			lv = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", lv), ColAccent)
		y += 26
	}
	// #122 idle breathing: a gentle bob + breathing-scale on static sprites (AsyncAO-only).
	// The amplitude/speed sliders + the two component toggles show only while it's on. The
	// keybind (Settings → Keybinds, or the default below) toggles it hands-free.
	brth := a.d.Prefs.Load().IdleBreath
	if next := c.Checkbox(pad, y, "Idle breathing (OFF by default): static sprites gently bob + breathe so they feel alive — only other AsyncAO users won't see your local view, this is your viewer", brth); next != brth {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				IdleBreath =

				next

		})

		brth = next
	}
	y += 26
	if brth {
		amp := a.d.Prefs.Load().BreathAmp()
		c.Label(pad+16, y+4, "Amount:", ColTextDim)
		if next := int(c.Slider("breathamp", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(amp), 100)); next != amp {
			a.d.Prefs.SetBreathAmp(next)
			amp = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", amp), ColAccent)
		y += 26
		spd := a.d.Prefs.Load().BreathSpeed()
		c.Label(pad+16, y+4, "Speed:", ColTextDim)
		if next := int(c.Slider("breathspd", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(spd), 100)); next != spd {
			a.d.Prefs.SetBreathSpeed(next)
			spd = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", spd), ColAccent)
		y += 26
		bob := a.d.Prefs.Load().BreathBobOn()
		if next := c.Checkbox(pad+16, y, "Vertical bob", bob); next != bob {
			a.d.Prefs.SetBreathBob(next)
		}
		scl := a.d.Prefs.Load().BreathScaleOn()
		if next := c.Checkbox(pad+180, y, "Breathing scale", scl); next != scl {
			a.d.Prefs.SetBreathScale(next)
		}
		y += 26
	}
	// #123 glass-floor reflection: a flipped, faded mirror of the sprites below the floor line.
	refl := a.d.Prefs.Load().Reflection
	if next := c.Checkbox(pad, y, "Glass-floor reflection (OFF by default): a flipped, faded mirror of the characters on the floor below them", refl); next != refl {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.Reflection = next })
		refl = next
	}
	y += 26
	if refl {
		op := a.d.Prefs.Load().ReflectStrength()
		c.Label(pad+16, y+4, "Opacity:", ColTextDim)
		if next := int(c.Slider("reflop", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(op), 100)); next != op {
			a.d.Prefs.SetReflectStrength(next)
			op = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", op), ColAccent)
		y += 26
	}
	// #124 ambient weather: a cycle picker (None → Snow → Rain → Sakura → Embers) + an
	// Intensity slider while a weather is on. The keybind cycles it hands-free.
	wk := a.d.Prefs.WeatherType()
	c.Label(pad, y+4, "Weather (OFF by default):", ColTextDim)
	if c.Button(sdl.Rect{X: pad + 210, Y: y, W: 90, H: 22}, render.WeatherName(render.Weather(wk))) {
		a.d.Prefs.SetWeatherType((wk + 1) % int(render.WeatherCount))
	}
	y += 26
	if wk != 0 {
		wi := a.d.Prefs.Load().WeatherIntensity()
		c.Label(pad+16, y+4, "Intensity:", ColTextDim)
		if next := int(c.Slider("wxint", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(wi), 100)); next != wi {
			a.d.Prefs.SetWeatherIntensity(next)
			wi = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", wi), ColAccent)
		y += 26
	}
	return y
}

// drawThemeFitPreview draws a big interactive preview of the Custom theme fit:
// the theme's courtroom background scaled + panned exactly as the live courtroom
// would (the box carries the window aspect, so the crop you see is the crop you
// get). Drag to pan, scroll to zoom — both write the prefs and re-fit live.
// Settings-screen only; never the courtroom hot path.
func (a *App) drawThemeFitPreview(box sdl.Rect) {
	c := a.ctx
	c.Fill(box, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	court, ok := a.skin.themeRects["courtroom"]
	if ok && court.W > 0 && court.H > 0 {
		base := float64(box.W) / float64(court.W) // min-fit within the box
		if by := float64(box.H) / float64(court.H); by < base {
			base = by
		}
		s := base * float64(a.d.Prefs.Load().ThemeZoom()) / 100
		artW, artH := int32(float64(court.W)*s), int32(float64(court.H)*s)
		px, py := a.d.Prefs.Load().ThemePan()
		art := sdl.Rect{
			X: box.X + (box.W-artW)/2 + int32(px)*box.W/100,
			Y: box.Y + (box.H-artH)/2 + int32(py)*box.H/100,
			W: artW, H: artH,
		}
		clipPrev, clipHad := c.pushClip(box)
		if page, pok := a.themePage("courtroombackground"); pok {
			_ = c.Ren.Copy(a.themeFrame(page), nil, &art)
		} else {
			c.Fill(art, ColPanel)
			c.Border(art, ColAccent)
			c.LabelClipped(art.X+6, art.Y+6, art.W-12, "(theme ships no courtroombackground — this outline is its design area)", ColTextDim)
		}
		c.popClip(clipPrev, clipHad)
	} else {
		c.LabelClipped(box.X+8, box.Y+8, box.W-16, "This theme has no courtroom_design.ini — the fit modes don't apply to it.", ColTextDim)
	}
	c.Border(box, ColAccent)
	c.Label(box.X+6, box.Y+4, "Drag to pan · scroll to zoom", ColTextDim)

	// Interaction: wheel zooms, drag pans; both clamp via the setters and re-fit
	// the live courtroom by invalidating the geometry cache.
	if c.hovering(box) && c.wheelY != 0 {
		c.wheelTaken = true
		{
			pv1 := a.d.Prefs.Load().ThemeZoom() + int(c.wheelY)*5
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.ThemeFitZoom = pv1
			})
		}
		a.invalidateThemeCanvases()
	}
	if c.hovering(box) && c.mouseDown && !a.client.themeFitDrag {
		a.client.themeFitDrag = true
		a.client.themeFitDragStart = [2]int32{c.mouseX, c.mouseY}
		px, py := a.d.Prefs.Load().ThemePan()
		a.client.themeFitDragBase = [2]int{px, py}
	}
	if a.client.themeFitDrag {
		if !c.mouseDown {
			a.client.themeFitDrag = false
		} else if box.W > 0 && box.H > 0 {
			dx := int(c.mouseX-a.client.themeFitDragStart[0]) * 100 / int(box.W)
			dy := int(c.mouseY-a.client.themeFitDragStart[1]) * 100 / int(box.H)
			a.d.Prefs.SetThemeFitPan(a.client.themeFitDragBase[0]+dx, a.client.themeFitDragBase[1]+dy)
			a.invalidateThemeCanvases()
		}
	}
}

// drawSettingsAssets: predictive prefetch, the missing-sprite placeholder, local
// mounts, the opt-in downloader, and the cache browser/actions. Format probing
// and the learned-format tools are on the Formats tab (settingsformats.go).
// prefetchAggroLabel names the predictive-prefetch level (#100) for the slider readout.
func prefetchAggroLabel(n int) string {
	switch n {
	case 1:
		return "Conservative (1 sprite)"
	case 2:
		return "Balanced (2 sprites)"
	case 3:
		return "Eager (3 sprites)"
	default:
		return "Aggressive (4 sprites)"
	}
}

func (a *App) drawSettingsAssets(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()

	// Pointer, not a duplicate: every format control (which file types are asked
	// for, the per-server profile, autodetect, fallbacks, the learned-format
	// tools) now lives on the Formats tab. Keeping a second copy here is what
	// scattered them in the first place.
	y = a.settingsSection(y, w, "Image & audio formats")
	y = a.settingsDesc(pad, y, "Art or sounds not loading on a server? The controls that decide which file type AsyncAO asks for — including the one-click fix for the server you're on — moved to their own tab.", ColTextDim)
	y += 6
	y = a.formatsTabPointer(y)

	y = a.settingsSection(y, w, "Predictive prefetch")
	c.LabelClipped(pad, y, w-pad-scrollBarW, "How many of the next sprites AsyncAO guesses ahead and warms while you chat — higher means snappier sprite swaps but more speculative downloading.", ColTextDim)
	y += 22
	aggro := int32(a.d.Prefs.Load().PrefetchAggressiveness())
	c.Label(pad, y+4, "Level:", ColText)
	if nv := c.Slider("prefetchaggro", sdl.Rect{X: pad + 70, Y: y, W: 200, H: btnH}, aggro, 4); nv != aggro {
		if nv < 1 {
			nv = 1
		}
		a.d.Prefs.SetPrefetchAggressiveness(int(nv))
		if a.link.room != nil && a.link.room.Predictor != nil {
			a.link.room.Predictor.SetAggressiveness(int(nv)) // apply live to the running predictor
		}
	}
	c.Label(pad+285, y+4, prefetchAggroLabel(a.d.Prefs.Load().PrefetchAggressiveness()), ColAccent)
	y += 30

	// (Image formats are on the Formats tab; desk-format policy is on Power user.)

	// #127 full-character bundle prefetch (default OFF): pre-grab a character's whole sprite
	// set on load so emote switches are instant — speculative + low priority, so it sheds
	// under load and never blocks live fetches (it uses more bandwidth + cache up front).
	bundle := a.d.Prefs.Load().CharBundlePrefetch
	if next := c.Checkbox(pad, y, "Preload a character's whole sprite set (OFF by default): grabs every emote up front so switching is instant — speculative, low-priority, more bandwidth", bundle); next != bundle {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				CharBundlePrefetch =

				next

		})

	}
	y += 28

	// #128 connection-quality chip: a tiny signal-bar icon (bottom-left) showing the server
	// round-trip time, with the exact ms on hover. Off by default → no ping loop runs.
	pingChip := a.d.Prefs.Load().PingChip
	if next := c.Checkbox(pad, y, "Connection ping chip (OFF by default): a tiny signal-bar icon (bottom-left); hover it for the exact ms", pingChip); next != pingChip {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				PingChip =

				next

		})

	}
	y += 28

	// Missing-asset chip: opt-in (default OFF). The failures always reach the
	// debug log; this only governs the count chip in the menu bar (GH #27 —
	// the old red banner it replaced is gone, so the label must not promise one).
	showWarn := a.d.Prefs.Load().ShowAssetWarnings
	if next := c.Checkbox(pad, y, "Show missing-asset warnings (OFF by default): a count chip in the menu bar — click it for Debug ▸ Log", showWarn); next != showWarn {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				ShowAssetWarnings =

				next

		})

	}
	y += 28

	// Loop preanimations (OFF by default): non-canonical. AO2 plays a preanim once
	// and settles into talk/idle; ON keeps it wrapping while it's on stage.
	loopPre := a.d.Prefs.Load().LoopPreanim
	if next := c.Checkbox(pad, y, "Loop preanimations (OFF by default): keep a preanim wrapping while it's on stage — AO2 plays them once", loopPre); next != loopPre {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				LoopPreanim =

				next

		})

	}
	y += 28

	// Missing-sprite placeholder (missingno) — the AO2 "placeholder" for a
	// character sprite whose whole fallback chain 404'd. This is where people
	// look for it. Distinct from
	// the still-loading hold: this fires ONLY once there is nothing left to
	// fetch, so it never flashes over a sprite that's merely streaming.
	y = a.settingsSection(y, w, "Missing sprites (missingno)")
	mno := a.d.Prefs.Load().ShowMissingPlaceholder
	if next := c.Checkbox(pad, y, "Show a placeholder for missing character sprites (like AO2's \"missingno\")", mno); next != mno {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ShowMissingPlaceholder = next })
	}
	y += 26
	y = a.settingsDesc(pad, y, "On (default): when a character's idle/talk sprite can't be found at all (every spelling and format 404'd), the stage shows a distinct placeholder image instead of holding the previous character. Off: the previous sprite stays (or the stage goes blank), exactly as before. Only fires once nothing is left to fetch — a still-downloading sprite never shows it.", ColTextDim)
	y += 6
	// Custom placeholder image path: TextField + Apply, with an inline error label
	// on a bad path. The load runs OFF-THREAD (rule §17.2); Apply re-triggers it.
	c.Label(pad, y+4, "Custom image:", ColText)
	if !settings.errorSpriteLoaded {
		settings.errorSpriteInput = a.d.Prefs.Load().ErrorSpriteFile
		settings.errorSpriteLoaded = true
	}
	var esCommit bool
	settings.errorSpriteInput, esCommit = c.TextField("errorsprite", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH},
		settings.errorSpriteInput, "path to a PNG/WebP/GIF/APNG/JPG (blank = built-in missingno)")
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Apply") || esCommit {
		raw := strings.TrimSpace(settings.errorSpriteInput)
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ErrorSpriteFile = raw })
		a.errorSpriteErr = ""   // clear a stale error; the load reports fresh via pollErrorSprite
		a.applyErrorSprite(raw) // "" restores the embedded default synchronously
		if raw == "" {
			settings.statusLine = "Custom missing-sprite image cleared — using the built-in placeholder."
		} else {
			settings.statusLine = "Loading custom missing-sprite image…"
		}
	}
	y += 30
	if a.errorSpriteErr != "" {
		c.LabelClipped(pad+130, y, w-pad-130-scrollBarW, "Couldn't load that image: "+a.errorSpriteErr+" — using the built-in placeholder.", ColDanger)
		y += 22
	}
	y = a.settingsDesc(pad, y, "Optional: replace the built-in glitch placeholder with your own image (a PNG, WebP, GIF, APNG or JPG). Loaded in the background; if the file can't be read or decoded it's ignored and the built-in one stays. Leave blank for the default.", ColTextDim)
	y += 10

	// (The per-type audio-format fallbacks are on the Formats tab, with the
	// image ones — they are the same control for a different asset class.)

	y = a.settingsSection(y, w, "Asset source")
	y = a.drawAssetSourceSettings(y, w)

	y = a.settingsSection(y, w, "Downloader")
	// Built-in single-asset downloader (opt-in).
	y = a.drawDownloaderSettings(y, w)

	y = a.settingsSection(y, w, "Cache")
	// Cache browser: live tier stats, T3 size on demand, open-in-Explorer.
	t2 := a.d.Manager.T2Stats()
	hitPct := 0.0
	if total := t2.Hits + t2.Misses; total > 0 {
		hitPct = float64(t2.Hits) / float64(total) * 100
	}
	c.Label(pad, y, fmt.Sprintf("Memory cache (T2): %d entries · %.1f / %.0f MiB · %.0f%% hit rate · %d evictions",
		t2.Entries, float64(t2.Bytes)/(1<<20), float64(t2.Budget)/(1<<20), hitPct, t2.Evictions), ColTextDim)
	y += 24
	zstdOn := a.d.Prefs.Load().DiskZstd
	if next := c.Checkbox(pad, y, "Compress disk cache with zstd (new writes only; smaller T3, tiny CPU on hits — old blobs always read fine)", zstdOn); next != zstdOn {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DiskZstd =

				next

		})

		a.d.Manager.SetDiskCompression(next)
	}
	y += 26
	// #34: opt-in T3 byte-budget auto-prune. DEFAULT 0 = unlimited (far-left of
	// the track) — T3's unboundedness is a deliberate spec exception, so an
	// update never silently deletes a cache. Past a positive cap the writer
	// goroutine sweeps the OLDEST blobs. The slider step keeps values round.
	c.Label(pad, y+4, "Disk cache size limit:", ColText)
	dcb := a.d.Prefs.Load().DiskCacheBudgetMiBVal
	dcbTrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	ndcb := int(clampI32(c.Slider("diskcachebudget", dcbTrack, int32(dcb), config.DiskCacheBudgetMaxMiB), 0, config.DiskCacheBudgetMaxMiB))
	if ndcb != 0 { // snap positive values to a coarse grid and pull sub-minimum up to the floor
		ndcb = (ndcb / diskCacheBudgetStepMiB) * diskCacheBudgetStepMiB
		if ndcb < config.DiskCacheBudgetMinMiB {
			ndcb = config.DiskCacheBudgetMinMiB
		}
	}
	c.Tooltip(dcbTrack, "Cap the on-disk asset cache (T3). Far LEFT = unlimited (default — nothing is ever auto-deleted). Set a limit and the OLDEST assets auto-delete once the cache grows past it.")
	dcbLabel := "unlimited (default)"
	if ndcb != 0 {
		dcbLabel = diskCacheBudgetLabel(ndcb)
	}
	c.Label(pad+200+236, y+4, dcbLabel, ColTextDim)
	if ndcb != dcb {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DiskCacheBudgetMiBVal =

				ndcb

		})

		a.d.Manager.SetDiskBudget(int64(a.d.Prefs.Load().DiskCacheBudgetMiBVal) << 20) // live; writer prunes on next sweep
	}
	y += 28
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Measure disk cache") {
		measureDiskCacheAsync(a.d.Manager.DiskRoot())
	}
	if c.Button(sdl.Rect{X: pad + 180, Y: y, W: 170, H: btnH}, "Open cache folder") {
		if root := a.d.Manager.DiskRoot(); root != "" {
			// Fire-and-forget Explorer launch; never blocks the frame.
			_ = exec.Command("explorer.exe", root).Start()
		}
	}
	y += 32

	// Cache actions. (The learned-format buttons — clear / export / import, and
	// "Fix stuck images" — are on the Formats tab: they are format state, not
	// cache state.)
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Clear disk cache") {
		if err := a.d.Manager.ClearDisk(); err != nil {
			settings.statusLine = "Clear failed: " + err.Error()
		} else {
			settings.statusLine = "Disk cache cleared."
		}
	}
	// Retry-missing: the manual half of the session miss memory. The client stops
	// asking for an asset once it has probed every format of it and come up empty,
	// which is what keeps it from re-404ing the server every couple of seconds
	// forever; that answer is otherwise only revisited on a reconnect or a folder
	// change, so a server that uploads the missing files mid-session needs a way
	// to say so without one.
	missCount := a.d.Manager.ConclusiveMissCount()
	if c.Button(sdl.Rect{X: pad + 180, Y: y, W: 190, H: btnH}, "Retry missing assets") {
		a.d.Manager.ForgetConclusiveMisses()
		a.d.Manager.ForgetNotFound() // the network 404 LRU + local miss memo
		if a.d.Store != nil {        // nil only in headless tests
			a.d.Store.ForgetMissing() // the render side's copy, or the viewport keeps drawing placeholders
		}
		settings.statusLine = "Will look for " + strconv.Itoa(missCount) + " missing asset(s) again."
	}
	c.Tooltip(sdl.Rect{X: pad + 180, Y: y, W: 190, H: btnH}, "Look again for assets this server didn't have. Use it if files were added while you were connected. Reconnecting does the same thing.")
	y += 30
	c.Label(pad, y, "\"Clear disk cache\" wipes the on-disk asset cache (T3); assets re-download fresh on next use.", ColTextDim)
	y += 18
	c.Label(pad, y, "\"Retry missing assets\" asks again for the "+strconv.Itoa(missCount)+" asset(s) this session found the server doesn't have.", ColTextDim)
	y += 18
	c.Label(pad, y, "Recommended after a server that's behind Cloudflare / a CDN updates its assets: otherwise the CDN —", ColTextDim)
	y += 18
	c.Label(pad, y, "or your cache — can keep serving the OLD file, so you'd see the wrong (outdated) version. Worth keeping", ColTextDim)
	y += 18
	c.Label(pad, y, "in mind if a character or background looks stale right after a server update.", ColTextDim)
	y += 30

	return y
}

// drawSettingsReset is the dedicated Reset tab: the factory-reset launcher, moved
// out of the Assets tab into its own section so it stands alone and isn't a
// misclick risk among other controls (playtest: factory reset wanted a section of
// its own, and the settings wanted splitting up).
func (a *App) drawSettingsReset(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.settingsSection(y, w, "Reset")
	c.Label(pad, y, "Reset the settings page, or wipe everything (favourites, logins, data, cache).", ColTextDim)
	y += 26
	// Factory reset: opens a pop-up offering settings-only or a full wipe.
	if c.Button(sdl.Rect{X: pad, Y: y, W: 220, H: btnH}, "Reset to defaults…") {
		a.inibrowse.showReset = true
	}
	y += 34
	return y
}

// drawResetConfirm is the factory-reset pop-up: settings-only (keeps your data,
// logins and cache) or a full wipe (erases everything, fresh-install state). It
// owns the screen + input while open, so its buttons can't double-fire with the
// settings widgets underneath.
func (a *App) drawResetConfirm(w, h int32) {
	c := a.ctx
	if c.escPressed { // ESC cancels the confirm (the safe default), like Cancel
		a.inibrowse.showReset = false
		return
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 235})
	pw, ph := int32(620), int32(300)
	panel := sdl.Rect{X: (w - pw) / 2, Y: (h - ph) / 2, W: pw, H: ph}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	x, y := panel.X+24, panel.Y+18
	c.Heading(x, y, "Reset AsyncAO", ColText)
	y += 40

	c.LabelClipped(x, y, pw-48, "Reset settings — restores the settings page to defaults (scales, volumes,", ColText)
	y += 18
	c.LabelClipped(x, y, pw-48, "theme, hotkeys, colours, toggles). KEEPS favourites, wardrobes, servers &", ColTextDim)
	y += 18
	c.LabelClipped(x, y, pw-48, "logins, callwords, learned formats, and your disk cache.", ColTextDim)
	y += 26
	if c.Button(sdl.Rect{X: x, Y: y, W: pw - 48, H: btnH}, "Reset settings (keep my data, logins & cache)") {
		a.applyFactoryReset(false)
	}
	y += 42

	c.LabelClipped(x, y, pw-48, "Wipe everything — a brand-new install: ALSO erases favourites, wardrobes,", ColDanger)
	y += 18
	c.LabelClipped(x, y, pw-48, "servers, logins, callwords, jukebox, notes, and the disk cache. No undo.", ColDanger)
	y += 26
	if c.Button(sdl.Rect{X: x, Y: y, W: pw - 48, H: btnH}, "WIPE EVERYTHING — logins, data, cache (no undo)") {
		a.applyFactoryReset(true)
	}
	y += 42
	if c.Button(sdl.Rect{X: x, Y: y, W: 120, H: btnH}, "Cancel") {
		a.inibrowse.showReset = false
	}
}

// applyFactoryReset resets preferences (settings-only or full wipe), clears the
// disk cache on a full wipe, then re-applies the derived UI state and refreshes
// the lobby so the change is visible immediately.
func (a *App) applyFactoryReset(wipeAll bool) {
	if wipeAll {
		a.d.Prefs.ResetAll()
		_ = a.d.Manager.ClearDisk()
		a.d.Resolver.InvalidateAll()
		if a.jukebox.juke != nil {
			a.jukebox.juke.Clear() // the jukebox library lives in its own file — wipe it too
		}
		if a.notebook != nil {
			a.notebook.Clear() // empty + stop its flush before deleting the files
		}
		_ = config.WipeNotebooks() // per-server case notes live in their own dir
		a.warnLine = "Everything wiped — fresh-install state."
	} else {
		a.d.Prefs.ResetSettings()
		a.warnLine = "Settings reset to defaults."
	}
	a.applyPrefsToState()
	// RefreshServers deliberately PRESERVES ping state now (the on-open
	// auto-refresh funnels through it), so a factory reset clears it here
	// explicitly — an ephemeral sort mode + RTT cache must not outlive
	// "fresh-install state". The gen bump discards an in-flight sweep.
	a.lobby.pings, a.lobby.pingMode, a.lobby.pinging = nil, false, false
	a.lobby.pingGen++
	a.RefreshServers() // favourites/master re-merge
	a.inibrowse.showReset = false
	a.warnAt = time.Now()
}

// applyPrefsToState re-pulls the App-cached values derived from preferences —
// the subset NewApp seeds — so a reset (or import) takes effect without a
// restart. Not a hot path.
func (a *App) applyPrefsToState() {
	a.classic.seedHiddenFromPrefs(&a.env) // hidden-chrome set + the A6 no-strand normalization
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = a.d.Prefs.Load().LayoutScales()
	a.oocPct = a.d.Prefs.Load().OOCScalePct
	a.uiScalePct = a.d.Prefs.Load().UIScalePct
	a.ctx.SetUIScale(a.UIScale())
	a.compose.oocName = a.defaultOOCName() // the active tab re-seeds; parked tabs keep theirs
	a.refreshCharKeys()
	a.applyThemeAsync()
	a.applyAudioVolumes()
	if a.link.room != nil {
		a.applyTimingToRoom()
		a.link.room.ForceCharNames = a.d.Prefs.Load().ForceCharNames
		a.link.room.ReduceMotion = a.d.Prefs.Load().ReduceMotionOn
		a.link.room.ScreenEffects = a.d.Prefs.Load().ScreenEffects
	}
}

// drawSettingsAudio: per-channel volumes (master/music/SFX/blip/alert) and music
// ducking. Split out of the old combined Audio & Chat tab so audio settings stand
// on their own (playtest: audio and chat did not belong in one group).
func (a *App) drawSettingsAudio(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.settingsSection(y, w, "Volume")
	// Volumes save PER SERVER while you're connected to one, so muting blips (or
	// anything) on one server leaves the others untouched — each tab keeps its own.
	// Edited from the lobby they set the global default new servers start from.
	if a.srvMeta.serverName != "" {
		c.Label(pad, y, "Saved for this server ("+a.srvMeta.serverName+") — each tab keeps its own volumes; a channel you never touch follows the global default.", ColTextDim)
		y += 22
	}
	em, emu, es, eb := a.effectiveVolumes()
	nm := a.volumeRow(y, "Master volume", em) // scales everything; also on the Extras box
	y += 30
	nmu := a.volumeRow(y, "Music volume", emu)
	y += 26
	ns := a.volumeRow(y, "SFX volume", es)
	y += 26
	nb := a.volumeRow(y, "Blip volume", eb)
	y += 26
	if nm != em || nmu != emu || ns != es || nb != eb {
		a.setEffectiveVolumes(nm, nmu, ns, nb) // per-server when connected, else global
	}
	// Callword/friend ping volume — its OWN control, independent of SFX (so a quiet
	// SFX mix or the SFX mute never silences your name-pings) and global (alerts
	// should reach you the same on every server).
	if av := a.volumeRow(y, "Callword/alert volume", a.d.Prefs.Load().AlertVol); av != a.d.Prefs.Load().AlertVol {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AlertVol =

				av

		})

		a.applyAudioVolumes()
	}
	y += 32
	// Music ducking (off by default): dip the music while a message plays.
	duck := a.d.Prefs.Load().MusicDuckingOn
	if next := c.Checkbox(pad, y, "Duck music while someone talks (lower music during a message so dialogue stays clear)", duck); next != duck {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				MusicDuckingOn =

				next

		})

	}
	y += 28
	// Custom /play music streaming (ON by default): fetch and play /play tracks.
	// OFF stops AsyncAO downloading any custom music — including your OWN plays,
	// which go silent for you (they still play for everyone else).
	stream := a.d.Prefs.Load().MusicStreaming
	if next := c.Checkbox(pad, y, "Stream custom /play music (off stops fetching /play tracks — including your own, which go silent for you)", stream); next != stream {
		a.applyMusicStreaming(next) // persists + re-syncs the room + stops playback on OFF
	}
	y += 28
	// Cross-tab music continuity (default OFF): music never stops on a server-tab
	// switch — it keeps playing and is ducked to silence by default so tabs stay
	// acoustically isolated (switching back restores it in sync). ON keeps a
	// backgrounded tab's music AUDIBLE while you browse another tab that has none.
	across := a.d.Prefs.Load().MusicAcrossTabs
	if next := c.Checkbox(pad, y, "Keep music playing (audible) across server tabs (off = keep it playing but silent while backgrounded)", across); next != across {
		// Both directions act immediately. ON un-wedges a backgrounded stream: it
		// clears BOTH silencers (the duck AND the delivery-await latch) and re-applies
		// volumes, so it works even when the stream is stuck behind a never-arriving
		// await. OFF re-ducks an audible background stream on the spot — the label
		// promises "silent while backgrounded" and the flip is the yank request
		// (ducked, not stopped: the position keeps advancing). See
		// applyMusicAcrossTabs.
		a.applyMusicAcrossTabs(next)
	}
	y += 28

	y = a.settingsSection(y, w, "Blips")
	rate, onSpaces := a.d.Prefs.Load().BlipTyping()
	if v := a.sliderRow(y, "Blip rate (1 blip / N letters)", rate, 1, config.MinBlipRate, config.MaxBlipRate); v != rate {
		a.d.Prefs.SetBlipTyping(v, onSpaces)
		a.applyTimingToRoom() // applies from the next message
	}
	c.LabelClipped(pad+300, y+4, w-pad-300-scrollBarW, "2 = Ace Attorney style · 1 = every letter", ColTextDim)
	y += 30
	if next := c.Checkbox(pad, y, "Blip on spaces too (off = skip whitespace, like Ace Attorney)", onSpaces); next != onSpaces {
		a.d.Prefs.SetBlipTyping(rate, next)
		a.applyTimingToRoom()
	}
	y += 28
	return y
}
