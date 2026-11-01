package assets

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

type stalledSource struct {
	release chan struct{}
	fetches atomic.Int64
}

func newStalledSource() *stalledSource { return &stalledSource{release: make(chan struct{})} }

func (s *stalledSource) Fetch(context.Context, string) ([]byte, error) {
	s.fetches.Add(1)
	<-s.release
	return nil, errors.New("host stalled")
}

// occupyWorkers parks every rig worker on a distinct stalled asset, so later jobs stay queued.
func occupyWorkers(t *testing.T, rig *testRig, src *stalledSource) {
	t.Helper()
	workers := rig.pool.Stats().Workers
	for i := range workers {
		rig.manager.Prefetch(fmt.Sprintf("https://h.example/characters/c/(a)busy%d", i), AssetTypeCharSprite, network.PriorityHigh)
	}
	deadline := time.Now().Add(managerWait)
	for src.fetches.Load() < workers {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d workers started", src.fetches.Load(), workers)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRepeatRequestsDoNotFillTheHighLane: UI code asks every frame for a missing asset, and a stalled host must not turn
// those asks into a full lane and refused requests.
func TestRepeatRequestsDoNotFillTheHighLane(t *testing.T) {
	const repeats = 1000 // well past the lane's capacity
	src := newStalledSource()
	rig := newRig(t, src, false)
	t.Cleanup(func() { close(src.release) })
	occupyWorkers(t, rig, src)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range repeats {
			rig.manager.Prefetch("https://h.example/characters/c/(a)hover", AssetTypeCharSprite, network.PriorityHigh)
		}
	}()
	select {
	case <-done:
	case <-time.After(managerWait):
		t.Fatal("repeated requests for one asset blocked the caller")
	}
	if got := rig.pool.Stats().HighQueued; got != 1 {
		t.Errorf("high lane holds %d jobs, want the one job for the asset", got)
	}
	if got := rig.manager.RefusedRequests(); got != 0 {
		t.Errorf("refused = %d, want none", got)
	}
}

// TestFullHighLaneRefusesInsteadOfBlocking: the caller is the frame thread, so a stalled host must never make it wait.
func TestFullHighLaneRefusesInsteadOfBlocking(t *testing.T) {
	const extra = 8
	const refusedBase = "https://h.example/characters/c/(a)refused"
	src := newStalledSource()
	rig := newRig(t, src, false)
	// Every released fetch reports a failure here, and the frame loop isn't running to take them.
	go func() {
		for range rig.manager.Decoded() {
		}
	}()
	occupyWorkers(t, rig, src)
	released := false
	release := func() {
		if !released {
			released = true
			close(src.release)
		}
	}
	t.Cleanup(release)

	lane := rig.pool.Stats().HighCap
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range lane + extra {
			rig.manager.Prefetch(fmt.Sprintf("https://h.example/characters/c/(a)fill%d", i), AssetTypeCharSprite, network.PriorityHigh)
		}
		rig.manager.Prefetch(refusedBase, AssetTypeCharSprite, network.PriorityHigh)
	}()
	select {
	case <-done:
	case <-time.After(managerWait):
		t.Fatal("a full high lane blocked the caller")
	}
	if got := rig.manager.RefusedRequests(); got != extra+1 {
		t.Errorf("refused = %d, want %d", got, extra+1)
	}
	if got := rig.manager.WorkStats().Refused; got != extra+1 {
		t.Errorf("work stats refused = %d, want %d", got, extra+1)
	}

	// A refusal leaves no trace, so the asset can be asked for again once there's room.
	release()
	waitUntil(t, "the lane to drain", func() bool {
		s := rig.pool.Stats()
		return s.HighQueued == 0 && s.Busy == 0
	})
	before := src.fetches.Load()
	rig.manager.Prefetch(refusedBase, AssetTypeCharSprite, network.PriorityHigh)
	waitUntil(t, "the refused asset to be fetched when asked again", func() bool { return src.fetches.Load() > before })
}

// TestHighRequestIsNotMergedIntoLowJob: an asset that becomes urgent must not wait behind shed-able speculation.
func TestHighRequestIsNotMergedIntoLowJob(t *testing.T) {
	const base = "https://h.example/characters/c/(a)urgent"
	src := newStalledSource()
	rig := newRig(t, src, false)
	t.Cleanup(func() { close(src.release) })
	occupyWorkers(t, rig, src)

	rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityLow)
	rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	s := rig.pool.Stats()
	if s.LowQueued != 1 || s.HighQueued != 1 {
		t.Errorf("lanes hold low=%d high=%d, want one job in each", s.LowQueued, s.HighQueued)
	}
}

// TestRequestAfterARoomChangeIsNotMergedIntoStaleJob: the old room's job runs stale, so the new room's would be lost.
func TestRequestAfterARoomChangeIsNotMergedIntoStaleJob(t *testing.T) {
	const base = "https://h.example/background/court/defenseempty"
	src := newStalledSource()
	rig := newRig(t, src, false)
	t.Cleanup(func() { close(src.release) })
	occupyWorkers(t, rig, src)

	rig.manager.Prefetch(base, AssetTypeBackground, network.PriorityHigh)
	rig.pool.BumpEpoch()
	rig.manager.Prefetch(base, AssetTypeBackground, network.PriorityHigh)
	if got := rig.pool.Stats().HighQueued; got != 2 {
		t.Errorf("high lane holds %d jobs, want the stale one and the new room's", got)
	}
}

// TestAssetCanBeRequestedAgainOnceItsJobRan: a merged request must not stop a later retry.
func TestAssetCanBeRequestedAgainOnceItsJobRan(t *testing.T) {
	const base = "https://h.example/characters/c/(a)retry"
	src := newStalledSource()
	close(src.release) // every fetch fails at once
	rig := newRig(t, src, false)

	rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	waitUntil(t, "the first job to finish", func() bool {
		s := rig.manager.WorkStats()
		return src.fetches.Load() > 0 && s.Pool.HighQueued == 0 && s.Pool.Busy == 0
	})
	first := src.fetches.Load()
	rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	waitUntil(t, "a finished job to leave the asset requestable", func() bool { return src.fetches.Load() > first })
}

func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(managerWait)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestWorkStatsShowsWhatIsRunningAndWaiting pins the debug panel's data.
func TestWorkStatsShowsWhatIsRunningAndWaiting(t *testing.T) {
	const waiting = "https://h.example/characters/c/(a)waiting"
	src := newStalledSource()
	rig := newRig(t, src, false)
	t.Cleanup(func() { close(src.release) })
	occupyWorkers(t, rig, src)
	rig.manager.Prefetch(waiting, AssetTypeCharSprite, network.PriorityHigh)
	rig.manager.Prefetch(waiting, AssetTypeCharSprite, network.PriorityHigh)

	s := rig.manager.WorkStats()
	if int64(len(s.Fetching)) != s.Pool.Workers || s.Pool.Busy != s.Pool.Workers {
		t.Errorf("fetching=%d busy=%d, want every worker listed", len(s.Fetching), s.Pool.Busy)
	}
	if s.Coalesced != 1 {
		t.Errorf("merged asks = %d, want 1", s.Coalesced)
	}
	if len(s.PerType) != 1 || s.PerType[0].Queued != s.Pool.Workers+1 || s.PerType[0].Started != s.Pool.Workers {
		t.Errorf("per type = %+v", s.PerType)
	}
}
