package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestDPISeedNeverPersists is the load-bearing #77 Part-B contract: seeding the
// scale from the OS DPI is RUNTIME-ONLY. It must never write the UI-scale
// preference — a persisted seed would look like a user choice, so moving to a
// different monitor would stop re-seeding (and turning Auto off would then keep a
// stale seeded value instead of the real default). SetDisplayDPIScale (the sink
// SeedDisplayDPIScale funnels through) must touch detected state only.
func TestDPISeedNeverPersists(t *testing.T) {
	a := testTabApp(t)
	before := a.d.Prefs.UIScale()
	a.SetDisplayDPIScale(150) // as if a 144-dpi monitor seeded 150%
	if got := a.d.Prefs.UIScale(); got != before {
		t.Fatalf("DPI seed leaked into the persisted scale: prefs = %d, want unchanged %d", got, before)
	}
	if a.dpiScalePct != 150 {
		t.Fatalf("SetDisplayDPIScale must record the DPI component, got %d", a.dpiScalePct)
	}
}

// TestDPISeedFloor pins the never-auto-shrink rule (#6, kept for Part B): an
// unreliable / low DPI reading must never drive the auto scale below 100.
func TestDPISeedFloor(t *testing.T) {
	a := testTabApp(t)
	a.SetDisplayDPIScale(80) // e.g. a bogus sub-baseline reading
	if a.dpiScalePct != config.MinAutoUIScalePercent {
		t.Fatalf("sub-100 DPI seed must floor at %d, got %d", config.MinAutoUIScalePercent, a.dpiScalePct)
	}
}

// TestExplicitScaleWinsOverSeed pins that an explicitly-saved scale ALWAYS wins:
// with Auto off (the only state in which a user can commit a manual scale),
// UIScale() returns the manual value and ignores the DPI-seeded detected value
// entirely. This is the structural reason Part B needs no new "user chose it"
// marker pref — UIScaleAuto IS the marker.
func TestExplicitScaleWinsOverSeed(t *testing.T) {
	a := testTabApp(t)
	// Simulate a HiDPI seed having fired…
	a.detectedScalePct = 150
	a.dpiScalePct = 150
	// …and a user who explicitly picked 125% (Auto off is required to reach the slider).
	a.d.Prefs.SetUIScaleAuto(false)
	a.uiScalePct = 125
	if got := a.UIScale(); got != 125 {
		t.Fatalf("explicit scale must win over the DPI seed: UIScale() = %d, want 125", got)
	}
}

// TestSeededScaleFollowsAutoWhenUnset is the complementary case: while the user
// has NEVER explicitly chosen a scale (Auto on, the default), UIScale() follows
// the DPI-seeded detected value — so a 150% monitor starts at 150%.
func TestSeededScaleFollowsAutoWhenUnset(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetUIScaleAuto(true)
	a.detectedScalePct = 150 // as SetAutoScaleFromWindow would compute from the seed
	if got := a.UIScale(); got != 150 {
		t.Fatalf("with Auto on, UIScale() must follow the seed: got %d, want 150", got)
	}
}

// TestSeedAndNoteDisplayNilWindowSafe pins that both entry points are nil-window
// safe (headless / pre-window paths): they must not panic or seed. lastDPIDisplayIndex
// starts at -1 (no query yet).
func TestSeedAndNoteDisplayNilWindowSafe(t *testing.T) {
	a := testTabApp(t) // ctx has no window
	a.lastDPIDisplayIndex = -1
	a.dpiScalePct = 0
	a.SeedDisplayDPIScale()
	a.NoteDisplayChanged()
	if a.dpiScalePct != 0 {
		t.Fatalf("no window → no seed, but dpiScalePct = %d", a.dpiScalePct)
	}
	if a.lastDPIDisplayIndex != -1 {
		t.Fatalf("no window → display index untouched, got %d", a.lastDPIDisplayIndex)
	}
}
