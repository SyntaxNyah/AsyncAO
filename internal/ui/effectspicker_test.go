package ui

import (
	"image"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// stampOpenDropdown fakes what dropdownEx stamps for an OPEN list, so the
// right-click hit test can be exercised without an SDL renderer.
func stampOpenDropdown(c *Ctx, id string, list sdl.Rect, rowH int32, rows int, scroll int32) {
	c.ddOpen = id
	c.ddHitID, c.ddHitList, c.ddHitRowH, c.ddHitRows, c.ddHitScroll = id, list, rowH, rows, scroll
}

// TestDropdownRowRightClicked pins the ONE additive Ctx method this feature
// added: it answers the row under the pointer for the OPEN list of `id`, and
// CONSUMES the click so it cannot leak to the widget under the list.
func TestDropdownRowRightClicked(t *testing.T) {
	list := sdl.Rect{X: 100, Y: 200, W: 120, H: 120}
	c := &Ctx{mouseX: 150, mouseY: 260} // 60px into the list → row 2 at rowH 30
	stampOpenDropdown(c, "effectsdd", list, 30, 4, 0)

	if _, ok := c.DropdownRowRightClicked("effectsdd"); ok {
		t.Fatal("no right-click this frame: nothing may fire")
	}
	c.rightClicked = true
	row, ok := c.DropdownRowRightClicked("effectsdd")
	if !ok || row != 2 {
		t.Fatalf("row = %d ok = %v, want row 2", row, ok)
	}
	if c.rightClicked {
		t.Error("the click must be CONSUMED — an unconsumed one leaks past the modal capture (app.go's pointer fence)")
	}

	// A different id must never answer for this list.
	c.rightClicked = true
	if _, ok := c.DropdownRowRightClicked("sfxdd"); ok {
		t.Error("a stale stamp answered for another dropdown id")
	}
	// Outside the list rect: not our click.
	c.mouseY = 900
	if _, ok := c.DropdownRowRightClicked("effectsdd"); ok {
		t.Error("a right-click outside the open list must not select a row")
	}
	// A CLOSED list never answers.
	c.mouseY = 260
	c.ddOpen = ""
	if _, ok := c.DropdownRowRightClicked("effectsdd"); ok {
		t.Error("a closed dropdown has no rows to right-click")
	}
	// Scrolled lists offset the row index by the scroll, exactly like the
	// left-click hit test inside dropdownEx.
	stampOpenDropdown(c, "effectsdd", list, 30, 10, 60)
	c.rightClicked = true
	if row, ok = c.DropdownRowRightClicked("effectsdd"); !ok || row != 4 {
		t.Errorf("scrolled row = %d ok = %v, want 4", row, ok)
	}
	// An out-of-range index (a click in the list's dead tail) is refused, and the
	// click is NOT consumed — nothing happened, so nothing is swallowed.
	stampOpenDropdown(c, "effectsdd", list, 30, 1, 0)
	c.rightClicked = true
	if _, ok = c.DropdownRowRightClicked("effectsdd"); ok {
		t.Error("a click past the last row must not select one")
	}
	if !c.rightClicked {
		t.Error("a refused hit must leave the click for whoever else wants it")
	}
}

// TestEffectsDropdownSlotIsNoLongerInert — themeslots census: the key now has a
// painter (drawEffectsPicker), so an inert row would make it undraggable in the
// layout editor and invisible to the unbound-key report.
func TestEffectsDropdownSlotIsNoLongerInert(t *testing.T) {
	s := themeSlotFor("effects_dropdown")
	if s == nil {
		t.Fatal("effects_dropdown lost its themeSlots row")
	}
	if s.state == slotStateInert {
		t.Error("effects_dropdown is drawn now (theme_layout.go drawEffectsPicker) — an inert row is the music_search ghost-box bug")
	}
}

// TestOverlayThemeKeysCannotCollideWithAssetURLs — theme-tier overlay art lives
// under the theme:// scheme, which no asset URL can spell, in its own sub-
// namespace so an effect called "chat" cannot shadow the chatbox skin.
func TestOverlayThemeKeysCannotCollideWithAssetURLs(t *testing.T) {
	key := overlayThemeKey(`C:\themes\mytheme\effects\Realization.webp`)
	if !strings.HasPrefix(key, overlayThemeKeyPrefix) {
		t.Fatalf("key = %q, want the %q prefix", key, overlayThemeKeyPrefix)
	}
	if key == themeTexKey("chat") {
		t.Fatal("an overlay key collided with a theme stem key")
	}
	if strings.HasPrefix(themeTexKey("chat"), overlayThemeKeyPrefix) {
		t.Fatal("the sub-namespace is not distinct from the plain theme stems")
	}
	// The key is case-folded: Windows paths differ only by case for the same file,
	// and two keys for one file would double the T1 cost of every overlay.
	if overlayThemeKey("/A/B.PNG") != overlayThemeKey("/a/b.png") {
		t.Error("overlay keys must fold case so one file is one page")
	}
}

// TestOverlayRosterKeyIsPerOrigin — cache keys carry the host, so per-server
// separation is structural (the repo's cache-key convention).
func TestOverlayRosterKeyIsPerOrigin(t *testing.T) {
	a := &App{}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	first := a.overlayRosterKey("Transitions")
	a.urls = courtroom.NewURLBuilder("https://two.test/base")
	if second := a.overlayRosterKey("Transitions"); second == first {
		t.Error("two origins produced the same roster key — one server's pack could answer for another's")
	}
	if a.overlayRosterKey("PACK") != a.overlayRosterKey("pack") {
		t.Error("the folder is case-folded: AO's local filesystem is case-insensitive and mirrors disagree")
	}
}

// TestOverlayChoicesPrependNoneAndPreserveDuplicates — get_effects parity: the
// list is theme-file ++ misc-file, duplicates preserved and never re-sorted,
// with AO2's "None" row prepended at index 0 (courtroom.cpp:5600-5604).
func TestOverlayChoicesPrependNoneAndPreserveDuplicates(t *testing.T) {
	a := &App{}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey(""): {
			set: theme.MergeOverlayFX([]string{"flash", "realization", "realization", "tv on"})},
	}
	got := a.overlayChoices()
	want := []string{overlayNoneRow, "flash", "realization", "realization", "tv on"}
	if len(got) != len(want) {
		t.Fatalf("choices = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("choices = %v, want %v (no dedupe — the wire carries the NAME, not the index)", got, want)
		}
	}
	// An empty roster yields nothing at all, so the control never draws (AO2 hides
	// ui_effects_dropdown when the list is empty, courtroom.cpp:5586-5598).
	a.overlayRosters[a.overlayRosterKey("")] = &overlayRoster{set: theme.MergeOverlayFX(nil)}
	a.overlayInvalidateMemos() // the poll does this for real; here we mutate the map directly
	if got := a.overlayChoices(); len(got) != 0 {
		t.Errorf("an empty roster must yield no choices, got %v", got)
	}
}

