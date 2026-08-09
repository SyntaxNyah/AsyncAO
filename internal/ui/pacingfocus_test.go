package ui

// Gates for the "keep full speed when unfocused" option and the seam it rides
// (pacingfocus.go). One behavioural gate per pacer entry point, plus a source
// census proving none of them can quietly stop consulting the seam.

import (
	"go/ast"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// pacerPrefs is a real preferences value (the option's default must come from
// config, not from a literal in this file) on a throwaway path.
func pacerPrefs(t *testing.T) *config.AssetPreferences {
	t.Helper()
	p, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// TestUnfocusedFullRateShipsOff pins the DEFAULT. It is an option, not a
// behaviour change: a fresh install must pace an unfocused window exactly as it
// did before this preference existed.
func TestUnfocusedFullRateShipsOff(t *testing.T) {
	if pacerPrefs(t).UnfocusedFullRateOn() {
		t.Fatal("UnfocusedFullRate must ship OFF — turning it on by default IS the behaviour change the user ruled out")
	}
}

// TestPacingFocusedOverridesOnlyWhenAsked pins the seam itself: focus is
// untouched, an unfocused window stays unfocused by default, and only the
// preference lifts it.
func TestPacingFocusedOverridesOnlyWhenAsked(t *testing.T) {
	a := &App{d: Deps{Prefs: pacerPrefs(t)}}
	if !a.pacingFocused(true) {
		t.Error("a focused window must stay focused")
	}
	if a.pacingFocused(false) {
		t.Error("with the option off an unfocused window must stay unfocused")
	}
	a.d.Prefs.SetUnfocusedFullRate(true)
	if !a.pacingFocused(false) {
		t.Error("with the option on an unfocused window must pace as focused")
	}
	// A nil Prefs must not panic and must not silently enable the override — the
	// headless harnesses in this package construct App values without preferences.
	if (&App{}).pacingFocused(false) {
		t.Error("a nil Prefs must answer 'unfocused', never 'full rate'")
	}
}

// TestUnfocusedFullRateLiftsEveryPacerTier drives the four pacer answers that an
// unfocused window changes, with the background cap set to its hardest position
// (OFF = never redraw while tabbed out). With the option on, every one of them
// must answer exactly what it answers for a focused window — that is what "treat
// it like a focused window" means, and checking only FramePace would have missed
// the static park, the ceiling and the wake schedule.
func TestUnfocusedFullRateLiftsEveryPacerTier(t *testing.T) {
	prefs := pacerPrefs(t)
	prefs.SetUnfocusedFPS(config.FPSOff) // the hardest background setting there is
	a := &App{d: Deps{Prefs: prefs}}

	if !a.SkipFrame(false, false) {
		t.Fatal("baseline: an unfocused window with the background cap OFF must park")
	}
	if !a.RenderSuppressed(false) {
		t.Fatal("baseline: pending damage must stay suppressed while parked")
	}
	if _, render := a.NextWakeDelay(false); render {
		t.Fatal("baseline: a parked window must schedule no redraw wakes")
	}

	prefs.SetUnfocusedFullRate(true)
	if a.SkipFrame(false, false) != a.SkipFrame(true, false) {
		t.Error("SkipFrame must answer the focused way once the option is on")
	}
	if a.RenderSuppressed(false) {
		t.Error("RenderSuppressed must stop suppressing once the option is on")
	}
	if got, want := a.HardCapBudget(false), a.HardCapBudget(true); got != want {
		t.Errorf("HardCapBudget(unfocused) = %v, want the focused ceiling %v", got, want)
	}
	if got, want := a.FramePace(false), a.FramePace(true); got != want {
		t.Errorf("FramePace(unfocused) = %v, want the focused budget %v", got, want)
	}
	wUnf, rUnf := a.NextWakeDelay(false)
	wFoc, rFoc := a.NextWakeDelay(true)
	if wUnf != wFoc || rUnf != rFoc {
		t.Errorf("NextWakeDelay(unfocused) = (%v,%v), want the focused schedule (%v,%v)", wUnf, rUnf, wFoc, rFoc)
	}
}

// TestEveryPacerEntryConsultsTheFocusSeam is the encapsulation gate. The option
// works ONLY because all five methods the main loop hands a `focused bool` funnel
// it through pacingFocused first; a sixth entry point added later, or one that
// quietly drops the call, would leave the preference half-applied — an unfocused
// window that renders at full rate but is still parked by SkipFrame, which is
// indistinguishable from the option not working at all.
//
// Source, not behaviour, and deliberately: the failure this catches is a MISSING
// call, and no return value distinguishes "consulted the seam and it said no"
// from "never consulted it".
func TestEveryPacerEntryConsultsTheFocusSeam(t *testing.T) {
	for _, fn := range []string{"FramePace", "HardCapBudget", "SkipFrame", "RenderSuppressed", "NextWakeDelay"} {
		body := funcBodySource(t, "app.go", fn)
		if !containsCall(body, "pacingFocused") {
			t.Errorf("%s does not call pacingFocused — the unfocused-full-rate option is bypassed on this path", fn)
		}
	}
	// …and the census must stay complete: every method in app.go whose FIRST
	// parameter is named `focused` is a pacer entry point by construction, so a new
	// one is caught here rather than by a bug report.
	_, f := parsedFile(t, "app.go")
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Recv == nil || fd.Type.Params == nil {
			continue
		}
		if len(fd.Type.Params.List) == 0 || len(fd.Type.Params.List[0].Names) == 0 {
			continue
		}
		if fd.Type.Params.List[0].Names[0].Name != "focused" {
			continue
		}
		if !containsCall(fd.Body, "pacingFocused") {
			t.Errorf("%s takes a `focused` parameter but never calls pacingFocused — add it to the seam or rename the parameter", fd.Name.Name)
		}
	}
}
