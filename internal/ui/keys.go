package ui

import "github.com/veandco/go-sdl2/sdl"

// keys is the key registry: the one place that answers "which bound key is the
// user pressing right now" for every plain-key namespace (characters, IC phrases,
// shownames, style presets, jukebox, macros, voice PTT, wardrobe binds).
//
// It holds no state of its own. The key state is the frame's Ctx and the capture
// arbitration stays on App, because four of the eight capture flags are GLOBAL
// (jukebox, hold-to-clear, voice PTT, macro) while four are per-session. Both
// arrive as explicit parameters — never a back-pointer to App — which is also why
// the component needs no wiring and is nil-safe in every rig.
type keys struct{}

// bareBindKey is THE gate for every user-assignable "plain key" bind namespace.
// It returns the pressed key only when the chord is Alt+<key> and no capture is
// armed; 0 means "this press is not a bind".
//
// ALT specifically: Ctrl is fully occupied (hotkeyDefs' collision sweep documents
// that every Ctrl+letter, Ctrl+digit and Ctrl+symbol is already claimed), Shift is
// text, and Alt was unused as a key chord — the layout editor's Alt+DRAG is a
// mouse gesture in a different namespace.
//
// !ctrlHeld is load-bearing, not tidiness: AltGr on Windows reports as Ctrl+Alt
// and produces real text, so a bare altHeld test would swallow every accented
// character a European layout types into the IC box.
//
// The BIND-CAPTURE paths (bindingFor, jukeBindFor, …) deliberately keep reading
// the plain key: you still press "m" to bind m — it simply fires as Alt+M.
func (k *keys) bareBindKey(c *Ctx, capturing bool) sdl.Keycode {
	if c == nil || capturing {
		return 0
	}
	if c.keyPressed == 0 || !c.altHeld || c.ctrlHeld {
		return 0
	}
	return c.keyPressed
}
