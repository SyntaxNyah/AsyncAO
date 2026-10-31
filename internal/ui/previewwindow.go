package ui

import (
	"fmt"
	"image"
	"time"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/platform/host"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// previewWindowTitle names the detached OS window.
const previewWindowTitle = "AsyncAO — Emote Preview"

// previewWinDefaultW/H is the client size on the first pop-out, before a rect is saved. Present letterboxes any aspect.
const (
	previewWinDefaultH = int32(config.DefaultPreviewHeightPx)
	previewWinDefaultW = previewWinDefaultH
)

// previewIsDetached reports whether the preview is in its own OS window. There is no separate bool to go stale when the
// window closes itself. Nil-safe, for tests that never wire Deps.Preview.
func (a *App) previewIsDetached() bool {
	return a.d.Preview.IsOpen()
}

// toggleDetachPreview pops the preview out or re-attaches it. Does nothing with nothing previewed: only
// feedDetachedPreviewFrame, which requires previewBase, can fill the window.
func (a *App) toggleDetachPreview() {
	if a.previewBase == "" {
		return
	}
	if a.previewIsDetached() {
		a.d.Preview.Close()
		return
	}
	a.openDetachedPreview()
}

// openDetachedPreview creates the window at the remembered rect clamped to a connected display, or a default on first
// use. On failure it stays attached, so an unsupported platform can't leave the feature half-open.
func (a *App) openDetachedPreview() {
	var err error
	if x, y, w, h, ok := a.d.Prefs.Load().PreviewWindowRect(); ok && w > 0 && h > 0 {
		err = a.d.Preview.OpenAtClamped(previewWindowTitle, int32(x), int32(y), int32(w), int32(h))
	} else {
		err = a.d.Preview.Open(previewWindowTitle, previewWinDefaultW, previewWinDefaultH)
	}
	if err != nil {
		return
	}
	// Ties z-order to the main window (Windows only, a no-op elsewhere). Best-effort: a failed link just leaves
	// the preview's independent z-order.
	host.LinkWindowOwner(a.d.Preview.Window(), a.ctx.win)
	// A new OS window has no texture, so forget what a prior window was fed.
	a.previewWinFedBase = ""
	a.previewWinFedPage = nil
	a.previewWinFedFrame = 0
	// Feed the first frame now to avoid a blank frame, and set previewWinWasOpen now so a rect is
	// captured even if the window closes immediately.
	a.syncPreviewWindow()
}

// syncPreviewWindow is per-frame maintenance, run before handlePreviewInput's early returns: the window must be fed and
// its close noticed even when the in-app box never draws. Nothing calls back on the window's own X, so polling IsOpen
// against last frame is the only way to notice. The rect persists once on that edge, never per move (hard rules 2 and
// 3).
func (a *App) syncPreviewWindow() {
	if a.previewIsDetached() {
		a.previewWinWasOpen = true
		a.feedDetachedPreviewFrame()
		return
	}
	if a.previewWinWasOpen {
		if x, y, w, h, ok := a.d.Preview.LastRect(); ok {
			a.d.Prefs.SetPreviewWindowRect(int(x), int(y), int(w), int(h))
		}
	}
	a.previewWinWasOpen = false
}

// feedDetachedPreviewFrame keeps the window's texture on the current previewBase and frame index. GPU work happens only
// on a new pick, a new source page (evicted and re-decoded) or an advancing index; pageFrameLoop is cheap to call each
// pass. A one-frame page uses SetFrame once per pick; others use ShowAnimFrame, which caches each frame on first visit,
// so a looping animation reads back at most once per distinct frame. A still-loading pick is retried next call, and the
// window keeps its previous content.
func (a *App) feedDetachedPreviewFrame() {
	if a.previewBase == "" {
		return
	}
	page, ok := a.d.Store.Get(a.previewBase)
	if !ok || len(page.Frames) == 0 {
		return // still loading — retried next call, previewWinFedBase left alone
	}
	idx := pageFrameLoop(page, a.now().Sub(a.previewAt))
	animated := page.Animated && len(page.Frames) > 1 // pageFrameLoop's own "loop at all" guard
	if !previewFeedNeeded(a.previewWinFedBase, a.previewBase, a.previewWinFedPage, page,
		animated, a.previewWinFedFrame, idx) {
		return // same pick, same source page, nothing new to show
	}
	// Marked even if the readback below fails, which must not retry every call.
	a.previewWinFedBase = a.previewBase
	a.previewWinFedPage = page
	a.previewWinFedFrame = idx
	if !animated {
		img, err := readTexturePixels(a.ctx.Ren, page.Frames[idx], page.W, page.H)
		if err != nil {
			return // e.g. a backend without render-target support — window keeps its last good frame
		}
		_ = a.d.Preview.SetFrame(img)
		return
	}
	_ = a.d.Preview.ShowAnimFrame(a.previewBase, page, idx, func() (*image.RGBA, error) {
		return readTexturePixels(a.ctx.Ren, page.Frames[idx], page.W, page.H)
	})
}

// previewFeedNeeded decides whether there is anything new to show; pure, so a test can drive it.
// When a pick overflows the animation cache, an uncached ordinal calls fill on every visit, so without the
// fedIdx == idx test a 60 Hz loop would run a GPU readback per rendered frame.
func previewFeedNeeded(fedBase, base string, fedPage, page *render.TexturePage, animated bool, fedIdx, idx int) bool {
	if fedBase != base || fedPage != page {
		return true // different pick, or the same base re-decoded into a new page
	}
	return animated && idx != fedIdx
}

// AdvanceDetachedPreview runs syncPreviewWindow on the main loop's non-Frame passes (minimized, SkipFrame), or the
// popped-out animation freezes in exactly the case it exists for: watching while alt-tabbed.
func (a *App) AdvanceDetachedPreview() {
	a.syncPreviewWindow()
}

// previewAnimWakeCap bounds a non-drawing pass's sleep while the detached window animates, or the long idle nap freezes
// it. A coarse wake, not NextWakeDelay's render-forcing contract, which would redraw the main window for a small one.
// ~10 Hz reads as smooth for typical AO idle sprites.
const previewAnimWakeCap = 100 * time.Millisecond

// PreviewAnimWakeCap returns the longest the main loop should sleep on a non-Frame pass, or 0 for no constraint.
// It only bounds a sleep and never changes the decision not to draw. Render thread only.
func (a *App) PreviewAnimWakeCap() time.Duration {
	if !a.previewIsDetached() || a.d.Store == nil {
		return 0
	}
	page, ok := a.d.Store.Get(a.previewBase)
	if !ok || !page.Animated || len(page.Frames) < 2 {
		return 0
	}
	return previewAnimWakeCap
}

// readTexturePixels reads an uploaded main-renderer texture to CPU memory for another renderer's SetFrame. A second
// decode won't do: Manager's T1 check makes a repeat Prefetch a silent no-op. Render thread only; every step is an SDL
// call, and the blit and readback are the only way to get pixels out of an SDL texture.
func readTexturePixels(ren *sdl.Renderer, tex *sdl.Texture, w, h int32) (*image.RGBA, error) {
	if ren == nil || tex == nil || w <= 0 || h <= 0 {
		return nil, fmt.Errorf("ui: readTexturePixels: nothing to read")
	}
	target, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_TARGET, w, h)
	if err != nil {
		return nil, fmt.Errorf("ui: readTexturePixels: scratch target: %w", err)
	}
	defer target.Destroy()
	prevTarget := ren.GetRenderTarget()
	if err := ren.SetRenderTarget(target); err != nil {
		return nil, fmt.Errorf("ui: readTexturePixels: set target: %w", err)
	}
	defer ren.SetRenderTarget(prevTarget)
	_ = ren.SetDrawColor(0, 0, 0, 0)
	_ = ren.Clear()
	if err := ren.Copy(tex, nil, nil); err != nil {
		return nil, fmt.Errorf("ui: readTexturePixels: blit: %w", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	if err := ren.ReadPixels(nil, uint32(sdl.PIXELFORMAT_ABGR8888), unsafe.Pointer(&img.Pix[0]), img.Stride); err != nil {
		return nil, fmt.Errorf("ui: readTexturePixels: read: %w", err)
	}
	return img, nil
}
