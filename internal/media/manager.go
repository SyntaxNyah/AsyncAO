package media

import (
	"context"
	"errors"
	"maps"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultMaxAssets caps the Assets the shared map holds.
	DefaultMaxAssets = 32768
	// DefaultMissingTTL is how long a missing answer lasts, so a file a server owner adds gets noticed.
	DefaultMissingTTL = 60 * time.Second
	// winnersCap bounds the table of files keys were found as; a full table starts over.
	winnersCap = 16384
)

// Config is everything a Manager is built from.
type Config struct {
	Chains  map[Place]*Chain
	Formats map[Kind]Format
	// Candidates lists the exact files one name of a key may be, best first; it runs on the Manager's goroutine.
	Candidates func(Key, string) []File
	Compute    *Pool
	// KeepBytes stores fetched bytes so an evicted result can be rebuilt without I/O; nil keeps nothing.
	KeepBytes func(File, []byte)
	// Found runs on the Manager's goroutine when a key's file turns up, so later searches can try that format first.
	Found func(Key, File)
	// Finished runs on the Manager's goroutine when a request ends, and must not block.
	Finished func(*Asset)
	// ClaimLimit is the memory decodes may claim at once; Now work may pass it, so a message can't wait on memory.
	ClaimLimit int64
	MaxAssets  int
	MissingTTL time.Duration
	// IsMainThread lets Load refuse to block the main thread.
	IsMainThread func() bool
	// Clock defaults to time.Now.
	Clock func() time.Time
}

type step uint8

const (
	stepData step = iota
	stepMeasure
	stepDecode
	stepWork
)

type eventKind uint8

const (
	eventNew eventKind = iota
	eventRaise
	eventWithdraw
	eventWithdrawIf
	eventWork
	eventDo
)

// request is the Manager's side of an Asset, like a QPromise; only the Manager's goroutine touches it after posting.
type request struct {
	event   eventKind
	key     Key
	keep    func(*Asset) bool
	asset   *Asset
	urgency Urgency
	// The search only moves forward: names holds each name's files, the file found last time first; name is the next
	// one to list; left is the current name's copies not yet asked; failed means one of them failed rather than being
	// absent, so the file may exist and no later name may count.
	names   [][]File
	name    int
	left    []location
	failed  bool
	step    step
	entry   int
	file    File
	host    string
	bytes   []byte
	claim   int64
	result  any
	err     error
	lastErr error
	queued  bool
	dead    bool
	pool    *Pool
	work    func() any
	done    func(any)
	next    *request
}

// pipe is a lock-free list many threads push to and one thread takes whole.
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

