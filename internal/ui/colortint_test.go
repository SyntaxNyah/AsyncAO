package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestChatboxTint pins #14: the blend math, that different speakers tint differently and the
// same name is stable, alpha is preserved, and the box stays dark enough to read light text.
func TestChatboxTint(t *testing.T) {
	if mixByte(0, 255, 0) != 0 || mixByte(0, 255, 100) != 255 || mixByte(10, 110, 50) != 60 {
		t.Fatal("mixByte blend wrong")
	}
	base := sdl.Color{R: 16, G: 16, B: 24, A: 215}
	phoenix := chatboxTintFor("Phoenix", base)
	edgeworth := chatboxTintFor("Edgeworth", base)
	if phoenix == edgeworth {
		t.Error("different speakers should tint to different colours")
	}
	if phoenix.A != base.A {
		t.Errorf("alpha changed: %d != %d", phoenix.A, base.A)
	}
	if phoenix.R > 110 || phoenix.G > 110 || phoenix.B > 110 {
		t.Errorf("tint too light (%v) — light text would be unreadable", phoenix)
	}
	if chatboxTintFor("Phoenix", base) != phoenix {
		t.Error("tint must be deterministic per name")
	}
}
