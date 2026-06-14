package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestInputSnapshotOrder pins the Ctx frame contract main.go's loop relies
// on: BeginFrame opens (and clears) the input snapshot, HandleEvent then
// fills it, and widgets read it during the draw pass. The inverse order
// shipped once — events fed before BeginFrame — and the reset erased every
// click/keypress/wheel tick before any widget saw it, leaving the whole UI
// dead. No SDL init is needed: events are plain structs and GetMouseState
// reads SDL's static mouse state.
func TestInputSnapshotOrder(t *testing.T) {
	c := &Ctx{}

	c.BeginFrame(time.Millisecond)
	c.HandleEvent(&sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, X: 5, Y: 6})
	if c.mouseX != 5 || c.mouseY != 6 {
		t.Fatalf("motion event must refresh the cursor snapshot, got (%d,%d)", c.mouseX, c.mouseY)
	}
	c.HandleEvent(&sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONUP, Button: sdl.BUTTON_LEFT, X: 40, Y: 20})
	if !c.clicked {
		t.Fatal("MOUSEBUTTONUP after BeginFrame must leave clicked set for the draw pass")
	}
	if c.mouseX != 40 || c.mouseY != 20 {
		t.Fatalf("button event must move the cursor snapshot to the release point, got (%d,%d)", c.mouseX, c.mouseY)
	}
	if !c.hovering(sdl.Rect{X: 30, Y: 10, W: 20, H: 20}) {
		t.Fatal("hovering must hit-test against the release coordinates")
	}
	c.HandleEvent(&sdl.MouseWheelEvent{Type: sdl.MOUSEWHEEL, Y: 3})
	if c.wheelY != 3 {
		t.Fatalf("wheel ticks must accumulate, got %d", c.wheelY)
	}

	// The next BeginFrame consumes the frame's input.
	c.BeginFrame(time.Millisecond)
	if c.clicked || c.wheelY != 0 {
		t.Fatal("BeginFrame must clear the previous frame's input snapshot")
	}
}

// TestParseIniswapList pins the iniswap.txt contract: one folder name per
// line, CRLF tolerated, blanks skipped, case-insensitive dedupe, bounded
// by iniswapListCap, sorted case-insensitively for the menu.
func TestParseIniswapList(t *testing.T) {
	data := []byte("zeta\r\n\r\n  aigis  \r\namong us_red\nAigis\namong us_red\n\n")
	got := parseIniswapList(data)
	want := []string{"aigis", "among us_red", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parsed %v, want %v", got, want)
		}
	}

	// Cap holds against a hostile file.
	var huge []byte
	for i := 0; i < iniswapListCap+500; i++ {
		huge = append(huge, []byte(fmt.Sprintf("char%06d\n", i))...)
	}
	if got := parseIniswapList(huge); len(got) != iniswapListCap {
		t.Fatalf("cap = %d entries, want %d", len(got), iniswapListCap)
	}
}

// TestParseAutoindexDirs pins the background-listing parser: trailing-slash
// hrefs only (folders), parent/self/absolute/external/sort links skipped,
// files skipped, percent-decoded, case-insensitively de-duplicated, sorted.
func TestParseAutoindexDirs(t *testing.T) {
	// nginx autoindex (the shape miku.pizza actually serves) plus a few
	// Apache/Caddy quirks mixed in.
	page := []byte(`<html><head><title>Index of /base/background/</title></head><body>
<h1>Index of /base/background/</h1><hr><pre><a href="../">../</a>
<a href="09-houses/">09-houses/</a>                 05-Apr-2026 12:59    -
<a href="999apartment/">999apartment/</a>           10-Mar-2026 09:38    -
<a href="my%20court/">my court/</a>                  10-Mar-2026 09:38    -
<a href="readme.txt">readme.txt</a>                  10-Mar-2026 09:38  194
<a href="999apartment/">999apartment/</a>
<a href="?C=N;O=D">Name</a>
<a href="/base/">absolute</a>
<a href="https://evil.example/x/">external</a>
<a href="nested/sub/">nested</a>
</pre><hr></body></html>`)
	got := parseAutoindexDirs(page)
	want := []string{"09-houses", "999apartment", "my court"}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parsed %v, want %v", got, want)
		}
	}

	// A non-autoindex response (custom error page) yields no names, never
	// garbage.
	if got := parseAutoindexDirs([]byte(`<html><body><h1>403 Forbidden</h1></body></html>`)); len(got) != 0 {
		t.Fatalf("non-listing parsed to %v, want empty", got)
	}

	// Cap holds against a hostile listing.
	var huge []byte
	for i := 0; i < bgListCap+500; i++ {
		huge = append(huge, []byte(fmt.Sprintf(`<a href="bg%06d/">x</a>`+"\n", i))...)
	}
	if got := parseAutoindexDirs(huge); len(got) != bgListCap {
		t.Fatalf("cap = %d entries, want %d", len(got), bgListCap)
	}
}

