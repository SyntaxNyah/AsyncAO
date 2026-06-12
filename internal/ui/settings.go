package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// settingsState lives on App lazily (kept here for file cohesion).
type settingsState struct {
	mountInput string
	loaded     bool
	statusLine string
	scroll     int32 // page scroll (playtest: settings must wheel-scroll)

	// callwords edit buffer (loaded once per settings entry).
	callInput  string
	callLoaded bool

	// font override edit buffer (semicolon-separated chain).
	fontInput  string
	fontLoaded bool

	// importArmed routes the next dropped .json into ImportSettings.
	importArmed bool

	// macro editor buffers (name, captured key, |-separated lines).
	macroName  string
	macroKey   string
	macroLines string

	// theme-binding picker (shares the login section's server list cache).
	themeBindKey string

	// login section: the picked server + its credential edit buffers
	// (configurable for ANY known server, connected or not).
	loginKey      string
	loginUser     string
	loginPass     string
	loginAuto     bool
	loginLoaded   bool
	loginNames    []string // picker cache (WebSocketURL allocates)
	loginKeys     []string
	loginSrvCount int
	loginSrvFor   string

	// theme picker state: list scanning runs on a goroutine (directory
	// I/O stays off the render thread — §17.2) and lands on themeRes.
	themeName string
	themeDir  string
	themeList []string
	themeRes  chan themeScan
	themeBusy bool

	// folder picking: native dialog output / resolved drag-drops land
	// here from goroutines (never block or stat on the render thread).
	folderRes  chan string
	browseBusy bool

	// ioRes carries one-line results of off-thread file ops (learned
	// format export/import) back to the status line.
	ioRes chan string
}

// themeScan is one scan result: the theme names found, the NORMALIZED
// root (users paste the themes folder itself, or a single theme inside
// it — both resolve to the root theme.Load expects), and an optional
// auto-pick when the pasted path WAS a single theme.
type themeScan struct {
	names    []string
	root     string
	pickName string
}

var settings = settingsState{
	themeRes:  make(chan themeScan, 1),
	folderRes: make(chan string, 1),
	ioRes:     make(chan string, 1),
}

// imageTypes get the per-format toggle treatment.
var imageTypeNames = []string{
	config.TypeCharIcon,
	config.TypeCharSprite,
	config.TypeBackground,
	config.TypeDeskOverlay,
	config.TypeShoutBubble,
	config.TypeEmoteButton,
	config.TypeMisc,
}

