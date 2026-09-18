package render

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestSpeedlineFallbackKey pins the ZoomSide → bundled speedline key mapping (#126).
func TestSpeedlineFallbackKey(t *testing.T) {
	if got := SpeedlineFallbackKey("defense"); got != SpeedlineDefenseKey {
		t.Errorf("defense = %q, want %q", got, SpeedlineDefenseKey)
	}
	if got := SpeedlineFallbackKey("prosecution"); got != SpeedlineProsecutionKey {
		t.Errorf("prosecution = %q, want %q", got, SpeedlineProsecutionKey)
	}
	for _, s := range []string{"", "jud", "unknown"} {
		if got := SpeedlineFallbackKey(s); got != "" {
			t.Errorf("SpeedlineFallbackKey(%q) = %q, want empty", s, got)
		}
	}
}

// TestEffectiveSpeedlineBase pins the char-folder chain → bundled fallback
// resolution: the FIRST resident candidate wins (most specific first), the stock
// burst is drawn when the character ships none of the chain, and a non-zoom scene
// draws nothing (#126).
func TestEffectiveSpeedlineBase(t *testing.T) {
	// No zoom → empty (the side check short-circuits before the store is touched).
	if got := effectiveSpeedlineBase(&courtroom.Scene{}, nil); got != "" {
		t.Fatalf("no zoom = %q, want empty", got)
	}

	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	const posBase = "http://x/base/characters/judge/jud_speedlines"
	const sideBase = "http://x/base/characters/judge/defense_speedlines"
	if err := store.Upload(sideBase, decodedFixture()); err != nil {
		t.Fatal(err)
	}
	scene := &courtroom.Scene{
		SpeedlinesSide:  "defense",
		SpeedlinesChain: []string{posBase, sideBase},
	}
	// Only the SIDE stem is resident: the chain's first entry misses and the
	// second wins — the chain is a walk, not a single lookup.
	if got := effectiveSpeedlineBase(scene, store); got != sideBase {
		t.Fatalf("chain walk = %q, want the resident side stem %q", got, sideBase)
	}

	// The position's own art now lands: it is MORE specific, so it wins.
	if err := store.Upload(posBase, decodedFixture()); err != nil {
		t.Fatal(err)
	}
	if got := effectiveSpeedlineBase(scene, store); got != posBase {
		t.Fatalf("resident <pos> art = %q, want %q (most specific first)", got, posBase)
	}

	// Nothing in the chain is resident → the bundled stock burst for the side.
	scene.SpeedlinesChain = []string{
		"http://x/base/characters/other/jud_speedlines",
		"http://x/base/characters/other/defense_speedlines",
	}
	if got := effectiveSpeedlineBase(scene, store); got != SpeedlineDefenseKey {
		t.Fatalf("missing char speedline = %q, want %q", got, SpeedlineDefenseKey)
	}

	// An EMPTY chain is what a zoom can no longer produce (ZoomSide never returns
	// ""), but the bundled tier must still answer if one ever arrives.
	scene.SpeedlinesChain = nil
	if got := effectiveSpeedlineBase(scene, store); got != SpeedlineDefenseKey {
		t.Fatalf("empty chain = %q, want the bundled fallback %q", got, SpeedlineDefenseKey)
	}
}
