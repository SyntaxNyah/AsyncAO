package media

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// DefaultMaxAssets caps the Assets the shared map holds.
const DefaultMaxAssets = 32768

// Config is everything a Manager is built from.
type Config struct {
	Chains  map[Place]*Chain
	Formats map[Kind]Format
	// Candidates lists the exact files a key may be, best first; it runs on the Manager's goroutine for each search.
	Candidates func(Key) []File
	Compute    *Pool
	// KeepBytes stores fetched bytes so an evicted result can be rebuilt without I/O; nil keeps nothing.
	KeepBytes func(File, []byte)
	// ClaimLimit is the memory decodes may claim at once; Now work may pass it, so a message can't wait on memory.
	ClaimLimit int64
	MaxAssets  int
	// IsMainThread lets Load refuse to block the main thread.
	IsMainThread func() bool
}

type step uint8

const (
	stepData step = iota
	stepMeasure
	stepDecode
)

// request is the Manager's side of an Asset, like a QPromise; only the Manager's goroutine touches it.
type request struct {
	asset   *Asset
	urgency Urgency
	skip    map[tried]bool
	step    step
	entry   int
	file    File
	host    string
	bytes   []byte
	claim   int64
	result  any
	err     error
	next    *request
}

// pipe is a lock-free list many threads push to and one thread takes whole, newest first.
type pipe struct{ head atomic.Pointer[request] }

func (p *pipe) push(r *request) {
	for {
		h := p.head.Load()
		r.next = h
		if p.head.CompareAndSwap(h, r) {
			return
		}
	}
}

// take returns everything pushed so far in the order it was pushed.
func (p *pipe) take() *request {
	var ordered *request
	for r := p.head.Swap(nil); r != nil; {
		next := r.next
		r.next = ordered
		ordered = r
		r = next
	}
	return ordered
}

// waiting holds requests with no free worker yet, in arrival order within each urgency.
type waiting struct{ now, later []*request }

func (w *waiting) push(r *request) {
	if r.urgency == Now {
		w.now = append(w.now, r)
	} else {
		w.later = append(w.later, r)
	}
}

func (w *waiting) peek() *request {
	if len(w.now) > 0 {
		return w.now[0]
	}
	if len(w.later) > 0 {
		return w.later[0]
	}
	return nil
}

func (w *waiting) pop() {
	if len(w.now) > 0 {
		w.now = w.now[1:]
		return
	}
	w.later = w.later[1:]
}

// poolQueue keeps one waiting list per host, so a hung host's work can't block another host's.
type poolQueue struct {
	pool   *Pool
	groups map[string]*waiting
	order  []string
}

func (q *poolQueue) push(r *request) {
	w := q.groups[r.host]
	if w == nil {
		w = &waiting{}
		q.groups[r.host] = w
		q.order = append(q.order, r.host)
	}
	w.push(r)
}

// Manager owns every table on one goroutine, like a QObject on a QThread; only Get and the calls beside it are safe from other threads.
type Manager struct {
	cfg    Config
	assets sync.Map // Key to *Asset
	count  atomic.Int64

	incoming pipe
	finished pipe
	results  pipe
	freed    atomic.Int64
	wake     chan struct{}
	parked   atomic.Bool
	stop     chan struct{}
	stopped  chan struct{}
	closing  sync.Once

	queues   map[*Pool]*poolQueue
	queueSeq []*poolQueue
	hostBusy map[string]int
	claimed  int64
}

// NewManager starts a Manager's goroutine.
func NewManager(cfg Config) *Manager {
	if cfg.MaxAssets <= 0 {
		cfg.MaxAssets = DefaultMaxAssets
	}
	m := &Manager{cfg: cfg, wake: make(chan struct{}, 1), stop: make(chan struct{}), stopped: make(chan struct{}),
		queues: map[*Pool]*poolQueue{}, hostBusy: map[string]int{}}
	go m.run()
	return m
}

// Close stops the Manager's goroutine; pools are the caller's to close.
func (m *Manager) Close() {
	m.closing.Do(func() { close(m.stop) })
	<-m.stopped
}

