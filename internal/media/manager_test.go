package media

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testTimeout        = 5 * time.Second
	testWorkers        = 4
	testHungWorkers    = 2
	testCallers        = 16
	testClaim          = int64(100)
	testAskCount       = 64
	testLaterPerWorker = 3
	testPathKinds      = 3
)

// fakeSource answers from a table and can hang its Data calls.
type fakeSource struct {
	name     string
	mu       sync.Mutex
	has      map[File]Answer
	data     map[File][]byte
	hang     chan struct{}
	calls    atomic.Int64
	observed atomic.Int64
	active   atomic.Int64
	peak     atomic.Int64
}

func newFakeSource(name string) *fakeSource {
	return &fakeSource{name: name, has: map[File]Answer{}, data: map[File][]byte{}}
}

func (s *fakeSource) put(f File, b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.has[f], s.data[f] = Yes, b
}

func (s *fakeSource) answer(f File, a Answer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.has[f] = a
}

func (s *fakeSource) Has(f File) Answer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.has[f]
}

func (s *fakeSource) Data(f File) ([]byte, error) {
	s.calls.Add(1)
	n := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if s.hang != nil {
		<-s.hang
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data[f]
	if !ok {
		return nil, errors.New("not here")
	}
	return b, nil
}

func (s *fakeSource) Size() Contents { return Contents{Name: s.name, Entries: len(s.has)} }

func (s *fakeSource) Observe(f File, _ []byte, err error) {
	s.observed.Add(1)
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.has[f] = No
	}
}

// hostSource is a fakeSource behind one limited host.
type hostSource struct {
	*fakeSource
	limit int
}

func (s *hostSource) Host(File) string { return s.name }
func (s *hostSource) Limit(string) int { return s.limit }

// fakeFormat copies bytes to a string, and can fail, hold its decodes, or need the main thread.
type fakeFormat struct {
	bad      map[string]bool
	hold     chan struct{}
	onMain   bool
	active   atomic.Int64
	started  atomic.Int64
	measured int64
}

func (f *fakeFormat) Decode(_ Key, _ Hit, b []byte) (any, error) {
	f.started.Add(1)
	if f.hold != nil {
		<-f.hold
	}
	if f.bad[string(b)] {
		return nil, errors.New("corrupt")
	}
	return string(b), nil
}

func (f *fakeFormat) OnMainThread() bool { return f.onMain }

type measuringFormat struct{ *fakeFormat }

func (f measuringFormat) Measure([]byte) int64 { return f.measured }

func oneFile(_ Key, name string) []File { return []File{File(name)} }

func image(path string) Key { return Key{Place: PlaceServer, Path: path, Kind: KindImage} }

func waitFinal(t *testing.T, a *Asset) {
	t.Helper()
	select {
	case <-a.done:
	case <-time.After(testTimeout):
		t.Fatalf("%q still %d after %v", a.key.Path, a.State(), testTimeout)
	}
}

func newRig(t *testing.T, format Format, entries ...Entry) *Manager {
	t.Helper()
	compute := NewPool("compute", testWorkers, 1)
	m := NewManager(Config{
		Chains:     map[Place]*Chain{PlaceServer: NewChain(entries...)},
		Formats:    map[Kind]Format{KindImage: format},
		Candidates: oneFile,
		Compute:    compute,
	})
	t.Cleanup(func() {
		m.Close()
		compute.Close()
	})
	return m
}

func TestFileFromASourceBecomesReady(t *testing.T) {
	src := newFakeSource("disk")
	src.put("a", []byte("hello"))
	disk := NewPool("disk", testWorkers, 0)
	defer disk.Close()
	m := newRig(t, &fakeFormat{}, Entry{src, disk})

	a := m.Get(image("a"), Now, 0)
	waitFinal(t, a)
	if !a.Ready() || a.Result() != "hello" {
		t.Fatalf("state %d result %v", a.State(), a.Result())
	}
}

