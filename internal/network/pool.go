package network

import (
	"sync"
	"sync/atomic"
)

const (
	// DefaultWorkers is the fetch worker count (PROMPT.md §7).
	DefaultWorkers = 8
	// highLaneCap and lowLaneCap bound the two priority lanes. The low lane
	// holds speculative prefetches and sheds its OLDEST job when full; the
	// high lane briefly blocks the producer instead — results are never
	// dropped or stolen (PROMPT.md §17.7).
	highLaneCap = 64
	lowLaneCap  = 256
)

// Priority selects a worker-pool lane.
type Priority int

const (
	// PriorityHigh is for assets the live message needs right now.
	PriorityHigh Priority = iota
	// PriorityLow is for speculative prefetches; jobs may be shed (run with
	// stale=true) under pressure or after an epoch bump.
	PriorityLow
)

// EpochAny marks a job that survives room/server changes (e.g. UI chrome).
const EpochAny int64 = -1

// Job is one unit of pool work. Run is invoked exactly once for every
// accepted job — with stale=false to do the work, or stale=true when the job
// was cancelled (epoch bump, shedding, shutdown) so waiters always hear back.
type Job struct {
	// ID exists for tracing/metrics. Allocate via Pool.NextID.
	ID int64
	// Epoch is the pool epoch the job belongs to (Pool.Epoch at queue
	// time), or EpochAny to never go stale.
	Epoch int64
	// Run does the work. It must be non-nil.
	Run func(stale bool)
}

// Pool is the prioritized fetch worker pool with epoch cancellation:
// room/server changes bump the epoch and all queued jobs from earlier epochs
// no-op (their Run gets stale=true) without spawning goroutines or losing
// anybody's results.
type Pool struct {
	high chan Job
	low  chan Job

	epoch  atomic.Int64
	nextID atomic.Int64

	stop      chan struct{}
	workersWG sync.WaitGroup
	closeOnce sync.Once

	executed atomic.Int64
	stale    atomic.Int64
	shed     atomic.Int64
}

// NewPool starts workers goroutines draining the two lanes, high first.
func NewPool(workers int) *Pool {
	if workers <= 0 {
		workers = DefaultWorkers
	}
	p := &Pool{
		high: make(chan Job, highLaneCap),
		low:  make(chan Job, lowLaneCap),
		stop: make(chan struct{}),
	}
	p.workersWG.Add(workers)
	for i := 0; i < workers; i++ {
		go p.worker()
	}
	return p
}

// NextID returns a unique job ID (atomic counter — collision-free, unlike
// the random IDs PROMPT.md §7 bans).
func (p *Pool) NextID() int64 {
	return p.nextID.Add(1)
}

// Epoch returns the current pool epoch. Capture it when building a Job.
func (p *Pool) Epoch() int64 {
	return p.epoch.Load()
}

// BumpEpoch invalidates every queued job from earlier epochs (room change,
// server switch). Queued stale jobs still get Run(stale=true) when a worker
// reaches them; in-flight jobs finish normally.
func (p *Pool) BumpEpoch() int64 {
	return p.epoch.Add(1)
}

// Submit queues job on the given lane.
//
//   - PriorityHigh: blocks briefly if the lane is full (live-message work is
//     never shed).
//   - PriorityLow: never blocks; when the lane is full the OLDEST queued low
//     job is shed — cancelled via Run(stale=true) — to make room, because
//     the newest speculation tends to be the best-informed one.
//
// Returns false only when the pool is closed (the job is then cancelled via
// Run(stale=true) on the caller's goroutine).
func (p *Pool) Submit(prio Priority, job Job) bool {
	if job.Run == nil {
		return false
	}
	select {
	case <-p.stop:
		job.Run(true)
		return false
	default:
	}

	if prio == PriorityHigh {
		select {
		case p.high <- job:
			return true
		case <-p.stop:
			job.Run(true)
			return false
		}
	}

	for {
		select {
		case p.low <- job:
			return true
		case <-p.stop:
			job.Run(true)
			return false
		default:
		}
		// Lane full: shed the oldest queued low job and retry. The shed
		// job's Run(stale=true) executes here on the producer, which must
		// therefore stay cheap (manager cancellations are flag flips).
		select {
		case victim := <-p.low:
			p.shed.Add(1)
			p.runJob(victim, true)
		default:
			// A worker drained the lane in the meantime; loop and enqueue.
		}
	}
}

// worker drains high before low, then waits on all lanes.
func (p *Pool) worker() {
	defer p.workersWG.Done()
	for {
		// Priority pass: exhaust high first.
		select {
		case job := <-p.high:
			p.dispatch(job)
			continue
		default:
		}
		select {
		case job := <-p.high:
			p.dispatch(job)
		case job := <-p.low:
			p.dispatch(job)
		case <-p.stop:
			return
		}
	}
}

// dispatch applies epoch staleness and runs the job.
func (p *Pool) dispatch(job Job) {
	stale := job.Epoch != EpochAny && job.Epoch != p.epoch.Load()
	p.runJob(job, stale)
}

func (p *Pool) runJob(job Job, stale bool) {
	if stale {
		p.stale.Add(1)
	} else {
		p.executed.Add(1)
	}
	job.Run(stale)
}

// Close stops the workers and cancels (Run(stale=true)) every job still
// queued, so no caller is left waiting. Safe to call multiple times.
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		close(p.stop)
		p.workersWG.Wait()
		for {
			select {
			case job := <-p.high:
				p.runJob(job, true)
			case job := <-p.low:
				p.runJob(job, true)
			default:
				return
			}
		}
	})
}

// PoolStats is a point-in-time counter snapshot.
type PoolStats struct {
	Executed int64 // jobs run fresh
	Stale    int64 // jobs cancelled (epoch bump, shed, shutdown)
	Shed     int64 // low-priority jobs shed by backpressure (subset of Stale)
}

// Stats snapshots the pool's counters.
func (p *Pool) Stats() PoolStats {
	return PoolStats{
		Executed: p.executed.Load(),
		Stale:    p.stale.Load(),
		Shed:     p.shed.Load(),
	}
}