// TestOutgoingEffectsFieldShape — "<name>|<folder>|<sound>", "" for None, and
// AO2's anti-overlap rule: Pre unchecked + an SFX override forces sound "0"
// (courtroom.cpp:2302-2305).
func TestOutgoingEffectsFieldShape(t *testing.T) {
	// REAL shipped preferences, not a bare struct: the sticky assertion below is
	// about AO2's own default value, so reading it out of a zero-valued
	// AssetPreferences would pin `false` and keep passing whatever
	// defaultStickyEffects actually says.
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	fx := []theme.OverlayFX{{Name: "tv on", Sound: "switch tv"}, {Name: "sticky one", Sound: "s", Sticky: true}}
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey(""): {
			set: theme.MergeOverlayFX([]string{"tv on", "sticky one"}, fx)},
	}
	a.icPreanim = true

	a.overlayChoiceIdx = 0
	if got := a.outgoingEffectsField(); got != "" {
		t.Errorf("the None row must send %q, got %q", "", got)
	}
	a.overlayChoiceIdx = 1
	if got := a.outgoingEffectsField(); got != "tv on||switch tv" {
		t.Errorf("field = %q, want \"tv on||switch tv\"", got)
	}
	// Pre unchecked AND an explicit SFX override: the effect's own sound is
	// muted so the two do not overlap.
	a.icPreanim, a.sfxChoiceIdx = false, 3
	if got := a.outgoingEffectsField(); got != "tv on||0" {
		t.Errorf("field = %q, want the anti-overlap \"0\" sound", got)
	}
	a.icPreanim, a.sfxChoiceIdx = true, 0

	// AO2's shipped default is `stickyeffects = true` (options.cpp:430-433) and
	// courtroom.cpp:2348 resets ONLY when that is false — so a stock client keeps
	// the pick after a send whatever the effect declares.
	a.overlayChoiceIdx = 1
	a.clearEffectsAfterSend()
	if a.overlayChoiceIdx != 1 {
		t.Error("the shipped default is sticky: the pick must survive the send (options.cpp:430-433)")
	}
	// With the (OWED) global turned off, the per-effect `sticky` override is what
	// decides — the other half of courtroom.cpp:2348-2354.
	a.overlayChoiceIdx = 2
	a.overlayPostSendReset(false)
	if a.overlayChoiceIdx != 2 {
		t.Error("a sticky effect must survive the send (courtroom.cpp:2348-2354)")
	}
	a.overlayChoiceIdx = 1
	a.overlayPostSendReset(false)
	if a.overlayChoiceIdx != 0 {
		t.Error("a non-sticky effect must clear to None once the global is off")
	}
}

// TestEffectsDropdownHiddenWhenSpectating (courtroom.cpp:5586-5598: m_cid == -1).
func TestEffectsDropdownHiddenWhenSpectating(t *testing.T) {
	a := &App{}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey(""): {set: theme.MergeOverlayFX([]string{"flash"})},
	}
	if a.overlayEffectsVisible() {
		t.Error("with no session there is nothing to send an effect on")
	}
	a.sess = &courtroom.Session{}
	a.sess.MyCharID = -1
	if a.overlayEffectsVisible() {
		t.Error("a SPECTATOR must not see the effects dropdown")
	}
	a.sess.MyCharID = 3
	if !a.overlayEffectsVisible() {
		t.Error("a seated player with a non-empty roster must see it")
	}
}

