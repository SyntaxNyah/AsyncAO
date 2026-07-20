package assets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// seedOverlayMount writes a file at rel (forward-slash) under mount, so a
// LocalFetcher over that mount reads it back for origin+rel.
func seedOverlayMount(t *testing.T, mount, rel string, data []byte) {
	t.Helper()
	full := filepath.Join(mount, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStreamingManagerLocalOverlayServes is the load-bearing proof for Part 1:
// a STREAMING manager (network client source, localMode=false) with a mount
// overlay installed resolves a local:// URL END TO END from the seeded mount —
// exactly the "Local base" source working while connected. Without the overlay
// the network client would transport-error on a local:// URL.
func TestStreamingManagerLocalOverlayServes(t *testing.T) {
	mount := t.TempDir()
	seedOverlayMount(t, mount, "characters/phoenix/(a)normal.png", []byte("PNGDATA"))
	overlay := NewLocalFetcher([]string{mount})

	// Streaming rig: the source is a real network client, NOT a LocalFetcher.
	rig := newRig(t, network.NewClient(), false)
	rig.manager.SetLocalOverlay(overlay)

	url := overlay.BaseURL() + "characters/phoenix/(a)normal.png"
	data, err := rig.manager.FetchRaw(context.Background(), url)
	if err != nil {
		t.Fatalf("streaming manager + overlay must serve a local:// URL: %v", err)
	}
	if string(data) != "PNGDATA" {
		t.Fatalf("overlay served %q, want PNGDATA", data)
	}
}

// TestStreamingManagerLocalOverlay404Conclusive pins that a MISSING mount file
// (over a real, non-nil overlay) reports a conclusive ErrAssetNotFound — the
// same status a streaming 404 produces — so learned formats / warnings behave
// identically. This is distinct from the nil-overlay "cannot serve" error.
func TestStreamingManagerLocalOverlay404Conclusive(t *testing.T) {
	mount := t.TempDir() // empty: every relative path is absent
	overlay := NewLocalFetcher([]string{mount})
	rig := newRig(t, network.NewClient(), false)
	rig.manager.SetLocalOverlay(overlay)

	url := overlay.BaseURL() + "characters/nobody/(a)normal.png"
	_, err := rig.manager.FetchRaw(context.Background(), url)
	if !errors.Is(err, network.ErrAssetNotFound) {
		t.Fatalf("a missing mount file must be a conclusive ErrAssetNotFound, got %v", err)
	}
}

// TestStreamingManagerNilOverlayCannotServe pins that a local:// URL requested
// of a streaming manager with NO overlay installed returns
// ErrLocalOverlayUnavailable — NOT ErrAssetNotFound. This is the crux of the
// false-missing fix: a nil overlay means "cannot query," which the report must
// treat as unreachable, never as "the asset is absent" (which would make every
// asset falsely [missing]).
func TestStreamingManagerNilOverlayCannotServe(t *testing.T) {
	rig := newRig(t, network.NewClient(), false) // no SetLocalOverlay

	url := LocalScheme + "m-deadbeef/characters/phoenix/(a)normal.png"
	_, err := rig.manager.FetchRaw(context.Background(), url)
	if !errors.Is(err, ErrLocalOverlayUnavailable) {
		t.Fatalf("nil overlay must report ErrLocalOverlayUnavailable, got %v", err)
	}
	if errors.Is(err, network.ErrAssetNotFound) {
		t.Fatal("nil overlay must NOT masquerade as a 404 (would false-[missing] every asset)")
	}
}

// TestStreamingManagerLocalOverlaySkipsDisk pins the T3 discipline: a local://
// URL fetched through a streaming manager is NEVER written to the disk tier (the
// mounts ARE disk). Close() drains the async writer, then a disk Get for the
// local:// URL must miss — proving no Put was queued. A sanity control seeds an
// http URL through the SAME manager and confirms THAT one DID land on disk, so
// the test proves selective skipping, not a broken disk tier.
func TestStreamingManagerLocalOverlaySkipsDisk(t *testing.T) {
	mount := t.TempDir()
	seedOverlayMount(t, mount, "characters/phoenix/(a)normal.png", []byte("LOCALBYTES"))
	overlay := NewLocalFetcher([]string{mount})

	// Control: an http origin that serves one asset, to prove disk Put works.
	cs := newCountingServer(t, map[string][]byte{
		"/characters/miles/(a)normal.png": []byte("NETBYTES"),
	})
	rig := newRig(t, network.NewClient(), false)
	rig.manager.SetLocalOverlay(overlay)

	localURL := overlay.BaseURL() + "characters/phoenix/(a)normal.png"
	netURL := cs.srv.URL + "/characters/miles/(a)normal.png"

	if _, err := rig.manager.FetchRaw(context.Background(), localURL); err != nil {
		t.Fatalf("local fetch: %v", err)
	}
	if _, err := rig.manager.FetchRaw(context.Background(), netURL); err != nil {
		t.Fatalf("net fetch: %v", err)
	}

	// Close drains the disk writer synchronously (closeOnce makes the rig's own
	// deferred Close a no-op afterward).
	rig.disk.Close()

	if _, ok := rig.disk.Get(localURL); ok {
		t.Error("a local:// URL must NOT be written to the disk tier (the mounts ARE disk)")
	}
	if _, ok := rig.disk.Get(netURL); !ok {
		t.Error("control: an http URL SHOULD land on the disk tier — the test's disk path is broken otherwise")
	}
}

// TestStreamingManagerOverlayOfflineStillServes pins that rehearsal/offline mode
// does NOT block local:// fetches: local mount reads are disk, not network
// egress, so they are legal offline. The offline gate must sit below the overlay
// route in netFetch.
func TestStreamingManagerOverlayOfflineStillServes(t *testing.T) {
	mount := t.TempDir()
	seedOverlayMount(t, mount, "background/court/defenseempty.png", []byte("BGBYTES"))
	overlay := NewLocalFetcher([]string{mount})
	rig := newRig(t, network.NewClient(), false)
	rig.manager.SetLocalOverlay(overlay)
	rig.manager.SetOffline(true) // rehearsal: no network egress

	url := overlay.BaseURL() + "background/court/defenseempty.png"
	data, err := rig.manager.FetchRaw(context.Background(), url)
	if err != nil {
		t.Fatalf("offline mode must still serve local:// (disk, not network): %v", err)
	}
	if string(data) != "BGBYTES" {
		t.Fatalf("offline overlay served %q, want BGBYTES", data)
	}
}

// TestLocalOverlaySwapDuringFetchesRaceClean stresses SetLocalOverlay swapping
// concurrently with in-flight local:// fetches — the render thread swaps the
// overlay on a mounts change while pool workers read it. Run under -race; the
// atomic.Pointer must make every read consistent (no torn pointer, no data
// race). Correctness of any individual fetch is not asserted (the overlay may be
// swapped mid-flight); the point is race-cleanliness of the swap.
func TestLocalOverlaySwapDuringFetchesRaceClean(t *testing.T) {
	mountA := t.TempDir()
	mountB := t.TempDir()
	seedOverlayMount(t, mountA, "characters/a/(a)normal.png", []byte("A"))
	seedOverlayMount(t, mountB, "characters/b/(a)normal.png", []byte("B"))
	ovA := NewLocalFetcher([]string{mountA})
	ovB := NewLocalFetcher([]string{mountB})

	rig := newRig(t, network.NewClient(), false)
	rig.manager.SetLocalOverlay(ovA)

	stop := make(chan struct{})

	// Swapper: flip the overlay (and nil) as fast as it can until stopped.
	var swapWG sync.WaitGroup
	swapWG.Add(1)
	go func() {
		defer swapWG.Done()
		overlays := []*LocalFetcher{ovA, ovB, nil}
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				rig.manager.SetLocalOverlay(overlays[i%len(overlays)])
				i++
			}
		}
	}()

	// Fetchers: hammer local:// URLs against whatever overlay is current for a
	// fixed iteration budget (bounded work → the -race detector sees many
	// concurrent Load/Store pairs on the atomic pointer). Results are not asserted
	// (the overlay may be swapped or nil mid-flight); the point is race-cleanliness.
	urlA := ovA.BaseURL() + "characters/a/(a)normal.png"
	urlB := ovB.BaseURL() + "characters/b/(a)normal.png"
	var fetchWG sync.WaitGroup
	for w := 0; w < 4; w++ {
		fetchWG.Add(1)
		go func() {
			defer fetchWG.Done()
			for j := 0; j < 300; j++ {
				_, _ = rig.manager.FetchRaw(context.Background(), urlA)
				_, _ = rig.manager.FetchRaw(context.Background(), urlB)
			}
		}()
	}

	fetchWG.Wait() // all fetchers finished their budget
	close(stop)    // stop the swapper
	swapWG.Wait()
}

