package render

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestGlitchParamsMode pins the per-mode fringe/jolt tuning: Classic passes the
// original parameters through untouched, Heavy/Echo widen the split by their named
// multipliers, Echo keeps the classic jolt, and every jolt stays inside its mode's
// bounded amplitude — across a few seconds of clock so both window states are hit.
func TestGlitchParamsMode(t *testing.T) {
	const vpH = 400
	for clock := time.Duration(0); clock < 3*time.Second; clock += 17 * time.Millisecond {
		cs, cj := glitchParamsMode(courtroom.GlitchClassic, clock, vpH)
		bs, bj := glitchParams(clock, vpH)
		if cs != bs || cj != bj {
			t.Fatalf("classic must pass through: got %d,%d want %d,%d", cs, cj, bs, bj)
		}
		hs, hj := glitchParamsMode(courtroom.GlitchHeavy, clock, vpH)
		if hs != cs*glitchHeavySplitMul {
			t.Fatalf("heavy split = %d, want %d", hs, cs*glitchHeavySplitMul)
		}
		if max := hs * glitchHeavyJoltMul; hj < -max || hj > max {
			t.Fatalf("heavy jolt %d exceeds ±%d", hj, max)
		}
		es, ej := glitchParamsMode(courtroom.GlitchEcho, clock, vpH)
		if es != cs*glitchEchoSplitMul || ej != cj {
			t.Fatalf("echo = %d,%d want %d,%d (wider split, classic jolt)", es, ej, cs*glitchEchoSplitMul, cj)
		}
		ss, sj := glitchParamsMode(courtroom.GlitchStatic, clock, vpH)
		if ss != cs || sj != cj {
			t.Fatalf("static keeps the classic split/jolt, got %d,%d want %d,%d", ss, sj, cs, cj)
		}
	}
	// Heavy jolts inside its window and rests between windows.
	if _, j := glitchParamsMode(courtroom.GlitchHeavy, glitchHeavyJoltWindow/2, vpH); j == 0 {
		t.Error("heavy: no jolt inside the jolt window")
	}
	rest := glitchHeavyJoltWindow + (glitchHeavyJoltPeriod-glitchHeavyJoltWindow)/2
	if _, j := glitchParamsMode(courtroom.GlitchHeavy, rest, vpH); j != 0 {
		t.Errorf("heavy: jolt %d between windows, want 0", j)
	}
}

// TestGlitchFlickerPct bounds Static's signal-loss alpha: every bucket lands either
// on the hard dropout or inside [floor, 99] — never darker than the dropout, never
// a full blackout, never above the sprite's own alpha.
func TestGlitchFlickerPct(t *testing.T) {
	sawNormal := false
	for clock := time.Duration(0); clock < 10*time.Second; clock += glitchStaticBucket {
		pct := glitchFlickerPct(clock)
		if pct != glitchStaticDropPct && (pct < glitchStaticFloorPct || pct > 99) {
			t.Fatalf("flicker %d%% out of range at %v", pct, clock)
		}
		if pct >= glitchStaticFloorPct {
			sawNormal = true
		}
	}
	if !sawNormal {
		t.Error("flicker never reached the normal band — every bucket was a dropout")
	}
}

// TestGlitchHashSpreads sanity-checks the offset scrambler: deterministic, and
// consecutive inputs don't collapse to one value (the torn bands would all shove
// the same way).
func TestGlitchHashSpreads(t *testing.T) {
	if glitchHash(42) != glitchHash(42) {
		t.Fatal("glitchHash must be deterministic")
	}
	seen := map[uint32]bool{}
	for i := uint32(0); i < 16; i++ {
		seen[glitchHash(i*glitchTornBandSalt)] = true
	}
	if len(seen) < 12 {
		t.Errorf("hash collapsed: %d distinct of 16", len(seen))
	}
}
