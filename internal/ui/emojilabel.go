package ui

// Emoji-aware labels for the IC/OOC log lines and shownames. Those draw through the single-font label path, which
// renders colour emoji as the chat font's tofu. labelEmoji keeps that exact fast path for plain text behind one byte
// scan (no font work, no allocation), and only a label with emoji the chat font can't draw builds a multi-font
// render.MessageRaster (RasterizeFallback routes each emoji rune to the colour-emoji face). Rasters are cached, so a
// steady-state frame just blits.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

const (
	// emojiRasterMax bounds the emoji-label raster cache. Each entry owns its own SDL textures, so it is deliberately
	// small; emoji labels are rare.
	emojiRasterMax = 256
	// emojiNoWrap is the wrap width for a single-line label: a large sentinel so the content never wraps. The cache key
	// carries no width, so a wrapped raster would desync from the column; the draw clips to maxW instead.
	emojiNoWrap = 1 << 20
)

// emojiKey keys the raster cache like textKey: the same text, colour and font pointers always give the same raster. An
// emoji-font swap (SetEmojiFont) or a primary-font rebuild (purgeTextCache) purges the whole cache.
type emojiKey struct {
	text  string
	color sdl.Color
	font  *ttf.Font // the primary (text) face
	emoji *ttf.Font // the colour-emoji face — DISTINCT per size, so the same emoji at two
	// sizes (picker, IC-bar button, reaction float) can't share a raster and render at the wrong size.
	// bold is the raster's weight: bold and plain draws of one showname must not share a raster.
	bold bool
}

// labelEmoji draws text that may contain colour emoji, clipped to maxW. Plain text takes the LabelClippedFont fast path
// after one scan; an emoji label blits a cached multi-font raster and kicks the one-shot emoji-face load (a showname or
// log emoji must trigger it too). While the face loads the label shows tofu, then repaints in colour.
func (a *App) labelEmoji(primary, emoji *ttf.Font, x, y, maxW int32, text string, col sdl.Color) {
	a.labelEmojiWeight(primary, emoji, x, y, maxW, text, col, false)
}

// labelEmojiWeight is labelEmoji at a weight. bold draws the face's real bold cut (one texture on the plain path, a
// bold span on the raster path); a 1 px-offset second pass smeared into a double at a fractional UI scale (see
// Ctx.textTextureBold).
func (a *App) labelEmojiWeight(primary, emoji *ttf.Font, x, y, maxW int32, text string, col sdl.Color, bold bool) {
	c := a.ctx
	needEmoji := render.NeedsEmojiFallback(text)
	// Per-glyph raster only when the label has emoji or mixes scripts no single face covers (covers() reads the
	// caller's *FontFor pick); plain single-script text stays on the fast path.
	if primary == nil || (!needEmoji && c.coversFace(primary, text)) {
		c.LabelClippedFontWeight(primary, x, y, maxW, text, col, bold)
		return
	}
	// We are on the raster branch because the whole-line pick failed; for a bare BMP pictograph (♥ ✨ ⭐ ⌛) that means no
	// text face has the glyph. NeedsEmojiFallback only sees supplementary-plane runes and VS16, so the colour face was
	// never requested and the character drew as a box. Ask for the colour face now.
	if !needEmoji && c.uncoveredPictograph(primary, text) {
		needEmoji = true
		if emoji == nil {
			// The caller gated its own emoji face on NeedsEmojiFallback too and handed us nil. Resolve one at the
			// primary's own scale so the emoji matches the row's text size.
			emoji = c.emojiFaceFor(primary)
		}
	}
	if needEmoji {
		a.ensureEmojiFontLoad() // colour-emoji face; a mixed-script label alone needs none
	}
	m := c.emojiRasterWeight(text, col, primary, emoji, bold)
	if m == nil { // build failed → single-font (tofu) path
		c.LabelClippedFontWeight(primary, x, y, maxW, text, col, bold)
		return
	}
	cp, ch := c.pushClip(sdl.Rect{X: x, Y: y, W: maxW, H: m.Height()})
	// DrawScaled, not Draw: the spans are rasterized at the device sibling faces (emojiRasterWeight folds
	// c.textDevPct), so the plain scaled path would divide that device-precise geometry back to logical and let
	// ren.SetScale multiply it up (the resample MessageRaster.deviceExact documents). c.clipRect is the rect pushClip
	// just asserted, in logical px; DrawScaled needs it to re-assert the clip in device pixels (see its doc).
	m.DrawScaled(c.Ren, m.TotalRunes(), x, y, c.RenderScalePct(), &c.clipRect)
	c.popClip(cp, ch)
}

