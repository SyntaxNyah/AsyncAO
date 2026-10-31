package render

import (
	"fmt"
	"image"
	"math"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// previewWindowTexBudgetBytes bounds the displayed frame; 16 MiB covers 2048x2048 RGBA, above decoder.go's spriteCap.
const previewWindowTexBudgetBytes = 16 << 20

// previewWindowAnimCacheBudgetBytes bounds one pick's retained frames: two decoded-asset caps at the default T1.
const previewWindowAnimCacheBudgetBytes = 32 << 20

// PreviewWindow owns a second SDL window and renderer with one texture, created on Open, torn down on Close.
// Every method must run on the main thread: SDL documents its renderer calls as main-thread-only (hard rule 1).
type PreviewWindow struct {
	open WindowOpener
	win  *sdl.Window
	ren  *sdl.Renderer
	tex  *sdl.Texture
	// texW/texH are the displayed texture's size, for Present's fit math.
	texW, texH int32
	// texIsCached means tex belongs to animFrames, not the display alone; confusing them double-frees or leaks.
	texIsCached bool
	// id is cached at Open so per-event routing doesn't call SDL.
	id uint32
	// lastX/Y/W/H and hadLastRect are snapped in Close, as reading after Destroy is a use-after-free.
	lastX, lastY, lastW, lastH int32
	hadLastRect                bool
	// animFrames/animFor/animSource/animBytes hold ShowAnimFrame's per-pick cache, indexed by source frame.
	animFrames []previewAnimFrame
	animFor    string
	animSource any
	animBytes  int64
	// animOverflowed means the cache budget is reached; uncached frames then call fill each show.
	animOverflowed bool
}

// previewAnimFrame carries its size because Texture.Query's cgo out-parameters escape, 4 allocs per call.
type previewAnimFrame struct {
	tex  *sdl.Texture
	w, h int32
}

// WindowOpener opens a window with its own renderer; the caller destroys both.
type WindowOpener func(title string, w, h int32) (*sdl.Window, *sdl.Renderer, error)

// NewPreviewWindow returns a closed PreviewWindow that opens through open.
func NewPreviewWindow(open WindowOpener) *PreviewWindow { return &PreviewWindow{open: open} }

// Window returns the OS window, nil when closed.
func (p *PreviewWindow) Window() *sdl.Window {
	if p == nil {
		return nil
	}
	return p.win
}

// IsOpen reports whether the OS window exists; the nil check is the entire cost when closed.
func (p *PreviewWindow) IsOpen() bool { return p != nil && p.win != nil }

// ID returns the window's SDL id, or 0 when closed; SDL ids start at 1, so callers need no IsOpen check.
func (p *PreviewWindow) ID() uint32 {
	if p == nil {
		return 0
	}
	return p.id
}

// Open creates the window and renderer, replacing any previous; two vsync'd direct3d renderers don't serialize.
func (p *PreviewWindow) Open(title string, w, h int32) error {
	p.Close() // idempotent-replace, not additive
	win, ren, err := p.open(title, w, h)
	if err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	_ = ren.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
	id, err := win.GetID()
	if err != nil {
		// A new window must have an id, but fail safe rather than leak.
		ren.Destroy()
		win.Destroy()
		return fmt.Errorf("preview window id: %w", err)
	}
	p.win, p.ren, p.id = win, ren, id
	return nil
}

// OpenAt is Open at a desktop position; callers restoring a saved position should use OpenAtClamped.
func (p *PreviewWindow) OpenAt(title string, x, y, w, h int32) error {
	if err := p.Open(title, w, h); err != nil {
		return err
	}
	p.win.SetPosition(x, y)
	return nil
}

// OpenAtClamped is OpenAt through ClampPositionToDisplays, so a saved position can't land off every display.
func (p *PreviewWindow) OpenAtClamped(title string, x, y, w, h int32) error {
	cx, cy := ClampPositionToDisplays(x, y, w, h)
	return p.OpenAt(title, cx, cy, w, h)
}

// previewOffscreenMarginPx is a title bar's height: enough of the corner must show for the user to grab it.
const previewOffscreenMarginPx = 40

// ClampPositionToDisplays keeps x,y if on a display, else recentres on the primary; no displays trusts the input.
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
	// Display index 0 is always the primary (SDL_GetDisplayBounds).
	b, err := sdl.GetDisplayUsableBounds(0)
	if err != nil {
		return x, y // nothing to go on; leave the input rather than guess
	}
	return b.X + (b.W-w)/2, b.Y + (b.H-h)/2
}

// Position returns the window's desktop top-left; ok=false when closed.
func (p *PreviewWindow) Position() (x, y int32, ok bool) {
	if p == nil || p.win == nil {
		return 0, 0, false
	}
	x, y = p.win.GetPosition()
	return x, y, true
}