func TestRepeatAsksShareOneAssetAndOneRead(t *testing.T) {
	src := newFakeSource("disk")
	src.put("a", []byte("x"))
	disk := NewPool("disk", testWorkers, 0)
	defer disk.Close()
	m := newRig(t, &fakeFormat{}, Entry{src, disk})

	var wg sync.WaitGroup
	got := make([]*Asset, testCallers)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = m.Get(image("a"), Now, 0)
		}()
	}
	wg.Wait()
	for _, a := range got {
		if a != got[0] {
			t.Fatal("two Assets for one key")
		}
	}
	waitFinal(t, got[0])
	if n := src.calls.Load(); n != 1 {
		t.Fatalf("%d reads for one file", n)
	}
}

func TestMissingEverywhereEnds(t *testing.T) {
	m := newRig(t, &fakeFormat{}, Entry{newFakeSource("disk"), nil})
	a := m.Get(image("nowhere"), Now, 0)
	waitFinal(t, a)
	if a.State() != Missing || !IsMissing(a.Err()) {
		t.Fatalf("state %d err %v", a.State(), a.Err())
	}
}

func TestStaleEntryFallsThroughToNextSource(t *testing.T) {
	stale, good := newFakeSource("disk"), newFakeSource("remote")
	stale.answer("a", Yes) // says yes, but the read fails
	good.put("a", []byte("real"))
	pool := NewPool("disk", testWorkers, 0)
	defer pool.Close()
	m := newRig(t, &fakeFormat{}, Entry{stale, pool}, Entry{good, pool})

	a := m.Get(image("a"), Now, 0)
	waitFinal(t, a)
	if !a.Ready() || a.Result() != "real" {
		t.Fatalf("state %d err %v", a.State(), a.Err())
	}
	if stale.observed.Load() != 1 || stale.Has("a") != No {
		t.Fatal("the source was not told its entry failed")
	}
}

func TestBadCopyDoesNotHideTheNextSource(t *testing.T) {
	pack, server := newFakeSource("pack"), newFakeSource("server")
	pack.put("a", []byte("broken"))
	server.put("a", []byte("good"))
	pool := NewPool("disk", testWorkers, 0)
	defer pool.Close()
	m := newRig(t, &fakeFormat{bad: map[string]bool{"broken": true}}, Entry{pack, pool}, Entry{server, pool})

	a := m.Get(image("a"), Now, 0)
	waitFinal(t, a)
	if !a.Ready() || a.Result() != "good" {
		t.Fatalf("state %d err %v", a.State(), a.Err())
	}
}

func TestKnownCopyBeatsAskingAnEarlierUnknownSource(t *testing.T) {
	remote, disk := newFakeSource("remote"), newFakeSource("disk")
	remote.answer("a", Unknown)
	disk.put("a", []byte("local"))
	pool := NewPool("disk", testWorkers, 0)
	defer pool.Close()
	m := newRig(t, &fakeFormat{}, Entry{remote, pool}, Entry{disk, pool})

	a := m.Get(image("a"), Now, 0)
	waitFinal(t, a)
	if a.Result() != "local" || remote.calls.Load() != 0 {
		t.Fatalf("result %v, %d remote requests", a.Result(), remote.calls.Load())
	}
}

