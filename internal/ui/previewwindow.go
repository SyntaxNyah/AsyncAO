package ui

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// previewWindowTitle names the detached OS window in the taskbar/alt-tab and
// the titlebar itself.
const previewWindowTitle = "AsyncAO — Emote Preview"

// previewWinDefaultW/H is the detached window's INITIAL client size on the
// very first pop-out (no saved rect yet — config.AssetPreferences.
// PreviewWindowRect reports ok=false). A square matching the in-app box's
// own shipped default HEIGHT: big enough to read most sprites without
// re-deriving the box's own width-from-aspect math a second time, and
// Present already letterboxes whatever aspect actually arrives. The user's
// own OS-native window-border resize takes over from there, and THAT size
// is what gets remembered (SetPreviewWindowRect) — this constant is only
// ever consulted once per install.
const (
	previewWinDefaultH = int32(config.DefaultPreviewHeightPx)
	previewWinDefaultW = previewWinDefaultH
)

// previewIsDetached reports whether the sprite preview currently lives in
// its own OS window rather than the in-app box. This is the SOLE source of
// truth the pop-out button, drawSpritePreview and previewIsPinned all read —
// there is no separate "detached" bool anywhere on App to go stale if the
// window closes itself (its own titlebar X, per measured fact 1):
// render.PreviewWindow.IsOpen() is already live, and already reconciled the
// instant its own Close() runs. a.d.Preview is nil-safe (IsOpen checks the
// receiver itself), so this is safe even in tests that never wire Deps.Preview.
func (a *App) previewIsDetached() bool {
	return a.d.Preview.IsOpen()
}

// toggleDetachPreview pops the sprite-preview box out into its own OS window,
// or closes that window and re-attaches to the in-app box — requirement 1
// (v1.93.0 preview-window-wire). A no-op with nothing currently previewed:
// detaching an empty box would open a window nothing could ever fill (only
// feedDetachedPreviewFrame, gated on previewBase, ever calls SetFrame).
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

// openDetachedPreview creates the OS window at the user's last remembered
// position/size (requirement 4), clamped to a currently connected display
// (requirement 5), or at a sane default on the very first-ever pop-out.
// Silently declines (stays attached) if window creation fails — a headless
// or otherwise unsupported platform must not leave the feature half-open.
func (a *App) openDetachedPreview() {
	var err error
	if x, y, w, h, ok := a.d.Prefs.PreviewWindowRect(); ok && w > 0 && h > 0 {
		err = a.d.Preview.OpenAtClamped(previewWindowTitle, int32(x), int32(y), int32(w), int32(h))
	} else {
		err = a.d.Preview.Open(previewWindowTitle, previewWinDefaultW, previewWinDefaultH)
	}
	if err != nil {
		return
	}
	// Force a fresh feed even if this exact previewBase was already fed to a
	// PRIOR (now-closed) window — a new OS window has no texture of its own
	// yet, and previewWinFedBase otherwise still names this base from before.
	a.previewWinFedBase = ""
	// Feeds the first frame immediately (no visible blank window for the one
	// frame it would otherwise take handlePreviewInput's own per-frame call
	// to notice), and — just as importantly — arms previewWinWasOpen right
	// now rather than waiting for the NEXT frame's syncPreviewWindow call, so
	// a detach immediately followed by a close (however unlikely) still gets
	// its rect captured on the way out.
	a.syncPreviewWindow()
}

// syncPreviewWindow is the detached preview's own per-frame maintenance,
// called unconditionally once per Frame (handlePreviewInput's own top,
// before either of its early returns — the detached window must still be fed
// and its close reconciled on passes where the in-app box itself never
// draws, e.g. previewBase=="" after noteScreenTransition force-cleared it
// while the window stayed open).
//
// Reconciling a self-close: whether the user re-attached via the pop-out
// toggle or closed the OS window with its own titlebar X (measured fact 1 —
// nothing calls back into this package when that happens), the ONLY way to
// notice is polling IsOpen() and comparing against last frame's answer. The
// falling edge is exactly when the window's final rect (PreviewWindow.
// LastRect, captured by Close() before it destroys anything) gets persisted
// — once, not every frame, and never a per-move write (CLAUDE.md hard rules
// 2/3).
//
// "Closed = free": a.d.Preview.IsOpen() is one nil+nil check when nothing
// has ever been detached, matching every other preview-window touch point's
// own contract (TestSyncPreviewWindowClosedIsZeroAlloc).
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

// feedDetachedPreviewFrame keeps the detached window's texture in step with
// the currently selected previewBase. It feeds ONCE per pick
// (previewWinFedBase tracks "already fed this base"), not every render
// frame: SetFrame rebuilds a whole GPU texture, and Present already re-fits
// whatever is currently uploaded against the window's live size on its own
// every frame, so feeding more often buys nothing. A still-loading pick
// (nothing resident yet) is retried next frame — cheap (a texture-store
// lookup, no allocation) since a real decode settles within a handful of
// frames, and the window simply keeps showing its previous content (or a
// black window on the very first-ever pick) until then.
func (a *App) feedDetachedPreviewFrame() {
	if a.previewBase == "" || a.previewWinFedBase == a.previewBase {
		return
	}
	page, ok := a.d.Store.Get(a.previewBase)
	if !ok || len(page.Frames) == 0 {
		return // still loading — retried next frame, previewWinFedBase left alone
	}
	// Marked attempted regardless of what follows: a decode that resolved to
	// a page with no frames is a terminal answer for THIS base, not a
	// "still loading" one, and must not retry forever.
	a.previewWinFedBase = a.previewBase
	frame := page.Frames[pageFrameLoop(page, a.now().Sub(a.previewAt))]
	img, err := readTexturePixels(a.ctx.Ren, frame, page.W, page.H)
	if err != nil {
		return // e.g. a backend without render-target support — window keeps its last good frame
	}
	_ = a.d.Preview.SetFrame(img)
}

// readTexturePixels reads an ALREADY-UPLOADED texture on the MAIN renderer
// back into CPU memory, for handing to a DIFFERENT renderer's SetFrame.
// render.PreviewWindow's own doc explains why this — not re-entering
// internal/assets for a second decode — is the right way to feed it: the
// asset is, by construction, already decoded and uploaded by the time a
// preview is on screen to detach, and internal/assets.Manager's own
// T1-residency short-circuit would make a second Prefetch for it a silent
// no-op with no decode event to catch.
//
// Render-thread only (every call is an SDL call: a scratch render-target
// texture, a blit, ReadPixels, then the render target is restored). Called
// at most once per emote pick while the preview window is open — never per
// render frame — so the extra blit+readback this needs (there is no cheaper
// way to get pixels out of an SDL texture) is a bounded, human-timescale
// cost, not a hot-path one.
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
