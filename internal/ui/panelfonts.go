package ui

import (
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Per-panel font and size, chosen by the USER rather than by a theme.
//
// A theme already sets a family and a point size for each courtroom element, and
// #39 made AsyncAO honour them. But a theme author picked those for their own
// design; only the person reading knows whether they can actually read it. This
// is the override — same table, same off-thread resolution, applied last.
//
// Size and family are INDEPENDENT on purpose. The common request is "the same
// look, but bigger", and forcing someone to also name a font to get that would
// make the feature useless to exactly the people who need it most.

// panelFontRow is one editable element on the Settings screen.
type panelFontRow struct {
	// id is the AO2 identifier. It is the PERSISTED key: theme.FontElements'
	// indices are load-bearing and appended to, so storing an index would silently
	// re-point every saved override the next time an element joins that list.
	id    string
	el    themeFontElem
	label string
	hint  string
}

// panelFontRows is what the Settings screen offers, in the order a user thinks
// about them rather than theme.FontElements' wire order.
//
// showname and message are FIRST because they are the chatbox — the text
// everyone reads most — and debug_log is last because almost nobody wants it.
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

// panelFontSizeW / panelFontFamilyW are the two field widths. The family field
// takes most of the room because a path can be long; the size field fits three
// digits and no more, since PanelFontMaxPx is two.
const (
	panelFontFamilyW = 250
	panelFontSizeW   = 60
	panelFontLabelW  = 130
)

// applyPanelFontPrefs re-runs the theme apply so the new font is resolved and
// read OFF-THREAD, exactly as the "use the theme's fonts" checkbox does. There
// is no lighter path on purpose: resolving a family walks the disk, and doing
// that on the render thread to save a few milliseconds is the rule this codebase
// exists around.
func (a *App) applyPanelFontPrefs() {
	a.applyThemeAsync()
}

// currentPanelSizePx is what the size field shows when the user has set nothing:
// the size the element is ACTUALLY being drawn at right now, so the field starts
// from what they can see rather than from a blank or a zero.
func (a *App) currentPanelSizePx(el themeFontElem) int {
	return UIFontSize * a.elemPct(el, DefaultScalePct) / DefaultScalePct
}

func (a *App) drawPanelFontSettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX

	// The theme's OWN missing fonts, before the override rows — because if this is
	// showing, it is almost certainly why the user came to this screen, and the
	// answer is to install the fonts rather than to override every panel by hand.
	// Persistent here, unlike the toast on apply, which is gone by the time anyone
	// goes looking.
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
			openInFileManager(dir) // windows→explorer / darwin→open / else→xdg-open (openpath.go)
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
		cur := a.d.Prefs.PanelFontFor(row.id)

		c.Label(pad, y+4, row.label, ColText)
		c.Tooltip(sdl.Rect{X: pad, Y: y, W: panelFontLabelW, H: fieldH}, row.hint)

		famRect := sdl.Rect{X: pad + panelFontLabelW, Y: y, W: panelFontFamilyW, H: fieldH}
		if next, _ := c.TextField("panelfont-"+row.id, famRect, cur.Family, a.panelFontPlaceholder(row.el)); next != cur.Family {
			cur.Family = next
			a.d.Prefs.SetPanelFont(row.id, cur)
			a.applyPanelFontPrefs()
		}

		// The size box shows the size actually in use when nothing is set, so the
		// starting point is what the user is looking at rather than a blank.
		//
		// WHILE THE FIELD HAS FOCUS ITS OWN TEXT IS AUTHORITATIVE, and that is the
		// whole fix rather than a nicety. This used to clamp on every keystroke and
		// feed the clamped number straight back in, so typing the "1" of "18" became
		// a 6 (the minimum) before the "8" could be typed, and no size below ten was
		// reachable at all. A field must never rewrite what someone is in the middle
		// of typing.
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

	// A family that resolved to nothing is the single most likely way this
	// confuses someone, so it is reported HERE, beside the box they typed it in,
	// and not only in the debug log.
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

// panelSizeText is what the size box should display this frame: the text the
// user is typing while the box has focus, and the SAVED value otherwise.
//
// The two differ on purpose, and only while typing. A half-typed number is not a
// size — "1" on the way to "18" is below the minimum, and "1" on the way to "120"
// is below it too — so it is held as text and never round-tripped through the
// clamp. The moment focus leaves, the saved value is the truth again, which is
// also what discards a box left holding something unusable.
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

// commitPanelSize takes one keystroke's worth of typing: it always remembers the
// raw text, and saves a size only when the text actually is one.
//
// Emptying the box CLEARS the override — that is how a panel is put back — so an
// empty string commits immediately. Anything below the minimum is treated as
// incomplete rather than wrong: it is almost always a number mid-typing, and
// rejecting it outright is the same trap as clamping it.
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

// panelFontPlaceholder shows what the panel is using NOW — the theme's family
// when it declared one, otherwise the client's own — so an empty box reads as
// "currently X" rather than as "nothing".
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
