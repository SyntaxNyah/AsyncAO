package render

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

// previewWindowTexBudgetBytes bounds the preview window's OWN texture slot —
// it holds exactly ONE frame at a time (never a general-purpose cache, hard
// rule 4: no unbounded cache), so the cap only needs to cover the largest
// single decoded RGBA frame this client can legitimately produce. 16 MiB
// covers a 2048x2048 frame (2048*2048*4 bytes) — comfortably above
// decoder.go's spriteCap ceiling (the display height minus the courtroom's
// UI reserve, cmd/asyncao's stageBottomReservePx, so at most ~2160px even on
// a 4K display) — while staying a rounding error against the 256 MiB total
// memory budget (cmd/asyncao's memoryBudgetBytes) and dwarfed by T1's 64 MiB
// / T2's 128 MiB (cache.DefaultT1BudgetBytes / DefaultT2BudgetBytes): a
// single preview frame can never meaningfully compete with the main asset
// budgets.
const previewWindowTexBudgetBytes = 16 << 20

// previewWindowAnimCacheBudgetBytes bounds the SUM of every DISTINCT decoded
// frame ShowAnimFrame retains for one animated pick — separate from, and in
// ADDITION to, previewWindowTexBudgetBytes' per-frame ceiling above. Hard
// rule 4 (no unbounded cache): a looping animation's whole cached frame set,
// not just its momentarily-displayed frame, needs its own named cap. Sized
// to comfortably exceed the decoder's own default per-asset decoded-bytes
// cap (cache.MaxDecodedAssetBytes(cache.DefaultT1BudgetBytes) == 16 MiB —
// the sum of every frame of one animated asset, by construction, at the
// default T1 budget) with headroom for GPU texture padding/alignment: two
// decoded-asset-caps' worth rather than an unrelated round number. A
// power-user who raises T1's budget raises the per-asset cap proportionally
// (budget/4); a pick whose total decoded bytes exceed THIS window's cap
// degrades gracefully instead of growing past it — see animOverflowed.
const previewWindowAnimCacheBudgetBytes = 32 << 20

