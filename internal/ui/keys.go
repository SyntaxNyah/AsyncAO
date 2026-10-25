package ui

import "github.com/veandco/go-sdl2/sdl"

// keys is the key registry: the one place that answers "which bound key is the
// user pressing right now" for every plain-key namespace (characters, IC phrases,
// shownames, style presets, jukebox, macros, voice PTT, wardrobe binds).
type keys struct{}

// bareBindKey is THE gate for every user-assignable "plain key" bind namespace.
func (k *keys) bareBindKey(c *Ctx, capturing bool) sdl.Keycode {
	if c == nil || capturing {
		return 0
	}
	if c.keyPressed == 0 || !c.altHeld || c.ctrlHeld {
		return 0
	}
	return c.keyPressed
}
