package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

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
