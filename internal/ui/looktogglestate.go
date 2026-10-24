package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/veandco/go-sdl2/sdl"
)

// lookToggleState owns lookUI state.
type lookToggleState struct {
	// Text FX picker (#M5): the FX button opens a floating effect list instead of cycling 13 effects.
	showFxPicker bool
	// showICColorWheel floats the free-hex colour wheel anchored to
	// the IC colour swatch; icSwatchRect remembers where that swatch drew this
	// frame (either layout), icColorHexBuf is the wheel's hex-field edit buffer.
	showICColorWheel bool
	icSwatchRect     sdl.Rect
	icColorHexBuf    string
	fxBtnRect        sdl.Rect // last-drawn FX button rect (the picker anchors above it + the fence finds it)
	// Per-part layout colours: the parsed draw cache (refreshPartColors),
	// the Settings wheel's selected part, and its hex-field edit buffer.
	partColors   [config.LayoutPartColorCount]sdl.Color
	partColorOn  [config.LayoutPartColorCount]bool
	partEditSlot int
	partHexBuf   string
	// showPowerUser reveals the advanced/power-user Settings options (TLS, Asset Origin, asset
	// casing) — hidden by default so they're not changed by accident. Session-only.
	showPowerUser bool
	// powerNukeArm is the two-click confirm on "Reset all power-user options": the first
	// click arms (label flips), the second fires ResetPowerUser. Cleared on the reveal
	// toggle and on fire, so a stale arm can't linger across visits.
	powerNukeArm bool
}
