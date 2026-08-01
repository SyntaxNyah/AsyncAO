package ui

import (
	"image"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// solidRGBA is a one-colour test image. Alpha 255 throughout, because the luma
// sampler composites transparency against transparentSkinLuma and a half-alpha
// fixture would make every expected value a second calculation.
func solidRGBA(w, h int, col sdl.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = col.R, col.G, col.B, 255
	}
	return img
}

// splitRGBA is white for the first splitX columns and black after — the shape
// every rect-scaling assertion below needs.
func splitRGBA(w, h, splitX int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			v := uint8(0)
			if x < splitX {
				v = 255
			}
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
		}
	}
	return img
}

func decodedOf(img *image.RGBA) *assets.Decoded {
	return &assets.Decoded{Frames: []*image.RGBA{img}}
}

// TestAvgSkinLumaRectSamplesSubRect: the rect argument has to actually bound the
// scan. If it were ignored, every element would be handed the same whole-screen
// average and the guard would be worthless — it would drop or keep all of them
// together.
func TestAvgSkinLumaRectSamplesSubRect(t *testing.T) {
	img := splitRGBA(64, 64, 32)
	if got := avgSkinLumaRect(img, image.Rect(0, 0, 32, 64), lumaSampleStep); got < 250 {
		t.Errorf("left half luma = %d, want ~255", got)
	}
	if got := avgSkinLumaRect(img, image.Rect(32, 0, 64, 64), lumaSampleStep); got > 5 {
		t.Errorf("right half luma = %d, want ~0", got)
	}
	// The whole-image wrapper must keep behaving exactly as it did before the
	// split: the shipped chatbox guard calls it and its verdicts are pinned
	// elsewhere.
	whole := avgSkinLuma(img, lumaSampleStep)
	if whole < 120 || whole > 135 {
		t.Errorf("whole-image luma = %d, want ~127 (the average of the two halves)", whole)
	}
	// A rect outside the image is empty after the intersect, not a panic and not
	// a silent whole-image read.
	if got := avgSkinLumaRect(img, image.Rect(500, 500, 600, 600), lumaSampleStep); got != transparentSkinLuma {
		t.Errorf("off-image rect luma = %d, want the transparent stand-in %d", got, transparentSkinLuma)
	}
}

// TestDesignRectToArtScales pins the design-space → art-space mapping. A theme
// whose art is not exactly its design size (4x art on a 1x design is common) has
// to be sampled where the widget really lands, not at raw design coordinates.
func TestDesignRectToArtScales(t *testing.T) {
	court := theme.Rect{X: 0, Y: 0, W: 944, H: 600}
	art := image.Rect(0, 0, 1888, 1200) // exactly 2x
	got := designRectToArt(theme.Rect{X: 10, Y: 20, W: 100, H: 50}, court, art)
	want := image.Rect(20, 40, 220, 140)
	if got != want {
		t.Errorf("designRectToArt = %v, want %v", got, want)
	}
	// A canvas rect with a non-zero origin: element coordinates are relative to
	// the canvas, so the origin has to come off before scaling.
	court = theme.Rect{X: 100, Y: 50, W: 200, H: 100}
	art = image.Rect(0, 0, 200, 100) // 1:1
	if got := designRectToArt(theme.Rect{X: 150, Y: 60, W: 10, H: 10}, court, art); got != image.Rect(50, 10, 60, 20) {
		t.Errorf("offset canvas: got %v, want (50,10)-(60,20)", got)
	}
}

// applyWithBackdrop builds the sampler's inputs: a design layout and one
// background image, sampled the way the apply goroutine does.
func applyWithBackdrop(layout map[string]theme.Rect, images map[string]*image.RGBA) *themeApply {
	res := &themeApply{layout: layout, images: map[string]*assets.Decoded{}}
	for stem, img := range images {
		res.images[stem] = decodedOf(img)
	}
	res.sampleThemeBackdrops()
	return res
}

