package ui

import (
	"testing"
	"unicode/utf8"
)

// TestInsertRunesAt pins the pure splice: at the caret, clamped, never mid-rune.
func TestInsertRunesAt(t *testing.T) {
	cases := []struct{ name, s, code string; at int; want string }{
		{"middle", "hello", "X", 2, "heXllo"},
		{"start", "hello", "X", 0, "Xhello"},
		{"end", "hello", "X", 5, "helloX"},
		{"clamp-negative", "hello", "X", -1, "Xhello"},
		{"clamp-over", "hello", "X", 99, "helloX"},
	}
	for _, tc := range cases {
		if got := insertRunesAt(tc.s, tc.at, tc.code); got != tc.want {
			t.Errorf("%s: insertRunesAt(%q, %d, %q) = %q, want %q", tc.name, tc.s, tc.at, tc.code, got, tc.want)
		}
	}
}

// TestInsertICInlineAtCaret pins the caret splice: the code lands at the caret and
// the caret advances past it (issue #131).
func TestInsertICInlineAtCaret(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.icInput = "hello"
	a.ctx.focusID = icFieldID
	a.ctx.caret = 2
	a.insertICInline(`\s`)
	if a.icInput != "he\\sllo" {
		t.Fatalf("icInput = %q, want %q", a.icInput, "he\\sllo")
	}
	if a.ctx.caret != 2+utf8.RuneCountInString(`\s`) {
		t.Errorf("caret = %d, want %d", a.ctx.caret, 2+utf8.RuneCountInString(`\s`))
	}
}

// TestInsertICInlineAppendsWhenUnfocused pins the fallback: an unfocused field
// appends (the same behaviour as insertICEmoji) so the button always lands the code.
func TestInsertICInlineAppendsWhenUnfocused(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.icInput = "hi"
	a.ctx.focusID = ""
	a.insertICInline(`\f`)
	if a.icInput != "hi\\f" {
		t.Fatalf("icInput = %q, want %q", a.icInput, "hi\\f")
	}
}
