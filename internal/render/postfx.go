package render

import (
	"math"
	"math/rand"

	"github.com/veandco/go-sdl2/sdl"
)

// Post-processing overlays (#10): retro / CRT looks blended OVER the composited stage —
// vignette (darkened edges), scanlines, and animated film grain. All OFF by default; each is
// a CACHED texture blended in a single stretched blit, so an enabled effect costs one GPU
// blit per frame and a disabled one is free (an early return). Built GPU-side via the
// renderer (SDL2's 2D path has no custom fragment shaders); the size-dependent scanline
// texture regenerates only when the stage size changes — never per frame. Steady-state is
// 0-alloc: the overlays are reused and the blit destination is a Viewport scratch rect.

// PostFX selects which overlays are active (all OFF/false = no work). CRT is a
// PRESET: it forces scanlines + vignette and adds a phosphor/aperture-grille mask,
// so one toggle gives the whole retro-TV look. (True barrel curvature + bloom need
// the whole stage captured to a render target — a perf-sensitive change deferred
// out of this 0-alloc overlay path.)
type PostFX struct {
	Vignette  bool
	Scanlines bool
	Grain     bool
	CRT       bool
}

// Active reports whether any overlay is on (so applyPostFX returns immediately when off).
func (p PostFX) Active() bool { return p.Vignette || p.Scanlines || p.Grain || p.CRT }

const (
	vignetteSize   = 256  // the radial-gradient texture is square and stretched to the stage
	vignetteInner  = 0.42 // fraction of the radius that stays fully clear before darkening
	vignetteMaxA   = 165  // darkest alpha at the corners
	scanlinePeriod = 3    // one dark line every N rows
	scanlineAlpha  = 70   // darkness of a scan line
	grainFrames    = 8    // pre-built noise tiles, cycled one per frame
	grainTileSize  = 96   // noise tile (stretched over the stage — random, so blur is fine)
	grainAlpha     = 26   // subtle
	grainSeed      = 0x5eed

	// #77 CRT aperture-grille mask: vertical R/G/B phosphor stripes multiplied
	// (BLENDMODE_MOD) over the stage. phosphorStripeW is each colour stripe's
	// width (the RGB triad is 3× it); phosphorDim is what the two non-lit channels
	// of a stripe are multiplied to (out of 255) — high, so the tint stays subtle.
	phosphorStripeW = 2
	phosphorDim     = 200
)

// SetPostFX mirrors the user's post-processing prefs onto the viewport (once per frame, like
// SetSpriteFX). Cheap: a value copy; the textures build lazily on first enable.
func (v *Viewport) SetPostFX(p PostFX) { v.postFX = p }

// applyPostFX blends the enabled overlays over the stage rect (the ORIGINAL vp, before the
// shout-punch / shake moved it — the frame, not the art). 0-alloc when off (early return)
// and on (cached textures + a scratch rect).
func (v *Viewport) applyPostFX(ren *sdl.Renderer, vp sdl.Rect) {
	if !v.postFX.Active() {
		return
	}
	// CRT is a preset that layers scanlines + an aperture-grille mask + vignette,
	// so those two overlays turn on with it as well as on their own toggles.
	scan := v.postFX.Scanlines || v.postFX.CRT
	vign := v.postFX.Vignette || v.postFX.CRT
	if scan {
		if t := v.ensureScanlines(ren, vp.W, vp.H); t != nil {
			v.postRect = vp
			_ = ren.Copy(t, nil, &v.postRect)
		}
	}
	if v.postFX.CRT {
		// Phosphor stripes MULTIPLY the stage (+ scanlines) — drawn before the
		// vignette so the darkened edges sit on top of the grille.
		if t := v.ensureCRTMask(ren, vp.W, vp.H); t != nil {
			v.postRect = vp
			_ = ren.Copy(t, nil, &v.postRect)
		}
	}
	if vign {
		if v.vignetteTex == nil {
			v.vignetteTex = buildVignette(ren)
		}
		if v.vignetteTex != nil {
			v.postRect = vp
			_ = ren.Copy(v.vignetteTex, nil, &v.postRect)
		}
	}
	if v.postFX.Grain {
		if v.grainTex[0] == nil {
			buildGrain(ren, &v.grainTex)
		}
		if t := v.grainTex[v.grainIdx]; t != nil {
			v.postRect = vp
			_ = ren.Copy(t, nil, &v.postRect)
		}
		v.grainIdx = (v.grainIdx + 1) % grainFrames
	}
}

