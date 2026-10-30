package render

import (
	"math"
	"math/rand"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Post-processing overlays are cached textures blended over the stage, as SDL's 2D renderer has no custom shaders.

// PostFX selects the active overlays; CRT is a preset that forces scanlines and vignette and adds a phosphor mask.
type PostFX struct {
	Vignette  bool
	Scanlines bool
	Grain     bool
	CRT       bool
}

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

	// CRT mask: R/G/B stripes multiplied over the stage; phosphorDim is high so the tint stays subtle.
	phosphorStripeW = 2
	phosphorCount   = 3 // red, green, blue stripes
	phosphorDim     = 200
)

// SetPostFX sets the post-processing prefs once per frame; textures build lazily on first enable.
func (v *Viewport) SetPostFX(p PostFX) { v.postFX = p }

// applyPostFX blends enabled overlays over the original vp, before the shout punch or shake moved it.
func (v *Viewport) applyPostFX(ren *sdl.Renderer, vp sdl.Rect) {
	if !v.postFX.Active() {
		return
	}
	// CRT turns these two overlays on as well as their own toggles.
	scan := v.postFX.Scanlines || v.postFX.CRT
	vign := v.postFX.Vignette || v.postFX.CRT
	if scan {
		if t := v.ensureScanlines(ren, vp.W, vp.H); t != nil {
			v.postRect = vp
			_ = ren.Copy(t, nil, &v.postRect)
		}
	}
	if v.postFX.CRT {
		// Before the vignette, so the darkened edges sit on top of the grille.
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

// ensureScanlines returns the WxH scanline texture, rebuilt only on a resize; a 1:1 blit keeps lines crisp.
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
	pix := make([]byte, int(w)*int(h)*texBytesPerPixel)
	for y := int32(0); y < h; y++ {
		if y%scanlinePeriod != 0 {
			continue
		}
		row := int(y) * int(w) * texBytesPerPixel
		for x := 0; x < int(w); x++ {
			pix[row+x*texBytesPerPixel+3] = scanlineAlpha // black (RGB already 0), only the dark rows get alpha
		}
	}
	t, err := uploadPixels(ren, pix, w, h)
	if err != nil {
		return nil
	}
	v.scanlineTex, v.scanlineW, v.scanlineH = t, w, h
	return t
}

// ensureCRTMask returns the WxH mask, rebuilt on resize; MOD blend scales each stripe's unlit channels.
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
	pix := make([]byte, int(w)*int(h)*texBytesPerPixel)
	for y := 0; y < int(h); y++ {
		row := y * int(w) * texBytesPerPixel
		for x := 0; x < int(w); x++ {
			r, g, b := byte(phosphorDim), byte(phosphorDim), byte(phosphorDim)
			switch (x / phosphorStripeW) % phosphorCount { // which phosphor this column lights
			case 0:
				r = math.MaxUint8
			case 1:
				g = math.MaxUint8
			default:
				b = math.MaxUint8
			}
			i := row + x*texBytesPerPixel
			pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, math.MaxUint8
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

// buildVignette makes the square radial texture, stretched at draw time so the ellipse follows the frame aspect.
func buildVignette(ren *sdl.Renderer) *sdl.Texture {
	const n = vignetteSize
	pix := make([]byte, n*n*texBytesPerPixel)
	cx, cy := float64(n-1)/2, float64(n-1)/2
	maxD := math.Hypot(cx, cy)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / maxD // 0 centre → 1 corner
			a := 0.0
			if d > vignetteInner {
				a = (d - vignetteInner) / (1 - vignetteInner)
			}
			pix[(y*n+x)*texBytesPerPixel+3] = byte(value.Clamp(a, 0, 1) * vignetteMaxA) // black, alpha = darkness
		}
	}
	t, err := uploadPixels(ren, pix, n, n)
	if err != nil {
		return nil
	}
	return t
}

// buildGrain fills out with noise tiles from a deterministic seed; cycling them provides the motion.
func buildGrain(ren *sdl.Renderer, out *[grainFrames]*sdl.Texture) {
	rng := rand.New(rand.NewSource(grainSeed))
	const n = grainTileSize
	for f := 0; f < grainFrames; f++ {
		pix := make([]byte, n*n*texBytesPerPixel)
		for i := 0; i < n*n; i++ {
			g := byte(rng.Intn(math.MaxUint8 + 1))
			pix[i*texBytesPerPixel], pix[i*texBytesPerPixel+1], pix[i*texBytesPerPixel+2] = g, g, g
			pix[i*texBytesPerPixel+3] = byte(rng.Intn(grainAlpha + 1))
		}
		t, err := uploadPixels(ren, pix, n, n)
		if err != nil {
			return
		}
		out[f] = t
	}
}

// PurgePostFX frees the overlay textures; render thread only.
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
