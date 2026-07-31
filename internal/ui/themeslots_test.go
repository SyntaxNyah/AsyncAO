package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

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
	th, err := theme.Load("probe", []string{dir})
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
	th2, err := theme.Load("probe", []string{dir})
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

// TestBoundButInertSlotsAreNotEditable pins the rule that ends the ghost box: the
// themed layout editor may only offer a drag box for a rect something actually
// paints. music_search was in themeLayoutKeys from the day it was written and has
// never had a draw site, so the editor offered a box that moved nothing.
func TestBoundButInertSlotsAreNotEditable(t *testing.T) {
	for i := range themeSlots {
		s := &themeSlots[i]
		if s.state == slotStateInert && themeKeyEditable(s.key) {
			t.Errorf("%q is inert but editable — a ghost box", s.key)
		}
		if s.fixed && themeKeyEditable(s.key) {
			t.Errorf("%q is fixed but editable — it rides its parent, it is not placeable", s.key)
		}
	}
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

// TestMsChatlogIsTheDebugLog pins the one deliberate behaviour change in the
// registry commit. AO2 places the DEBUG log at ms_chatlog — set_size_and_pos(
// ui_debug_log, "ms_chatlog"); // Old name, still use it to not break compatibility
// (AO2-Client courtroom.cpp:831) — and the server chatlog at its own key (:834).
// Treating ms_chatlog as a server_chatlog alias drew the OOC log in the debug log's
// box. It must stay ingested (so the key is not "unbound") but inert.
func TestMsChatlogIsTheDebugLog(t *testing.T) {
	s := themeSlotFor("ms_chatlog")
	if s == nil {
		t.Fatal("ms_chatlog lost its row — it would be reported as an unbound key")
	}
	if s.state != slotStateInert {
		t.Errorf("ms_chatlog state = %d, want inert until the debug log is bound to it", s.state)
	}
	if themeKeyEditable("ms_chatlog") {
		t.Error("ms_chatlog is editable but nothing paints it — a ghost box")
	}
}