// TestOverlayResolveNeedsAnOriginAndAManager — the ORIGIN rung (8, the only
// network rung) may be walked only when there IS an origin and a Manager to ask
// with, the guard every other origin-dependent path in this package carries
// (charMetaFor charmeta.go:58, previewSFX sfxbrowser.go:56, warmCharINI
// app.go:7510, overlayRosterFetchOne above).
//
// Without it, an origin-less URLBuilder hands back the RELATIVE
// "misc/<folder>/<name>": that would be submitted to the worker pool (the manager
// rejects only an EMPTY base) and stored as Scene.Overlay.Base — a permanent junk
// T1 key that can never resolve. It is reachable today, not theoretical: App.urls
// is the ZERO value until a connect assigns one (app.go:4757), so a .demo replayed
// from the LOBBY resolves effects through an origin-less builder.
func TestOverlayResolveNeedsAnOriginAndAManager(t *testing.T) {
	landed := func(a *App) {
		a.overlayRosters = map[string]*overlayRoster{
			a.overlayRosterKey("transitions"): {
				// Properties but NO local art: rung 8 is the only one left, which is
				// exactly the shape that leaked.
				set:  theme.MergeOverlayFX([]string{"tv on"}, []theme.OverlayFX{{Name: "tv on", Loop: true}}),
				art:  map[string]string{},
				icon: map[string]string{},
			},
		}
	}

	lobby := &App{} // no connect yet: urls is the zero value
	landed(lobby)
	if lobby.urls.Origin() != "" {
		t.Fatal("fixture: a lobby App must have no origin")
	}
	// The space is escaped per SEGMENT (segPath, urlbuilder.go:134) — the sibling
	// TestOverlayEffectMiscCandidates pins the same %20 form for a real origin. The
	// fixture name KEEPS its space precisely so this guards the escaping too: a
	// whole-value PathEscape would mint "misc%2Ftransitions%2Ftv%20on" (#40).
	if got := lobby.urls.OverlayEffectMisc("transitions", "tv on"); len(got) == 0 || got[0] != "misc/transitions/tv%20on" {
		t.Fatalf("candidates = %v — this test exists BECAUSE an origin-less builder yields a relative url", got)
	}
	res, ok := lobby.overlayResolve("tv on", "transitions")
	if ok {
		t.Error("with no origin there is no art anywhere: canon clears (courtroom.cpp:3188-3194)")
	}
	if res.Base != "" {
		t.Errorf("Base = %q — a relative url must never be armed as a T1 key or probed", res.Base)
	}

	// An origin but no Manager: nil-checked everywhere else in this package
	// (mountlayer.go:37,75; scenemaker.go:276) and a nil-Manager PrefetchChain would
	// panic on the render thread.
	noMgr := &App{}
	noMgr.urls = courtroom.NewURLBuilder("https://one.test/base")
	landed(noMgr)
	if res, ok = noMgr.overlayResolve("tv on", "transitions"); ok || res.Base != "" {
		t.Errorf("resolve with no Manager: ok = %v base = %q, want a clear", ok, res.Base)
	}
}

// TestOverlayIconBakeNeedsAnOrigin — the dropdown's row ICONS are baked from the
// same rung 8 as the art (bakeOverlayIconKeys), so they carry the same origin
// guard as the resolve above, and for the same reason: an origin-less URLBuilder
// hands back the RELATIVE "misc/<folder>/icons/<name>", which the icon painter
// would arm as a T1 page key and submit to the worker pool. That key can never
// resolve, and it is baked ONCE per roster and read on every frame the picker
// draws, so it would sit there for the life of the session.
//
// Reachable, not theoretical, in exactly the way the resolve's guard is:
// App.urls is the ZERO value until a connect assigns one, so a .demo replayed
// from the LOBBY bakes its icon keys through an origin-less builder.
func TestOverlayIconBakeNeedsAnOrigin(t *testing.T) {
	// Properties but NO local icon, so the origin rung is the only one left —
	// exactly the shape the guard is about.
	roster := &overlayRoster{
		set:  theme.MergeOverlayFX([]string{"tv on"}, []theme.OverlayFX{{Name: "tv on"}}),
		art:  map[string]string{},
		icon: map[string]string{},
	}
	const folder = "transitions"
	rows := []string{overlayNoneRow, "tv on"}

	lobby := &App{} // no connect yet: urls is the zero value
	lobby.overlayRosters = map[string]*overlayRoster{lobby.overlayRosterKey(""): roster}
	if lobby.urls.Origin() != "" {
		t.Fatal("fixture: a lobby App must have no origin")
	}
	// The roster really does reach the dropdown — the rows the bake is indexed by
	// are the rows overlayChoices publishes.
	if got := lobby.overlayChoices(); len(got) != len(rows) {
		t.Fatalf("choices = %v, want %q plus the one effect", got, overlayNoneRow)
	}
	// Driven with an effects FOLDER, because a lobby App's character has none and
	// an empty folder yields no candidates for any origin — that would test the
	// wrong thing.
	lobby.overlayChoicesCache = rows
	lobby.bakeOverlayIconKeys(folder, roster)
	if len(lobby.overlayIconKeys) != len(rows) {
		t.Fatalf("baked %d icon keys for %d rows — the keys are indexed BY ROW", len(lobby.overlayIconKeys), len(rows))
	}
	for i, k := range lobby.overlayIconKeys {
		if k != "" {
			t.Errorf("row %d baked %q with no origin — a relative url is a permanent junk T1 key and a probe that can never resolve", i, k)
		}
	}

	// The same call WITH an origin proves the fixture would otherwise bake
	// something: the guard has to skip the key, not the feature.
	live := &App{}
	live.urls = courtroom.NewURLBuilder("https://one.test/base")
	live.overlayChoicesCache = rows
	live.bakeOverlayIconKeys(folder, roster)
	if live.overlayIconKeys[0] != "" {
		t.Errorf("the None row baked %q — AO2 asks for its icon too, but no theme in the corpus ships one, so it must not burn a probe per open", live.overlayIconKeys[0])
	}
	if !strings.HasPrefix(live.overlayIconKeys[1], "https://one.test/base") {
		t.Errorf("row 1 key = %q, want an absolute url built off the origin", live.overlayIconKeys[1])
	}
}

// effectPreviewApp is the shared fixture for the two preview tests: one landed
// roster with LOCAL art (so the resolve needs no network) whose effect declares
// KFO's `sound = 0` no-sound idiom.
func effectPreviewApp(t *testing.T) (*App, []string) {
	t.Helper()
	a := &App{ctx: &Ctx{}}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey(""): {
			set:  theme.MergeOverlayFX([]string{"tv on"}, []theme.OverlayFX{{Name: "tv on", Sound: "0"}}),
			art:  map[string]string{"tv on": "/themes/t/effects/tv on.webp"},
			icon: map[string]string{},
		},
	}
	choices := a.overlayChoices()
	if len(choices) != 2 {
		t.Fatalf("fixture: choices = %v, want None + one effect", choices)
	}
	return a, choices
}

