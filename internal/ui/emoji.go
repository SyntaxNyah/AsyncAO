package ui

import (
	_ "embed"
	"os"
	"runtime"
)

// twemojiTTF is the bundled Twemoji colour-emoji face (CC BY 4.0, see fonts/Twemoji-LICENSE.txt), used when the OS
// emoji font isn't renderable by SDL_ttf (Windows' Segoe UI Emoji is COLRv1, which SDL_ttf draws as empty boxes).
// It is a COLRv0 compile of jdecked/twemoji produced by scripts/build-emoji-font.ps1, with metrics pinned to
// upem 512, ascender 448, descender -64.
// A replacement MUST keep COLR version 0 (colorFontRenderable silently falls back otherwise) and the GSUB
// ligatures (Emoji 15.1 added only ZWJ sequences, which a cmap probe cannot see).
//
//go:embed fonts/Twemoji.ttf
var twemojiTTF []byte

// Colour-emoji fallback face loader: the chat font has no emoji, so the first message containing emoji reads the
// system emoji font off-thread and hands it to the Ctx, which segments mixed messages onto it per glyph.

// emojiFontSystemPath is the OS colour-emoji font, preferred when SDL_ttf can render it. Windows ships Segoe UI
// Emoji; other platforms return "" so the bundled Twemoji is used (bestEmojiFont).
func emojiFontSystemPath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\Fonts\seguiemj.ttf`
	}
	return ""
}

// colorFontRenderable reports whether SDL_ttf can render this font's colour glyphs: a CBDT/sbix bitmap strike or a
// COLR version 0 table. COLR version 1 (Windows' Segoe UI Emoji) has no paint support and draws empty boxes.
func colorFontRenderable(b []byte) bool {
	if len(b) < 12 {
		return false
	}
	num := int(b[4])<<8 | int(b[5])
	colrOff, hasBitmap := -1, false
	for i := 0; i < num; i++ {
		o := 12 + i*16
		if o+16 > len(b) {
			break
		}
		switch string(b[o : o+4]) {
		case "CBDT", "sbix":
			hasBitmap = true
		case "COLR":
			colrOff = int(b[o+8])<<24 | int(b[o+9])<<16 | int(b[o+10])<<8 | int(b[o+11])
		}
	}
	if hasBitmap {
		return true
	}
	if colrOff >= 0 && colrOff+2 <= len(b) {
		return int(b[colrOff])<<8|int(b[colrOff+1]) == 0 // COLR v0 renders; v1 doesn't
	}
	return false
}

// bestEmojiFont returns the colour-emoji face bytes to install: the OS font when colorFontRenderable, otherwise
// the bundled Twemoji. Run off-thread (it reads the ~12 MB system font).
func bestEmojiFont() []byte {
	if path := emojiFontSystemPath(); path != "" {
		if b, err := os.ReadFile(path); err == nil && colorFontRenderable(b) {
			return b
		}
	}
	return twemojiTTF
}

// ensureEmojiFontLoad starts the one off-thread pick of the colour-emoji face the first time a message needs it.
// Idempotent; the bytes land on emojiFontRes, drained by pollEmojiFont.
func (a *App) ensureEmojiFontLoad() {
	if a.emojiLoadStarted {
		return
	}
	a.emojiLoadStarted = true
	ch := a.emojiFontRes
	go func() {
		if b := bestEmojiFont(); len(b) > 0 {
			select {
			case ch <- b:
				PushWake() // an idle screen would otherwise keep its emoji boxes until the next input
			default:
			}
		}
	}()
}

// pollEmojiFont installs the emoji face on the render thread once its bytes land: it pre-warms the current chat
// scale so the first emoji doesn't stall a frame, and forces a chat re-raster.
func (a *App) pollEmojiFont() {
	select {
	case data := <-a.emojiFontRes:
		a.ctx.SetEmojiFont(data)
		a.ctx.EmojiFont(a.chatPct)        // pre-warm at the chat size
		a.ctx.EmojiFont(emojiPickerPct)   // …and the picker-grid size, so the grid paints colour at once
		a.ctx.EmojiFont(emojiBtnPct)      // …and the IC-bar button size
		a.ctx.EmojiFont(reactionFloatPct) // …and the floating-reaction size (#2)
		a.warmReactBadges()               // build the 12 reaction badges so the first float never hitches
		a.rasterText = ""                 // re-raster the visible message with the emoji face
	default:
	}
}
