package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// NewCtx loads the embedded Go font and prepares the kit.
func NewCtx(ren *sdl.Renderer) (*Ctx, error) {
	font, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		return nil, err
	}
	fontBig, err := loadEmbeddedFont(UIFontSizeBig)
	if err != nil {
		return nil, err
	}
	return &Ctx{
		Ren:     ren,
		font:    font,
		fontBig: fontBig,
		// At 100% the device faces share the logical ones; SetTextDevScale rebuilds them when the UI scale changes.
		fontDev:    font,
		fontBigDev: fontBig,
		textDevPct: DefaultScalePct,
		devScale:   1.0,
		textCache:  map[textKey]cachedText{},
		widthCache: map[string]int32{},
	}, nil
}

// SetWindow attaches the SDL window for attention requests (FlashWindow).
func (c *Ctx) SetWindow(win *sdl.Window) { c.win = win }

// MousePos returns this frame's logical cursor position, the coordinates every widget's hit-testing uses; exported so a
// caller outside this package can observe the shared cursor state.
func (c *Ctx) MousePos() (x, y int32) { return c.mouseX, c.mouseY }

// WindowFocused reports whether the window has input focus and isn't minimised, to gate desktop toasts to when you're
// tabbed away. A nil window (headless) reads as focused so tests never toast. Render thread only.
func (c *Ctx) WindowFocused() bool {
	if c.win == nil {
		return true
	}
	f := c.win.GetFlags()
	return f&sdl.WINDOW_INPUT_FOCUS != 0 && f&sdl.WINDOW_MINIMIZED == 0
}

// FlashWindow requests user attention until the window regains focus (AO2-Client's QApplication::alert).
func (c *Ctx) FlashWindow() {
	if c.win != nil {
		_ = c.win.Flash(sdl.FLASH_UNTIL_FOCUSED)
	}
}

// windowDisplayIndex is the index of the display the window sits on, falling back to the primary (0) when unknown.
// Every "which monitor are we on?" caller must use it: reading display N's bounds while recentering on display 0 is the
// multi-monitor bug CenteredWindowPos documents. Render thread only.
func (c *Ctx) windowDisplayIndex() int {
	if c.win == nil {
		return primaryDisplayIndex
	}
	di, err := c.win.GetDisplayIndex()
	if err != nil || di < 0 {
		return primaryDisplayIndex
	}
	return di
}

// WindowDisplayUsable returns the usable size (work area minus the taskbar) of the display the window is on, or (0,0)
// if unknown. The caller clamps a requested size into it (config.ClampWindowSize). Render thread only.
func (c *Ctx) WindowDisplayUsable() (int, int) {
	if c.win == nil {
		return 0, 0
	}
	r, err := sdl.GetDisplayUsableBounds(c.windowDisplayIndex())
	if err != nil {
		return 0, 0
	}
	return int(r.W), int(r.H)
}

// primaryDisplayIndex is SDL's first display, the fallback when the real one is unknown; it is also what the bare
// sdl.WINDOWPOS_CENTERED hard-codes.
const primaryDisplayIndex = 0

// sdlWindowPosDisplayMax is the largest display index SDL's window-position encoding carries.
// SDL_video.h defines SDL_WINDOWPOS_CENTERED_DISPLAY(X) as a bare OR that does not mask X, while
// SDL_SetWindowPosition reads the target from the low 16 bits only (SDL_video.c, `x & 0xFFFF`). An index that overflows
// corrupts the 0x2FFF0000 mask, the value stops reading as centered, and SDL places the window at a garbage absolute
// position off every monitor. CenteredWindowPos is exported and called from cmd/asyncao, so its input isn't limited to
// SDL's own GetDisplayIndex values.
const sdlWindowPosDisplayMax = 0xFFFF

// CenteredWindowPos builds the SDL_WINDOWPOS_CENTERED_DISPLAY(n) value for one display, for SetPosition / CreateWindow.
//
// The bare sdl.WINDOWPOS_CENTERED is not "centre on the current display": it is SDL_WINDOWPOS_CENTERED_DISPLAY(0),
// always display 0, so recentering with it teleports a window dragged to a second monitor back to the primary, with a
// size clamped against the other monitor. An index outside the encodable range degrades to the primary; SDL itself
// clamps a nonexistent in-range index to 0.
func CenteredWindowPos(display int) int32 {
	if display < 0 || display > sdlWindowPosDisplayMax {
		display = primaryDisplayIndex
	}
	return int32(sdl.WINDOWPOS_CENTERED_MASK | uint32(display))
}

// WindowMaximized reports whether the OS window is maximized (false headless). Render thread only.
func (c *Ctx) WindowMaximized() bool {
	if c.win == nil {
		return false
	}
	return c.win.GetFlags()&sdl.WINDOW_MAXIMIZED != 0
}

// UnmaximizeWindow drops a maximized window back to its restored geometry so a following SetSize/SetPosition sticks.
// SDL_SetWindowSize has no maximized guard (SDL_video.c), and WIN_SetWindowSize's SetWindowPos runs against an HWND
// still carrying WS_MAXIMIZE (SDL_windowswindow.c), so Windows keeps the maximized geometry and the resize does
// nothing.
//
// The maximized-only gate is deliberate: SDL_RestoreWindow gates on (MAXIMIZED | MINIMIZED), so calling it blind would
// also pop a minimized window onto the screen. Render thread only.
func (c *Ctx) UnmaximizeWindow() {
	if c.win == nil || c.win.GetFlags()&sdl.WINDOW_MAXIMIZED == 0 {
		return
	}
	c.win.Restore()
}

// WindowSize reports the current window size (0,0 headless). Render thread only.
func (c *Ctx) WindowSize() (int, int) {
	if c.win == nil {
		return 0, 0
	}
	w, h := c.win.GetSize()
	return int(w), int(h)
}

// ResizeWindow sets the windowed size and recenters it on the display it is on (not display 0; see CenteredWindowPos).
// No-op headless; the caller clamps the size first.
//
// Deliberately does not un-maximize: applyFullscreen's leave-fullscreen snap-back shares this call and F11 must not
// change a maximized window's state. App.applyWindowSize owns that. Render thread only.
func (c *Ctx) ResizeWindow(w, h int) {
	if c.win == nil {
		return
	}
	pos := CenteredWindowPos(c.windowDisplayIndex())
	c.win.SetSize(int32(w), int32(h))
	c.win.SetPosition(pos, pos)
}

// SetWindowFullscreen toggles borderless desktop fullscreen (no display-mode change). Render thread only.
func (c *Ctx) SetWindowFullscreen(on bool) {
	if c.win == nil {
		return
	}
	var flags uint32
	if on {
		flags = sdl.WINDOW_FULLSCREEN_DESKTOP
	}
	_ = c.win.SetFullscreen(flags)
}