// ensureScanlines returns the W×H scanline texture, rebuilding it only when the stage size
// changed (a resize — never per frame). Blitting it 1:1 over the stage keeps the lines crisp.
func (v *Viewport) ensureScanlines(ren *sdl.Renderer, w, h int32) *sdl.Texture {
	if w <= 0 || h <= 0 {
		return nil
	}
	if v.scanlineTex != nil && v.scanlineW == w && v.scanlineH == h {
		return v.scanlineTex
	}
	if v.scanlineTex != nil {
		_ = v.scanlineTex.Destroy()
		v.scanlineTex = nil
	}
	pix := make([]byte, int(w)*int(h)*4)
	for y := int32(0); y < h; y++ {
		if y%scanlinePeriod != 0 {
			continue
		}
		row := int(y) * int(w) * 4
		for x := 0; x < int(w); x++ {
			pix[row+x*4+3] = scanlineAlpha // black (RGB already 0), only the dark rows get alpha
		}
	}
	t, err := uploadPixels(ren, pix, w, h)
	if err != nil {
		return nil
	}
	v.scanlineTex, v.scanlineW, v.scanlineH = t, w, h
	return t
}

// ensureCRTMask returns the W×H aperture-grille mask (vertical R/G/B phosphor
// stripes), rebuilding it only when the stage size changed. Its blend mode is MOD
// so blitting MULTIPLIES the stage: the "lit" channel of each stripe stays 255
// (×1) while the other two scale to phosphorDim/255 — a subtle colour shimmer.
// 0-alloc after the one-time (per size) build, like the scanline texture.
func (v *Viewport) ensureCRTMask(ren *sdl.Renderer, w, h int32) *sdl.Texture {
	if w <= 0 || h <= 0 {
		return nil
	}
	if v.crtMaskTex != nil && v.crtMaskW == w && v.crtMaskH == h {
		return v.crtMaskTex
	}
	if v.crtMaskTex != nil {
		_ = v.crtMaskTex.Destroy()
		v.crtMaskTex = nil
	}
	pix := make([]byte, int(w)*int(h)*4)
	for y := 0; y < int(h); y++ {
		row := y * int(w) * 4
		for x := 0; x < int(w); x++ {
			r, g, b := byte(phosphorDim), byte(phosphorDim), byte(phosphorDim)
			switch (x / phosphorStripeW) % 3 { // which phosphor this column lights
			case 0:
				r = 255
			case 1:
				g = 255
			default:
				b = 255
			}
			i := row + x*4
			pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, 255
		}
	}
	t, err := uploadPixels(ren, pix, w, h)
	if err != nil {
		return nil
	}
	_ = t.SetBlendMode(sdl.BLENDMODE_MOD) // blit multiplies the stage
	v.crtMaskTex, v.crtMaskW, v.crtMaskH = t, w, h
	return t
}

// buildVignette makes the square radial-darkening texture (clear centre → dark corners),
// stretched to the stage at draw time (an ellipse that follows the frame aspect).
func buildVignette(ren *sdl.Renderer) *sdl.Texture {
	const n = vignetteSize
	pix := make([]byte, n*n*4)
	cx, cy := float64(n-1)/2, float64(n-1)/2
	maxD := math.Hypot(cx, cy)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / maxD // 0 centre → 1 corner
			a := 0.0
			if d > vignetteInner {
				a = (d - vignetteInner) / (1 - vignetteInner)
			}
			pix[(y*n+x)*4+3] = byte(clampF(a, 0, 1) * vignetteMaxA) // black, alpha = darkness
		}
	}
	t, err := uploadPixels(ren, pix, n, n)
	if err != nil {
		return nil
	}
	return t
}

// buildGrain fills out with grainFrames noise tiles (grey speckle at a low alpha), cycled to
// animate. Deterministic seed so it looks the same each launch (cycling provides the motion).
func buildGrain(ren *sdl.Renderer, out *[grainFrames]*sdl.Texture) {
	rng := rand.New(rand.NewSource(grainSeed))
	const n = grainTileSize
	for f := 0; f < grainFrames; f++ {
		pix := make([]byte, n*n*4)
		for i := 0; i < n*n; i++ {
			g := byte(rng.Intn(256))
			pix[i*4], pix[i*4+1], pix[i*4+2] = g, g, g
			pix[i*4+3] = byte(rng.Intn(grainAlpha + 1))
		}
		t, err := uploadPixels(ren, pix, n, n)
		if err != nil {
			return
		}
		out[f] = t
	}
}

// PurgePostFX frees the cached overlay textures (shutdown). Render thread only.
func (v *Viewport) PurgePostFX() {
	if v.vignetteTex != nil {
		_ = v.vignetteTex.Destroy()
		v.vignetteTex = nil
	}
	if v.scanlineTex != nil {
		_ = v.scanlineTex.Destroy()
		v.scanlineTex = nil
	}
	if v.crtMaskTex != nil {
		_ = v.crtMaskTex.Destroy()
		v.crtMaskTex = nil
	}
	for i := range v.grainTex {
		if v.grainTex[i] != nil {
			_ = v.grainTex[i].Destroy()
			v.grainTex[i] = nil
		}
	}
	v.particles.purge() // #124 free the cached weather dot texture too
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