// Size returns the client-area size; ok=false when closed.
func (p *PreviewWindow) Size() (w, h int32, ok bool) {
	if p == nil || p.win == nil {
		return 0, 0, false
	}
	w, h = p.win.GetSize()
	return w, h, true
}

// LastRect returns the rect at the last Close, for a window closed by its own X; ok=false before the first Close.
func (p *PreviewWindow) LastRect() (x, y, w, h int32, ok bool) {
	if p == nil || !p.hadLastRect {
		return 0, 0, 0, 0, false
	}
	return p.lastX, p.lastY, p.lastW, p.lastH, true
}

// Close destroys everything and returns to the closed state; safe when already closed.
func (p *PreviewWindow) Close() {
	if p.win != nil {
		// Must precede Destroy: reading a destroyed window's rect is a use-after-free.
		p.lastX, p.lastY = p.win.GetPosition()
		p.lastW, p.lastH = p.win.GetSize()
		p.hadLastRect = true
	}
	// resetAnimCache also frees the displayed texture in whichever ownership case applies.
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

// resetAnimCache destroys the pick's cached textures, on a pick or source change, SetFrame and Close.
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

// setDisplayTexture shows tex; the old texture is destroyed only if the display owned it, not animFrames.
func (p *PreviewWindow) setDisplayTexture(tex *sdl.Texture, w, h int32, cached bool) {
	if p.tex != nil && !p.texIsCached && p.tex != tex {
		p.tex.Destroy()
	}
	p.tex, p.texW, p.texH, p.texIsCached = tex, w, h, cached
}

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
	// Leaving animation mode: nothing else would ever visit the previous pick's textures, so they would leak.
	p.resetAnimCache()
	p.setDisplayTexture(tex, int32(img.Rect.Dx()), int32(img.Rect.Dy()), false)
	return nil
}

// uploadFrame builds a texture within the frame budget; (nil, nil) for zero area; the caller owns the result.
func (p *PreviewWindow) uploadFrame(img *image.RGBA) (*sdl.Texture, error) {
	w, h := int32(img.Rect.Dx()), int32(img.Rect.Dy())
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	if int64(w)*int64(h)*4 > previewWindowTexBudgetBytes {
		return nil, fmt.Errorf("render: preview frame %dx%d exceeds the %d MiB preview budget", w, h, previewWindowTexBudgetBytes>>mibShift)
	}
	// Recreated each call: this runs on selection or frame-index changes, so a streaming texture isn't worth it.
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

// ShowAnimFrame shows frame idx of (base, source), calling fill once per frame until the cache budget is reached.
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
	frameBytes := int64(w) * int64(h) * texBytesPerPixel
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
		// Show it but don't retain it; never grow the cache past its cap (hard rule 4).
		p.animOverflowed = true
		p.setDisplayTexture(tex, w, h, false)
	}
	return nil
}

// Present draws the texture contained in the window, re-fitting every frame because SetFrame doesn't run on a resize.
func (p *PreviewWindow) Present() {
	if p.ren == nil {
		return
	}
	_ = p.ren.SetDrawColor(0, 0, 0, math.MaxUint8)
	_ = p.ren.Clear()
	if p.tex != nil {
		winW, winH := p.win.GetSize()
		dst := previewFitRect(winW, winH, p.texW, p.texH)
		_ = p.ren.Copy(p.tex, nil, &dst)
	}
	_ = p.ren.Present() // the preview has its own renderer; a failed present only drops a preview frame
}

// previewFitRect fits src in win on both axes, centred, as the window can be narrower than the art.
func previewFitRect(winW, winH, srcW, srcH int32) sdl.Rect {
	if winW <= 0 || winH <= 0 || srcW <= 0 || srcH <= 0 {
		return sdl.Rect{W: winW, H: winH}
	}
	// Cross-multiply to compare the ratios using only integer math.
	var w, h int32
	if int64(winW)*int64(srcH) <= int64(winH)*int64(srcW) {
		w = winW
		h = int32((int64(srcH)*int64(winW) + int64(srcW)/2) / int64(srcW)) // rounded, not truncated
	} else {
		h = winH
		w = int32((int64(srcW)*int64(winH) + int64(srcH)/2) / int64(srcH))
	}
	return sdl.Rect{X: (winW - w) / 2, Y: (winH - h) / 2, W: w, H: h}
}

// HandleEvent handles this window's events; the caller routes by window id, so no id check is needed.
func (p *PreviewWindow) HandleEvent(ev sdl.Event) {
	if e, ok := ev.(*sdl.WindowEvent); ok && e.Event == sdl.WINDOWEVENT_CLOSE {
		p.Close()
	}
}