// TestEffectPreviewFiltersTheSoundSentinels — the preview plays the effect's own
// sound through the SAME filter the message path uses. previewSFX rejects only ""
// and the empty origin, so an unfiltered preview mints urls.SFX("0") — a real
// probe plus a 404-cache entry per format in the SFX fallback chain, per row
// right-clicked. That is the exact probe spec §1.b told this wave to guard.
//
// The fixture has NO audio engine, so a regression panics here rather than
// silently costing probes (the nil-Manager idiom of the resolve test above).
func TestEffectPreviewFiltersTheSoundSentinels(t *testing.T) {
	a, choices := effectPreviewApp(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the preview played the `sound = 0` sentinel (KFO's no-sound idiom): %v", r)
		}
	}()
	a.previewOverlayRow(choices, 1)
	if want := overlayThemeKey("/themes/t/effects/tv on.webp"); a.previewBase != want {
		t.Errorf("previewBase = %q, want the effect's own art %q", a.previewBase, want)
	}
	// A preview is an audition: it commits nothing.
	if a.overlayChoiceIdx != 0 || a.sfxChoiceIdx != 0 {
		t.Errorf("the preview changed a selection: effect = %d sfx = %d", a.overlayChoiceIdx, a.sfxChoiceIdx)
	}
}

// TestPreviewClearsWhenTheDropdownCloses (spec §1.d) — the preview box is PINNED
// while the list is open (the modal capture blanks hovering(), so
// closeSpritePreviewOnLeave would otherwise kill it on the next frame), which
// means it must be taken down on the close EDGE. Without that the box outlives the
// dropdown, the message and the rest of the session, with only its own x to close
// it.
func TestPreviewClearsWhenTheDropdownCloses(t *testing.T) {
	a, choices := effectPreviewApp(t)
	a.ctx.ddOpen = overlayEffectsDDID
	a.previewOverlayRow(choices, 1)
	if a.previewBase == "" || !a.previewPinned {
		t.Fatalf("fixture: the preview must open pinned, base = %q pinned = %v", a.previewBase, a.previewPinned)
	}

	a.pollOverlayRosters() // the per-frame home of the close edge
	if a.previewBase == "" {
		t.Fatal("the box must survive while the list is OPEN — auditioning a second row re-points it")
	}

	a.ctx.ddOpen = "" // click-away / pick / toggle shut: dropdownEx clears it
	a.pollOverlayRosters()
	if a.previewBase != "" {
		t.Errorf("previewBase = %q — the preview must clear when the dropdown closes (spec 1.d)", a.previewBase)
	}
	if a.previewPinned {
		t.Error("the pin must go with the box, or the next hover preview can never leave-close")
	}
	if a.overlayPreviewBase != "" {
		t.Error("the ownership record must be dropped with the box")
	}

	// Ownership: a box some OTHER trigger re-pointed since is not ours to close.
	a.ctx.ddOpen = overlayEffectsDDID
	a.previewOverlayRow(choices, 1)
	a.previewBase = "characters/phoenix/(a)normal" // an emote-grid hover took the shared slot
	a.ctx.ddOpen = ""
	a.pollOverlayRosters()
	if a.previewBase != "characters/phoenix/(a)normal" {
		t.Error("the close edge closed a box it does not own (previewBase is one shared slot)")
	}
	if a.overlayPreviewBase != "" {
		t.Error("the claim must still be dropped even when the box moved on")
	}
}

// TestOverlayResolveDefersWhileTheRosterIsInFlight — the roster carries the LOCAL
// ladder (rungs 1-7, resolved off-thread from disk) AND the merged properties, so
// answering before it lands would (a) spend the one network rung on art the theme
// may already hold — spec §1.f, "rung 8 is the ONLY network rung", reached only
// when the whole local ladder misses — and (b) arm the effect with a zero-valued
// OverlayFX nothing would ever correct. That is not a rare path: a 3-part wire
// field carries the folder, so it is the FIRST message from any character
// shipping its own misc effects pack.
//
// The App here has no Manager at all, so a regression that falls through to
// PrefetchChain fails loudly rather than silently costing two probes.
func TestOverlayResolveDefersWhileTheRosterIsInFlight(t *testing.T) {
	a := &App{}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	// "Resolving": a nil answer from overlayRosterFor. Staged as an unsettled cache
	// entry rather than through overlayRosterInFlight so this pins the ANSWER the
	// resolver gives the courtroom, whichever way the marker happens to be stored.
	a.overlayRosters = map[string]*overlayRoster{a.overlayRosterKey("transitions"): nil}
	res, ok := a.overlayResolve("tv on", "transitions")
	if !ok {
		t.Fatal("an in-flight roster must not report a MISS — that clears the overlay and loses the effect outright")
	}
	if !res.Pending {
		t.Error("an in-flight roster must report Pending so the courtroom re-asks")
	}
	if res.Base != "" {
		t.Errorf("Base = %q — nothing may be armed or probed before the local ladder has been consulted", res.Base)
	}
	if res.FX != (theme.OverlayFX{}) {
		t.Error("a deferred resolve must not hand back properties it has not read")
	}
	// Once it lands, the LOCAL art wins with no network rung at all.
	a.overlayRosters[a.overlayRosterKey("transitions")] = &overlayRoster{
		set:  theme.MergeOverlayFX([]string{"tv on"}, []theme.OverlayFX{{Name: "tv on", Loop: true}}),
		art:  map[string]string{"tv on": "/themes/t/effects/tv on.webp"},
		icon: map[string]string{},
	}
	a.overlayInvalidateMemos() // the poll does this for real
	res, ok = a.overlayResolve("tv on", "transitions")
	if !ok || res.Pending {
		t.Fatal("a landed roster must answer, not defer")
	}
	if res.Base != overlayThemeKey("/themes/t/effects/tv on.webp") {
		t.Errorf("Base = %q, want the theme-tier T1 key — rungs 1-7 come before the origin", res.Base)
	}
	if !res.FX.Loop {
		t.Error("the landed answer must carry the file's merged properties")
	}
}

