package ui

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Per-panel font and size chosen by the user, applied after the theme's. Size and family are independent so a panel
// can be made bigger without naming a font.

// panelFontRow is one editable element on the Settings screen.
type panelFontRow struct {
	// id is the AO2 identifier and the persisted key; a theme.FontElements index could re-point saved overrides.
	id    string
	el    themeFontElem
	label string
	hint  string
}

// panelFontRows is the Settings order: chatbox text first, debug_log last.
var panelFontRows = [...]panelFontRow{
	{id: "message", el: elemMessage, label: "Chat message", hint: "what characters say, in the chatbox"},
	{id: "showname", el: elemShowname, label: "Character name", hint: "the name above the chatbox"},
	{id: "ic_chatlog", el: elemICChatlog, label: "IC log", hint: "the scrollback of in-character lines"},
	{id: "server_chatlog", el: elemServerChatlog, label: "OOC log", hint: "server and out-of-character messages"},
	{id: "music_list", el: elemMusicList, label: "Music list", hint: "track names in the jukebox"},
	{id: "music_name", el: elemMusicName, label: "Now playing", hint: "the current track's name"},
	{id: "area_list", el: elemAreaList, label: "Area list", hint: "room names and their details"},
	{id: "player_list", el: elemPlayerList, label: "Player list", hint: "the roster of who is in the room"},
	{id: "notes", el: elemNotes, label: "Notes", hint: "your own notes tab"},
	{id: "friends", el: elemFriends, label: "Friends", hint: "the friends and ignored lists"},
	{id: "debug_log", el: elemDebugLog, label: "Debug log", hint: "the failure log a theme can show"},
}

// The family field is widest because a path can be long.
const (
	panelFontFamilyW = 250
	panelFontSizeW   = 60
	panelFontLabelW  = 130
)

// applyPanelFontPrefs re-runs the theme apply because resolving a family walks the disk, off the render thread.
func (a *App) applyPanelFontPrefs() {
	a.applyThemeAsync()
}

// currentPanelSizePx is the size the element is drawn at now, shown in the size field when the user has set nothing.
func (a *App) currentPanelSizePx(el themeFontElem) int {
	return UIFontSize * a.elemPct(el, DefaultScalePct) / DefaultScalePct
}

