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
		// #77: at 100% the device faces SHARE the logical ones (no duplicate
		// raster). SetTextDevScale rebuilds them when the UI scale changes.
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

// MousePos returns this frame's logical cursor position — the same
// coordinates every widget's hit-testing consults. Exported so a caller
// outside this package can observe the shared cursor state (e.g. proving a
// second window's input never reached it) without duplicating HandleEvent's
// own coordinate math.
func (c *Ctx) MousePos() (x, y int32) { return c.mouseX, c.mouseY }

// WindowFocused reports whether the window currently has input focus and isn't
// minimised — used to gate desktop (OS) toasts to "you're tabbed away" only (#M4). A nil
// window (headless tests) reads as focused, so tests never toast. Render thread only.
func (c *Ctx) WindowFocused() bool {
	if c.win == nil {
		return true
	}
	f := c.win.GetFlags()
	return f&sdl.WINDOW_INPUT_FOCUS != 0 && f&sdl.WINDOW_MINIMIZED == 0
}

// FlashWindow requests user attention until the window regains focus —
// AO2-Client's QApplication::alert on modcalls/case announcements.
func (c *Ctx) FlashWindow() {
	if c.win != nil {
		_ = c.win.Flash(sdl.FLASH_UNTIL_FOCUSED)
	}
}

// windowDisplayIndex is the index of the display the window currently sits on,
// falling back to the primary display (0) when it can't be determined (headless,
// or SDL failing the query). Every "which monitor are we on?" caller must go
// through this one accessor: reading the usable bounds of display N while
// recentering on display 0 is exactly the multi-monitor bug CenteredWindowPos
// documents. Render thread only.
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

// WindowDisplayUsable returns the usable size (work area, minus the taskbar) of
// the display the window currently sits on; (0,0) if unknown (headless). The
// caller clamps a requested size into this (config.ClampWindowSize). Render
// thread only.
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

// primaryDisplayIndex is SDL's first display — the fallback whenever the real
// one is unknowable (no window yet, or a failed query). Named because it is also
// precisely what the bare sdl.WINDOWPOS_CENTERED constant hard-codes.
const primaryDisplayIndex = 0

// sdlWindowPosDisplayMax is the largest display index SDL's window-position
// encoding can carry. SDL_video.h defines SDL_WINDOWPOS_CENTERED_DISPLAY(X) as
// (SDL_WINDOWPOS_CENTERED_MASK|(X)) — a bare OR that does NOT mask X — while
// SDL_SetWindowPosition reads the target back out of the low 16 bits only
// (`int displayIndex = (x & 0xFFFF)`, SDL 2.32.10 src/video/SDL_video.c). So the
// index field is exactly 16 bits wide, and an index that overflows it does not
// merely wrap: its high bits corrupt the 0x2FFF0000 mask, the value stops reading
// as "centered" at all, and SDL places the window at a garbage ABSOLUTE position
// off every monitor. Hence the explicit upper bound as well as the lower one —
// CenteredWindowPos is exported and called from cmd/asyncao, so its input is not
// confined to the bounded values SDL's own GetDisplayIndex returns.
const sdlWindowPosDisplayMax = 0xFFFF

// CenteredWindowPos builds the SDL_WINDOWPOS_CENTERED_DISPLAY(n) magic value for
// one display, for SetPosition / CreateWindow.
//
// This exists because the bare sdl.WINDOWPOS_CENTERED is NOT "centre on whatever
// display you are on": SDL_video.h defines it as SDL_WINDOWPOS_CENTERED_DISPLAY(0),
// and SDL_SetWindowPosition derives the target from the low 16 bits, so it always
// means display 0. Recentering with it teleports a window the user dragged onto a
// second monitor back to the primary one — and worse, it does so with a size that
// was clamped against the OTHER monitor's usable bounds.
//
// Anything outside the encodable range degrades to the primary display. SDL
// itself clamps an in-range-but-nonexistent index back to 0, so only the encoding
// bound has to be enforced here — see sdlWindowPosDisplayMax for why a too-large
// index would otherwise produce a position word that is not "centered" at all.
func CenteredWindowPos(display int) int32 {
	if display < 0 || display > sdlWindowPosDisplayMax {
		display = primaryDisplayIndex
	}
	return int32(sdl.WINDOWPOS_CENTERED_MASK | uint32(display))
}

// WindowMaximized reports whether the OS window is currently maximized (false
// headless). Render thread only.
func (c *Ctx) WindowMaximized() bool {
	if c.win == nil {
		return false
	}
	return c.win.GetFlags()&sdl.WINDOW_MAXIMIZED != 0
}

// UnmaximizeWindow drops a maximized window back to its restored geometry so a
// following SetSize/SetPosition actually sticks.
//
// It has to be explicit because SDL_SetWindowSize has NO maximized guard: it
// writes window->windowed.w/h and calls the backend regardless
// (SDL 2.32.10 src/video/SDL_video.c), and WIN_SetWindowSize's SetWindowPos runs
// against an HWND that still carries WS_MAXIMIZE
// (src/video/windows/SDL_windowswindow.c), so Windows keeps the maximized
// geometry and the resize silently does nothing.
//
// The MAXIMIZED-only gate is deliberate and must not be replaced by SDL's own:
// SDL_RestoreWindow self-gates on (MAXIMIZED | MINIMIZED), so calling it blind
// would also pop a MINIMIZED window back onto the screen. Render thread only.
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

// ResizeWindow sets the windowed size and recenters it on the display it is
// actually on (NOT display 0 — see CenteredWindowPos), so a too-big or
// off-screen window snaps back into view. No-op headless; the caller clamps the
// size first.
//
// Deliberately does NOT un-maximize: applyFullscreen's leave-fullscreen snap-back
// shares this call, and F11 must not also change a maximized window's state.
// App.applyWindowSize owns that (it is the layer that persists the size too).
// Render thread only.
func (c *Ctx) ResizeWindow(w, h int) {
	if c.win == nil {
		return
	}
	pos := CenteredWindowPos(c.windowDisplayIndex())
	c.win.SetSize(int32(w), int32(h))
	c.win.SetPosition(pos, pos)
}

// SetWindowFullscreen toggles borderless desktop fullscreen (no display-mode
// change — safe on every monitor). Render thread only.
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
