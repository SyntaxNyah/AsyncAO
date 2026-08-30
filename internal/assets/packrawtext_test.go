package assets

// The text half of the local pack layer (issue #72): a mounted base answers
// char.ini and effects.ini, not just art. These pin the four things that make
// that safe — the pack wins over a cached server copy, it teaches the server
// nothing, it writes no server-keyed cache, and the deliberate exclusions
// (extensions.json, directory listings) still reach the network.

import (
	"context"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// TestPackServesCharINI is the issue itself: a sprite maker's own char.ini is
// what decides their emote list, showname, blips, chatbox skin and scaling, and
// before this it was read off the server while the art beside it came from the
// pack.
func TestPackServesCharINI(t *testing.T) {
	const ini = "[Options]\nshowname = Witch\n[Emotions]\nnumber = 1\n1 = one#-#normal#0#\n"
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/char.ini": "SERVER INI"},
		map[string]string{"characters/witch/char.ini": ini},
	)

	data, err := pr.rig.manager.FetchRawLayered(context.Background(), pr.base("characters/witch/char.ini"))
	if err != nil {
		t.Fatalf("FetchRawLayered: %v", err)
	}
	if string(data) != ini {
		t.Errorf("got %q, want the PACK's ini — the server answered instead", data)
	}
	if n := pr.hits["/base/characters/witch/char.ini"]; n != 0 {
		t.Errorf("the server was probed %d time(s) for a file the pack holds", n)
	}
	if got := pr.rig.manager.Stats().MountFetches; got != 1 {
		t.Errorf("MountFetches = %d, want 1", got)
	}
}

// TestPackCharINIBeatsCachedServerCopy pins the ORDERING that makes the feature
// usable at all: the pack is consulted before T2, so a server copy already read
// this session does not shadow the file the user just edited. Read after write,
// on the same Manager, is exactly the sprite maker's loop.
func TestPackCharINIBeatsCachedServerCopy(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/char.ini": "SERVER INI"},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)
	url := pr.base("characters/witch/char.ini")

	// Warm T2 with the server's copy the way any pre-mount fetch would have.
	if _, err := pr.rig.manager.FetchRaw(context.Background(), url); err != nil {
		t.Fatalf("seeding the server copy: %v", err)
	}
	data, err := pr.rig.manager.FetchRawLayered(context.Background(), url)
	if err != nil {
		t.Fatalf("FetchRawLayered: %v", err)
	}
	if string(data) != "PACK INI" {
		t.Errorf("got %q — a cached server copy beat the pack", data)
	}
}

// TestPackTextMissFallsThroughToServer pins the partial pack: a base holding a
// couple of characters must leave every other char.ini streaming normally.
func TestPackTextMissFallsThroughToServer(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/other/char.ini": "SERVER INI"},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)

	data, err := pr.rig.manager.FetchRawLayered(context.Background(), pr.base("characters/other/char.ini"))
	if err != nil {
		t.Fatalf("FetchRawLayered: %v", err)
	}
	if string(data) != "SERVER INI" {
		t.Errorf("got %q, want the server's copy", data)
	}
	if st := pr.rig.manager.Stats(); st.MountFetches != 0 || st.NetFetches != 1 {
		t.Errorf("MountFetches = %d, NetFetches = %d; want 0 and 1", st.MountFetches, st.NetFetches)
	}
}

// TestPackTextWritesNoServerKeyedCache pins invariant 3 for the text lane. Pack
// bytes must never land in T2 or T3 under the SERVER's URL: they would outlive
// the pack there and be served as the server's own content after the folder is
// unmounted.
func TestPackTextWritesNoServerKeyedCache(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)
	url := pr.base("characters/witch/char.ini")

	if _, err := pr.rig.manager.FetchRawLayered(context.Background(), url); err != nil {
		t.Fatalf("FetchRawLayered: %v", err)
	}
	if _, ok := pr.rig.manager.t2.Get(url); ok {
		t.Error("pack bytes were cached in T2 under the SERVER's URL")
	}
	if _, ok := pr.rig.manager.disk.Get(url); ok {
		t.Error("pack bytes were written to the disk tier under the SERVER's URL")
	}
}

// TestPackTextNeverTeachesLearnedFormats pins invariant 2 for the text lane. The
// raw path has no AssetType and so no candidate list, but a future "learn from
// the winning read" would be the v1.61.0 / v1.87.2 regression class again.
func TestPackTextNeverTeachesLearnedFormats(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)
	host := HostOf(pr.origin)
	before := pr.rig.manager.resolver.LearnedList(host, AssetTypeCharSprite)

	if _, err := pr.rig.manager.FetchRawLayered(context.Background(), pr.base("characters/witch/char.ini")); err != nil {
		t.Fatalf("FetchRawLayered: %v", err)
	}
	if after := pr.rig.manager.resolver.LearnedList(host, AssetTypeCharSprite); len(after) != len(before) {
		t.Errorf("the pack taught the SERVER host a format: %v -> %v", before, after)
	}
}

