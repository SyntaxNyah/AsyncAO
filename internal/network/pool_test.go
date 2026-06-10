package network

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolRunsEveryAcceptedJobExactlyOnce(t *testing.T) {
	p := NewPool(4)
	defer p.Close()

	const jobs = 500
	var runs atomic.Int64
	var wg sync.WaitGroup
	wg.Add(jobs)
	for i := 0; i < jobs; i++ {
		prio := PriorityHigh
		if i%2 == 0 {
			prio = PriorityLow
		}
		p.Submit(prio, Job{
			ID:    p.NextID(),
			Epoch: p.Epoch(),
			Run: func(stale bool) {
				runs.Add(1)
				wg.Done()
			},
		})
	}
	wg.Wait()
	if got := runs.Load(); got != jobs {
		t.Errorf("runs = %d, want %d (every job exactly once)", got, jobs)
	}
}

func TestPoolHighPriorityRunsFirst(t *testing.T) {
	p := NewPool(1) // single worker so ordering is observable
	defer p.Close()

	// Occupy the worker so subsequent jobs queue up.
	gate := make(chan struct{})
	started := make(chan struct{})
	p.Submit(PriorityHigh, Job{Epoch: EpochAny, Run: func(bool) {
		close(started)
		<-gate
	}})
	<-started

	var order []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	record := func(name string) Job {
		wg.Add(1)
		return Job{Epoch: EpochAny, Run: func(bool) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			wg.Done()
		}}
	}
	p.Submit(PriorityLow, record("low1"))
	p.Submit(PriorityLow, record("low2"))
	p.Submit(PriorityHigh, record("high1"))
	p.Submit(PriorityHigh, record("high2"))

	close(gate)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 4 || order[0] != "high1" || order[1] != "high2" {
		t.Errorf("execution order = %v, want high1,high2 before lows", order)
	}
}

func TestPoolEpochCancelsQueuedJobs(t *testing.T) {
	p := NewPool(1)
	defer p.Close()

	gate := make(chan struct{})
	started := make(chan struct{})
	p.Submit(PriorityHigh, Job{Epoch: EpochAny, Run: func(bool) {
		close(started)
		<-gate
	}})
	<-started

	var staleRuns, freshRuns atomic.Int64
	var wg sync.WaitGroup
	const queued = 8
	wg.Add(queued)
	oldEpoch := p.Epoch()
	for i := 0; i < queued; i++ {
		p.Submit(PriorityLow, Job{Epoch: oldEpoch, Run: func(stale bool) {
			if stale {
				staleRuns.Add(1)
			} else {
				freshRuns.Add(1)
			}
			wg.Done()
		}})
	}

	p.BumpEpoch() // room change while jobs are still queued
	close(gate)
	wg.Wait()

	if got := staleRuns.Load(); got != queued {
		t.Errorf("stale runs = %d, want %d (epoch bump must cancel queued jobs)", got, queued)
	}
	if got := freshRuns.Load(); got != 0 {
		t.Errorf("fresh runs = %d, want 0", got)
	}
}

func TestPoolEpochAnySurvivesBump(t *testing.T) {
	p := NewPool(1)
	defer p.Close()

	gate := make(chan struct{})
	started := make(chan struct{})
	p.Submit(PriorityHigh, Job{Epoch: EpochAny, Run: func(bool) {
		close(started)
		<-gate
	}})
	<-started

	result := make(chan bool, 1)
	p.Submit(PriorityLow, Job{Epoch: EpochAny, Run: func(stale bool) { result <- stale }})
	p.BumpEpoch()
	close(gate)

	if stale := <-result; stale {
		t.Error("EpochAny job went stale after a bump")
	}
}

func TestPoolShedsOldestLowJobWhenFull(t *testing.T) {
	p := NewPool(1)
	defer p.Close()

	gate := make(chan struct{})
	started := make(chan struct{})
	p.Submit(PriorityHigh, Job{Epoch: EpochAny, Run: func(bool) {
		close(started)
		<-gate
	}})
	<-started

	// Fill the low lane completely.
	type outcome struct {
		id    int
		stale bool
	}
	outcomes := make(chan outcome, lowLaneCap+1)
	for i := 0; i < lowLaneCap; i++ {
		id := i
		p.Submit(PriorityLow, Job{Epoch: EpochAny, Run: func(stale bool) {
			outcomes <- outcome{id: id, stale: stale}
		}})
	}
	// One more: must shed the OLDEST (id 0), not block, not drop itself.
	overflowDone := make(chan struct{})
	go func() {
		p.Submit(PriorityLow, Job{Epoch: EpochAny, Run: func(stale bool) {
			outcomes <- outcome{id: lowLaneCap, stale: stale}
		}})
		close(overflowDone)
	}()
	select {
	case <-overflowDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Submit blocked on a full low lane; must shed instead")
	}

	close(gate)

	results := make(map[int]bool, lowLaneCap+1)
	for i := 0; i < lowLaneCap+1; i++ {
		select {
		case o := <-outcomes:
			results[o.id] = o.stale
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d/%d outcomes delivered: a result was dropped", i, lowLaneCap+1)
		}
	}
	if stale, ok := results[0]; !ok || !stale {
		t.Errorf("oldest job: stale=%v present=%v, want shed (stale=true)", stale, ok)
	}
	if stale := results[lowLaneCap]; stale {
		t.Error("newest job was shed; must shed the oldest")
	}
	if s := p.Stats(); s.Shed == 0 {
		t.Error("Shed counter not incremented")
	}
}

func TestPoolCloseCancelsQueuedJobs(t *testing.T) {
	p := NewPool(1)
	gate := make(chan struct{})
	started := make(chan struct{})
	p.Submit(PriorityHigh, Job{Epoch: EpochAny, Run: func(bool) {
		close(started)
		<-gate
	}})
	<-started

	var cancelled atomic.Int64
	const queued = 4
	var wg sync.WaitGroup
	wg.Add(queued)
	for i := 0; i < queued; i++ {
		p.Submit(PriorityLow, Job{Epoch: EpochAny, Run: func(stale bool) {
			if stale {
				cancelled.Add(1)
			}
			wg.Done()
		}})
	}

	close(gate)
	p.Close()
	wg.Wait()

	// After Close, submissions are refused but still hear back.
	heardBack := false
	accepted := p.Submit(PriorityHigh, Job{Epoch: EpochAny, Run: func(stale bool) { heardBack = stale }})
	if accepted {
		t.Error("Submit accepted after Close")
	}
	if !heardBack {
		t.Error("post-Close job did not get Run(stale=true)")
	}
}

func TestPoolConcurrentSubmitAndBump(t *testing.T) {
	p := NewPool(DefaultWorkers)

	var wg sync.WaitGroup
	var delivered atomic.Int64
	const producers = 8
	const perProducer = 200
	for g := 0; g < producers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				prio := PriorityLow
				if i%4 == 0 {
					prio = PriorityHigh
				}
				// Every job must hear back exactly once — fresh, stale, or
				// refused-inline — regardless of concurrent epoch bumps.
				p.Submit(prio, Job{Epoch: p.Epoch(), Run: func(bool) {
					delivered.Add(1)
				}})
				if i%50 == 0 {
					p.BumpEpoch()
				}
			}
		}()
	}
	wg.Wait()
	p.Close() // cancels whatever is still queued, inline

	if got := delivered.Load(); got != producers*perProducer {
		t.Errorf("delivered = %d, want %d (results must never be dropped)", got, producers*perProducer)
	}
}
