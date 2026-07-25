package ui

// SDL-needing half of the #39 tests: the per-element font SETS and the pick memo
// that routes lines into them. These build real ttf faces, so they skip when
// SDL_ttf is unavailable (the headless pattern of chromefont_test.go).

import (
	"testing"

	"github.com/veandco/go-sdl2/ttf"
)

// newFaceCtx is a headless Ctx with the embedded chrome face — enough for every
// font-set path (no renderer is touched: the caches start empty).
func newFaceCtx(t *testing.T) *Ctx {
	t.Helper()
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	t.Cleanup(ttf.Quit)
	font, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Skipf("embedded font: %v", err)
	}
	return &Ctx{font: font, textCache: map[textKey]cachedText{}, widthCache: map[string]int32{}}
}

// TestPickMemoScopedToSetAndPct is the §5.6 regression guard, and it FAILS on the
// pre-#39 memo. pickSet used to key its per-line pick by TEXT ALONE, across every
// set and every scale — so the first surface to pick a face for a string handed
// that exact face to every other surface. It was masked only by the len(fonts)==1
// fast path; the moment any fallback tier (or a custom font) makes a set
// multi-face it is live, and with per-element theme sets it becomes a
// wrong-FAMILY bug rather than merely a wrong size.
func TestPickMemoScopedToSetAndPct(t *testing.T) {
	c := newFaceCtx(t)
	defer c.Destroy()
	// A custom chain makes both sets MULTI-face (chain face + embedded last
	// resort), which is what takes pickSet off its single-font fast path.
	c.SetFontChain([]string{dyslexiaFontName}, [][]byte{openDyslexicOTF})
	const line = "Objection!"
	if len(c.fontsFor(&c.logSet, DefaultScalePct)) < 2 {
		t.Skip("fixture needs a multi-face set for the memo to matter")
	}
	chat := c.ChatFontFor(150, line)
	log := c.LogFontFor(DefaultScalePct, line)
	if chat == nil || log == nil {
		t.Fatal("both picks must resolve a face")
	}
	if chat == log {
		t.Fatal("the chat set at 150% and the log set at 100% must not share one memoized face")
	}
	if chat.Height() == log.Height() {
		t.Errorf("faces at 150%% and 100%% have the same height %d — the pick ignored the scale", chat.Height())
	}
	// Re-asking must be stable (the memo has to actually memoize, per set).
	if again := c.LogFontFor(DefaultScalePct, line); again != log {
		t.Error("the log pick must be stable across repeat calls")
	}
	if again := c.ChatFontFor(150, line); again != chat {
		t.Error("the chat pick must be stable across repeat calls")
	}
}

// TestThemeElemSetsPinTheirOwnScale pins the reason each element gets its OWN
// fontSet: a theme that sizes ic_chatlog at 12 pt and server_chatlog at 8 pt
// draws both every frame, and one shared set would rebuild (and purge the whole
// label cache) twice per frame forever.
func TestThemeElemSetsPinTheirOwnScale(t *testing.T) {
	c := newFaceCtx(t)
	defer c.Destroy()
	c.SetThemeFaces([][]byte{openDyslexicOTF})
	genAfterInstall := c.fontChainGen
	ic := c.ThemeFont(elemICChatlog, 0, themeFontPct(12))
	oc := c.ThemeFont(elemServerChatlog, 0, themeFontPct(8))
	if ic == nil || oc == nil {
		t.Fatal("both element sets must build a face")
	}
	if ic == oc || ic.Height() == oc.Height() {
		t.Fatalf("elements at 12 pt and 8 pt must get DIFFERENT faces (heights %d / %d)",
			ic.Height(), oc.Height())
	}
	for i := 0; i < 50; i++ { // the per-frame ask, both elements, interleaved
		if got := c.ThemeFont(elemICChatlog, 0, themeFontPct(12)); got != ic {
			t.Fatalf("ic_chatlog set rebuilt on iteration %d — the sets are thrashing", i)
		}
		if got := c.ThemeFont(elemServerChatlog, 0, themeFontPct(8)); got != oc {
			t.Fatalf("server_chatlog set rebuilt on iteration %d — the sets are thrashing", i)
		}
	}
	if c.fontChainGen != genAfterInstall {
		t.Errorf("drawing must not bump fontChainGen (%d → %d)", genAfterInstall, c.fontChainGen)
	}
	if c.themeElemSets[elemICChatlog].pct != themeFontPct(12) ||
		c.themeElemSets[elemServerChatlog].pct != themeFontPct(8) {
		t.Errorf("element sets lost their pinned scales: %d / %d",
			c.themeElemSets[elemICChatlog].pct, c.themeElemSets[elemServerChatlog].pct)
	}
}

