package ui

import (
	"testing"
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestInlineSpanAtMapsHeadAndBody pins the per-row span mapping: the head folds
// into a leading default-colour span, then the body's style runs resolve to their
// authored colours (default → def, a palette index → that colour).
func TestInlineSpanAtMapsHeadAndBody(t *testing.T) {
	def := sdl.Color{R: 1, G: 2, B: 3, A: 255}
	styles := []courtroom.StyleRun{
		{Len: 3, Color: courtroom.ColorDefault},
		{Len: 3, Color: 2}, // green
	}
	got := inlineSpanAt(styles, 5, 0, 6, def)
	if len(got) != 3 {
		t.Fatalf("spans = %d, want 3 (head + default body + green body)", len(got))
	}
	if got[0] != (render.ColorSpan{Len: 5, Color: def}) {
		t.Errorf("head span = %+v", got[0])
	}
	if got[1] != (render.ColorSpan{Len: 3, Color: def}) {
		t.Errorf("default body span = %+v", got[1])
	}
	if got[2].Len != 3 || got[2].Color != render.TextColor(2) {
		t.Errorf("green body span = %+v", got[2])
	}
}

// TestInlineSpanAtWindowsContinuationRow pins the wrap-continuation case: a row
// that is pure body (no head) at a non-zero body offset maps to the sub-span it
// covers, not the whole run.
func TestInlineSpanAtWindowsContinuationRow(t *testing.T) {
	def := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	styles := []courtroom.StyleRun{{Len: 4, Color: 1}} // 4 green runes
	got := inlineSpanAt(styles, 0, 2, 2, def)
	if len(got) != 1 || got[0].Len != 2 || got[0].Color != render.TextColor(1) {
		t.Fatalf("window = %+v, want one 2-rune green span", got)
	}
}

// TestInlineSpanAtEmptyBodyDegrades pins the head-only / zero-body cases: they
// must never panic and must produce a usable single default span.
func TestInlineSpanAtEmptyBodyDegrades(t *testing.T) {
	def := sdl.Color{R: 9, G: 9, B: 9, A: 255}
	if got := inlineSpanAt(nil, 0, 0, 0, def); len(got) != 0 {
		t.Fatalf("nil styles = %d spans, want 0", len(got))
	}
	if got := inlineSpanAt([]courtroom.StyleRun{{Len: 1, Color: 1}}, 4, 0, 0, def); len(got) != 1 || got[0].Len != 4 {
		t.Fatalf("head-only = %+v, want one 4-rune default span", got)
	}
}

// TestICBodyStyledColorsBody pins the issue #123 core: an AO2 backtick-delimited
// span strips to clean text AND records the green colour run over the body.
func TestICBodyStyledColorsBody(t *testing.T) {
	m := &protocol.ChatMessage{CharName: "Phoenix", Message: "hello `Doc` there"}
	body, styles := icBodyStyled(m)
	if body != "hello Doc there" {
		t.Fatalf("body = %q, want %q", body, "hello Doc there")
	}
	if len(styles) != 3 {
		t.Fatalf("styles = %v, want 3 runs", styles)
	}
	if styles[1].Color != 1 || styles[1].Len != 3 {
		t.Errorf("middle run = %+v, want green (1) over 3 runes", styles[1])
	}
}

// TestICLogEntryBodyOffset pins that the body's rune offset is the head length,
// so the renderer can map the style runs onto the body slice.
func TestICLogEntryBodyOffset(t *testing.T) {
	m := &protocol.ChatMessage{CharName: "Phoenix", Message: "`Doc`"}
	line, speaker, bodyRuneStart, styles := icLogEntry(m, false, "", nil)
	if speaker != "Phoenix" || line != "Phoenix: Doc" {
		t.Fatalf("line=%q speaker=%q, want %q/%q", line, speaker, "Phoenix: Doc", "Phoenix")
	}
	if bodyRuneStart != utf8.RuneCountInString("Phoenix: ") {
		t.Errorf("bodyRuneStart = %d, want %d", bodyRuneStart, utf8.RuneCountInString("Phoenix: "))
	}
	if len(styles) != 1 || styles[0].Color != 1 || styles[0].Len != 3 {
		t.Errorf("styles = %v, want one green run of 3", styles)
	}
}