// --- spec §4.4 / §4.5 gates: allocation, demand, staleness -------------------

// drawableEffectsApp stages a real Ctx + TextureStore over a landed one-effect
// roster whose icon page is RESIDENT — exactly the state the CLOSED dropdown
// control repaints on every settled frame once an effect has been picked.
func drawableEffectsApp(t *testing.T, iconPath string) *App {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	t.Cleanup(cleanup)
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a := &App{ctx: ctx}
	a.d.Store = store
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey(""): {
			set:  theme.MergeOverlayFX([]string{"tv on"}, []theme.OverlayFX{{Name: "tv on"}}),
			art:  map[string]string{},
			icon: map[string]string{"tv on": iconPath},
		},
	}
	dec := &assets.Decoded{
		Frames: []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 8, 8))},
		Delays: []time.Duration{0},
		Width:  8, Height: 8,
	}
	if err := store.Upload(overlayThemeKey(iconPath), dec); err != nil {
		t.Fatalf("upload the icon page: %v", err)
	}
	return a
}

// TestEffectsDropdownClosedIsZeroAlloc is spec §3's "closed dropdown and
// no-effect frames must be byte-identical to today", measured where it bites.
//
// dropdownEx paints the CLOSED control's current pick through the SAME thumb
// painter on every frame (ui.go:3938-3942) — the open list is the exceptional
// case, the closed chip is the steady state. So from the moment a user picks an
// effect, overlayIconDraw runs once per courtroom frame forever, and the version
// that built its page key inside the painter (overlayThemeKey's concatenation, or
// URLBuilder.OverlayEffectMiscIcon's candidate SLICE, or &tr into cgo) allocated
// on every one of them — under the whole-screen courtroom alloc gate.
//
// The measured surface is the two calls dropdownEx makes per frame for a closed
// thumbnail control: the option list and the current row's thumb. Drawing the
// whole picker would drag in the kit's text path, which has its own gates.
func TestEffectsDropdownClosedIsZeroAlloc(t *testing.T) {
	const iconPath = "/themes/t/effects/icons/tv on.webp"
	a := drawableEffectsApp(t, iconPath)

	choices := a.overlayChoices()
	if len(choices) != 2 {
		t.Fatalf("fixture: choices = %v, want None + one effect", choices)
	}
	// The keys must be BAKED, not built in the painter — that is the fix, and
	// allocation counts alone would not say which half moved.
	if got, want := a.overlayIconKeys[1], overlayThemeKey(iconPath); got != want {
		t.Fatalf("row 1 icon key = %q, want the precomputed %q", got, want)
	}
	if a.overlayIconPaths[1] != iconPath {
		t.Fatalf("row 1 icon path = %q, want the local file the key decodes from", a.overlayIconPaths[1])
	}
	if a.overlayIconKeys[0] != "" {
		t.Errorf("the None row must carry NO key (courtroom.cpp:5606-5610 asks, no theme ships one) — got %q", a.overlayIconKeys[0])
	}

	a.overlayChoiceIdx = 1 // an effect is PICKED: the state that regressed
	tr := sdl.Rect{X: 4, Y: 4, W: 24, H: 20}
	draw := func() {
		_ = a.overlayChoices()
		a.overlayIconDraw(a.overlayChoiceIdx, tr)
	}
	draw() // warm the cached page pointer and any lazy renderer state
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("the closed effects dropdown allocates %.1f/frame, want 0 — the icon key is being "+
			"rebuilt per frame again (spec §3: precomputed when the list is built)", n)
	}
}

// TestOverlayIconKeysFollowTheRoster — baked keys are only correct while they
// belong to the roster that produced them, so a pack change must rebuild them AND
// drop the index-keyed page cache: cachedPage reallocates only on a row-COUNT
// change, so two packs of equal size would otherwise wear each other's icons.
func TestOverlayIconKeysFollowTheRoster(t *testing.T) {
	const localIcon = "/themes/t/misc/packb/icons/tv on.png"
	a := &App{ctx: &Ctx{}}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey("packa"): {
			set:  theme.MergeOverlayFX([]string{"flash"}),
			art:  map[string]string{},
			icon: map[string]string{},
		},
		a.overlayRosterKey("packb"): {
			set:  theme.MergeOverlayFX([]string{"tv on"}),
			art:  map[string]string{},
			icon: map[string]string{"tv on": localIcon},
		},
	}
	// Drive the folder through the memo the picker itself uses (char "" so the
	// memo hits without a char.ini fetch).
	a.overlayMyFolderValid, a.overlayMyFolderChar, a.overlayMyFolder = true, "", "packa"

	if got := a.overlayChoices(); len(got) != 2 || got[1] != "flash" {
		t.Fatalf("fixture: choices = %v", got)
	}
	// No local icon but an origin: the row falls to the misc-tier URL (rung 8).
	if want := "https://one.test/base/misc/packa/icons/flash"; a.overlayIconKeys[1] != want {
		t.Errorf("origin icon key = %q, want %q", a.overlayIconKeys[1], want)
	}
	if a.overlayIconPaths[1] != "" {
		t.Errorf("an origin-tier row must carry no LOCAL path, got %q", a.overlayIconPaths[1])
	}
	a.overlayIconPages = make([]*render.TexturePage, 2) // pretend a frame cached both rows

	// Swap to a character declaring its own pack — the SAME row count, so
	// cachedPage would happily keep the old pages under the new names.
	a.overlayMyFolder = "packb"
	if got := a.overlayChoices(); len(got) != 2 || got[1] != "tv on" {
		t.Fatalf("after the swap: choices = %v", got)
	}
	if a.overlayIconPages != nil {
		t.Error("the index-keyed icon pages survived a pack swap of the same size — that is a stale icon under the wrong row")
	}
	if want := overlayThemeKey(localIcon); a.overlayIconKeys[1] != want {
		t.Errorf("icon key = %q, want the new pack's local art %q", a.overlayIconKeys[1], want)
	}
	if a.overlayIconPaths[1] != localIcon {
		t.Errorf("icon path = %q, want %q", a.overlayIconPaths[1], localIcon)
	}
}

