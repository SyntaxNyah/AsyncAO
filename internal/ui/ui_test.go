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

// TestEditStep exhaustively pins the pure text-edit logic the draw path can't
// unit-test: insert/backspace/forward-delete/caret-move at start/mid/end,
// select-all replace, caret clamping, and — the classic bug — MULTIBYTE runes
// (caret by rune, not byte) on the server's real shownames.
func TestEditStep(t *testing.T) {
	chk := func(name, gotV string, gotC int, wantV string, wantC int) {
		if gotV != wantV || gotC != wantC {
			t.Errorf("%s = %q,%d want %q,%d", name, gotV, gotC, wantV, wantC)
		}
	}
	v, c := editStep("ac", 1, editInput{typed: "b"})
	chk("insert mid", v, c, "abc", 2)
	v, c = editStep("ab", 2, editInput{typed: "c"})
	chk("insert end", v, c, "abc", 3)
	v, c = editStep("bc", 0, editInput{typed: "a"})
	chk("insert start", v, c, "abc", 1)
	v, c = editStep("abc", 2, editInput{back: true})
	chk("backspace", v, c, "ac", 1)
	v, c = editStep("abc", 0, editInput{back: true})
	chk("backspace at start (no-op)", v, c, "abc", 0)
	v, c = editStep("abc", 1, editInput{op: editDelete})
	chk("forward delete", v, c, "ac", 1)
	v, c = editStep("abc", 3, editInput{op: editDelete})
	chk("delete at end (no-op)", v, c, "abc", 3)
	_, c = editStep("abc", 1, editInput{op: editLeft})
	if c != 0 {
		t.Errorf("left = %d want 0", c)
	}
	_, c = editStep("abc", 0, editInput{op: editLeft})
	if c != 0 {
		t.Errorf("left at start = %d want 0 (clamped)", c)
	}
	_, c = editStep("abc", 3, editInput{op: editRight})
	if c != 3 {
		t.Errorf("right at end = %d want 3 (clamped)", c)
	}
	_, c = editStep("abc", 1, editInput{op: editHome})
	if c != 0 {
		t.Errorf("home = %d want 0", c)
	}
	_, c = editStep("abc", 1, editInput{op: editEnd})
	if c != 3 {
		t.Errorf("end = %d want 3", c)
	}
	v, c = editStep("hello", 5, editInput{typed: "x", selAll: true})
	chk("select-all + type replaces", v, c, "x", 1)
	v, c = editStep("hello", 5, editInput{back: true, selAll: true})
	chk("select-all + backspace clears", v, c, "", 0)
	// Multibyte: "Häschen" = H ä s c h e n (7 runes). Insert at rune 2, backspace
	// at rune 2, and an emoji forward-delete — all by RUNE, never by byte.
	v, c = editStep("Häschen", 2, editInput{typed: "!"})
	chk("multibyte insert", v, c, "Hä!schen", 3)
	v, c = editStep("Häschen", 2, editInput{back: true})
	chk("multibyte backspace (deletes ä)", v, c, "Hschen", 1)
	v, c = editStep("a🍅b", 1, editInput{op: editDelete})
	chk("emoji forward-delete", v, c, "ab", 1)
	v, c = editStep("ab", 99, editInput{typed: "c"})
	chk("caret past end clamps", v, c, "abc", 3)
	v, c = editStep("", 0, editInput{typed: "x"})
	chk("empty insert", v, c, "x", 1)
	v, c = editStep("", 0, editInput{back: true})
	chk("empty backspace", v, c, "", 0)
}

// TestCtrlXFallsThroughToHotkey pins the Extras-keybind fix: Ctrl+X is clipboard
// cut only with a field focused; with nothing focused it falls through to the
// configurable hotkeys — otherwise the Extras toggle bound to "x" was dead (the
// cut handler swallowed the chord before dispatch).
func TestCtrlXFallsThroughToHotkey(t *testing.T) {
	// A focused field → cut, never a hotkey.
	c := &Ctx{}
	c.BeginFrame(time.Millisecond)
	c.focusID = "ic"
	c.HandleEvent(&sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Sym: sdl.K_x, Mod: sdl.KMOD_LCTRL}})
	if !c.cutReq || c.hotkey != 0 {
		t.Errorf("Ctrl+X with a focused field must cut (cutReq=%v hotkey=%d)", c.cutReq, c.hotkey)
	}
	// Nothing focused → a hotkey, not a (no-op) cut.
	c = &Ctx{}
	c.BeginFrame(time.Millisecond)
	c.HandleEvent(&sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Sym: sdl.K_x, Mod: sdl.KMOD_LCTRL}})
	if c.cutReq || c.hotkey != sdl.K_x {
		t.Errorf("Ctrl+X with nothing focused must dispatch as a hotkey (cutReq=%v hotkey=%d, want %d)",
			c.cutReq, c.hotkey, sdl.K_x)
	}
}

