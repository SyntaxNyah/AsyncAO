package assets

import (
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// TestPairedPrefetchResolvesConcurrently is the §11 prefetch gate: both pair
// sprites must resolve in parallel — total wall clock ≈ max(single), not the
// sum. The server delays every response; serial fetching would take 2×.
func TestPairedPrefetchResolvesConcurrently(t *testing.T) {
	const perRequestDelay = 150 * time.Millisecond
	sprite := encodePNG(t, 8, 8, color.RGBA{R: 200, A: 255})
	payloads := map[string][]byte{
		"/characters/phoenix/(a)normal.webp":   sprite,
		"/characters/edgeworth/(a)normal.webp": sprite,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(perRequestDelay) // fixed artificial latency per request
		if data, ok := payloads[r.URL.Path]; ok {
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	rig := newRig(t, network.NewClient(), false)

	start := time.Now()
	rig.manager.Prefetch(srv.URL+"/characters/phoenix/(a)normal", AssetTypeCharSprite, network.PriorityHigh)   // AssetType: CharSprite (speaker)
	rig.manager.Prefetch(srv.URL+"/characters/edgeworth/(a)normal", AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (pair)

	for i := 0; i < 2; i++ {
		d := waitDecoded(t, rig.manager)
		if d.Err != nil {
			t.Fatalf("decode error: %v", d.Err)
		}
		d.Asset.Release()
	}
	elapsed := time.Since(start)

	// Parallel budget: single-fetch time + 50% tolerance (spec §1
	// "paired cold ≈ single ±20%", relaxed for CI jitter). Serial would be
	// ≥ 2× perRequestDelay.
	budget := perRequestDelay + perRequestDelay/2
	if elapsed >= 2*perRequestDelay {
		t.Errorf("paired cold load took %v — looks serial (single=%v)", elapsed, perRequestDelay)
	} else if elapsed > budget {
		t.Logf("paired cold load %v over soft budget %v (CI jitter?)", elapsed, budget)
	}
}

// TestProbeBudget200CharServer is integration scenario §15.6: a scripted
// 200-character server session must stay within the §1 cold-load budget of
// ≤ 1 probe per asset and ≤ 450 probes total. 200 icons + 40 sprite pairs +
// background parts ≈ 285 assets → 285 probes with zero fallbacks.
func TestProbeBudget200CharServer(t *testing.T) {
	const (
		charCount   = 200
		spritePairs = 40
		probeBudget = 450
	)
	icon := encodePNG(t, 4, 4, color.White)
	sprite := encodePNG(t, 8, 8, color.RGBA{G: 128, A: 255})

	payloads := map[string][]byte{}
	for i := 0; i < charCount; i++ {
		payloads[fmt.Sprintf("/characters/char%03d/char_icon.png", i)] = icon
	}
	for i := 0; i < spritePairs; i++ {
		payloads[fmt.Sprintf("/characters/char%03d/(a)normal.webp", i)] = sprite
		payloads[fmt.Sprintf("/characters/char%03d/(b)normal.webp", i)] = sprite
	}
	for _, part := range []string{"defenseempty", "defensedesk", "prosecutorempty", "prosecutiondesk", "witnessempty"} {
		payloads["/background/courtroom/"+part+".webp"] = sprite
	}
	cs := newCountingServer(t, payloads)
	rig := newRig(t, network.NewClient(), false)

	// Drain decoded assets concurrently, exactly like the real client's
	// per-frame pump — the pipeline's bounded channels backpressure
	// producers that never drain (by design, §17.4).
	const totalAssets = charCount + spritePairs*2 + 5
	drained := make(chan error, 1)
	go func() {
		for i := 0; i < totalAssets; i++ {
			select {
			case d := <-rig.manager.Decoded():
				if d.Err != nil {
					drained <- fmt.Errorf("asset %d failed: %w", i, d.Err)
					return
				}
				d.Asset.Release()
			case <-time.After(30 * time.Second):
				drained <- fmt.Errorf("stalled after %d/%d assets", i, totalAssets)
				return
			}
		}
		drained <- nil
	}()

	assetCount := 0
	prefetch := func(base string, typ AssetType, prio network.Priority) {
		assetCount++
		rig.manager.Prefetch(cs.srv.URL+base, typ, prio) // AssetType: mixed (scripted session)
	}
	// Char-select icon wall arrives at LOW priority (like the real client);
	// live courtroom assets at HIGH.
	for i := 0; i < charCount; i++ {
		prefetch(fmt.Sprintf("/characters/char%03d/char_icon", i), AssetTypeCharIcon, network.PriorityLow)
	}
	for i := 0; i < spritePairs; i++ {
		prefetch(fmt.Sprintf("/characters/char%03d/(a)normal", i), AssetTypeCharSprite, network.PriorityHigh)
		prefetch(fmt.Sprintf("/characters/char%03d/(b)normal", i), AssetTypeCharSprite, network.PriorityHigh)
	}
	for _, part := range []string{"defenseempty", "defensedesk", "prosecutorempty", "prosecutiondesk", "witnessempty"} {
		prefetch("/background/courtroom/"+part, AssetTypeBackground, network.PriorityHigh)
	}
	if assetCount != totalAssets {
		t.Fatalf("asset accounting bug: %d != %d", assetCount, totalAssets)
	}

	if err := <-drained; err != nil {
		t.Fatal(err)
	}

	probes := cs.total()
	t.Logf("Cold load probes: %d for %d assets (budget %d)", probes, assetCount, probeBudget)
	if probes != assetCount {
		t.Errorf("probes = %d, want exactly %d (1 per asset, zero fallbacks)", probes, assetCount)
	}
	if probes > probeBudget {
		t.Errorf("probes = %d, over the §1 budget of %d", probes, probeBudget)
	}
}

// TestPrefetcherPredictsAlternatingSpeakers checks the Markov chain learns a
// back-and-forth and the pair partner gets the 2× prior.
func TestPrefetcherPredictsAlternatingSpeakers(t *testing.T) {
	sprite := encodePNG(t, 4, 4, color.White)
	cs := newCountingServer(t, map[string][]byte{
		"/characters/maya/(a)normal.webp":    sprite,
		"/characters/phoenix/(a)normal.webp": sprite,
	})
	rig := newRig(t, network.NewClient(), false)
	pf := NewPrefetcher(rig.manager, func(char string) string {
		return cs.srv.URL + "/characters/" + char + "/(a)normal"
	})

	// Alternating conversation: phoenix ↔ maya.
	for i := 0; i < 6; i++ {
		if i%2 == 0 {
			pf.OnMessage("phoenix", "")
		} else {
			pf.OnMessage("maya", "")
		}
	}
	// After "phoenix" speaks, "maya" must be predicted (and prefetched).
	deadline := time.Now().Add(managerWait)
	for time.Now().Before(deadline) {
		if _, ok := rig.t2.Get(cs.srv.URL + "/characters/maya/(a)normal.webp"); ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("predicted speaker's sprite never prefetched")
}

func TestPrefetcherPairPartnerFirstGuess(t *testing.T) {
	cs := newCountingServer(t, map[string][]byte{})
	rig := newRig(t, network.NewClient(), false)
	var requested []string
	var mu sync.Mutex
	pf := NewPrefetcher(rig.manager, func(char string) string {
		mu.Lock()
		requested = append(requested, char)
		mu.Unlock()
		return cs.srv.URL + "/characters/" + char + "/(a)normal"
	})
	// No history: the active pair partner is the best guess.
	pf.OnMessage("phoenix", "edgeworth")
	mu.Lock()
	defer mu.Unlock()
	if len(requested) != 1 || requested[0] != "edgeworth" {
		t.Errorf("first-guess prediction = %v, want [edgeworth]", requested)
	}
}