// TestPackTextHonoursTheManifestExclusion is the exclusion that matters most on
// THIS lane, because extensions.json is a text file the pack really does hold: a
// pack is usually carved out of some server's base and ships a copy, and serving
// it would reseed the CURRENT host's learned probe order from the pack author's
// conventions — a wrong-format probe on every cold asset for that host.
//
// The predicate itself (listings, empty rels) is pinned by
// TestMountLayerExcludesManifestAndListings; this is the end-to-end half, and it
// is the manifest alone because a folder mount cannot hold a listing rel to be
// wrongly served in the first place.
func TestPackTextHonoursTheManifestExclusion(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/" + ManifestFileName: "SERVER MANIFEST"},
		map[string]string{ManifestFileName: "PACK MANIFEST"},
	)

	data, err := pr.rig.manager.FetchRawLayered(context.Background(), pr.base(ManifestFileName))
	if err != nil {
		t.Fatalf("FetchRawLayered(%s): %v", ManifestFileName, err)
	}
	if string(data) != "SERVER MANIFEST" {
		t.Errorf("got %q — the pack answered for the server's own format declaration", data)
	}
}

// awaitNetFetches polls the manager's own counter, which is what a warm can be
// observed through without racing the httptest handler's hit map (the pool runs
// the job on a worker, so pr.hits has a writer on another goroutine).
func awaitNetFetches(t *testing.T, m *Manager, want int64) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.Stats().NetFetches >= want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// TestPrefetchRawSkipsWhatThePackHolds pins the warm-path early-out. Warming a
// file the pack answers buys no RTT (the read is local) and would spend a real
// probe — plus a remembered miss — on a URL the server need not have at all.
func TestPrefetchRawSkipsWhatThePackHolds(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/char.ini": "SERVER INI"},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)
	url := pr.base("characters/witch/char.ini")

	pr.rig.manager.PrefetchRaw(url, network.PriorityLow)

	// The early-out is synchronous — the job is never submitted — so any fetch
	// this window catches is the bug. The window is generous on purpose: a flaky
	// PASS here would hide exactly the regression the test exists for.
	if awaitNetFetches(t, pr.rig.manager, 1) {
		t.Error("the warm probed the server for a file the pack holds")
	}
	if _, ok := pr.rig.manager.t2.Get(url); ok {
		t.Error("the warm cached the server's copy under a URL the pack answers")
	}
}

// TestPrefetchRawStillWarmsWhatThePackLacks is the other half: the early-out
// must not disarm warming for everything else the moment a folder is mounted.
func TestPrefetchRawStillWarmsWhatThePackLacks(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/other/char.ini": "SERVER INI"},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)

	pr.rig.manager.PrefetchRaw(pr.base("characters/other/char.ini"), network.PriorityLow)

	if !awaitNetFetches(t, pr.rig.manager, 1) {
		t.Error("the server was never probed — mounting a pack disarmed the warm for everything else")
	}
}

// TestFetchRawLayeredCostsNothingWithoutMounts is the standing rule for this
// whole feature: a user with no folders configured pays one atomic load and a
// predicted branch, and nothing else. No index, no allocation, no extra probe.
func TestFetchRawLayeredCostsNothingWithoutMounts(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/char.ini": "SERVER INI"},
		map[string]string{},
	)
	pr.rig.manager.SetMountLayer(nil)
	url := pr.base("characters/witch/char.ini")

	// Prime T2 so the measured call is the pure layered-lookup + cache-hit path,
	// with no network or allocation of its own to drown the signal.
	if _, err := pr.rig.manager.FetchRawLayered(context.Background(), url); err != nil {
		t.Fatalf("priming: %v", err)
	}
	ctx := context.Background()
	bare := testing.AllocsPerRun(100, func() {
		_, _ = pr.rig.manager.FetchRaw(ctx, url)
	})
	layered := testing.AllocsPerRun(100, func() {
		_, _ = pr.rig.manager.FetchRawLayered(ctx, url)
	})
	if layered > bare {
		t.Errorf("the nil-layer path allocates %v per call vs FetchRaw's %v — the no-mounts user is paying for the feature", layered, bare)
	}
}

// warmNoMountsRig is the shape both raw-text benchmarks measure: a streaming
// Manager with NO layer and the file already in T2, so what is timed is the
// lookup path itself rather than a network round trip.
func warmNoMountsRig(b *testing.B) (*packRig, string) {
	b.Helper()
	pr := newPackRig(b,
		map[string]string{"/base/characters/witch/char.ini": "SERVER INI"},
		map[string]string{},
	)
	pr.rig.manager.SetMountLayer(nil)
	url := pr.base("characters/witch/char.ini")
	if _, err := pr.rig.manager.FetchRaw(context.Background(), url); err != nil {
		b.Fatalf("priming T2: %v", err)
	}
	return pr, url
}

// BenchmarkFetchRawNoMounts is the BASELINE half of the pair: what the raw-text
// read cost before the pack layer existed. It is only meaningful next to
// BenchmarkFetchRawLayeredNoMounts below.
func BenchmarkFetchRawNoMounts(b *testing.B) {
	pr, url := warmNoMountsRig(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := pr.rig.manager.FetchRaw(ctx, url); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFetchRawLayeredNoMounts measures what a user who has configured NO
// local folder pays for issue #72's fix. BenchmarkActiveMountLayerNoMounts times
// the gate hook in isolation; this times the whole call a char.ini read actually
// makes, so the "no slowdown in stream mode" claim rests on a measurement of the
// real path instead of an inspection of it. The delta against
// BenchmarkFetchRawNoMounts is the entire cost of the feature to that user.
func BenchmarkFetchRawLayeredNoMounts(b *testing.B) {
	pr, url := warmNoMountsRig(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := pr.rig.manager.FetchRawLayered(ctx, url); err != nil {
			b.Fatal(err)
		}
	}
}
