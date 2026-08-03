package assets

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// packRig wires a streaming Manager against a real httptest origin plus a local
// pack, which is the only configuration these behaviours are meaningful in.
type packRig struct {
	rig    *testRig
	srv    *httptest.Server
	origin string
	dir    string
	layer  *MountLayer
	hits   map[string]int
}

// serveAndCount answers the given rels and records every request path, so a test
// can assert the pack cost the network NOTHING.
func newPackRig(t *testing.T, server map[string]string, pack map[string]string) *packRig {
	t.Helper()
	pr := &packRig{hits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		pr.hits[r.URL.Path]++
		body, ok := server[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	pr.srv = httptest.NewServer(mux)
	t.Cleanup(pr.srv.Close)
	pr.origin = pr.srv.URL + "/base/"

	pr.dir = t.TempDir()
	writePack(t, pr.dir, pack)
	ix, errs := BuildMountIndex([]string{pr.dir})
	if len(errs) != 0 {
		t.Fatalf("index build: %v", errs)
	}
	t.Cleanup(ix.Retire)
	pr.layer = NewMountLayer(ix, []string{pr.dir}, []string{pr.origin})

	pr.rig = newRig(t, network.NewClient(), false)
	pr.rig.manager.SetMountLayer(pr.layer)
	return pr
}

func (pr *packRig) base(rel string) string { return pr.origin + rel }

// awaitDecoded waits for one delivery, failing the test rather than hanging.
func (pr *packRig) awaitDecoded(t *testing.T) DecodedAsset {
	t.Helper()
	select {
	case d := <-pr.rig.manager.Decoded():
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no decoded asset within 5s")
		return DecodedAsset{}
	}
}

// TestPackOverridesServerAcrossFormats is the headline behaviour AND the
// regression guard for the learned table. A .png pack must beat a .webp server
// for the same emote, and the server host's learned formats must come out
// byte-identical — a pack teaching the server's probe order is the v1.61.0 /
// v1.87.2 bug class.
func TestPackOverridesServerAcrossFormats(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/(a)normal.webp": "SERVER"},
		map[string]string{"characters/witch/(a)normal.png": "PACK"},
	)
	host := HostOf(pr.origin)
	before := pr.rig.manager.resolver.LearnedList(host, AssetTypeCharSprite)

	pr.rig.manager.resolveChain(pr.base("characters/witch/(a)normal"), nil, AssetTypeCharSprite, false)

	if got := pr.rig.manager.Stats().MountFetches; got != 1 {
		t.Errorf("MountFetches = %d, want 1 — the pack did not win", got)
	}
	if n := pr.hits["/base/characters/witch/(a)normal.webp"]; n != 0 {
		t.Errorf("the server was probed %d time(s) for an asset the pack holds", n)
	}
	after := pr.rig.manager.resolver.LearnedList(host, AssetTypeCharSprite)
	if len(after) != len(before) {
		t.Errorf("the pack taught the SERVER host a format: %v -> %v", before, after)
	}
}

// TestPackMissFallsThroughToServer pins the partial-pack case — a pack covering
// a handful of characters must leave every other asset streaming normally.
func TestPackMissFallsThroughToServer(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/other/(a)normal.webp": "SERVER"},
		map[string]string{"characters/witch/(a)normal.png": "PACK"},
	)
	pr.rig.manager.resolveChain(pr.base("characters/other/(a)normal"), nil, AssetTypeCharSprite, false)

	st := pr.rig.manager.Stats()
	if st.MountFetches != 0 {
		t.Errorf("MountFetches = %d for an asset the pack does not hold", st.MountFetches)
	}
	if st.NetFetches != 1 {
		t.Errorf("NetFetches = %d, want 1 — the fall-through did not reach the server", st.NetFetches)
	}
}