// PreviewWindow owns a second, independent SDL window + renderer + a single
// texture slot, created on Open and fully torn down on Close.
//
// The zero value (returned by NewPreviewWindow) is CLOSED: no sdl.Window, no
// sdl.Renderer, no texture. Every method on a closed PreviewWindow costs one
// nil check and nothing else — see IsOpen, ID and Present — which is the
// "closed = free" contract BenchmarkPreviewWindowPresentClosed /
// TestPreviewWindowClosedPresentIsZeroAlloc exist to prove (modeled on
// internal/assets/mountserve_test.go's TestNoMountsIsExactlyOneAtomicLoad /
// BenchmarkActiveMountLayerNoMounts, the same "0 base = 0 cost" contract for
// the local-mounts feature).
//
// Hard rule 1 (no SDL off the render thread): every method here makes SDL
// calls and must run on the SAME OS thread as the main window's — SDL2
// allows many windows/renderers from one thread but never allows a second
// thread touching SDL at all (measured empirically on this dev box; see
// cmd/asyncao/quit.go's doc comment for the full probe). Callers must never
// wrap Open/Close/Present/HandleEvent/SetFrame in a goroutine.
type PreviewWindow struct {
	win *sdl.Window
	ren *sdl.Renderer
	tex *sdl.Texture
	// texW/texH are the CURRENTLY DISPLAYED texture's pixel size (queried
	// off the texture itself by setDisplayTexture, never hand-tracked), used
	// by Present's fit math.
	texW, texH int32
	// texIsCached reports whether tex is currently one of animFrames' own
	// entries (owned by the animation cache below — destroyed only by
	// resetAnimCache/Close) rather than a texture the display slot alone
	// owns (built by SetFrame, or ShowAnimFrame's cache-overflow fallback —
	// destroyed by setDisplayTexture's own replace, or by Close). Without
	// this distinction, leaving an animated pick would either double-free a
	// texture still referenced by animFrames or leak a solo one.
	texIsCached bool
	// id is cached once at Open (an SDL window's id never changes after
	// creation) so the main loop's per-event routing gate (ID) never touches
	// SDL to answer a question the window already answered once.
	id uint32
	// lastX/Y/W/H + hadLastRect capture the window's final desktop position
	// and client size the MOMENT Close tears it down — whether Close was
	// called by internal/ui's own re-attach toggle or by HandleEvent reacting
	// to the window's OWN titlebar X (measured fact 1: SDL posts
	// WINDOWEVENT_CLOSE and nothing else; nobody destroys the window for
	// you). Reading GetPosition/GetSize AFTER Destroy would be a use-after-
	// free, so Close is the one place that must snapshot them, and LastRect
	// is the one place a caller reads the snapshot back — this is the whole
	// "closing with its own X reconciles state" contract: there is no
	// separate "detached" bool anywhere to go stale, because IsOpen() is
	// already live and LastRect() already survives the very Close() call
	// that flips it.
	lastX, lastY, lastW, lastH int32
	hadLastRect                bool
	// animFrames/animFor/animSource/animBytes/animOverflowed together are
	// ShowAnimFrame's bounded per-pick cache of distinct decoded animation
	// frames — see ShowAnimFrame's doc for the full contract. animFrames is
	// indexed by the source page's own frame ordinal (nil until that ordinal
	// has been visited); animFor/animSource are the (base, source-identity)
	// pair the cache was built for — either changing invalidates the whole
	// cache, never just one slot, because a re-decode/eviction of the source
	// page (a NEW source identity for the SAME base) means every previously
	// cached frame's content is stale, not just the one at the current idx.
	animFrames []previewAnimFrame
	animFor    string
	animSource any
	animBytes  int64
	// animOverflowed latches once caching one more distinct frame for the
	// CURRENT pick would exceed previewWindowAnimCacheBudgetBytes: no further
	// frames are added to animFrames for this pick, and every subsequent
	// ShowAnimFrame call for an uncached ordinal falls back to calling fill
	// and uploading directly every time it's shown — graceful degrade (still
	// correct, still bounded by the animation's own frame-index-change rate,
	// never by render-frame rate) rather than growing the cache past its cap.
	animOverflowed bool
}

// previewAnimFrame is one cached animation frame: the texture plus the pixel
// size Present's fit math needs. The size is carried alongside rather than
// asked back off the texture because sdl.Texture.Query crosses cgo with four
// out-parameters, which escape to the heap — measured at 4 allocs / 16 B per
// call, the same class of trap as Ren.GetScale (CLAUDE.md). Every producer
// already knows the size at upload time, so nothing has to ask.
type previewAnimFrame struct {
	tex  *sdl.Texture
	w, h int32
}

// NewPreviewWindow returns a closed PreviewWindow. See the type doc for the
// "closed is a nil check" contract.
func NewPreviewWindow() *PreviewWindow { return &PreviewWindow{} }

// IsOpen reports whether the OS window exists. THE NIL CHECK COMES FIRST and
// is the entire cost when closed — the overwhelmingly common case, mirroring
// internal/assets.Manager.activeMountLayer's documented ordering.
func (p *PreviewWindow) IsOpen() bool { return p != nil && p.win != nil }

// ID returns the SDL window id events for this window are tagged with, or 0
// when closed. 0 is never a real SDL window id (SDL ids are assigned from 1),
// the same sentinel cmd/asyncao's mainWindowShouldQuit relies on — so a
// caller comparing an event's WindowID against ID() never has to branch on
// IsOpen() first (see cmd/asyncao's eventForPreviewWindow).
func (p *PreviewWindow) ID() uint32 {
	if p == nil {
		return 0
	}
	return p.id
}

