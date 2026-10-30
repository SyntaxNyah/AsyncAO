package render

import (
	"math"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// glyphWhite is every glyph's colour for SetColorMod; a coloured glyph can't be recoloured to any hue.
var glyphWhite = sdl.Color{R: math.MaxUint8, G: math.MaxUint8, B: math.MaxUint8, A: math.MaxUint8}

// glyphKey identifies a glyph by rune and size-specific font pointer.
type glyphKey struct {
	r    rune
	font *ttf.Font
}

type cachedGlyph struct {
	tex  *sdl.Texture // nil for a blank glyph (space / render failure) — Draw skips it
	w, h int32
}

// GlyphCache renders glyphs once to white textures; FIFO eviction is safe as messages re-resolve each frame.
type GlyphCache struct {
	m     map[glyphKey]*cachedGlyph
	order []glyphKey
	cap   int
}

// NewGlyphCache returns a cache of about cap glyphs, floored so one message can't evict its own glyphs mid-frame.
func NewGlyphCache(cap int) *GlyphCache {
	const minGlyphCacheCap = 256 // one Latin page plus punctuation
	if cap < minGlyphCacheCap {
		cap = minGlyphCacheCap
	}
	return &GlyphCache{m: make(map[glyphKey]*cachedGlyph, cap), cap: cap}
}

// glyph returns r's white glyph; an unrenderable rune caches a nil texture so it isn't retried every frame.
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

func (g *GlyphCache) Purge() {
	for _, cg := range g.m {
		if cg != nil && cg.tex != nil {
			_ = cg.tex.Destroy()
		}
	}
	g.m = make(map[glyphKey]*cachedGlyph, g.cap)
	g.order = g.order[:0]
}