// TestStaleRosterResultIsDropped — a resolve holds an 8 s network timeout, so one
// begun before a theme apply can land long after it. Its art paths, its
// theme-tier property files and its display ORDER all belong to the theme that
// was applied when it started; filing it under the NEW one shows (and loads) the
// old theme's effects until something else invalidates the cache. Same class as
// the v1.81 theme-font stale-gen fix, and the same shape of guard.
func TestStaleRosterResultIsDropped(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	a.themeAppliedGen = 7
	key := a.overlayRosterKey("transitions")
	a.overlayRosterRes = make(chan overlayRosterFetch, overlayManifestResCap)
	a.overlayRosterInFlight = map[string]bool{key: true}

	stale := &overlayRoster{
		set:  theme.MergeOverlayFX([]string{"old theme fx"}),
		art:  map[string]string{"old theme fx": "/themes/OLD/effects/x.webp"},
		icon: map[string]string{},
	}
	a.overlayRosterRes <- overlayRosterFetch{key: key, gen: 6, roster: stale} // started a theme ago
	a.pollOverlayRosters()
	if got, ok := a.overlayRosters[key]; ok {
		t.Fatalf("a resolve from theme gen 6 landed under gen 7: %+v", got)
	}
	if a.overlayRosterInFlight[key] {
		t.Error("the fetch slot must be freed even when the result is dropped, or the folder can never resolve again")
	}

	// The current generation lands normally — the guard drops staleness, not results.
	fresh := &overlayRoster{
		set:  theme.MergeOverlayFX([]string{"new theme fx"}),
		art:  map[string]string{},
		icon: map[string]string{},
	}
	a.overlayRosterInFlight[key] = true
	a.overlayRosterRes <- overlayRosterFetch{key: key, gen: 7, roster: fresh}
	a.pollOverlayRosters()
	if a.overlayRosters[key] != fresh {
		t.Fatalf("the current-gen resolve did not land: %+v", a.overlayRosters[key])
	}
	if a.overlayRosterInFlight[key] {
		t.Error("a landed resolve must free its fetch slot")
	}
}

// TestOverlayManifestFetchedOncePerFolderAndMissesAreCached (spec §4.5, hard
// rule 6). The in-flight marker is what makes "once" true; the settled entry —
// which a MISS also produces — is what stops the re-fetch loop. Exercised against
// the real resolver goroutine with no Manager and therefore no network rung, so
// the only thing under test is the marker discipline.
func TestOverlayManifestFetchedOncePerFolderAndMissesAreCached(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	key := a.overlayRosterKey("transitions")

	if r := a.overlayRosterFor("transitions"); r != nil {
		t.Fatalf("a cold folder must answer nil while it resolves, got %+v", r)
	}
	if !a.overlayRosterInFlight[key] {
		t.Fatal("no resolve was started for a cold folder")
	}
	// Ask again (memo dropped, as a landed char.ini or a new speaker would do):
	// the marker must suppress a second goroutine.
	for i := 0; i < 3; i++ {
		a.overlayInvalidateMemos()
		if r := a.overlayRosterFor("transitions"); r != nil {
			t.Fatalf("ask %d answered %+v before the resolve landed", i, r)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for a.overlayRosters[key] == nil && time.Now().Before(deadline) {
		a.pollOverlayRosters()
		time.Sleep(time.Millisecond)
	}
	if a.overlayRosters[key] == nil {
		t.Fatal("the resolve never landed")
	}
	// Exactly ONE goroutine ran: a second would have queued a second result.
	select {
	case extra := <-a.overlayRosterRes:
		t.Fatalf("a second resolve ran for the same folder: %+v", extra)
	default:
	}
	// A MISS caches: the settled (empty) roster answers, and nothing re-resolves.
	a.overlayInvalidateMemos()
	if r := a.overlayRosterFor("transitions"); r == nil {
		t.Fatal("a settled miss must answer from the cache, not re-resolve")
	}
	if a.overlayRosterInFlight[key] {
		t.Error("a settled folder must hold no fetch slot")
	}
}

// effectsPickerRoster stages n effects on a drawable App with a claimed
// character (courtroom.cpp:5586-5598) and a COUNTING thumb painter bound ahead of
// the draw — drawEffectsPicker only binds its own when the slot is empty, so this
// counts every call dropdownEx and FinishFrame make without touching the real
// painter's demand paths (this fixture has neither Store nor Manager).
func effectsPickerRoster(t *testing.T, n int) (*App, *int) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	t.Cleanup(cleanup)
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	a := &App{ctx: ctx}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	a.sess = &courtroom.Session{} // no Chars: myCharName is "", so no char.ini fetch
	a.sess.MyCharID = 0
	names := make([]string, n)
	for i := range names {
		names[i] = "fx" + strconv.Itoa(i)
	}
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey(""): {
			set:  theme.MergeOverlayFX(names),
			art:  map[string]string{},
			icon: map[string]string{},
		},
	}
	painted := 0
	a.overlayThumbFn = func(int, sdl.Rect) { painted++ }
	return a, &painted
}