func (a *App) drawSettings(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	c.Heading(pad, pad, "Settings", ColText)
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
		_ = a.d.Prefs.SaveNow() // Settings-Apply synchronous flush
		a.screen = a.prevScreen
		return
	}

	if !settings.loaded {
		settings.themeName, settings.themeDir = a.d.Prefs.Theme()
		if settings.themeName == "" {
			settings.themeName = theme.DefaultThemeName
		}
		settings.loaded = true
		a.scanThemes()
	}
	a.pollThemeScan()

	// The page flows from top minus the scroll offset; the wheel handler
	// and the bar live at the END of the function, where the content
	// height is known and every spinbox row has had first claim on the
	// wheel (wheelTaken).
	top := pad + 44
	y := top - settings.scroll

	// Showname: write-through to prefs. A stale once-per-session copy
	// here used to overwrite names typed in the courtroom on Back
	// (playtest: "open the settings and the showname gets reset").
	c.Label(pad, y+4, "Showname (saved):", ColText)
	shown := a.d.Prefs.SavedShowname()
	if next, _ := c.TextField("showname", sdl.Rect{X: pad + 150, Y: y, W: 220, H: fieldH}, shown, "Your showname"); next != shown {
		a.d.Prefs.SetShowname(next)
	}
	// Default OOC name: applied on every join (like the showname); blank
	// sends as a sticky AsyncAO<n> so commands always work.
	c.Label(pad+390, y+4, "OOC name:", ColText)
	if next, _ := c.TextField("oocdefault", sdl.Rect{X: pad + 480, Y: y, W: 200, H: fieldH}, a.oocName, "blank = AsyncAO<n>"); next != a.oocName {
		a.oocName = next
		a.d.Prefs.SetOOCName(next)
	}
	y += 38

	// Global toggles.
	global := a.d.Prefs.GlobalFallbacks()
	if next := c.Checkbox(pad, y, "Enable format fallbacks globally (probe legacy formats after the preferred one)", global); next != global {
		a.d.Prefs.SetGlobalFallbacks(next)
		a.d.Resolver.InvalidateAll()
		a.d.Resolver.WarmFromPrefs()
	}
	y += 26
	anims := a.d.Prefs.AnimationsEnabled()
	if next := c.Checkbox(pad, y, "Play animations (off = render first frames only; never affects network probes)", anims); next != anims {
		a.d.Prefs.SetAnimationsEnabled(next)
	}
	y += 26
	emoteImgs := a.d.Prefs.EmoteButtonImagesEnabled()
	if next := c.Checkbox(pad, y, "Image emote buttons (characters/<char>/emotions/button art — WebP by default, formats below)", emoteImgs); next != emoteImgs {
		a.d.Prefs.SetEmoteButtonImages(next)
	}
	y += 26
	dbg := a.d.Prefs.DebugOverlayEnabled()
	if next := c.Checkbox(pad, y, "Debug overlay (live log of failures: missing assets, theme problems, unhandled server packets)", dbg); next != dbg {
		a.d.Prefs.SetDebugOverlay(next)
	}
	y += 26
	streamer := a.d.Prefs.StreamerMode()
	if next := c.Checkbox(pad, y, "Streamer mode (masks OOC names + IPs in the log display, silences callword pings)", streamer); next != streamer {
		a.d.Prefs.SetStreamerMode(next)
	}
	y += 26
	smooth := a.d.Prefs.SmoothScalingEnabled()
	if next := c.Checkbox(pad, y, "Smooth texture scaling (linear filtering; re-streams loaded images when toggled)", smooth); next != smooth {
		a.d.Prefs.SetSmoothScaling(next)
		hint := "1"
		if !next {
			hint = "0"
		}
		// The hint applies at texture CREATION; purge so everything
		// re-streams (demand pipeline + scenery heal repopulate live).
		sdl.SetHint(sdl.HINT_RENDER_SCALE_QUALITY, hint)
		a.d.Store.Purge()
		c.purgeTextCache()
		a.themeChatbox = false
		a.applyThemeAsync()
		settings.statusLine = "Re-streaming textures with new filtering."
	}
	y += 26
	// Global scale: DPI-driven by default (HiDPI screens land readable),
	// with the manual spinbox taking over when auto is unticked.
	scaleAuto := a.d.Prefs.UIScaleAuto()
	scaleAutoLabel := "Auto UI scale from display DPI"
	if a.detectedScalePct > 0 {
		scaleAutoLabel = fmt.Sprintf("Auto UI scale from display DPI (this display: %d%%)", a.detectedScalePct)
	}
	if next := c.Checkbox(pad, y, scaleAutoLabel, scaleAuto); next != scaleAuto {
		a.d.Prefs.SetUIScaleAuto(next)
		a.ctx.SetUIScale(a.UIScale())
	}
	y += 26
	if scaleAuto {
		c.Label(pad, y+4, fmt.Sprintf("UI scale %%:  %d (auto)", a.UIScale()), ColTextDim)
	} else {
		uiPct := a.numberRow(y, "UI scale %", a.uiScalePct, config.UIScaleStepPercent, config.MinUIScalePercent, config.MaxUIScalePercent)
		if uiPct != a.uiScalePct {
			a.uiScalePct = uiPct
			a.ctx.SetUIScale(uiPct)
			a.d.Prefs.SetUIScale(uiPct)
		}
	}
	y += 34

	// IC/OOC font override: a chain of TTF/TTC paths, first covering font
	// per line wins (put a CJK-capable font later in the chain; sizes ride
	// the existing Text/Log knobs and Ctrl+wheel).
	c.Label(pad, y+4, "IC/OOC font:", ColText)
	if !settings.fontLoaded {
		settings.fontInput = a.d.Prefs.FontPaths()
		settings.fontLoaded = true
	}
	var fontCommit bool
	settings.fontInput, fontCommit = c.TextField("fontpaths", sdl.Rect{X: pad + 110, Y: y, W: 420, H: fieldH},
		settings.fontInput, `C:\Windows\Fonts\meiryo.ttc; more fallbacks... (blank = built-in)`)
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Apply") || fontCommit {
		raw := strings.TrimSpace(settings.fontInput)
		a.d.Prefs.SetFontPaths(raw)
		a.loadFontChainAsync(raw)
		if raw == "" {
			settings.statusLine = "Font override cleared — built-in font."
		}
	}
	if names := a.ctx.FontChainNames(); len(names) > 0 {
		c.LabelClipped(pad+620, y+4, w-pad-620-scrollBarW, "chain: "+strings.Join(names, " → "), ColTextDim)
	}
	y += 34

	// Theme picker: cycle through scanned themes; the folder field points
	// at a custom root containing themes/<name> directories.
	c.Label(pad, y+4, "Theme:", ColText)
	if c.Button(sdl.Rect{X: pad + 60, Y: y, W: 26, H: btnH}, "<") {
		a.cycleTheme(-1)
	}
	nameW := c.TextWidth(settings.themeName)
	c.Label(pad+96, y+6, settings.themeName, ColAccent)
	if c.Button(sdl.Rect{X: pad + 104 + nameW, Y: y, W: 26, H: btnH}, ">") {
		a.cycleTheme(1)
	}
	if settings.themeBusy {
		c.Label(pad+140+nameW, y+6, "scanning...", ColTextDim)
	} else {
		c.Label(pad+140+nameW, y+6, fmt.Sprintf("(%d found)", len(settings.themeList)), ColTextDim)
	}
	y += 32
	c.Label(pad, y+4, "Theme folder:", ColText)
	settings.themeDir, _ = c.TextField("themedir", sdl.Rect{X: pad + 110, Y: y, W: 340, H: fieldH}, settings.themeDir, `optional root holding themes\<name> — or drop a folder anywhere`)
	if c.Button(sdl.Rect{X: pad + 460, Y: y, W: 130, H: btnH}, "Apply & rescan") {
		a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
		a.scanThemes()
		a.applyThemeAsync()
	}
	if runtime.GOOS == "windows" {
		if c.Button(sdl.Rect{X: pad + 600, Y: y, W: 90, H: btnH}, "Browse...") {
			browseForFolder()
		}
	}
	// Drag-and-drop: SDL DROPFILE anywhere on this screen points the
	// theme folder (a dropped file resolves to its directory, off-thread)
	// — unless a settings import is armed, which claims .json drops.
	if c.dropped != "" {
		if settings.importArmed && strings.EqualFold(filepath.Ext(c.dropped), ".json") {
			settings.importArmed = false
			importSettingsAsync(a, c.dropped)
		} else {
			resolveDroppedFolder(c.dropped)
		}
	}
	a.pollFolderPick()
	y += 36

	// Theme-driven courtroom geometry (courtroom_design.ini).
	tlay := a.d.Prefs.ThemeLayoutEnabled()
	if next := c.Checkbox(pad, y, "Use the theme's courtroom layout (courtroom_design.ini positions every widget; off = classic layout)", tlay); next != tlay {
		a.d.Prefs.SetThemeLayout(next)
	}
	y += 28

	// Live preview: the actual applied chatbox skin + theme text colors,
	// so "did the theme land?" is answerable without joining a server.
	y = a.drawThemePreview(y)

	// Per-server theme binding: "this server always uses that theme".
	y = a.drawThemeBindRow(y, w)

	// Format detection mode: the server manifest by default, manual
	// per-type probing when switched off. While auto is ON the manual
	// rows are read-only — the server's extensions.json owns the formats.
	auto := a.d.Prefs.FormatAutoDetect()
	if next := c.Checkbox(pad, y, "Auto-detect formats from the server's extensions.json on connect (recommended)", auto); next != auto {
		auto = next
		a.d.Prefs.SetFormatAutoDetect(next)
		if next {
			a.manifestFor = "" // re-check the current server right away
			a.fetchManifestAsync()
		}
	}
	y += 26

	// Per-type format toggles (interactive only in manual mode).
	if auto {
		c.Label(pad, y, "Manual tuning disabled — formats come from each server's extensions.json (untick above to tune by hand):", ColTextDim)
		y += 22
		for _, typeName := range imageTypeNames {
			c.Label(pad, y+2, typeName+":", ColTextDim)
			c.Label(pad+110, y+2, strings.Join(a.d.Prefs.FormatOrder(typeName), "  "), ColTextDim)
			y += 26
		}
	} else {
		c.Label(pad, y, "Image formats probed per asset type (defaults: char_icon=PNG only, everything else=WebP only):", ColTextDim)
		y += 22
		for _, typeName := range imageTypeNames {
			y = a.drawTypeFormatRow(typeName, y)
		}
	}
	y += 8

	// Audio volumes.
	music, sfx, blip := a.d.Prefs.AudioVolumes()
	music = a.volumeRow(y, "Music volume", music)
	y += 26
	sfx = a.volumeRow(y, "SFX volume", sfx)
	y += 26
	blip = a.volumeRow(y, "Blip volume", blip)
	y += 32
	if m0, s0, b0 := a.d.Prefs.AudioVolumes(); m0 != music || s0 != sfx || b0 != blip {
		a.d.Prefs.SetAudioVolumes(music, sfx, blip)
		a.d.Audio.SetVolumes(a.d.Prefs.AudioVolumes())
	}

	// Message timing (AO2-Client options.ini parity); applies live.
	crawl, stay, rate := a.d.Prefs.Timing()
	crawl = a.numberRow(y, "Text crawl ms", crawl, 5, config.MinTextCrawlMs, config.MaxTextCrawlMs)
	y += 26
	stay = a.numberRow(y, "Text stay ms", stay, 100, 0, config.MaxTextStayMs)
	y += 26
	rate = a.numberRow(y, "Chat limit ms", rate, 100, 0, config.MaxChatRateLimitMs)
	y += 30
	if c0, s0, r0 := a.d.Prefs.Timing(); c0 != crawl || s0 != stay || r0 != rate {
		a.d.Prefs.SetTiming(crawl, stay, rate)
		a.applyTimingToRoom()
	}

	// Case announcements (CASEA, tsuserver-family): subscribe by role.
	y = a.drawCasingRow(y)

	// Discord Rich Presence (optional — never required to build or run).
	y = a.drawDiscordRow(y, w)

	// Callwords: comma-separated highlight words (flash + sound on match).
	c.Label(pad, y+4, "Callwords:", ColText)
	if !settings.callLoaded {
		settings.callInput = strings.Join(a.d.Prefs.CallWords(), ", ")
		settings.callLoaded = true
	}
	var callCommit bool
	settings.callInput, callCommit = c.TextField("callwords", sdl.Rect{X: pad + 110, Y: y, W: 420, H: fieldH}, settings.callInput, "your name, nickname, ... (flash + sound when seen in IC/OOC)")
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Save") || callCommit {
		a.d.Prefs.SetCallWords(strings.Split(settings.callInput, ","))
		settings.statusLine = "Callwords saved."
	}
	y += 34

	// Hotkeys: Ctrl+<key> per action (blank = the default shown).
	c.Label(pad, y, "Hotkeys (Ctrl + key — single letters/digits; blank uses the default):", ColTextDim)
	y += 22
	hx := pad
	for _, def := range hotkeyDefs {
		c.Label(hx, y+4, def.label+":", ColText)
		cur := a.d.Prefs.Hotkey(def.id)
		placeholder := def.def
		next, _ := c.TextField("hk_"+def.id, sdl.Rect{X: hx + 150, Y: y, W: 44, H: fieldH}, cur, placeholder)
		if next != cur {
			a.d.Prefs.SetHotkey(def.id, strings.ToLower(strings.TrimSpace(next)))
		}
		hx += 210
		if hx > 700 {
			hx = pad
			y += 30
		}
	}
	y += 36

	// Master list override (blank = official). Refresh in the lobby applies.
	c.Label(pad, y+4, "Master list:", ColText)
	master := a.d.Prefs.MasterList()
	if next, _ := c.TextField("masterurl", sdl.Rect{X: pad + 110, Y: y, W: 420, H: fieldH}, master, network.DefaultMasterServerURL); next != master {
		a.d.Prefs.SetMasterList(next)
	}
	y += 34

	// Audio fallbacks.
	for _, typeName := range []string{config.TypeSFX, config.TypeMusic, config.TypeBlip} {
		enabled := a.d.Prefs.TypeFallbacksEnabled(typeName)
		if next := c.Checkbox(pad, y, typeName+": probe legacy audio formats (.ogg/.wav/.mp3) after .opus", enabled); next != enabled {
			a.d.Prefs.SetTypeFallbacks(typeName, next)
			a.d.Resolver.InvalidateAll()
			a.d.Resolver.WarmFromPrefs()
		}
		y += 24
	}
	y += 10

	// Local assets (no-streaming legacy mode).
	enabled, mounts := a.d.Prefs.LocalAssets()
	if next := c.Checkbox(pad, y, "Read assets from local folders instead of streaming (legacy servers without an asset URL)", enabled); next != enabled {
		a.d.Prefs.SetLocalAssets(next, mounts)
		a.rebuildAssetOrigin()
	}
	y += 28
	c.Label(pad, y+4, "Mount folder:", ColText)
	settings.mountInput, _ = c.TextField("mount", sdl.Rect{X: pad + 110, Y: y, W: 340, H: fieldH}, settings.mountInput, `C:\AO2\base or /home/you/ao2/base`)
	if c.Button(sdl.Rect{X: pad + 460, Y: y, W: 80, H: btnH}, "Add") && strings.TrimSpace(settings.mountInput) != "" {
		a.d.Prefs.SetLocalAssets(enabled, append(mounts, strings.TrimSpace(settings.mountInput)))
		settings.mountInput = ""
		a.rebuildAssetOrigin()
	}
	y += 32
	for i, m := range mounts {
		c.LabelClipped(pad+20, y+4, w-220, fmt.Sprintf("%d. %s", i+1, m), ColText)
		if c.Button(sdl.Rect{X: w - 180, Y: y, W: 90, H: 24}, "Remove") {
			next := append(append([]string{}, mounts[:i]...), mounts[i+1:]...)
			a.d.Prefs.SetLocalAssets(enabled, next)
			a.rebuildAssetOrigin()
			break
		}
		y += 28
	}
	y += 10

	// Cache browser: live tier stats, T3 size on demand, open-in-Explorer.
	t2 := a.d.Manager.T2Stats()
	hitPct := 0.0
	if total := t2.Hits + t2.Misses; total > 0 {
		hitPct = float64(t2.Hits) / float64(total) * 100
	}
	c.Label(pad, y, fmt.Sprintf("Memory cache (T2): %d entries · %.1f / %.0f MiB · %.0f%% hit rate · %d evictions",
		t2.Entries, float64(t2.Bytes)/(1<<20), float64(t2.Budget)/(1<<20), hitPct, t2.Evictions), ColTextDim)
	y += 24
	zstdOn := a.d.Prefs.DiskZstdEnabled()
	if next := c.Checkbox(pad, y, "Compress disk cache with zstd (new writes only; smaller T3, tiny CPU on hits — old blobs always read fine)", zstdOn); next != zstdOn {
		a.d.Prefs.SetDiskZstd(next)
		a.d.Manager.SetDiskCompression(next)
	}
	y += 26
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

	// Cache actions.
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Clear disk cache") {
		if err := a.d.Manager.ClearDisk(); err != nil {
			settings.statusLine = "Clear failed: " + err.Error()
		} else {
			settings.statusLine = "Disk cache cleared."
		}
	}
	if c.Button(sdl.Rect{X: pad + 180, Y: y, W: 190, H: btnH}, "Clear learned formats") {
		a.d.Prefs.ClearLearned()
		a.d.Resolver.InvalidateAll()
		settings.statusLine = "Learned formats cleared."
	}
	// Learned-format portability: one player's warm state seeds another's.
	if c.Button(sdl.Rect{X: pad + 380, Y: y, W: 150, H: btnH}, "Export learned") {
		exportLearnedAsync(a)
	}
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 150, H: btnH}, "Import learned") {
		importLearnedAsync(a)
	}
	y += 32

	// Auto-login: ITS OWN automation, not a macro — per-server creds,
	// software-detected wire flow, fires on join (or via hotkey/button).
	y = a.drawLoginSettings(y, w)
	y += 8

	// Macros: user-defined OOC command sequences with optional keybinds.
	y = a.drawMacroSettings(y, w)
	y += 8

	// Whole-settings portability: the new-PC bundle (every knob,
	// favorites, per-server wardrobes/keybinds, learned formats).
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Export settings") {
		exportSettingsAsync(a)
	}
	importLabel := "Import settings..."
	if settings.importArmed {
		importLabel = "Drop the .json here"
	}
	if c.Button(sdl.Rect{X: pad + 180, Y: y, W: 190, H: btnH}, importLabel) {
		settings.importArmed = !settings.importArmed
		if settings.importArmed {
			settings.statusLine = "Drop an exported asyncao-settings .json anywhere on this window."
		}
	}
	y += 36
	select {
	case line := <-settings.ioRes:
		settings.statusLine = line
	default:
	}
	if settings.statusLine != "" {
		c.Label(pad, y, settings.statusLine, ColAccent)
		y += 24
	}

	// Page scroll: wheel anywhere a spinbox row didn't already consume
	// it, plus a draggable bar on the right edge.
	contentH := (y + settings.scroll) - top + pad
	visibleH := h - top - pad
	if !c.ctrlHeld && !c.wheelTaken {
		settings.scroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: w - scrollBarW - 2, Y: top, W: scrollBarW, H: visibleH}
	settings.scroll = c.VScrollbar("settscroll", track, settings.scroll, contentH, visibleH)
}

