package ui

import (
	"sort"
	"strings"
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
		out = append(out, hkEntry{key: "Ctrl+" + strings.ToUpper(k), label: def.label, custom: k != def.def})
	}
	for _, fx := range [...]struct{ key, label string }{
		{"F1", "show / hide this list"},
		{"F3", "performance HUD"},
		{"Esc", "close menus / exit theater"},
		{"Ctrl+wheel", "resize text · zoom the stage"},
	} {
		out = append(out, hkEntry{key: fx.key, label: fx.label})
	}

	// Your macros (custom-made; a macro may be saved without a key bound).
	if macros := a.d.Prefs.Macros(); len(macros) > 0 {
		out = append(out, hkEntry{label: "Your macros", header: true})
		for _, m := range macros {
			key := "(no key)"
			if m.Key != "" {
				key = "Ctrl+" + strings.ToUpper(m.Key)
			}
			out = append(out, hkEntry{key: key, label: m.Name, custom: true})
		}
	}

	// Character keybinds (bare key wears a character; this server).
	out = appendKeyMapSection(out, "Character keys", a.charKeys, "Wear: ")
	// Showname keybinds (bare/Ctrl per the binder; key → showname).
	out = appendKeyMapSection(out, "Showname keys", a.shownameKeys, "Showname: ")
	// IC quick-phrases (bare key sends a canned IC line).
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
		out = append(out, hkEntry{key: strings.ToUpper(k), label: prefix + binds[k], custom: true})
	}
	return out
}

// openHotkeyCheatSheet shows the cheat sheet and (re)builds its rows so a freshly
// opened sheet reflects the current bindings. Used by the Extras menu entry; the
// F1 toggle rebuilds the same way on its on-transition.
func (a *App) openHotkeyCheatSheet() {
	a.showHotkeys = true
	a.hkCache = a.hotkeyCheatEntries()
}
