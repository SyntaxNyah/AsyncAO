package render

import (
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// glyphWhite is the colour every cached glyph is rendered in; a per-draw SetColorMod
// tints it to the message colour or the rainbow hue. White + ColorMod is the ONE path
// that does shake/wave (displacement) AND rainbow (recolour) cleanly — you can't recolour
// an already-coloured glyph to arbitrary hues, and slicing a line texture at advance
// boundaries clips antialiased edges the moment a glyph is displaced.
var glyphWhite = sdl.Color{R: 255, G: 255, B: 255, A: 255}

// glyphKey identifies a cached glyph by rune + the (size-specific) font pointer.
type glyphKey struct {
	r    rune
	font *ttf.Font
}

// cachedGlyph is a WHITE single-glyph texture, tinted per draw via SetColorMod.
type cachedGlyph struct {
	tex  *sdl.Texture // nil for a blank glyph (space / render failure) — Draw skips it
	w, h int32
}

// GlyphCache renders individual glyphs to white textures once and reuses them, so #M5
// animated chat text can displace + tint each glyph with no per-frame rasterise. Bounded
// (FIFO eviction); a live message re-resolves its glyphs every frame, so eviction is safe
// — a miss just re-renders. Render thread only.
type GlyphCache struct {
	m     map[glyphKey]*cachedGlyph
	order []glyphKey
	cap   int
}

// NewGlyphCache returns a cache bounded to ~cap glyphs (floored so one message never
// evicts its own glyphs mid-frame).
func NewGlyphCache(cap int) *GlyphCache {
	if cap < 256 {
		cap = 256
	}
	return &GlyphCache{m: make(map[glyphKey]*cachedGlyph, cap), cap: cap}
}

// glyph returns the white glyph for r in font, rendering + caching on a miss. A blank or
// unrenderable rune caches a nil-tex entry so it isn't retried every frame. On a HIT it's
// a plain map read — zero allocation, the per-frame property the whole feature rests on.
func (g *GlyphCache) glyph(ren *sdl.Renderer, font *ttf.Font, r rune) *cachedGlyph {
	k := glyphKey{r, font}
	if cg, ok := g.m[k]; ok {
		return cg
	}
	cg := &cachedGlyph{}
	if r != ' ' && r != 0 && font != nil {
		if surf, err := font.RenderUTF8Blended(string(r), glyphWhite); err == nil {
			cg.w, cg.h = surf.W, surf.H
			if tex, err := ren.CreateTextureFromSurface(surf); err == nil {
				_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND) // alpha-blend the AA edges under ColorMod
				cg.tex = tex
			}
			surf.Free()
		}
	}
	g.m[k] = cg
	g.order = append(g.order, k)
	if len(g.order) > g.cap {
		old := g.order[0]
		g.order = g.order[1:]
		if ev := g.m[old]; ev != nil && ev.tex != nil {
			_ = ev.tex.Destroy()
		}
		delete(g.m, old)
	}
	return cg
}

// Purge frees every cached glyph (font rebuild / shutdown).
func (g *GlyphCache) Purge() {
	for _, cg := range g.m {
		if cg != nil && cg.tex != nil {
			_ = cg.tex.Destroy()
		}
	}
	g.m = make(map[glyphKey]*cachedGlyph, g.cap)
	g.order = g.order[:0]
}