// TestCleanAutoindexEntry pins the downloader's security boundary: parent,
// self, absolute, external, nested and ".." links (raw OR percent-encoded)
// are all rejected, so a hostile listing can never write outside the dest.
func TestCleanAutoindexEntry(t *testing.T) {
	reject := []string{
		"../", "./", "..", "%2e%2e/", "%2e%2e%2f", "a/..%2f", // traversal
		"/abs/", "/etc/passwd", // absolute
		"https://evil.example/x/", "http://x/y", // external
		"a/b", "a/b/", `a\b`, // nested / separators
		"", "%2e%2e", // empty / bare encoded ..
	}
	for _, raw := range reject {
		if e, ok := cleanAutoindexEntry(raw); ok {
			t.Errorf("cleanAutoindexEntry(%q) accepted as %+v, want rejected", raw, e)
		}
	}

	accept := map[string]autoindexEntry{
		"maya/":         {href: "maya/", name: "maya", dir: true},
		"char_icon.png": {href: "char_icon.png", name: "char_icon.png", dir: false},
		"my%20court/":   {href: "my%20court/", name: "my court", dir: true},
	}
	for raw, want := range accept {
		got, ok := cleanAutoindexEntry(raw)
		if !ok || got != want {
			t.Errorf("cleanAutoindexEntry(%q) = %+v, %v; want %+v, true", raw, got, ok, want)
		}
	}
}

// TestSelectAllChordArms pins Ctrl+A semantics: the chord arms a pending
// select-all that survives BeginFrame until a field consumes it (typing
// replaces the whole value, backspace clears it — handled in TextField).
func TestSelectAllChordArms(t *testing.T) {
	c := &Ctx{}
	c.BeginFrame(time.Millisecond)
	c.HandleEvent(&sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Sym: sdl.K_a, Mod: sdl.KMOD_LCTRL}})
	if !c.selectAll {
		t.Fatal("Ctrl+A must arm select-all")
	}
	c.BeginFrame(time.Millisecond)
	if !c.selectAll {
		t.Fatal("select-all must persist across frames until consumed")
	}
}

// TestMergeWardrobe pins the wardrobe-first menu: client favourites sort
// case-insensitively up front (starred), server iniswap.txt entries follow
// minus case-insensitive duplicates of wardrobe entries.
func TestMergeWardrobe(t *testing.T) {
	names, stars := mergeWardrobe(
		[]string{"Zeta", "aigis"},
		[]string{"AIGIS", "bread", "zeta", "shrek"},
	)
	wantNames := []string{"aigis", "Zeta", "bread", "shrek"}
	wantStars := []bool{true, true, false, false}
	if len(names) != len(wantNames) {
		t.Fatalf("merged %v, want %v", names, wantNames)
	}
	for i := range wantNames {
		if names[i] != wantNames[i] || stars[i] != wantStars[i] {
			t.Fatalf("merged %v/%v, want %v/%v", names, stars, wantNames, wantStars)
		}
	}
}