// TestSampleThemeBackdropsPerElement: each element is measured under its OWN
// rect, through the design→art scale. RED if the scale factor is dropped (the
// element would read the wrong column of a non-1:1 background) or if the rects
// are confused for one another.
func TestSampleThemeBackdropsPerElement(t *testing.T) {
	// 1888x1200 art on a 944x600 canvas: the left 432 art px = the left 216
	// design px, which is exactly the ic_chatlog column below.
	res := applyWithBackdrop(map[string]theme.Rect{
		ao2CanvasKey:     {X: 0, Y: 0, W: 944, H: 600},
		"ic_chatlog":     {X: 0, Y: 0, W: 216, H: 300},
		"server_chatlog": {X: 700, Y: 0, W: 216, H: 300},
	}, map[string]*image.RGBA{
		themeStemCourtroomBG: splitRGBA(1888, 1200, 432),
	})
	if !res.backdropSampled[elemICChatlog] || res.backdropLuma[elemICChatlog] < 250 {
		t.Errorf("ic_chatlog backdrop = %d (sampled=%v), want ~255 — the design rect was not scaled into art space",
			res.backdropLuma[elemICChatlog], res.backdropSampled[elemICChatlog])
	}
	if !res.backdropSampled[elemServerChatlog] || res.backdropLuma[elemServerChatlog] > 5 {
		t.Errorf("server_chatlog backdrop = %d (sampled=%v), want ~0",
			res.backdropLuma[elemServerChatlog], res.backdropSampled[elemServerChatlog])
	}
	// A rect the theme never declared stays unsampled rather than reading 0.
	if res.backdropSampled[elemMusicList] {
		t.Error("music_list has no rect in this layout — it must stay unsampled, not read as black")
	}
	// area_list is deliberately never sampled: AsyncAO fills its cards opaque.
	if res.backdropSampled[elemAreaList] {
		t.Error("area_list draws on opaque cards — sampling the theme art behind them would measure the wrong thing")
	}
}

// TestSampleThemeBackdropsWithoutArt: no background means UNSAMPLED, and the
// zero value must not read as a black backdrop. Black-on-black is precisely the
// pair that would keep unreadable ink.
func TestSampleThemeBackdropsWithoutArt(t *testing.T) {
	res := applyWithBackdrop(map[string]theme.Rect{
		ao2CanvasKey: {X: 0, Y: 0, W: 944, H: 600},
		"ic_chatlog": {X: 0, Y: 0, W: 216, H: 300},
	}, nil)
	for el := themeFontElem(0); el < themeFontElemCount; el++ {
		if res.backdropSampled[el] {
			t.Errorf("element %s sampled with no theme art at all", theme.FontElements[el])
		}
	}
	// And the guard then measures against ColPanel, which is what actually gets
	// filled in that case.
	tbl := &themeFontTable{}
	tbl.e[elemICChatlog] = themeElemFont{color: sdl.Color{A: 255}, colorSet: true} // black
	guardThemeInk(res, tbl)
	if tbl.e[elemICChatlog].colorSet {
		t.Errorf("black ink kept against ColPanel (luma %d) — the unsampled fallback is wrong", colLuma(ColPanel))
	}
}

// TestSampleThemeBackdropsPrefersTheMusicPlate: music_name draws on the theme's
// music_display plate when it ships one, so the plate is what gets measured —
// not the courtroom background underneath it.
func TestSampleThemeBackdropsPrefersTheMusicPlate(t *testing.T) {
	res := applyWithBackdrop(map[string]theme.Rect{
		ao2CanvasKey: {X: 0, Y: 0, W: 100, H: 100},
		"music_list": {X: 0, Y: 0, W: 100, H: 100},
	}, map[string]*image.RGBA{
		themeStemCourtroomBG: solidRGBA(100, 100, sdl.Color{}),                            // black everywhere
		themeStemMusicPlate:  solidRGBA(20, 8, sdl.Color{R: 255, G: 255, B: 255, A: 255}), // white plate
	})
	if got := res.backdropLuma[elemMusicName]; !res.backdropSampled[elemMusicName] || got < 250 {
		t.Errorf("music_name backdrop = %d, want the PLATE's ~255 — the plate is what the line draws on", got)
	}
	if got := res.backdropLuma[elemMusicList]; !res.backdropSampled[elemMusicList] || got > 5 {
		t.Errorf("music_list backdrop = %d, want the background's ~0", got)
	}
}

