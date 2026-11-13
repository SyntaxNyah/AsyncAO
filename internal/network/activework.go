package network

import (
	"sync/atomic"
	"time"
)

// WorkItem names one job a worker is running, for diagnostics.
type WorkItem struct {
	Kind  string
	Name  string
	Since time.Time
}

// ActiveWork lists what a fixed set of workers is running; lock-free, so diagnostics never hold a worker up.
type ActiveWork struct {
	items []atomic.Pointer[WorkItem]
}

// NewActiveWork tracks up to workers jobs at once; a job past that simply goes unlisted.
func NewActiveWork(workers int) *ActiveWork {
	return &ActiveWork{items: make([]atomic.Pointer[WorkItem], workers)}
}

// Begin lists a job and returns the handle to pass to End; -1 means it was not listed.
func (w *ActiveWork) Begin(kind, name string) int {
	item := &WorkItem{Kind: kind, Name: name, Since: time.Now()}
	for i := range w.items {
		if w.items[i].CompareAndSwap(nil, item) {
			return i
		}
	}
	return -1
}

// End removes a job listed by Begin.
func (w *ActiveWork) End(handle int) {
	if handle >= 0 {
		w.items[handle].Store(nil)
	}
}

// Snapshot returns the jobs running now, in no particular order.
func (w *ActiveWork) Snapshot() []WorkItem {
	out := make([]WorkItem, 0, len(w.items))
	for i := range w.items {
		if item := w.items[i].Load(); item != nil {
			out = append(out, *item)
		}
	}
	return out
}
