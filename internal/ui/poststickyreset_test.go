package ui

import (
	"go/ast"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// AO2's Courtroom::reset_ui post-send block (courtroom.cpp:2320-2363) has three
// independently-preferenced clears, and every one of them reads its option out of
// AssetPreferences. These gates DRIVE clearSFXAfterSend / clearPreAfterSend /
// clearEffectsAfterSend through REAL config.AssetPreferences at both polarities,
// because the previous coverage was a mirror: it either exercised the pref at the
// config layer (defaults + round-trip) or called the private rule with a
// hand-passed bool, so deleting the :2340 idle guard or hard-coding the effects
// global back to `true` left every gate green.

// newStickyPrefsApp builds an App on a REAL preference store, so a rule that stops
// consulting Prefs (or consults the wrong key) fails here instead of being papered
// over by a zero-valued struct that happens to answer false.
func newStickyPrefsApp(t *testing.T) (*App, *config.AssetPreferences) {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	return &App{d: Deps{Prefs: prefs}}, prefs
}

// stickySFXPickedRow is any non-zero IC SFX dropdown row: row 0 is the "Default"
// (auto) row the clear resets TO, so the fixture has to sit off it to be visible.
const stickySFXPickedRow = 3

// TestClearSFXAfterSendReadsTheRealPreferences is the courtroom.cpp:2340 truth
// table, driven end to end:
//
//	!clearSoundsDropdownOnPlayEnabled()  ==  !StickySoundsOn()
//	&& (playSelectedSFXOnIdle() || ui_pre->isChecked())
//
// The second conjunct is the half that had NO behavioural gate at all — deleting it
// outright was mutation-proven green. It exists so a pick made on an idle line (a
// stock client transmits that silently) is not thrown away before the user gets to
// tick Pre and send it for real.
func TestClearSFXAfterSendReadsTheRealPreferences(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stickySounds bool
		sfxOnIdle    bool
		pre          bool
		wantCleared  bool
	}{
		// Sticky Sounds is AO2's shipped default (defaultStickySounds = true): the
		// pick survives whatever the other two say.
		{"shipped default keeps the pick", true, false, false, false},
		{"sticky wins even with Pre ticked", true, false, true, false},
		{"sticky wins even with sfx_on_idle", true, true, false, false},
		// Sticky OFF: the :2340 disjunction decides.
		{"not sticky, idle line, no promotion — the pick is KEPT", false, false, false, false},
		{"not sticky, Pre ticked", false, false, true, true},
		{"not sticky, sfx_on_idle promotes the sound", false, true, false, true},
		{"not sticky, both", false, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, prefs := newStickyPrefsApp(t)
			prefs.SetStickySounds(tc.stickySounds)
			prefs.SetSFXOnIdle(tc.sfxOnIdle)
			a.icPreanim = tc.pre
			a.sfxChoiceIdx = stickySFXPickedRow
			a.clearSFXAfterSend()
			cleared := a.sfxChoiceIdx == 0
			if cleared != tc.wantCleared {
				t.Errorf("sfxChoiceIdx=%d (cleared=%v), want cleared=%v — courtroom.cpp:2340",
					a.sfxChoiceIdx, cleared, tc.wantCleared)
			}
		})
	}
}

// TestClearPreAfterSendReadsTheRealPreferences — courtroom.cpp:2356-2359. One
// condition upstream, one here; this function had no test whatsoever.
func TestClearPreAfterSendReadsTheRealPreferences(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sticky   bool
		wantPre  bool
		wantWhy  string
		startPre bool
	}{
		{"shipped default keeps Pre ticked", true, true, "defaultStickyPreanims = true", true},
		{"sticky off unticks Pre", false, false, "courtroom.cpp:2358 ui_pre->setChecked(false)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, prefs := newStickyPrefsApp(t)
			prefs.SetStickyPreanims(tc.sticky)
			a.icPreanim = tc.startPre
			a.clearPreAfterSend()
			if a.icPreanim != tc.wantPre {
				t.Errorf("icPreanim = %v, want %v (%s)", a.icPreanim, tc.wantPre, tc.wantWhy)
			}
		})
	}
}

// stickyFXFixture arms the effects roster the post-send reset walks: row 1 is a
// plain effect, row 2 declares `sticky` (courtroom.cpp:2348's
// get_effect_property(..., "sticky")).
func stickyFXFixture(a *App) {
	a.urls = courtroom.NewURLBuilder("https://one.test/base")
	fx := []theme.OverlayFX{{Name: "tv on", Sound: "switch tv"}, {Name: "sticky one", Sound: "s", Sticky: true}}
	a.overlayRosters = map[string]*overlayRoster{
		a.overlayRosterKey(""): {set: theme.MergeOverlayFX([]string{"tv on", "sticky one"}, fx)},
	}
}

