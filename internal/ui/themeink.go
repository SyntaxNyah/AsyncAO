package ui

// Per-element theme font COLOURS (issue #21 label 16) — the other half of what
// AO2-Client Courtroom::set_font reads out of courtroom_fonts.ini. Alongside
// "<id>" (size), "<id>_font" (family) and "<id>_bold" it reads "<id>_color" and
// stamps it on the widget as a `<class> { color: rgba(...) }` stylesheet rule
// (../AO2-Client/src/courtroom.cpp:1223-1225 and :1300). AsyncAO parsed all
// eight identifiers' colours and then used exactly two of them — showname and
// message — so a theme that coloured its OOC log or its track list was ignored.
//
// Two rules shape everything here.
//
// CANVAS ONLY. These are AO2 widgets, and AO2's widgets cannot exist outside the
// courtroom design canvas at all: set_size_and_pos hides any widget the theme is
// silent about (courtroom.cpp:1334-1338), so an off-canvas copy of ui_music_list
// is not representable there. The ink is therefore gated on a canvasInk
// PARAMETER passed down from the draw site, never a per-frame App latch — a
// torn-off tab draws its music/area list AFTER the courtroom pass (torntabs.go),
// and a latch would still read true and paint canvas ink into floating chrome.
//
// READABILITY. AO2 gets away with colours AsyncAO cannot copy verbatim, because
// its legibility for the list widgets comes from a source we deliberately do not
// paint: opaque per-ROW brushes. found_song_color / missing_song_color are
// applied with treeItem->setBackground(0, ...) at courtroom.cpp:1771/1775, and
// the seven area_*_color brushes (read at :1177-1183) at :1851-1882. Those are a
// non-goal and not out of laziness — AO2's found/missing split is
// file_exists(get_real_path(get_music_path(i_song))), a synchronous LOCAL disk
// stat PER ROW, which a streaming client cannot reproduce without violating hard
// rule 2. So a theme's black track-list ink lands on our unpainted background
// instead of AO2's pale green brush, and the same contrast guard the chatbox ink
// already uses drops it when the gap is too small.
//
// Measured on the theme this issue came from (aceattorney2x): its
// courtroombackground is luma 17 under ic_chatlog, 139 under server_chatlog and
// 44 under music_list. So server_chatlog_color = 0,0,0 is kept — a real
// readability win over the white we drew before — while music_list_color = 0,0,0
// is dropped, because black on 44 is invisible without AO2's row brush.

