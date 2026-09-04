package ui

// The "the emote preview must follow each click" gates (screens.go's
// followPinnedPreview, wired into selectEmote and drawEmoteGridThemed's inline
// duplicate of it).
//
// Three behavior tests pin the contract from the App's point of view:
// pinned+open follows, unpinned+open is left for the draw-site close contract,
// and closed never spontaneously opens. A fourth, source-level gate closes the
// hole that caused the bug in the first place — the themed grid keeps its own
// copy of "an emote was picked" instead of calling selectEmote — by demanding
// every such copy, present or future, also calls followPinnedPreview. It reads
// production source and contains none of followPinnedPreview's own logic, so
// it cannot go green against a re-implementation (CLAUDE.md rule 11).

import (
	"go/ast"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// emotePreviewFollowTestApp is a headless App wired with a real (local-mount)
// Manager and URLBuilder — the two dependencies selectEmote's prefetch calls
// need — plus a small fixed emote list. No SDL: the seam under test is pure
// App state, not a draw.
func emotePreviewFollowTestApp(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	mount := t.TempDir()
	resolver := assets.NewResolver(a.d.Prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver:  resolver,
		Prefs:     a.d.Prefs,
		T2:        t2,
		Disk:      disk,
		Source:    assets.NewLocalFetcher([]string{mount}),
		LocalMode: true,
		Pool:      pool,
		Decoder:   decoder,
	})
	a.urls = courtroom.NewURLBuilder(assets.LocalOriginFor([]string{mount}))
	a.iniChar = "TestChar" // activeCharName() returns this with no session/iniswap
	a.emotes = []courtroom.Emote{
		{Anim: "happy", Comment: "happy"},
		{Anim: "sad", Comment: "sad"},
		{Anim: "angry", Comment: "angry"},
	}
	return a
}

// TestSelectEmoteUpdatesPinnedPreview is the user's report, pinned: with the
// box already open and PINNED (a right-click latch or the sticky pref) on
// emote 0, clicking emote 1 must retarget it — pinned means "keep showing
// whatever's picked", not "freeze on whatever opened it".
func TestSelectEmoteUpdatesPinnedPreview(t *testing.T) {
	a := emotePreviewFollowTestApp(t)
	a.previewEmote("TestChar", &a.emotes[0]) // as if emote 0's hover/right-click opened the box
	a.previewPinned = true                   // …and the right-click pinned it
	openedOn := a.previewBase
	if openedOn == "" {
		t.Fatal("fixture did not open a preview — nothing below is measuring the follow")
	}

	a.selectEmote(1)

	if a.previewBase == openedOn {
		t.Fatal("selectEmote(1) left the pinned preview on emote 0 — it must follow the click")
	}
	want := a.urls.Emote("TestChar", "sad", courtroom.EmoteTalk)
	if a.previewBase != want {
		t.Fatalf("previewBase = %q, want %q (emote 1's talk sprite)", a.previewBase, want)
	}
	if got := a.previewedEmoteName(); got != "sad" {
		t.Fatalf("previewedEmoteName() = %q, want %q — the caption must move with the box", got, "sad")
	}
}

// TestSelectEmoteLeavesUnpinnedPreviewAlone: an UNPINNED open box (a bare hover
// preview, not right-clicked) is not selectEmote's to touch — its close-on-click
// contract lives at the draw site (closeSpritePreviewOnLeave / dismissPreviewOnClick),
// which TestSpritePreviewTravelCorridor already pins as "a click always dismisses".
// If selectEmote also repointed an unpinned box, that box would show the NEW
// emote for one frame before the draw-site close ran — a visible flicker this
// test would not exist to catch if followPinnedPreview's pin guard were removed.
func TestSelectEmoteLeavesUnpinnedPreviewAlone(t *testing.T) {
	a := emotePreviewFollowTestApp(t)
	a.previewEmote("TestChar", &a.emotes[0]) // hover only — no right-click, no sticky pref
	openedOn := a.previewBase
	if openedOn == "" || a.previewIsPinned() {
		t.Fatal("fixture must open an UNPINNED preview")
	}

	a.selectEmote(1)

	if a.previewBase != openedOn {
		t.Fatalf("previewBase changed to %q — selectEmote must leave an unpinned preview alone "+
			"(closing it is the draw site's job)", a.previewBase)
	}
}