const (
	stickyFXPlainRow  = 1 // "tv on" — no sticky property
	stickyFXStickyRow = 2 // "sticky one" — sticky=true
)

// TestClearEffectsAfterSendReadsTheRealPreference drives the PUBLIC entry point
// (not overlayPostSendReset with a hand-passed bool) so that reverting
// clearEffectsAfterSend to a constant — the exact mutation that stayed green —
// fails. Both polarities of Prefs.StickyEffectsOn, and with it OFF both polarities
// of the per-effect `sticky` property: courtroom.cpp:2348-2354.
func TestClearEffectsAfterSendReadsTheRealPreference(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sticky  bool
		row     int
		wantRow int
	}{
		{"shipped default keeps a plain effect", true, stickyFXPlainRow, stickyFXPlainRow},
		{"shipped default keeps a sticky effect", true, stickyFXStickyRow, stickyFXStickyRow},
		{"global off clears a plain effect to None", false, stickyFXPlainRow, 0},
		{"global off still keeps a sticky effect", false, stickyFXStickyRow, stickyFXStickyRow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, prefs := newStickyPrefsApp(t)
			prefs.SetStickyEffects(tc.sticky)
			stickyFXFixture(a)
			a.overlayChoiceIdx = tc.row
			a.clearEffectsAfterSend()
			if a.overlayChoiceIdx != tc.wantRow {
				t.Errorf("overlayChoiceIdx = %d, want %d — courtroom.cpp:2348-2354 with stickyeffects=%v",
					a.overlayChoiceIdx, tc.wantRow, tc.sticky)
			}
		})
	}
}

// TestStickyResetsSurviveAPreferenceRoundTrip closes the save-but-never-load class
// for these four keys AT THE CONSUMER: the rules are re-driven against a store
// reopened from disk, so a key that persists but does not reload would flip the
// observed behaviour back to the shipped default here.
func TestStickyResetsSurviveAPreferenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), config.PrefsFileName)
	prefs, err := config.New(path)
	if err != nil {
		t.Fatal(err)
	}
	prefs.SetStickySounds(false)
	prefs.SetStickyEffects(false)
	prefs.SetStickyPreanims(false)
	prefs.SetSFXOnIdle(true)
	if err := prefs.Close(); err != nil { // Close flushes the debounced saver
		t.Fatal(err)
	}

	reloaded, err := config.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	a := &App{d: Deps{Prefs: reloaded}}
	stickyFXFixture(a)

	a.sfxChoiceIdx, a.icPreanim = stickySFXPickedRow, false
	a.clearSFXAfterSend()
	if a.sfxChoiceIdx != 0 {
		t.Error("after a reload, sfx_on_idle=true must still let an idle-line send clear the SFX pick")
	}
	a.icPreanim = true
	a.clearPreAfterSend()
	if a.icPreanim {
		t.Error("after a reload, stickyPreanims=false must still untick Pre")
	}
	a.overlayChoiceIdx = stickyFXPlainRow
	a.clearEffectsAfterSend()
	if a.overlayChoiceIdx != 0 {
		t.Error("after a reload, stickyEffects=false must still clear a plain effect to None")
	}
}

// TestSendICRunsEveryPostSendReset is the WIRING gate. The three rules are correct
// in isolation and worthless if the send path stops calling one, which no
// behavioural test above would notice. Source, because sendIC needs a live session
// to run: courtroom.cpp calls reset_ui once per send and so must we.
func TestSendICRunsEveryPostSendReset(t *testing.T) {
	body := funcBodySource(t, "screens.go", "sendIC")
	for _, fn := range []string{"clearEffectsAfterSend", "clearSFXAfterSend", "clearPreAfterSend"} {
		if !containsCall(body, fn) {
			t.Errorf("sendIC no longer calls %s — that half of AO2's reset_ui (courtroom.cpp:2320-2363) stopped shipping", fn)
		}
	}
	// The one-shots AO2 clears in the same block, unconditionally: realization,
	// screenshake and the slide ("Slides can't be sticky for nausea reasons",
	// :2362). They are plain assignments, not calls, so they need their own scan —
	// without it a refactor could move the sticky resets into a helper and drop
	// these on the floor with the wiring gate still green.
	for _, f := range []string{"icSlide", "icRealize", "icShake"} {
		if !bodyAssignsField(body, f) {
			t.Errorf("sendIC must clear the one-shot arm %s beside the sticky resets (courtroom.cpp:2327-2329, :2362)", f)
		}
	}
}

// bodyAssignsField reports whether n contains an assignment whose left-hand side
// includes a selector ending in `.<field>` (i.e. `a.icSlide = ...`, including the
// multi-target form the send path uses).
func bodyAssignsField(n ast.Node, field string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok {
			return !found
		}
		for _, lhs := range as.Lhs {
			if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
				found = true
			}
		}
		return !found
	})
	return found
}
