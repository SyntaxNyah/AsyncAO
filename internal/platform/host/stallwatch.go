package host

import (
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// sleepGapTicks is how many check periods a tick may come late before it means the machine slept, not that a frame
// stalled: the checking goroutine itself never stalls with the frame.
const sleepGapTicks = 4

// StallWatch notices a frame stuck inside the graphics driver: a call that never returns, which no error path sees.
// It reports a stall once, from its own goroutine, since the main thread is the one stuck.
type StallWatch struct {
	limit   time.Duration
	every   time.Duration
	now     func() time.Time
	base    time.Time
	onStall func(stuck time.Duration)
	// frameStart is when the open frame began, as nanoseconds after base plus one; 0 while no frame is open.
	frameStart atomic.Int64
	lastCheck  time.Time // the goroutine's own; never read elsewhere
	done       chan struct{}
	closeOnce  sync.Once
}

// NewStallWatch checks every period whether a frame has been open longer than limit, and calls onStall the first time.
func NewStallWatch(limit, every time.Duration, now func() time.Time, onStall func(stuck time.Duration)) *StallWatch {
	start := now()
	w := &StallWatch{limit: limit, every: every, now: now, base: start, onStall: onStall, lastCheck: start, done: make(chan struct{})}
	go w.run()
	return w
}

// FrameStart marks the main thread entering the graphics driver for a frame.
func (w *StallWatch) FrameStart() { w.frameStart.Store(w.sinceBase(w.now())) }

// FrameDone marks the frame finished.
func (w *StallWatch) FrameDone() { w.frameStart.Store(0) }

// Close stops the checking goroutine.
func (w *StallWatch) Close() { w.closeOnce.Do(func() { close(w.done) }) }

func (w *StallWatch) sinceBase(t time.Time) int64 { return int64(t.Sub(w.base)) + 1 }

func (w *StallWatch) run() {
	ticker := time.NewTicker(w.every)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
		}
		if stuck, stalled := w.check(w.now()); stalled {
			log.Printf("gpu: a frame has been stuck in the graphics driver for %s", stuck.Round(time.Millisecond))
			w.onStall(stuck)
			return
		}
	}
}

// check reports how long the open frame has been stuck once that passes the limit. After a sleep the open frame
// counts from now, since the clock moved while nothing ran.
func (w *StallWatch) check(now time.Time) (time.Duration, bool) {
	slept := now.Sub(w.lastCheck) > w.every*sleepGapTicks
	w.lastCheck = now
	started := w.frameStart.Load()
	if started == 0 {
		return 0, false
	}
	if slept {
		w.frameStart.CompareAndSwap(started, w.sinceBase(now))
		return 0, false
	}
	stuck := time.Duration(w.sinceBase(now) - started)
	return stuck, stuck >= w.limit
}
