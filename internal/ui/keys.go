package ui

import "github.com/veandco/go-sdl2/sdl"

// keys is the key registry: the one place that answers "which bound key is the
// user pressing right now" for every plain-key namespace (characters, IC phrases,
// shownames, style presets, jukebox, macros, voice PTT, wardrobe binds).
type keys struct{}

// bareBindKey is THE gate for every user-assignable "plain key" bind namespace.
// It returns the pressed key only when the chord is Alt+<key> and no capture is
// armed; 0 means "this press is not a bind".
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
