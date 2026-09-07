package ui

import (
	"fmt"
	"image"
	"time"
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
// the currently selected previewBase and, for an animated pick, its current
// frame index. Called every pass (see AdvanceDetachedPreview) regardless of
// whether app.Frame() itself runs this pass — a looping animation must keep
// advancing even while the main loop's SkipFrame/minimized branches suppress
// every full redraw of the main window, which is the whole reason a preview
// gets popped out in the first place.
//
// The GPU work (readTexturePixels' render-target blit+readback, and the
// texture upload underneath SetFrame/ShowAnimFrame) only happens on a
// genuine CHANGE — a new pick, a new source page (T1 evicted and re-decoded
// the same base), or the frame INDEX advancing — never merely because this
// function was called again. pageFrameLoop is a pure, cheap function of the
// wall clock (already proven safe to call every pass by the in-app box's own
// identical use, screens.go), so recomputing idx here every call costs
// nothing on the passes where nothing changed.
//
// A static/1-frame page (page.Animated false, or a single frame) always
// resolves idx to 0 and never changes it, so it keeps the ORIGINAL "fed
// exactly once per pick" behavior via SetFrame, completely unchanged from
// before this animated-preview feature existed
// (TestFeedDetachedPreviewFrameUploadsOnce is unmodified evidence of that).
// A page with more than one frame calls render.PreviewWindow.ShowAnimFrame
// instead, which caches each distinct frame the first time it is visited so
// a LOOPING animation performs the readback at most once per distinct
// frame, never once per visit.
//
// A still-loading pick (nothing resident yet) is retried next call — cheap
// (a texture-store lookup, no allocation) since a real decode settles within
// a handful of frames, and the window simply keeps showing its previous
// content (or a black window on the very first-ever pick) until then.
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
	samePick := a.previewWinFedBase == a.previewBase && a.previewWinFedPage == page
	if samePick && (!animated || idx == a.previewWinFedFrame) {
		return // same pick, same source page, nothing new to show
	}
	// Marked attempted regardless of what follows: a decode that resolved to
	// a page with no frames is a terminal answer for THIS base (already
	// handled above); a readback failure below must not retry every call
	// either, matching the original single-feed contract this preserves.
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

// AdvanceDetachedPreview is syncPreviewWindow's entry point for the main
// loop's NON-Frame passes — cmd/asyncao's minimized branch and its
// SkipFrame branch both call app.Background(dt) and skip app.Frame()
// entirely, and handlePreviewInput (the only other syncPreviewWindow call
// site) only runs inside Frame(). Without this second call site, a popped-out
// window showing an animated pick would freeze the instant the main window
// minimizes or the static-skip gate engages — exactly the case a user pops
// the window out FOR (watching it while alt-tabbed away).
//
// Not a second implementation of anything: this calls the SAME
// syncPreviewWindow handlePreviewInput already calls, just reachable from a
// pass that never reaches handlePreviewInput. The closed-preview cost is
// unchanged — syncPreviewWindow's own "closed = free" contract
// (TestSyncPreviewWindowClosedIsZeroAlloc) already covers every call site.
func (a *App) AdvanceDetachedPreview() {
	a.syncPreviewWindow()
}

// previewAnimWakeCapMs bounds how long the main loop may sleep/park on a
// pass that isn't calling app.Frame(), while the detached preview is open
// and showing a page with more than one frame — otherwise the loop's own
// much longer idle/minimized nap would freeze the popped-out animation for
// the length of that nap even though AdvanceDetachedPreview is being called.
// Modeled on assetDemandWakeInterval (app.go): a coarse, bounded extra wake,
// not a precise "time until this animation's next frame-index change"
// computation — NextWakeDelay's considerRender/render=true contract is
// deliberately NOT reused here (see PreviewAnimWakeCap's doc) because that
// would force a full main-window redraw just to animate an unrelated small
// window, exactly the cost this feature's own performance constraint rules
// out. ~10 Hz is brisk enough that typical AO idle-sprite frame delays (tens
// to a couple hundred ms) read as smooth, and this cap only ever applies
// while a detached window is actually open AND actually animating — the
// overwhelmingly common case (nothing detached) pays nothing at all.
const previewAnimWakeCapMs = 100 * time.Millisecond

// PreviewAnimWakeCap reports the longest cmd/asyncao's main loop should
// sleep/park on a pass that isn't calling app.Frame(), or 0 for "no extra
// constraint" — the overwhelmingly common case (preview closed, or showing a
// static pick). Callers combine it with whatever nap/wait duration they
// already chose, e.g. `if cap := a.PreviewAnimWakeCap(); cap > 0 && nap >
// cap { nap = cap }`.
//
// Deliberately NOT routed through SkipFrame/NextWakeDelay's render-forcing
// contract: this only bounds a SLEEP duration on a pass that has ALREADY
// decided not to draw the main window, it never flips that decision — the
// main window's own pacing (its "last frame is still exactly right"
// contract) is untouched regardless of what a completely separate OS window
// is doing. Render-thread only, like every other a.d.Store/a.d.Preview touch
// point.
func (a *App) PreviewAnimWakeCap() time.Duration {
	if !a.previewIsDetached() || a.d.Store == nil {
		return 0
	}
	page, ok := a.d.Store.Get(a.previewBase)
	if !ok || !page.Animated || len(page.Frames) < 2 {
		return 0
	}
	return previewAnimWakeCapMs
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