// Open creates the OS window and its own renderer, destroying anything a
// previous Open already had (idempotent-replace, never additive/leaking).
// w/h are the initial window size in pixels.
//
// PRESENTVSYNC: MEASURED on this dev box (165 Hz display) that two vsync'd
// direct3d renderers do NOT serialize — presenting the second one measured
// as cheap as not having it at all (see cmd/asyncao/quit.go's probe comment)
// — so there is no reason to run the preview unsynced.
func (p *PreviewWindow) Open(title string, w, h int32) error {
	p.Close() // idempotent-replace, not additive
	win, err := sdl.CreateWindow(title, sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		w, h, sdl.WINDOW_SHOWN|sdl.WINDOW_RESIZABLE)
	if err != nil {
		return fmt.Errorf("preview window: %w", err)
	}
	ren, err := sdl.CreateRenderer(win, -1, sdl.RENDERER_ACCELERATED|sdl.RENDERER_PRESENTVSYNC)
	if err != nil {
		// VMs/headless (dummy video driver) have no accelerated backend —
		// same fallback cmd/asyncao's main renderer takes (main.go).
		ren, err = sdl.CreateRenderer(win, -1, sdl.RENDERER_SOFTWARE)
		if err != nil {
			win.Destroy()
			return fmt.Errorf("preview renderer: %w", err)
		}
	}
	_ = ren.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
	id, err := win.GetID()
	if err != nil {
		// Unreachable in practice (a window that was just created must have
		// an id), but fail safe rather than leak: tear down what Open built.
		ren.Destroy()
		win.Destroy()
		return fmt.Errorf("preview window id: %w", err)
	}
	p.win, p.ren, p.id = win, ren, id
	return nil
}

// OpenAt is Open but places the window at a specific desktop position instead
// of letting the OS/window manager choose — the entry point for reopening at
// a remembered spot (config.AssetPreferences' persisted rect). x,y are the
// window's top-left in desktop coordinates. Callers restoring a SAVED
// position should go through OpenAtClamped instead, so a monitor that no
// longer exists can never strand the window somewhere unreachable.
func (p *PreviewWindow) OpenAt(title string, x, y, w, h int32) error {
	if err := p.Open(title, w, h); err != nil {
		return err
	}
	p.win.SetPosition(x, y)
	return nil
}

// OpenAtClamped is OpenAt after passing x,y through ClampPositionToDisplays —
// the sanity net for requirement 5 (preview-window-wire): a position saved
// while a second monitor was connected must not reopen off every currently
// connected display.
func (p *PreviewWindow) OpenAtClamped(title string, x, y, w, h int32) error {
	cx, cy := ClampPositionToDisplays(x, y, w, h)
	return p.OpenAt(title, cx, cy, w, h)
}

// previewOffscreenMarginPx is how much of the window's top-left corner must
// land inside a currently connected display's usable bounds for a saved
// position to be trusted. Sized to a typical title bar's height — enough
// that if the corner lands within a display at all, the user can actually
// see and grab that title bar to drag the window the rest of the way back,
// rather than requiring the WHOLE window to already be on-screen (which
// would needlessly recentre a window that is mostly, but not entirely, on a
// display whose resolution merely shrank).
const previewOffscreenMarginPx = 40

// ClampPositionToDisplays returns x,y unchanged if the window's top-left
// corner (inset by previewOffscreenMarginPx) lands inside ANY currently
// connected display's usable bounds, or recentres it on the PRIMARY display
// otherwise — requirement 5 (preview-window-wire): reopening on a monitor
// that was unplugged, had its resolution changed, or belonged to a laptop
// that's since been undocked must never strand the window somewhere the user
// can never reach again.
//
// A missing/unavailable display API (headless dummy driver with no reported
// displays) trusts the input rather than guessing — there is nothing to
// clamp AGAINST.
func ClampPositionToDisplays(x, y, w, h int32) (cx, cy int32) {
	n, err := sdl.GetNumVideoDisplays()
	if err != nil || n <= 0 {
		return x, y
	}
	for i := 0; i < n; i++ {
		b, err := sdl.GetDisplayUsableBounds(i)
		if err != nil {
			continue
		}
		if x+previewOffscreenMarginPx >= b.X && x+previewOffscreenMarginPx < b.X+b.W &&
			y+previewOffscreenMarginPx >= b.Y && y+previewOffscreenMarginPx < b.Y+b.H {
			return x, y
		}
	}
	// Unreachable on every connected display: recentre on the primary
	// display's usable bounds. Display index 0 is always the primary
	// (SDL_GetDisplayBounds's own documented convention).
	b, err := sdl.GetDisplayUsableBounds(0)
	if err != nil {
		return x, y // nothing to go on; leave the input rather than guess
	}
	return b.X + (b.W-w)/2, b.Y + (b.H-h)/2
}