// A hung host may only hold its own workers: this is the failure the old pipeline reproduced.
func TestHungHostHoldsOnlyItsOwnWorkers(t *testing.T) {
	hung := &hostSource{fakeSource: newFakeSource("hung"), limit: testHungWorkers}
	hung.hang = make(chan struct{})
	cached := newFakeSource("memory")
	cached.put("cached", []byte("x"))
	healthy := &hostSource{fakeSource: newFakeSource("healthy"), limit: testWorkers}
	healthy.put("cold", []byte("y"))
	network := NewPool("network", testWorkers, 0)
	defer func() {
		close(hung.hang) // before Close, which waits for its workers
		network.Close()
	}()

	compute := NewPool("compute", testWorkers, 1)
	defer compute.Close()
	m := NewManager(Config{
		Chains: map[Place]*Chain{
			PlaceServer: NewChain(Entry{hung, network}),
			PlacePacks:  NewChain(Entry{healthy, network}),
			PlaceTheme:  NewChain(Entry{cached, nil}),
		},
		Formats:    map[Kind]Format{KindImage: &fakeFormat{}},
		Candidates: oneFile,
		Compute:    compute,
	})
	defer m.Close()

	for i := range testAskCount {
		k := image(string(rune('A' + i)))
		hung.answer(File(k.Path), Unknown)
		m.Get(k, Now, 0)
	}
	deadline := time.Now().Add(testTimeout)
	for hung.active.Load() < testHungWorkers && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	inMemory := m.Get(Key{Place: PlaceTheme, Path: "cached", Kind: KindImage}, Now, 0)
	waitFinal(t, inMemory)
	cold := m.Get(Key{Place: PlacePacks, Path: "cold", Kind: KindImage}, Now, 0)
	waitFinal(t, cold)
	if !inMemory.Ready() || !cold.Ready() {
		t.Fatal("work on other sources did not finish")
	}
	if p := hung.peak.Load(); p > testHungWorkers {
		t.Fatalf("hung host held %d workers", p)
	}
}

func TestNowGoesAheadOfWaitingLaterWork(t *testing.T) {
	src := newFakeSource("memory")
	format := &fakeFormat{hold: make(chan struct{})}
	const later = testWorkers * testLaterPerWorker
	for i := range later {
		src.put(File(rune('a'+i)), []byte("later"))
	}
	src.put("message", []byte("now"))
	m := newRig(t, format, Entry{src, nil})

	waitingWork := make([]*Asset, later)
	for i := range waitingWork {
		waitingWork[i] = m.Get(image(string(rune('a'+i))), Later, 0)
	}
	deadline := time.Now().Add(testTimeout)
	// One worker is kept for Now work, so later work fills all but one.
	for format.started.Load() < testWorkers-1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	now := m.Get(image("message"), Now, 0)
	for format.started.Load() < testWorkers && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if format.started.Load() != testWorkers {
		t.Fatalf("%d decodes started, want the kept worker to take the message", format.started.Load())
	}
	close(format.hold)
	waitFinal(t, now)
	for _, a := range waitingWork {
		waitFinal(t, a)
	}
}

func TestClaimLimitHoldsLaterDecodesButNotNow(t *testing.T) {
	src := newFakeSource("memory")
	for _, f := range []File{"a", "b", "message"} {
		src.put(f, []byte(f))
	}
	format := measuringFormat{&fakeFormat{hold: make(chan struct{}), measured: testClaim}}
	compute := NewPool("compute", testWorkers, 0)
	defer compute.Close()
	m := NewManager(Config{
		Chains:     map[Place]*Chain{PlaceServer: NewChain(Entry{src, nil})},
		Formats:    map[Kind]Format{KindImage: format},
		Candidates: oneFile,
		Compute:    compute,
		ClaimLimit: testClaim,
	})
	defer m.Close()

	first := m.Get(image("a"), Later, 0)
	second := m.Get(image("b"), Later, 0)
	deadline := time.Now().Add(testTimeout)
	for format.started.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(testSettle)
	if n := format.started.Load(); n != 1 {
		t.Fatalf("%d later decodes under a limit that fits one", n)
	}
	now := m.Get(image("message"), Now, 0)
	for format.started.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := format.started.Load(); n != 2 {
		t.Fatalf("message decode did not pass the limit, %d started", n)
	}
	close(format.hold)
	waitFinal(t, first)
	waitFinal(t, now)
	waitFinal(t, second)
}

const testSettle = 20 * time.Millisecond

func TestMainThreadResultsWaitForFinish(t *testing.T) {
	src := newFakeSource("memory")
	src.put("a", []byte("pixels"))
	m := newRig(t, &fakeFormat{onMain: true}, Entry{src, nil})

	a := m.Get(image("a"), Now, 0)
	var got any
	deadline := time.Now().Add(testTimeout)
	for got == nil && time.Now().Before(deadline) {
		m.TakeResults(func(_ *Asset, r any) { got = r })
		time.Sleep(time.Millisecond)
	}
	if got != "pixels" {
		t.Fatalf("result %v", got)
	}
	if a.Ready() {
		t.Fatal("ready before the main thread used it")
	}
	m.Finish(a, "texture")
	if !a.Ready() || a.Result() != "texture" {
		t.Fatalf("state %d result %v", a.State(), a.Result())
	}
}

