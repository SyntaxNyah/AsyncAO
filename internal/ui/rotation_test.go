package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// A zero App is enough for the rotation resolvers: they read only classicRot /
// themeLay.ang and never touch SDL. These pin the architect's ruling-1 invariant
// at the resolver level — the draw sites route angle==0 through the plain Copy
// path, so a resolver returning exactly 0 for an unrotated element is what keeps
// the unedited courtroom byte-identical.

func TestClassicSlotRotationResolve(t *testing.T) {
	var a App
	// No rotation map at all → 0 (the plain-Copy path), the settled/unedited case.
	if got := a.classicSlotRotation(slotViewport); got != 0 {
		t.Fatalf("nil classicRot → %g, want 0 (plain Copy path)", got)
	}
	a.classicRot = map[string]uint8{slotViewport: 0, slotOOC: 64}
	// An explicit 0 byte still resolves 0 (routes to Copy).
	if got := a.classicSlotRotation(slotViewport); got != 0 {
		t.Fatalf("explicit-0 byte → %g, want 0", got)
	}
	// A nonzero byte resolves to its degrees (routes to CopyEx).
	if got := a.classicSlotRotation(slotOOC); got != 90 {
		t.Fatalf("byte 64 → %g, want 90", got)
	}
	// An absent key → 0.
	if got := a.classicSlotRotation("nope"); got != 0 {
		t.Fatalf("absent key → %g, want 0", got)
	}
}

// TestClassicSlotRotationNoAlloc pins that the always-drawn resolve stays off the
// heap on the settled path (the unrotated case reads a nil/empty map).
func TestClassicSlotRotationNoAlloc(t *testing.T) {
	var a App
	if n := testing.AllocsPerRun(200, func() {
		_ = a.classicSlotRotation(slotViewport) // nil map: the settled-gate case
	}); n != 0 {
		t.Fatalf("classicSlotRotation allocates %v/op on the unrotated path; want 0", n)
	}
	a.classicRot = map[string]uint8{slotOOC: 128}
	if n := testing.AllocsPerRun(200, func() {
		_ = a.classicSlotRotation(slotOOC) // rotated: a map probe + float mul, still 0-alloc
	}); n != 0 {
		t.Fatalf("classicSlotRotation allocates %v/op on the rotated path; want 0", n)
	}
}

func TestThemedRotationResolve(t *testing.T) {
	var a App
	// nil ang cache → 0.
	if got := a.themedRotationDeg("defense_bar"); got != 0 {
		t.Fatalf("nil ang → %g, want 0", got)
	}
	a.themeLay.ang = map[string]uint8{"defense_bar": 0, "call_mod": 192}
	if got := a.themedRotationDeg("defense_bar"); got != 0 {
		t.Fatalf("explicit-0 byte → %g, want 0", got)
	}
	if got := a.themedRotationDeg("call_mod"); got != 270 {
		t.Fatalf("byte 192 → %g, want 270", got)
	}
	// The cache accessor agrees with the App helper.
	if got := a.themeLay.angle("call_mod"); got != 270 {
		t.Fatalf("themeLayoutCache.angle = %g, want 270", got)
	}
	if got := a.themeLay.angle("absent"); got != 0 {
		t.Fatalf("absent key angle = %g, want 0", got)
	}
}

// TestNextRotationByte pins the R-key cycle: coarse advances 0→90→180→270→0, and
// the Shift fine step advances ~15° recomputed from degrees (not nudged on the
// byte) so quantization doesn't accumulate.
func TestNextRotationByte(t *testing.T) {
	// Coarse cycle.
	cur := uint8(0)
	for _, want := range []uint8{64, 128, 192, 0} {
		cur = nextRotationByte(cur, false)
		if cur != want {
			t.Fatalf("coarse cycle: got %d, want %d", cur, want)
		}
	}
	// Fine step: 0 → ~15° → byte 11 (15*256/360 rounds to 11).
	if got := nextRotationByte(0, true); got != config.RotationDegToByte(15) {
		t.Fatalf("fine step from 0 = %d, want %d", got, config.RotationDegToByte(15))
	}
	// Fine step recomputes from degrees: a byte at 90° + 15° = 105° → its byte.
	if got := nextRotationByte(64, true); got != config.RotationDegToByte(105) {
		t.Fatalf("fine step from 90° = %d, want %d (105°)", got, config.RotationDegToByte(105))
	}
}

// TestRotationChipLabel pins the banner readout: empty at angle 0, "Rot N°" for a
// nonzero angle rounded to whole degrees.
func TestRotationChipLabel(t *testing.T) {
	if got := rotationChipLabel(0); got != "" {
		t.Fatalf("chip at angle 0 = %q, want empty", got)
	}
	if got := rotationChipLabel(64); got != "Rot 90°" {
		t.Fatalf("chip at byte 64 = %q, want \"Rot 90°\"", got)
	}
	if got := rotationChipLabel(192); got != "Rot 270°" {
		t.Fatalf("chip at byte 192 = %q, want \"Rot 270°\"", got)
	}
}

// TestClassicSlotRotatablePredicate pins ruling 5's classic result: no classic
// slot is texture-backed today, so the predicate is false everywhere and the R-key
// reports n/a (never persists a rotation for a non-rotatable slot).
func TestClassicSlotRotatablePredicate(t *testing.T) {
	for _, slot := range []string{slotViewport, slotOOC, slotChatbox, slotEmotes, slotControls} {
		if classicSlotRotatable(slot) {
			t.Fatalf("classicSlotRotatable(%q) = true, want false (no classic slot is texture-backed)", slot)
		}
	}
}