// Position returns the window's current desktop-pixel top-left, and
// ok=false when closed (there is nothing to report).
func (p *PreviewWindow) Position() (x, y int32, ok bool) {
	if p == nil || p.win == nil {
		return 0, 0, false
	}
	x, y = p.win.GetPosition()
	return x, y, true
}

// Size returns the window's current client-area pixel size, and ok=false
// when closed.
func (p *PreviewWindow) Size() (w, h int32, ok bool) {
	if p == nil || p.win == nil {
		return 0, 0, false
	}
	w, h = p.win.GetSize()
	return w, h, true
}

// LastRect returns the position+size the window had at the moment it was
// last torn down by Close — the ONLY way to learn a rect for a window that
// closed itself via its own titlebar X (Position/Size are both "closed" by
// the time HandleEvent's caller could otherwise ask). ok=false before the
// very first Close, matching a freshly constructed PreviewWindow that has
// never been open at all — there is nothing to have remembered yet.
func (p *PreviewWindow) LastRect() (x, y, w, h int32, ok bool) {
	if p == nil || !p.hadLastRect {
		return 0, 0, 0, 0, false
	}
	return p.lastX, p.lastY, p.lastW, p.lastH, true
}

// Close destroys the texture/renderer/window and returns PreviewWindow to
// its zero (closed) state. Safe on an already-closed PreviewWindow (a no-op)
// and safe to call more than once.
func (p *PreviewWindow) Close() {
	if p.win != nil {
		// MUST happen before Destroy: reading position/size from a destroyed
		// window is a use-after-free, and this is the only chance to ever
		// learn the final rect of a window that is closing itself (see
		// LastRect's doc).
		p.lastX, p.lastY = p.win.GetPosition()
		p.lastW, p.lastH = p.win.GetSize()
		p.hadLastRect = true
	}
	// resetAnimCache frees every cached animation-frame texture AND the display
	// slot, in whichever of the two ownership cases applies. Close deliberately
	// does not repeat that test itself: one place knowing the rule is what
	// keeps a solo-owned frame from surviving a teardown it should not.
	p.resetAnimCache()
	if p.ren != nil {
		p.ren.Destroy()
		p.ren = nil
	}
	if p.win != nil {
		p.win.Destroy()
		p.win = nil
	}
	p.texW, p.texH, p.id = 0, 0, 0
}

// resetAnimCache destroys every texture ShowAnimFrame has cached for the
// CURRENT (animFor, animSource) pick and returns the cache to empty. Called
// whenever ShowAnimFrame sees the pick or its source page's identity change
// (a stale cache would otherwise show frames from the WRONG animation or the
// wrong decode generation of the right one), whenever SetFrame is used
// (a plain single-picture display means leaving animation mode for this
// window entirely — there is nothing left to invalidate it later), and from
// Close.
//
// THE DISPLAY SLOT IS CLEARED IN BOTH OWNERSHIP CASES, and the second one is
// not symmetry for its own sake:
//   - A cache-owned texture (texIsCached) was just freed by the loop above, so
//     leaving it in the slot would be a use-after-free on Present's next call.
//   - An outright-owned one — what the over-budget path hands to
//     setDisplayTexture(tex, false) — is still valid memory, so its failure is
//     quieter and worse. animFor/animSource move to the new pick while the
//     window keeps PAINTING THE OLD ONE, and that split state sticks: the
//     caller marks a pick fed BEFORE the readback that might fail, so a fill
//     error on the first frame of the new pick is never retried and the
//     previous emote stays on screen until the user picks a third one.
func (p *PreviewWindow) resetAnimCache() {
	for _, f := range p.animFrames {
		if f.tex != nil {
			f.tex.Destroy()
		}
	}
	p.animFrames = nil
	p.animFor = ""
	p.animSource = nil
	p.animBytes = 0
	p.animOverflowed = false
	if p.tex != nil && !p.texIsCached {
		p.tex.Destroy() // solo-owned: the loop above never visited it
	}
	p.tex, p.texW, p.texH, p.texIsCached = nil, 0, 0, false
}

