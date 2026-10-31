package ui

// Per-element theme font colours: AO2's Courtroom::set_font also reads "<id>_color" (courtroom.cpp:1223-1225 and
// :1300).
// Canvas only: set_size_and_pos hides widgets the theme omits (courtroom.cpp:1334-1338), so nothing like an off-canvas
// ui_music_list exists; canvasInk is a parameter because a torn-off tab draws after the courtroom pass, and a stored
// flag would still read true there.
// Readability: AO2's list widgets rely on opaque per-row brushes (courtroom.cpp:1771/1775,
// courtroom.cpp:1851-1882) that
// need a synchronous disk stat per row, which hard rule 2 forbids, so a contrast guard drops ink that would vanish.

import (
	"fmt"
	"image"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// themeInkRect names the design rect each element's text draws over, indexed by themeFontElem;
// "" falls back to ColPanel.
var themeInkRect = [themeFontElemCount]string{
	// Drawn on the chatbox skin, and buildThemeFontTable skips their colours; named so the table is total.
	elemShowname: "",
	elemMessage:  "",

	elemICChatlog:     "ic_chatlog",
	elemServerChatlog: "server_chatlog",
	elemMusicList:     "music_list",
	// Inside the music panel without a music_display plate; with one, sampleThemeBackdrops measures the plate itself.
	elemMusicName: "music_list",
	// Unsampled: each area is an opaque ColPanel card, so "" measures the right thing.
	// AO2 likewise puts area status in the row background (courtroom.cpp:1851-1882).
	elemAreaList: "",
	// AO2's kept-for-compatibility key for ui_debug_log (courtroom.cpp:831).
	elemDebugLog: "ms_chatlog",
}

// themeStemMusicPlate and themeStemCourtroomBG are the themeImageStems keys this file samples.
const (
	themeStemMusicPlate  = "music_display"
	themeStemCourtroomBG = "courtroombackground"
)

// sampleThemeBackdrops fills res.backdropLuma on the theme-apply goroutine,
// where res.images is reachable (hard rule 2);
// must run after applyAO2DefaultRects, so an element is measured where it draws.
func (res *themeApply) sampleThemeBackdrops() {
	sample := func(el themeFontElem, luma int) {
		res.backdropLuma[el], res.backdropSampled[el] = luma, true
	}
	// AO2 parents ui_music_name inside the plate (courtroom.cpp:171), so the plate is what the line draws over.
	if plate := res.images[themeStemMusicPlate]; plate != nil && len(plate.Frames) > 0 {
		sample(elemMusicName, avgSkinLuma(plate.Frames[0], lumaSampleStep))
	}
	bg := res.images[themeStemCourtroomBG]
	if bg == nil || len(bg.Frames) == 0 {
		return
	}
	art := bg.Frames[0]
	if art.Bounds().Empty() {
		return
	}
	// The art is stretched to the "courtroom" rect, so design pixels are not 1:1 with art;
	// a 4x theme would sample wrongly.
	court, ok := res.layout[ao2CanvasKey]
	if !ok || !court.Valid() {
		return
	}
	for i, key := range themeInkRect {
		el := themeFontElem(i)
		if key == "" || res.backdropSampled[el] {
			continue
		}
		r, ok := res.layout[key]
		if !ok || !r.Valid() {
			continue
		}
		sample(el, avgSkinLumaRect(art, designRectToArt(r, court, art.Bounds()), lumaSampleStep))
	}
}

// designRectToArt maps a design rect into the art's pixels; integer slop can't change an average.
func designRectToArt(r, court theme.Rect, art image.Rectangle) image.Rectangle {
	scaleX := func(v int) int { return art.Min.X + (v-court.X)*art.Dx()/court.W }
	scaleY := func(v int) int { return art.Min.Y + (v-court.Y)*art.Dy()/court.H }
	return image.Rect(scaleX(r.X), scaleY(r.Y), scaleX(r.X+r.W), scaleY(r.Y+r.H))
}

// guardThemeInk drops ink with no contrast against its backdrop and reports it in the debug log.
// On the render thread because ColPanel is final only after applyThemePalette lands. It drops rather than lifts,
// since a lifted 0,0,0 is a grey neither the theme nor the client intended.
func guardThemeInk(res *themeApply, tbl *themeFontTable) {
	var dropped []string
	for el := range tbl.e {
		f := &tbl.e[el]
		if !f.colorSet {
			continue
		}
		back := colLuma(ColPanel)
		if res.backdropSampled[el] {
			back = res.backdropLuma[el]
		}
		if absInt(colLuma(f.color)-back) >= minInkSkinContrast {
			continue
		}
		f.colorSet = false
		dropped = append(dropped, fmt.Sprintf("%s (on luma %d)", theme.FontElements[el], back))
	}
	if len(dropped) == 0 {
		return
	}
	line := "theme " + strings.Join(dropped, ", ") + " color unreadable on the theme's own art — using client default"
	if res.inkGuard == "" {
		res.inkGuard = line
	} else {
		res.inkGuard += "; " + line
	}
}

// elemInk is el's theme ink, or ok=false for the draw site's colour; never take it as a method value, which allocates.
func (a *App) elemInk(el themeFontElem, canvasInk bool) (sdl.Color, bool) {
	if !canvasInk {
		return sdl.Color{}, false
	}
	f := a.themeFonts.e[el]
	return f.color, f.colorSet
}

// elemInkOr is elemInk with the fallback folded in.
func (a *App) elemInkOr(el themeFontElem, canvasInk bool, fallback sdl.Color) sdl.Color {
	if ink, ok := a.elemInk(el, canvasInk); ok {
		return ink
	}
	return fallback
}