func (a *App) drawPanelFontSettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX

	// The theme's missing fonts come first: they are likely why the user opened this screen.
	if a.themeFontWarn != "" {
		y = a.settingsSection(y, w, "This theme's fonts are missing")
		y = a.settingsDesc(pad, y, a.themeFontWarn, ColDanger)
		y += 6
	} else {
		y = a.settingsSection(y, w, "Your font folder")
		y = a.settingsDesc(pad, y, "AO themes name the fonts they want but usually don't include them — in AO2 those live in the base's fonts folder, so a theme downloaded on its own arrives with none of them. Drop font files (.ttf, .otf, .ttc) straight into the folder below and any theme can use them. The file name is what's matched, so a theme asking for \"Ace Name\" wants a file called \"Ace Name.ttf\".", ColTextDim)
		y += 6
	}
	if dir := config.UserFontsDir(); dir != "" {
		c.Label(pad, y+4, "Folder:", ColText)
		c.LabelClipped(pad+60, y+4, w-60-190, dir, ColTextDim)
		if c.Button(sdl.Rect{X: pad + w - 186, Y: y, W: 86, H: btnH}, "Open") {
			a.openInFileManager(dir)
		}
		if c.Button(sdl.Rect{X: pad + w - 96, Y: y, W: 92, H: btnH}, "Reload theme") {
			a.applyThemeAsync()
		}
		c.Tooltip(sdl.Rect{X: pad + w - 96, Y: y, W: 92, H: btnH},
			"Read the theme again — use this after adding font files, so you don't have to restart.")
		y += btnH + 10
	}

	y = a.settingsSection(y, w, "Font and size per panel")
	y = a.settingsDesc(pad, y, "Your own font and text size for each part of the courtroom, overriding whatever the theme picked. Leave a box empty to keep the theme's choice for that panel — the two are independent, so you can make one panel bigger without changing its font.", ColTextDim)
	y += 6
	c.Label(pad+panelFontLabelW, y, "Font family or file", ColTextDim)
	c.Label(pad+panelFontLabelW+panelFontFamilyW+8, y, "Size", ColTextDim)
	y += 18

	for i := range panelFontRows {
		row := &panelFontRows[i]
		cur := a.d.Prefs.Load().PanelFontFor(row.id)

		c.Label(pad, y+4, row.label, ColText)
		c.Tooltip(sdl.Rect{X: pad, Y: y, W: panelFontLabelW, H: fieldH}, row.hint)

		famRect := sdl.Rect{X: pad + panelFontLabelW, Y: y, W: panelFontFamilyW, H: fieldH}
		if next, _ := c.TextField("panelfont-"+row.id, famRect, cur.Family, a.panelFontPlaceholder(row.el)); next != cur.Family {
			cur.Family = next
			a.d.Prefs.SetPanelFont(row.id, cur)
			a.applyPanelFontPrefs()
		}

		// While focused the field's own text is authoritative: clamping each keystroke would turn the "1" of "18" into
		// the
		// minimum.
		sizeID := "panelsize-" + row.id
		sizeRect := sdl.Rect{X: pad + panelFontLabelW + panelFontFamilyW + 8, Y: y, W: panelFontSizeW, H: fieldH}
		shown := a.panelSizeText(sizeID, row.id, cur.SizePx)
		if next, _ := c.TextField(sizeID, sizeRect, shown, strconv.Itoa(a.currentPanelSizePx(row.el))); next != shown {
			a.commitPanelSize(sizeID, row.id, cur, next)
		}
		y += fieldH + 4
	}

	y += 4
	if c.Button(sdl.Rect{X: pad, Y: y, W: 190, H: btnH}, "Put every panel back") {
		a.d.Prefs.ClearPanelFonts()
		a.applyPanelFontPrefs()
	}
	c.Tooltip(sdl.Rect{X: pad, Y: y, W: 190, H: btnH},
		"Clear all eight overrides at once and go back to the theme's fonts. Worth knowing about before you try a font you can't read.")
	y += btnH + 6

	// Reported here as well as in the debug log, because an unresolved family is the likeliest confusion.
	if len(a.panelFontMissing) > 0 {
		y = a.settingsDesc(pad, y, "Not found, so the theme's font is still being used for: "+strings.Join(a.panelFontMissing, ", ")+
			". On Windows a family name like \"Arial\" or \"Comic Sans MS\" works; elsewhere give the full path to a .ttf or .otf file.", ColDanger)
		y += 6
	}
	y = a.settingsDesc(pad, y, "A family name is looked up in the theme's own fonts and then your system fonts; a full path to a .ttf, .otf or .ttc always works. Size is in pixels ("+
		strconv.Itoa(config.PanelFontMinPx)+"–"+strconv.Itoa(config.PanelFontMaxPx)+"), and the per-panel zoom (Ctrl+wheel over a panel) still applies on top of it.", ColTextDim)
	y += 10
	return y
}

// panelSizeText is the size box's text: what the user is typing while focused, otherwise the saved value.
// A half-typed number is held as text and never goes through the clamp.
func (a *App) panelSizeText(fieldID, rowID string, saved int) string {
	if a.ctx.focusID == fieldID {
		if s, editing := a.panelSizeEdit[rowID]; editing {
			return s
		}
	}
	if saved > 0 {
		return strconv.Itoa(saved)
	}
	return ""
}

// commitPanelSize remembers the raw text and saves a size only when the text is one.
// Empty clears the override; below the minimum is treated as mid-typing, not wrong.
func (a *App) commitPanelSize(fieldID, rowID string, cur config.PanelFont, text string) {
	if a.panelSizeEdit == nil {
		a.panelSizeEdit = make(map[string]string, len(panelFontRows))
	}
	a.panelSizeEdit[rowID] = text

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		if cur.SizePx == 0 {
			return // already cleared; don't re-apply the theme for nothing
		}
		cur.SizePx = 0
		a.d.Prefs.SetPanelFont(rowID, cur)
		a.applyPanelFontPrefs()
		return
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < config.PanelFontMinPx {
		return // not a size yet — keep the text, change nothing
	}
	if n > config.PanelFontMaxPx {
		n = config.PanelFontMaxPx
	}
	if n == cur.SizePx {
		return // re-applying the whole theme for an unchanged value is pure cost
	}
	cur.SizePx = n
	a.d.Prefs.SetPanelFont(rowID, cur)
	a.applyPanelFontPrefs()
}

// panelFontPlaceholder names the font in use now, so an empty box reads as "currently X".
func (a *App) panelFontPlaceholder(el themeFontElem) string {
	if name := a.themeFaceNameFor(el); name != "" {
		return "theme: " + name
	}
	return "the client's own font"
}

// panelFontElementIndex is the position of id in theme.FontElements, or -1.
func panelFontElementIndex(id string) int {
	for i, e := range theme.FontElements {
		if e == id {
			return i
		}
	}
	return -1
}