// measureDiskCacheAsync walks the T3 directory off-thread and reports the
// blob count + total size on the status line.
func measureDiskCacheAsync(root string) {
	if root == "" {
		return
	}
	go func() {
		var files int
		var bytes int64
		_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // best-effort measurement
			}
			if info, ierr := d.Info(); ierr == nil {
				files++
				bytes += info.Size()
			}
			return nil
		})
		line := fmt.Sprintf("Disk cache (T3): %d blobs · %.1f MiB at %s", files, float64(bytes)/(1<<20), root)
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// learnedExportFileName sits next to the executable — easy to hand to a
// friend, easy to find.
const learnedExportFileName = "learned-formats.json"

func learnedExportPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), learnedExportFileName), nil
}

// exportLearnedAsync writes the learned table off-thread (§17.2: no sync
// disk I/O on the render thread) and reports on the status line.
func exportLearnedAsync(a *App) {
	go func() {
		path, err := learnedExportPath()
		if err == nil {
			var data []byte
			if data, err = a.d.Prefs.ExportLearnedJSON(); err == nil {
				err = os.WriteFile(path, data, 0o644)
			}
		}
		line := "Learned formats exported to " + path
		if err != nil {
			line = "Export failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// exportSettingsAsync writes the whole-settings bundle beside the exe
// (timestamped, so repeated exports never clobber each other).
func exportSettingsAsync(a *App) {
	go func() {
		var path string
		exe, err := os.Executable()
		if err == nil {
			path = filepath.Join(filepath.Dir(exe),
				"asyncao-settings-"+time.Now().Format("20060102-150405")+".json")
			err = a.d.Prefs.ExportSettings(path)
		}
		line := "Settings exported to " + path + " — copy it to the new PC and Import there."
		if err != nil {
			line = "Settings export failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// importSettingsAsync replaces the preferences file with a dropped
// bundle; the import owns the file from then on (saver freezes) and
// applies on the next start.
func importSettingsAsync(a *App, path string) {
	go func() {
		line := "Settings imported — RESTART AsyncAO to apply (changes made this session won't save)."
		if err := a.d.Prefs.ImportSettings(path); err != nil {
			line = "Settings import failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// importLearnedAsync merges a learned-formats export and republishes the
// resolver snapshot (its table swap is atomic — safe off-thread).
func importLearnedAsync(a *App) {
	go func() {
		var line string
		path, err := learnedExportPath()
		if err == nil {
			var data []byte
			if data, err = os.ReadFile(path); err == nil {
				var n int
				if n, err = a.d.Prefs.ImportLearnedJSON(data); err == nil {
					a.d.Resolver.WarmFromPrefs()
					line = fmt.Sprintf("Imported %d learned entries from %s", n, path)
				}
			}
		}
		if err != nil {
			line = "Import failed: " + err.Error() + " (expected " + learnedExportFileName + " beside the exe)"
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// drawTypeFormatRow renders the per-type format checkboxes; ticking builds a
// new format order: the type's default first, then enabled extras in the
// OptionalImageFormats order.
func (a *App) drawTypeFormatRow(typeName string, y int32) int32 {
	c := a.ctx
	c.Label(pad, y+2, typeName+":", ColText)
	x := pad + 110

	current := a.d.Prefs.FormatOrder(typeName)
	enabled := map[string]bool{}
	for _, ext := range current {
		enabled[ext] = true
	}

	changed := false
	for _, ext := range config.OptionalImageFormats {
		on := enabled[ext]
		next := c.Checkbox(x, y, ext, on)
		if next != on {
			enabled[ext] = next
			changed = true
		}
		x += c.TextWidth(ext) + 46
	}
	if changed {
		def := config.DefaultFormatOrder(typeName)
		order := make([]string, 0, len(config.OptionalImageFormats))
		for _, ext := range def {
			if enabled[ext] {
				order = append(order, ext)
			}
		}
		for _, ext := range config.OptionalImageFormats {
			if enabled[ext] && !containsExt(order, ext) {
				order = append(order, ext)
			}
		}
		if len(order) == 0 {
			order = def // never allow zero probes
		}
		a.d.Prefs.SetFormatOrder(typeName, order)
		a.d.Resolver.InvalidateAll()
		a.d.Resolver.WarmFromPrefs()
	}

	// Probe-order chips: with 2+ formats ticked, clicking a chip promotes
	// it one slot toward "probed first" (zero-fallback order is the user's
	// to arrange — ticking chooses the set, chips choose the order).
	if len(current) > 1 && !changed {
		c.Label(x+12, y+2, "order:", ColTextDim)
		cx := x + 12 + c.TextWidth("order:") + 8
		for i, ext := range current {
			bw := c.TextWidth(ext) + 14
			if c.Button(sdl.Rect{X: cx, Y: y, W: bw, H: 22}, ext) && i > 0 {
				order := append([]string(nil), current...)
				order[i-1], order[i] = order[i], order[i-1]
				a.d.Prefs.SetFormatOrder(typeName, order)
				a.d.Resolver.InvalidateAll()
				a.d.Resolver.WarmFromPrefs()
			}
			cx += bw + 6
		}
	}
	return y + 26
}

func containsExt(list []string, ext string) bool {
	for _, e := range list {
		if e == ext {
			return true
		}
	}
	return false
}

// drawDiscordRow renders the optional Rich Presence section: a master
// toggle (default OFF), one checkbox per displayed field (the tick-on
// defaults show showname + character + server; the area stays private
// unless chosen), and the application-ID field. Returns the next y.
func (a *App) drawDiscordRow(y, w int32) int32 {
	c := a.ctx
	dp := a.d.Prefs.Discord()
	changed := false
	if next := c.Checkbox(pad, y, "Discord Rich Presence (\"Playing AsyncAO\" on your profile while Discord runs; fully optional)", dp.Enabled); next != dp.Enabled {
		dp.Enabled = next
		changed = true
	}
	y += 26
	if dp.Enabled {
		c.Label(pad+20, y+2, "Show:", ColTextDim)
		x := pad + 70
		fields := []struct {
			label string
			v     *bool
		}{
			{"server", &dp.ShowServer},
			{"character", &dp.ShowChar},
			{"showname", &dp.ShowName},
			{"area", &dp.ShowArea},
		}
		for _, f := range fields {
			if next := c.Checkbox(x, y, f.label, *f.v); next != *f.v {
				*f.v = next
				changed = true
			}
			x += c.TextWidth(f.label) + 52
		}
		y += 28
		c.Label(pad+20, y+4, "App ID:", ColText)
		if next, _ := c.TextField("discordappid", sdl.Rect{X: pad + 90, Y: y, W: 220, H: fieldH}, dp.AppID, "Discord application ID"); next != dp.AppID {
			dp.AppID = next
			changed = true
		}
		status := "(create an app named AsyncAO at discord.com/developers, icon asset \"appicon\"; ID changes apply on restart)"
		if a.d.Presence != nil {
			status = "status: " + a.d.Presence.Status() + " — ID changes apply on restart"
		}
		c.LabelClipped(pad+320, y+4, w-pad-330, status, ColTextDim)
		y += 32
	}
	if changed {
		a.d.Prefs.SetDiscord(dp)
		a.updatePresence()
	}
	return y + 4
}

// casingRoles drives the per-role subscription checkboxes (wire order).
var casingRoles = []struct {
	bit   int
	label string
}{
	{courtroom.CaseRoleDef, "def"},
	{courtroom.CaseRolePro, "pro"},
	{courtroom.CaseRoleJudge, "judge"},
	{courtroom.CaseRoleJury, "jury"},
	{courtroom.CaseRoleSteno, "steno"},
}

// drawCasingRow renders the case-announcement subscription (SETCASE roles);
// changes re-subscribe live when connected. Returns the next y.
func (a *App) drawCasingRow(y int32) int32 {
	c := a.ctx
	enabled, roles := a.d.Prefs.Casing()
	changed := false
	if next := c.Checkbox(pad, y, "Case announcements (get notified when someone needs your role)", enabled); next != enabled {
		enabled = next
		changed = true
	}
	y += 26
	if enabled {
		x := pad + 20
		for _, r := range casingRoles {
			on := roles&r.bit != 0
			if next := c.Checkbox(x, y, r.label, on); next != on {
				roles ^= r.bit
				changed = true
			}
			x += c.TextWidth(r.label) + 52
		}
		y += 26
	}
	if changed {
		a.d.Prefs.SetCasing(enabled, roles)
		a.sendCasingPrefs() // live re-subscribe (no-op when disconnected)
	}
	return y + 8
}

// --- theme picker -----------------------------------------------------------

// drawThemePreview renders the applied theme's chatbox skin (or the flat
// fallback panel) with sample text in the theme's colors — instant visual
// proof of what the current pick actually changes. Returns the next y.
func (a *App) drawThemePreview(y int32) int32 {
	c := a.ctx
	const prevW, prevH = 340, 70
	prev := sdl.Rect{X: pad, Y: y, W: prevW, H: prevH}
	skinned := false
	if page, ok := a.themePage(themeStemChatbox); ok {
		_ = c.Ren.Copy(a.themeFrame(page), nil, &prev)
		skinned = true
	}
	if !skinned {
		c.Fill(prev, sdl.Color{R: 16, G: 16, B: 24, A: 215})
		c.Border(prev, ColAccent)
	}
	// Same skin-gated color rule as the live chatbox (readability).
	nameCol := ColAccent
	if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	msgCol := ColText
	if skinned && a.themeHasMsg {
		msgCol = a.themeMsgCol
	}
	c.Label(prev.X+8, prev.Y+6, "Showname", nameCol)
	c.Label(prev.X+8, prev.Y+30, "Message text preview.", msgCol)
	label := "preview: theme chatbox skin"
	if !skinned {
		label = "preview: no chatbox skin in this theme (flat panel)"
	}
	c.Label(prev.X+prevW+12, prev.Y+6, label, ColTextDim)
	return y + prevH + 10
}

// drawThemeBindRow binds the PICKED theme to a chosen server: joining
// that server applies the bound theme, leaving restores the global one.
// Works for any known server (same picker as the login section).
func (a *App) drawThemeBindRow(y, w int32) int32 {
	c := a.ctx
	names, keys := a.loginTargets()
	if len(names) == 0 {
		return y
	}
	cur := 0
	if settings.themeBindKey == "" && a.serverKey != "" {
		settings.themeBindKey = a.serverKey
	}
	for i, k := range keys {
		if k == settings.themeBindKey {
			cur = i
			break
		}
	}
	settings.themeBindKey = keys[cur]
	c.Label(pad, y+4, "Bind theme to server:", ColText)
	if next, changed := c.Dropdown("themebindsrv", sdl.Rect{X: pad + 170, Y: y, W: 260, H: btnH}, names, cur); changed {
		settings.themeBindKey = keys[next]
	}
	bound := a.d.Prefs.ServerWarmInfoFor(settings.themeBindKey).Theme
	if c.Button(sdl.Rect{X: pad + 440, Y: y, W: 150, H: btnH}, "Bind "+clampLine(settings.themeName)) {
		a.d.Prefs.SetServerTheme(settings.themeBindKey, settings.themeName)
		if settings.themeBindKey == a.serverKey && a.sess != nil {
			a.themeBound = settings.themeName
			a.ensureThemeForSession()
		}
		settings.statusLine = "Theme " + settings.themeName + " bound — that server always uses it now."
	}
	if bound != "" {
		if c.Button(sdl.Rect{X: pad + 600, Y: y, W: 90, H: btnH}, "Unbind") {
			a.d.Prefs.SetServerTheme(settings.themeBindKey, "")
			if settings.themeBindKey == a.serverKey && a.sess != nil {
				a.themeBound = ""
				a.ensureThemeForSession()
			}
			settings.statusLine = "Theme binding removed."
		}
		c.LabelClipped(pad+700, y+4, w-pad-700-scrollBarW, "bound: "+bound, ColAccent)
	} else {
		c.Label(pad+600, y+4, "no binding (uses the global theme)", ColTextDim)
	}
	return y + 32
}

// cycleTheme steps through the scanned theme list and persists the pick.
func (a *App) cycleTheme(step int) {
	list := settings.themeList
	if len(list) == 0 {
		return
	}
	idx := 0
	for i, name := range list {
		if name == settings.themeName {
			idx = i
			break
		}
	}
	idx = (idx + step + len(list)) % len(list)
	settings.themeName = list[idx]
	a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
	a.applyThemeAsync() // chatbox skin + colors follow the pick live
}

// scanThemes lists themes/<name> directories under the custom root and the
// executable's directory, off-thread; pollThemeScan picks up the result.
func (a *App) scanThemes() {
	if settings.themeBusy {
		return
	}
	settings.themeBusy = true
	customRoot := strings.TrimSpace(settings.themeDir)
	go func() {
		root, pick := normalizeThemeRoot(customRoot)
		roots := make([]string, 0, 2)
		if root != "" {
			roots = append(roots, root)
		}
		if exe, err := os.Executable(); err == nil {
			roots = append(roots, filepath.Dir(exe))
		}
		settings.themeRes <- themeScan{names: scanThemeDirs(roots), root: root, pickName: pick}
	}()
}

func (a *App) pollThemeScan() {
	select {
	case res := <-settings.themeRes:
		settings.themeBusy = false
		settings.themeList = res.names
		// The scanner may have normalized the pasted path (the themes
		// folder itself, or one theme inside it) into the root
		// theme.Load expects — reflect and persist it.
		if res.root != "" && res.root != strings.TrimSpace(settings.themeDir) {
			settings.themeDir = res.root
			settings.statusLine = "Theme folder normalized to " + res.root
		}
		if res.pickName != "" {
			settings.themeName = res.pickName
		}
		if res.root != "" || res.pickName != "" {
			a.d.Prefs.SetTheme(settings.themeName, settings.themeDir)
			a.applyThemeAsync()
		}
	default:
	}
}

// themeINIFiles marks a directory as a single theme folder.
var themeINIFiles = []string{theme.DesignFileName, theme.FontsFileName, theme.SoundsFileName}

// normalizeThemeRoot turns whatever the user pasted or dropped into the
// root theme.Load expects (the folder CONTAINING themes/). Users paste
// all three shapes — the root, the themes folder itself, or a single
// theme inside it (returned as pickName and auto-selected). Runs off the
// render thread (it stats directories).
func normalizeThemeRoot(path string) (root, pickName string) {
	// Explorer's "Copy as path" wraps in quotes — strip them or every
	// stat below misses and the root never normalizes.
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		return "", ""
	}
	path = filepath.Clean(path)
	// A single theme folder? Its name is the pick; the root is two up
	// (…/root/themes/<name> → …/root).
	for _, ini := range themeINIFiles {
		if _, err := os.Stat(filepath.Join(path, ini)); err == nil {
			return filepath.Dir(filepath.Dir(path)), filepath.Base(path)
		}
	}
	// The themes folder itself → its parent is the root.
	if strings.EqualFold(filepath.Base(path), theme.ThemesDirName) {
		return filepath.Dir(path), ""
	}
	return path, ""
}

// volumeRow draws one "<name>  − NN% +" control and returns the value.
func (a *App) volumeRow(y int32, name string, value int) int {
	c := a.ctx
	const volumeStep = 10
	c.Label(pad, y+4, name+":", ColText)
	if c.Button(sdl.Rect{X: pad + 130, Y: y, W: 24, H: 24}, "-") && value >= volumeStep {
		value -= volumeStep
	}
	c.Label(pad+162, y+4, fmt.Sprintf("%3d%%", value), ColAccent)
	if c.Button(sdl.Rect{X: pad + 210, Y: y, W: 24, H: 24}, "+") && value <= 100-volumeStep {
		value += volumeStep
	}
	return value
}

// numberRow is volumeRow for arbitrary units/steps/bounds (spinbox-style:
// −/+ plus mousewheel over the row).
func (a *App) numberRow(y int32, label string, value, step, min, max int) int {
	c := a.ctx
	c.Label(pad, y+4, label+":", ColText)
	if c.Button(sdl.Rect{X: pad + 130, Y: y, W: 24, H: 24}, "-") && value-step >= min {
		value -= step
	}
	c.Label(pad+162, y+4, fmt.Sprintf("%5d", value), ColAccent)
	if c.Button(sdl.Rect{X: pad + 224, Y: y, W: 24, H: 24}, "+") && value+step <= max {
		value += step
	}
	if c.hovering(sdl.Rect{X: pad, Y: y, W: 252, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered spinbox owns the wheel — no page scroll
		next := value + int(c.wheelY)*step
		if next >= min && next <= max {
			value = next
		}
	}
	return value
}

// browseForFolder shells the native Windows folder picker on a goroutine;
// the chosen path lands on folderRes (empty = cancelled, dropped).
func browseForFolder() {
	if settings.browseBusy {
		return
	}
	settings.browseBusy = true
	go func() {
		const dialog = `Add-Type -AssemblyName System.Windows.Forms; ` +
			`$d = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
			`$d.Description = 'Pick the folder that CONTAINS themes\<name>'; ` +
			`if ($d.ShowDialog() -eq 'OK') { Write-Output $d.SelectedPath }`
		out, err := exec.Command("powershell", "-NoProfile", "-STA", "-Command", dialog).Output()
		path := strings.TrimSpace(string(out))
		if err != nil || path == "" {
			settings.folderRes <- ""
			return
		}
		settings.folderRes <- path
	}()
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
			return
		}
		settings.themeDir = path
		a.d.Prefs.SetTheme(settings.themeName, path)
		a.scanThemes()
		settings.statusLine = "Theme folder set: " + path
	default:
	}
}

// scanThemeDirs collects theme names across roots, "default" always first
// (the built-in fallback theme.Load uses even when no folder exists).
func scanThemeDirs(roots []string) []string {
	names := []string{theme.DefaultThemeName}
	seen := map[string]bool{theme.DefaultThemeName: true}
	for _, root := range roots {
		entries, err := os.ReadDir(filepath.Join(root, theme.ThemesDirName))
		if err != nil {
			continue // missing themes/ dir is normal
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			seen[e.Name()] = true
			names = append(names, e.Name())
		}
	}
	return names
}
