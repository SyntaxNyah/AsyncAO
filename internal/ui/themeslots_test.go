package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestUnboundDesignKeysNamesOnlyTheOrphans pins the observability that makes
// "a missing rect draws nothing" supportable: a theme author must be able to
// learn which of their declared rects AsyncAO does not read. The report must
// name a genuinely unread key and must stay silent about the three kinds of key
// that ARE accounted for — a bound slot, a documented deferral, and a
// char-select rect.
func TestUnboundDesignKeysNamesOnlyTheOrphans(t *testing.T) {
	dir := t.TempDir()
	themeDir := filepath.Join(dir, "themes", "probe")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const design = "" +
		"courtroom = 0, 0, 944, 600\n" + // bound
		"viewport = 216, 0, 512, 384\n" + // bound
		"evidence_name = 112, 4, 264, 19\n" + // deliberately deferred
		"char_select = 0, 0, 714, 668\n" + // char-select key
		"found_song_color = 144, 238, 144\n" + // a colour, not a rect
		"showname_extra_width = 48\n" + // a scalar, not a rect
		"wobbulator = 1, 2, 3, 4\n" // an orphan: no widget reads this
	if err := os.WriteFile(filepath.Join(themeDir, "courtroom_design.ini"), []byte(design), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := theme.Load("probe", "", []string{dir})
	if err != nil {
		t.Fatal(err)
	}

	got := unboundDesignKeys(th)
	if !strings.Contains(got, "wobbulator") {
		t.Errorf("report %q does not name the orphan key — the diagnostic is blind", got)
	}
	for _, quiet := range []string{"courtroom", "viewport", "evidence_name", "found_song_color", "showname_extra_width"} {
		if strings.Contains(got, quiet) {
			t.Errorf("report %q names %q, which is accounted for", got, quiet)
		}
	}
	// A fully-covered theme must report nothing at all, or the debug log would
	// carry a line on every apply for every theme.
	const covered = "courtroom = 0, 0, 944, 600\nviewport = 216, 0, 512, 384\n"
	if err := os.WriteFile(filepath.Join(themeDir, "courtroom_design.ini"), []byte(covered), 0o644); err != nil {
		t.Fatal(err)
	}
	th2, err := theme.Load("probe", "", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if line := unboundDesignKeys(th2); line != "" {
		t.Errorf("a fully-covered theme reported %q, want silence", line)
	}
}

// TestThemeSlotTableIsTotal pins the registry's structural invariants. The table is
// the contract every later #21 commit binds against, so a duplicate row or a
// mis-derived table would silently mis-place a widget rather than fail.
func TestThemeSlotTableIsTotal(t *testing.T) {
	if len(themeSlots) > themeSlotCap {
		t.Fatalf("themeSlots has %d rows, cap is %d (hard rule 4)", len(themeSlots), themeSlotCap)
	}
	seen := map[string]bool{}
	for i := range themeSlots {
		s := &themeSlots[i]
		if s.key == "" {
			t.Errorf("row %d has an empty key", i)
			continue
		}
		if seen[s.key] {
			t.Errorf("duplicate row for %q — themeSlotIndex would keep only one", s.key)
		}
		seen[s.key] = true
		if s.alt != "" {
			if seen[s.alt] {
				t.Errorf("alt %q collides with another row's key", s.alt)
			}
			seen[s.alt] = true
		}
		// A key cannot be both a live slot and a documented deferral.
		if _, deferred := themeSlotDeferred[s.key]; deferred {
			t.Errorf("%q is both a themeSlots row and a themeSlotDeferred entry", s.key)
		}
		// Every deferral must carry a reason — the map is documentation, and an
		// empty string would let a key be dropped with no argument for it.
		if s.state == slotStateInert && s.fixed {
			t.Errorf("%q is inert AND fixed — redundant; inert already blocks the editor", s.key)
		}
	}
	for k, why := range themeSlotDeferred {
		if why == "" {
			t.Errorf("themeSlotDeferred[%q] has no reason", k)
		}
	}
}

// TestEveryDrawnKeyIsMarkedHandDrawn catches the drift that actually happened: a
// binding commit added a draw site in theme_layout.go and forgot to move the row's
// state off slotStateInert. The registry then LIES about what paints, and the
// consequence is not cosmetic — themeKeyEditable returns false for an inert row, so
// the themed layout editor refuses to offer a drag box for a widget that is on
// screen. That is the ghost box inverted.
//
// The probe is the draw site's own rect lookup. Every themed control resolves its
// rect through one of two helpers, so a key named in either call is painted.
func TestEveryDrawnKeyIsMarkedHandDrawn(t *testing.T) {
	src, err := os.ReadFile("theme_layout.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for i := range themeSlots {
		s := &themeSlots[i]
		if s.state != slotStateInert {
			continue
		}
		// Both spellings a draw site can use to resolve this row's rect.
		for _, probe := range []string{
			`themedToggleRect(lay, "` + s.key + `"`,
			`lay.rect("` + s.key + `")`,
		} {
			if strings.Contains(body, probe) {
				t.Errorf("%q is drawn (%s) but still marked slotStateInert — the layout editor will refuse to place it",
					s.key, probe)
				break
			}
		}
	}
}

// TestNoSlotHasGraduatedToTheTableYet pins where the registry currently sits in
// the #21 arc, and keeps the two forward-looking fields honest. Rows land inert or
// hand-drawn; the harvest commit is what moves the hand-ordered draw bodies into
// `draw` and flips those rows to slotStateTable. Until then EVERY row must still
// have a nil draw — a row that carried one while the dispatcher does not exist yet
// would simply never paint.
//
// When the harvest lands, this test is what tells you to rewrite it.
func TestNoSlotHasGraduatedToTheTableYet(t *testing.T) {
	for i := range themeSlots {
		s := &themeSlots[i]
		if s.draw != nil {
			t.Errorf("%q carries a draw func, but no dispatcher reads it yet", s.key)
		}
		if s.state == slotStateTable {
			t.Errorf("%q is slotStateTable before the harvest commit — nothing would paint it", s.key)
		}
	}
}

// TestEveryHandDrawnKeyHasAPainter is the gate that actually ends the ghost box,
// and it is the INVERSE of TestEveryDrawnKeyIsMarkedHandDrawn above. That one
// catches "paints but is marked inert" (the editor refuses a box for a visible
// widget). This one catches "marked hand-drawn but nothing paints it" — which is
// the failure #21 is named for: music_search sat in the old themeLayoutKeys from
// the day it was written with no draw site, so the editor offered a drag box that
// moved nothing.
//
// It replaces a test that could not fail. The old TestBoundButInertSlotsAreNotEditable
// asserted `s.state == slotStateInert && themeKeyEditable(s.key)` and
// `s.fixed && themeKeyEditable(s.key)`, but themeKeyEditable is DERIVED from
// exactly those two fields — `s.state != slotStateInert && !s.fixed` — so both
// conjunctions are structurally unreachable. It reported the ghost-box rule as
// covered while covering nothing, which is worse than no test: every later commit
// that flipped a row to hand-drawn believed it was guarded.
func TestEveryHandDrawnKeyHasAPainter(t *testing.T) {
	// Themed draw sites resolve a rect through one of two helpers, and they live
	// in more than one file — the chatbox fitter and the GIF exporter read theme
	// rects too, so scanning only theme_layout.go would report false ghosts.
	var body strings.Builder
	for _, f := range []string{"theme_layout.go", "court_extras.go", "chatboxfit.go", "gifexport.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		body.Write(src)
	}
	painters := body.String()

	for i := range themeSlots {
		s := &themeSlots[i]
		if s.state == slotStateInert {
			continue // inert is a declared "ingested, nothing paints it" — the other test's job
		}
		if s.external {
			continue // drawn outside the design canvas by its own chrome path
		}
		if s.fixed {
			continue // never editable by derivation, so it cannot become a ghost box
		}
		// Probe for the key as a QUOTED LITERAL anywhere in the painter sources,
		// not for a particular call shape. Real draw sites take three forms and a
		// shape-matching probe only sees the first: a direct lay.rect("key"), a
		// TABLE of keys walked by a loop (the shouts at theme_layout.go:1116, the
		// HP buttons at :1156), and the OVERRIDE argument of themedToggleRect (the
		// asyncao_* tier, e.g. :928). All three mention the key verbatim, so the
		// literal is the honest signal that something references it.
		if !strings.Contains(painters, `"`+s.key+`"`) {
			t.Errorf("%q is marked non-inert (state %d) but its key appears in no painter source — "+
				"the layout editor will offer a drag box that moves nothing (the music_search ghost)",
				s.key, s.state)
		}
	}
}

// TestDerivedEditabilityHoldsForTheNamedExceptions covers what is left of the old
// test once its two tautological arms are gone: the specific keys the editor must
// never offer, and the unknown-key path. These are real assertions — they read the
// TABLE's `fixed` field through the derivation, so unfixing one here fails.
func TestDerivedEditabilityHoldsForTheNamedExceptions(t *testing.T) {
	// The three rects the editor must never offer, named explicitly so a future
	// table edit that unfixes one fails here rather than in the field. These are
	// exactly the old layoutEditSkip.
	for _, k := range []string{"courtroom", "showname", "message"} {
		if themeKeyEditable(k) {
			t.Errorf("%q must never be editable (was layoutEditSkip)", k)
		}
	}
	// An unknown key is not editable either — the editor builds its set from the
	// layout cache, which can hold a key a theme invented.
	if themeKeyEditable("totally_not_a_real_key") {
		t.Error("an unknown design key must not be editable")
	}
}

// TestThemeLayoutKeysDerivesFromTheTable pins that the three derived tables really
// are derived — the whole point of the registry is that a key cannot be ingested
// without a row, and a row cannot carry art without being ingested.
func TestThemeLayoutKeysDerivesFromTheTable(t *testing.T) {
	if !sort.StringsAreSorted(themeLayoutKeys) {
		t.Error("themeLayoutKeys is not sorted — a failing table test would name a map-order key")
	}
	have := map[string]bool{}
	for _, k := range themeLayoutKeys {
		if have[k] {
			t.Errorf("themeLayoutKeys contains %q twice", k)
		}
		have[k] = true
	}
	for i := range themeSlots {
		s := &themeSlots[i]
		if !have[s.key] {
			t.Errorf("row %q is missing from themeLayoutKeys", s.key)
		}
		if s.alt != "" && !have[s.alt] {
			t.Errorf("alt %q is missing from themeLayoutKeys", s.alt)
		}
	}
	stems := themeButtonStems()
	for key := range stems {
		s := themeSlotFor(key)
		if s == nil {
			t.Errorf("themeButtonStems has art for %q with no themeSlots row", key)
			continue
		}
		if len(s.art) == 0 {
			t.Errorf("themeButtonStems has art for %q but its row declares none", key)
		}
	}
	for i := range themeSlots {
		if s := &themeSlots[i]; len(s.art) > 0 && stems[s.key] == nil {
			t.Errorf("row %q declares art that themeButtonStems did not pick up", s.key)
		}
	}
}

// TestThemeSlotRelSpaces pins the coordinate systems. Before the registry the
// transform hard-coded `key != "showname" && key != "message"`, so the two
// non-courtroom families it could not express (viewport-relative evidence icons,
// music_display-relative music_name) would have been placed window-absolute.
func TestThemeSlotRelSpaces(t *testing.T) {
	cases := map[string]themeSlotRelSpace{
		"courtroom":           relCourtroom,
		"viewport":            relCourtroom,
		"ao2_chatbox":         relCourtroom,
		"showname":            relChatbox,
		"message":             relChatbox,
		"left_evidence_icon":  relViewport,
		"right_evidence_icon": relViewport,
		"music_name":          relMusicDisplay,
		"music_display":       relCourtroom,
		// An unknown key falls back to courtroom-relative, which is what the
		// generic transform did before the table existed.
		"totally_not_a_real_key": relCourtroom,
	}
	for key, want := range cases {
		if got := themeSlotRel(key); got != want {
			t.Errorf("themeSlotRel(%q) = %d, want %d", key, got, want)
		}
	}
}

// TestMsChatlogIsTheDebugLog pins that ms_chatlog is the DEBUG log, not a
// server_chatlog alias. AO2: set_size_and_pos(ui_debug_log, "ms_chatlog"); //
// Old name, still use it to not break compatibility (AO2-Client courtroom.cpp:831),
// with the server chatlog at its own key (:834). Treating the two as one drew the
// OOC log in the debug log's box.
//
// The row started INERT — ingested so the key wasn't reported unbound, but with
// nothing painting it. It is now hand-drawn: drawThemedDebugLog paints the failure
// ring there, and ooc_toggle swaps the two panels per on_ooc_toggle_clicked (:5197).
func TestMsChatlogIsTheDebugLog(t *testing.T) {
	s := themeSlotFor("ms_chatlog")
	if s == nil {
		t.Fatal("ms_chatlog lost its row — it would be reported as an unbound key")
	}
	if s.state != slotStateHandDrawn {
		t.Errorf("ms_chatlog state = %d, want hand-drawn (drawThemedDebugLog paints it)", s.state)
	}
	if !themeKeyEditable("ms_chatlog") {
		t.Error("ms_chatlog draws a real panel, so the layout editor must offer a drag box for it")
	}
	// The button that swaps it in must be drawn too, or the debug log is unreachable.
	if tg := themeSlotFor("ooc_toggle"); tg == nil || tg.state != slotStateHandDrawn {
		t.Error("ooc_toggle must be hand-drawn — without the button nothing can reach the debug log")
	}
}

// TestOOCToggleLabelNamesTheVisiblePanel pins the direction of AO2's label, which
// is the easy thing to invert: ui_ooc_toggle reads "Server" WHILE server chat is
// showing (courtroom.cpp:1014) and flips to "Debug" once the debug log is up
// (:5203). It names what you are looking at, not what clicking will do.
//
// It also pins the zero value. debugOOC is stored inverted from AO2's server_ooc
// precisely so a fresh sessionState needs no seeding — a default-true field that
// resetSessionState forgets is a standing bug class in this package.
func TestOOCToggleLabelNamesTheVisiblePanel(t *testing.T) {
	var s sessionState
	if got := s.oocToggleLabel(); got != "Server" {
		t.Errorf("a fresh session must open on server chat and read %q, got %q", "Server", got)
	}
	s.debugOOC = true
	if got := s.oocToggleLabel(); got != "Debug" {
		t.Errorf("with the debug log showing the button must read %q, got %q", "Debug", got)
	}
}

// TestEmoteDropdownRowIndexIsTheEmoteIndex pins the invariant that makes the
// dropdown safe to wire straight into selectEmote: row N must BE emote N.
//
// The list is built from a.emotes, and the tempting "improvement" is to build it
// from a.emoteVisible instead so favourites filtering applies. That would be
// silently wrong in the worst way — the dropdown would keep showing sensible
// labels while every pick selected a different emote — because selectEmote indexes
// the unfiltered slice.
//
// Also pins the 1-based label, which is AO2's (emotes.cpp:176 builds
// QString::number(n + 1) + ": " + comment), and the empty-comment fallback, which
// is deliberately NOT AO2's — see the comment at ensureEmoteChoices.
func TestEmoteDropdownRowIndexIsTheEmoteIndex(t *testing.T) {
	a := testTabApp(t)
	a.emotes = []courtroom.Emote{
		{Comment: "normal", Anim: "normal"},
		{Comment: "", Anim: "pointing"}, // no comment: falls back to the anim name
		{Comment: "thinking", Anim: "think"},
	}
	a.ensureEmoteChoices()

	want := []string{"1: normal", "2: pointing", "3: thinking"}
	if len(a.emoteChoices) != len(want) {
		t.Fatalf("built %d rows %v, want %d — a filtered list would desync row index from emote index",
			len(a.emoteChoices), a.emoteChoices, len(want))
	}
	for i, w := range want {
		if a.emoteChoices[i] != w {
			t.Errorf("row %d = %q, want %q", i, a.emoteChoices[i], w)
		}
	}

	// The guard must rebuild when the emote COUNT changes (an iniswap to a
	// character with a different sheet), not just when the name does.
	a.emotes = a.emotes[:2]
	a.ensureEmoteChoices()
	if len(a.emoteChoices) != 2 {
		t.Errorf("a shorter emote list must rebuild the rows, got %d", len(a.emoteChoices))
	}
}

// TestEnsureEmoteChoicesIsAllocFreeWhenSettled pins the cache guard itself. The
// draw site calls this EVERY frame, so a guard that misses would rebuild three
// strings per frame forever on the zero-allocation render path.
func TestEnsureEmoteChoicesIsAllocFreeWhenSettled(t *testing.T) {
	a := testTabApp(t)
	a.emotes = []courtroom.Emote{{Comment: "normal", Anim: "normal"}, {Comment: "point", Anim: "point"}}
	a.ensureEmoteChoices() // prime

	if n := testing.AllocsPerRun(100, func() { a.ensureEmoteChoices() }); n != 0 {
		t.Errorf("ensureEmoteChoices allocates %v/op when nothing changed, want 0 — "+
			"the guard must compare plain fields, never a built key string", n)
	}
}

// TestButtonArtStemsMatchAO2 pins the image stems for the buttons a theme is most
// likely to notice, because the failure mode is silent and looks like a layout bug.
//
// drawThemeButton falls back to a TEXT CHIP when a row carries no art, so five
// rows that draw but declared none were painting "Pair", "D+", "D-", "P+", "P-"
// into AO2 icon rects — pair_button is 42x42 and the HP steppers are 9x9 — while
// the stock base ships pair_button.png, defplus.png, defminus.png, proplus.png and
// prominus.png right beside them and the theme fallback already reaches that
// folder. Nothing was broken enough to fail; it just looked wrong.
//
// evidence_button is a different failure: it listed "addevidence" as a fallback,
// which is a DIFFERENT WIDGET's art. AO2 uses it for an empty evidence SLOT
// (evidence.cpp:356 setThemeImage("addevidence.png")), not for the button
// (evidence.cpp:103 setImage("evidence_button")). In the stock base
// evidence_button.png and evidencebutton.png are byte-identical while
// addevidence.png is a distinct "+" glyph — so a theme shipping only addevidence
// drew a plus sign where the Evidence button belongs.
func TestButtonArtStemsMatchAO2(t *testing.T) {
	// stem -> the AO2 call it mirrors, so a wrong value fails with its own citation.
	want := map[string]struct {
		stems []string
		cite  string
	}{
		"defense_plus":      {[]string{"defplus"}, "courtroom.cpp:1123"},
		"defense_minus":     {[]string{"defminus"}, "courtroom.cpp:1127"},
		"prosecution_plus":  {[]string{"proplus"}, "courtroom.cpp:1131"},
		"prosecution_minus": {[]string{"prominus"}, "courtroom.cpp:1135"},
		"pair_button":       {[]string{"pair_button"}, "courtroom.cpp:861"},
		"change_character":  {[]string{"change_character"}, "courtroom.cpp:1038"},
		"reload_theme":      {[]string{"reload_theme"}, "courtroom.cpp:1043"},
		// AO2 probes courtroom_settings and only falls back to the pre-2.10 "settings"
		// when that icon comes back null (courtroom.cpp:1053-1057), so the order is
		// INVERTED relative to the design key and easy to write backwards.
		"settings": {[]string{"courtroom_settings", "settings"}, "courtroom.cpp:1053"},
		// Upstream's current spelling first, the pre-2.9 one as fallback.
		"evidence_button": {[]string{"evidence_button", "evidencebutton"}, "evidence.cpp:103"},
	}
	for key, w := range want {
		s := themeSlotFor(key)
		if s == nil {
			t.Errorf("%q has no registry row", key)
			continue
		}
		if len(s.art) != len(w.stems) {
			t.Errorf("%q art = %v, want %v (AO2 %s)", key, s.art, w.stems, w.cite)
			continue
		}
		for i, stem := range w.stems {
			if s.art[i] != stem {
				t.Errorf("%q art[%d] = %q, want %q (AO2 %s)", key, i, s.art[i], stem, w.cite)
			}
		}
	}

	// The specific regression: addevidence must never be an evidence_button stem.
	if s := themeSlotFor("evidence_button"); s != nil {
		for _, stem := range s.art {
			if stem == "addevidence" {
				t.Error("evidence_button lists \"addevidence\", which is the empty-SLOT art " +
					"(evidence.cpp:356) — a theme shipping only that file draws a \"+\" where the button goes")
			}
		}
	}

	// Every row that DRAWS and names art must name it non-empty; an empty string
	// would resolve to a stem of "" and silently fall through to the text chip.
	for i := range themeSlots {
		s := &themeSlots[i]
		for j, stem := range s.art {
			if stem == "" {
				t.Errorf("%q art[%d] is empty", s.key, j)
			}
		}
	}
}

// TestMusicNameIsFixedToItsPlate pins the one rect in the registry whose
// coordinates are NOT window-absolute once resolved.
//
// AO2 parents ui_music_name inside ui_music_display (courtroom.cpp:171), so its
// declared rect is relative to the plate — which is why the registry marks it
// relMusicDisplay and why the layout transform deliberately leaves such rects RAW
// (no offX/offY, no clamp). The themed layout editor builds its drag boxes
// straight from the resolved rect for every editable key and converts a drag back
// to design space by subtracting the same offsets unconditionally. So a
// music_name that is editable offers a box in the window's top-left corner that
// moves the wrong thing — the ghost box the registry exists to end, in the one
// place the usual painter check cannot see it.
//
// fixed:true is what keeps it out, exactly as showname and message are held out
// of the editor for the same relative-rect reason.
func TestMusicNameIsFixedToItsPlate(t *testing.T) {
	s := themeSlotFor("music_name")
	if s == nil {
		t.Fatal("music_name lost its row")
	}
	if s.rel != relMusicDisplay {
		t.Errorf("music_name rel = %d, want relMusicDisplay — AO2 parents it in the plate", s.rel)
	}
	if s.state == slotStateInert {
		t.Error("music_name is painted by drawThemedMusicPlate; leaving it inert misreports the registry")
	}
	if !s.fixed {
		t.Error("music_name must be fixed:true — its rect is plate-relative, so an editor " +
			"drag box built from it lands in the window's top-left corner and moves the wrong thing")
	}
	if themeKeyEditable("music_name") {
		t.Error("music_name must never be editable — a ghost box the painter check cannot catch")
	}
	// Every relative-rect row carries the same hazard, so hold the whole class.
	for i := range themeSlots {
		row := &themeSlots[i]
		if row.rel == relCourtroom || row.state == slotStateInert {
			continue
		}
		if themeKeyEditable(row.key) {
			t.Errorf("%q has a NON-courtroom-relative rect (rel %d) but is editable — the editor "+
				"treats every box as window-absolute", row.key, row.rel)
		}
	}
}