// peek returns the next live request, dropping ones withdrawn or raised to the other list since they queued.
func (w *waiting) peek() *request {
	for len(w.now) > 0 {
		if !w.now[0].dead {
			return w.now[0]
		}
		w.now = w.now[1:]
	}
	for len(w.later) > 0 {
		if r := w.later[0]; !r.dead && r.urgency == Later {
			return r
		}
		w.later = w.later[1:]
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

func (q *poolQueue) list(host string) *waiting {
	w := q.groups[host]
	if w == nil {
		w = &waiting{}
		q.groups[host] = w
		q.order = append(q.order, host)
	}
	return w
}

// Manager owns every table on one goroutine, like a QObject on a QThread; only the exported methods are safe from other threads.
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

	active   map[Key]*request
	queues   map[*Pool]*poolQueue
	queueSeq []*poolQueue
	hostBusy map[string]int
	claimed  int64
	// winners maps a key to the file it was last found as, tried first next time, as AO2's asset_lookup_cache is.
	winners map[Key]File
}

// NewManager starts a Manager's goroutine.
func NewManager(cfg Config) *Manager {
	if cfg.MaxAssets <= 0 {
		cfg.MaxAssets = DefaultMaxAssets
	}
	if cfg.MissingTTL <= 0 {
		cfg.MissingTTL = DefaultMissingTTL
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	m := &Manager{cfg: cfg, wake: make(chan struct{}, 1), stop: make(chan struct{}), stopped: make(chan struct{}),
		active: map[Key]*request{}, queues: map[*Pool]*poolQueue{}, hostBusy: map[string]int{}, winners: map[Key]File{}}
	go m.run()
	return m
}

// Close stops the Manager's goroutine; pools are the caller's to close.
func (m *Manager) Close() {
	m.closing.Do(func() { close(m.stop) })
	<-m.stopped
}

// Get returns the Asset for key from any thread and never blocks: a repeat ask takes no lock and posts nothing.
// alts are the other names it may be found under, best first, each asked only once every earlier one is known absent.
func (m *Manager) Get(key Key, urgency Urgency, flags Flags, alts ...string) *Asset {
	if v, ok := m.assets.Load(key); ok {
		a := v.(*Asset)
		if !m.expired(a) && !a.lacks(alts) {
			a.add(flags)
			if urgency == Now && !a.urgent.Load() && a.urgent.CompareAndSwap(false, true) {
				m.post(&request{event: eventRaise, key: key})
			}
			return a
		}
		fresh := newAsset(key, urgency, flags, alts)
		if m.assets.CompareAndSwap(key, a, fresh) {
			m.post(&request{event: eventNew, asset: fresh, urgency: urgency})
			return fresh
		}
		return m.Get(key, urgency, flags, alts...)
	}
	// Later work is refused at the cap, unstored, so the caller's next ask tries again.
	if urgency == Later && m.count.Load() >= int64(m.cfg.MaxAssets) {
		a := newAsset(key, urgency, flags, alts)
		a.finish(Refused, nil, ErrRefused, m.cfg.Clock())
		return a
	}
	a := newAsset(key, urgency, flags, alts)
	if v, loaded := m.assets.LoadOrStore(key, a); loaded {
		v.(*Asset).add(flags)
		return v.(*Asset)
	}
	m.count.Add(1)
	m.post(&request{event: eventNew, asset: a, urgency: urgency})
	return a
}

func (m *Manager) expired(a *Asset) bool {
	return a.State() == Missing && m.cfg.Clock().Sub(time.Unix(0, a.finished.Load())) >= m.cfg.MissingTTL
}

// Load waits for key on a worker thread; it panics on the main thread, which must never wait.
func (m *Manager) Load(ctx context.Context, key Key, urgency Urgency) (any, error) {
	if m.cfg.IsMainThread != nil && m.cfg.IsMainThread() {
		panic("media: Load called on the main thread")
	}
	a := m.Get(key, urgency, 0)
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

// Withdraw says nobody wants key any more: work not yet started never starts, and a running step's result is dropped.
func (m *Manager) Withdraw(key Key) {
	m.Forget(key)
	m.post(&request{event: eventWithdraw, key: key})
}

// WithdrawOrigin withdraws everything asked for from one server, for when a tab disconnects.
func (m *Manager) WithdrawOrigin(origin string) {
	m.WithdrawIf(func(a *Asset) bool { return a.key.Origin == origin })
}

// WithdrawIf withdraws every request whose Asset matches; match also runs on the Manager's goroutine.
func (m *Manager) WithdrawIf(match func(*Asset) bool) {
	m.ForgetIf(match)
	m.post(&request{event: eventWithdrawIf, keep: match})
}

// ForgetWinners drops the remembered files that match, such as ones on a host no longer asked; call it inside Do.
func (m *Manager) ForgetWinners(match func(Key, File) bool) {
	maps.DeleteFunc(m.winners, match)
}

// ForgetIf drops every Asset that matches, so the next Get asks again.
func (m *Manager) ForgetIf(match func(*Asset) bool) {
	m.assets.Range(func(_, v any) bool {
		if a := v.(*Asset); match(a) {
			m.ForgetAsset(a)
		}
		return true
	})
}

// ForgetAsset drops this exact Asset, leaving a newer one for the same key alone.
func (m *Manager) ForgetAsset(a *Asset) {
	if m.assets.CompareAndDelete(a.key, a) {
		m.count.Add(-1)
	}
}

// Peek returns the Asset for key without asking for anything, or nil if there is none or its answer has expired.
func (m *Manager) Peek(key Key) *Asset {
	v, ok := m.assets.Load(key)
	if !ok {
		return nil
	}
	a := v.(*Asset)
	if m.expired(a) {
		return nil
	}
	return a
}

// Do runs fn on the Manager's goroutine, for touching a source's tables; fn must not block.
func (m *Manager) Do(fn func()) {
	m.post(&request{event: eventDo, work: func() any { fn(); return nil }})
}

// Run does work on pool, then done on the Manager's goroutine with its result, for jobs that aren't one file.
func (m *Manager) Run(pool *Pool, urgency Urgency, work func() any, done func(any)) {
	m.post(&request{event: eventWork, pool: pool, urgency: urgency, work: work, done: done, step: stepWork})
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
	a.finish(Ready, result, nil, m.cfg.Clock())
}

func (m *Manager) post(r *request) {
	m.incoming.push(r)
	m.signal()
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
			m.event(r)
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

func (m *Manager) event(r *request) {
	switch r.event {
	case eventNew:
		m.active[r.asset.key] = r
		m.search(r)
	case eventRaise:
		if a := m.active[r.key]; a != nil && a.urgency == Later {
			a.urgency = Now
			if a.queued {
				m.queueList(a).now = append(m.queueList(a).now, a)
			}
		}
	case eventWithdraw:
		if a := m.active[r.key]; a != nil {
			m.kill(a)
		}
	case eventWithdrawIf:
		for _, a := range m.active {
			if r.keep(a.asset) {
				m.kill(a)
			}
		}
	case eventWork:
		m.queue(r.pool, r)
	case eventDo:
		r.work()
	}
}

// kill ends a request now if it is only waiting; one on a worker ends when the worker reports.
func (m *Manager) kill(r *request) {
	r.dead = true
	if r.queued {
		m.end(r, Withdrawn, nil, ErrWithdrawn)
	}
}

// end finishes a request and takes it off the tables; a failed one leaves the map, so the next ask tries again.
func (m *Manager) end(r *request, state AssetState, result any, err error) {
	if m.active[r.asset.key] == r {
		delete(m.active, r.asset.key)
	}
	if c := r.asset.claim.Swap(0); c != 0 {
		m.claimed -= c
	}
	if state == Failed && m.assets.CompareAndDelete(r.asset.key, r.asset) {
		m.count.Add(-1)
	}
	r.asset.finish(state, result, err, m.cfg.Clock())
	if m.cfg.Finished != nil {
		m.cfg.Finished(r.asset)
	}
}

// search asks the next copy of the current name, moving to the next name only once every copy is known absent, as AO2's
// get_image_path does (path_functions.cpp:231); it ends the request when no name is left.
func (m *Manager) search(r *request) {
	chain := m.cfg.Chains[r.asset.key.Place]
	if chain == nil {
		m.end(r, Missing, nil, ErrMissing)
		return
	}
	if r.names == nil {
		r.names = m.namesOf(r.asset)
	}
	for len(r.left) == 0 {
		if r.failed {
			m.end(r, Failed, nil, r.lastErr)
			return
		}
		if r.name == len(r.names) {
			delete(m.winners, r.asset.key)
			m.end(r, Missing, nil, ErrMissing)
			return
		}
		r.left = chain.locations(r.names[r.name])
		r.name++
	}
	at := r.left[0]
	r.left = r.left[1:]
	e := chain.entries[at.entry]
	r.entry, r.file, r.step, r.host = at.entry, at.file, stepData, ""
	if e.Pool == nil {
		r.bytes, r.err = e.Source.Data(at.file)
		m.dataDone(r)
		return
	}
	if l, ok := e.Source.(HostLimiter); ok {
		r.host = l.Host(at.file)
	}
	r.pool = e.Pool
	m.queue(e.Pool, r)
}

// namesOf lists each name's files in order, after the file the key was found as last time.
func (m *Manager) namesOf(a *Asset) [][]File {
	names := make([][]File, 0, len(a.names)+1)
	if w, ok := m.winners[a.key]; ok {
		names = append(names, []File{w})
	}
	for _, name := range a.names {
		names = append(names, m.cfg.Candidates(a.key, name))
	}
	return names
}

func (m *Manager) queue(p *Pool, r *request) {
	r.queued = true
	r.pool = p
	m.queueList(r).push(r)
}

func (m *Manager) queueList(r *request) *waiting {
	q := m.queues[r.pool]
	if q == nil {
		q = &poolQueue{pool: r.pool, groups: map[string]*waiting{}}
		m.queues[r.pool] = q
		m.queueSeq = append(m.queueSeq, q)
	}
	return q.list(r.host)
}

// handle takes one finished step on the Manager's goroutine.
func (m *Manager) handle(r *request) {
	r.pool.busy--
	if r.step == stepWork {
		r.done(r.result)
		return
	}
	if r.host != "" {
		m.hostBusy[r.host]--
	}
	if r.step == stepData {
		// A dead request's answer still updates the tables, as it is true whoever asked.
		m.observe(r)
	}
	if r.dead {
		m.end(r, Withdrawn, nil, ErrWithdrawn)
		return
	}
	switch r.step {
	case stepData:
		m.afterData(r)
	case stepMeasure:
		r.step = stepDecode
		m.queue(m.cfg.Compute, r)
	case stepDecode:
		m.decodeDone(r)
	}
}

func (m *Manager) observe(r *request) {
	e := m.cfg.Chains[r.asset.key.Place].entries[r.entry]
	if o, ok := e.Source.(Observer); ok {
		o.Observe(r.file, r.bytes, r.err)
	}
}

func (m *Manager) dataDone(r *request) {
	m.observe(r)
	m.afterData(r)
}

func (m *Manager) afterData(r *request) {
	e := m.cfg.Chains[r.asset.key.Place].entries[r.entry]
	if r.err != nil {
		m.retry(r)
		return
	}
	if m.cfg.KeepBytes != nil && e.Pool != nil {
		m.cfg.KeepBytes(r.file, r.bytes)
	}
	if m.cfg.Found != nil {
		m.cfg.Found(r.asset.key, r.file)
	}
	// An exact key with one name is one file already, so only a key with formats or names to choose from is remembered.
	if r.asset.key.Class != ClassExact || len(r.asset.names) > 1 {
		if len(m.winners) >= winnersCap {
			clear(m.winners)
		}
		m.winners[r.asset.key] = r.file
	}
	format := m.cfg.Formats[r.asset.key.Kind]
	if format == nil {
		m.end(r, Ready, r.bytes, nil)
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
		delete(m.active, r.asset.key)
		m.results.push(r)
		return
	}
	m.end(r, Ready, r.result, nil)
}

// retry moves past the copy that just failed; a source reporting absence isn't an error to keep.
func (m *Manager) retry(r *request) {
	if !IsAbsent(r.err) {
		r.failed = true
		r.lastErr = r.err
	}
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
			if r == nil || r.urgency != u || !m.allowed(r) {
				break
			}
			w.pop()
			m.start(q.pool, r)
		}
	}
}

// allowed checks the limits that apply beyond a free worker: a host's limit and the memory claim.
func (m *Manager) allowed(r *request) bool {
	if r.host != "" {
		l := m.cfg.Chains[r.asset.key.Place].entries[r.entry].Source.(HostLimiter)
		if m.hostBusy[r.host] >= l.Limit(r.host) {
			return false
		}
	}
	return r.step != stepDecode || r.urgency == Now || m.claimed == 0 || m.cfg.ClaimLimit <= 0 || m.claimed+r.claim <= m.cfg.ClaimLimit
}

func (m *Manager) start(p *Pool, r *request) {
	r.queued = false
	p.busy++
	switch r.step {
	case stepWork:
		p.jobs <- func() {
			r.result = r.work()
			m.report(r)
		}
	case stepData:
		if r.host != "" {
			m.hostBusy[r.host]++
		}
		source := m.cfg.Chains[r.asset.key.Place].entries[r.entry].Source
		file := r.file
		p.jobs <- func() {
			r.bytes, r.err = source.Data(file)
			m.report(r)
		}
	case stepMeasure:
		measurer := m.cfg.Formats[r.asset.key.Kind].(Measurer)
		data := r.bytes
		p.jobs <- func() {
			r.claim = measurer.Measure(data)
			m.report(r)
		}
	case stepDecode:
		m.claimed += r.claim
		r.asset.claim.Store(r.claim)
		format := m.cfg.Formats[r.asset.key.Kind]
		key, data := r.asset.key, r.bytes
		hit := Hit{File: r.file, Source: m.cfg.Chains[key.Place].entries[r.entry].Source}
		p.jobs <- func() {
			r.result, r.err = format.Decode(key, hit, data)
			m.report(r)
		}
	}
}

func (m *Manager) report(r *request) {
	m.finished.push(r)
	m.signal()
}

// ErrAbsent is what a source's Data returns when the file isn't there, as opposed to failing to find out.
var ErrAbsent = errors.New("media: file absent from this source")

// IsAbsent reports whether err means the file isn't in that source.
func IsAbsent(err error) bool { return errors.Is(err, ErrAbsent) }

// IsMissing reports whether err means no source has the file.
func IsMissing(err error) bool { return errors.Is(err, ErrMissing) }