// setDisplayTexture points the window's display slot at tex — either a
// freshly built texture this call is handing off ownership of, or a cached
// animation frame an earlier ShowAnimFrame call already owns (cached=true).
// The PREVIOUS display texture is destroyed only when this slot owned it
// outright (cached==false last time): a cache-owned one belongs to
// animFrames and is torn down there (resetAnimCache/Close), never here.
//
// w/h are the texture's pixel size, passed in rather than queried back off
// tex: every caller already has it (uploadFrame validated it, the cache
// stored it), and sdl.Texture.Query would cross cgo and heap-allocate its
// out-parameters on a path that runs on every animation frame tick.
func (p *PreviewWindow) setDisplayTexture(tex *sdl.Texture, w, h int32, cached bool) {
	if p.tex != nil && !p.texIsCached && p.tex != tex {
		p.tex.Destroy()
	}
	p.tex, p.texW, p.texH, p.texIsCached = tex, w, h, cached
}

// SetFrame uploads img as the preview's next displayed frame.
//
// A no-op (returns nil) when closed or handed a nil/empty image, so a caller
// need not gate every call on IsOpen() first.
//
// WHY THE PREVIEW NEEDS ITS OWN UPLOAD, and why the signature is a plain
// *image.RGBA rather than a render.TexturePage: the main render.TextureStore
// releases a Decoded's image.RGBA right after its own GPU upload
// (uploadTier's `defer d.Release()`, textures.go), and an SDL texture
// belongs to the renderer that created it — this renderer can no more borrow
// the main store's already-uploaded textures than it could borrow another
// process's. *image.RGBA is exactly the pre-upload shape every decode in
// this codebase already produces (assets.Decoded.Frames). internal/ui feeds
// this by reading the main renderer's ALREADY-resident texture back to CPU
// memory (render-target blit + ReadPixels, both against the MAIN renderer —
// see internal/ui's readTexturePixels) rather than re-entering
// internal/assets: the asset is by construction already decoded and uploaded
// by the time a preview is on screen to detach, and internal/assets.Manager
// has its own T1-residency short-circuit (manager.go's t1Contains checks)
// that would make a second Prefetch for an already-resident base a silent
// no-op — it would never re-emit a decode event to catch. One texture,
// rebuilt whenever the content changes (a new emote pick), capped at
// previewWindowTexBudgetBytes: deliberately NOT a general-purpose multi-page
// cache, and NOT re-uploaded every render frame — see Present's doc for the
// idle-animation tradeoff that follows from that, and ShowAnimFrame below for
// the animated-pick sibling that IS a (bounded) multi-frame cache.
func (p *PreviewWindow) SetFrame(img *image.RGBA) error {
	if p.ren == nil || img == nil {
		return nil
	}
	tex, err := p.uploadFrame(img)
	if err != nil {
		return err
	}
	if tex == nil {
		return nil // degenerate (zero) size — matches the previous silent no-op
	}
	// A plain SetFrame means leaving animation mode for this window entirely
	// (the caller has a single, non-animated picture to show) — tear down
	// whatever animation cache the PREVIOUS pick may have built, or its
	// textures would simply leak (nothing else ever visits them again).
	p.resetAnimCache()
	p.setDisplayTexture(tex, int32(img.Rect.Dx()), int32(img.Rect.Dy()), false)
	return nil
}