// TestNormalizeThemeRoot pins the three path shapes users actually paste:
// the root, the themes folder itself, and a single theme inside it (which
// also auto-picks that theme).
func TestNormalizeThemeRoot(t *testing.T) {
	root := t.TempDir()
	themes := filepath.Join(root, "themes")
	one := filepath.Join(themes, "themeexample1")
	if err := os.MkdirAll(one, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(one, "courtroom_design.ini"), []byte("[a]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, pick := normalizeThemeRoot(root); got != root || pick != "" {
		t.Errorf("root form → %q/%q", got, pick)
	}
	if got, pick := normalizeThemeRoot(themes); got != root || pick != "" {
		t.Errorf("themes form → %q/%q, want %q", got, pick, root)
	}
	if got, pick := normalizeThemeRoot(one); got != root || pick != "themeexample1" {
		t.Errorf("single-theme form → %q/%q, want %q/themeexample1", got, pick, root)
	}
	// Explorer "Copy as path" pastes the path quoted — must still resolve.
	if got, pick := normalizeThemeRoot(`"` + themes + `"`); got != root || pick != "" {
		t.Errorf("quoted themes form → %q/%q, want %q", got, pick, root)
	}
	if names := scanThemeDirs([]string{root}); len(names) != 2 || names[1] != "themeexample1" {
		t.Errorf("scan = %v, want [default themeexample1]", names)
	}
}

// TestShelfPacker pins the label-atlas allocator: rows fill left to
// right, new shelves open below, padding separates slots, and oversize
// requests fail cleanly (the dedicated-texture fallback).
func TestShelfPacker(t *testing.T) {
	p := shelfPacker{edge: 100}

	a, ok := p.take(40, 10)
	if !ok || a.X != 0 || a.Y != 0 || a.W != 40 || a.H != 10 {
		t.Fatalf("first slot = %+v ok=%v", a, ok)
	}
	b, ok := p.take(40, 10)
	if !ok || b.X != 41 || b.Y != 0 {
		t.Fatalf("second slot = %+v ok=%v (want padded to x=41)", b, ok)
	}
	// 40 more won't fit the 100-wide shelf (41+41+41 > 100): new shelf.
	cSlot, ok := p.take(40, 12)
	if !ok || cSlot.X != 0 || cSlot.Y != 11 {
		t.Fatalf("third slot = %+v ok=%v (want new shelf at y=11)", cSlot, ok)
	}
	// A label taller than the page can never fit.
	if _, ok := p.take(10, 200); ok {
		t.Error("oversize label accepted")
	}
	// Fill vertically until the page refuses.
	for i := 0; i < 32; i++ {
		if _, ok := p.take(90, 20); !ok {
			return // refused cleanly once full — expected
		}
	}
	t.Error("packer never refused on a full page")
}

// TestClampRectInto pins the Qt-edge emulation: overhanging widgets shift
// inside the stage, oversized ones shrink, and fully-off ones are hidden.
func TestClampRectInto(t *testing.T) {
	bounds := sdl.Rect{X: 10, Y: 10, W: 100, H: 100}

	// Hanging off the right edge: shifted left, size preserved.
	r, ok := clampRectInto(sdl.Rect{X: 90, Y: 20, W: 40, H: 20}, bounds)
	if !ok || r.X != 70 || r.Y != 20 || r.W != 40 {
		t.Errorf("right overhang → %+v ok=%v", r, ok)
	}
	// Bigger than the stage: shrunk to fit.
	r, ok = clampRectInto(sdl.Rect{X: 0, Y: 0, W: 300, H: 50}, bounds)
	if !ok || r.W != 100 || r.X != 10 {
		t.Errorf("oversized → %+v ok=%v", r, ok)
	}
	// Fully outside: hidden.
	if _, ok = clampRectInto(sdl.Rect{X: 500, Y: 500, W: 40, H: 20}, bounds); ok {
		t.Error("fully-off rect not hidden")
	}
	// Fully inside: untouched.
	in := sdl.Rect{X: 20, Y: 20, W: 30, H: 30}
	if r, ok = clampRectInto(in, bounds); !ok || r != in {
		t.Errorf("inside rect changed: %+v", r)
	}
}

// TestRectOverlapFrac pins the log-merge detector.
func TestRectOverlapFrac(t *testing.T) {
	a := sdl.Rect{X: 0, Y: 0, W: 100, H: 100}
	if f := rectOverlapFrac(a, a); f != 1 {
		t.Errorf("identical rects → %v, want 1", f)
	}
	if f := rectOverlapFrac(a, sdl.Rect{X: 200, Y: 0, W: 50, H: 50}); f != 0 {
		t.Errorf("disjoint rects → %v, want 0", f)
	}
	// Half of the smaller rect inside the bigger one.
	if f := rectOverlapFrac(a, sdl.Rect{X: 75, Y: 0, W: 50, H: 100}); f != 0.5 {
		t.Errorf("half overlap → %v, want 0.5", f)
	}
}

// TestStreamerMaskLine pins the on-stream redaction: sender prefixes and
// IPv4 tokens mask, in-sentence colons survive, message text is kept.
func TestStreamerMaskLine(t *testing.T) {
	// Every OOC entry is "name: text" by construction (pushOOC); lines
	// without the separator are system notices.
	cases := map[string]string{
		"Nyah: hello there":              "???: hello there",
		"[MOD CALL] 203.0.113.7 called":  "[MOD CALL] █.█.█.█ called",
		"server: join 198.51.100.23 now": "???: join █.█.█.█ now",
		"no colon line":                  "no colon line",
	}
	for in, want := range cases {
		if got := streamerMaskLine(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWrapToWidthMOTD pins the OOC wrapper that replaced the 120-char
// truncation: words stay whole and in order, nothing exceeds the column,
// oversized words hard-split, and the per-entry line cap holds. The nil
// font measures at 8 px/char (deterministic headless metric).
func TestWrapToWidthMOTD(t *testing.T) {
	const colW = 80 // 10 chars at the fallback metric
	// All words fit the column, so join-equality holds (hard-split words
	// rejoin with an inserted space and are asserted separately below).
	motd := "welcome to the courtroom enjoy your stay in here"
	lines := wrapToWidth(nil, motd, colW, 24)
	if len(lines) < 4 {
		t.Fatalf("MOTD wrapped to %d lines: %v", len(lines), lines)
	}
	if got := strings.Join(lines, " "); got != motd {
		t.Errorf("wrap lost/reordered text:\n got %q\nwant %q", got, motd)
	}
	for i, l := range lines {
		if len(l)*8 > colW {
			t.Errorf("line %d (%q) wider than the column", i, l)
		}
	}
	// A 40-char "URL" must hard-split, not overflow.
	long := strings.Repeat("x", 40)
	for i, l := range wrapToWidth(nil, long, colW, 24) {
		if len(l)*8 > colW {
			t.Errorf("hard-split line %d (%q) wider than the column", i, l)
		}
	}
	// Hostile entry: the per-entry cap holds.
	if got := wrapToWidth(nil, strings.Repeat("word ", 500), colW, 24); len(got) > 24 {
		t.Errorf("line cap breached: %d lines", len(got))
	}
	if got := wrapToWidth(nil, "   ", colW, 24); got != nil {
		t.Errorf("blank text → %v, want nil", got)
	}
}

// TestICLogFilterCache pins the per-frame filter cache: stable while
// nothing changed, invalidated by pushes and query edits.
func TestICLogFilterCache(t *testing.T) {
	a := &App{}
	a.pushIC("Phoenix: hello court", 0, false)
	a.pushIC("Edgeworth: OBJECTION", 2, false)

	first := a.icLogFiltered()
	if len(first) != 2 {
		t.Fatalf("filtered = %d, want 2", len(first))
	}
	if again := a.icLogFiltered(); &again[0] != &first[0] {
		t.Error("cache not reused on unchanged input")
	}
	a.logSearch = "objection"
	if got := a.icLogFiltered(); len(got) != 1 || got[0] != 1 {
		t.Errorf("query filter = %v, want [1]", got)
	}
	a.pushIC("Maya: objection noted", 0, false)
	if got := a.icLogFiltered(); len(got) != 2 {
		t.Errorf("post-push filter = %v, want 2 matches", got)
	}
}

// TestWrapTextCaps pins the lobby description wrapper: nil for blank
// input, the line cap holds, and the capped line gains an ellipsis.
// (Headless: TextWidth returns 0 without a font, so everything fits one
// line unless the cap forces splits — we exercise the cap path.)
func TestWrapTextCaps(t *testing.T) {
	c := &Ctx{widthCache: map[string]int32{}}
	if got := c.WrapText("   ", 100, 4); got != nil {
		t.Fatalf("blank input → %v, want nil", got)
	}
	if got := c.WrapText("one two three", 100, 4); len(got) != 1 {
		t.Fatalf("zero-width font must yield one line, got %v", got)
	}
}
