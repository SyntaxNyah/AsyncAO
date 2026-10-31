package ui

// Per-rune font resolution for the animated chat path: SDL_ttf reports .notdef metrics without error, so a rune the
// chat face lacks would silently draw tofu. Uses the same chain as plain messages, but with the logical faces, since
// the animated path draws 1:1 and is scaled by the renderer's SetScale.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// animFontResolver builds one message's per-rune resolver: emoji face, then covering face, else base. It returns nil
// when base covers the text and there is no emoji. Call once per message, at layout; emoji kick the off-thread load.
func (a *App) animFontResolver(base *ttf.Font, pct int, text string) render.FontResolver {
	needEmoji := render.NeedsEmojiFallback(text)
	runes := []rune(text)
	// Plain single-script text: coverRunes would return base for every rune. Mirrors renderRaster's covers() check.
	if !needEmoji && a.ctx.coversFace(base, text) {
		return nil
	}
	if needEmoji {
		a.ensureEmojiFontLoad() // kick the one off-thread system-emoji read (as renderRaster does)
	}
	// Logical faces: the animated path draws 1:1.
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