// TestPackNeverWritesServerKeyedCaches pins invariant 3. The pack's bytes may be
// cached under their OWN local:// transport key (a disjoint keyspace, and it is
// what keeps an evicted background from being re-read from disk), but nothing may
// land under the SERVER's URL, where it would outlive the pack and be clearable
// only by wiping the whole disk cache.
func TestPackNeverWritesServerKeyedCaches(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/(a)normal.webp": "SERVER"},
		map[string]string{"characters/witch/(a)normal.png": "PACK"},
	)
	base := pr.base("characters/witch/(a)normal")
	pr.rig.manager.resolveChain(base, nil, AssetTypeCharSprite, false)

	for _, ext := range []string{".png", ".webp"} {
		if _, ok := pr.rig.manager.t2.Get(base + ext); ok {
			t.Errorf("pack bytes landed in T2 under the SERVER url %s", base+ext)
		}
	}
	// ...but they ARE cached under the pack's own key, or every T1 eviction would
	// re-read and re-allocate the file from disk.
	packURL := pr.layer.LocalURLFor("characters/witch/(a)normal.png")
	if _, ok := pr.rig.manager.t2.Get(packURL); !ok {
		t.Error("pack bytes were not cached under their own transport key")
	}
}

// TestPackReadErrorFallsThroughToServer pins invariant 4a. serveFromMount has no
// error return, so an unreadable pack entry cannot reach walkCandidates'
// pass-aborting default arm — it degrades to the server instead of killing the
// whole resolution.
func TestPackReadErrorFallsThroughToServer(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/(a)normal.webp": "SERVER"},
		map[string]string{"characters/witch/(a)normal.png": "PACK"},
	)
	// Delete the file AFTER indexing: the index still lists it, the read fails.
	if err := os.Remove(filepath.Join(pr.dir, "characters", "witch", "(a)normal.png")); err != nil {
		t.Fatal(err)
	}
	pr.rig.manager.resolveChain(pr.base("characters/witch/(a)normal"), nil, AssetTypeCharSprite, false)

	st := pr.rig.manager.Stats()
	if st.NetFetches != 1 {
		t.Errorf("NetFetches = %d, want 1 — a dead pack entry aborted the pass", st.NetFetches)
	}
	// A read error must NOT quarantine: it is environmental, often transient, and
	// stickily disabling a pack that recovers is worse than re-reading it.
	if st.PackQuarantined != 0 {
		t.Errorf("a read error quarantined %d entries; only decode failures may", st.PackQuarantined)
	}
}

// TestQuarantinedPackEntryIsSkippedOnTheNextPass pins that the quarantine
// actually takes effect — the failure mode where the key spaces disagree makes
// this silently green, so it asserts the SERVER is reached afterwards.
func TestQuarantinedPackEntryIsSkippedOnTheNextPass(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/(a)normal.webp": "SERVER"},
		map[string]string{"characters/witch/(a)normal.png": "PACK"},
	)
	base := pr.base("characters/witch/(a)normal")

	pr.rig.manager.resolveChain(base, nil, AssetTypeCharSprite, false)
	d := pr.awaitDecoded(t)
	if !d.FromPack {
		t.Fatal("the first pass did not come from the pack")
	}
	if !pr.rig.manager.QuarantinePack(d.URL) {
		t.Fatal("QuarantinePack refused our own pack URL — the key spaces disagree")
	}

	pr.rig.manager.resolveChain(base, nil, AssetTypeCharSprite, false)
	if got := pr.rig.manager.Stats().NetFetches; got != 1 {
		t.Errorf("NetFetches = %d, want 1 — the quarantined entry kept winning", got)
	}
}

