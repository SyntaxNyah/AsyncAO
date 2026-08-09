package assets

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// mountWith creates a mount folder holding one file at rel and returns the path.
func mountWith(t *testing.T, rel, body string) string {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRetiredMountOriginsStayServable is the regression pin for the mount-rescan
// origin race, at the generation depth the field report actually reaches.
//
// A LocalFetcher's origin hashes its whole mount list (local.go NewLocalFetcher),
// so attaching a folder MINTS A NEW ORIGIN and the old fetcher refuses the old
// URLs ("is not under local origin"). Routing by origin fixed the first
// generation — the overlay serves the new URLs, the Manager's own source keeps
// serving the ones minted at startup. But a URL builder is PER-TAB and the UI
// does not re-mint it on a tab switch (ui/tabs.go activateTab), so after a
// SECOND change a parked tab holds generation-2 URLs while the overlay is on 3
// and the fixed source on 1: nothing claims them, and only a restart fixes it —
// the same symptom, one attach later.
//
// Every generation inside the cap must keep answering its own URLs, and each
// must answer ONLY its own (the cache separation is the feature, not a
// side-effect).
func TestRetiredMountOriginsStayServable(t *testing.T) {
	const rel = "characters/Phoenix/(a)normal.webp"
	gens := []struct {
		mount string
		body  string
	}{
		{mountWith(t, rel, "GEN1"), "GEN1"},
		{mountWith(t, rel, "GEN2"), "GEN2"},
		{mountWith(t, rel, "GEN3"), "GEN3"},
	}

	// Generation 1 is the Manager's construction-time source (local mode: exactly
	// what cmd/asyncao builds at startup).
	startup := NewLocalFetcher([]string{gens[0].mount})
	rig := newRig(t, startup, true)
	rig.manager.SetLocalOverlay(startup) // what rebuildAssetOrigin pushes on boot

	fetchers := []*LocalFetcher{startup}
	for _, g := range gens[1:] {
		lf := NewLocalFetcher([]string{g.mount})
		rig.manager.SetLocalOverlay(lf) // the user attaches another folder
		fetchers = append(fetchers, lf)
	}

	for i, lf := range fetchers {
		url := lf.BaseURL() + rel
		data, err := rig.manager.FetchRaw(context.Background(), url)
		if err != nil {
			t.Errorf("generation %d URL %q: %v", i+1, url, err)
			continue
		}
		if string(data) != gens[i].body {
			t.Errorf("generation %d served %q, want %q — an origin answered another generation's URL", i+1, data, gens[i].body)
		}
	}
}

// TestRetiredMountOriginsAreBounded pins the cap: the history is a ring, not a
// leak, and the OLDEST generations are the ones that fall off (rule §17.4).
func TestRetiredMountOriginsAreBounded(t *testing.T) {
	const rel = "background/gs4/defenseempty.webp"
	rig := newRig(t, network.NewClient(), false)

	// One more generation than the cap can hold, plus the live overlay.
	total := localOriginHistoryCap + 2
	var fetchers []*LocalFetcher
	for i := 0; i < total; i++ {
		lf := NewLocalFetcher([]string{mountWith(t, rel, "gen")})
		rig.manager.SetLocalOverlay(lf)
		fetchers = append(fetchers, lf)
	}

	if retired := rig.manager.localRetired.Load(); retired == nil || len(*retired) > localOriginHistoryCap {
		got := 0
		if retired != nil {
			got = len(*retired)
		}
		t.Fatalf("retired origins = %d, want ≤ %d", got, localOriginHistoryCap)
	}

	// The live overlay plus the last localOriginHistoryCap generations resolve.
	for i := total - 1 - localOriginHistoryCap; i < total; i++ {
		if _, err := rig.manager.FetchRaw(context.Background(), fetchers[i].BaseURL()+rel); err != nil {
			t.Errorf("generation %d (within the cap) no longer resolves: %v", i+1, err)
		}
	}
	// The oldest one is gone — a streaming Manager has no LocalFetcher source to
	// fall back on, so it reports "cannot serve", never a fake 404.
	_, err := rig.manager.FetchRaw(context.Background(), fetchers[0].BaseURL()+rel)
	if err == nil {
		t.Error("the evicted generation still resolved — the ring is not bounded")
	} else if !strings.Contains(err.Error(), ErrLocalOverlayUnavailable.Error()) {
		t.Errorf("evicted generation error = %v, want %v (a miss here is NOT a 404)", err, ErrLocalOverlayUnavailable)
	}
}

// TestRePushingTheSameMountSetRetiresNothing pins the no-op case. Settings
// re-pushes the overlay on every preference change that touches assets, most of
// which do not touch the mount list at all; if each of those retired a
// generation, the ring would flush the real history within a few clicks and the
// hole would reopen silently.
func TestRePushingTheSameMountSetRetiresNothing(t *testing.T) {
	const rel = "sounds/general/sfx-realization.opus"
	mount := mountWith(t, rel, "SFX")
	rig := newRig(t, network.NewClient(), false)

	first := NewLocalFetcher([]string{mount})
	rig.manager.SetLocalOverlay(first)
	for i := 0; i < localOriginHistoryCap*3; i++ {
		rig.manager.SetLocalOverlay(NewLocalFetcher([]string{mount})) // same set, new value
	}
	if retired := rig.manager.localRetired.Load(); retired != nil && len(*retired) != 0 {
		t.Errorf("re-pushing an identical mount set retired %d generations, want 0", len(*retired))
	}

	// A genuine change retires exactly one, and the original still answers.
	rig.manager.SetLocalOverlay(NewLocalFetcher([]string{mountWith(t, rel, "OTHER")}))
	if _, err := rig.manager.FetchRaw(context.Background(), first.BaseURL()+rel); err != nil {
		t.Errorf("the previous generation stopped answering after one real change: %v", err)
	}
}

// TestClearingTheOverlayKeepsThePreviousGenerationServable pins the
// mounts-removed path: turning local assets off pushes nil, and a scene already
// on stage must not blink out mid-frame just because the checkbox moved.
func TestClearingTheOverlayKeepsThePreviousGenerationServable(t *testing.T) {
	const rel = "characters/Edgeworth/(a)normal.webp"
	lf := NewLocalFetcher([]string{mountWith(t, rel, "ART")})
	rig := newRig(t, network.NewClient(), false)
	rig.manager.SetLocalOverlay(lf)
	rig.manager.SetLocalOverlay(nil)

	data, err := rig.manager.FetchRaw(context.Background(), lf.BaseURL()+rel)
	if err != nil {
		t.Fatalf("a base already on stage stopped resolving the moment mounts cleared: %v", err)
	}
	if string(data) != "ART" {
		t.Errorf("served %q, want ART", data)
	}
}
