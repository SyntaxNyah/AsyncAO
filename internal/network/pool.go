package network

import (
	"sync"
	"sync/atomic"
)

const (
	// DefaultWorkers: fetches are RTT-bound, not CPU-bound, and the transport allows 16 conns per host.
	DefaultWorkers = 16
	// highLaneCap and lowLaneCap bound the lanes: low sheds its oldest job when full, high briefly blocks the
	// producer; results are never dropped or stolen (hard rule 7).
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

// Job is one unit of pool work; Run is invoked exactly once per accepted job, with stale=true if cancelled.
type Job struct {
	// ID exists for tracing/metrics. Allocate via Pool.NextID.
	ID int64
	// Epoch is Pool.Epoch at queue time, or EpochAny to never go stale.
	Epoch int64
	// Run does the work and must be non-nil.
	Run func(stale bool)
}

// Pool is the fetch worker pool; bumping the epoch makes queued jobs from earlier epochs get Run(stale=true).
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

// NextID returns a unique job ID; an atomic counter can't collide, unlike random IDs.
func (p *Pool) NextID() int64 {
	return p.nextID.Add(1)
}

// Epoch returns the current pool epoch. Capture it when building a Job.
func (p *Pool) Epoch() int64 {
	return p.epoch.Load()
}

// BumpEpoch invalidates queued jobs from earlier epochs; in-flight jobs finish normally.
func (p *Pool) BumpEpoch() int64 {
	return p.epoch.Add(1)
}

// Submit: high priority blocks briefly when full (live-message work is never shed); low never blocks and
// sheds the oldest low job, as the newest speculation is the best informed.
// It returns false only when the pool is closed, with Run(stale=true) called on the caller's goroutine.
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
		// The shed job's Run(stale=true) runs here on the producer, so it must stay cheap.
		select {
		case victim := <-p.low:
			p.shed.Add(1)
			p.runJob(victim, true)
		default:
			// A worker drained the lane; loop and enqueue.
		}
	}
}

func (p *Pool) worker() {
	defer p.workersWG.Done()
	for {
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

// Close stops the workers and cancels every queued job, so no caller is left waiting; safe to call more than once.
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
