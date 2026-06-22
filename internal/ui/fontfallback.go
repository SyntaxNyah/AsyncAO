package ui

import (
	"os"
	"runtime"

	"golang.org/x/image/font/sfnt"
)

// hasNonASCII reports whether s has any byte ≥ 0x80, i.e. any non-ASCII rune. This is
// the cheap (no decode, no alloc) LOAD trigger: a pure-ASCII session never trips it, so
// the broad fallback never loads and the single-font fast path stays untouched. It is
// deliberately coarse — over-triggering on a covered rune (é, Cyrillic) merely loads
// the faces once off-thread; the sfnt PICK then still resolves such text to the
// embedded font, so there's no visual change, only availability.
func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

// parseCover parses font bytes for the cmap-based coverage check used by the PICK.
// SDL_ttf's GlyphMetrics returns .notdef metrics WITHOUT error for missing glyphs in
// this build, so it can't tell coverage — sfnt's cmap lookup (GlyphIndex != 0) can.
// Handles both single fonts (.ttf) and collections (.ttc, e.g. Nirmala) by trying the
// first face. nil on parse failure → that face is simply never chosen by coverage.
func parseCover(data []byte) *sfnt.Font {
	if f, err := sfnt.Parse(data); err == nil {
		return f
	}
	if c, err := sfnt.ParseCollection(data); err == nil && c.NumFonts() > 0 {
		if f, err := c.Font(0); err == nil {
			return f
		}
	}
	return nil
}

// Broad-Unicode TEXT fallback fonts. The embedded chat font (goregular) only covers
// Latin-ish ranges, and the override chain is empty by default — so Cyrillic, Greek,
// symbols and other scripts in shownames / messages rendered as tofu ("□□"). These
// system fonts are read off-thread at launch and appended AFTER the embedded font, so
// Latin keeps the embedded look and only the runes goregular LACKS fall through to a
// covering face. Separate from the colour-EMOJI fallback (seguiemj), which is a
// per-glyph colour path; this is whole-message script coverage via the existing
// pickFont "first chain font that covers every rune" rule.

// fallbackFontCandidates lists the system fonts to try, broadest first. Windows ships
// Segoe UI (Latin / Cyrillic / Greek / Armenian / Hebrew / Arabic / Thai / many
// symbols — always present) and, where installed, Microsoft YaHei (CJK). Other OSes
// return none (this is a Windows client), so the embedded font stays the only face,
// exactly as before.
func fallbackFontCandidates() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	return []string{
		`C:\Windows\Fonts\segoeui.ttf`,  // broad Latin / Cyrillic / Greek / Hebrew / Arabic / Thai
		`C:\Windows\Fonts\seguisym.ttf`, // Segoe UI Symbol — arrows / math / technical / dingbats
		`C:\Windows\Fonts\ebrima.ttf`,   // Ebrima — African scripts incl. Tifinagh (the reported gap)
		`C:\Windows\Fonts\Nirmala.ttc`,  // Nirmala UI (a .ttc collection) — Indic (Devanagari, Tamil, ...)
	}
}

// ensureFallbackFontLoad reads the present system fallback fonts ONCE, off-thread,
// landing their bytes on fallbackFontRes. Idempotent (gated by fallbackLoadStarted) —
// called each frame, kicks the read on the first. Eager (not lazy like the emoji
// face): script gaps are common, so we want coverage from the start.
func (a *App) ensureFallbackFontLoad() {
	if a.fallbackLoadStarted {
		return
	}
	a.fallbackLoadStarted = true
	cands := fallbackFontCandidates()
	if len(cands) == 0 {
		return
	}
	go func() {
		var data [][]byte
		for _, p := range cands {
			if b, err := os.ReadFile(p); err == nil && len(b) > 0 && len(b) <= fontFileMaxBytes {
				data = append(data, b)
			}
		}
		if len(data) == 0 {
			return
		}
		select {
		case a.fallbackFontRes <- data:
		default:
		}
	}()
}

// pollFallbackFont installs the broad fallback faces on the render thread once their
// bytes land, forcing a chat re-raster so a visible message with previously-tofu runes
// repaints with coverage.
func (a *App) pollFallbackFont() {
	select {
	case data := <-a.fallbackFontRes:
		a.ctx.SetFallbackFonts(data)
		a.rasterText = "" // re-raster the visible message with the broader coverage
	default:
	}
}