// emojiFaceFor is the colour-emoji face at primary's own resolved scale: the emoji baseline must match the text face in
// the same row. Falls back to the chrome scale when primary belongs to no scaled set (chrome-only, headless).
func (c *Ctx) emojiFaceFor(primary *ttf.Font) *ttf.Font {
	return c.EmojiFont(c.emojiPctFor(primary))
}

// emojiPctFor is emojiFaceFor's scale alone; the device-sibling resolution needs the percent, not the face.
func (c *Ctx) emojiPctFor(primary *ttf.Font) int {
	if log, _, idx := c.setIndexOf(primary); log != nil && idx >= 0 {
		return log.pct
	}
	return DefaultScalePct
}

// labelCoveringCentered draws chrome text that may need a face the fixed chrome font lacks (another script, or colour
// emoji), vertically centred in a rowH-tall row and clipped to maxW. It is labelEmoji's logic for the widget kit, which
// has no App: widgets showing server-supplied text (showname picker rows, lobby server labels) draw through Ctx alone.
// chromeFaceFor fixes the script half; only a per-glyph raster fixes a string that needs the emoji face too.
//
// The plain-text branch is the textTexture/blitLabel path the widgets already used, behind the same cheap gate, so
// ASCII chrome keeps its speed.
func (c *Ctx) labelCoveringCentered(x, rowY, rowH, maxW int32, text string, col sdl.Color) {
	face := c.chromeFaceFor(text)
	needEmoji := render.NeedsEmojiFallback(text)
	if face == nil || (!needEmoji && c.coversFace(face, text)) {
		c.blitCenteredLabel(face, x, rowY, rowH, maxW, text, col)
		return
	}
	if needEmoji && c.emojiLoad != nil {
		c.emojiLoad() // first emoji on screen may well be a roster/dropdown row
	}
	m := c.emojiRaster(text, col, face, c.EmojiFont(DefaultScalePct))
	if m == nil { // build failed → the single-face (tofu) path, as before
		c.blitCenteredLabel(face, x, rowY, rowH, maxW, text, col)
		return
	}
	h := m.Height()
	y := rowY + (rowH-h)/2
	cp, ch := c.pushClip(sdl.Rect{X: x, Y: y, W: maxW, H: h})
	// DrawScaled, as in labelEmojiWeight: this raster's spans are device-sibling faces too (emojiRaster shares
	// emojiRasterWeight).
	m.DrawScaled(c.Ren, m.TotalRunes(), x, y, c.RenderScalePct(), &c.clipRect)
	c.popClip(cp, ch)
}

// blitCenteredLabel is the single-face row label: cached texture, clipped to maxW, centred on the row's logical height.
func (c *Ctx) blitCenteredLabel(face *ttf.Font, x, rowY, rowH, maxW int32, text string, col sdl.Color) {
	t, ok := c.textTexture(text, col, face)
	if !ok {
		return
	}
	w := t.logicalW() // #77-LOGICAL px
	if w > maxW && maxW > 0 {
		w = maxW
	}
	c.blitLabel(t, x, rowY+(rowH-uiLogicalFromDevice(t.h, t.devPct))/2, w)
}

// labelEmojiWidth is the logical width labelEmoji would draw text at, measured through the same two paths and caches as
// the draw, so a caller aligning a label in a box can't measure on one face and paint with another.
//
// Not c.TextWidth: that measures the fixed chrome face, while a showname draws in the theme's family and size.
// Allocation-free once warm: each branch probes the cache the draw will hit again (textCache on the plain path,
// emojiCache on the fallback, whose nil result is cached too).
func (a *App) labelEmojiWidth(primary, emoji *ttf.Font, text string, col sdl.Color) int32 {
	c := a.ctx
	if primary == nil || text == "" {
		return 0
	}
	// Same gate and order as labelEmoji.
	if !render.NeedsEmojiFallback(text) && c.coversFace(primary, text) {
		return c.labelWidthFont(primary, text, col)
	}
	m := c.emojiRaster(text, col, primary, emoji)
	if m == nil { // build failed / nothing to add → labelEmoji falls back too
		return c.labelWidthFont(primary, text, col)
	}
	return m.PrefixWidth(m.TotalRunes())
}