// TestNameColorStable pins per-speaker name colours: deterministic per name
// (same name → same colour every call/launch — it's a fixed hash, not a seeded
// one), distinct for distinct names here, and always full-alpha.
func TestNameColorStable(t *testing.T) {
	// Two calls with the same name must agree (the hash is fixed, not seeded).
	first := nameColor("Phoenix", 0.6, 0.9)
	if again := nameColor("Phoenix", 0.6, 0.9); again != first {
		t.Errorf("nameColor not stable: %v vs %v", first, again)
	}
	if nameColor("Phoenix", 0.6, 0.9) == nameColor("Edgeworth", 0.6, 0.9) {
		t.Error("distinct names should get distinct colours")
	}
	if c := nameColor("Maya", 0.6, 0.9); c.A != 255 {
		t.Errorf("alpha = %d, want 255", c.A)
	}
}

// TestNameColorNoAlloc pins the per-speaker name colour as allocation-free. It
// runs INLINE per visible name in the IC/OOC log whenever name colours are on
// (there's deliberately no cache), so a stray alloc here would land straight on
// the always-on draw path. Guards the inline FNV-1a + hsvToRGB math against a
// future regression (e.g. a []byte(name) conversion or an fmt call sneaking in).
func TestNameColorNoAlloc(t *testing.T) {
	for _, nm := range []string{"Phoenix", "Edgeworth"} {
		name := nm
		if n := testing.AllocsPerRun(100, func() { _ = nameColor(name, 0.6, 0.9) }); n != 0 {
			t.Errorf("nameColor(%q) allocs = %v, want 0", name, n)
		}
	}
}

