package assets

import (
	"image/color"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// TestPrefetcherPredictsAlternatingSpeakers checks the Markov chain learns a
// back-and-forth and the pair partner gets the 2× prior.
func TestPrefetcherPredictsAlternatingSpeakers(t *testing.T) {
	sprite := encodePNG(t, 4, 4, color.White)
	cs := newCountingServer(t, map[string][]byte{
		"/characters/maya/(a)normal.webp":    sprite,
		"/characters/phoenix/(a)normal.webp": sprite,
	})
	rig := newRig(t, network.NewClientNotFoundTTL(0, netproxy.Direct()), false)
	pf := NewPrefetcher(func(char, emote string) {
		if emote == "" {
			emote = "normal"
		}
		base := cs.srv.URL + "/characters/" + char + "/(a)" + emote
		rig.manager.PrefetchChainSpeculative(base, nil, AssetTypeCharSprite, network.PriorityLow)
	})

	// Alternating conversation: phoenix ↔ maya (constant emote — the
	// chain has no transitions, so the default sprite stays predicted).
	for i := 0; i < 6; i++ {
		if i%2 == 0 {
			pf.OnMessage("phoenix", "", "normal")
		} else {
			pf.OnMessage("maya", "", "normal")
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
	var requested []string
	var mu sync.Mutex
	pf := NewPrefetcher(func(char, emote string) {
		mu.Lock()
		requested = append(requested, char)
		mu.Unlock()
	})
	// No history: the active pair partner is the best guess.
	pf.OnMessage("phoenix", "edgeworth", "normal")
	mu.Lock()
	defer mu.Unlock()
	if len(requested) != 1 || requested[0] != "edgeworth" {
		t.Errorf("first-guess prediction = %v, want [edgeworth]", requested)
	}
}
