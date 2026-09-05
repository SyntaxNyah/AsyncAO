package render

import (
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// Badge is a single small text / emoji texture the UI can position, scale and ALPHA-FADE
// freely — Copy into a chosen dst rect plus SetTextureAlphaMod. Unlike MessageRaster (a
// multi-line, multi-span message raster with no alpha control), a Badge is exactly one
// texture with its blend mode forced to BLENDMODE_BLEND, so an alpha-mod actually fades it
// rather than the glyph popping in and out at full opacity. The floating-reaction overlay
// (#2) needs that. Built once per distinct glyph (the caller caches it), then drawn every
// frame with zero allocation.
type Badge struct {
	tex  *sdl.Texture
	w, h int32
	// devScale is the device font scale (#77) the glyph was rasterized at — the same
	// contract MessageRaster.devScale carries: the caller measures/positions the badge
	// in LOGICAL px (Size), and Draw either folds the device pixels back to logical (the
	// ambient SetScale then re-multiplies them, exactly like the pre-#77 behavior) or,
	// when the renderer's CURRENT scale equals devScale, blits the device pixels 1:1
	// instead of letting SetScale resample a rounded-down-then-up rect. DefaultDevScale
	// (100) means the texture already IS logical px — RasterizeBadge's pre-#77 callers
	// (and every existing test) are byte-identical at that value.
	devScale int32
	// cgoRect is the scratch dst rect for the device-exact 1:1 Copy, and cgoClip the
	// scratch clip rect for its bracket's re-assert. Both are FIELDS, not locals: a
	// &sdl.Rect{} built inline and handed across the cgo boundary heap-escapes every
	// call (the same reason MessageRaster keeps cgoClip/cgoRect/dstGet/srcGet) — Draw's
	// own 0-alloc gate (TestBadgeBlendModeAndDraw) would catch a local here.
	cgoRect sdl.Rect
	cgoClip sdl.Rect
}

// RasterizeBadge renders text (one emoji, or a short glyph run) to a standalone texture in
// col, with BLENDMODE_BLEND set explicitly so SetTextureAlphaMod fades it. A colour-emoji
// glyph keeps its own colours and ignores col (SDL_ttf draws the colour bitmap) — exactly
// what a reaction emoji wants. Render thread; call once per distinct badge and cache the
// result. Returns (nil, nil) for empty text or a nil font.
//
// devScale is the device font scale (100 = 1:1) the caller opened font at — see
// Badge.devScale. Passing DefaultDevScale reproduces every pre-#77 caller byte for byte
// (Size/Draw both take the identity branch).
func RasterizeBadge(ren *sdl.Renderer, font *ttf.Font, text string, col sdl.Color, devScale int32) (*Badge, error) {
	if font == nil || text == "" {
		return nil, nil
	}
	surf, err := font.RenderUTF8Blended(text, col)
	if err != nil {
		return nil, err
	}
	defer surf.Free()
	tex, err := ren.CreateTextureFromSurface(surf)
	if err != nil {
		return nil, err
	}
	// CreateTextureFromSurface already picks BLEND for an alpha surface, but set it
	// explicitly so the fade can never silently no-op if that default ever changes.
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	return &Badge{tex: tex, w: surf.W, h: surf.H, devScale: devScale}, nil
}

// Size returns the badge's LOGICAL pixel dimensions — what the caller positions and
// scales with, matching every other UI measurement (the ambient SetScale, not this
// call, is what turns logical px into physical ones). Folds devScale back down via the
// SAME rounding rule MessageRaster's geometry uses (logicalFromDevice), so a badge and a
// label of the same on-screen size never disagree about it by a pixel.
func (b *Badge) Size() (w, h int32) {
	if b == nil {
		return 0, 0
	}
	return logicalFromDevice(b.w, b.devScale), logicalFromDevice(b.h, b.devScale)
}

// RawSize returns the badge's actual DEVICE-pixel texture dimensions — Size()'s
// pre-fold twin, and the exact rect the device-exact branch of Draw blits. Exists so a
// caller (or a wiring test) can tell the two apart: Size() is what stays constant
// across a UI-scale change (the on-screen footprint), RawSize() is what changes (the
// pixel density) — proving the fold actually reached the raster, not just the label
// used to describe it.
func (b *Badge) RawSize() (w, h int32) {
	if b == nil {
		return 0, 0
	}
	return b.w, b.h
}

// Draw blits the badge into dst (LOGICAL px) at the given alpha (0..255), then restores
// full opacity so the cached texture is never left dimmed for the next caller (the
// set→draw→restore discipline the shared-texture FX use). Zero-alloc — dst must be a
// reused scratch rect, not a fresh local (a cgo address-of forces a heap escape).
//
// renderPct is the renderer's CURRENT scale percent (100 = 1:1) — the same contract
// MessageRaster.DrawScaled uses, and read from the same place (Ctx.RenderScalePct).
// When it matches the devScale the badge was rasterized at, the blit goes device-exact:
// a SetScale(1,1) bracket copies the texture's OWN device pixels (RawSize) with no
// resample, instead of letting ren.SetScale stretch a rounded-down-then-up logical dst
// back to a (possibly mismatched) device size — see MessageRaster.deviceExact for the
// mechanism this mirrors, one fixed-size texture instead of one growing raster.
//
// clip is the LOGICAL clip rect currently set on ren, or nil for none — required, not
// optional, for the reason MessageRaster.DrawScaled documents at length: SDL backends
// disagree on WHEN a clip rect converts to device pixels (most bake it at SET time;
// macOS's Metal renderer evaluates it against whatever scale is in force at USE time),
// so the exact bracket must re-assert whatever clip is ambient, in device pixels, itself
// — or a Metal-backed renderer silently shrinks a clip that was set under a different
// scale for the duration of this blit. Passing nil when no clip is active costs nothing
// extra (no SetClipRect call at all, exactly like before this parameter existed).
func (b *Badge) Draw(ren *sdl.Renderer, dst *sdl.Rect, alpha uint8, renderPct int32, clip *sdl.Rect) {
	if b == nil || b.tex == nil {
		return
	}
	_ = b.tex.SetAlphaMod(alpha)
	if deviceExactAt(renderPct, b.devScale) {
		// Drop to 1:1 FIRST, then re-assert the clip in device pixels — same order,
		// same reason, as MessageRaster.draw's exact branch (text.go): setting the
		// clip before the scale change is what let Metal read it back at the wrong
		// scale and shrink it.
		_ = ren.SetScale(1, 1)
		if clip != nil {
			b.cgoClip = sdl.Rect{
				X: deviceFromLogicalAt(clip.X, b.devScale), Y: deviceFromLogicalAt(clip.Y, b.devScale),
				W: deviceFromLogicalAt(clip.W, b.devScale), H: deviceFromLogicalAt(clip.H, b.devScale),
			}
			_ = ren.SetClipRect(&b.cgoClip)
		}
		// W/H come from the texture's OWN device pixel count (RawSize), not from
		// re-projecting dst — that IS the fix: dst.W is a logical value already
		// rounded once, and multiplying it back up by scale can land on a different
		// device width than the texture actually has (the mismatch deviceExact's own
		// doc measures for text). Copying the native size can never mismatch it.
		b.cgoRect = sdl.Rect{X: deviceFromLogicalAt(dst.X, b.devScale), Y: deviceFromLogicalAt(dst.Y, b.devScale), W: b.w, H: b.h}
		_ = ren.Copy(b.tex, nil, &b.cgoRect)
		// Restore by recomputing the scale, not by reading it back: GetScale takes the
		// address of its named returns for cgo and heap-allocates two float32s per
		// call — a per-frame alloc this draw path can't have (see MessageRaster.draw).
		s := float32(renderPct) / float32(DefaultDevScale)
		_ = ren.SetScale(s, s)
		if clip != nil {
			b.cgoClip = *clip
			_ = ren.SetClipRect(&b.cgoClip)
		}
	} else {
		_ = ren.Copy(b.tex, nil, dst)
	}
	_ = b.tex.SetAlphaMod(0xFF) // restore: the texture is cached and reused next frame
}

// Destroy frees the badge's texture. Render thread only.
func (b *Badge) Destroy() {
	if b != nil && b.tex != nil {
		_ = b.tex.Destroy()
		b.tex = nil
	}
}
