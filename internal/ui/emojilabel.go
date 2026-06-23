package ui

// Emoji-aware labels for the IC/OOC log lines and shownames. Those draw through
// the single-font label path (textTexture / LabelClippedFont), which renders any
// colour emoji as the chat font's tofu. labelEmoji keeps that exact fast path for
// plain text — the overwhelming common case — behind one cheap byte scan (no font
// work, no allocation), and only the rare label that actually contains emoji the
// chat font can't draw builds a multi-font render.MessageRaster (RasterizeFallback
// routes each emoji rune to the colour-emoji face). Those rasters are CACHED, so a
// steady-state frame just blits — the IC/OOC draw stays off the per-frame font
// path (hard rule §17.1: no synchronous font work on the render hot path).

import (
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

const (
	// emojiRasterMax bounds the emoji-label raster cache. Each entry owns its own
	// SDL textures (not the shared label atlas), so this is deliberately small —
	// emoji labels are rare; the cap only guards a pathological churn.
	emojiRasterMax = 256
	// emojiNoWrap is the build-time wrap width for a single-line label: a large
	// finite sentinel so the content NEVER wraps. The cache key carries no width,
	// so a wrapped raster would desync from the column — the DRAW clips to maxW
	// instead (a long emoji label is cut on the right, like any clipped label).
	emojiNoWrap = 1 << 20
)

// emojiKey keys the raster cache exactly like textKey: the same text, colour and
// primary-font pointer always produce the same raster. The emoji face is derived
// from the primary's scale, so it needn't be in the key — an emoji-font swap
// purges the whole cache (SetEmojiFont), as does a primary-font rebuild
// (purgeTextCache).
type emojiKey struct {
	text  string
	color sdl.Color
	font  *ttf.Font
}

// labelEmoji draws text that may contain colour emoji, clipped to maxW. Plain
// text takes the byte-identical LabelClippedFont fast path after one scan; an
// emoji label blits a cached multi-font raster, kicking the one-shot emoji-face
// load the first time it's needed (a showname/log emoji must trigger the load
// too, not just a chatbox message). While the face is still loading the label
// degrades to today's tofu and repaints in colour once it lands.
func (a *App) labelEmoji(primary, emoji *ttf.Font, x, y, maxW int32, text string, col sdl.Color) {
	c := a.ctx
	needEmoji := render.NeedsEmojiFallback(text)
	// Per-glyph raster only when the label has emoji OR mixes scripts no single face
	// covers (covers() reads the pick made by the caller's *FontFor, no rescan). Plain
	// single-script text — the overwhelming common case — stays on the fast path.
	if primary == nil || (!needEmoji && c.covers(text)) {
		c.LabelClippedFont(primary, x, y, maxW, text, col)
		return
	}
	if needEmoji {
		a.ensureEmojiFontLoad() // colour-emoji face; a mixed-script label alone needs none
	}
	m := c.emojiRaster(text, col, primary, emoji)
	if m == nil { // build failed → single-font (tofu) path
		c.LabelClippedFont(primary, x, y, maxW, text, col)
		return
	}
	cp, ch := c.pushClip(sdl.Rect{X: x, Y: y, W: maxW, H: m.Height()})
	m.Draw(c.Ren, m.TotalRunes(), x, y)
	c.popClip(cp, ch)
}

// icFieldFonts returns the fallback faces for an IC / OOC input box, or (nil, nil) for plain
// ASCII — so a normal message keeps the field's exact single-font fast path with no per-frame
// font work. For non-ASCII it kicks the colour-emoji load (NeedsEmojiFallback) and returns a
// log-set covering face (LogFontFor also latches the broad / CJK unicode load via noteScript)
// at the field's 1:1 size, so typed emoji / non-Latin render real glyphs instead of the
// chrome font's tofu. #M5 input-tofu fix.
func (a *App) icFieldFonts(text string) (primary, emoji *ttf.Font) {
	if isASCII(text) {
		return nil, nil
	}
	if render.NeedsEmojiFallback(text) {
		a.ensureEmojiFontLoad()
	}
	return a.ctx.LogFontFor(a.logPct, text), a.ctx.EmojiFont(a.logPct)
}

// emojiRaster returns (and caches) the multi-font raster for one emoji label, or
// nil when there's no emoji face yet / the build failed (the caller then degrades
// to the single-font path). The colour spans the whole label (one ColorSpan);
// the slice + the build are paid once per (text, colour, font), never per frame.
func (c *Ctx) emojiRaster(text string, col sdl.Color, primary, emoji *ttf.Font) *render.MessageRaster {
	if primary == nil {
		return nil
	}
	key := emojiKey{text: text, color: col, font: primary}
	if m, ok := c.emojiCache[key]; ok {
		return m
	}
	runes := []rune(text)
	textFonts := c.coverRunes(primary, runes) // per-rune covering face (mixed-script)
	// No emoji face AND every rune covered by primary → nothing this raster can add;
	// degrade to the single-font path (the caller tofus the emoji until the face lands),
	// exactly as before. A mixed-script run (textFonts differ) still builds.
	if emoji == nil && allSameFont(textFonts, primary) {
		return nil
	}
	spans := []render.ColorSpan{{Len: len(runes), Color: col}}
	m, err := render.RasterizeFallback(c.Ren, textFonts, emoji, text, spans, emojiNoWrap)
	if err != nil || m == nil {
		return nil
	}
	if c.emojiCache == nil {
		c.emojiCache = make(map[emojiKey]*render.MessageRaster, 16)
	} else if len(c.emojiCache) >= emojiRasterMax {
		c.purgeEmojiCache() // wholesale reset (like purgeTextCache); hot labels repopulate
	}
	c.emojiCache[key] = m
	return m
}

// purgeEmojiCache destroys every cached raster's textures and empties the cache.
// Render thread only (the textures are render-thread-owned). Called from
// purgeTextCache (a primary-font rebuild leaves the keys' pointers dead) and from
// SetEmojiFont (an emoji-face swap leaves the cached glyphs stale).
func (c *Ctx) purgeEmojiCache() {
	for k, m := range c.emojiCache {
		if m != nil {
			m.Destroy()
		}
		delete(c.emojiCache, k)
	}
}
