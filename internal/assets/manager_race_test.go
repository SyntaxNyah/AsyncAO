package assets

import (
	"image/color"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// requestedWithExt reports whether the counting server ever saw a request for
// path (its full URL path) with the given extension appended. It reads the
// shared request map under the same mutex the request handler writes it with,
// so it is safe once the concurrent load has quiesced (all resolveChain
// goroutines joined).
func (cs *countingServer) requestedWithExt(path, ext string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.requests[path+ext] > 0
}

// requestCount returns how many times the counting server saw a request for the
// exact URL path (read under the request-map mutex).
func (cs *countingServer) requestCount(path string) int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.requests[path]
}

// TestManagerConcurrentInvalidateWindowDoesNotStrandExistingAssets is the
// regression for the learned-format empty-window race (the "every emote button
// renders the same character icon" report on PNG-buttons hosts).
//
// Root cause it pins: the OLD tryBase, when every learned-first candidate 404'd,
// blanked the shared per-(host, AssetType) learned slot with a speculative
// resolver.Invalidate BEFORE re-probing/restoring it. During that window ANY
// other asset of the same type on the same host — resolved on a different pool
// worker with no serialization between distinct bases — read the empty slot,
// fell back to the hardcoded type default (EmoteButton = .webp), spuriously
// 404'd a file that exists as .png, and (because it began with usedLearned ==
// false) took the early return without ever re-probing: it reported the asset
// missing and never fetched the .png that was right there.
//
// The invariant the fix upholds: no reader of the learned table for
// (host, AssetType) may EVER observe an empty entry while a different, still
// valid asset of that type is being re-validated. The fix satisfies it by never
// blanking the shared slot — the stale-learned re-probe reads the type's full
// format list without touching the table, and the only table write is
// walkCandidates' RecordSuccess CAS (old-valid -> new-valid), so a concurrent
// reader sees one valid value or the other, never nothing.
//
// The test forces true wall-clock overlap by calling resolveChain directly from
// its own goroutines (this test is in package assets): one goroutine drives the
// per-iteration MISSING button (its 404 round-trip opens the invalidate window
// on loopback ~0.1-1ms after start), and several drive EXISTING .png buttons,
// staggered across that window so at least one reader lands inside it. Distinct
// bases per iteration are mandatory — the inflight map dedups identical bases
// (which would collapse the concurrency) and T2 caches by full URL (a reused
// base would short-circuit before the resolver ran).
func TestManagerConcurrentInvalidateWindowDoesNotStrandExistingAssets(t *testing.T) {
	// raceIterations: enough distinct (missing, existing) bundles to hit the
	// narrow invalidate window many times over across the stagger sweep, while
	// keeping the whole test comfortably under the -race budget (~30s). Each
	// iteration is one missing-button round-trip plus a handful of existing
	// probes on loopback — sub-millisecond apiece — so ~100 iterations is a few
	// hundred ms of server work, dwarfed by decode/scheduling slack.
	const raceIterations = 100
	// readersPerIteration: several existing buttons per iteration so at least one
	// reader's BuildCandidates lands inside the (short) empty-slot window even if
	// others miss it.
	const readersPerIteration = 4
	// staggerStep spaces successive reader starts across the invalidate window.
	// The window opens only after the missing button's first 404 completes
	// (~0.1-1ms on loopback), so a same-instant start alone usually MISSES it;
	// sweeping reader delays across ~0..2.25ms ((readers-1) steps) makes a hit
	// near-certain without depending on scheduler luck.
	const staggerStep = 750 * time.Microsecond

	art := encodePNG(t, 8, 8, color.RGBA{G: 180, A: 255})

	// The per-(iteration, reader) EXISTING button base path (relative to the
	// origin): distinct per pair so the inflight map and T2 never collapse the
	// concurrency the test depends on.
	existingPath := func(i, k int) string {
		return "/characters/witch/emotions/btn" + strconv.Itoa(i) + "_" + strconv.Itoa(k) + "_off"
	}
	// The per-iteration absent optional button (no art in any format); its 404
	// opens the invalidate window in the old code.
	missingPath := func(i int) string {
		return "/characters/witch/emotions/button" + strconv.Itoa(i) + "_on"
	}

	// Pre-populate every existing button's .png. The missing per-iteration
	// buttons deliberately have NO art in any format.
	payloads := map[string][]byte{}
	for i := 0; i < raceIterations; i++ {
		for k := 0; k < readersPerIteration; k++ {
			payloads[existingPath(i, k)+config.ExtPNG] = art
		}
	}
	cs := newCountingServer(t, payloads)
	rig := newRig(t, network.NewClient(), false)

	host := hostOf(cs.srv.URL + "/x")
	// Seed the learned format once, mirroring the extensions.json manifest seed
	// for a PNG-buttons host.
	rig.resolver.RecordSuccess(host, AssetTypeEmoteButton, config.ExtPNG)

	// The set of existing-button bases (full URLs), so assertion 2 can classify a
	// warned base without brittle substring matching.
	existingBaseSet := map[string]bool{}
	for i := 0; i < raceIterations; i++ {
		for k := 0; k < readersPerIteration; k++ {
			existingBaseSet[cs.srv.URL+existingPath(i, k)] = true
		}
	}

	// A single background drain goroutine consumes BOTH manager channels for the
	// whole test: without it, decoded/warning deliveries block and the resolve
	// goroutines deadlock. It records every decoded base (with error state) and
	// every warned base into mutex-guarded maps. Stopped cleanly at test end so
	// the race detector / -count runs see no leaked goroutine.
	var (
		drainMu     sync.Mutex
		decodedOK   = map[string]bool{}
		decodedErr  = map[string]bool{}
		warnedBases = map[string]bool{}
	)
	drainStop := make(chan struct{})
	var drainWG sync.WaitGroup
	drainWG.Add(1)
	go func() {
		defer drainWG.Done()
		for {
			select {
			case d := <-rig.manager.Decoded():
				if d.Asset != nil {
					d.Asset.Release()
				}
				drainMu.Lock()
				if d.Err != nil {
					decodedErr[d.Base] = true
				} else {
					decodedOK[d.Base] = true
				}
				drainMu.Unlock()
			case w := <-rig.manager.Warnings():
				drainMu.Lock()
				warnedBases[w.Base] = true
				drainMu.Unlock()
			case <-drainStop:
				return
			}
		}
	}()

	for i := 0; i < raceIterations; i++ {
		missingBase := cs.srv.URL + missingPath(i)
		start := make(chan struct{})
		var iterWG sync.WaitGroup

		// The missing-button goroutine: its learned .png 404 opens the invalidate
		// window (old code) roughly one loopback RTT after start.
		iterWG.Add(1)
		go func() {
			defer iterWG.Done()
			<-start
			rig.manager.resolveChain(missingBase, nil, AssetTypeEmoteButton, false)
		}()

		// Existing-button readers, staggered across the window.
		existingBases := make([]string, readersPerIteration)
		for k := 0; k < readersPerIteration; k++ {
			existingBases[k] = cs.srv.URL + existingPath(i, k)
			iterWG.Add(1)
			go func(base string, k int) {
				defer iterWG.Done()
				<-start
				if d := time.Duration(k) * staggerStep; d > 0 {
					time.Sleep(d)
				}
				rig.manager.resolveChain(base, nil, AssetTypeEmoteButton, false)
			}(existingBases[k], k)
		}

		close(start)
		iterWG.Wait()

		// Assertion 1: every existing button of this iteration eventually
		// decoded, with no error. Pre-fix, a raced reader took the early return
		// and reported missing — it never delivered a decode, detectable here as
		// a base that never appears in decodedOK. Bounded deadline mirrors the
		// managerWait idiom (drains happen on the background goroutine, so allow
		// slack past iterWG.Wait for the decode + delivery to land).
		deadline := time.Now().Add(managerWait)
		for k := 0; k < readersPerIteration; k++ {
			base := existingBases[k]
			for {
				drainMu.Lock()
				ok := decodedOK[base]
				bad := decodedErr[base]
				drainMu.Unlock()
				if ok {
					break
				}
				if bad {
					t.Fatalf("iteration %d reader %d: existing button %q decoded with error", i, k, base)
				}
				if time.Now().After(deadline) {
					t.Fatalf("iteration %d reader %d: existing button %q never decoded "+
						"(stranded by the learned-format empty window)", i, k, base)
				}
				time.Sleep(time.Millisecond)
			}
		}
	}

	close(drainStop)
	drainWG.Wait()

	// Assertion 2: no existing button was ever warned as missing (missing-button
	// warnings are expected and fine — we do not assert their count, the warning
	// channel is lossy by design). Softer than #1/#3 because warnings can drop
	// under flood, but a surviving one is a clear indictment.
	drainMu.Lock()
	for base := range warnedBases {
		if existingBaseSet[base] {
			t.Errorf("existing button %q was warned missing", base)
		}
	}
	drainMu.Unlock()

	// Assertion 3 (sharpest, immune to warning drops): interrogate the server's
	// request map directly.
	//   (a) No existing .png button was EVER probed with the .webp type default
	//       — pre-fix, a reader that read the blanked slot probed exactly
	//       existing-base + .webp. Post-fix the slot never blanks, so every
	//       existing base resolves learned-first as .png only.
	//   (b) Each existing .png path was requested exactly once (learned-first
	//       single probe; T2 never re-fetches a distinct base).
	for i := 0; i < raceIterations; i++ {
		for k := 0; k < readersPerIteration; k++ {
			p := existingPath(i, k)
			if cs.requestedWithExt(p, config.ExtWebP) {
				t.Fatalf("existing button %q was probed with the .webp default "+
					"(a reader saw the blanked learned slot — the empty-window race)", p)
			}
			if got := cs.requestCount(p + config.ExtPNG); got != 1 {
				t.Errorf("existing button %q.png requested %d times, want exactly 1 "+
					"(learned-first single probe)", p, got)
			}
		}
	}
}
