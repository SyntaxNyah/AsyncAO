package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

func newFormatsTestPrefs(t *testing.T) *config.AssetPreferences {
	t.Helper()
	p, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return p
}

// TestPinnedProfileSeedsEveryImageClass is the contract behind the Formats
// tab's per-server one-click fix: clicking "PNG" must actually make every kind
// of streamed image ask this server for PNG. The button writes an
// extensions.json document, so the guarantee is only real if that document
// parses AND covers every manifest class — a typo'd key would parse fine and
// silently fix nothing, which is exactly the failure the tab exists to end.
func TestPinnedProfileSeedsEveryImageClass(t *testing.T) {
	const host = "example.com"
	for _, ext := range config.OptionalImageFormats {
		m, err := assets.ParseManifest([]byte(pinnedProfileJSON(ext)))
		if err != nil {
			t.Fatalf("pinnedProfileJSON(%s) does not parse: %v", ext, err)
		}
		p := newFormatsTestPrefs(t)
		if n := m.SeedLearned(p, host); n == 0 {
			t.Fatalf("pinnedProfileJSON(%s) seeded nothing", ext)
		}
		// Every class a player would notice missing. DeskOverlay is included
		// since the 2026-08-08 default flip: desks follow the manifest by default
		// like every other class, so a pin covers them too. (A player who ticked
		// "Always use WebP for desks" opts desks out of ANY seeding — see
		// Manifest.SeedLearned — but that is their explicit choice, not this
		// profile's business.)
		want := []string{
			config.TypeCharIcon,
			config.TypeCharSprite,
			config.TypeShoutBubble,
			config.TypeEmoteButton,
			config.TypeBackground,
			config.TypeDeskOverlay,
		}
		learned := p.LearnedSnapshot()
		for _, typeName := range want {
			got, ok := learned[config.LearnedKey(host, typeName)]
			if !ok {
				t.Errorf("pinning %s left %s unpinned — that class would still ask for the wrong type", ext, typeName)
				continue
			}
			if len(got) != 1 || got[0] != ext {
				t.Errorf("pinning %s gave %s = %v, want [%s]", ext, typeName, got, ext)
			}
		}
	}
}

// TestPinnedProfileIsScopedToOneHost pins the "scoped correctly" half of the
// fix: a profile is stored per host, so fixing one server must leave every
// other server exactly as it was.
func TestPinnedProfileIsScopedToOneHost(t *testing.T) {
	p := newFormatsTestPrefs(t)
	const fixed, other = "needs-png.example.com", "fine.example.com"
	p.SetExtProfile(fixed, pinnedProfileJSON(config.ExtPNG))
	if got := p.ExtProfile(fixed); got == "" {
		t.Fatal("the pinned host has no profile")
	}
	if got := p.ExtProfile(other); got != "" {
		t.Errorf("an unrelated host picked up a profile: %q", got)
	}
	p.SetExtProfile(fixed, "")
	if got := p.ExtProfile(fixed); got != "" {
		t.Errorf("unpinning left %q behind", got)
	}
}

// TestFormatPresetLabelsCoverEveryOfferedFormat guards the two parallel slices
// the tab indexes with one loop variable: a format added to
// config.OptionalImageFormats without a label would draw a blank button, and a
// short slice would panic on the last one.
func TestFormatPresetLabelsCoverEveryOfferedFormat(t *testing.T) {
	if len(formatPresetLabels) != len(config.OptionalImageFormats) {
		t.Fatalf("labels = %d, offered formats = %d", len(formatPresetLabels), len(config.OptionalImageFormats))
	}
	if len(formatPresetTips) != len(config.OptionalImageFormats) {
		t.Fatalf("tips = %d, offered formats = %d", len(formatPresetTips), len(config.OptionalImageFormats))
	}
	for i, ext := range config.OptionalImageFormats {
		if formatPresetLabels[i] == "" {
			t.Errorf("%s has no button label", ext)
		}
		if !strings.Contains(formatPresetTips[i], formatPresetLabels[i]) {
			t.Errorf("%s tip %q does not name its own format", ext, formatPresetTips[i])
		}
	}
	for _, name := range config.TypeNames {
		if typeRowLabels[name] != name+":" {
			t.Errorf("typeRowLabels[%s] = %q, want %q", name, typeRowLabels[name], name+":")
		}
	}
	if len(audioFallbackLabels) != len(audioTypeNames) {
		t.Fatalf("audio labels = %d, audio types = %d", len(audioFallbackLabels), len(audioTypeNames))
	}
}

// TestFormatOrderTextTracksGeneration pins the Formats tab's per-frame budget.
// The settings screen has NO allocation gate, so nothing else would catch this
// cache going stale (wrong text forever) or never caching (string building on
// every frame the tab is open). Both directions are asserted here.
func TestFormatOrderTextTracksGeneration(t *testing.T) {
	p := newFormatsTestPrefs(t)
	settings.fmtOrderOK, settings.fmtOrderGen, settings.fmtOrderText = false, 0, nil
	t.Cleanup(func() { settings.fmtOrderOK, settings.fmtOrderGen, settings.fmtOrderText = false, 0, nil })

	first := formatOrderText(p)
	if len(first) != len(config.TypeNames) {
		t.Fatalf("formatOrderText returned %d rows, want %d (one per asset type)", len(first), len(config.TypeNames))
	}
	// Indexed by assets.AssetType, so the sprite row must describe sprites.
	spriteRow := first[assets.AssetTypeCharSprite]
	if !strings.Contains(spriteRow, config.ExtWebP) {
		t.Errorf("CharSprite row = %q, want it to mention %s (the shipped default)", spriteRow, config.ExtWebP)
	}
	gen := settings.fmtOrderGen

	// No preference change: the cache must answer without rebuilding.
	if second := formatOrderText(p); settings.fmtOrderGen != gen {
		t.Errorf("generation moved from %d to %d without a preference change (rebuilt every call)", gen, settings.fmtOrderGen)
	} else if len(second) != len(first) {
		t.Fatalf("cached read returned %d rows, want %d", len(second), len(first))
	}

	// A real change must invalidate it, or the tab would show stale formats.
	p.SetFormatOrder(config.TypeCharSprite, []string{config.ExtPNG})
	after := formatOrderText(p)
	if settings.fmtOrderGen == gen {
		t.Error("the format generation did not move after SetFormatOrder — the cache would go stale")
	}
	if got := after[assets.AssetTypeCharSprite]; !strings.Contains(got, config.ExtPNG) {
		t.Errorf("CharSprite row = %q after pinning %s, want it to mention it", got, config.ExtPNG)
	}
}
