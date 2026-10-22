package ui

// paletteState owns palette state.
type paletteState struct {
	// #39 command palette (Ctrl+Space): open state, live query, keyboard selection.
	paletteOpen  bool
	paletteQuery string
	paletteSel   int
}
