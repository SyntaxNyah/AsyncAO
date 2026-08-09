package ui

import (
	"sort"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// Hotkey cheat-sheet (#79): a one-glance window of every keyboard shortcut,
// including the user's OWN custom bindings — remapped actions, macros, character
// keys, and showname keys — so "all your hotkeys in one place" really means all
// of them. Opened with F1 or the Extras menu's "Hotkeys" entry. Built into a
// cached slice once per open (hotkeyCheatEntries), never per frame, and drawn
// only while open, so it costs nothing on the live courtroom.

// hkEntry is one cheat-sheet row. custom marks a binding the user created or
// remapped (drawn brighter), so they can tell their own bindings from defaults.
type hkEntry struct {
	key    string
	label  string
	custom bool
	header bool // a section header row (label only)
	// action is the hotkeyDefs id this row rebinds, or "" for a row that is not
	// rebindable — the fixed function keys, the layout-editor gestures, and the
	// macro / character / showname sections, which are bound elsewhere and own
	// their own editors. Non-empty is what makes a row clickable in the sheet.
	action string
}

// consumeHotkeyCapture binds the next keypress to whichever action armed the
// capture; Esc cancels. The key is consumed so it cannot also fire something
// else on its way past.
//
// Shared by BOTH rebinding surfaces — the Settings rows and the F1 sheet — and
// that sharing is the point rather than tidiness: it used to live inside the
// Settings draw, so a capture armed anywhere else would simply never complete,
// leaving a row stuck on "press a key…" until the user opened Settings.
//
// Invalidates the sheet's cached rows on a real change, since one of them now
// shows a key that just moved.
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

// hotkeyCheatEntries gathers every binding into display order: built-in actions
// (flagging remaps), the fixed function keys, then the user's macros, character
// keybinds, and showname keybinds. Map-derived sections are sorted by key so the
// list is stable frame to frame. Allocates — called once per open, cached.
func (a *App) hotkeyCheatEntries() []hkEntry {
	out := make([]hkEntry, 0, len(hotkeyDefs)+24)

	out = append(out, hkEntry{label: "Shortcuts", header: true})
	for _, def := range hotkeyDefs {
		k := a.hotkeyFor(def.id)
		// action carries the id so the sheet itself can rebind the row. Only this
		// section gets one: everything below is either a fixed key or lives in its
		// own editor.
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

	// Layout editor gestures (opened from the bottom-right toolbox's Edit chip,
	// Ctrl+Space → Edit Layout, or the Edit-layout hotkey — Ctrl+F2 by default).
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
	if macros := a.d.Prefs.Macros(); len(macros) > 0 {
		out = append(out, hkEntry{label: "Your macros", header: true})
		for _, m := range macros {
			// Alt+, not Ctrl+: this row has always LIED. handleMacroKeys reads
			// c.keyPressed, which a Ctrl chord never reaches (HandleEvent routes those
			// to c.hotkey and returns), so the bind was a bare key printed as a Ctrl
			// one. It is a real Alt chord now (bareBindKey) and the row says the chord
			// that actually fires it.
			out = append(out, hkEntry{key: bareBindLabel(m.Key), label: m.Name, custom: true})
		}
	}

	// The three user-bound key namespaces. They fire on ALT+<key> now, not on the
	// bare letter (bareBindKey, qol.go — the Discord focus model gave every plain
	// letter to the IC input), so the rows say so: a sheet that still printed a
	// bare "M" would be advertising a key that no longer does anything.
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

// bareBindLabel / bareBindChordPrefix moved to bindlabel.go: the cheat sheet was
// their only caller for a whole wave, which is precisely the drift they exist to
// prevent, so the sanctioned spelling now lives in a file every bind surface can
// see rather than inside this overlay's own source.

// openHotkeyCheatSheet shows the cheat sheet and (re)builds its rows so a freshly
// opened sheet reflects the current bindings. Used by the Extras menu entry; the
// F1 toggle rebuilds the same way on its on-transition.
func (a *App) openHotkeyCheatSheet() {
	a.showHotkeys = true
	a.hkCache = a.hotkeyCheatEntries()
}