// labelWidthFont is the drawn width of a single-font label: the texture the next LabelClippedFont blits, measured
// rather than re-rendered.
func (c *Ctx) labelWidthFont(font *ttf.Font, text string, col sdl.Color) int32 {
	t, ok := c.textTexture(text, col, font)
	if !ok {
		return 0
	}
	return t.logicalW()
}

// labelName draws a user-authored name (or short user text) at the chrome scale with full CJK / non-Latin / emoji
// fallback, clipped to maxW; it replaces c.LabelClipped on name surfaces that would tofu Japanese. Plain ASCII stays on
// the exact LabelClippedFont path (LogFontFor returns the single-font set and labelEmoji's gate agrees). For non-ASCII,
// LogFontFor latches the broad/CJK load via noteScript, so the name repaints with real glyphs once the face streams in.
func (a *App) labelName(x, y, maxW int32, text string, col sdl.Color) {
	c := a.ctx
	primary := c.LogFontFor(DefaultScalePct, text) // covering face (+ latches the font load)
	var emoji *ttf.Font
	if render.NeedsEmojiFallback(text) {
		a.ensureEmojiFontLoad()
		emoji = c.EmojiFont(DefaultScalePct)
	}
	a.labelEmoji(primary, emoji, x, y, maxW, text, col)
}

// labelNameElem is labelName dressed by one per-element font: the user's per-panel override, or a theme's. It is
// separate from labelName because that helper has many callers with no element of their own, which must keep the shared
// log face.
func (a *App) labelNameElem(el themeFontElem, x, y, maxW int32, text string, col sdl.Color) {
	primary := a.elemFontFor(el, DefaultScalePct, text)
	var emoji *ttf.Font
	if render.NeedsEmojiFallback(text) {
		a.ensureEmojiFontLoad()
		// The emoji baseline must match the text face's resolved scale, or the two draw at different sizes.
		emoji = a.elemEmoji(el, DefaultScalePct)
	}
	a.labelEmoji(primary, emoji, x, y, maxW, text, col)
}

// icFieldFonts returns the fallback faces for an IC / OOC input box, or (nil, nil) for plain ASCII so a normal message
// keeps the field's single-font fast path. For non-ASCII it kicks the colour-emoji load and returns a log-set covering
// face (LogFontFor also latches the broad / CJK load via noteScript) so typed emoji and non-Latin render real glyphs.
//
// Always the chrome size (DefaultScalePct), the size the field's single-font path draws, so typing one non-Latin rune
// doesn't resize the field.
func (a *App) icFieldFonts(text string) (primary, emoji *ttf.Font) {
	if isASCII(text) {
		return nil, nil
	}
	if render.NeedsEmojiFallback(text) {
		a.ensureEmojiFontLoad()
	}
	return a.ctx.LogFontFor(DefaultScalePct, text), a.ctx.EmojiFont(DefaultScalePct)
}

// emojiRaster returns (and caches) the multi-font raster for one emoji label, or nil when there is no emoji face yet or
// the build failed (the caller then uses the single-font path). One ColorSpan covers the label; the build is paid once
// per (text, colour, font).
func (c *Ctx) emojiRaster(text string, col sdl.Color, primary, emoji *ttf.Font) *render.MessageRaster {
	return c.emojiRasterWeight(text, col, primary, emoji, false)
}

