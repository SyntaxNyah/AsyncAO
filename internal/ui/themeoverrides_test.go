package ui

// Gates for the AsyncAO tier's [overrides] applier (themeoverrides.go).
//
// The DISCIPLINE gates below drive foldSidecarOverrides directly: it rewrites,
// never adds, it refuses a key nothing paints, and it honours AO2's second
// spelling of the one widget that has two.
//
// These are the only half left. This file used to carry a second shape — a
// SHIPPED-CONTENT gate driving the real ingest chain over the fourteen themes in
// themes/ — and that was the half that mattered, because the tier was PARSED,
// EXPOSED and DOCUMENTED for a whole wave with no applier behind it: every unit
// test of the reader passed, every theme loaded, and ~60 rows per theme did
// nothing. That corpus has since been retired from the repository, so the gate
// went with it. Re-derive it from a corpus you actually ship before trusting an
// applier change again; this package's own fixtures cannot see that class of
// defect, by construction.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// The call site
// ---------------------------------------------------------------------------

// ovLiveThemeName is the fixture theme the end-to-end gate below applies. Its own
// folder, not one of ours: the point is to drive the client's real apply, and a
// shipped theme would let the gate pass on a rect that happened to match.
const ovLiveThemeName = "ovlivepath"

// ovLiveMovedKey is the widget the fixture moves and ovLiveStillStockKey the one it
// leaves alone. Both must be EDITABLE and both must come from AO2's stock backstop
// rather than from the fixture's design file, so the pair measures two different
// things with one apply: the override landed, and it landed on that key ONLY.
const (
	ovLiveMovedKey      = "emote_dropdown"
	ovLiveStillStockKey = "sfx_dropdown"
)

// ovLiveRect is deliberately nothing like AO2's stock emote_dropdown
// {5, 380, 105, 20}: every component differs, so a partial write cannot read as a
// clean one, and it is far from any other stock rect so a mis-keyed write is loud.
var ovLiveRect = theme.Rect{X: 123, Y: 234, W: 145, H: 26}

// ovLiveCanvas is the fixture's `courtroom`, and it is AO2's OWN 714x579 on
// purpose: applyAO2DefaultRects refuses a default that would land on an
// author-placed widget unless the theme works on that canvas
// (ao2defaultrects.go), so this is what makes the backstop fill every key the
// two-line design file is silent about.
var ovLiveCanvas = theme.Rect{X: 0, Y: 0, W: 714, H: 579}

