package assets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// The field defect these pin (2026-08-09 debug panel, credit Crystalwarrior for
// the find): one absent desk overlay —
// local://m-…/background/botc-townsquare/evening_overlay — was re-probed nine
// times in twenty-three seconds. Hard rule 6 says a cached 404 is never
// re-probed inside its TTL, but the negative cache lived only inside
// network.Client and Manager.netFetch routes local:// URLs straight to
// LocalFetcher, so the no-streaming path had no negative cache AT ALL. Every
// re-demand site — and the courtroom re-Prefetches Scene.DeskBase on EVERY IC
// message — re-walked formats × spellings × mounts of os.ReadFile.

// TestLocalMissIsProbedOncePerTTL is the pin: after a conclusive miss the mount
// is not consulted again inside the window, and IS consulted again once the
// window closes.
//
// The discriminator is stronger than a probe counter: the file MATERIALISES
// between the two fetches. A re-probe would necessarily find it, so a second
// miss can only mean the disk was never touched — and the third fetch, past the
// TTL, proves the memo is a window and not a permanent latch (a user who drops
// the missing file into a mount must not need a restart).
func TestLocalMissIsProbedOncePerTTL(t *testing.T) {
	const ttl = 60 * time.Millisecond
	const rel = "background/botc-townsquare/evening_overlay.png"
	mount := t.TempDir()
	lf := newLocalFetcher([]string{mount}, ttl)
	url := lf.BaseURL() + rel
	ctx := context.Background()

	if _, err := lf.Fetch(ctx, url); !errors.Is(err, network.ErrAssetNotFound) {
		t.Fatalf("first probe of an absent asset: want ErrAssetNotFound, got %v", err)
	}
	seedOverlayMount(t, mount, rel, []byte("PNG"))
	if _, err := lf.Fetch(ctx, url); !errors.Is(err, network.ErrAssetNotFound) {
		t.Fatalf("a cached miss must not re-probe inside its TTL (rule 6); got %v", err)
	}

	time.Sleep(4 * ttl)
	data, err := lf.Fetch(ctx, url)
	if err != nil {
		t.Fatalf("past the TTL the mount is authoritative again: %v", err)
	}
	if string(data) != "PNG" {
		t.Fatalf("post-TTL fetch served %q, want PNG", data)
	}
}

// TestLocalMissMemoNeverShadowsAHit guards the obvious way this could go wrong:
// the memo must only ever answer for URLs that genuinely missed. A present file
// resolves on every fetch, and a miss recorded for one URL says nothing about
// its siblings (the memo is keyed by the FULL URL, the client-wide cache-key
// convention).
func TestLocalMissMemoNeverShadowsAHit(t *testing.T) {
	mount := t.TempDir()
	seedOverlayMount(t, mount, "characters/phoenix/(a)normal.png", []byte("HIT"))
	lf := newLocalFetcher([]string{mount}, time.Minute)
	ctx := context.Background()

	if _, err := lf.Fetch(ctx, lf.BaseURL()+"characters/phoenix/(b)normal.png"); !errors.Is(err, network.ErrAssetNotFound) {
		t.Fatalf("absent sibling: want ErrAssetNotFound, got %v", err)
	}
	for i := 0; i < 3; i++ {
		data, err := lf.Fetch(ctx, lf.BaseURL()+"characters/phoenix/(a)normal.png")
		if err != nil || string(data) != "HIT" {
			t.Fatalf("fetch %d of a present file: %q, %v", i, data, err)
		}
	}
}

// TestLocalMissMemoIsScopedToItsMountGeneration pins why the TTL can be short
// without stranding anyone: NewLocalFetcher hashes the mount list into the
// origin, so attaching a folder mints a new origin, a disjoint keyspace and an
// empty memo. Adding the mount that HAS the file resolves immediately — no
// restart, no waiting the window out.
func TestLocalMissMemoIsScopedToItsMountGeneration(t *testing.T) {
	const rel = "characters/phoenix/(a)normal.png"
	ctx := context.Background()
	mountA := t.TempDir()
	genA := newLocalFetcher([]string{mountA}, time.Minute)
	if _, err := genA.Fetch(ctx, genA.BaseURL()+rel); !errors.Is(err, network.ErrAssetNotFound) {
		t.Fatalf("generation A: want ErrAssetNotFound, got %v", err)
	}

	mountB := t.TempDir()
	seedOverlayMount(t, mountB, rel, []byte("PNG"))
	genB := newLocalFetcher([]string{mountA, mountB}, time.Minute)
	if genA.BaseURL() == genB.BaseURL() {
		t.Fatal("a changed mount set must mint a new origin")
	}
	data, err := genB.Fetch(ctx, genB.BaseURL()+rel)
	if err != nil || string(data) != "PNG" {
		t.Fatalf("the new mount generation must serve the file at once: %q, %v", data, err)
	}
}

// TestMissingDeskOverlayWalksTheMountOnce drives the WHOLE pipeline the field
// report exercised — Manager.Prefetch → resolveChain → walkCandidates →
// netFetch → LocalFetcher — and pins that the second per-message re-demand of a
// missing desk overlay reaches the mount zero times.
//
// Same discriminator, applied end to end: every candidate format is written to
// the mount between the two prefetches. Without the memo the second pass would
// deliver a decoded asset; with it, the pass is still a conclusive miss.
func TestMissingDeskOverlayWalksTheMountOnce(t *testing.T) {
	mount := t.TempDir()
	source := NewLocalFetcher([]string{mount})
	rig := newRig(t, source, true) // localMode: the no-streaming path
	base := source.BaseURL() + "background/botc-townsquare/evening_overlay"

	rig.manager.Prefetch(base, AssetTypeDeskOverlay, network.PriorityHigh) // AssetType: DeskOverlay
	if w := waitWarning(t, rig.manager); w.Base != base {
		t.Fatalf("first pass warned about %q, want %q", w.Base, base)
	}

	// The mount grows every spelling the walk could accept.
	for _, ext := range []string{".png", ".webp", ".apng", ".gif", ".avif", ".jpg", ".bmp"} {
		seedOverlayMount(t, mount, "background/botc-townsquare/evening_overlay"+ext, []byte("PNG"))
	}
	rig.manager.Prefetch(base, AssetTypeDeskOverlay, network.PriorityHigh) // AssetType: DeskOverlay
	if w := waitWarning(t, rig.manager); w.Base != base {
		t.Fatalf("the re-demand must still be a conclusive miss (no re-probe), got a pass for %q", w.Base)
	}
}
