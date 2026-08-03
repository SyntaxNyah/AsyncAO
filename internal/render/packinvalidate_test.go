package render

import (
	"strings"
	"testing"
)

// These gate the invalidation half of v1.89.0 local-pack layering. Resolution
// short-circuits on a resident texture, so without a sweep the current speaker
// and background keep showing the OLD source's art after the user switches asset
// source or presses Rescan — the first thing anyone tests.

// TestRemoveWhereSkipsReservedSchemes pins that a pack sweep touches only server
// asset URLs. The reserved namespaces are not something a mount can re-answer,
// and dropping the pinned missingno placeholder or the theme pages would blank
// chrome that has nothing to do with the pack.
func TestRemoveWhereSkipsReservedSchemes(t *testing.T) {
	s, done := packStore(t)
	defer done()
	reserved := []string{
		"theme://chatbox", "held://background", "thumb://witch",
		"shape://mask", "missingno://placeholder",
	}
	for _, k := range reserved {
		putStubPage(t, s, k)
	}
	putStubPage(t, s, "http://h/base/characters/witch/(a)normal")

	// A predicate that would happily eat everything.
	n := s.RemoveWhere(func(string) bool { return true })

	if n != 1 {
		t.Errorf("RemoveWhere dropped %d pages, want 1 (only the asset URL)", n)
	}
	for _, k := range reserved {
		if !s.Contains(k) {
			t.Errorf("reserved key %q was swept", k)
		}
	}
	if s.Contains("http://h/base/characters/witch/(a)normal") {
		t.Error("the asset page survived a sweep that matched it")
	}
}

// TestRemoveWhereSnapshotsBeforeRemoving pins the ordering contract: removing
// while iterating re-enters the cache through the eviction callback, which the
// onEvict contract forbids. A large matching set is the case that would surface
// it.
func TestRemoveWhereSnapshotsBeforeRemoving(t *testing.T) {
	s, done := packStore(t)
	defer done()
	const n = 64
	for i := 0; i < n; i++ {
		putStubPage(t, s, "http://h/base/characters/c"+string(rune('a'+i%26))+string(rune('a'+i/26))+"/(a)normal")
	}
	got := s.RemoveWhere(func(k string) bool { return strings.HasPrefix(k, "http://h/base/") })
	if got != n {
		t.Errorf("swept %d of %d pages", got, n)
	}
}

// TestForgetMissingHealsOnNextDemand pins that a base which 404'd on the server
// becomes re-demandable — that base is exactly what a user adds a pack to supply.
func TestForgetMissingHealsOnNextDemand(t *testing.T) {
	s, done := packStore(t)
	defer done()
	const base = "http://h/base/characters/witch/(a)normal"
	s.MarkMissing(base)
	if !s.IsMissing(base) {
		t.Fatal("fixture: MarkMissing did not take")
	}
	s.ForgetMissing()
	if s.IsMissing(base) {
		t.Error("ForgetMissing left the base marked — a newly mounted pack could not heal it")
	}
}

// TestForgetFailedUngatesAfterRescan pins the decode negative cache reset. The
// prefetch gate consults the failed set BEFORE the mount layer is reached, so a
// base whose SERVER copy is corrupt — the single most plausible reason to deploy
// a pack — would stay suppressed for the full decodeFailTTL after the user adds
// the fixing file and presses Rescan.
func TestForgetFailedUngatesAfterRescan(t *testing.T) {
	s, done := packStore(t)
	defer done()
	const base = "http://h/base/characters/witch/(a)normal"
	s.MarkFailed(base)
	if !s.FailedRecently(base) {
		t.Fatal("fixture: MarkFailed did not take")
	}
	s.ForgetFailed()
	if s.FailedRecently(base) {
		t.Error("ForgetFailed left the base gated — Rescan would appear to do nothing")
	}
}

// TestRemoveWhereNilPredicateIsSafe — the mode-flip path passes a predicate built
// from a layer that may be nil.
func TestRemoveWhereNilPredicateIsSafe(t *testing.T) {
	s, done := packStore(t)
	defer done()
	putStubPage(t, s, "http://h/base/x")
	if n := s.RemoveWhere(nil); n != 0 {
		t.Errorf("a nil predicate swept %d pages", n)
	}
	if !s.Contains("http://h/base/x") {
		t.Error("a nil predicate dropped a page")
	}
}

// --- helpers -------------------------------------------------------------------

// packStore builds a headless TextureStore, skipping when SDL is unavailable
// (the package convention).
func packStore(t *testing.T) (*TextureStore, func()) {
	t.Helper()
	ren, cleanup := newHeadlessRenderer(t)
	store, err := NewTextureStore(ren)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	return store, func() {
		store.Purge()
		cleanup()
	}
}

// putStubPage uploads a tiny page under key so the sweep has something resident
// to find.
func putStubPage(t *testing.T, s *TextureStore, key string) {
	t.Helper()
	if err := s.Upload(key, decodedFixture()); err != nil {
		t.Fatalf("upload %q: %v", key, err)
	}
}