// TestBuildThemeFontTableCarriesElementInk: "<id>_color" reaches the table for
// the panel elements and is deliberately SKIPPED for the two chatbox ones.
func TestBuildThemeFontTableCarriesElementInk(t *testing.T) {
	th := loadAceAttorney2x(t)
	var res themeApply
	res.buildThemeFontTable(th, nil)

	black := sdl.Color{A: 255}
	for _, tc := range []struct {
		el   themeFontElem
		want sdl.Color
	}{
		{elemServerChatlog, black},
		{elemMusicList, black},
		{elemAreaList, black},
		{elemICChatlog, sdl.Color{R: 255, G: 255, B: 255, A: 255}},
		{elemMusicName, sdl.Color{R: 255, G: 255, B: 255, A: 255}},
	} {
		got := res.fontTable.e[tc.el]
		if !got.colorSet || got.color != tc.want {
			t.Errorf("%s ink = %v (set=%v), want %v", theme.FontElements[tc.el], got.color, got.colorSet, tc.want)
		}
	}
	// showname / message ride a.themeMsgCol + a.themeNameCol behind the chatbox
	// SKIN guard. Filling them here too would run one colour through two different
	// readability guards.
	for _, el := range []themeFontElem{elemShowname, elemMessage} {
		if res.fontTable.e[el].colorSet {
			t.Errorf("%s must NOT carry table ink — its colour belongs to the chatbox path", theme.FontElements[el])
		}
	}
	// The fixture declares ms_chatlog_color but no debug_log_color, and AO2 reads
	// this widget's font from "debug_log" (courtroom.cpp:1198). ms_chatlog survives
	// only as a RECT key, so nothing may leak across.
	if res.fontTable.e[elemDebugLog].colorSet {
		t.Error("debug_log took a colour the theme never declared — ms_chatlog_color is not its key")
	}
}

// TestElemInkNeedsColorSetNotHasFont: a size-only declaration must not stamp the
// parser's white default as though the theme had asked for it. This is the exact
// mistake the chatbox path made through HasFont before declaredFontInk.
func TestElemInkNeedsColorSetNotHasFont(t *testing.T) {
	th, _ := writeThemeFonts(t, "SizeOnly", "music_list = 8\nmusic_list_bold = 1\n", nil)
	var res themeApply
	res.buildThemeFontTable(th, nil)
	got := res.fontTable.e[elemMusicList]
	if got.colorSet {
		t.Errorf("music_list ink = %v with no music_list_color declared — a size key implies no colour", got.color)
	}
	if !got.bold || got.pct == 0 {
		t.Errorf("the rest of the element must still resolve: %+v", got)
	}
}

// inkApp lands the fixture's font table on a test App with a supplied per-element
// backdrop, and returns it.
func inkApp(t *testing.T, backdrop map[themeFontElem]int) *App {
	t.Helper()
	a := testTabApp(t)
	th := loadAceAttorney2x(t)
	res := &themeApply{}
	res.buildThemeFontTable(th, nil)
	for el, luma := range backdrop {
		res.backdropLuma[el], res.backdropSampled[el] = luma, true
	}
	a.landThemeFonts(res)
	return a
}

// TestElemInkGuardKeepsHighContrast: server_chatlog_color = 0,0,0 over the
// theme's own luma-139 art is the one element where this whole item is a visible
// improvement — black on beige instead of the white we drew before. Pinned apart
// from the drop case so a guard tweak cannot quietly take it away.
func TestElemInkGuardKeepsHighContrast(t *testing.T) {
	a := inkApp(t, map[themeFontElem]int{elemServerChatlog: 139})
	ink, ok := a.elemInk(elemServerChatlog, true)
	if !ok || ink != (sdl.Color{A: 255}) {
		t.Errorf("server_chatlog ink = %v (ok=%v), want black kept over luma 139", ink, ok)
	}
}

