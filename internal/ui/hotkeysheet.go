package ui

import (
	"sort"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Hotkey cheat-sheet: every shortcut including the user's own bindings (remaps, macros, character and showname keys).
// Opened with F1 or Extras > Hotkeys. Rows are cached once per open (hotkeyCheatEntries), not built per frame.

// hkEntry is one cheat-sheet row; custom marks a user-created or remapped binding, drawn brighter.
type hkEntry struct {
	key    string
	label  string
	custom bool
	header bool // a section header row (label only)
	// action is the hotkeyDefs id this row rebinds, or "" for rows bound elsewhere (fixed keys, layout gestures,
	// macro / character / showname sections). Non-empty makes the row clickable.
	action string
}

// consumeHotkeyCapture binds the next keypress to the armed action; Esc cancels. The key is consumed so it fires
// nothing else. Shared by the Settings rows and the F1 sheet so a capture armed in either completes; a change
// invalidates the sheet's cached rows.
func (a *App) consumeHotkeyCapture() {
	c := a.ctx
	if a.hkCapture == "" || c.keyPressed == 0 {
		return
	}
	if c.keyPressed != sdl.K_ESCAPE {
		a.d.Prefs.SetHotkey(a.hkCapture, strings.ToLower(sdl.GetKeyName(c.keyPressed)))
		a.hkCache = nil
	}
	a.hkCapture = ""
	c.keyPressed = 0
}

// hotkeyCheatEntries gathers every binding in display order; map-derived sections are sorted so the list is stable.
// Allocates; called once per open.
func (a *App) hotkeyCheatEntries() []hkEntry {
	out := make([]hkEntry, 0, len(hotkeyDefs)+24)

	out = append(out, hkEntry{label: "Shortcuts", header: true})
	for _, def := range hotkeyDefs {
		k := a.hotkeyFor(def.id)
		// action carries the id so the sheet can rebind the row; only this section gets one.
		out = append(out, hkEntry{
			key:    "Ctrl+" + strings.ToUpper(k),
			label:  def.label,
			custom: k != def.def,
			action: def.id,
		})
	}
	for _, fx := range [...]struct{ key, label string }{
		{"F1", "show / hide this list"},
		{"F3", "performance HUD"},
		{"F8", "Debug panel (diagnostics)"},
		{"Esc", "close menus / exit theater"},
		{"Ctrl+wheel", "resize text · zoom the stage"},
	} {
		out = append(out, hkEntry{key: fx.key, label: fx.label})
	}

	// Layout editor gestures.
	out = append(out, hkEntry{label: "Layout editor", header: true})
	for _, e := range [...]struct{ key, label string }{
		{"drag", "move a box · grab an edge to resize"},
		{"Alt+drag", "always move (for small buttons that would otherwise resize)"},
		{"Shift+drag", "pixel-precise (bypass the grid snap)"},
		{"right-click", "reset a box to default · redock a torn panel"},
		{"Ctrl+Z / Ctrl+Y", "undo / redo a layout change"},
	} {
		out = append(out, hkEntry{key: e.key, label: e.label})
	}

	// Your macros (custom-made; a macro may be saved without a key bound).
	if macros := a.d.Prefs.Load().Macros(); len(macros) > 0 {
		out = append(out, hkEntry{label: "Your macros", header: true})
		for _, m := range macros {
			// Alt+, not Ctrl+: handleMacroKeys reads c.keyPressed, which a Ctrl chord never reaches
			// (HandleEvent routes it to c.hotkey), so the real chord is Alt (bareBindKey).
			out = append(out, hkEntry{key: bareBindLabel(m.Key), label: m.Name, custom: true})
		}
	}

	// The user-bound key namespaces fire on ALT+<key> (bareBindKey, qol.go) since plain letters go to the IC input.
	out = appendKeyMapSection(out, "Character keys", a.charKeys, "Wear: ")
	out = appendKeyMapSection(out, "Showname keys", a.shownameKeys, "Showname: ")
	out = appendKeyMapSection(out, "IC phrases", a.icPhraseKeys, "IC: ")
	return out
}

// appendKeyMapSection adds a header + one sorted row per (key→name) binding,
// prefixing the name with prefix. No-op (no header either) when the map is empty.
func appendKeyMapSection(out []hkEntry, header string, binds map[string]string, prefix string) []hkEntry {
	if len(binds) == 0 {
		return out
	}
	keys := make([]string, 0, len(binds))
	for k := range binds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out = append(out, hkEntry{label: header, header: true})
	for _, k := range keys {
		out = append(out, hkEntry{key: bareBindLabel(k), label: prefix + binds[k], custom: true})
	}
	return out
}

// bareBindLabel / bareBindChordPrefix live in bindlabel.go, where every bind surface can use them.

// openHotkeyCheatSheet shows the sheet and rebuilds its rows; the F1 toggle rebuilds the same way.
func (a *App) openHotkeyCheatSheet() {
	a.showHotkeys = true
	a.hkCache = a.hotkeyCheatEntries()
}