import (
	"fmt"
	"image"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// themeInkRect names the courtroom_design.ini rect each font element's text is
// drawn OVER, for sampling the theme art behind it. "" = there is nothing on the
// background to measure and the guard falls back to ColPanel.
//
// Indices are themeFontElem, i.e. theme.FontElements order — the same order
// TestThemeFontElemOrder pins.
var themeInkRect = [themeFontElemCount]string{
	// showname / message draw on the CHATBOX SKIN, not the courtroom background,
	// and their colours never reach this table at all: buildThemeFontTable skips
	// them (they ride a.themeMsgCol / a.themeNameCol behind the skin guard that
	// has shipped since v1.5x). Named here so the table is total.
	elemShowname: "",
	elemMessage:  "",

	elemICChatlog:     "ic_chatlog",
	elemServerChatlog: "server_chatlog",
	elemMusicList:     "music_list",
	// music_name's "Now playing" line draws inside the music panel when the theme
	// ships no music_display plate; when it DOES ship one, sampleThemeBackdrops
	// measures the plate art itself instead of this rect.
	elemMusicName: "music_list",
	// area_list is deliberately unsampled: AsyncAO draws each area as an OPAQUE
	// card (c.Fill with ColPanel / ColPanelHi / the locked and current tints), so
	// the theme's background is not behind that text — the card fill is. That fill
	// is ColPanel, which is exactly what the "" fallback measures against. This is
	// also the closest single-colour mapping of AO2's own scheme, which puts area
	// STATUS in the row BACKGROUND (courtroom.cpp:1851-1882) and never in the text.
	elemAreaList: "",
	// debug_log lives at the ms_chatlog rect — AO2's kept-for-compatibility key
	// for ui_debug_log (courtroom.cpp:831).
	elemDebugLog: "ms_chatlog",
}

// themeStemMusicPlate / themeStemCourtroomBG are the themeImageStems keys this
// file samples. Named so a rename of either table entry breaks the build here
// rather than silently turning the guard into a ColPanel comparison.
const (
	themeStemMusicPlate  = "music_display"
	themeStemCourtroomBG = "courtroombackground"
)

// sampleThemeBackdrops fills res.backdropLuma: the average luma of the theme's
// own art behind each font element. Runs on the THEME-APPLY GOROUTINE, right
// after the layout map is complete, because res.images is only reachable there
// (hard rule 2 — no decode-buffer access from the render thread). Cost is one
// strided sample per element per theme apply, the same shape as the chatbox
// skin's.
//
// MUST be called after applyAO2DefaultRects, so an element that gets its rect
// from AO2's stock defaults is measured at the place it will actually draw.
func (res *themeApply) sampleThemeBackdrops() {
	sample := func(el themeFontElem, luma int) {
		res.backdropLuma[el], res.backdropSampled[el] = luma, true
	}
	// The music plate is its own art, not a patch of the background: when the
	// theme ships one, AO2 parents ui_music_name INSIDE it (courtroom.cpp:171) and
	// so does the themed pass, which means the plate is what the line draws over.
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
	// The background is stretched to the whole "courtroom" design rect
	// (theme_layout.go's stage blit), so design pixels map to art pixels by that
	// rect's dimensions — NOT 1:1. A theme whose art is not exactly its design
	// size (AOHDUltra ships 4x art for a 1x design) would otherwise be sampled in
	// the wrong corner entirely.
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

// designRectToArt maps a courtroom_design.ini rect into the background art's own
// pixel space, given the canvas rect the art is stretched across. Integer math
// throughout: this is a sampling window, and a pixel of slop either way cannot
// change an average.
func designRectToArt(r, court theme.Rect, art image.Rectangle) image.Rectangle {
	scaleX := func(v int) int { return art.Min.X + (v-court.X)*art.Dx()/court.W }
	scaleY := func(v int) int { return art.Min.Y + (v-court.Y)*art.Dy()/court.H }
	return image.Rect(scaleX(r.X), scaleY(r.Y), scaleX(r.X+r.W), scaleY(r.Y+r.H))
}

// guardThemeInk drops per-element ink that has no contrast against what it will
// actually be drawn on, and appends a line to the theme's debug-log report
// naming every element it dropped and the luma it measured.
//
// Runs on the RENDER THREAD (from landThemeFonts) and not beside the sampling,
// because an element with no art behind it is compared against ColPanel — and
// ColPanel is only final once applyThemePalette has landed this theme's
// stylesheet, which pollThemeApply does immediately before. What is left here is
// integer arithmetic on a landed struct.
//
// On failure the element DROPS to the kit colour rather than being hue-lifted
// into readability: lifting 0,0,0 yields a hue-free grey that is neither the
// theme's intent nor the client's, and both existing guards (the chatbox skin's
// and applyThemePalette's kit-wide floor) drop for the same reason.
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

// elemInk is element el's theme-declared ink, or ok=false to keep the draw
// site's own colour. canvasInk MUST be the caller's own answer to "am I drawing
// inside the theme's design canvas": see the file header for why it cannot be
// derived here.
//
// One indexed load into the fixed table — no map, no prefs read, no pointer
// return — so the zero-alloc whole-screen gates stay green. Never take this as a
// method VALUE; that allocates a closure.
func (a *App) elemInk(el themeFontElem, canvasInk bool) (sdl.Color, bool) {
	if !canvasInk {
		return sdl.Color{}, false
	}
	f := a.themeFonts.e[el]
	return f.color, f.colorSet
}

// elemInkOr is elemInk with the fallback folded in — the shape every draw site
// actually wants, and the reason none of them repeat the two-line if.
func (a *App) elemInkOr(el themeFontElem, canvasInk bool, fallback sdl.Color) sdl.Color {
	if ink, ok := a.elemInk(el, canvasInk); ok {
		return ink
	}
	return fallback
}