// TestQuarantinePackRejectsForeignURL pins that a URL which is not this layer's
// records nothing. The caller relies on the false return to avoid
// negative-caching the SERVER's base for bytes that were never the server's.
func TestQuarantinePackRejectsForeignURL(t *testing.T) {
	pr := newPackRig(t, nil, map[string]string{"characters/witch/(a)normal.png": "PACK"})
	for _, u := range []string{
		pr.base("characters/witch/(a)normal.webp"),                 // the server's own URL
		NewLocalFetcher([]string{t.TempDir()}).BaseURL() + "x.png", // another mount set
	} {
		if pr.rig.manager.QuarantinePack(u) {
			t.Errorf("QuarantinePack claimed a foreign URL: %s", u)
		}
	}
}

// TestPackIsInertDuringArchiveReplay pins replay hermeticity: a bundled scene
// must play back exactly as recorded, so the user's live pack may not fill its
// holes.
func TestPackIsInertDuringArchiveReplay(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/(a)normal.webp": "SERVER"},
		map[string]string{"characters/witch/(a)normal.png": "PACK"},
	)
	var src Fetcher = NewLocalFetcher([]string{t.TempDir()})
	pr.rig.manager.SetArchiveSource(src)
	defer pr.rig.manager.ClearArchiveSource()

	if l := pr.rig.manager.activeMountLayer(); l != nil {
		t.Error("the mount layer is live during an archive replay")
	}
	pr.rig.manager.resolveChain(pr.base("characters/witch/(a)normal"), nil, AssetTypeCharSprite, false)
	if got := pr.rig.manager.Stats().MountFetches; got != 0 {
		t.Errorf("MountFetches = %d during a replay, want 0", got)
	}
}

// TestMountLayerSwapDuringResolveRaceClean mirrors the overlay's race gate: the
// render thread republishes the layer while pool workers resolve.
func TestMountLayerSwapDuringResolveRaceClean(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/(a)normal.webp": "SERVER"},
		map[string]string{"characters/witch/(a)normal.png": "PACK"},
	)
	dirB := t.TempDir()
	writePack(t, dirB, map[string]string{"characters/witch/(a)normal.png": "PACK-B"})
	ixB, _ := BuildMountIndex([]string{dirB})
	defer ixB.Retire()
	layerB := NewMountLayer(ixB, []string{dirB}, []string{pr.origin})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			if i%2 == 0 {
				pr.rig.manager.SetMountLayer(layerB)
			} else {
				pr.rig.manager.SetMountLayer(pr.layer)
			}
		}
	}()
	for i := 0; i < 200; i++ {
		pr.rig.manager.resolveExact(pr.base("evidence/knife.png"), AssetTypeMisc)
	}
	<-done
}

// --- the no-mounts contract ----------------------------------------------------

// TestNoMountsIsExactlyOneAtomicLoad is the structural half of the "0 base = 0
// cost" rule: with no layer installed the whole feature must resolve to a nil
// pointer and stop, touching no index, no goroutine and no disk.
func TestNoMountsIsExactlyOneAtomicLoad(t *testing.T) {
	rig := newRig(t, network.NewClient(), false)
	if l := rig.manager.activeMountLayer(); l != nil {
		t.Fatal("a Manager with no mounts reports a live layer")
	}
	if n := testing.AllocsPerRun(1000, func() { _ = rig.manager.activeMountLayer() }); n != 0 {
		t.Errorf("the no-mounts check allocates %.1f/op, want 0", n)
	}
}

// BenchmarkActiveMountLayerNoMounts is the gate behind the "no slowdown when no
// local base is configured" rule. It exists because NO pre-existing benchmark
// traverses this hook, which would make any "no regression" claim vacuous — the
// old gates would pass without ever executing the new code.
func BenchmarkActiveMountLayerNoMounts(b *testing.B) {
	m := &Manager{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if m.activeMountLayer() != nil {
			b.Fatal("unexpected layer")
		}
	}
}

// BenchmarkActiveMountLayerConfigured is the comparison point: what a user who
// HAS configured mounts pays for the same check.
func BenchmarkActiveMountLayerConfigured(b *testing.B) {
	m := &Manager{}
	m.SetMountLayer(&MountLayer{})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.activeMountLayer()
	}
}