// Get returns the Asset for key from any thread and never blocks: a repeat ask takes no lock and posts nothing.
func (m *Manager) Get(key Key, urgency Urgency) *Asset {
	if v, ok := m.assets.Load(key); ok {
		return v.(*Asset)
	}
	// Later work is refused at the cap, unstored, so the caller's next ask tries again.
	if urgency == Later && m.count.Load() >= int64(m.cfg.MaxAssets) {
		a := newAsset(key)
		a.finish(Refused, nil, ErrRefused)
		return a
	}
	a := newAsset(key)
	if v, loaded := m.assets.LoadOrStore(key, a); loaded {
		return v.(*Asset)
	}
	m.count.Add(1)
	m.incoming.push(&request{asset: a, urgency: urgency})
	m.signal()
	return a
}

// Load waits for key on a worker thread; it panics on the main thread, which must never wait.
func (m *Manager) Load(ctx context.Context, key Key, urgency Urgency) (any, error) {
	if m.cfg.IsMainThread != nil && m.cfg.IsMainThread() {
		panic("media: Load called on the main thread")
	}
	a := m.Get(key, urgency)
	select {
	case <-a.done:
		return a.Result(), a.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Forget drops a finished Asset, such as one whose texture was evicted, so the next Get asks again.
func (m *Manager) Forget(key Key) {
	if _, ok := m.assets.LoadAndDelete(key); ok {
		m.count.Add(-1)
	}
}

// TakeResults hands the main thread every result that needs it, once per frame, with one atomic swap.
func (m *Manager) TakeResults(use func(*Asset, any)) {
	for r := m.results.take(); r != nil; r = r.next {
		use(r.asset, r.result)
	}
}

// Finish marks an Asset from TakeResults ready and frees its memory claim, after the main thread has used it.
func (m *Manager) Finish(a *Asset, result any) {
	if c := a.claim.Swap(0); c != 0 {
		m.freed.Add(c)
		m.signal()
	}
	a.finish(Ready, result, nil)
}

func (m *Manager) signal() {
	if m.parked.Load() {
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) pending() bool {
	return m.incoming.head.Load() != nil || m.finished.head.Load() != nil || m.freed.Load() != 0
}

func (m *Manager) run() {
	defer close(m.stopped)
	for {
		for r := m.incoming.take(); r != nil; {
			next := r.next
			m.search(r)
			r = next
		}
		for r := m.finished.take(); r != nil; {
			next := r.next
			m.handle(r)
			r = next
		}
		if f := m.freed.Swap(0); f != 0 {
			m.claimed -= f
		}
		m.dispatch()
		if m.pending() {
			continue
		}
		m.parked.Store(true)
		// Checked again after parking, or a post made in between would sleep until the next one.
		if m.pending() {
			m.parked.Store(false)
			continue
		}
		select {
		case <-m.wake:
		case <-m.stop:
			return
		}
		m.parked.Store(false)
	}
}

// search finds the next place to get the bytes from, or ends the request.
func (m *Manager) search(r *request) {
	chain := m.cfg.Chains[r.asset.key.Place]
	if chain == nil {
		m.fail(r, Missing, ErrMissing)
		return
	}
	i, file, found := chain.find(m.cfg.Candidates(r.asset.key), r.skip)
	if !found {
		m.fail(r, Missing, ErrMissing)
		return
	}
	e := chain.entries[i]
	r.entry, r.file, r.step, r.host = i, file, stepData, ""
	if e.Pool == nil {
		data, err := e.Source.Data(file)
		r.bytes, r.err = data, err
		m.dataDone(r)
		return
	}
	if l, ok := e.Source.(HostLimiter); ok {
		r.host = l.Host(file)
	}
	m.queue(e.Pool, r)
}

func (m *Manager) queue(p *Pool, r *request) {
	q := m.queues[p]
	if q == nil {
		q = &poolQueue{pool: p, groups: map[string]*waiting{}}
		m.queues[p] = q
		m.queueSeq = append(m.queueSeq, q)
	}
	q.push(r)
}

func (m *Manager) fail(r *request, state AssetState, err error) {
	if c := r.asset.claim.Swap(0); c != 0 {
		m.claimed -= c
	}
	r.asset.finish(state, nil, err)
}

// handle takes one finished step on the Manager's goroutine.
func (m *Manager) handle(r *request) {
	switch r.step {
	case stepData:
		e := m.cfg.Chains[r.asset.key.Place].entries[r.entry]
		e.Pool.busy--
		if r.host != "" {
			m.hostBusy[r.host]--
		}
		m.dataDone(r)
	case stepMeasure:
		m.cfg.Compute.busy--
		r.step = stepDecode
		m.queue(m.cfg.Compute, r)
	case stepDecode:
		m.cfg.Compute.busy--
		m.decodeDone(r)
	}
}

func (m *Manager) dataDone(r *request) {
	e := m.cfg.Chains[r.asset.key.Place].entries[r.entry]
	if o, ok := e.Source.(Observer); ok {
		o.Observe(r.file, r.bytes, r.err)
	}
	if r.err != nil {
		m.retry(r)
		return
	}
	if m.cfg.KeepBytes != nil && e.Pool != nil {
		m.cfg.KeepBytes(r.file, r.bytes)
	}
	format := m.cfg.Formats[r.asset.key.Kind]
	if format == nil {
		r.asset.finish(Ready, r.bytes, nil)
		return
	}
	r.asset.state.Store(int32(Decoding))
	r.host = ""
	r.step = stepDecode
	if _, ok := format.(Measurer); ok {
		r.step = stepMeasure
	}
	m.queue(m.cfg.Compute, r)
}

func (m *Manager) decodeDone(r *request) {
	if r.err != nil {
		// A failure belongs to this source's copy, so the search carries on past it.
		if c := r.asset.claim.Swap(0); c != 0 {
			m.claimed -= c
		}
		m.retry(r)
		return
	}
	if m.cfg.Formats[r.asset.key.Kind].OnMainThread() {
		m.results.push(r)
		return
	}
	if c := r.asset.claim.Swap(0); c != 0 {
		m.claimed -= c
	}
	r.asset.finish(Ready, r.result, nil)
}

// retry skips the copy that just failed and searches again.
func (m *Manager) retry(r *request) {
	if r.skip == nil {
		r.skip = map[tried]bool{}
	}
	// A file its source now answers No for needs no mark, and a marked one can't be retried by this request.
	r.skip[tried{r.entry, r.file}] = true
	r.bytes, r.err = nil, nil
	r.asset.state.Store(int32(Searching))
	m.search(r)
}

func (m *Manager) dispatch() {
	for _, q := range m.queueSeq {
		m.dispatchUrgency(q, Now)
		m.dispatchUrgency(q, Later)
	}
}

func (m *Manager) dispatchUrgency(q *poolQueue, u Urgency) {
	for _, host := range q.order {
		w := q.groups[host]
		for q.pool.canStart(u) {
			r := w.peek()
			if r == nil || (u == Later && r.urgency == Now) || (u == Now && r.urgency != Now) || !m.allowed(q.pool, r) {
				break
			}
			w.pop()
			m.start(q.pool, r)
		}
	}
}

// allowed checks the limits that apply beyond a free worker: a host's limit and the memory claim.
func (m *Manager) allowed(p *Pool, r *request) bool {
	if r.host != "" {
		l := m.cfg.Chains[r.asset.key.Place].entries[r.entry].Source.(HostLimiter)
		if m.hostBusy[r.host] >= l.Limit(r.host) {
			return false
		}
	}
	if r.step == stepDecode && r.urgency != Now && m.claimed > 0 && m.cfg.ClaimLimit > 0 && m.claimed+r.claim > m.cfg.ClaimLimit {
		return false
	}
	return p != nil
}

func (m *Manager) start(p *Pool, r *request) {
	p.busy++
	switch r.step {
	case stepData:
		if r.host != "" {
			m.hostBusy[r.host]++
		}
		source := m.cfg.Chains[r.asset.key.Place].entries[r.entry].Source
		p.jobs <- func() {
			r.bytes, r.err = source.Data(r.file)
			m.report(r)
		}
	case stepMeasure:
		measurer := m.cfg.Formats[r.asset.key.Kind].(Measurer)
		p.jobs <- func() {
			r.claim = measurer.Measure(r.bytes)
			m.report(r)
		}
	case stepDecode:
		m.claimed += r.claim
		r.asset.claim.Store(r.claim)
		format := m.cfg.Formats[r.asset.key.Kind]
		p.jobs <- func() {
			r.result, r.err = format.Decode(r.asset.key, r.bytes)
			m.report(r)
		}
	}
}

func (m *Manager) report(r *request) {
	m.finished.push(r)
	m.signal()
}

// IsMissing reports whether err means no source has the file.
func IsMissing(err error) bool { return errors.Is(err, ErrMissing) }