// TestLocalModeManagerIgnoresOverlay pins the no-op guarantee for a LOCAL-mode
// Manager: its source already IS the LocalFetcher, so SetLocalOverlay must not
// change behavior. We install an overlay pointing at a DIFFERENT mount that does
// NOT contain the asset; the local-mode manager must still resolve from its own
// source (never the overlay). netFetch's overlay branch is gated !localMode.
func TestLocalModeManagerIgnoresOverlay(t *testing.T) {
	realMount := t.TempDir()
	seedOverlayMount(t, realMount, "characters/phoenix/(a)normal.png", []byte("REAL"))
	source := NewLocalFetcher([]string{realMount})
	rig := newRig(t, source, true) // LOCAL mode: source IS the LocalFetcher

	// A DECOY overlay over an empty mount. If the local-mode manager wrongly read
	// the overlay, it would 404 the asset the real source has.
	decoy := NewLocalFetcher([]string{t.TempDir()})
	rig.manager.SetLocalOverlay(decoy)

	url := source.BaseURL() + "characters/phoenix/(a)normal.png"
	data, err := rig.manager.FetchRaw(context.Background(), url)
	if err != nil {
		t.Fatalf("local-mode manager must resolve from its own source, ignoring the overlay: %v", err)
	}
	if string(data) != "REAL" {
		t.Fatalf("local-mode manager served %q, want REAL (the overlay must be a no-op)", data)
	}
}
