package ui

// Task E — per-rune font resolution for the animated (#M5 effects) chat path. An effects
// message renders through the per-glyph AnimatedText path (shake/wave/rainbow/…), which used
// to lay out EVERY rune with one chat face. SDL_ttf reports .notdef metrics WITHOUT error, so
// a rune the chat face lacks silently produced tofu (emoji) or a uniform box advance (CJK) —
// while PLAIN messages resolved a covering face per rune. This file builds the SAME per-rune
// chain the plain fallback path uses (screens.go renderRaster → deviceCoverRunes / emoji.go
// assignEmoji) and hands it to render.RasterizeAnimated as a FontResolver, so an effects line
// becomes pixel-consistent with a plain line.
//
// The chain is precomputed ONCE per message (layout time) — the closure it returns is a pair
// of index reads, so RasterizeAnimated's per-rune calls stay allocation-light, and Draw never
// touches it (the resolved face is stored on each animRune). The animated path stays on the
// LOGICAL faces (the #77 PUNT: its per-glyph pen positions + pixel-amplitude effects aren't
// device-scale-folded yet, so it draws 1:1 and the renderer's SetScale stretches it), so this
// resolver uses the logical coverRunes / EmojiFont — matching the logical base face that
// renderAnimated passes as the fallback.

import (
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// animFontResolver builds the per-rune font resolver for one effects message: the colour-emoji
// face for emoji runes (assignEmoji), the covering CJK/broad face for a script the chat font
// lacks (coverRunes), else base. Returns nil (RasterizeAnimated then uses base for every rune,
// the pre-fallback behaviour) when the message is plain ASCII / single-script with no emoji, so
// the overwhelming common case pays nothing extra. When emoji are present it kicks the one
// off-thread colour-emoji load, exactly like the plain renderRaster path. Layout thread; call
// once per message. base is the logical ChatFontFor face (the resolver's fallback + the glyph
// cache's key partner).
func (a *App) animFontResolver(base *ttf.Font, pct int, text string) render.FontResolver {
	needEmoji := render.NeedsEmojiFallback(text)
	runes := []rune(text)
	// Cheap gate: a plain single-script message with no emoji needs no per-rune resolution —
	// coverRunes would return base for every rune anyway, so skip the whole build and let
	// RasterizeAnimated take its single-font path. This mirrors renderRaster's covers() gate.
	if !needEmoji && a.ctx.covers(text) {
		return nil
	}
	if needEmoji {
		a.ensureEmojiFontLoad() // kick the one off-thread system-emoji read (as renderRaster does)
	}
	// Per-rune covering text faces + the emoji mask + the emoji face — the SAME inputs the
	// plain RasterizeFallback path builds (emoji.go), so an effects line resolves identically.
	// Logical faces (see the file header): the animated path draws 1:1.
	textFonts := a.ctx.coverRunes(base, runes)
	mask := render.AssignEmoji(runes)
	emojiFont := a.ctx.EmojiFont(pct)
	return func(gi int, _ rune) (*ttf.Font, bool) {
		if gi < 0 || gi >= len(runes) {
			return base, false // out of range (shouldn't happen: gi indexes []rune(text)) — safe base
		}
		if mask[gi] && emojiFont != nil {
			return emojiFont, true // colour-emoji glyph: drawn tint-free (render.Draw skips the recolour)
		}
		return textFonts[gi], false // covering text face (base when nothing else covers it)
	}
}
