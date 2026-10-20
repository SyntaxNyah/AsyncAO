package ui

// The join between a theme face's Qt row pitch and the size it is drawn at, and
// the gate that keeps the chatbox from growing a second opinion about it.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// newPitchCtx is a Ctx with just enough state for SetThemeFaces — no renderer and
// no face. The theme-face install is pure parsing; nothing here touches SDL.
func newPitchCtx() *Ctx {
	return &Ctx{textCache: map[textKey]cachedText{}, widthCache: map[string]int32{}}
}

// TestThemeFaceRowPitchReadsTheInstalledFace holds the index contract: the Qt
// pitch table is filled in SetThemeFaces's own loop, index-aligned with the cover
// table, so slot N's pitch always belongs to slot N's bytes.
//
// OpenDyslexic is the bundled face internal/theme's own gate measured against Qt
// 6.5.3: 43 px at a 24 px em, 73 at 40.
func TestThemeFaceRowPitchReadsTheInstalledFace(t *testing.T) {
	c := newPitchCtx()
	c.SetThemeFaces([][]byte{openDyslexicOTF})
	if got, want := c.themeFaceRowPitch(0, 24), int32(43); got != want {
		t.Errorf("slot 0 @24px = %d, want %d", got, want)
	}
	if got, want := c.themeFaceRowPitch(0, 40), int32(73); got != want {
		t.Errorf("slot 0 @40px = %d, want %d", got, want)
	}
	// The undressed element's slot index and any out-of-range one answer "no
	// opinion" rather than panicking — faceIdx() returns -1 for an element the
	// theme never dressed, and that value reaches here on every unthemed frame.
	for _, bad := range []int{-1, 1, 99} {
		if got := c.themeFaceRowPitch(bad, 24); got != 0 {
			t.Errorf("slot %d = %d, want 0", bad, got)
		}
	}
	// Clearing the theme has to clear the pitches with the bytes, or the next
	// theme's elements inherit the previous theme's leading.
	c.SetThemeFaces(nil)
	if got := c.themeFaceRowPitch(0, 24); got != 0 {
		t.Errorf("after SetThemeFaces(nil), slot 0 = %d, want 0", got)
	}
}

// TestThemeRowPitchNeedsTheElementToBeDressed pins that the pitch follows the
// ELEMENT's face binding, not merely the presence of a theme font: a theme that
// dresses only its showname must not space the message for the showname's face.
func TestThemeRowPitchNeedsTheElementToBeDressed(t *testing.T) {
	c := newPitchCtx()
	c.SetThemeFaces([][]byte{openDyslexicOTF})
	a := &App{env: env{ctx: c}}
	if got := a.themeRowPitchLogical(elemMessage, DefaultScalePct); got != 0 {
		t.Errorf("an undressed message element = %d, want 0", got)
	}
	// face is 1-BASED (0 = undressed), so slot 0 is spelled 1 here.
	a.themeFonts.e[elemShowname].face = 1
	if got := a.themeRowPitchLogical(elemMessage, DefaultScalePct); got != 0 {
		t.Errorf("dressing the SHOWNAME moved the message pitch to %d", got)
	}
	a.themeFonts.e[elemMessage].face = 1
	want := c.themeFaceRowPitch(0, faceEmPx(DefaultScalePct, DefaultScalePct))
	if got := a.themeRowPitchLogical(elemMessage, DefaultScalePct); got != want {
		t.Errorf("a dressed message element = %d, want %d", got, want)
	}
	if want == 0 {
		t.Fatal("the fixture must produce a non-zero pitch or this proves nothing")
	}
}

