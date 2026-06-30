package assets

import "testing"

// TestPredictTopN pins the aggressiveness slider's core (#100): top-N predicted
// next speakers come back best-first, and the active pair partner keeps its 2x prior.
func TestPredictTopN(t *testing.T) {
	p := &Prefetcher{transitions: map[string]map[string]int{
		"phoenix": {"maya": 5, "edgeworth": 3, "godot": 1},
	}}
	if got := p.predictTopNLocked("phoenix", "", 1); len(got) != 1 || got[0] != "maya" {
		t.Errorf("top1 = %v, want [maya]", got)
	}
	if got := p.predictTopNLocked("phoenix", "", 2); len(got) != 2 || got[0] != "maya" || got[1] != "edgeworth" {
		t.Errorf("top2 = %v, want [maya edgeworth]", got)
	}
	// Pair partner gets the 2x prior: edgeworth 3*2 = 6 now beats maya 5.
	if got := p.predictTopNLocked("phoenix", "edgeworth", 1); len(got) != 1 || got[0] != "edgeworth" {
		t.Errorf("with edgeworth pair prior, top1 = %v, want [edgeworth]", got)
	}
}

// TestSetAggressivenessClamps pins the 1..prefetchMaxPredict clamp.
func TestSetAggressivenessClamps(t *testing.T) {
	p := NewPrefetcher(nil, nil)
	p.SetAggressiveness(0)
	if p.maxPredict != 1 {
		t.Errorf("maxPredict=%d, want clamped to 1", p.maxPredict)
	}
	p.SetAggressiveness(99)
	if p.maxPredict != prefetchMaxPredict {
		t.Errorf("maxPredict=%d, want clamped to %d", p.maxPredict, prefetchMaxPredict)
	}
}
