package assets

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// newMixedFleetResolver is a resolver over throwaway prefs, seeded from a
// manifest that declares BOTH formats for the emote-button class — exactly what
// umineko.online publishes ("emotions_extensions": [".png", ".webp"]).
func newMixedFleetResolver(t *testing.T, host string) (*Resolver, *config.AssetPreferences) {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	m, err := ParseManifest([]byte(`{"emotions_extensions": [".png", ".webp"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if n := m.SeedLearned(prefs, host); n == 0 {
		t.Fatal("the manifest seeded nothing")
	}
	r := NewResolver(prefs)
	return r, prefs
}

// TestMixedFleetBothFormatsStayReachable is the regression test for the
// umineko.online "every emote button shows the same icon" report.
//
// That server declares its emote buttons as [".png", ".webp"] and genuinely
// ships both (~560 PNG-button characters, ~490 WebP-button). The learned table
// used to hold ONE extension per (host, type), so the first WebP character to
// load flipped the host to .webp for everyone — and a PNG-only character then
// probed .webp, 404'd, and had nothing left to try, because EmoteButton's
// configured list (config.defaultFormatOrders) is the single entry .webp. The
// grid fell back to the character icon in every cell.
//
// The invariant this pins: whichever format most recently succeeded, the OTHER
// one the server declared is still reachable on the recovery path. Order may
// churn; membership must not shrink.
func TestMixedFleetBothFormatsStayReachable(t *testing.T) {
	const host = "umineko.online"
	r, _ := newMixedFleetResolver(t, host)

	const base = "https://umineko.online/base/characters/don%20lc/emotions/button1_off"
	reachable := func() []string {
		// What the manager can probe in total: the learned-first candidate, then
		// the recovery list once that 404s (manager.tryBase).
		c := r.BuildCandidates(base, AssetTypeEmoteButton, host)
		got := append([]string(nil), c.URLs...)
		r.PutCandidates(c)
		full := r.BuildFullListCandidates(base, AssetTypeEmoteButton, host)
		for _, u := range full.URLs {
			if !containsExt(got, u) {
				got = append(got, u)
			}
		}
		r.PutCandidates(full)
		return got
	}
	hasExt := func(urls []string, ext string) bool {
		for _, u := range urls {
			if u == base+ext {
				return true
			}
		}
		return false
	}

	// Seeded from the manifest: .png is probed first, .webp is behind it.
	if got, want := r.BuildCandidates(base, AssetTypeEmoteButton, host), base+config.ExtPNG; true {
		if len(got.URLs) != 1 || got.URLs[0] != want {
			t.Errorf("seeded probe = %v, want exactly [%s] (one probe per asset)", got.URLs, want)
		}
		r.PutCandidates(got)
	}

	// A WebP-button character loads and succeeds on .webp. This is the event that
	// used to strand every PNG character on the server.
	r.RecordSuccess(host, AssetTypeEmoteButton, config.ExtWebP)

	if got, want := r.LearnedList(host, AssetTypeEmoteButton), config.ExtWebP; len(got) == 0 || got[0] != want {
		t.Errorf("after a .webp success the preferred format = %v, want %s first", got, want)
	}
	after := reachable()
	if !hasExt(after, config.ExtPNG) {
		t.Fatalf("REGRESSION: .png is unreachable after a .webp success — reachable = %v", after)
	}
	if !hasExt(after, config.ExtWebP) {
		t.Errorf(".webp unreachable after succeeding: %v", after)
	}

	// ...and back the other way: a PNG character now succeeds. The flip must be
	// symmetric — the old table only ratcheted png -> webp and never returned.
	r.RecordSuccess(host, AssetTypeEmoteButton, config.ExtPNG)
	back := reachable()
	if !hasExt(back, config.ExtWebP) {
		t.Errorf("REGRESSION: .webp unreachable after a .png success — reachable = %v", back)
	}
	if !hasExt(back, config.ExtPNG) {
		t.Errorf(".png unreachable after succeeding: %v", back)
	}
}

// TestLearnedListSurvivesRestart pins that a two-format host comes back with
// BOTH formats after a reload. WarmFromPrefs used to take exts[0] only, so a
// restart quietly re-truncated the list and re-armed the dead end.
func TestLearnedListSurvivesRestart(t *testing.T) {
	const host = "umineko.online"
	_, prefs := newMixedFleetResolver(t, host)
	prefs2 := prefs // same prefs object; WarmFromPrefs reads the persisted snapshot

	fresh := NewResolver(prefs2)
	got := fresh.LearnedList(host, AssetTypeEmoteButton)
	if len(got) != 2 || got[0] != config.ExtPNG || got[1] != config.ExtWebP {
		t.Errorf("after a warm-from-prefs the learned list = %v, want [%s %s]", got, config.ExtPNG, config.ExtWebP)
	}
}

// TestPromoteExtKeepsTheLoser pins promoteExt directly: it reorders, it never
// discards, it stays inside the cap, and it returns the SAME slice when nothing
// changes (the steady-state no-alloc, no-CAS path in RecordSuccess).
func TestPromoteExtKeepsTheLoser(t *testing.T) {
	prev := []string{".png", ".webp"}
	got := promoteExt(prev, ".webp")
	if len(got) != 2 || got[0] != ".webp" || got[1] != ".png" {
		t.Errorf("promoteExt(%v, .webp) = %v, want [.webp .png]", prev, got)
	}
	if prev[0] != ".png" || prev[1] != ".webp" {
		t.Errorf("promoteExt mutated the published slice: %v", prev)
	}
	if same := promoteExt(got, ".webp"); &same[0] != &got[0] {
		t.Error("promoting the existing front reallocated; the steady state must be free")
	}
	if got := promoteExt(nil, ".png"); len(got) != 1 || got[0] != ".png" {
		t.Errorf("promoteExt(nil, .png) = %v, want [.png]", got)
	}
	// Cap: an absurd manifest must not grow the probe list without bound.
	long := []string{".a", ".b", ".c", ".d", ".e", ".f"}
	if got := promoteExt(long, ".z"); len(got) != learnedExtCap {
		t.Errorf("promoteExt over-cap returned %d entries, want the %d cap", len(got), learnedExtCap)
	}
}