// uploadFrame is the single primitive that turns decoded pixels into a
// texture on THIS window's own renderer — SetFrame and ShowAnimFrame's
// cache-miss path both build on it, so there is exactly one place in this
// type that knows how to do that and one place that enforces the per-frame
// budget.
//
// Returns (nil, nil) for a degenerate (zero-area) image — the existing
// silent-no-op shape SetFrame documented before this was extracted — and
// (nil, err) when img exceeds previewWindowTexBudgetBytes or an SDL call
// fails. The caller owns the returned texture on success.
func (p *PreviewWindow) uploadFrame(img *image.RGBA) (*sdl.Texture, error) {
	w, h := int32(img.Rect.Dx()), int32(img.Rect.Dy())
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	if int64(w)*int64(h)*4 > previewWindowTexBudgetBytes {
		return nil, fmt.Errorf("render: preview frame %dx%d exceeds the %d MiB preview budget", w, h, previewWindowTexBudgetBytes>>20)
	}
	// Rebuilt every call rather than reused+Update()'d: both callers invoke
	// this on a selection change or one animation-frame-index tick, never in
	// the main per-frame hot path (that's Present, below), so the simpler
	// always-recreate shape — the exact one TextureStore.buildPage already
	// uses for every sprite frame in the client — is worth more here than the
	// small win of a STREAMING texture nothing else in this package uses.
	tex, err := p.ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_STATIC, w, h)
	if err != nil {
		return nil, err
	}
	if err := tex.Update(nil, unsafe.Pointer(&img.Pix[0]), img.Stride); err != nil {
		_ = tex.Destroy()
		return nil, err
	}
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	return tex, nil
}

// ShowAnimFrame displays frame idx of the animated pick identified by
// (base, source), building a small per-pick cache the first time each
// distinct ordinal is visited so that a LOOPING animation calls fill at most
// ONCE PER DISTINCT FRAME, no matter how many times ShowAnimFrame itself is
// called afterward (every subsequent loop through the same frames is a
// cache hit: point the display slot at the already-uploaded texture, no
// readback, no upload — the exact per-render-frame cost Present already
// pays regardless). fill is the caller's readback+decode step (in practice,
// internal/ui's readTexturePixels against the MAIN renderer); it is called
// at most once per (base, source, idx) triple under budget, or on every
// call once previewWindowAnimCacheBudgetBytes is exhausted for this pick
// (see animOverflowed) — still correct, just no longer avoiding the repeat
// readback for that one oversized pick.
//
// source is an opaque, comparable identity token for the frame's origin
// (in practice, the *render.TexturePage the frames were read from) — this
// package does not interpret it beyond equality. That keeps PreviewWindow
// free of any dependency on what "source" means to a caller: a re-decode or
// eviction that replaces the page with a NEW pointer for the SAME base
// naturally invalidates the cache (base unchanged, source changed), exactly
// as a genuinely different pick would (base changed) — both call the same
// resetAnimCache path, because both mean "everything cached is now stale."
//
// A no-op (returns nil) when closed, mirroring SetFrame.
func (p *PreviewWindow) ShowAnimFrame(base string, source any, idx int, fill func() (*image.RGBA, error)) error {
	if p.ren == nil {
		return nil
	}
	if idx < 0 {
		return nil // pageFrameLoop never returns negative; defensive against a misbehaving caller
	}
	if base != p.animFor || source != p.animSource {
		p.resetAnimCache()
		p.animFor, p.animSource = base, source
	}
	if idx < len(p.animFrames) && p.animFrames[idx].tex != nil {
		hit := p.animFrames[idx]
		p.setDisplayTexture(hit.tex, hit.w, hit.h, true) // cache hit: no readback, no upload
		return nil
	}
	img, err := fill()
	if err != nil {
		return err
	}
	if img == nil {
		return nil
	}
	tex, err := p.uploadFrame(img)
	if err != nil {
		return err
	}
	if tex == nil {
		return nil
	}
	w, h := int32(img.Rect.Dx()), int32(img.Rect.Dy())
	frameBytes := int64(w) * int64(h) * 4
	if !p.animOverflowed && p.animBytes+frameBytes <= previewWindowAnimCacheBudgetBytes {
		if idx >= len(p.animFrames) {
			grown := make([]previewAnimFrame, idx+1)
			copy(grown, p.animFrames)
			p.animFrames = grown
		}
		p.animFrames[idx] = previewAnimFrame{tex: tex, w: w, h: h}
		p.animBytes += frameBytes
		p.setDisplayTexture(tex, w, h, true)
	} else {
		// Over cache budget for this pick: show it, but do not retain it —
		// the NEXT visit to this ordinal calls fill again (graceful degrade,
		// hard rule 4 — never grow the cache past its named cap).
		p.animOverflowed = true
		p.setDisplayTexture(tex, w, h, false)
	}
	return nil
}

