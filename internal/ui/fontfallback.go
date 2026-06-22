package ui

import (
	"os"
	"runtime"

	"golang.org/x/image/font/sfnt"
)

// scanScriptBytes does ONE byte pass over s: nonASCII is set by any byte ≥ 0x80;
// cjkMaybe by any byte ≥ 0xE3 (the UTF-8 lead bytes of U+3000+, where CJK lives —
// Cyrillic/Greek/Arabic/Tifinagh/Indic all have lower leads). No rune decode, no
// alloc: a pure-ASCII line returns both false, so the broad/CJK loads never trip and
// the single-font fast path stays untouched (the 0-perf gate). Deliberately coarse —
// over-triggering merely loads faces once off-thread; the sfnt PICK still resolves the
// text to the right face, so there's no visual change, only availability.
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

// isCJKLetter reports a Han / Kana / Hangul letter (NOT CJK punctuation, which the
// embedded font may already cover) — the runes that need the big CJK faces.
func isCJKLetter(r rune) bool {
	switch {
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

// hasCJKLetter confirms an actual CJK letter, after scanScriptBytes' cheap byte gate
// said "maybe" (so the rune decode runs only on the rare ≥0xE3 line, never on ASCII or
// the common European non-ASCII).
func hasCJKLetter(s string) bool {
	for _, r := range s {
		if isCJKLetter(r) {
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

// cjkFontList picks the CJK faces to load: the FIRST present broad-Han (+ Kana) face,
// plus Korean malgun for Hangul (the Han faces don't include it — proven by
// TestFontCoverageMatrix). One Han face (not several — they overlap and are 13-35 MB
// each) keeps the CJK tier near ~30 MB. os.Stat (disk) — called only off-thread.
func cjkFontList() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	var out []string
	for _, p := range []string{
		`C:\Windows\Fonts\msyh.ttc`,     // Microsoft YaHei — Simplified Chinese Han + Kana (most Windows)
		`C:\Windows\Fonts\YuGothR.ttc`,  // Yu Gothic — Japanese kanji + Kana (Han fallback)
		`C:\Windows\Fonts\msgothic.ttc`, // MS Gothic — older Japanese fallback
		`C:\Windows\Fonts\mingliub.ttc`, // PMingLiU — Traditional Chinese (last resort)
	} {
		if fileReadable(p) {
			out = append(out, p)
			break
		}
	}
	if fileReadable(`C:\Windows\Fonts\malgun.ttf`) {
		out = append(out, `C:\Windows\Fonts\malgun.ttf`) // Korean Hangul — not in the Han faces
	}
	return out
}

func fileReadable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// ensureCJKFontLoad reads the CJK faces ONCE, off-thread, the first time a CJK LETTER
// is drawn. Its latch (cjkLoadStarted) is INDEPENDENT of the broad-fallback latch: a
// session routinely hits a European non-ASCII name before any CJK, loading the broad
// set — if CJK detection shared that guard it would die silently after the first
// accent and never fire.
func (a *App) ensureCJKFontLoad() {
	if a.cjkLoadStarted {
		return
	}
	a.cjkLoadStarted = true
	go func() {
		var data [][]byte
		for _, p := range cjkFontList() {
			if b, err := os.ReadFile(p); err == nil && len(b) > 0 && len(b) <= fontFileMaxBytes {
				data = append(data, b)
			}
		}
		if len(data) == 0 {
			return
		}
		select {
		case a.cjkFontRes <- data:
		default:
		}
	}()
}

// pollCJKFont installs the CJK faces on the render thread once their bytes land,
// re-rastering the visible message so a previously-tofu CJK name/line repaints.
func (a *App) pollCJKFont() {
	select {
	case data := <-a.cjkFontRes:
		a.ctx.SetCJKFonts(data)
		a.rasterText = ""
	default:
	}
}
