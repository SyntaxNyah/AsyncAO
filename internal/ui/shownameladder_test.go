package ui

import (
	"go/ast"
	"go/token"
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

// TestChatboxBlankRungIsDistinctAndDeclinesADuplicate covers the fourth rung —
// AO2's plate-less `chatblank` skin — which rides the same enum as the two
// widening variants but answers both halves of a rung's identity differently.
//
// Its T1 stem is fixed like the others, but its FILE stem is not derived from the
// base skin: AO2 names it outright, `setImage("chatblank", p_misc)`
// (AO2-Client courtroom.cpp:3322), where med/big are string-appended to the
// resolved base's own path (courtroom.cpp:3358/:3362). The consequence is the case
// the other rungs cannot have: for the two reference themes whose BASE skin
// already resolved to chatblank, the blank rung is the base, so it declines with
// an empty stem rather than pinning the same art under a second key.
//
// The decline is driven through the loader's own lookup expression
// (app.go: theme.FindAssetIn(res.chatboxDir, rung.fileStem(res.chatboxStem), …)),
// not re-implemented here, and the fixture ships the `.png` dotfile that the empty
// stem would otherwise adopt — theme.TestFindAssetInRefusesAnEmptyStem is the same
// guard from the other side.
func TestChatboxBlankRungIsDistinctAndDeclinesADuplicate(t *testing.T) {
	// (1) Identity: four rungs, four distinct T1 keys. A collision would have one
	// rung's art overwrite another's in themeTex.
	stems := map[string]chatboxRung{}
	for _, r := range []chatboxRung{chatboxRungBase, chatboxRungMed, chatboxRungBig, chatboxRungBlank} {
		if other, dup := stems[r.texStem()]; dup {
			t.Fatalf("rungs %d and %d both pin under %q", other, r, r.texStem())
		}
		stems[r.texStem()] = r
	}
	if chatboxRungBlank.texStem() != themeStemChatboxBlank {
		t.Errorf("the blank rung must pin under %q, got %q", themeStemChatboxBlank, chatboxRungBlank.texStem())
	}
	// ...and the loader must actually reach it, or the stem is pinned by nobody.
	found := false
	for _, r := range chatboxLadderRungs {
		found = found || r == chatboxRungBlank
	}
	if !found {
		t.Error("chatboxLadderRungs no longer contains the blank rung — nothing loads chatblank and " +
			"chatboxSkinForShowname can never fire (narration posts go back to the empty name plate)")
	}

	// (2) The file stem is FIXED, whichever base spelling the theme shipped —
	// except when the base already is that file.
	for _, base := range []string{"chat", "chatbox"} {
		if got := chatboxRungBlank.fileStem(base); got != themeChatBlankFileStem {
			t.Errorf("blank file stem for base %q = %q, want AO2's literal %q", base, got, themeChatBlankFileStem)
		}
	}
	if got := chatboxRungBlank.fileStem(themeChatBlankFileStem); got != "" {
		t.Errorf("a theme whose base skin IS %q must decline the blank rung, got %q",
			themeChatBlankFileStem, got)
	}

	// (3) And the decline reaches the disk probe as "find nothing", including in a
	// directory that ships the dotfile an unguarded join would have produced.
	dir := t.TempDir()
	for _, name := range []string{"chatblank.png", ".png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := theme.FindAssetIn(dir, chatboxRungBlank.fileStem("chat"), themeImageExts); !ok {
		t.Fatal("a chat-based theme did not find chatblank.png beside its base skin — " +
			"the fixture is not exercising the probe at all")
	}
	if p, ok := theme.FindAssetIn(dir, chatboxRungBlank.fileStem(themeChatBlankFileStem), themeImageExts); ok {
		t.Errorf("the declining rung still resolved %q — the empty stem must not probe", p)
	}
}

// TestChatboxSkinForShownameFollowsTheTrimmedName drives AO2's OTHER per-message
// skin decision, the one the widen-and-swap ladder is not: a post whose showname
// trims to nothing wears the plate-less box (AO2-Client courtroom.cpp:3320-3331,
// `if (ui_vp_showname->text().trimmed().isEmpty())`).
//
// Both arms of AO2's branch are here, and so are the two states a streaming client
// adds: a theme that ships no blank art at all (most of them) must be byte-identical
// to the pre-fix draw, and the decision must stay free per frame — it runs on every
// themed chatbox frame, not once per message.
func TestChatboxSkinForShownameFollowsTheTrimmedName(t *testing.T) {
	a, cleanup := ladderHarness(t)
	defer cleanup()

	base := pinFlatThemeArt(t, a, themeStemChatbox)

	// 1. No blank art pinned — the corpus majority, and the two themes whose base
	//    skin IS chatblank (the rung declined, so nothing was pinned under the blank
	//    stem). Every showname, blank or not, keeps the base page.
	for _, name := range []string{"", "   ", "Phoenix"} {
		if page, blank := a.chatboxSkinForShowname(name, base); page != base || blank {
			t.Errorf("showname %q with no blank art: page swapped=%v blank=%v, want the base page untouched",
				name, page != base, blank)
		}
	}

	// 2. With the blank skin resident, the trimmed-empty names take it and only them.
	blankPage := pinFlatThemeArt(t, a, themeStemChatboxBlank)
	if blankPage == base {
		t.Fatal("the fixture pinned one page under both stems — nothing below could tell them apart")
	}
	for _, tc := range []struct {
		name      string
		showname  string
		wantBlank bool
	}{
		{"a narration post", "", true},
		{"spaces only, like QString::trimmed()", "   ", true},
		{"tabs and newlines are whitespace too", "\t\n", true},
		// TrimSpace's cut set is Unicode space where QString::trimmed() is ASCII
		// only: U+3000 reads blank here and would not in AO2. Recorded as the
		// deliberate deviation chatboxfit.go documents, pinned so it stays one.
		{"an ideographic space (documented deviation)", "　", true},
		{"an ordinary speaker", "Phoenix", false},
		{"a padded name is still a name", "  Phoenix  ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, blank := a.chatboxSkinForShowname(tc.showname, base)
			if blank != tc.wantBlank {
				t.Fatalf("showname %q: blank=%v, want %v", tc.showname, blank, tc.wantBlank)
			}
			want := base
			if tc.wantBlank {
				want = blankPage
			}
			if page != want {
				t.Fatalf("showname %q returned the wrong page (blank art = %v)", tc.showname, page == blankPage)
			}
		})
	}

	// 3. Per FRAME, not per message: the draw sites call this inside drawCourtroom,
	//    so it must cost nothing. themePage's first line is a map probe.
	a.chatboxSkinForShowname("", base) // warm the themePages entry
	if n := testing.AllocsPerRun(1000, func() {
		a.chatboxSkinForShowname("", base)
		a.chatboxSkinForShowname("Phoenix", base)
	}); n != 0 {
		t.Errorf("the per-message skin pick allocates %.1f/op, want 0", n)
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
	if n := allocsPerFrame(allocGateFrames, 0, draw); n != 0 {
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

// ---------------------------------------------------------------------------
// The blank-skin selection's mirror-site guard.
// ---------------------------------------------------------------------------

// chatboxBlankSkinFn is the seam every themed chatbox blit has to go through, and
// the name the census below hunts for in the sources.
const chatboxBlankSkinFn = "chatboxSkinForShowname"

// chatboxBlankSkinArg is the field a live site must hand it: the SCENE's showname
// for the message currently up.
//
// Pinned as the selector's last segment, not as `sc.ShownameText`, so renaming the
// local variable is free and only a site that stops feeding the per-message name
// fails. That is the failure worth catching: AO2's branch is per MESSAGE
// (courtroom.cpp:3320), and a site that passed a constant, a theme-level name, or
// the character's canonical name would compile, draw, and be wrong on exactly the
// posts the fix was for.
const chatboxBlankSkinArg = "ShownameText"

// chatboxBlankSkinSite is one LIVE draw site that blits a themed chatbox skin, and
// the reason it is on this list. Each entry IS the audit.
type chatboxBlankSkinSite struct {
	file string
	fn   string
	why  string
}

// chatboxBlankSkinSites is every place on screen a themed chatbox skin reaches the
// renderer. Both wear the theme's art, so both own AO2's per-message choice between
// the ordinary skin and the plate-less one.
//
// gifexport.go's drawGifThemedChatbox is the THIRD site that blits this art and it
// is deliberately absent: it does not make the selection today, which is a live
// defect (found in this lane's review pass) owned outside this file's territory.
// Listing it now would fail this gate for a reason no edit here can fix. When the
// export path is fixed it belongs on this census, exactly as it is already on
// TestShownameLadderReachesBothDrawSites' list.
var chatboxBlankSkinSites = []chatboxBlankSkinSite{
	{
		file: "theme_layout.go",
		fn:   "drawThemedChatBox",
		why:  "the themed courtroom's own chatbox: the theme declared an ao2_chatbox rect and this draws into it",
	},
	{
		file: "screens.go",
		fn:   "drawChatOverlay",
		why:  "the classic overlay chatbox, which still wears the THEME's skin whenever the theme shipped one",
	},
}

// TestChatboxBlankSkinReachesEveryLiveDrawSite is the deletion-catcher for the
// per-message blank-skin selection, and it exists because there was not one.
//
// TestChatboxSkinForShownameFollowsTheTrimmedName drives the function and
// TestChatboxBlankRungIsDistinctAndDeclinesADuplicate drives the loader that makes
// its art resident, but neither touches the two places that actually CALL it: both
// calls could be deleted and the whole package would stay green while every
// narration post went back to wearing the ordinary skin's empty name plate — the
// precise defect the seam was added to end (AO2-Client courtroom.cpp:3320-3331).
// Hard rule 11 asks for a test that proves a seam cannot be bypassed or deleted
// while the suite stays green; this is it, in the shape srcgate_test.go documents
// (a deletion-catcher over production sources, containing no copy of the logic).
//
// Three properties per site, all of them regressions somebody could ship without
// the compiler noticing: the call is THERE, it is fed the live per-message
// showname, and its answer is not thrown away.
//
// The third one is the one that needs teeth, because Go gives "thrown away" three
// spellings and only the loudest of them is a bare statement. A multi-value call is
// a legal ExprStmt; `_, _ = f(...)` is a legal assignment; and an assignment whose
// target is never read again afterwards is legal too — all three compile, all three
// draw the pre-selection page, and all three used to pass this gate. So the property
// is stated positively and structurally instead: the call must be the RHS of an
// assignment whose FIRST target is a named variable, and that variable must be READ
// somewhere after the call in the same body. That is what "the chosen page is the one
// that gets blitted" means in source terms, and it stays site-agnostic — it never has
// to name Copy/CopyEx, and it is not fooled by drawChatOverlay's earlier charSkin
// blit, which happens before the selection and reads a different variable.
func TestChatboxBlankSkinReachesEveryLiveDrawSite(t *testing.T) {
	for _, site := range chatboxBlankSkinSites {
		t.Run(site.file+"/"+site.fn, func(t *testing.T) {
			body := funcBodySource(t, site.file, site.fn)

			var calls []*ast.CallExpr
			ast.Inspect(body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && callName(call) == chatboxBlankSkinFn {
					calls = append(calls, call)
				}
				return true
			})
			if len(calls) == 0 {
				t.Fatalf("%s (%s) blits a themed chatbox skin without asking %s which skin THIS MESSAGE wears — "+
					"a post with a blank showname goes back to the ordinary skin's empty name plate "+
					"(AO2-Client courtroom.cpp:3320-3331). The selection is per message, so every draw site owns it.",
					site.fn, site.why, chatboxBlankSkinFn)
			}
			for _, call := range calls {
				if !callReadsField(call, chatboxBlankSkinArg) {
					t.Errorf("%s calls %s without passing the scene's .%s — the skin would follow something "+
						"other than the showname this message actually posted with",
						site.fn, chatboxBlankSkinFn, chatboxBlankSkinArg)
				}
				// (a) The chosen page has to land in a variable. A bare statement or a
				// `_, _ =` assignment compiles and draws whatever page the code already
				// had, which is precisely the empty name plate this seam exists to end.
				target, ok := assignedFirstResult(body, call)
				if !ok {
					t.Errorf("%s discards what %s returned — the chosen page has to be assigned to a variable "+
						"and blitted, or the selection is decoration", site.fn, chatboxBlankSkinFn)
					continue
				}
				// (b) ...and that variable has to be USED after the call. Go does not
				// require an assigned value to be read, so hoisting the blit above the
				// selection (or leaving the assignment stranded) also compiles, also
				// draws the pre-selection page, and is invisible to (a).
				if !identReadAfter(body, target.Name, call.End()) {
					t.Errorf("%s assigns %s's answer to %q and never reads it again — the blit is running on the "+
						"page chosen BEFORE the selection, so a blank-showname post keeps the ordinary skin's "+
						"empty name plate (AO2-Client courtroom.cpp:3320-3331)",
						site.fn, chatboxBlankSkinFn, target.Name)
				}
			}
		})
	}
}

// assignedFirstResult returns the identifier call's FIRST result is assigned to.
//
// It reports false for every spelling of "the answer went nowhere": a bare
// statement (no assignment at all), a call used as an argument to something else,
// and an assignment whose first target is the blank identifier. Only the first
// target matters — this seam's second result is the boolean the draw sites may
// legitimately ignore; the PAGE is the one that has to survive.
func assignedFirstResult(body *ast.BlockStmt, call *ast.CallExpr) (*ast.Ident, bool) {
	var target *ast.Ident
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || target != nil {
			return target == nil
		}
		// The call must BE the right-hand side, not merely contain it: `x, _ =
		// wrap(f(...))` hands the wrapper's answer on, and whether the wrapper
		// respects the choice is beyond what a source gate can see.
		if len(as.Rhs) != 1 || unparen(as.Rhs[0]) != ast.Expr(call) {
			return true
		}
		if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
			target = id
		}
		return false
	})
	return target, target != nil
}

