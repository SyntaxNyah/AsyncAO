package ui

import (
	"testing"

	"golang.org/x/image/font/sfnt"
)

// TestBundledEmojiCommonCoverage pins that the bundled colour-emoji font stays an
// SDL_ttf-renderable COLRv0 face that covers the COMMON emoji set (#35). It guards
// against a bad font swap that would tofu everyday emoji. The post-2021 Unicode 14/15
// additions (pink/grey/light-blue hearts, etc.) are a KNOWN gap in this 2020 Twemoji
// build — see twemojiTTF's doc comment — and are deliberately not asserted here.
func TestBundledEmojiCommonCoverage(t *testing.T) {
	if !colorFontRenderable(twemojiTTF) {
		t.Fatal("bundled emoji font is not SDL_ttf-renderable (expected a COLRv0 face)")
	}
	f, err := sfnt.Parse(twemojiTTF)
	if err != nil {
		t.Fatalf("parse bundled emoji font: %v", err)
	}
	var buf sfnt.Buffer
	for _, r := range []rune{
		0x2764,  // red heart
		0x1F499, // blue heart
		0x1F90D, // white heart
		0x1F497, // growing heart
		0x1F600, // grinning face
		0x1F44D, // thumbs up
	} {
		if idx, err := f.GlyphIndex(&buf, r); err != nil || idx == 0 {
			t.Errorf("bundled emoji font missing common glyph U+%04X (idx=%d err=%v)", r, idx, err)
		}
	}
}
