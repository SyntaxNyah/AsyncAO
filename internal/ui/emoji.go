package ui

import (
	"os"
	"runtime"
)

// Color-emoji fallback face loader. SDL_ttf 2.20+ renders color emoji, but only
// from a font that HAS them — and the chat font doesn't. So the first time a
// message contains emoji, read the system emoji font off-thread (it's ~12 MB) and
// hand it to the Ctx, which segments mixed messages onto it per glyph. Lazy: a
// user who never types emoji never pays the read.

// emojiFontSystemPath is the OS color-emoji font used as the fallback face.
// Windows ships Segoe UI Emoji (a COLR color font); other platforms return "" —
// no face loads and emoji fall back to the chat font's tofu, as before. (The
// system font was the chosen source, and this is a Windows client.)
func emojiFontSystemPath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\Fonts\seguiemj.ttf`
	}
	return ""
}

// ensureEmojiFontLoad kicks off the ONE off-thread read of the system emoji font,
// the first time a message needs it. Idempotent; the bytes land on emojiFontRes,
// drained by pollEmojiFont. No-op where there's no system emoji font.
func (a *App) ensureEmojiFontLoad() {
	if a.emojiLoadStarted {
		return
	}
	a.emojiLoadStarted = true
	path := emojiFontSystemPath()
	if path == "" {
		return
	}
	go func() {
		if b, err := os.ReadFile(path); err == nil {
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
		a.ctx.EmojiFont(a.chatPct) // pre-warm at the chat size
		a.rasterText = ""          // re-raster the visible message with the emoji face
	default:
	}
}