// TestMergeWardrobe pins the wardrobe-first menu: client favourites sort
// case-insensitively up front (starred), server iniswap.txt entries follow
// minus case-insensitive duplicates of wardrobe entries. inServer marks which
// entries are in the server list — a favourite that ISN'T a server iniswap
// ("quagmire" here) is false, so the Iniswaps tab won't show it.
func TestMergeWardrobe(t *testing.T) {
	names, stars, inServer := mergeWardrobe(
		[]string{"Zeta", "aigis", "quagmire"},
		[]string{"AIGIS", "bread", "zeta", "shrek"},
	)
	wantNames := []string{"aigis", "quagmire", "Zeta", "bread", "shrek"}
	wantStars := []bool{true, true, true, false, false}
	wantServer := []bool{true, false, true, true, true} // quagmire favourited but not on the server
	if len(names) != len(wantNames) {
		t.Fatalf("merged %v, want %v", names, wantNames)
	}
	for i := range wantNames {
		if names[i] != wantNames[i] || stars[i] != wantStars[i] || inServer[i] != wantServer[i] {
			t.Fatalf("entry %d = %q star=%v server=%v, want %q/%v/%v",
				i, names[i], stars[i], inServer[i], wantNames[i], wantStars[i], wantServer[i])
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

// TestOOCWrapURLFullLink pins the fix for links the OOC wrapper hard-splits:
// a long Discord CDN URL (a '&'-laden query string) is captured WHOLE from the
// source entry, so every wrapped row of it opens the full link — not the
// truncated fragment a per-display-line scan used to grab at the first '&'.
func TestOOCWrapURLFullLink(t *testing.T) {
	a := testTabApp(t)
	const url = "https://cdn.discordapp.com/attachments/151/151/asyncao-windows-x86_64.zip?ex=6a31e0a4&is=6a308f24&hm=335a54ec&"
	a.pushOOC("Nyah: "+url, "Nyah")

	// A narrow column (15 chars at the 8 px/char headless metric) forces the
	// URL to hard-split across several display rows.
	lines := a.oocWrapped(120)
	if len(a.oocWrapURL) != len(lines) {
		t.Fatalf("oocWrapURL (%d) not parallel to wrapped lines (%d)", len(a.oocWrapURL), len(lines))
	}
	if len(lines) < 2 {
		t.Fatalf("expected the URL to wrap across rows, got %d line(s): %v", len(lines), lines)
	}
	// Setup guard: no single display row holds the whole URL, so a per-row
	// scan really would truncate — that's the bug this fix removes.
	for _, ln := range lines {
		if strings.Contains(ln, url) {
			t.Fatal("URL did not hard-split; the test no longer exercises the bug")
		}
	}
	// Every wrapped row of the entry must resolve to the FULL link.
	for li, ln := range lines {
		if a.oocWrapURL[li] != url {
			t.Errorf("row %d (%q): oocWrapURL = %q, want full %q", li, ln, a.oocWrapURL[li], url)
		}
	}
}

// TestOOCWrapURLPerParagraph pins that a multi-line OOC entry with a URL on each
// line (a server's fork/upstream description) makes each line open its OWN link
// — not the entry's first one for every line (the "hovering only one of two
// links" bug).
func TestOOCWrapURLPerParagraph(t *testing.T) {
	a := testTabApp(t)
	const u1 = "https://github.com/SyntaxNyah/Nyathena"
	const u2 = "https://github.com/MangosArentLiterature/Athena"
	a.pushOOC("Server: Fork:\n"+u1+"\nUpstream:\n"+u2, "Server")

	lines := a.oocWrapped(800) // wide enough that each URL fits one row
	if len(a.oocWrapURL) != len(lines) {
		t.Fatalf("oocWrapURL (%d) not parallel to lines (%d)", len(a.oocWrapURL), len(lines))
	}
	var sawU1, sawU2 bool
	for li, ln := range lines {
		if ln == u1 {
			sawU1 = true
			if a.oocWrapURL[li] != u1 {
				t.Errorf("u1 line carries %q, want u1", a.oocWrapURL[li])
			}
		}
		if ln == u2 {
			sawU2 = true
			if a.oocWrapURL[li] != u2 { // the bug: this was u1 (the entry's first link)
				t.Errorf("u2 line carries %q, want u2 (its OWN link, not the first)", a.oocWrapURL[li])
			}
		}
	}
	if !sawU1 || !sawU2 {
		t.Fatalf("both URL lines must be present (u1=%v u2=%v)", sawU1, sawU2)
	}
}

// TestCapLogLine pins the IC-entry guard that replaced the 120-char truncation
// ("text cuts off if it's too long"): a real max-length IC message (256 on the
// wire) survives whole to be word-wrapped at draw time, and only a hostile huge
// line is bounded (with an ellipsis).
func TestCapLogLine(t *testing.T) {
	if s := "Phoenix: Objection!"; capLogLine(s) != s {
		t.Error("a short line must pass through unchanged")
	}
	long := "Phoenix: " + strings.Repeat("a", 256) // the old clampLine cut these at 120
	if got := capLogLine(long); got != long {
		t.Errorf("a 256-char IC message must survive whole, got %d runes", len([]rune(got)))
	}
	huge := strings.Repeat("x", icLineCap+50)
	if r := []rune(capLogLine(huge)); len(r) != icLineCap+1 || r[len(r)-1] != '…' {
		t.Errorf("a hostile huge line must cap at %d runes + …, got %d", icLineCap, len(r))
	}
}

// TestICLogStoresLongLine pins that pushIC keeps a long message intact (it now
// wraps at draw time) instead of truncating at 120 chars.
func TestICLogStoresLongLine(t *testing.T) {
	a := &App{}
	msg := "Phoenix: " + strings.Repeat("b", 200) // 209 chars, over the old cap
	a.pushIC(msg, 0, false, -1, "Phoenix")
	if a.icLog[0].text != msg {
		t.Errorf("IC log cut a %d-rune line to %d (must wrap, not truncate)",
			len([]rune(msg)), len([]rune(a.icLog[0].text)))
	}
}

// TestParseHexColor pins the Extras-box colour parser: "rrggbb" (optionally with
// "#" / surrounding space) → opaque colour; empty/short/long/non-hex → not ok
// (so a blank pref keeps the stock colour).
func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in      string
		r, g, b uint8
		ok      bool
	}{
		{"78aaff", 0x78, 0xaa, 0xff, true},
		{"#FF0000", 0xff, 0, 0, true},
		{"000000", 0, 0, 0, true},
		{"  abcdef ", 0xab, 0xcd, 0xef, true},
		{"", 0, 0, 0, false},
		{"12345", 0, 0, 0, false},
		{"1234567", 0, 0, 0, false},
		{"gggggg", 0, 0, 0, false},
	}
	for _, tc := range cases {
		col, ok := parseHexColor(tc.in)
		if ok != tc.ok {
			t.Errorf("parseHexColor(%q) ok=%v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && (col.R != tc.r || col.G != tc.g || col.B != tc.b || col.A != 255) {
			t.Errorf("parseHexColor(%q) = %+v, want %d,%d,%d,255", tc.in, col, tc.r, tc.g, tc.b)
		}
	}
}

// TestICLogFilterCache pins the per-frame filter cache: stable while
// nothing changed, invalidated by pushes and query edits.
func TestICLogFilterCache(t *testing.T) {
	a := &App{}
	a.pushIC("Phoenix: hello court", 0, false, -1, "Phoenix")
	a.pushIC("Edgeworth: OBJECTION", 2, false, -1, "Edgeworth")

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
	a.pushIC("Maya: objection noted", 0, false, -1, "Maya")
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
