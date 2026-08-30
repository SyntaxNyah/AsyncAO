package assets

import (
	"image/color"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// missWait bounds a "did the pipeline settle" wait. Every pass here is against a
// loopback 404, so a settled state arrives in milliseconds; the budget is only
// generous enough to survive a loaded CI box.
const missWait = 3 * time.Second

// settleMiss drives one prefetch to full exhaustion and returns once the
// manager has recorded it, so a following assertion measures the SETTLED state
// rather than racing the pool worker that is still walking candidates.
func settleMiss(t *testing.T, rig *testRig, prefetch func()) {
	t.Helper()
	prefetch()
	waitWarning(t, rig.manager)
	deadline := time.Now().Add(missWait)
	for rig.manager.ConclusiveMissCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the pass reported a miss but never remembered it")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestConclusiveMissStopsTheProbeButNotTheWarning is the field defect, reduced:
// a grid cell whose art the server does not have is re-demanded every
// charIconRetryInterval for as long as it is on screen (ui/app.go demandAsset),
// because that loop only ever terminated on the texture becoming resident and a
// 404'd base never does. On a roster where most characters ship no char_icon and
// no emotions/button<N>_off art that is a 404 leaving the client every couple of
// seconds, indefinitely.
//
// Both halves are load-bearing and they pull in opposite directions. The server
// must see the probes STOP — that is the whole fix. The warning must keep firing
// — Courtroom.NotifyAssetMissing rides that lane to skip a preanimation that can
// never arrive, so a memory that also swallowed the report would trade a network
// storm for every later message with that preanim holding the stage for the full
// PreanimTimeout.
func TestConclusiveMissStopsTheProbeButNotTheWarning(t *testing.T) {
	cs := newCountingServer(t, nil) // serves nothing: every probe 404s
	rig := newRig(t, network.NewClient(), false)
	base := cs.srv.URL + "/characters/uma/emotions/button3_off"

	demand := func() {
		rig.manager.Prefetch(base, AssetTypeEmoteButton, network.PriorityHigh) // AssetType: EmoteButton
	}
	settleMiss(t, rig, demand)
	probed := cs.total()
	if probed == 0 {
		t.Fatal("the first demand must actually probe the server — otherwise this test proves nothing")
	}

	// The cadence, compressed: what demandAsset does every two seconds.
	for i := 0; i < 20; i++ {
		demand()
		if w := waitWarning(t, rig.manager); w.Base != base {
			t.Fatalf("re-demand %d: warning is for %q, want %q", i, w.Base, base)
		}
	}
	if got := cs.total(); got != probed {
		t.Errorf("re-demanding an absent asset cost %d further request(s); the session memory must make it cost none", got-probed)
	}
}

// TestConclusiveMissKeysOnBaseAndType pins the granularity. The memory has to be
// per ASSET, not per character or per type: an emote whose _off art is absent
// almost always still has _on art (that is the shape of the roster that produced
// this), and a character with no icon may still have every sprite. A memory
// keyed any coarser would blank art that exists.
func TestConclusiveMissKeysOnBaseAndType(t *testing.T) {
	sprite := encodePNG(t, 8, 8, color.RGBA{G: 255, A: 255})
	cs := newCountingServer(t, map[string][]byte{
		"/characters/uma/emotions/button3_on.webp": sprite, // EmoteButton probes .webp
		"/characters/uma/char_icon.png":            sprite, // CharIcon probes .png (config.defaultFormatOrders)
	})
	rig := newRig(t, network.NewClient(), false)
	stem := cs.srv.URL + "/characters/uma/"

	settleMiss(t, rig, func() {
		rig.manager.Prefetch(stem+"emotions/button3_off", AssetTypeEmoteButton, network.PriorityHigh) // AssetType: EmoteButton
	})

	// A DIFFERENT base under the same character: must still be fetched.
	on := retryDecoded(t, rig.manager, func() {
		rig.manager.Prefetch(stem+"emotions/button3_on", AssetTypeEmoteButton, network.PriorityHigh) // AssetType: EmoteButton
	})
	if on.Err != nil {
		t.Fatalf("_on art was silenced by the _off miss: %v", on.Err)
	}

	// The SAME spelling under a different type is a different question (the
	// candidate format list is per-type), so it must not be answered from the
	// _off verdict either.
	if rig.manager.IsConclusiveMiss(stem+"emotions/button3_off", AssetTypeCharIcon) {
		t.Error("a miss recorded for one asset type leaked into another")
	}

	icon := retryDecoded(t, rig.manager, func() {
		rig.manager.Prefetch(stem+"char_icon", AssetTypeCharIcon, network.PriorityHigh) // AssetType: CharIcon
	})
	if icon.Err != nil {
		t.Fatalf("char_icon was silenced by the _off miss: %v", icon.Err)
	}
}

// TestNarrowChainDoesNotSilenceAWiderOne is why missChain carries the alt list.
//
// The same base is prefetched with DIFFERENT chains from different call sites —
// ui/screens.go asks a preview sprite bare, then asks the same base again with
// the (a)/(b)/bare ladder — and "the bare spelling 404s" says nothing about
// whether the ladder would. Keying on base alone would let the narrow pass
// answer the wide one's question, which is the same class of bug as blanking a
// shared learned-format slot: one asset's verdict applied where it was never
// established.
func TestNarrowChainDoesNotSilenceAWiderOne(t *testing.T) {
	sprite := encodePNG(t, 8, 8, color.RGBA{G: 255, A: 255})
	cs := newCountingServer(t, map[string][]byte{
		"/characters/uma/normal.webp": sprite, // only the BARE spelling exists
	})
	rig := newRig(t, network.NewClient(), false)
	primary := cs.srv.URL + "/characters/uma/(a)normal"
	bare := cs.srv.URL + "/characters/uma/normal"

	settleMiss(t, rig, func() {
		rig.manager.Prefetch(primary, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	})

	d := retryDecoded(t, rig.manager, func() {
		rig.manager.PrefetchWithFallback(primary, bare, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	})
	if d.Err != nil {
		t.Fatalf("the bare fallback was never probed: an alt-less miss silenced a chain that had alts: %v", d.Err)
	}
	if d.Base != primary {
		t.Errorf("delivered under %q; the chain's identity must stay the primary base", d.Base)
	}
}

// TestRehearsalMissesAreNotRemembered guards the one case where "not found" is
// not a finding. Rehearsal mode closes the network gate INSIDE netFetch, which
// answers ErrAssetNotFound for every candidate without asking anyone — so a
// session-long memory built from it would carry rehearsal's silence into the
// live session and blank assets that were only ever absent because the client
// was offline. The resolver refuses to learn formats from the same silence.
func TestRehearsalMissesAreNotRemembered(t *testing.T) {
	sprite := encodePNG(t, 8, 8, color.RGBA{G: 255, A: 255})
	cs := newCountingServer(t, map[string][]byte{"/background/court/defenseempty.webp": sprite})
	rig := newRig(t, network.NewClient(), false)
	base := cs.srv.URL + "/background/court/defenseempty"

	rig.manager.SetOffline(true)
	rig.manager.Prefetch(base, AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	waitWarning(t, rig.manager)
	if n := rig.manager.ConclusiveMissCount(); n != 0 {
		t.Fatalf("rehearsal mode recorded %d conclusive miss(es); an offline 'not found' establishes nothing", n)
	}

	rig.manager.SetOffline(false)
	d := retryDecoded(t, rig.manager, func() {
		rig.manager.Prefetch(base, AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	})
	if d.Err != nil {
		t.Fatalf("an asset that exists stayed unreachable after rehearsal ended: %v", d.Err)
	}
}

// TestFormatPreferenceChangeRetiresTheMemory pins what "exhausted" is relative
// to, and it is not the base alone.
//
// A remembered miss means "every candidate the resolver generated 404'd", and
// the resolver generates candidates from the format preferences. Change them and
// that answer was about a list which is no longer the list. This is not a corner
// case: it is the FIRST thing a user does when sprites look missing — open
// Settings > Assets and add the format the server actually uses — and the gate
// sits above the resolver, so a memory that ignored the change would make that
// setting do nothing, silently and permanently.
//
// The scenario is TestManagerPNGOnlyServerWarnsThenFallbacksRecover's, which is
// how the hole was found: a PNG-only server with fallbacks off warns, then
// turning fallbacks on loads it without a restart.
func TestFormatPreferenceChangeRetiresTheMemory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(rig *testRig)
	}{
		{"global fallbacks turned on", func(rig *testRig) { rig.prefs.SetGlobalFallbacks(true) }},
		{"the type's format order gains the right one", func(rig *testRig) {
			rig.prefs.SetFormatOrder(AssetTypeCharSprite.Name(), []string{config.ExtPNG})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sprite := encodePNG(t, 8, 8, color.RGBA{B: 255, A: 255})
			// A PNG-only server. The default char-sprite probe list is webp-only,
			// so the first pass exhausts its whole (one-entry) list and records it.
			cs := newCountingServer(t, map[string][]byte{
				"/characters/edgeworth/(a)normal.png": sprite,
			})
			rig := newRig(t, network.NewClient(), false)
			base := cs.srv.URL + "/characters/edgeworth/(a)normal"

			settleMiss(t, rig, func() {
				rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
			})

			tc.change(rig)

			if rig.manager.IsConclusiveMiss(base, AssetTypeCharSprite) {
				t.Fatal("the old verdict outlived the probe list it was computed from — the new format is never tried, " +
					"so the setting the user just changed does nothing")
			}
			d := retryDecoded(t, rig.manager, func() {
				rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
			})
			if d.Err != nil {
				t.Fatalf("the sprite stayed unreachable after the probe list changed: %v", d.Err)
			}
			d.Asset.Release()
		})
	}
}

// TestSourceChangeForgetsRememberedMisses pins the flush where it cannot be
// skipped: inside the setters themselves.
//
// Supplying an asset the server does not have is the entire reason to attach a
// local pack, and the prefetch gate sits ABOVE the mount layer — so a remembered
// miss would shadow exactly the files the user just made available, with nothing
// on screen to explain it. Leaving that to the call sites is how it rots; the
// test therefore drives ONLY the setter.
func TestSourceChangeForgetsRememberedMisses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(m *Manager)
	}{
		{"mount layer installed", func(m *Manager) { m.SetMountLayer(&MountLayer{}) }},
		{"mount layer cleared", func(m *Manager) { m.SetMountLayer(nil) }},
		{"local overlay installed", func(m *Manager) { m.SetLocalOverlay(NewLocalFetcher([]string{t.TempDir()})) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := newCountingServer(t, nil)
			rig := newRig(t, network.NewClient(), false)
			base := cs.srv.URL + "/characters/uma/char_icon"

			settleMiss(t, rig, func() {
				rig.manager.Prefetch(base, AssetTypeCharIcon, network.PriorityHigh) // AssetType: CharIcon
			})
			tc.change(rig.manager)
			if rig.manager.IsConclusiveMiss(base, AssetTypeCharIcon) {
				t.Fatal("a byte-source change left the old verdict standing: the newly available files are unreachable")
			}
		})
	}
}

// TestRepushingTheSameMountSetKeepsTheMemory is the other side of the same
// guard. rebuildAssetOrigin re-pushes the overlay on every connect and every
// settings edit, and one Manager serves every open tab — so if a no-op re-push
// cleared the memory, opening a second server would send the first one's whole
// roster back to the wire.
func TestRepushingTheSameMountSetKeepsTheMemory(t *testing.T) {
	cs := newCountingServer(t, nil)
	rig := newRig(t, network.NewClient(), false)
	base := cs.srv.URL + "/characters/uma/char_icon"
	mount := t.TempDir()

	rig.manager.SetLocalOverlay(NewLocalFetcher([]string{mount}))
	settleMiss(t, rig, func() {
		rig.manager.Prefetch(base, AssetTypeCharIcon, network.PriorityHigh) // AssetType: CharIcon
	})
	rig.manager.SetLocalOverlay(NewLocalFetcher([]string{mount})) // same mounts, fresh fetcher
	if !rig.manager.IsConclusiveMiss(base, AssetTypeCharIcon) {
		t.Error("re-pushing an unchanged mount set wiped the memory; only a real source change may")
	}
}

// TestForgetUnderIsScopedToOneOrigin pins the reconnect flush's blast radius.
// One Manager serves every open server tab, so rejoining one of them must not
// send the others back to the wire for assets they had already settled — that
// traffic is what this whole mechanism exists to remove.
func TestForgetUnderIsScopedToOneOrigin(t *testing.T) {
	rejoined := newCountingServer(t, nil)
	other := newCountingServer(t, nil)
	rig := newRig(t, network.NewClient(), false)
	mine := rejoined.srv.URL + "/characters/uma/char_icon"
	theirs := other.srv.URL + "/characters/uma/char_icon"

	settleMiss(t, rig, func() {
		rig.manager.Prefetch(mine, AssetTypeCharIcon, network.PriorityHigh) // AssetType: CharIcon
	})
	rig.manager.Prefetch(theirs, AssetTypeCharIcon, network.PriorityHigh) // AssetType: CharIcon
	waitWarning(t, rig.manager)
	deadline := time.Now().Add(missWait)
	for rig.manager.ConclusiveMissCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	rig.manager.ForgetConclusiveMissesUnder(rejoined.srv.URL + "/")
	if rig.manager.IsConclusiveMiss(mine, AssetTypeCharIcon) {
		t.Error("the rejoined server's own verdict survived its reconnect")
	}
	if !rig.manager.IsConclusiveMiss(theirs, AssetTypeCharIcon) {
		t.Error("reconnecting one tab wiped another still-connected server's memory")
	}

	// An empty origin is a caller with nothing to name, not a licence to wipe.
	rig.manager.ForgetConclusiveMissesUnder("")
	if !rig.manager.IsConclusiveMiss(theirs, AssetTypeCharIcon) {
		t.Error("an empty origin behaved as a blanket flush")
	}
}

// TestEveryPrefetchEntryPointConsultsTheMissGate is the encapsulation test: the
// seam must not be bypassable, and a prefetch path added later must not be able
// to quietly reopen the storm.
//
// It reflects over the Manager's exported Prefetch* surface rather than naming
// the methods that exist today — that is the difference between pinning the
// behaviour and pinning the file. A new PrefetchWhatever that submits a job
// without consulting the memory fails here on the day it is written, with a
// message that says what it forgot.
//
// WHAT IT MEASURES, AND WHY NOT REQUESTS. The first cut of this test counted
// requests arriving at the server, and it PASSED for PrefetchSticky and
// PrefetchRaw while both were ungated: the network client's own 404 cache
// absorbs a repeat inside its TTL, so no wire traffic proves nothing about
// whether the pipeline above it ran. Two real gaps sat green underneath that
// assertion. The pool counter is the honest observable — a gated entry point
// submits NO job, so Executed+Stale cannot move, and the network cache has no
// say in it. The request count is still checked, because both costs matter, but
// it is the weaker of the two and must never be the only one.
func TestEveryPrefetchEntryPointConsultsTheMissGate(t *testing.T) {
	cs := newCountingServer(t, nil) // serves nothing
	rig := newRig(t, network.NewClient(), false)

	// Executed+Stale counts every job the pool ACCEPTED, however it ended, so a
	// submit cannot hide behind a shed or an epoch bump.
	poolWork := func() int64 {
		s := rig.pool.Stats()
		return s.Executed + s.Stale
	}

	mt := reflect.TypeOf(rig.manager)
	var entryPoints []reflect.Method
	for i := 0; i < mt.NumMethod(); i++ {
		if m := mt.Method(i); strings.HasPrefix(m.Name, "Prefetch") {
			entryPoints = append(entryPoints, m)
		}
	}
	if len(entryPoints) < 4 {
		t.Fatalf("found only %d Prefetch* entry points; the reflection walk has stopped seeing the surface it guards", len(entryPoints))
	}

	for _, m := range entryPoints {
		t.Run(m.Name, func(t *testing.T) {
			// The base carries the method name so each entry point owns its own
			// asset and cannot be settled by another's recorded miss.
			base := cs.srv.URL + "/characters/uma/" + m.Name
			args := prefetchArgs(t, m.Type, rig.manager, base)

			m.Func.Call(args)
			waitForRequests(t, cs, base)
			// Let the settling pass leave the pool before the baseline is taken,
			// or its own job would be counted against the repeats below.
			waitForPoolIdle(t, poolWork)
			settledReqs, settledJobs := cs.total(), poolWork()

			const repeats = 10
			for i := 0; i < repeats; i++ {
				m.Func.Call(args)
			}
			// Give a pass that DID escape the gate time to run and be counted.
			time.Sleep(200 * time.Millisecond)

			if got := poolWork(); got != settledJobs {
				t.Errorf("%s submitted %d pipeline job(s) for a chain already probed to exhaustion (want 0): "+
					"it must consult the conclusive-miss memory BEFORE pool.Submit, not rely on a cache further down",
					m.Name, got-settledJobs)
			}
			if got := cs.total(); got != settledReqs {
				t.Errorf("%s re-probed an exhausted chain %d time(s) on the wire", m.Name, got-settledReqs)
			}
		})
	}
}

// waitForPoolIdle blocks until the pool's accepted-job count stops moving, so a
// baseline is taken between passes rather than in the middle of one.
func waitForPoolIdle(t *testing.T, poolWork func() int64) {
	t.Helper()
	deadline := time.Now().Add(missWait)
	last, stable := int64(-1), 0
	for time.Now().Before(deadline) {
		n := poolWork()
		if n == last {
			if stable++; stable >= 3 {
				return
			}
		} else {
			last, stable = n, 0
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the pool never went idle: a settling pass is still submitting work, so any baseline taken here would be meaningless")
}

// prefetchArgs builds a call for any Prefetch* signature by parameter TYPE, so
// the reflection walk above keeps working across signatures it has never seen.
func prefetchArgs(t *testing.T, sig reflect.Type, m *Manager, base string) []reflect.Value {
	t.Helper()
	args := []reflect.Value{reflect.ValueOf(m)}
	for i := 1; i < sig.NumIn(); i++ {
		switch p := sig.In(i); {
		case p.Kind() == reflect.String:
			// Every string parameter is a URL base or an alternate spelling of one;
			// pointing them all at the same absent asset keeps the chain exhausted.
			args = append(args, reflect.ValueOf(base))
		case p == reflect.TypeOf([]string(nil)):
			args = append(args, reflect.ValueOf([]string(nil)))
		case p == reflect.TypeOf(AssetTypeCharIcon):
			args = append(args, reflect.ValueOf(AssetTypeCharIcon))
		case p == reflect.TypeOf(network.PriorityHigh):
			args = append(args, reflect.ValueOf(network.PriorityHigh))
		default:
			t.Fatalf("unhandled parameter %v in %v — teach prefetchArgs about it rather than exempting the method", p, sig)
		}
	}
	return args
}

// waitForRequests blocks until the server has seen at least one probe for base
// and then stopped seeing new ones, i.e. the chain has been walked to
// exhaustion and recorded.
func waitForRequests(t *testing.T, cs *countingServer, base string) {
	t.Helper()
	deadline := time.Now().Add(missWait)
	last, stable := -1, 0
	for time.Now().Before(deadline) {
		n := cs.total()
		if n > 0 && n == last {
			if stable++; stable >= 3 {
				return
			}
		} else {
			stable = 0
		}
		last = n
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no probe ever reached the server for %q; the entry point never resolved anything", base)
}
