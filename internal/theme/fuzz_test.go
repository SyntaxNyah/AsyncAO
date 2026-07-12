package theme

import (
	"strings"
	"testing"
)

// FuzzParseINI drives the QSettings-style INI reader (ini.go) with hostile
// bytes: theme design/fonts/sounds INIs AND every char.ini payload flow through
// ParseINI, all fetched from untrusted origins. The contract is robustness — no
// panic, no hang (CLAUDE.md rules #4, #7); a malformed line is simply skipped.
func FuzzParseINI(f *testing.F) {
	// Real INI shapes plus degenerate lines: an unterminated "[" header, an
	// empty "[]" section name, a lone "=", an empty key "= noval", a
	// separator-less "nokey", and a mid-file section switch — each must parse or
	// skip, never crash.
	seeds := []string{
		"; comment\n# also comment\nkey = value with = sign\nbroken-line\n[Sec]\nInner=2\n",
		"message = 16\nshowname_color = 10, 20, 30\n",
		"[Options]\nname = X\n",
		"",
		"[",
		"[]",
		"=",
		"= noval",
		"nokey",
		"[a]\n[b]\nk=v",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		ini, err := ParseINI(strings.NewReader(raw))
		if err != nil {
			return // scanner errors (e.g. an over-long line) are a valid outcome
		}
		_ = ini.Len()
		_, _ = ini.Get("name")
		_ = ini.SectionKeys("options")
	})
}

// FuzzParseStylesheet drives the QSS palette extractor (stylesheet.go), which
// hand-walks the byte stream for {…} blocks and #/rgb() colours — a manual
// scanner with more panic surface than the line-based INI reader. Themes ship
// courtroom_stylesheets.css from untrusted origins. Contract: robustness only;
// ParseStylesheet returns a Palette (no error), so any input must not crash.
func FuzzParseStylesheet(f *testing.F) {
	// Real QSS plus the manual-scanner edge cases: lone braces, an unterminated
	// block, a truncated "#" colour, an unterminated comment, and an unbalanced
	// "rgb(" — the byte-walk must terminate cleanly on all of them.
	seeds := []string{
		"* { color: #fff; background-color: #202020; }",
		"QPushButton { background: rgb(10, 20, 30); border-color: red; }",
		"/* comment */ QWidget { color: rgba(1,2,3,4); }",
		"{",
		"}",
		"a {",
		"* { color: #",
		"/* unterminated",
		"* { color: rgb( }",
		"",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		_ = ParseStylesheet([]byte(raw))
	})
}