func TestForgetAsksAgain(t *testing.T) {
	src := newFakeSource("disk")
	src.put("a", []byte("x"))
	disk := NewPool("disk", testWorkers, 0)
	defer disk.Close()
	m := newRig(t, &fakeFormat{}, Entry{src, disk})

	first := m.Get(image("a"), Now, 0)
	waitFinal(t, first)
	m.Forget(image("a"))
	second := m.Get(image("a"), Now, 0)
	if first == second {
		t.Fatal("Forget kept the old Asset")
	}
	waitFinal(t, second)
	if src.calls.Load() != 2 {
		t.Fatalf("%d reads", src.calls.Load())
	}
}

func TestLaterIsRefusedAtTheCapButNowIsNot(t *testing.T) {
	src := newFakeSource("memory")
	src.put("a", []byte("x"))
	src.put("message", []byte("y"))
	compute := NewPool("compute", testWorkers, 0)
	defer compute.Close()
	m := NewManager(Config{
		Chains:     map[Place]*Chain{PlaceServer: NewChain(Entry{src, nil})},
		Formats:    map[Kind]Format{KindImage: &fakeFormat{}},
		Candidates: oneFile,
		Compute:    compute,
		MaxAssets:  1,
	})
	defer m.Close()

	waitFinal(t, m.Get(image("a"), Later, 0))
	refused := m.Get(image("b"), Later, 0)
	if refused.State() != Refused || !errors.Is(refused.Err(), ErrRefused) {
		t.Fatalf("state %d", refused.State())
	}
	now := m.Get(image("message"), Now, 0)
	waitFinal(t, now)
	if !now.Ready() {
		t.Fatalf("state %d", now.State())
	}
}

func TestLoadWaitsOffMainAndRefusesOnMain(t *testing.T) {
	src := newFakeSource("memory")
	src.put("a", []byte("x"))
	var onMain atomic.Bool
	compute := NewPool("compute", testWorkers, 0)
	defer compute.Close()
	m := NewManager(Config{
		Chains:       map[Place]*Chain{PlaceServer: NewChain(Entry{src, nil})},
		Formats:      map[Kind]Format{KindImage: &fakeFormat{}},
		Candidates:   oneFile,
		Compute:      compute,
		IsMainThread: onMain.Load,
	})
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	got, err := m.Load(ctx, image("a"), Now)
	if err != nil || got != "x" {
		t.Fatalf("got %v err %v", got, err)
	}
	onMain.Store(true)
	defer func() {
		if recover() == nil {
			t.Fatal("Load on the main thread did not panic")
		}
	}()
	_, _ = m.Load(ctx, image("a"), Now)
}

// Every request ends in one final state, whatever mix of paths it takes; a hang here would freeze the wait gate.
func TestEveryRequestEnds(t *testing.T) {
	src := newFakeSource("disk")
	bad := map[string]bool{}
	var keys []Key
	for i := range testAskCount {
		k := image(string(rune('A' + i)))
		keys = append(keys, k)
		switch i % testPathKinds {
		case 0:
			src.put(File(k.Path), []byte(k.Path))
		case 1:
			src.put(File(k.Path), []byte(k.Path))
			bad[k.Path] = true
		}
	}
	disk := NewPool("disk", testWorkers, 0)
	defer disk.Close()
	m := newRig(t, &fakeFormat{bad: bad}, Entry{src, disk})

	var wg sync.WaitGroup
	for range testCallers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, k := range keys {
				m.Get(k, Later, 0)
			}
		}()
	}
	wg.Wait()
	for _, k := range keys {
		a := m.Get(k, Later, 0)
		waitFinal(t, a)
		if s := a.State(); s == Searching || s == Decoding {
			t.Fatalf("%q ended in state %d", k.Path, s)
		}
	}
}
