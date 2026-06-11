package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// settingsState lives on App lazily (kept here for file cohesion).
type settingsState struct {
	mountInput string
	showname   string
	loaded     bool
	statusLine string

	// theme picker state: list scanning runs on a goroutine (directory
	// I/O stays off the render thread — §17.2) and lands on themeRes.
	themeName string
	themeDir  string
	themeList []string
	themeRes  chan []string
	themeBusy bool

	// folder picking: native dialog output / resolved drag-drops land
	// here from goroutines (never block or stat on the render thread).
	folderRes  chan string
	browseBusy bool
}

var settings = settingsState{
	themeRes:  make(chan []string, 1),
	folderRes: make(chan string, 1),
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
		a.d.Prefs.SetShowname(settings.showname)
		a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
		_ = a.d.Prefs.SaveNow() // Settings-Apply synchronous flush
		a.screen = a.prevScreen
		return
	}

	if !settings.loaded {
		settings.showname = a.d.Prefs.SavedShowname()
		settings.themeName, settings.themeDir = a.d.Prefs.Theme()
		if settings.themeName == "" {
			settings.themeName = theme.DefaultThemeName
		}
		settings.loaded = true
		a.scanThemes()
	}
	a.pollThemeScan()

	y := pad + 44

	// Showname.
	c.Label(pad, y+4, "Showname (saved):", ColText)
	settings.showname, _ = c.TextField("showname", sdl.Rect{X: pad + 150, Y: y, W: 220, H: fieldH}, settings.showname, "Your showname")
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
	}
	if runtime.GOOS == "windows" {
		if c.Button(sdl.Rect{X: pad + 600, Y: y, W: 90, H: btnH}, "Browse...") {
			browseForFolder()
		}
	}
	// Drag-and-drop: SDL DROPFILE anywhere on this screen points the
	// theme folder (a dropped file resolves to its directory, off-thread).
	if c.dropped != "" {
		resolveDroppedFolder(c.dropped)
	}
	a.pollFolderPick()
	y += 36

	// Per-type format toggles.
	c.Label(pad, y, "Image formats probed per asset type (defaults: char_icon=PNG only, everything else=WebP only):", ColTextDim)
	y += 22
	for _, typeName := range imageTypeNames {
		y = a.drawTypeFormatRow(typeName, y)
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
	y += 36
	if settings.statusLine != "" {
		c.Label(pad, y, settings.statusLine, ColAccent)
	}
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

// --- theme picker -----------------------------------------------------------

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
		roots := make([]string, 0, 2)
		if customRoot != "" {
			roots = append(roots, customRoot)
		}
		if exe, err := os.Executable(); err == nil {
			roots = append(roots, filepath.Dir(exe))
		}
		settings.themeRes <- scanThemeDirs(roots)
	}()
}

func (a *App) pollThemeScan() {
	select {
	case names := <-settings.themeRes:
		settings.themeBusy = false
		settings.themeList = names
	default:
	}
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
