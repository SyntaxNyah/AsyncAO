package ui

import (
	"os"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// TestICColorWheelCaptionFits pins §3.4: the free-hex chat-colour picker's
// heading must fit inside the picker's fixed-width floating panel at the
// default chrome font. Before the fix the caption was drawn with the
// unclipped c.Label and overflowed the ~340px interior by ~110px, painting
// past the panel border onto the live scene behind the popup. The draw now
// clips as a hard guard AND the wording is trimmed to fit; this test measures
// the exact constant the draw call uses (icColorWheelCaption) against the same
// interior width (r.W - 16, the 8px side margins), so a future wording edit
// that reopens the overflow fails here rather than silently on-screen.
func TestICColorWheelCaptionFits(t *testing.T) {
	os.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	defer ttf.Quit()

	font, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Fatalf("embedded font: %v", err)
	}
	c := &Ctx{font: font, textCache: map[textKey]cachedText{}, widthCache: map[string]int32{}}

	// The panel width is fixed (icColorWheelRect.W == pw) regardless of the
	// swatch anchor, so a zero-value App and any window size measure the same
	// interior. The label draws at r.X+8 with a matching 8px right margin.
	var a App
	const labelSideMargin = 16 // 8px left + 8px right, matching the r.X+8 draw offset
	interior := a.icColorWheelRect(1280, 720).W - labelSideMargin

	if got := c.TextWidth(icColorWheelCaption); got > interior {
		t.Errorf("caption %q measures %dpx at the default font, wider than the %dpx panel interior — it will paint past the border",
			icColorWheelCaption, got, interior)
	}
}