// Present draws the preview's current texture CONTAINED within the window
// (aspect preserved, letterboxed, centred — never stretched) and presents it.
//
// Closed is a SINGLE NIL CHECK and nothing else — this is the per-frame
// touch point cmd/asyncao's main loop calls every rendered frame regardless
// of whether the preview is open, and the whole reason it can do that for
// free is this ordering (see BenchmarkPreviewWindowPresentClosed /
// TestPreviewWindowClosedPresentIsZeroAlloc).
//
// The window is user-resizable (Open's WINDOW_RESIZABLE) and SetFrame is
// deliberately NOT called every time the window resizes — only on a content
// change — so Present must re-fit against the CURRENT window size every
// frame rather than trusting a stale rect computed at SetFrame time.
func (p *PreviewWindow) Present() {
	if p.ren == nil {
		return
	}
	_ = p.ren.SetDrawColor(0, 0, 0, 255)
	_ = p.ren.Clear()
	if p.tex != nil {
		winW, winH := p.win.GetSize()
		dst := previewFitRect(winW, winH, p.texW, p.texH)
		_ = p.ren.Copy(p.tex, nil, &dst)
	}
	p.ren.Present()
}

// previewFitRect fits a srcW x srcH image inside a winW x winH window,
// preserving aspect ratio (CONTAIN, not stretch) and centring the result —
// the same non-stretch idiom splashfit.go documents for the shout bubble,
// but fit to BOTH axes here (splashFitRect only fits by height, correct for
// a viewport-anchored overlay but wrong for a freely resizable OS window,
// which can just as easily end up narrower than the art as shorter).
//
// Falls back to filling the whole window when either input has no usable
// size (mirrors splashFitRect's own fallback: a rect nobody can reason about
// still needs SOME answer, and "fill everything" is the least-wrong one).
// Integer arithmetic only, and it runs once per rendered frame while the
// preview window is open — no allocation, nothing to cache.
func previewFitRect(winW, winH, srcW, srcH int32) sdl.Rect {
	if winW <= 0 || winH <= 0 || srcW <= 0 || srcH <= 0 {
		return sdl.Rect{W: winW, H: winH}
	}
	// Cross-multiply instead of dividing to floats: winW/srcW vs winH/srcH,
	// compared as (winW*srcH) vs (winH*srcW) so the smaller ratio (the
	// tighter-fitting axis) is found with only integer math.
	var w, h int32
	if int64(winW)*int64(srcH) <= int64(winH)*int64(srcW) {
		// Width is the binding constraint: fill it, derive height from it.
		w = winW
		h = int32((int64(srcH)*int64(winW) + int64(srcW)/2) / int64(srcW)) // rounded, not truncated
	} else {
		h = winH
		w = int32((int64(srcW)*int64(winH) + int64(srcH)/2) / int64(srcH))
	}
	return sdl.Rect{X: (winW - w) / 2, Y: (winH - h) / 2, W: w, H: h}
}

// HandleEvent is the preview window's entire input handling.
//
// A real, bordered, resizable OS window already gives drag (title bar) and
// resize (edges) for free through the window manager — there is nothing for
// this type to implement for either. The only thing left for a single image
// view is closing ITSELF when its own titlebar X is clicked, and MEASURED
// FACT 1 (cmd/asyncao/quit.go) is why that has to be explicit: SDL does not
// destroy the window or post SDL_QUIT for a non-last window's close, so
// nothing would happen at all without this.
//
// Callers (cmd/asyncao's event routing gate) only ever invoke this with an
// event carrying THIS window's id, so no id check is needed here — see
// eventForPreviewWindow.
func (p *PreviewWindow) HandleEvent(ev sdl.Event) {
	if e, ok := ev.(*sdl.WindowEvent); ok && e.Event == sdl.WINDOWEVENT_CLOSE {
		p.Close()
	}
}
