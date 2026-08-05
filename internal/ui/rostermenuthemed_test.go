package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/veandco/go-sdl2/sdl"
)

// The roster shares the music panel, so the ⋮ it opens is the music overflow menu,
// and one of that menu's rows is the Volume-sliders view. Under a theme the canvas
// music list DISCARDS that view outright (`volMode := a.musicVolMode && !themed`,
// drawMusicList) while the toggle still persists through SetMusicVolMode — so a
// roster menu opened with a hardcoded themed=false offers a row that draws nothing
// now and silently arms a view the user never asked for in their next un-themed
// layout. That is the ghost/inert-control class this package pins by name, and it is
// what musicMenuRowLive's `!themed` guard exists to prevent.
//
// These tests pin the derived answer (rosterMenuThemed) and the fact that the draw
// site still uses it.

// newRosterMenuRig is the minimal App this predicate needs: prefs on a temp file and
// nothing else. rosterMenuThemed reads only the layout cache, the panel-hidden set
// and one preference, so no renderer is required — it runs on a headless box.
func newRosterMenuRig(t *testing.T) (*App, *config.AssetPreferences) {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	return &App{d: Deps{Prefs: prefs}}, prefs
}

// themedMusicListRect is aceattorney2x's own music_list declaration — the rect the
// bug was reported against (216 px wide, which is also why the toolbar wraps there).
var themedMusicListRect = sdl.Rect{X: 0, Y: 323, W: 216, H: 277}

// TestRosterMenuThemedTracksTheCanvasMusicList walks every state the roster's ⋮ can
// be opened in. The answer it must give is "is a THEMED music list the list this
// menu's rows act on", because that is exactly when the Volume row is inert.
func TestRosterMenuThemedTracksTheCanvasMusicList(t *testing.T) {
	a, prefs := newRosterMenuRig(t)

	// Classic geometry: no theme layout cached at all. The docked Players tab and any
	// torn-off panel are chrome, and the classic Music tab honours the volume view.
	if a.rosterMenuThemed() {
		t.Error("no themed layout is cached, so the roster's menu is chrome — the volume " +
			"row works from the classic Music tab and must be offered")
	}

	// A cached layout the user has switched OFF is still classic geometry: screens.go
	// dispatches on lay.valid AND the preference, and so must this.
	a.themeLay.valid = true
	a.themeLay.r = map[string]sdl.Rect{"music_list": themedMusicListRect}
	prefs.SetThemeLayout(false)
	if a.rosterMenuThemed() {
		t.Error("theme-driven geometry is switched off, so the CLASSIC courtroom drew — " +
			"reading the cache alone would hide a row that still works")
	}

	// Theme-driven geometry with the theme's own music_list rect: this roster IS being
	// painted inside AO2's design canvas, where the volume view can never draw.
	prefs.SetThemeLayout(true)
	if !a.rosterMenuThemed() {
		t.Error("the themed courtroom paints the roster inside its music_list rect, so the " +
			"menu must be themed — else it offers a Volume row drawMusicList discards")
	}

	// The whole themed list panel is gated on the log region being visible
	// (theme_layout.go). Hidden, there is no canvas music list on screen and the only
	// music list left is a torn-off one, which honours the volume view.
	a.hidden = map[string]bool{panelLog: true}
	if a.rosterMenuThemed() {
		t.Error("the log region is hidden, so the themed panel that hosts the music list " +
			"is not drawn at all")
	}
	a.hidden = nil

	// A theme that declares no music_list has no canvas music list to be inside, the
	// same gate theme_layout.go opens the shared panel behind.
	a.themeLay.r = map[string]sdl.Rect{"ic_chatlog": {X: 0, Y: 0, W: 200, H: 100}}
	if a.rosterMenuThemed() {
		t.Error("this theme declares no music_list rect, so no canvas music list exists")
	}

	// A declared-but-degenerate rect is not a rect (themeLayoutCache.rect drops
	// zero-area keys), so it must not read as a canvas either.
	a.themeLay.r = map[string]sdl.Rect{"music_list": {X: 0, Y: 323, W: 0, H: 277}}
	if a.rosterMenuThemed() {
		t.Error("a zero-width music_list paints nothing — themeLayoutCache.rect already " +
			"rejects it and this predicate must agree with it")
	}
}

// TestRosterMenuOpensWithTheDerivedThemedFlag is the draw-site half: the predicate
// above is worthless if drawPlayerList goes back to passing a literal.
//
// Source-scanned rather than driven, because reaching the call needs a renderer, a
// laid-out toolbar and a click — and the property is about the ARGUMENT, which a
// scan pins directly. Same idiom as TestThemedMusicPanelForcesTheTrackList.
func TestRosterMenuOpensWithTheDerivedThemedFlag(t *testing.T) {
	src, err := os.ReadFile("playerlist.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "a.openMusicMenuFromButton(sdl.Point{X: it.r.X, Y: it.r.Y + it.r.H}, a.rosterMenuThemed())") {
		t.Error("the roster's ⋮ no longer opens the overflow with the DERIVED themed flag; " +
			"a hardcoded false offers the Volume-sliders row inside the theme's music_list " +
			"rect, where drawMusicList discards it and SetMusicVolMode still persists it")
	}
	// The menu's own gate has to stay the thing that reads it, or the flag is inert.
	menu, err := os.ReadFile("musicheader.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(menu), "if k == musicMenuVolumeView {\n\t\treturn !themed\n\t}") {
		t.Error("musicMenuRowLive no longer drops the Volume row under a theme, so the " +
			"roster's derived flag has nothing to act on")
	}
}
