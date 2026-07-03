package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestLogHangingIndent pins the wrap-continuation presentation (playtest: a
// wrapped message read as a run of new paragraphs): rows the WRAP created
// indent by logWrapIndentPx, rows a paragraph's own newline created do not.
// IC rows key off entry adjacency, OOC rows off the cont flags built at wrap
// time. Nil fonts take the 8 px/char fallback measure, so 160 px ≈ 20 chars.
func TestLogHangingIndent(t *testing.T) {
	a := testTabApp(t)
	a.logPct = DefaultScalePct
	a.oocPct = DefaultScalePct

	// IC: one entry long enough to wrap into several rows.
	a.icLog = append(a.icLog, icEntry{text: "this message is long enough to wrap into rows"})
	a.icLogSeq++
	rows := a.icWrapped(160, false)
	if len(rows) < 2 {
		t.Fatalf("IC fixture must wrap, got %d rows", len(rows))
	}
	if got := a.logRowIndent(logSelIC, 0); got != 0 {
		t.Errorf("IC first row indent = %d, want 0", got)
	}
	if got := a.logRowIndent(logSelIC, 1); got != logWrapIndentPx {
		t.Errorf("IC continuation indent = %d, want %d", got, logWrapIndentPx)
	}

	// OOC: a wrapping paragraph THEN an explicit newline — the wrap's rows are
	// continuations, the newline's row starts a fresh unindented paragraph.
	a.oocLog = append(a.oocLog, "a long first paragraph that will wrap across rows\nsecond para")
	a.oocSpeakers = append(a.oocSpeakers, "MOTD")
	a.oocSeq++
	lines := a.oocWrapped(160)
	if len(lines) < 3 {
		t.Fatalf("OOC fixture must wrap, got %d rows", len(lines))
	}
	if len(a.oocWrapCont) != len(lines) {
		t.Fatalf("cont flags = %d, rows = %d — must stay parallel", len(a.oocWrapCont), len(lines))
	}
	if a.oocWrapCont[0] {
		t.Error("a paragraph's first row must not be a continuation")
	}
	if !a.oocWrapCont[1] {
		t.Error("a wrapped row must be a continuation")
	}
	if last := len(lines) - 1; a.oocWrapCont[last] {
		t.Error("a newline-started paragraph must not be a continuation")
	}
	if got := a.logRowIndent(logSelOOC, 1); got != logWrapIndentPx {
		t.Errorf("OOC continuation indent = %d, want %d", got, logWrapIndentPx)
	}
}

// TestOOCHoverLinkRun pins the block link highlight (playtest: a wrapped link
// highlighted only the hovered row): hovering any row of a linked paragraph
// returns the whole contiguous same-URL run; unlinked rows and the scrollbar
// gutter return an empty range (lo > hi).
func TestOOCHoverLinkRun(t *testing.T) {
	a := testTabApp(t)
	a.oocWrapURL = []string{"", "u1", "u1", "u1", "", "u2"}
	list := sdl.Rect{X: 0, Y: 0, W: 200, H: 120}
	const lineH, wrapW = 20, 190
	c := a.ctx

	c.mouseX, c.mouseY = 10, 2*lineH+5 // row 2, mid-run of u1
	lo, hi := a.oocHoverLinkRun(list, 0, lineH, wrapW, 6)
	if lo != 1 || hi != 3 {
		t.Fatalf("run = [%d,%d], want [1,3] (the whole wrapped paragraph)", lo, hi)
	}

	c.mouseY = 5 // row 0: no link
	if lo, hi = a.oocHoverLinkRun(list, 0, lineH, wrapW, 6); hi >= lo {
		t.Fatalf("unlinked row must give an empty range, got [%d,%d]", lo, hi)
	}

	c.mouseX, c.mouseY = wrapW+2, 2*lineH+5 // over the scrollbar gutter
	if lo, hi = a.oocHoverLinkRun(list, 0, lineH, wrapW, 6); hi >= lo {
		t.Fatalf("the scrollbar gutter must give an empty range, got [%d,%d]", lo, hi)
	}

	// Scroll offset shifts which row the cursor lands on.
	c.mouseX, c.mouseY = 10, 5 // top row on screen…
	if lo, hi = a.oocHoverLinkRun(list, 5*lineH, lineH, wrapW, 6); lo != 5 || hi != 5 {
		t.Fatalf("scrolled run = [%d,%d], want [5,5] (u2)", lo, hi)
	}
}

// TestWrapEmojiAwareBreaksEmojiNames pins the IC/OOC log wrap fix: a line with wide
// colour emoji (an emoji-laden showname) must break under the emoji-AWARE measure,
// where the plain word-wrap sizes the emoji as narrow tofu and lets the line overflow
// the column (the "text is cut, not wrapping" playtest bug). Using a larger embedded
// face as the stand-in emoji font reproduces it: assignEmoji keys on the codepoint, so
// those runes take the WIDE face's metrics exactly as a real colour-emoji face would.
func TestWrapEmojiAwareBreaksEmojiNames(t *testing.T) {
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	defer ttf.Quit()
	primary, err := loadEmbeddedFont(14)
	if err != nil {
		t.Skipf("embedded font: %v", err)
	}
	defer primary.Close()
	emoji, err := loadEmbeddedFont(56) // a much wider face stands in for a colour-emoji font
	if err != nil {
		t.Skipf("embedded font: %v", err)
	}
	defer emoji.Close()

	const width, maxLines = 200, 12
	text := "💖💙🤍💜🩷 Bwuhpi: hello there, this is a fairly long message to wrap"

	plain := wrapToWidth(primary, text, width, maxLines)                  // emoji sized as narrow tofu
	aware := render.WrapEmojiAware(primary, emoji, text, width, maxLines) // emoji sized at the wide face
	if len(aware) == 0 {
		t.Fatal("emoji-aware wrap returned no lines")
	}
	if len(aware) <= len(plain) {
		t.Errorf("emoji-aware wrap = %d lines, plain = %d — expected MORE (the wide emoji force breaks)", len(aware), len(plain))
	}

	// A nil emoji face degrades to a plain single-font wrap (no panic, still wraps).
	if got := render.WrapEmojiAware(primary, nil, text, width, maxLines); len(got) == 0 {
		t.Error("emoji=nil wrap returned no lines")
	}
	// maxLines caps the output.
	if got := render.WrapEmojiAware(primary, emoji, text, width, 2); len(got) > 2 {
		t.Errorf("maxLines=2 produced %d lines", len(got))
	}
}
