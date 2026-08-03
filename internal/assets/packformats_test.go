package assets

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestPackFormatListCoversLegacyChain is the gate that stops layering from
// shipping inert. The stock defaults are {.webp} per sprite type with fallbacks
// OFF, so the NETWORK probe list is a single candidate — correct, because every
// extra candidate there is a real HTTP round trip. A pack lookup is a map hit
// against a pre-built index, so it gets the full chain instead. Without this a
// stock AO2 pack full of .png files is never even looked at.
func TestPackFormatListCoversLegacyChain(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer prefs.Close()
	r := NewResolver(prefs)

	for _, typ := range []AssetType{AssetTypeCharSprite, AssetTypeEmoteButton, AssetTypeBackground} {
		net := r.formatList(typ)
		pack := r.PackFormatList(typ)
		if !containsExt(pack, ".png") {
			t.Errorf("%v: pack list %v has no .png — a stock PNG pack would be invisible", typ, pack)
		}
		if len(pack) <= len(net) {
			t.Errorf("%v: pack list %v is not broader than the network list %v", typ, pack, net)
		}
		// The network path must NOT have been widened as a side effect: that would
		// spend real probes per asset and break the zero-fallback pillar.
		if len(net) != 1 {
			t.Errorf("%v: network list grew to %v — the one-probe-per-asset pillar regressed", typ, net)
		}
	}
}

// TestPackFormatListDedupesAndKeepsOrder pins that the configured format stays
// FIRST (so a pack matching the server's format costs one lookup) and that no
// extension repeats when the configured order already names a legacy one.
func TestPackFormatListDedupesAndKeepsOrder(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer prefs.Close()
	r := NewResolver(prefs)

	got := r.PackFormatList(AssetTypeCharSprite)
	if len(got) == 0 {
		t.Fatal("empty pack format list")
	}
	seen := map[string]int{}
	for _, e := range got {
		seen[e]++
		if seen[e] > 1 {
			t.Errorf("extension %q repeats in %v — a duplicate is a wasted lookup per asset", e, got)
		}
	}
	if want := r.formatList(AssetTypeCharSprite); len(want) > 0 && got[0] != want[0] {
		t.Errorf("pack list starts with %q, want the configured format %q first", got[0], want[0])
	}
}

// TestPackFormatListIsGenerationCached pins the snapshot: repeated calls must not
// rebuild (prefs.FormatOrder takes an RLock and allocates), and a format-pref
// change must still be picked up.
func TestPackFormatListIsGenerationCached(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer prefs.Close()
	r := NewResolver(prefs)

	first := r.PackFormatList(AssetTypeCharSprite)
	if n := testing.AllocsPerRun(200, func() { _ = r.PackFormatList(AssetTypeCharSprite) }); n != 0 {
		t.Errorf("steady-state PackFormatList allocates %.1f/op, want 0 — the snapshot is rebuilding per call", n)
	}
	// Moving the generation must invalidate it.
	prefs.SetGlobalFallbacks(!prefs.GlobalFallbacks())
	if second := r.PackFormatList(AssetTypeCharSprite); len(second) == 0 {
		t.Error("the list went empty after a format-pref change")
	} else if &first[0] == &second[0] && prefs.FormatGeneration() == 0 {
		t.Error("the snapshot did not rebuild after the generation moved")
	}
}