// TestElemInkGuardDropsLowContrast: music_list_color = 0,0,0 over the theme's
// own luma-44 art is invisible. AO2 escapes it only by painting an opaque
// found_song brush per row first, which is a synchronous local disk stat per row
// and a deliberate non-goal here — so the ink drops instead.
func TestElemInkGuardDropsLowContrast(t *testing.T) {
	a := testTabApp(t)
	th := loadAceAttorney2x(t)
	res := &themeApply{}
	res.buildThemeFontTable(th, nil)
	res.backdropLuma[elemMusicList], res.backdropSampled[elemMusicList] = 44, true
	a.landThemeFonts(res)
	if _, ok := a.elemInk(elemMusicList, true); ok {
		t.Error("black music_list ink survived on luma-44 art — the track list would be unreadable")
	}
	if !strings.Contains(res.inkGuard, "music_list") || !strings.Contains(res.inkGuard, "44") {
		t.Errorf("inkGuard report = %q, want it to name music_list and the luma it measured", res.inkGuard)
	}
}

// TestElemInkOnlyOnCanvas: the classic docked panels and a torn-off tab fill
// their own ColPanel background, so ink authored for the theme's art must not
// follow them there.
func TestElemInkOnlyOnCanvas(t *testing.T) {
	a := inkApp(t, map[themeFontElem]int{elemServerChatlog: 139})
	if _, ok := a.elemInk(elemServerChatlog, false); ok {
		t.Error("canvas ink leaked to an off-canvas caller")
	}
	if got := a.elemInkOr(elemServerChatlog, false, ColText); got != ColText {
		t.Errorf("elemInkOr off-canvas = %v, want the caller's own ColText", got)
	}
}

// TestThemeFontsOffClearsInk: the opt-out zeroes the whole table, which is the
// only reason this feature needs no preference of its own.
func TestThemeFontsOffClearsInk(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetThemeFonts(false)
	th := loadAceAttorney2x(t)
	res := &themeApply{}
	res.buildThemeFontTable(th, nil)
	for el := range res.backdropLuma {
		res.backdropLuma[el], res.backdropSampled[el] = 139, true
	}
	a.landThemeFonts(res)
	for el := themeFontElem(0); el < themeFontElemCount; el++ {
		if _, ok := a.elemInk(el, true); ok {
			t.Errorf("%s still carries theme ink with the setting off", theme.FontElements[el])
		}
	}
}

// TestElemInkSurvivesAUserFontOverride: a manual font / the dyslexia toggle
// overrides the theme's FAMILIES, which is a different thing from its colours.
// Zeroing the ink there would make an accessibility font silently discard the
// theme's palette too.
func TestElemInkSurvivesAUserFontOverride(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetDyslexiaFont(true)
	th := loadAceAttorney2x(t)
	res := &themeApply{}
	res.buildThemeFontTable(th, nil)
	res.backdropLuma[elemServerChatlog], res.backdropSampled[elemServerChatlog] = 139, true
	a.landThemeFonts(res)
	if _, ok := a.elemInk(elemServerChatlog, true); !ok {
		t.Error("a user FONT override must not drop the theme's COLOURS")
	}
	if a.themeFonts.e[elemServerChatlog].face != 0 {
		t.Error("...but it must still drop the theme's face")
	}
}

// TestElemInkZeroAlloc: this runs per draw site per frame. A map probe, a
// pointer return or a captured method value would each cost an allocation on the
// render path.
func TestElemInkZeroAlloc(t *testing.T) {
	a := inkApp(t, map[themeFontElem]int{elemServerChatlog: 139})
	if n := testing.AllocsPerRun(1000, func() {
		_, _ = a.elemInk(elemICChatlog, true)
		_ = a.elemInkOr(elemServerChatlog, true, ColText)
	}); n != 0 {
		t.Errorf("elemInk allocated %v per call, want 0", n)
	}
}
