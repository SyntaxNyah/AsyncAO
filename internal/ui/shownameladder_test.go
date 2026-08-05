package ui

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// showname_extra_width
// ---------------------------------------------------------------------------

// TestParseShownameExtraWidth covers the real spread in the 74-theme reference
// corpus plus the shapes AO2's QString::toInt() swallows. AO2 tests the result
// with `extra_width > 0` (AO2-Client courtroom.cpp:3357), so anything that fails
// to parse must land on 0 = ladder off, never on an error that would abort the
// chatbox.
func TestParseShownameExtraWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want int
	}{
		{"modal corpus value", "24", 24},     // 32 of the 74 themes
		{"second commonest", "10", 10},       // 14 themes
		{"AceAttorney2x", "48", 48},          // 4 themes
		{"P5Theme, the maximum", "225", 225}, // 1 theme
		{"whitespace trimmed", " 24 ", 24},   // like every other design number
		{"explicit zero", "0", 0},            // 9 themes switch the ladder off outright
		{"key absent", "", 0},
		{"negative parses, then the ladder rejects it", "-5", -5},
		{"unparseable is 0, not an error", "wide", 0},
		{"and it is not a prefix parse", "24px", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseShownameExtraWidth(tc.raw); got != tc.want {
				t.Fatalf("parseShownameExtraWidth(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// TestShownameExtraWidthReadFromDesignINI walks the real plumbing the way the
// alignment key's sibling test does: the key name, the DesignValue lookup the
// theme-apply goroutine performs, and the INI reader in front of it (including
// Qt's inline-';' rule).
func TestShownameExtraWidthReadFromDesignINI(t *testing.T) {
	for _, tc := range []struct {
		name   string
		design string
		want   int
	}{
		{"stock", "showname_extra_width = 24", 24},
		{"absent", "showname = 1, 0, 46, 15", 0},
		{"zero", "showname_extra_width = 0", 0},
		{"trailing comment", "showname_extra_width = 48 ; wider name plate", 48},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := writeThemeDesign(t, tc.design+"\n")
			raw, _ := th.DesignValue(shownameExtraWidthKey)
			if got := parseShownameExtraWidth(raw); got != tc.want {
				t.Fatalf("design %q → raw %q → %d, want %d", tc.design, raw, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The ladder itself.
// ---------------------------------------------------------------------------

// TestShownameLadderMatchesAO2 is AO2's widen-and-swap ladder
// (AO2-Client courtroom.cpp:3356-3378) as a table, including every arm that does
// NOT widen — those are the ones a well-meaning implementation invents around.
func TestShownameLadderMatchesAO2(t *testing.T) {
	const boxW = 46 // the stock theme's showname = 1, 0, 46, 15
	for _, tc := range []struct {
		name         string
		textW, extra int32
		med, big     bool
		wantRung     chatboxRung
		wantW        int32
	}{
		{"name fits, nothing happens", 40, 24, true, true, chatboxRungBase, boxW},
		{"name exactly fills the box", boxW, 24, true, true, chatboxRungBase, boxW},
		{"one pixel over takes the med rung", boxW + 1, 24, true, true, chatboxRungMed, boxW + 24},
		{"med is enough", 60, 24, true, true, chatboxRungMed, boxW + 24},
		{"med exactly fits", boxW + 24, 24, true, true, chatboxRungMed, boxW + 24},
		{"past med takes big", boxW + 25, 24, true, true, chatboxRungBig, boxW + 48},
		{"past big is cut off, not widened further", 4000, 24, true, true, chatboxRungBig, boxW + 48},
		// The three "AO2 does nothing" arms.
		{"no extra_width: the ladder is off (nine corpus themes)", 4000, 0, true, true, chatboxRungBase, boxW},
		{"negative extra_width is off too", 4000, -24, true, true, chatboxRungBase, boxW},
		{"no variant art: NOT resized (P5Theme)", 4000, 225, false, false, chatboxRungBase, boxW},
		// med-only stops at one rung; big-without-med never fires, because AO2
		// nests the big test inside the med branch.
		{"med only", 4000, 24, true, false, chatboxRungMed, boxW + 24},
		{"big without med never fires", 4000, 24, false, true, chatboxRungBase, boxW},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rung, w := shownameLadder(boxW, tc.textW, tc.extra, tc.med, tc.big)
			if rung != tc.wantRung || w != tc.wantW {
				t.Fatalf("shownameLadder(%d, %d, %d, med=%v, big=%v) = (rung %d, %d px), want (rung %d, %d px)",
					boxW, tc.textW, tc.extra, tc.med, tc.big, rung, w, tc.wantRung, tc.wantW)
			}
			if w < boxW {
				t.Fatalf("the ladder narrowed the box to %d px — it only ever widens", w)
			}
		})
	}
}

// TestChatboxRungStems pins the two halves of a rung's identity that must never
// be confused: the T1 key it is pinned under (fixed, so every draw site names the
// same texture) and the file it comes from (derived from whichever base candidate
// the theme actually shipped).
func TestChatboxRungStems(t *testing.T) {
	if a, b, c := chatboxRungBase.texStem(), chatboxRungMed.texStem(), chatboxRungBig.texStem(); a == b || b == c || a == c {
		t.Fatalf("the three rungs must pin under distinct T1 stems, got %q / %q / %q", a, b, c)
	}
	if chatboxRungBase.texStem() != themeStemChatbox {
		t.Fatalf("the base rung must keep the existing chatbox stem %q, got %q",
			themeStemChatbox, chatboxRungBase.texStem())
	}
	// Every base spelling AsyncAO probes (app.go themeImageStems: AO2's
	// chat → chatbox ladder, plus chatblank for themes that ship only the blank
	// plate). 64 of the 74 reference themes ship `chat`; P5Theme ships `chatbox`.
	for _, base := range []string{"chat", "chatbox", "chatblank"} {
		if got, want := chatboxRungMed.fileStem(base), base+"med"; got != want {
			t.Errorf("med file stem for base %q = %q, want %q", base, got, want)
		}
		if got, want := chatboxRungBig.fileStem(base), base+"big"; got != want {
			t.Errorf("big file stem for base %q = %q, want %q", base, got, want)
		}
		if got := chatboxRungBase.fileStem(base); got != base {
			t.Errorf("base file stem for %q = %q, want the base itself", base, got)
		}
	}
}

// TestChatboxLadderArtComesFromTheBaseSkinsOwnDirectory pins the lookup rule that
// keeps the ladder honest across a theme that inherits from another.
//
// AO2 never looks the variants up by NAME: it appends to the resolved base
// image's own path (AO2-Client courtroom.cpp:3358, over AOImage's remembered
// m_file_name), so a variant can only ever come out of the directory the base
// came out of. FindAsset — which falls through to the bundled default theme —
// would happily pair one theme's base with another theme's variant and stretch
// mismatched art into the same box.
//
// The fixture is the shape that actually occurs: a theme with its own chat.png
// and chatmed.png, inheriting from a default theme that also ships a chatbig.png.
// AO2 shows that theme its med skin and never its parent's big.
func TestChatboxLadderArtComesFromTheBaseSkinsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	themeDir := filepath.Join(root, theme.ThemesDirName, "inheritor")
	defDir := filepath.Join(root, theme.ThemesDirName, theme.DefaultThemeName)
	for _, d := range []string{themeDir, defDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch := func(dir, name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touch(themeDir, "chat.png")
	touch(themeDir, "chatmed.png")
	touch(defDir, "chat.png")
	touch(defDir, "chatbig.png") // the parent's big must NOT be adopted

	th, err := theme.Load("inheritor", "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	basePath, ok := th.FindAsset("chat", themeImageExts)
	if !ok {
		t.Fatal("the base skin did not resolve")
	}
	if got := filepath.Dir(basePath); got != themeDir {
		t.Fatalf("base skin resolved from %q, want the theme's own dir %q", got, themeDir)
	}
	baseDir, baseStem := filepath.Dir(basePath), "chat"

	if _, ok := theme.FindAssetIn(baseDir, chatboxRungMed.fileStem(baseStem), themeImageExts); !ok {
		t.Error("the theme's own chatmed.png was not found beside its base skin")
	}
	if p, ok := theme.FindAssetIn(baseDir, chatboxRungBig.fileStem(baseStem), themeImageExts); ok {
		t.Errorf("adopted %q as the big rung — a variant may only come from the base skin's own directory", p)
	}
	// ...and the reason that is not just pedantry: the un-scoped lookup DOES find it.
	if _, ok := th.FindAsset(chatboxRungBig.fileStem(baseStem), themeImageExts); !ok {
		t.Fatal("fixture is not exercising anything: the parent's chatbig.png is not reachable at all")
	}
}

// TestChatboxLadderArtSkippedWithoutABaseSkin covers the six reference themes
// that ship a chatmed.png and NO chat.png / chatbox.png at all. AO2 resolves
// their base through its bundled default theme and then reads the variants
// relative to THAT, so their own chatmed is dead weight; a streaming client has
// no such tree, so the chatbox falls back to the flat panel and the ladder must
// not run at all.
func TestChatboxLadderArtSkippedWithoutABaseSkin(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, theme.ThemesDirName, "orphanmed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chatmed.png"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := theme.Load("orphanmed", "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	for _, cand := range []string{"chat", "chatbox", "chatblank"} {
		if p, ok := th.FindAsset(cand, themeImageExts); ok {
			t.Fatalf("fixture resolved a base skin at %q — it is supposed to have none", p)
		}
	}
	// With no base stem the loader never reaches the variant probe, so no stem is
	// pinned and every draw-time lookup answers false from the themeTex map.
	var a App
	if _, ok := a.themePage(chatboxRungMed.texStem()); ok {
		t.Error("a med skin resolved with nothing loaded")
	}
}

// ---------------------------------------------------------------------------
// The baked, canvas-scaled rung height.
// ---------------------------------------------------------------------------

// TestThemeLayoutBakesShownameExtra pins that the rung height is scaled with the
// rect it widens and baked on the cold rebuild, so the draw path stays integer
// only.
//
// AO2 does not scale it — get_element_dimensions applies themeScalingFactor to
// the showname rect while get_design_element hands extra_width back raw — but AO2
// also ships that factor at 1, so the two agree everywhere AO2 runs. Ours is 1
// only at the Native default on a window that fits the canvas.
func TestThemeLayoutBakesShownameExtra(t *testing.T) {
	const designW, designH = 714, 579 // the stock AO2 canvas

	// Native fit on a window at least as large as the canvas: scale 1, so the
	// baked value is the theme's own number and a stock theme is unchanged.
	a := stageNativeFit(t, designW, designH)
	a.themeShownameExtra = 24
	lay := a.themeLayout(1152, 864)
	if !lay.valid {
		t.Fatal("themeLayout did not validate")
	}
	if got := lay.shownameExtraPx(); got != 24 {
		t.Fatalf("at 1:1 the baked rung is %d px, want the theme's own 24", got)
	}

	// A canvas scaled DOWN scales the rung with it. Native takes the tighter axis,
	// so a full-height window half the canvas's width is exactly a half scale.
	b := stageNativeFit(t, designW, designH)
	b.themeShownameExtra = 24
	half := b.themeLayout(designW/2, designH)
	if got, want := half.shownameExtraPx(), int32(12); got != want {
		t.Fatalf("on a half-size canvas the baked rung is %d px, want %d", got, want)
	}

	// ...but never to nothing: a rung that rounded away would take the SKIN SWAP
	// with it, dropping a long name back onto the narrow art mid-message.
	c := stageNativeFit(t, designW, designH)
	c.themeShownameExtra = 1
	tiny := c.themeLayout(designW/40, designH/40)
	if got := tiny.shownameExtraPx(); got < 1 {
		t.Fatalf("a heavily downscaled canvas baked a %d px rung — the ladder would silently vanish", got)
	}

	// A theme that declares no ladder bakes zero, and so does a hand-built cache.
	d := stageNativeFit(t, designW, designH)
	d.themeShownameExtra = 0
	if got := d.themeLayout(1152, 864).shownameExtraPx(); got != 0 {
		t.Fatalf("a theme with no showname_extra_width baked %d px, want 0", got)
	}
	if got := (&themeLayoutCache{}).shownameExtraPx(); got != 0 {
		t.Fatalf("a canvas-less layout reports a %d px rung, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// The draw-path wiring, against real resident art.
// ---------------------------------------------------------------------------

// ladderHarness is a courtroom App with a real Ctx (so the showname measures on
// a real face) and a real texture store (so residency is residency, not a stub).
func ladderHarness(t *testing.T) (*App, func()) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	ctx, err := NewCtx(ren)
	if err != nil {
		cleanup()
		t.Skipf("Ctx unavailable: %v", err)
	}
	store, err := render.NewTextureStore(ren)
	if err != nil {
		cleanup()
		t.Skipf("texture store unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx = ctx
	a.uiScalePct = 100
	a.d.Store = store
	// The two theme-texture maps the real constructor always creates together
	// (themePage writes into themePages on the first resident lookup).
	a.themeTex = map[string]bool{}
	a.themePages = map[string]*render.TexturePage{}
	return a, func() {
		store.Purge()
		cleanup()
	}
}

// pinFlatThemeArt makes one theme stem resident, exactly as pollThemeApply does:
// pinned upload plus the themeTex flag themePage gates on.
func pinFlatThemeArt(t *testing.T, a *App, stem string) *render.TexturePage {
	t.Helper()
	dec := &assets.Decoded{
		Frames: []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 2, 2))},
		Delays: []time.Duration{0}, Width: 2, Height: 2,
	}
	if err := a.d.Store.UploadPinned(themeTexKey(stem), dec); err != nil {
		t.Skipf("theme upload unavailable: %v", err)
	}
	a.themeTex[stem] = true
	page, ok := a.themePage(stem)
	if !ok {
		t.Fatalf("pinned %q did not become resident", stem)
	}
	return page
}

// TestChatboxSkinLadderRidesResidency is the whole design in one test: the rung a
// message lands on is decided by what the ACTIVE THEME already has pinned in T1,
// and by nothing else. No probe, no fetch, no disk touch per message — a theme
// that ships no variants answers from a map read, forever.
//
// It walks the four cases the corpus actually contains: both variants (60 of 74
// themes), med only, neither (14, P5Theme among them), and a theme that declares
// no extra_width at all (nine).
func TestChatboxSkinLadderRidesResidency(t *testing.T) {
	a, cleanup := ladderHarness(t)
	defer cleanup()

	const name = "Furiously Long Showname"
	lay := &themeLayoutCache{valid: true, r: map[string]sdl.Rect{}}
	snFont, snEmoji := a.themedChatFace(elemShowname, DefaultScalePct, lay, name)
	textW := a.labelEmojiWidth(snFont, snEmoji, name, ColAccent)
	if textW <= 0 {
		t.Fatalf("the staged showname %q measures %d px — nothing below would be exercised", name, textW)
	}
	// A box the name overflows by a hair, and a rung small enough that ONE of them
	// is not enough and two are. That is the only geometry that tells the three
	// outcomes apart.
	const rung = 6
	if textW <= rung+1 {
		t.Fatalf("the staged showname measures only %d px — no box narrower than it exists", textW)
	}
	box := sdl.Rect{X: 100, Y: 50, W: textW - rung - 1, H: 15}
	lay.shownameExtra = rung

	base := pinFlatThemeArt(t, a, themeStemChatbox)

	// 1. Neither variant shipped — P5Theme, whose showname_extra_width is the
	//    corpus maximum at 225 and which AO2 still never widens.
	got, page := a.chatboxSkinLadder(box, lay, base, snFont, snEmoji, name, ColAccent)
	if got.W != box.W || page != base {
		t.Fatalf("with no variant art the box became %d px (want %d) and the skin swapped: %v",
			got.W, box.W, page != base)
	}

	// 2. med only.
	med := pinFlatThemeArt(t, a, chatboxRungMed.texStem())
	got, page = a.chatboxSkinLadder(box, lay, base, snFont, snEmoji, name, ColAccent)
	if got.W != box.W+rung {
		t.Fatalf("med rung widened the box to %d px, want %d", got.W, box.W+rung)
	}
	if page != med {
		t.Fatal("med rung did not swap the chatbox skin")
	}

	// 3. med + big: the name still overflows the med box, so the second rung fires.
	big := pinFlatThemeArt(t, a, chatboxRungBig.texStem())
	got, page = a.chatboxSkinLadder(box, lay, base, snFont, snEmoji, name, ColAccent)
	if got.W != box.W+2*rung {
		t.Fatalf("big rung widened the box to %d px, want %d", got.W, box.W+2*rung)
	}
	if page != big {
		t.Fatal("big rung did not swap the chatbox skin")
	}

	// 4. A theme that declares no extra_width keeps the base skin however much art
	//    it ships and however long the name is — AO2's own behaviour.
	off := *lay
	off.shownameExtra = 0
	got, page = a.chatboxSkinLadder(box, &off, base, snFont, snEmoji, name, ColAccent)
	if got.W != box.W || page != base {
		t.Fatalf("showname_extra_width = 0 still widened to %d px / swapped the skin (%v)", got.W, page != base)
	}

	// ...and a name that FITS never leaves the base rung, whatever is resident.
	wide := box
	wide.W = textW + 1
	got, page = a.chatboxSkinLadder(wide, lay, base, snFont, snEmoji, name, ColAccent)
	if got.W != wide.W || page != base {
		t.Fatalf("a name that fits took a rung anyway: %d px, swapped=%v", got.W, page != base)
	}
}

// TestChatboxSkinLadderProbesNothingPerMessage is the streaming-client contract,
// stated as a test rather than as a comment.
//
// The negative — "this theme has no med skin" — must be answered from state the
// theme apply already landed, with no lookup that could ever reach the disk or
// the network. themePage's first line is the themeTex map probe, so a stem that
// was never pinned costs one map read and never touches the store at all: the
// store generation, which every Get bumps recency against, does not move.
func TestChatboxSkinLadderProbesNothingPerMessage(t *testing.T) {
	a, cleanup := ladderHarness(t)
	defer cleanup()

	const name = "Furiously Long Showname"
	lay := &themeLayoutCache{valid: true, shownameExtra: 24, r: map[string]sdl.Rect{}}
	snFont, snEmoji := a.themedChatFace(elemShowname, DefaultScalePct, lay, name)
	base := pinFlatThemeArt(t, a, themeStemChatbox)
	box := sdl.Rect{X: 0, Y: 0, W: 4, H: 15} // guaranteed overflow

	// Warm the caches the measure rides, then prove a thousand "messages" cost no
	// allocation and no store activity.
	a.chatboxSkinLadder(box, lay, base, snFont, snEmoji, name, ColAccent)
	gen := a.d.Store.Generation()
	if n := testing.AllocsPerRun(1000, func() {
		a.chatboxSkinLadder(box, lay, base, snFont, snEmoji, name, ColAccent)
	}); n != 0 {
		t.Fatalf("the ladder allocates %.1f/op on a theme with no variant art, want 0 — "+
			"a per-message lookup shipped (fix it, do not loosen the gate)", n)
	}
	if got := a.d.Store.Generation(); got != gen {
		t.Fatalf("the texture store generation moved %d → %d — the ladder touched the store for a stem "+
			"the theme never shipped", gen, got)
	}
}

// TestDrawCourtroomThemedLadderZeroAlloc is the whole-screen gate with the ladder
// FIRING — the state a stock-theme player is in the moment somebody with a long
// showname speaks, and the only one of the themed gates that reaches the measure,
// the two variant probes and the skin swap on every frame.
//
// The other themed gates leave themeTex empty, so their chatbox is the flat panel
// and the ladder short-circuits on the first integer test. This one pins all three
// skins and gives the theme the stock 24 px rung, so the frame really does measure
// the name and blit a swapped page 200 times.
func TestDrawCourtroomThemedLadderZeroAlloc(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	// The two maps the real constructor creates together; stageThemedCourtroom's
	// chatbox is deliberately unskinned, so it never needed them.
	a.themeTex = map[string]bool{}
	a.themePages = map[string]*render.TexturePage{}
	for _, stem := range []string{themeStemChatbox, chatboxRungMed.texStem(), chatboxRungBig.texStem()} {
		pinFlatThemeArt(t, a, stem)
	}
	a.themeShownameExtra = 24 // the stock AO2 theme's own value
	a.themeShownameAlign = shownameAlignCenter
	a.themeLay.valid = false // rebake the rung height into the layout cache

	// The fixture's speaker has a five-letter showname, which fits the stock
	// theme's 46 px name box with room to spare — the ladder would never run. Give
	// the SAME character a long showname (msg.Showname, so every asset base the
	// fixture made resident stays exactly as it was) and settle it again.
	long := msgFor(0, "Witch", "settled line")
	long.Showname = "Beatrice the Golden Endless Witch"
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: long})
	a.room.SkipToIdle()

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	draw()
	if !a.themeLay.valid || !a.toolboxThemeRectOn {
		t.Fatal("the fixture did not reach the themed branch")
	}
	// Prove the ladder actually fires on this frame, or the gate passes vacuously.
	lay := &a.themeLay
	if lay.shownameExtraPx() <= 0 {
		t.Fatalf("the layout baked a %d px rung — the ladder is off", lay.shownameExtraPx())
	}
	name := a.room.Scene.ShownameText
	nameBox, _ := chatboxTextRects(mustRect(t, lay, "ao2_chatbox"), lay, 0)
	snFont, snEmoji := a.themedChatFace(elemShowname, DefaultScalePct, lay, name)
	if px := a.labelEmojiWidth(snFont, snEmoji, name, ColAccent); px <= nameBox.W {
		t.Fatalf("the staged showname %q measures %d px inside a %d px box — it never overflows, so no rung is taken",
			name, px, nameBox.W)
	}
	widened, page := a.chatboxSkinLadder(nameBox, lay, mustPage(t, a, themeStemChatbox), snFont, snEmoji, name, ColAccent)
	if widened.W <= nameBox.W || page == mustPage(t, a, themeStemChatbox) {
		t.Fatalf("the ladder did not fire: box %d → %d px, skin swapped %v", nameBox.W, widened.W, page != nil)
	}
	settle(draw)

	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled themed drawCourtroom on the med/big ladder allocates %.1f/op, want 0 — "+
			"the per-message skin decision is doing work it should not (fix the alloc, don't disarm the ladder)", n)
	}
}

func mustRect(t *testing.T, lay *themeLayoutCache, key string) sdl.Rect {
	t.Helper()
	r, ok := lay.rect(key)
	if !ok {
		t.Fatalf("the fixture layout has no %q rect", key)
	}
	return r
}

func mustPage(t *testing.T, a *App, stem string) *render.TexturePage {
	t.Helper()
	page, ok := a.themePage(stem)
	if !ok {
		t.Fatalf("theme stem %q is not resident", stem)
	}
	return page
}

// TestShownameLadderReachesBothDrawSites is the mirror-site guard (4.7e).
//
// The live themed chatbox and the video / comic export's themed chatbox draw the
// same theme's chatbox. If only one of them runs the ladder, a long showname
// renders on the wide plate on screen and spills off the narrow one in the
// exported file — which is exactly the class of drift chatboxTextRects was
// consolidated to end.
func TestShownameLadderReachesBothDrawSites(t *testing.T) {
	for _, file := range []string{"theme_layout.go", "gifexport.go"} {
		src, err := os.ReadFile(filepath.Join(".", file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(src), "chatboxSkinLadder(") {
			t.Errorf("%s draws a themed chatbox without running the showname widen-and-swap ladder — "+
				"the live chatbox and the export must agree on which skin a long name is on", file)
		}
	}
}