// identReadAfter reports whether the variable named name is READ anywhere in body
// at a position after pos.
//
// Assignment targets do not count as reads — `page = somethingElse` is the code
// throwing the selection away, not consuming it. Compound targets (`x += …`) are
// left alone deliberately: those do read.
//
// The match is by NAME, not by resolved object: go/ast's own resolution is
// deprecated and a type-checked pass would drag the whole package's imports into a
// source gate. The residual is a variable of the same name shadowing in an inner
// scope BELOW the call and being read there — which cannot happen by accident,
// because the shadow's own `:=` target is excluded above as a write, so someone
// would have to declare a second `page` after the selection and read it instead.
func identReadAfter(body *ast.BlockStmt, name string, pos token.Pos) bool {
	writes := map[*ast.Ident]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || (as.Tok != token.ASSIGN && as.Tok != token.DEFINE) {
			return true
		}
		for _, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok {
				writes[id] = true
			}
		}
		return true
	})
	read := false
	ast.Inspect(body, func(n ast.Node) bool {
		if read {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name && id.Pos() > pos && !writes[id] {
			read = true
		}
		return !read
	})
	return read
}

// unparen strips redundant parentheses so `(f(x))` is recognised as the call it is.
func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// callReadsField reports whether any argument of call reads the named struct field
// (the last segment of a selector), however deeply it is nested inside the
// expression — so `sc.ShownameText`, `a.room.Scene.ShownameText` and
// `strings.TrimSpace(sc.ShownameText)` all count, and a literal does not.
func callReadsField(call *ast.CallExpr, field string) bool {
	for _, arg := range call.Args {
		found := false
		ast.Inspect(arg, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}