// ovWriteLiveTheme writes the fixture to a fresh root and returns the root to hand
// to SetTheme. Two files and nothing else:
//
//   - a courtroom_design.ini with `courtroom` + `viewport` and NO widget keys.
//     That pair is what themeLayoutIn validates on and what gates the AO2
//     backstop, so this is the "a two-key design file plus a sidecar is a whole
//     layout" case foldSidecarOverrides was written for — and it is why the
//     assertions below can name AO2's stock rects as the not-applied answer.
//   - a sidecar with one [overrides] row.
func ovWriteLiveTheme(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, theme.ThemesDirName, ovLiveThemeName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	design := fmt.Sprintf("%s = %d, %d, %d, %d\n%s = 0, 0, 714, 382\n",
		ao2CanvasKey, ovLiveCanvas.X, ovLiveCanvas.Y, ovLiveCanvas.W, ovLiveCanvas.H, ao2ViewportKey)
	if err := os.WriteFile(filepath.Join(dir, theme.DesignFileName), []byte(design), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := fmt.Sprintf("[theme]\nname = %s\ncredit = All original.\n\n[overrides]\n%s = %d, %d, %d, %d\n",
		ovLiveThemeName, ovLiveMovedKey, ovLiveRect.X, ovLiveRect.Y, ovLiveRect.W, ovLiveRect.H)
	if err := os.WriteFile(filepath.Join(dir, theme.SidecarFileName), []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestThemeApplyRunsTheOverridesTier is the gate on the CALL SITE, and it is the
// one gate in this file that a mirror cannot fake.
//
// The discipline gates above reach foldSidecarOverrides directly, which is the
// right shape for pinning what the applier DOES and the wrong shape for pinning
// that anything calls it: delete the
// `foldSidecarOverrides(res.layout, res.sidecar)` line from app.go and every one
// of them still passes, because they call it themselves. The tier shipped in
// exactly that state for a whole wave — parsed, documented, exposed, and never
// once invoked on the path a player takes.
//
// So this one drives applyThemeAsync and reads a.themeRects: the preference, the
// apply goroutine, theme.Load, the design tier, the AO2 backstop, the fold,
// pollThemeApply's landing and the player's own layout-editor pass. The same shape
// panelfonts_test.go uses for the font rows, for the same reason.
//
// THE MUTATION IT ANSWERS TO: replace the fold's call in app.go with
// `_ = res.sidecar` and this test must fail on ovLiveMovedKey. If it does not, the
// gate is measuring the mirror again.
func TestThemeApplyRunsTheOverridesTier(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetTheme(ovLiveThemeName, ovWriteLiveTheme(t))
	awaitThemeApply(t, a)

	// The fixture loaded at all. Without this a missing theme would land an empty
	// map and every assertion below would read as "the fold did not run".
	if got, ok := a.themeRects[ao2CanvasKey]; !ok || got != ovLiveCanvas {
		t.Fatalf("the fixture theme did not load: %s = %+v (present=%v), want %+v — nothing below is "+
			"measuring the overrides tier", ao2CanvasKey, got, ok, ovLiveCanvas)
	}

	// The unmoved key proves the AO2 backstop ran, which is what makes the moved
	// key's stock value a meaningful "not applied" answer.
	stockUnmoved := ao2DefaultDesignRects[ovLiveStillStockKey]
	if got, ok := a.themeRects[ovLiveStillStockKey]; !ok || got != stockUnmoved {
		t.Fatalf("%s = %+v (present=%v), want AO2's stock %+v — the backstop did not fill the keys the "+
			"fixture's design file is silent about, so this gate cannot tell an applied override from an "+
			"unapplied one", ovLiveStillStockKey, got, ok, stockUnmoved)
	}

	stockMoved := ao2DefaultDesignRects[ovLiveMovedKey]
	if ovLiveRect == stockMoved {
		t.Fatalf("the fixture overrides %s to AO2's own stock rect %+v — the gate would pass with the "+
			"fold deleted", ovLiveMovedKey, stockMoved)
	}
	got, ok := a.themeRects[ovLiveMovedKey]
	if !ok {
		t.Fatalf("%s is absent from a.themeRects entirely", ovLiveMovedKey)
	}
	if got == stockMoved {
		t.Fatalf("%s landed at AO2's stock %+v, not the theme's authored %+v — pollThemeApply is not "+
			"running the [overrides] tier (app.go, foldSidecarOverrides). Every [overrides] row in every "+
			"theme is inert in that state, and the free elements anchored on those rects resolve against "+
			"the stock geometry instead.", ovLiveMovedKey, stockMoved, ovLiveRect)
	}
	if got != ovLiveRect {
		t.Errorf("%s landed at %+v, want the authored %+v — something between the fold and a.themeRects "+
			"rewrote it", ovLiveMovedKey, got, ovLiveRect)
	}
	// themeRectsOrig is the PRISTINE map the layout editor resets to. An override
	// that reached only the working copy would silently un-apply on the first reset.
	if orig := a.themeRectsOrig[ovLiveMovedKey]; orig != ovLiveRect {
		t.Errorf("themeRectsOrig has %s = %+v, want %+v — the author's rect must be the rect "+
			"\"reset layout\" restores, not the stock one", ovLiveMovedKey, orig, ovLiveRect)
	}
}

// ---------------------------------------------------------------------------
// The discipline
// ---------------------------------------------------------------------------

// ovTestRect / ovTestOther are two distinguishable rects. Named per hard rule 9,
// and distinguishable on every component so a partial write cannot look like a
// clean one.
var (
	ovTestRect  = theme.Rect{X: 11, Y: 22, W: 33, H: 44}
	ovTestOther = theme.Rect{X: 55, Y: 66, W: 77, H: 88}
)

// ovSidecar builds a sidecar carrying exactly the given [overrides] rows, in
// order. It goes through the REAL reader rather than assembling the struct, so a
// row this test believes it wrote is a row the reader would actually produce.
func ovSidecar(t *testing.T, body string) *theme.Sidecar {
	t.Helper()
	sc, err := theme.ParseSidecar([]byte("[overrides]\n" + body))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if sc == nil {
		t.Fatal("fixture parsed to a nil sidecar")
	}
	return sc
}

// TestSidecarOverridesRewriteExistingKeysOnly pins the applier's whole contract.
//
// The "never adds" arm is the load-bearing one. A key absent from the resolved
// design is a widget this build does not place — themeSlots' inert rows, and the
// keys AO2's own default file is silent about — so writing one in would put a rect
// in the map with no draw site behind it: the ghost box the registry was built to
// end (themeslots.go:12-14), and, for an inert key, a box the layout editor would
// then refuse to move.
func TestSidecarOverridesRewriteExistingKeysOnly(t *testing.T) {
	// The map deliberately carries ONE editable key and one inert one, so "present
	// but not editable" and "editable but not present" are both measured.
	// mute_button is the inert specimen. It used to be player_list, which stopped
	// being inert when the roster ruling gave the player list AO2's own rect
	// (theme_layout.go, courtroom.cpp:879) — the contract under test is unchanged.
	layout := map[string]theme.Rect{
		"emote_dropdown": ovTestOther,
		"mute_button":    ovTestOther, // slotStateInert: ingested for audit, painted by nothing
	}
	sc := ovSidecar(t, ""+
		"emote_dropdown = 11, 22, 33, 44\n"+ // present + editable: applied
		"mute_button    = 11, 22, 33, 44\n"+ // present, NOT editable: refused
		"ic_chatlog     = 11, 22, 33, 44\n"+ // editable, NOT present: refused, never added
		"courtroom      = 11, 22, 33, 44\n"+ // the canvas itself is `fixed`: refused
		"not_a_widget   = 11, 22, 33, 44\n") // no themeSlots row at all: refused
	foldSidecarOverrides(layout, sc)

	if got := layout["emote_dropdown"]; got != ovTestRect {
		t.Errorf("emote_dropdown is %+v, want the override %+v — a present, editable key must be rewritten",
			got, ovTestRect)
	}
	if got := layout["mute_button"]; got != ovTestOther {
		t.Errorf("mute_button is %+v, want its design rect %+v untouched — nothing paints it, so an "+
			"override there is a rect the editor cannot move and the screen never shows", got, ovTestOther)
	}
	for _, key := range []string{"ic_chatlog", "courtroom", "not_a_widget"} {
		if r, ok := layout[key]; ok {
			t.Errorf("the fold ADDED %q = %+v — it must rewrite existing keys only (layoutedit.go:832); "+
				"a key the resolved design does not carry is a widget this build does not place", key, r)
		}
	}
	if len(layout) != 2 {
		t.Errorf("the fold changed the key set (%d keys, want 2) — it may only change values", len(layout))
	}
}

// TestSidecarOverrideNilSidecarIsInert is the stock-AO2-theme case: no sidecar, no
// edits, and specifically no panic. A nil *Sidecar reaches this applier on every
// theme that is not an AsyncAO one, and Overrides is a FIELD read, not one of the
// nil-safe accessor methods.
func TestSidecarOverrideNilSidecarIsInert(t *testing.T) {
	layout := map[string]theme.Rect{"emote_dropdown": ovTestOther}
	foldSidecarOverrides(layout, nil)
	if got := layout["emote_dropdown"]; got != ovTestOther {
		t.Errorf("a nil sidecar changed the layout: %+v", got)
	}
}

// TestSidecarOverrideHonoursAO2SecondSpelling pins the one aliased widget.
//
// AO2 probes "immediate" first and falls back to "pre_no_interrupt"
// (courtroom.cpp:1072; themedToggleRect, theme_layout.go:444-459), but AO2's own
// stock design file spells it `pre_no_interrupt`, so that is the only spelling
// applyAO2DefaultRects can supply. An author who writes the spelling AO2 reads
// FIRST — themes/thh_trial writes exactly that — would otherwise have authored a
// row that resolves to nothing at all, which is the silent failure this whole file
// exists to end.
func TestSidecarOverrideHonoursAO2SecondSpelling(t *testing.T) {
	// Only the stock spelling is present, which is what the backstop produces.
	layout := map[string]theme.Rect{"pre_no_interrupt": ovTestOther}
	foldSidecarOverrides(layout, ovSidecar(t, "immediate = 11, 22, 33, 44\n"))
	if got := layout["pre_no_interrupt"]; got != ovTestRect {
		t.Errorf("an `immediate` override left pre_no_interrupt at %+v, want %+v — both spellings index "+
			"one themeSlots row, so the edit belongs in whichever the map carries", got, ovTestRect)
	}
	if _, added := layout["immediate"]; added {
		t.Error("the fold added `immediate` — the alias resolves into the EXISTING spelling; adding the " +
			"other one would be the add this applier does not do")
	}

	// ...and the mirror: the theme declares the first spelling itself, so the row
	// lands there and the stock one is left alone.
	both := map[string]theme.Rect{"immediate": ovTestOther, "pre_no_interrupt": ovTestOther}
	foldSidecarOverrides(both, ovSidecar(t, "immediate = 11, 22, 33, 44\n"))
	if got := both["immediate"]; got != ovTestRect {
		t.Errorf("immediate is %+v, want %+v — a present key is always its own target", got, ovTestRect)
	}
	if got := both["pre_no_interrupt"]; got != ovTestOther {
		t.Errorf("pre_no_interrupt is %+v, want %+v — the alias must not write both", got, ovTestOther)
	}
}