// TestEffectIconsAreNotFetchedUntilTheDropdownOpens (spec §4.5, hard rule 6 —
// "no wasted probes"). A CLOSED control paints exactly ONE row: its current pick.
// A 27-effect pack must not cost 27 icon lookups for a picker nobody opened.
func TestEffectIconsAreNotFetchedUntilTheDropdownOpens(t *testing.T) {
	a, painted := effectsPickerRoster(t, 27)
	a.drawEffectsPicker(sdl.Rect{X: 8, Y: 8, W: 140, H: 22})
	a.ctx.FinishFrame()
	if *painted != 1 {
		t.Fatalf("a CLOSED dropdown painted %d icons, want exactly 1 (the current pick)", *painted)
	}
	// And that one is row 0, "None", which carries no key at all — so a client that
	// never picks an effect and never opens the list asks the origin for nothing.
	if a.overlayChoiceIdx != 0 {
		t.Fatalf("fixture: the picker starts on None, got %d", a.overlayChoiceIdx)
	}
	if len(a.overlayIconKeys) == 0 || a.overlayIconKeys[0] != "" {
		t.Error("the None row carries an icon key, want none")
	}
}

// TestEffectIconRowsDemandOnlyVisibleRows (spec §4.5) — an OPEN list paints the
// ≤ ddMaxVisibleRows rows it can actually show, never the whole roster. That cap
// is what bounds icon demand per frame: the painter is the ONLY place an icon is
// asked for, so "rows painted" is exactly "icons demanded".
func TestEffectIconRowsDemandOnlyVisibleRows(t *testing.T) {
	const roster = 27 // the reference transitions pack
	a, painted := effectsPickerRoster(t, roster)
	a.ctx.ddOpen = overlayEffectsDDID
	a.drawEffectsPicker(sdl.Rect{X: 8, Y: 8, W: 140, H: 22})
	a.ctx.FinishFrame()
	// One for the closed control's chip (dropdownEx paints it open or shut) plus
	// the visible rows.
	if limit := ddMaxVisibleRows + 1; *painted > limit {
		t.Fatalf("an open %d-row list painted %d icons, want at most %d (%d visible rows + the chip)",
			roster, *painted, limit, ddMaxVisibleRows)
	}
	if *painted <= 1 {
		t.Fatalf("an OPEN list painted %d icons — the rows are not being drawn at all", *painted)
	}
	if *painted >= roster {
		t.Fatalf("an open list painted %d of %d rows — the visible-row bound is gone", *painted, roster)
	}
}

// TestCharINIRealizationOverridesTheEffectSound — canon's ONE special case in
// get_effect_property: fx == "realization" && prop == "sound" is replaced
// wholesale by get_custom_realization (text_file_functions.cpp:889-892), i.e. by
// char.ini [Options] realization, falling back to the theme's courtroom_sounds.ini
// value (:900). AO2 applies it when it BUILDS the outgoing field
// (courtroom.cpp:2299 calls that very accessor), so the wire must carry the
// character's own sound rather than the pack's default.
func TestCharINIRealizationOverridesTheEffectSound(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	a.sess = &courtroom.Session{Chars: []courtroom.CharacterSlot{{Name: "Phoenix"}}}
	a.sess.MyCharID = 0
	a.themeSounds = map[string]string{"realization": "themerealize"}
	// Pre-seeded (settled, nothing declared): charMetaFor would otherwise fire a
	// fetch through the nil Manager.
	a.charMetaCache = map[string]charMeta{a.charINIURL("Phoenix"): {done: true}}
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey(""): {
			set: theme.MergeOverlayFX(
				[]string{"realization", "tv on"},
				[]theme.OverlayFX{{Name: "realization", Sound: "packrealize"}, {Name: "tv on", Sound: "switch tv"}}),
			art:  map[string]string{},
			icon: map[string]string{},
		},
	}
	a.icPreanim = true

	// Nothing in char.ini: the THEME's courtroom_sounds.ini realization wins over
	// the pack's, which is get_custom_realization's own fallback (:900).
	if got := a.overlayEffectSound("", "realization"); got != "themerealize" {
		t.Errorf("sound = %q, want the theme's realization sfx (text_file_functions.cpp:900)", got)
	}
	// Every OTHER effect keeps its merged effects.ini sound — the override is one
	// name, not a policy.
	if got := a.overlayEffectSound("", "tv on"); got != "switch tv" {
		t.Errorf("sound = %q, want the pack's own value", got)
	}

	// The character declares one: it wins outright.
	a.charMetaCache[a.charINIURL("Phoenix")] = charMeta{realization: "mine", done: true}
	if got := a.overlayEffectSound("", "realization"); got != "mine" {
		t.Errorf("sound = %q, want char.ini [Options] realization", got)
	}
	// …and it is what actually rides the wire.
	choices := a.overlayChoices()
	if len(choices) != 3 || choices[1] != "realization" {
		t.Fatalf("fixture: choices = %v", choices)
	}
	a.overlayChoiceIdx = 1
	if got := a.outgoingEffectsField(); got != "realization||mine" {
		t.Errorf("outgoing field = %q, want \"realization||mine\"", got)
	}
	// The anti-overlap rule still runs LAST, exactly as in canon
	// (courtroom.cpp:2302-2305 mutates fx_sound after get_effect_property).
	a.icPreanim, a.sfxChoiceIdx = false, 3
	if got := a.outgoingEffectsField(); got != "realization||0" {
		t.Errorf("outgoing field = %q, want the anti-overlap \"0\"", got)
	}
}