// TestSelectEmoteNeverOpensAClosedPreview is the other half of the requirement:
// a pick must never pop the box open for someone who never used the feature,
// even when the STICKY PREFERENCE alone would count as "pinned" the moment a
// preview did open. previewIsPinned() ORs the latch with the pref, so this
// pins the pref-only branch specifically.
func TestSelectEmoteNeverOpensAClosedPreview(t *testing.T) {
	a := emotePreviewFollowTestApp(t)
	a.d.Prefs.SetPreviewPinned(true) // sticky "always pinned" — nothing hovered yet
	if a.previewBase != "" {
		t.Fatal("fixture must start with the box closed")
	}

	a.selectEmote(0)

	if a.previewBase != "" {
		t.Fatalf("previewBase = %q after a pick with no preview open — a selection must never "+
			"spontaneously open the box", a.previewBase)
	}
}

// TestFollowPinnedPreviewIsAPureGuard drives the extracted helper directly at
// its two edge cases, isolated from selectEmote's other side effects (the
// EmoteButton prefetches, the focus bounce) — the unit the themed grid's
// inline branch also calls.
func TestFollowPinnedPreviewIsAPureGuard(t *testing.T) {
	a := emotePreviewFollowTestApp(t)

	// Closed: must not open.
	a.followPinnedPreview("TestChar", &a.emotes[0])
	if a.previewBase != "" {
		t.Fatal("followPinnedPreview opened a closed box")
	}

	// Open + unpinned: must not move.
	a.previewEmote("TestChar", &a.emotes[0])
	before := a.previewBase
	a.followPinnedPreview("TestChar", &a.emotes[1])
	if a.previewBase != before {
		t.Fatal("followPinnedPreview moved an unpinned box")
	}

	// Open + pinned: must follow.
	a.previewPinned = true
	a.followPinnedPreview("TestChar", &a.emotes[1])
	want := a.urls.Emote("TestChar", "sad", courtroom.EmoteTalk)
	if a.previewBase != want {
		t.Fatalf("followPinnedPreview left previewBase = %q, want %q", a.previewBase, want)
	}
}

// TestEveryEmotePickImplementationFollowsThePinnedPreview is the source-level
// gate for the actual root cause: nothing in this package routes "an emote was
// picked" through one shared function. selectEmote is the closest thing to a
// shared entry point, but the themed emote grid keeps its own inline copy
// (documented at chatfocus.go:44-45 as a second, deliberate implementation),
// and applyStylePreset (mood presets / hotkeys) turned out to be a THIRD —
// found by this very gate while writing it, not by inspection. That
// duplication is why the classic grid could get the follow-hook while the
// other two silently kept freezing, and why a FOURTH copy, pasted anywhere
// else, could do the same again.
//
// Rather than naming the known sites, this walks every function in the
// package (packageFuncs — the same "no roster" census newroom_test.go uses)
// and demands that ANY function whose body assigns `<x>.icPreanim =
// emoteHasPreanim(...)` — the precise fingerprint of "an emote selection was
// just made", shared by all three sites today and by no unrelated one (sendIC
// also calls emoteHasPreanim, but into a local `hasPre`, to derive the SEND's
// wire field from whatever's already selected — not a pick, so it's correctly
// excluded) — also calls followPinnedPreview. A future copy that forgets the
// hook fails this test the moment it is written; one that remembers it passes
// without this file changing.
//
// The >=3 floor guards against the census going vacuous (e.g. every site
// renamed away from the icPreanim/emoteHasPreanim fingerprint) rather than
// pinning an exact roster count, so a legitimately added FOURTH site that gets
// the hook right does not have to edit this test to stay green.
func TestEveryEmotePickImplementationFollowsThePinnedPreview(t *testing.T) {
	found := 0
	packageFuncs(t, func(file, fn string, body *ast.BlockStmt) {
		if !assignsFieldFrom(body, "icPreanim", "emoteHasPreanim") {
			return
		}
		found++
		if !containsCall(body, "followPinnedPreview") {
			t.Errorf("%s: %s picks an emote (assigns icPreanim from emoteHasPreanim) but never "+
				"calls followPinnedPreview — a preview pinned open will freeze on whatever emote "+
				"opened it instead of following this pick", file, fn)
		}
	})
	if found < 3 {
		t.Fatalf("found %d emote-pick implementation(s) (want >= 3: selectEmote, drawEmoteGridThemed, "+
			"applyStylePreset) — the icPreanim/emoteHasPreanim fingerprint moved and this census is "+
			"now checking nothing", found)
	}
}