// emojiRasterWeight is emojiRaster at a weight, with the pictograph promotion.
func (c *Ctx) emojiRasterWeight(text string, col sdl.Color, primary, emoji *ttf.Font, bold bool) *render.MessageRaster {
	if primary == nil {
		return nil
	}
	key := emojiKey{text: text, color: col, font: primary, emoji: emoji, bold: bold}
	if m, ok := c.emojiCache[key]; ok {
		return m
	}
	runes := []rune(text)
	// Cache by the logical primary/emoji (what callers pass) but rasterize with the device siblings so the label is
	// crisp at UI scale. Draw divides the device dst back to logical via the raster's devScale, so pushClip (logical)
	// still fences it. A textDevPct change purges this cache (SetTextDevScale).
	dev := c.textDevPct
	textFonts := c.coverRunes(primary, runes) // per-rune covering face (mixed-script) — logical set
	// The runes no text face covers that the colour face can draw (♥ ✨ ⭐): coverRunes left those on primary (a .notdef
	// box); the mask says which to hand the emoji face. nil on ordinary labels, so no allocation.
	picto := c.uncoveredPictographMask(primary, runes)
	// No emoji face AND every rune covered by primary → nothing this raster can add;
	// No emoji face and every rune covered by primary: there is nothing to add, so degrade to the single-font path. A
	// mixed-script run still builds. The nil is cached: uncached, every visible emoji label re-ran []rune and
	// coverRunes per frame while the face was unloaded. Self-invalidating: the key embeds the emoji-face pointer, and
	// SetEmojiFont/SetFallbackFonts/SetCJKFonts/purgeTextCache purge the cache when faces change.
	// The pictograph mask is not an escape from this bail: with no colour face there is nothing to promote to, so the
	// label keeps its box and rebuilds when pollEmojiFont lands one (the key carries the face pointer).
	if emoji == nil && allSameFont(textFonts, primary) {
		return c.cacheEmojiRaster(key, nil)
	}
	// Swap the per-rune faces and the emoji face to their device siblings; the emoji size follows the primary's
	// per-element pct.
	devFonts := textFonts
	devEmoji := emoji
	if dev != DefaultScalePct && dev != 0 {
		devFonts = make([]*ttf.Font, len(textFonts))
		for i, f := range textFonts {
			devFonts[i] = c.deviceFontForAnyPct(f)
		}
		if emoji != nil {
			devEmoji = c.emojiDeviceFont(c.emojiPctFor(primary))
		}
	}
	// Hand the uncovered pictographs to the colour face, on the face slice RasterizeFallback rasterizes from so the
	// promotion picks up the device sibling at a fractional UI scale. Skipped while the colour face isn't loaded (the
	// label keeps its box and repaints when pollEmojiFont lands it).
	if picto != nil && devEmoji != nil {
		for i, p := range picto {
			if p {
				devFonts[i] = devEmoji
			}
		}
	}
	spans := []render.ColorSpan{{Len: len(runes), Color: col, Bold: bold}}
	m, err := render.RasterizeFallback(c.Ren, devFonts, devEmoji, text, spans, emojiNoWrap, dev)
	if err != nil || m == nil {
		// A failed build is cached as nil too: it fails deterministically with these exact faces, and retrying every
		// frame is the same per-frame alloc as the unloaded-face case. Any face change purges the entry.
		return c.cacheEmojiRaster(key, nil)
	}
	return c.cacheEmojiRaster(key, m)
}

// cacheEmojiRaster records a build outcome for key, including nil (no emoji face yet or the build failed), so a
// degraded label costs a map hit per frame, not a rebuild. purgeEmojiCache nil-guards.
func (c *Ctx) cacheEmojiRaster(key emojiKey, m *render.MessageRaster) *render.MessageRaster {
	if c.emojiCache == nil {
		c.emojiCache = make(map[emojiKey]*render.MessageRaster, 16)
	} else if len(c.emojiCache) >= emojiRasterMax {
		c.purgeEmojiCache() // wholesale reset (like purgeTextCache); hot labels repopulate
	}
	c.emojiCache[key] = m
	return m
}

// purgeEmojiCache destroys every cached raster's textures and empties the cache. Render thread only. Called from
// purgeTextCache (a primary-font rebuild leaves the keys' pointers dead) and SetEmojiFont (an emoji-face swap leaves
// the glyphs stale).
func (c *Ctx) purgeEmojiCache() {
	for k, m := range c.emojiCache {
		if m != nil {
			m.Destroy()
		}
		delete(c.emojiCache, k)
	}
}
