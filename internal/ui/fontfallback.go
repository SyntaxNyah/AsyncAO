package ui

import (
	"os"

	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

// scanScriptBytes does one byte pass over s: nonASCII is set by any byte >= 0x80; cjkMaybe by any byte >= 0xE3 (the
// UTF-8 lead bytes of U+3000 and up, where CJK lives; Cyrillic, Greek, Arabic and Indic have lower leads). No rune
// decode and no allocation, so a pure-ASCII line returns both false. It is coarse on purpose: over-triggering only
// loads faces once off-thread, and the sfnt pick still chooses the right face.
func scanScriptBytes(s string) (nonASCII, cjkMaybe bool) {
	for i := 0; i < len(s); i++ {
		if b := s[i]; b >= 0x80 {
			nonASCII = true
			if b >= 0xE3 {
				return true, true
			}
		}
	}
	return nonASCII, false
}

// isCJKRune reports a rune only the CJK faces draw: Han, Kana and Hangul, plus the CJK punctuation and full-width
// forms with them. 「」【】～・、。 and full-width Latin are in none of the broad fallback faces.
func isCJKRune(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x303F: // CJK Symbols and Punctuation
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // Halfwidth and Fullwidth Forms
		return true
	case r >= 0xFE30 && r <= 0xFE4F: // CJK Compatibility Forms
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
		return true
	case r >= 0x3400 && r <= 0x9FFF: // CJK Ext-A + Unified Ideographs (Han)
		return true
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return true
	case r >= 0x20000 && r <= 0x2FA1F: // CJK Ext-B and beyond (supplementary plane)
		return true
	}
	return false
}

// hasCJKRune confirms a CJK-only rune after scanScriptBytes' byte check said "maybe", so the rune decode runs only
// on the rare >= 0xE3 line.
func hasCJKRune(s string) bool {
	for _, r := range s {
		if isCJKRune(r) {
			return true
		}
	}
	return false
}

// parseCover parses font bytes for the cmap-based coverage check used by the pick. SDL_ttf's GlyphMetrics returns
// .notdef metrics without error for missing glyphs in this build, so it can't tell coverage; sfnt's cmap lookup
// (GlyphIndex != 0) can. Handles single fonts (.ttf) and collections (.ttc, e.g. Nirmala) via the first face. nil
// on parse failure means that face is never chosen by coverage.
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

// Broad-Unicode text fallback fonts. The embedded chat font (goregular) covers only Latin-ish ranges, so Cyrillic,
// Greek, symbols and other scripts rendered as tofu. These system fonts are read off-thread at launch and appended
// after the embedded font, so Latin keeps the embedded look and only runes goregular lacks fall through to a
// covering face. This is whole-message script coverage via pickFont's "first chain font that covers every rune"
// rule, separate from the per-glyph colour-emoji fallback (seguiemj).

// ensureFallbackFontLoad reads the present system fallback fonts once, off-thread, landing their bytes on
// fallbackFontRes. Idempotent (fallbackLoadStarted); eager rather than lazy like the emoji face, since script gaps
// are common.
func (a *App) ensureFallbackFontLoad() {
	if a.fallbackLoadStarted {
		return
	}
	a.fallbackLoadStarted = true
	cands := platform.SystemFonts().Fallback
	if len(cands) == 0 {
		return
	}
	go loadFontFiles(cands, a.fallbackFontRes)
}

// fontWorkPending reports font loading the next frame has to act on: a request a label raised while drawing, runes
// queued for the font scan, or a finished load waiting to be installed. SkipFrame renders a static screen only for a
// reason, and a wake event alone is not one. Cheap: field reads and channel lengths.
func (a *App) fontWorkPending() bool {
	if c := a.ctx; c != nil && (c.wantFallback || c.wantCJK || c.missingN > 0) {
		return true
	}
	return len(a.fallbackFontRes) > 0 || len(a.cjkFontRes) > 0 || len(a.emojiFontRes) > 0 || len(a.censusRes) > 0
}

// loadFontFiles reads the font files that exist, off the render thread, hands their bytes to ch and wakes the
// render loop; an idle screen draws no frame of its own, so without the wake the faces would sit in ch until input.
func loadFontFiles(paths []string, ch chan<- [][]byte) {
	var data [][]byte
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 && len(b) <= fontFileMaxBytes {
			data = append(data, b)
		}
	}
	if len(data) == 0 {
		return
	}
	select {
	case ch <- data:
		PushWake()
	default:
	}
}

// pollFallbackFont installs the broad fallback faces on the render thread once their bytes land, forcing a chat
// re-raster so a visible message with tofu runes repaints.
func (a *App) pollFallbackFont() {
	select {
	case data := <-a.fallbackFontRes:
		a.ctx.SetFallbackFonts(data)
		a.rasterText = "" // re-raster the visible message with the broader coverage
	default:
	}
}

// firstReadable returns the first path in cands that exists and is a readable file, or "". Split out so cjkFontList's
// ordering is testable with an injected predicate (firstReadableFunc).
func firstReadable(cands []string) string {
	return firstReadableFunc(cands, fileReadable)
}

// firstReadableFunc is firstReadable with an injectable existence predicate.
func firstReadableFunc(cands []string, exists func(string) bool) string {
	for _, p := range cands {
		if exists(p) {
			return p
		}
	}
	return ""
}

// cjkFontList picks the CJK faces to load: the first present Han (+ Kana) face by the Japanese-first order, then a
// distinct Chinese Han face as a CN-coverage backstop, then the first present Korean face. At most two Han faces
// keeps the CJK tier near 50 MB, loaded lazily on a CJK line. os.Stat (disk), so off-thread only.
func cjkFontList() []string {
	fonts := platform.SystemFonts()
	var out []string
	han := firstReadable(fonts.CJKHan)
	if han != "" {
		out = append(out, han)
	}
	// Chinese backstop for the Simplified-only Han Yu Gothic's cmap lacks, unless the Japanese pick is already a
	// Chinese face.
	if cn := firstReadable(fonts.CJKChineseBackstop); cn != "" && cn != han {
		out = append(out, cn)
	}
	if kr := firstReadable(fonts.CJKHangul); kr != "" {
		out = append(out, kr) // Korean Hangul — not in the Han faces
	}
	return out
}

func fileReadable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// ensureCJKFontLoad reads the CJK faces once, off-thread, the first time a CJK rune is drawn. Its guard
// (cjkLoadStarted) is separate from the broad-fallback one: a session usually hits a European non-ASCII name before
// any CJK, and sharing the guard would stop CJK from ever loading.
func (a *App) ensureCJKFontLoad() {
	if a.cjkLoadStarted {
		return
	}
	a.cjkLoadStarted = true
	ch := a.cjkFontRes
	go func() { loadFontFiles(cjkFontList(), ch) }() // cjkFontList stats files, so it runs off-thread too
}

// pollCJKFont installs the CJK faces on the render thread once their bytes land, re-rastering the visible message.
func (a *App) pollCJKFont() {
	select {
	case data := <-a.cjkFontRes:
		a.ctx.SetCJKFonts(data)
		a.rasterText = ""
	default:
	}
}
