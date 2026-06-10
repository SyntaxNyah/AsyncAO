// Package metrics implements the cold-load report and the 1 Hz runtime
// sampler (spec §15): cache hit rates, heap size via runtime/metrics,
// and GC pause p99 — feeding the debug HUD and --debug logging.
package metrics

import (
	"fmt"
	"runtime/metrics"
	"sync/atomic"
	"time"
)

const (
	// SampleInterval is the sampler cadence.
	SampleInterval = 1 * time.Second

	heapBytesMetric = "/memory/classes/heap/objects:bytes"
	gcPausesMetric  = "/sched/pauses/total/gc:seconds"

	p99 = 0.99
)

// StatsSource pulls counters from the engine; any field may be nil.
type StatsSource struct {
	CacheHits   func() (hits, misses int64)
	NetRequests func() (requests, cached404s int64)
	LearnedHits func() (hits, misses int64)
}

// Sample is one 1 Hz snapshot.
type Sample struct {
	When         time.Time
	HeapBytes    uint64
	GCPauseP99   time.Duration
	CacheHitRate float64 // 0..1; NaN-free (0 when no traffic)
	Probes       int64
	Cached404s   int64
}

// Profiler runs the sampler goroutine and formats reports.
type Profiler struct {
	source StatsSource

	latest  atomic.Pointer[Sample]
	stop    chan struct{}
	done    chan struct{}
	started atomic.Bool
}

// NewProfiler builds a profiler over the given sources.
func NewProfiler(source StatsSource) *Profiler {
	return &Profiler{
		source: source,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Start launches the 1 Hz sampler goroutine (idempotent).
func (p *Profiler) Start() {
	if !p.started.CompareAndSwap(false, true) {
		return
	}
	go p.loop()
}

// Stop halts the sampler.
func (p *Profiler) Stop() {
	if !p.started.Load() {
		return
	}
	close(p.stop)
	<-p.done
}

// Latest returns the most recent sample (nil before the first tick).
func (p *Profiler) Latest() *Sample {
	return p.latest.Load()
}

func (p *Profiler) loop() {
	defer close(p.done)
	ticker := time.NewTicker(SampleInterval)
	defer ticker.Stop()
	samples := []metrics.Sample{
		{Name: heapBytesMetric},
		{Name: gcPausesMetric},
	}
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			metrics.Read(samples)
			s := &Sample{When: time.Now()}
			if samples[0].Value.Kind() == metrics.KindUint64 {
				s.HeapBytes = samples[0].Value.Uint64()
			}
			if samples[1].Value.Kind() == metrics.KindFloat64Histogram {
				s.GCPauseP99 = histogramQuantile(samples[1].Value.Float64Histogram(), p99)
			}
			if p.source.CacheHits != nil {
				hits, misses := p.source.CacheHits()
				if total := hits + misses; total > 0 {
					s.CacheHitRate = float64(hits) / float64(total)
				}
			}
			if p.source.NetRequests != nil {
				s.Probes, s.Cached404s = p.source.NetRequests()
			}
			p.latest.Store(s)
		}
	}
}

// histogramQuantile extracts a quantile from a runtime/metrics histogram.
func histogramQuantile(h *metrics.Float64Histogram, q float64) time.Duration {
	if h == nil || len(h.Counts) == 0 {
		return 0
	}
	var total uint64
	for _, c := range h.Counts {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := uint64(float64(total) * q)
	var cumulative uint64
	for i, c := range h.Counts {
		cumulative += c
		if cumulative >= target {
			// Bucket i spans Buckets[i]..Buckets[i+1] (seconds).
			upper := h.Buckets[i+1]
			return time.Duration(upper * float64(time.Second))
		}
	}
	return time.Duration(h.Buckets[len(h.Buckets)-1] * float64(time.Second))
}

// ColdLoad tracks the first load burst after connect for the §15 report
// line: "Cold load: 87 ms, 212 probes, 3 misses".
type ColdLoad struct {
	start   time.Time
	probes  atomic.Int64
	misses  atomic.Int64
	stopped atomic.Bool
	took    atomic.Int64 // nanoseconds
}

// NewColdLoad starts timing now.
func NewColdLoad() *ColdLoad {
	return &ColdLoad{start: time.Now()}
}

// AddProbe counts one network probe (call from fetch instrumentation).
func (c *ColdLoad) AddProbe() {
	if !c.stopped.Load() {
		c.probes.Add(1)
	}
}

// AddMiss counts one all-formats-missing asset.
func (c *ColdLoad) AddMiss() {
	if !c.stopped.Load() {
		c.misses.Add(1)
	}
}

// Finish freezes the measurement (first fully-loaded frame).
func (c *ColdLoad) Finish() {
	if c.stopped.CompareAndSwap(false, true) {
		c.took.Store(int64(time.Since(c.start)))
	}
}

// Report renders the §15 line.
func (c *ColdLoad) Report() string {
	took := time.Duration(c.took.Load())
	if !c.stopped.Load() {
		took = time.Since(c.start)
	}
	return fmt.Sprintf("Cold load: %d ms, %d probes, %d misses",
		took.Milliseconds(), c.probes.Load(), c.misses.Load())
}
