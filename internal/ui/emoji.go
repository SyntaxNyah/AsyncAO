package ui

import (
	_ "embed"
	"os"
	"runtime"
)

// twemojiTTF is the bundled Twemoji (Mozilla COLRv0 build, CC-BY 4.0 — see
// fonts/TwemojiMozilla-LICENSE.txt). It's the colour-emoji fallback face used
// whenever the OS emoji font isn't renderable by SDL_ttf — notably Windows'
// 2025 Segoe UI Emoji, which became COLRv1 (SDL_ttf 2.x draws COLRv1 glyphs as
// empty boxes). COLRv0 is vector, small (~1.4 MB) and is exactly the format
// SDL_ttf renders in colour, so it also gives Linux/macOS colour emoji for free.
//
// KNOWN GAP (#35): this is mozilla/twemoji-colr v0.7.0 (2020) — the only published
// COLRv0 Twemoji TTF, and that project is dormant. It predates Unicode 14/15, so the
// NEWEST emoji tofu on a machine with no renderable OS emoji font (e.g. Windows'
// COLRv1 Segoe): the pink / grey / light-blue hearts (U+1FA75–77), a handful of
// 2021–22 faces, etc. Verified by cmap probe — the classic set (every standard heart,
// face and symbol) renders; only the post-2021 additions are missing. A full fix is a
// newer renderable emoji font: Noto Color Emoji (CBDT bitmap, SDL_ttf-renderable, full
// coverage) does it but is ~10 MB and restyles every emoji; the maintained Twemoji
// (jdecked, U16) ships SVG only, so a fresh COLRv0 build would need the nanoemoji
// pipeline. Left as a deliberate size/style decision, not a silent 7× bundle swap.
//
//go:embed fonts/TwemojiMozilla.ttf
var twemojiTTF []byte

// Color-emoji fallback face loader. SDL_ttf 2.20+ renders color emoji, but only
// from a font that HAS them — and the chat font doesn't. So the first time a
// message contains emoji, read the system emoji font off-thread (it's ~12 MB) and
// hand it to the Ctx, which segments mixed messages onto it per glyph. Lazy: a
// user who never types emoji never pays the read.

// emojiFontSystemPath is the OS colour-emoji font, preferred when SDL_ttf can
// render it. Windows ships Segoe UI Emoji; other platforms return "" so the
// bundled Twemoji is used instead (bestEmojiFont). NOTE: Windows' 2025 Segoe UI
// Emoji is COLRv1, which SDL_ttf can't render — bestEmojiFont detects that and
// falls back to Twemoji there too.
func emojiFontSystemPath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\Fonts\seguiemj.ttf`
	}
	return ""
}

// colorFontRenderable reports whether SDL_ttf (FreeType, via the version we ship)
// can actually render this font's colour glyphs: a CBDT/sbix bitmap strike or a
// COLR **version 0** table. A COLR **version 1** font (Windows' 2025 Segoe UI
// Emoji) is NOT renderable — SDL_ttf 2.x has no COLRv1 paint support, so its
// glyphs come out as empty boxes. Pure sfnt table-directory scan; unit-tested.
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

// bestEmojiFont returns the colour-emoji face bytes to install: the OS emoji font
// when SDL_ttf can render it (colorFontRenderable), otherwise the bundled Twemoji
// (COLRv0, always renderable). Run off-thread (it reads the ~12 MB system font).
func bestEmojiFont() []byte {
	if path := emojiFontSystemPath(); path != "" {
		if b, err := os.ReadFile(path); err == nil && colorFontRenderable(b) {
			return b
		}
	}
	return twemojiTTF
}

// ensureEmojiFontLoad kicks off the ONE off-thread pick of the colour-emoji face,
// the first time a message needs it. Idempotent; the bytes land on emojiFontRes,
// drained by pollEmojiFont. Always yields a renderable face now (the OS font when
// SDL_ttf can draw it, else bundled Twemoji), so emoji never tofu on a COLRv1 OS.
func (a *App) ensureEmojiFontLoad() {
	if a.emojiLoadStarted {
		return
	}
	a.emojiLoadStarted = true
	go func() {
		if b := bestEmojiFont(); len(b) > 0 {
			select {
			case a.emojiFontRes <- b:
			default:
			}
		}
	}()
}

// pollEmojiFont installs the emoji face on the render thread once its bytes land:
// pre-warm it at the current chat scale (so the first emoji doesn't stall a frame
// building a ~12 MB face mid-raster) and force a chat re-raster so a visible emoji
// message repaints in color.
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
