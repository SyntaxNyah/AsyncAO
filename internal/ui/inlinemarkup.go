package ui

import (
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

// Inline AO markup, made reachable without memorising the code language. The chatbox already renders these codes
// (typewriter.go); this is the input side: buttons that insert the code at the caret.

// insertRunesAt returns s with code spliced in at rune offset at (clamped). Pure, so a test can drive it.
func insertRunesAt(s string, at int, code string) string {
	rs := []rune(s)
	at = value.Clamp(at, 0, len(rs))
	out := make([]rune, 0, len(rs)+utf8.RuneCountInString(code))
	out = append(out, rs[:at]...)
	out = append(out, []rune(code)...)
	out = append(out, rs[at:]...)
	return string(out)
}

// insertICInline inserts code into the IC message at the caret, keeping focus and advancing the caret past the
// inserted runes. If the IC field isn't focused (the picker took focus) it appends, as insertICEmoji does.
func (a *App) insertICInline(code string) {
	c := a.ctx
	rc := utf8.RuneCountInString(a.icInput)
	if c.focusID != icFieldID || c.caret < 0 || c.caret > rc {
		a.icInput += code
		a.ctx.FocusField(icFieldID)
		return
	}
	a.icInput = insertRunesAt(a.icInput, c.caret, code)
	c.selAnchor = -1 // the spliced code is not selected; the caret follows it
	c.caret += utf8.RuneCountInString(code)
}
