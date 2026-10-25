package ui

import (
	"fmt"
	"image"
	"time"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// previewWindowTitle names the detached OS window in the taskbar/alt-tab and
// the titlebar itself.
const previewWindowTitle = "AsyncAO — Emote Preview"

// previewWinDefaultW/H is the detached window's INITIAL client size on the
// very first pop-out (no saved rect yet — config.AssetPreferences.
const (
	previewWinDefaultH = int32(config.DefaultPreviewHeightPx)
	previewWinDefaultW = previewWinDefaultH
)

// previewIsDetached reports whether the sprite preview currently lives in
// its own OS window rather than the in-app box. This is the SOLE source of
// truth the pop-out button, drawSpritePreview and previewIsPinned all read —
// there is no separate "detached" bool anywhere on App to go stale if the
// window closes itself (its own titlebar X, per measured fact 1):
func (a *App) previewIsDetached() bool {
	return a.d.Preview.IsOpen()
}

// toggleDetachPreview pops the sprite-preview box out into its own OS window,
// or closes that window and re-attaches to the in-app box — requirement 1
// (v1.93.0 preview-window-wire). A no-op with nothing currently previewed:
func (a *App) toggleDetachPreview() {
	if a.budget.previewBase == "" {
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
	if x, y, w, h, ok := a.d.Prefs.Load().PreviewWindowRect(); ok && w > 0 && h > 0 {
		err = a.d.Preview.OpenAtClamped(previewWindowTitle, int32(x), int32(y), int32(w), int32(h))
	} else {
		err = a.d.Preview.Open(previewWindowTitle, previewWinDefaultW, previewWinDefaultH)
	}
	if err != nil {
		return
	}
	// Ties the new OS window's z-order to the main window (Windows-only
	// native owner relationship; a documented no-op elsewhere) — "shared
	// priority with main async window, moving on top of other windows when
	// async is selected". Best-effort: LinkOwner's own bool return is
	// intentionally unchecked here, matching AllowSetForeground's existing
	// best-effort call convention elsewhere in this codebase — a failed
	// link just leaves the preview with its previous, already-workable,
	// independent z-order rather than blocking the pop-out over it. See
	// render.PreviewWindow.LinkOwner's doc for the measured reasoning
	// (why a focus-hook approach was ruled out first).
	a.d.Preview.LinkOwner(a.ctx.win)
	// Force a fresh feed even if this exact previewBase was already fed to a
	// PRIOR (now-closed) window — a new OS window has no texture of its own
	// yet, and previewWinFedBase/previewWinFedPage/previewWinFedFrame
	// otherwise still name that prior feed.
	a.previewWinFedBase = ""
	a.previewWinFedPage = nil
	a.previewWinFedFrame = 0
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
// the currently selected previewBase and, for an animated pick, its current
// frame index. Called every pass (see AdvanceDetachedPreview) regardless of
// whether app.Frame() itself runs this pass — a looping animation must keep
// advancing even while the main loop's SkipFrame/minimized branches suppress
// every full redraw of the main window, which is the whole reason a preview
// gets popped out in the first place.
func (a *App) feedDetachedPreviewFrame() {
	if a.budget.previewBase == "" {
		return
	}
	page, ok := a.d.Store.Get(a.budget.previewBase)
	if !ok || len(page.Frames) == 0 {
		return // still loading — retried next call, previewWinFedBase left alone
	}
	idx := pageFrameLoop(page, a.now().Sub(a.budget.previewAt))
	animated := page.Animated && len(page.Frames) > 1 // pageFrameLoop's own "loop at all" guard
	if !previewFeedNeeded(a.previewWinFedBase, a.budget.previewBase, a.previewWinFedPage, page,
		animated, a.previewWinFedFrame, idx) {
		return // same pick, same source page, nothing new to show
	}
	// Marked attempted regardless of what follows: a decode that resolved to
	// a page with no frames is a terminal answer for THIS base (already
	// handled above); a readback failure below must not retry every call
	// either, matching the original single-feed contract this preserves.
	a.previewWinFedBase = a.budget.previewBase
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
	_ = a.d.Preview.ShowAnimFrame(a.budget.previewBase, page, idx, func() (*image.RGBA, error) {
		return readTexturePixels(a.ctx.Ren, page.Frames[idx], page.W, page.H)
	})
}

// previewFeedNeeded is feedDetachedPreviewFrame's whole "is there anything new
// to show" decision, factored out as a pure function so it can be driven
// exhaustively by a test. It is not a duplicate of the gate: it IS the gate,
// its only production caller being the line above.
func previewFeedNeeded(fedBase, base string, fedPage, page *render.TexturePage, animated bool, fedIdx, idx int) bool {
	if fedBase != base || fedPage != page {
		return true // different pick, or the same base re-decoded into a new page
	}
	return animated && idx != fedIdx
}

// AdvanceDetachedPreview is syncPreviewWindow's entry point for the main
// loop's NON-Frame passes — cmd/asyncao's minimized branch and its
// SkipFrame branch both call app.Background(dt) and skip app.Frame()
func (a *App) AdvanceDetachedPreview() {
	a.syncPreviewWindow()
}

// previewAnimWakeCap bounds how long the main loop may sleep/park on a
// pass that isn't calling app.Frame(), while the detached preview is open
// and showing a page with more than one frame — otherwise the loop's own
// much longer idle/minimized nap would freeze the popped-out animation for
// the length of that nap even though AdvanceDetachedPreview is being called.
const previewAnimWakeCap = 100 * time.Millisecond

// PreviewAnimWakeCap reports the longest cmd/asyncao's main loop should
// sleep/park on a pass that isn't calling app.Frame(), or 0 for "no extra
// constraint" — the overwhelmingly common case (preview closed, or showing a
// static pick). Callers combine it with whatever nap/wait duration they
// already chose, e.g. `if cap := a.PreviewAnimWakeCap(); cap > 0 && nap >
// cap { nap = cap }`.
func (a *App) PreviewAnimWakeCap() time.Duration {
	if !a.previewIsDetached() || a.d.Store == nil {
		return 0
	}
	page, ok := a.d.Store.Get(a.budget.previewBase)
	if !ok || !page.Animated || len(page.Frames) < 2 {
		return 0
	}
	return previewAnimWakeCap
}

// readTexturePixels reads an ALREADY-UPLOADED texture on the MAIN renderer
// back into CPU memory, for handing to a DIFFERENT renderer's SetFrame.
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
