package ui

import "unicode/utf8"

// Inline AO markup, made reachable without memorising the code language (#131).
// The chatbox already renders every one of these codes (typewriter.go); this is
// the INPUT side — buttons that insert the code at the caret so a player can drop
// a shake / flash / pause / speed change mid-sentence the way they'd pick a colour.

// insertRunesAt returns s with code spliced in at rune offset at (clamped). Pure —
// no SDL — so the splice is drivable in a test.
func insertRunesAt(s string, at int, code string) string {
	rs := []rune(s)
	if at < 0 {
		at = 0
	}
	if at > len(rs) {
		at = len(rs)
	}
	out := make([]rune, 0, len(rs)+utf8.RuneCountInString(code))
	out = append(out, rs[:at]...)
	out = append(out, []rune(code)...)
	out = append(out, rs[at:]...)
	return string(out)
}

// insertICInline inserts code into the IC message at the caret, keeping focus and
// advancing the caret past the inserted runes. When the IC field isn't focused
// (the picker stole focus) it falls back to appending — the same behaviour
// insertICEmoji uses — so the button always lands the code in the line.
func (a *App) insertICInline(code string) {
	c := a.ctx
	rc := utf8.RuneCountInString(a.icInput)
	if c.focusID != icFieldID || c.caret < 0 || c.caret > rc {
		a.icInput += code
		a.ctx.FocusField(icFieldID)
		return
	}
	a.icInput = insertRunesAt(a.icInput, c.caret, code)
	c.selAnchor = -1          // the spliced code is not selected; the caret follows it
	c.caret += utf8.RuneCountInString(code)
}