// TestThemeRowPitchDeviceFollowsTheDeviceScale is #77's half: the plain raster
// rasterises with the DEVICE-scaled face, so its pitch has to be measured at the
// device em or a 150% UI scale would space device-sized glyphs by logical-sized
// rows — which is precisely the shape of the defect this whole mechanism fixes.
func TestThemeRowPitchDeviceFollowsTheDeviceScale(t *testing.T) {
	c := newPitchCtx()
	c.SetThemeFaces([][]byte{openDyslexicOTF})
	a := &App{env: env{ctx: c}}
	a.themeFonts.e[elemMessage].face = 1

	c.textDevPct = DefaultScalePct
	at100 := a.themeRowPitchDevice(elemMessage, DefaultScalePct)
	c.textDevPct = 200
	at200 := a.themeRowPitchDevice(elemMessage, DefaultScalePct)
	if at100 <= 0 || at200 <= 0 {
		t.Fatalf("both device pitches must be positive, got %d and %d", at100, at200)
	}
	if at200 <= at100 {
		t.Errorf("device pitch at 200%% = %d, at 100%% = %d — it must grow with the device scale", at200, at100)
	}
	// The LOGICAL figure must NOT move with the device scale: the animated path
	// lays out on the logical face and would be spaced for a size it never opened.
	if got, want := a.themeRowPitchLogical(elemMessage, DefaultScalePct), int32(0); got == want {
		t.Fatal("fixture produced no logical pitch")
	}
	logical200 := a.themeRowPitchLogical(elemMessage, DefaultScalePct)
	c.textDevPct = DefaultScalePct
	if logical100 := a.themeRowPitchLogical(elemMessage, DefaultScalePct); logical100 != logical200 {
		t.Errorf("the logical pitch moved with the device scale: %d vs %d", logical100, logical200)
	}
}

// TestFaceEmPxMatchesCanonFontPct holds the two halves of the same arithmetic
// together. canonFontPct (chatboxfit.go) inverts buildSet's size formula to find
// the smallest percent that still opens a given pixel size; faceEmPx reproduces
// the formula forwards. If they ever drift, the Qt pitch is computed against a
// size the face was not opened at.
func TestFaceEmPxMatchesCanonFontPct(t *testing.T) {
	for _, devPct := range []int{100, 125, 150, 200} {
		for pct := 34; pct <= 600; pct += 7 {
			want := faceEmPx(pct, devPct)
			canon := canonFontPct(pct, devPct)
			if got := faceEmPx(canon, devPct); got != want {
				t.Fatalf("pct=%d dev=%d: canonFontPct gave %d, which opens %d px, not %d",
					pct, devPct, canon, got, want)
			}
		}
	}
	// buildSet's floor of 1 is reproduced, and a missing device scale defaults the
	// same way buildSet defaults it.
	if got := faceEmPx(0, 100); got != 1 {
		t.Errorf("faceEmPx(0,100) = %d, want buildSet's floor of 1", got)
	}
	if faceEmPx(DefaultScalePct, 0) != faceEmPx(DefaultScalePct, DefaultScalePct) {
		t.Error("devPct 0 must default to 100%, as buildSet does")
	}
}

// TestMessageRowPitchNeverTightensTheFaceMetric is the safety half of the
// max(): whatever the theme's OS/2 says, the row advance can never come out
// below what SDL_ttf measured for the face actually drawing.
func TestMessageRowPitchNeverTightensTheFaceMetric(t *testing.T) {
	c := newFaceCtx(t) // skips without SDL_ttf
	defer c.Destroy()
	a := &App{env: env{ctx: c}}
	face := render.FontLineSpacing(c.font)
	if face <= 0 {
		t.Skip("the embedded face reported no line spacing")
	}
	if got := a.messageRowPitchLogical(DefaultScalePct, c.font); got != face {
		t.Errorf("with no theme face the pitch must be the face's own %d, got %d", face, got)
	}
	// Install a face whose Qt pitch is far larger than the chrome face's metric
	// and bind it to the message: the answer must move to the larger number.
	c.SetThemeFaces([][]byte{openDyslexicOTF})
	a.themeFonts.e[elemMessage].face = 1
	qt := a.themeRowPitchLogical(elemMessage, DefaultScalePct)
	if qt <= face {
		t.Skipf("fixture pitch %d does not exceed the face metric %d", qt, face)
	}
	if got := a.messageRowPitchLogical(DefaultScalePct, c.font); got != qt {
		t.Errorf("pitch = %d, want the larger Qt figure %d", got, qt)
	}
}
