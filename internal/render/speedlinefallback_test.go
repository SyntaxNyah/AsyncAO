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

// TestEffectiveSpeedlineBase pins the char-folder → bundled fallback resolution:
// the speaker's own speedline wins when resident, the stock burst otherwise, and
// a non-zoom scene draws nothing (#126).
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

	const charBase = "http://x/base/characters/phoenix/defense_speedlines"
	if err := store.Upload(charBase, decodedFixture()); err != nil {
		t.Fatal(err)
	}
	scene := &courtroom.Scene{SpeedlinesSide: "defense", SpeedlinesBase: charBase}
	if got := effectiveSpeedlineBase(scene, store); got != charBase {
		t.Fatalf("resident char speedline = %q, want %q", got, charBase)
	}

	scene.SpeedlinesBase = "http://x/base/characters/other/defense_speedlines"
	if got := effectiveSpeedlineBase(scene, store); got != SpeedlineDefenseKey {
		t.Fatalf("missing char speedline = %q, want %q", got, SpeedlineDefenseKey)
	}
}