// TestSetThemeFacesBumpsGenAndClears pins the invalidation contract: installing
// (or clearing) theme faces bumps fontChainGen — which is what every wrapped-text
// cache keys on, so a theme swap can't leave rows wrapped in the old face (#42) —
// and drops the pointer-keyed pick memo.
func TestSetThemeFacesBumpsGenAndClears(t *testing.T) {
	c := newFaceCtx(t)
	defer c.Destroy()
	before := c.fontChainGen
	c.SetThemeFaces([][]byte{openDyslexicOTF})
	if c.fontChainGen == before {
		t.Fatal("installing theme faces must bump fontChainGen (the wrap caches key on it)")
	}
	if len(c.themeFaceData) != 1 || len(c.themeFaceCover) != 1 {
		t.Fatalf("face bytes and cmap covers must stay index-aligned: %d / %d",
			len(c.themeFaceData), len(c.themeFaceCover))
	}
	themed := c.ThemeFont(elemMessage, 0, DefaultScalePct)
	plain := c.ChatFont(DefaultScalePct)
	if themed == plain {
		t.Error("a theme-declared face must not resolve to the client's own chain face")
	}
	mid := c.fontChainGen
	c.SetThemeFaces(nil)
	if c.fontChainGen == mid {
		t.Fatal("clearing theme faces must bump fontChainGen too")
	}
	if c.themeFaceData != nil {
		t.Error("SetThemeFaces(nil) must clear the installed bytes")
	}
	// With no theme face, an element set falls back to the client's own chain: a
	// different set object, but the same face metrics as the global one.
	fallback := c.ThemeFont(elemMessage, -1, DefaultScalePct)
	if fallback == nil || fallback.Height() != c.ChatFont(DefaultScalePct).Height() {
		t.Error("a familyless element must render at the same metrics as the client font")
	}
	// Clearing twice is a free no-op (the common no-theme-font path).
	gen := c.fontChainGen
	c.SetThemeFaces(nil)
	if c.fontChainGen != gen {
		t.Error("a redundant clear must not bump the generation")
	}
}

// TestThemeElemSetsCapAndOutOfRangeFace: a face index past what is installed (the
// themeFaceCap overflow, or a stale table landing before its faces) must degrade
// to the client font, never index out of range.
func TestThemeElemSetsCapAndOutOfRangeFace(t *testing.T) {
	c := newFaceCtx(t)
	defer c.Destroy()
	c.SetThemeFaces([][]byte{openDyslexicOTF})
	for _, face := range []int{-1, 1, 99} {
		got := c.ThemeFont(elemAreaList, face, DefaultScalePct)
		if got == nil {
			t.Fatalf("face %d resolved to nil", face)
		}
		if got.Height() != c.LogFont(DefaultScalePct).Height() {
			t.Errorf("face %d must fall back to the client font's metrics", face)
		}
	}
	// SetThemeFaces truncates to the cap rather than growing (hard rule 4).
	over := make([][]byte, themeFaceCap+3)
	for i := range over {
		over[i] = openDyslexicOTF
	}
	c.SetThemeFaces(over)
	if len(c.themeFaceData) != themeFaceCap {
		t.Errorf("installed %d faces, want the cap %d", len(c.themeFaceData), themeFaceCap)
	}
}

// TestCtxDestroyClosesElementSets: Destroy must close each element set's own
// faces exactly once and never the SHARED chrome face (which a set at 100% takes
// as its embedded last resort) — a double close is a use-after-free.
func TestCtxDestroyClosesElementSets(t *testing.T) {
	c := newFaceCtx(t)
	// One set that SHARES c.font (100%, no chain) and one that owns its face.
	shared := c.ThemeFont(elemMusicName, -1, DefaultScalePct)
	if shared != c.font {
		t.Fatalf("an undressed element set at 100%% must share the chrome face")
	}
	own := c.ThemeFont(elemMusicList, -1, themeFontPct(6))
	if own == c.font {
		t.Fatal("a scaled element set must own its own face")
	}
	c.Destroy() // must not double-close c.font, and must not leak `own`
	if c.themeElemSets[elemMusicList].fonts != nil {
		t.Error("Destroy must empty the element sets")
	}
}