// TestRightClickSFXRowPreviewsWithoutSelecting (spec §1.e, BOTH halves): the
// IC-bar sfxdd row and the SFX-BROWSER row audition on right-click and commit
// nothing. Left-click is what picks; before this there was no way to hear a sound
// without putting it on your next message.
//
// No origin, so previewSFX is its documented lobby no-op — this half pins the
// non-effects (selection, consumption, modal state). The positive half is the
// test below.
func TestRightClickSFXRowPreviewsWithoutSelecting(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	newApp := func(c *Ctx) *App {
		a := &App{ctx: c}
		a.sfxChoices = []string{"SFX: auto", "sfx-one", "sfx-two"}
		return a
	}

	// Half 1 — the open sfxdd list. Row 0 is "SFX: auto" (the emote's own sound,
	// which has no name to play): the audition refuses it, but the click is still
	// consumed. The consume is the OPEN LIST's fence, decided once in
	// DropdownRowRightClicked — every in-range row belongs to the list; only the
	// dead tail past the last row is left for others (TestDropdownRowRightClicked).
	// previewOverlayRow's row-0 "None" arm has the identical shape, and the proof
	// that row 0 does nothing is the negative case in TestRightClickSFXRowActuallyPreviews.
	a := newApp(&Ctx{})
	list := sdl.Rect{X: 100, Y: 200, W: 120, H: 90}
	stampOpenDropdown(a.ctx, "sfxdd", list, 30, len(a.sfxChoices), 0)
	a.ctx.mouseX, a.ctx.mouseY = 150, 265 // 65 px in → row 2
	a.ctx.rightClicked = true
	a.sfxRowRightClickPreview()
	if a.ctx.rightClicked {
		t.Error("the right-click must be CONSUMED — an unconsumed one leaks past the modal capture")
	}
	if a.sfxChoiceIdx != 0 {
		t.Errorf("the audition selected row %d — right-click previews, left-click picks", a.sfxChoiceIdx)
	}
	a.ctx.mouseY = 205 // row 0
	a.ctx.rightClicked = true
	a.sfxRowRightClickPreview()
	if a.ctx.rightClicked {
		t.Error("row 0 is still a row of the OPEN list: in-range rows are consumed whether or not " +
			"the caller has anything to do with them, or the click leaks past the modal capture")
	}
	if a.sfxChoiceIdx != 0 {
		t.Errorf("row 0 (\"SFX: auto\") set the override to %d", a.sfxChoiceIdx)
	}

	// Half 2 — an SFX-BROWSER row. Same gesture, same result: no override, no
	// toast, and the browser stays open (a left-click on the body would close it).
	b := newApp(ctx)
	b.showSfxBrowser = true
	row := sdl.Rect{X: 0, Y: 0, W: 300, H: sfxBrowserRowH}
	b.ctx.mouseX, b.ctx.mouseY = 200, sfxBrowserRowH/2 // over the LABEL, past ★ and ▶
	b.ctx.rightClicked = true
	b.drawSfxRow(row, "sfx-two", false, "")
	if b.ctx.rightClicked {
		t.Error("the browser row must consume its right-click (the modal backdrop is underneath)")
	}
	if b.sfxChoiceIdx != 0 {
		t.Errorf("the browser audition set the override to %d", b.sfxChoiceIdx)
	}
	if !b.showSfxBrowser {
		t.Error("auditioning closed the browser — that is what USING a sound does")
	}
	if b.warnLine != "" {
		t.Errorf("the audition raised the pick toast %q", b.warnLine)
	}
}

// TestRightClickSFXRowActuallyPreviews is the positive half, written inside-out on
// purpose: render.Audio is a concrete type with no seam, so the only way to
// observe that previewSFX was REACHED is to give the path an origin and no audio
// engine and catch the deref. Same detector TestEffectPreviewFiltersTheSoundSentinels
// uses, pointed the other way.
func TestRightClickSFXRowActuallyPreviews(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	reached := func(c *Ctx, hit func(*App)) (fired bool) {
		a := &App{ctx: c}
		a.urls = courtroom.NewURLBuilder("https://one.test/base") // an origin: previewSFX proceeds
		a.sfxChoices = []string{"SFX: auto", "sfx-one", "sfx-two"}
		defer func() { fired = recover() != nil }()
		hit(a)
		return false
	}
	if !reached(&Ctx{}, func(a *App) {
		stampOpenDropdown(a.ctx, "sfxdd", sdl.Rect{X: 100, Y: 200, W: 120, H: 90}, 30, 3, 0)
		a.ctx.mouseX, a.ctx.mouseY = 150, 265
		a.ctx.rightClicked = true
		a.sfxRowRightClickPreview()
	}) {
		t.Error("right-clicking an sfxdd row never reached previewSFX")
	}
	if !reached(ctx, func(a *App) {
		a.ctx.mouseX, a.ctx.mouseY = 200, sfxBrowserRowH/2
		a.ctx.rightClicked = true
		a.drawSfxRow(sdl.Rect{X: 0, Y: 0, W: 300, H: sfxBrowserRowH}, "sfx-two", false, "")
	}) {
		t.Error("right-clicking an SFX-browser row never reached previewSFX (spec §1.e, second half)")
	}
	// Pointed the other way once more: row 0 is "SFX: auto", the emote's own sound,
	// which has no name to play here. The refusal happens in sfxRowRightClickPreview
	// (AFTER the list consumes the click, which is the list's business), so the
	// detector staying quiet is the only observable that it never reached previewSFX.
	if reached(&Ctx{}, func(a *App) {
		stampOpenDropdown(a.ctx, "sfxdd", sdl.Rect{X: 100, Y: 200, W: 120, H: 90}, 30, 3, 0)
		a.ctx.mouseX, a.ctx.mouseY = 150, 205 // row 0
		a.ctx.rightClicked = true
		a.sfxRowRightClickPreview()
	}) {
		t.Error(`row 0 ("SFX: auto") has no sound name — auditioning it must not reach previewSFX`)
	}
}
