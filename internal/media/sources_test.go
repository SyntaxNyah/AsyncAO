package media

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	testOrigin     = "http://host.test/base/"
	testBudget     = int64(1 << 20)
	testRemotePool = 32
	testShortTTL   = time.Minute
	testPollEvery  = time.Millisecond
)

// fakeFetcher plays a host: it serves its table, answers 404 otherwise, and can hang or fail outright.
type fakeFetcher struct {
	mu     sync.Mutex
	files  map[string][]byte
	asked  []string
	hang   chan struct{}
	broken error
	active atomic.Int64
	peak   atomic.Int64
}

func (f *fakeFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	f.mu.Lock()
	f.asked = append(f.asked, url)
	hang, broken := f.hang, f.broken
	f.mu.Unlock()
	if hang != nil {
		<-hang
	}
	if broken != nil {
		return nil, broken
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.files[url]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("%w: %s", network.ErrAssetNotFound, url)
}

func (f *fakeFetcher) askedFor() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

func (f *fakeFetcher) setHang(c chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hang = c
}

func (f *fakeFetcher) setBroken(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broken = err
}

func (f *fakeFetcher) add(url string, b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[url] = b
}

// clockAt is a clock a test moves by hand.
type clockAt struct{ ns atomic.Int64 }

func newClock() *clockAt {
	c := &clockAt{}
	c.ns.Store(time.Now().UnixNano())
	return c
}
func (c *clockAt) now() time.Time      { return time.Unix(0, c.ns.Load()) }
func (c *clockAt) add(d time.Duration) { c.ns.Add(int64(d)) }

type remoteRig struct {
	m       *Manager
	remote  *RemoteSource
	builder *CandidateBuilder
	fetch   *fakeFetcher
	network *Pool
	clock   *clockAt
}

func newRemoteRig(t *testing.T, files map[string][]byte) *remoteRig {
	t.Helper()
	fetch := &fakeFetcher{files: files}
	clock := newClock()
	builder := NewCandidateBuilder()
	remote := NewRemoteSource(context.Background(), fetch, testShortTTL, clock.now)
	net := NewPool("network", testRemotePool, 0)
	compute := NewPool("compute", testWorkers, 1)
	m := NewManager(Config{
		Chains:     map[Place]*Chain{PlaceServer: NewChain(Entry{remote, net})},
		Formats:    map[Kind]Format{KindImage: &fakeFormat{}},
		Candidates: builder.Build,
		Found:      builder.Found,
		Compute:    compute,
		MissingTTL: testShortTTL,
		Clock:      clock.now,
	})
	t.Cleanup(func() {
		fetch.mu.Lock()
		hang := fetch.hang
		fetch.hang = nil
		fetch.mu.Unlock()
		if hang != nil {
			close(hang)
		}
		m.Close()
		net.Close()
		compute.Close()
	})
	return &remoteRig{m, remote, builder, fetch, net, clock}
}

func sprite(path string, alts ...string) Key {
	k := Key{Place: PlaceServer, Origin: testOrigin, Path: path, Kind: KindImage, Class: ClassEmote}
	copy(k.Alts[:], alts)
	return k
}

func TestRemoteTriesFormatsAndSpellingsInOrder(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{testOrigin + "c/e.png": []byte("x")})
	a := r.m.Get(sprite("c/(a)e", "c/e"), Now)
	waitFinal(t, a)
	if !a.Ready() {
		t.Fatalf("state %d err %v", a.State(), a.Err())
	}
	asked := r.fetch.askedFor()
	if asked[0] != testOrigin+"c/(a)e.webp" || asked[len(asked)-1] != testOrigin+"c/e.png" {
		t.Fatalf("asked %v", asked)
	}
}

func TestMissingIsRememberedThenNoticedAfterItsTime(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{})
	a := r.m.Get(sprite("c/e"), Now)
	waitFinal(t, a)
	if a.State() != Missing {
		t.Fatalf("state %d err %v", a.State(), a.Err())
	}
	asked := len(r.fetch.askedFor())
	if again := r.m.Get(sprite("c/e"), Now); again != a {
		t.Fatal("a repeat ask inside the time asked the host again")
	}
	r.fetch.add(testOrigin+"c/e.webp", []byte("added"))
	r.clock.add(testShortTTL)
	b := r.m.Get(sprite("c/e"), Now)
	waitFinal(t, b)
	if !b.Ready() || len(r.fetch.askedFor()) <= asked {
		t.Fatalf("a file added later was not noticed: state %d", b.State())
	}
}

func TestHostStartsAtOneWorkerAndAnAnswerUnlocksTheRest(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{})
	first := make(chan struct{})
	r.fetch.setHang(first)
	for i := range testAskCount {
		r.m.Get(sprite(fmt.Sprintf("c%d/e", i)), Later)
	}
	waitUntil(t, func() bool { return r.fetch.active.Load() == HostStartWorkers })
	time.Sleep(testSettle)
	if p := r.fetch.peak.Load(); p != HostStartWorkers {
		t.Fatalf("a host that had not answered held %d workers", p)
	}
	r.fetch.setHang(make(chan struct{}))
	close(first) // the first request answers, with a 404
	waitUntil(t, func() bool { return r.fetch.active.Load() == HostAnsweredWorkers })
}

func TestTransportFailurePutsAHostBackToOneWorker(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{})
	host := hostOf(File(testOrigin))
	r.remote.Confirm(host)
	if got := r.remote.Limit(host); got != HostAnsweredWorkers {
		t.Fatalf("limit %d", got)
	}
	r.remote.Observe(File(testOrigin+"a"), nil, errors.New("connection reset"))
	if got := r.remote.Limit(host); got != HostStartWorkers {
		t.Fatalf("limit %d after a transport failure", got)
	}
	r.remote.Observe(File(testOrigin+"a"), nil, fmt.Errorf("%w: 500", network.ErrUnexpectedStatus))
	if got := r.remote.Limit(host); got != HostAnsweredWorkers {
		t.Fatalf("limit %d after a status: the host answered", got)
	}
	if r.remote.Has(File(testOrigin+"a")) != Unknown {
		t.Fatal("a 500 was recorded as an answer about the file")
	}
}

func TestJoinConfirmsHostEvenWithA404(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{})
	done := make(chan bool, 1)
	Join(r.m, r.network, r.remote, r.fetch, r.builder, testOrigin, func(found bool) { done <- found })
	select {
	case found := <-done:
		if found {
			t.Fatal("a 404 counted as a manifest")
		}
	case <-time.After(testTimeout):
		t.Fatal("join did not finish")
	}
	r.fetch.setHang(make(chan struct{}))
	for i := range testAskCount {
		r.m.Get(sprite(fmt.Sprintf("c%d/e", i)), Later)
	}
	waitUntil(t, func() bool { return r.fetch.active.Load() == HostAnsweredWorkers })
}

func TestManifestLimitsFormatsButDoesNotSetTheirOrder(t *testing.T) {
	manifest := []byte(`{"emote_extensions": [".png", ".webp.animated", ".bogus"]}`)
	r := newRemoteRig(t, map[string][]byte{testOrigin + ManifestFile: manifest})
	done := make(chan bool, 1)
	Join(r.m, r.network, r.remote, r.fetch, r.builder, testOrigin, func(found bool) { done <- found })
	if !<-done {
		t.Fatal("manifest not read")
	}
	a := r.m.Get(sprite("c/e"), Now)
	waitFinal(t, a)
	want := []string{testOrigin + ManifestFile, testOrigin + "c/e.webp", testOrigin + "c/e.png"}
	if asked := r.fetch.askedFor(); !slices.Equal(asked, want) {
		t.Fatalf("asked %v, want %v", asked, want)
	}
}

func TestLearnedFormatGoesFirstAndStaysInsideTheManifest(t *testing.T) {
	b := NewCandidateBuilder()
	key := sprite("e")
	b.Learn(testOrigin, ClassEmote, config.ExtPNG)
	if got := b.Build(key)[0]; got != File(testOrigin+"e"+config.ExtPNG) {
		t.Fatalf("first %v", got)
	}
	b.Allow(testOrigin, map[Class][]string{ClassEmote: {config.ExtWebP}})
	if got := b.Build(key); !slices.Equal(got, []File{File(testOrigin + "e" + config.ExtWebP)}) {
		t.Fatalf("got %v", got)
	}
}

func TestAFoundFormatIsTriedFirstNextTime(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{
		testOrigin + "a" + config.ExtPNG: []byte("x"),
		testOrigin + "b" + config.ExtPNG: []byte("y"),
	})
	waitFinal(t, r.m.Get(sprite("a"), Now))
	before := len(r.fetch.askedFor())
	waitFinal(t, r.m.Get(sprite("b"), Now))
	if asked := r.fetch.askedFor(); len(asked) != before+1 {
		t.Fatalf("the second file cost %d requests: %v", len(asked)-before, asked)
	}
}

func TestOneLearnedSuccessDoesNotBeatACommonFormat(t *testing.T) {
	b := NewCandidateBuilder()
	for range testWorkers {
		b.Learn(testOrigin, ClassEmote, config.ExtWebP)
	}
	b.Learn(testOrigin, ClassEmote, config.ExtPNG)
	if got := b.Build(sprite("e"))[0]; got != File(testOrigin+"e"+config.ExtWebP) {
		t.Fatalf("first %v", got)
	}
}

func TestExactClassIsOneFile(t *testing.T) {
	b := NewCandidateBuilder()
	got := b.Build(Key{Origin: testOrigin, Path: "sounds/music/a.opus", Class: ClassExact})
	if !slices.Equal(got, []File{File(testOrigin + "sounds/music/a.opus")}) {
		t.Fatalf("got %v", got)
	}
}

func TestTransportFailureFailsRatherThanMissingAndRetries(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{testOrigin + "c/e.webp": []byte("x")})
	r.fetch.setBroken(errors.New("connection refused"))
	a := r.m.Get(sprite("c/e"), Now)
	waitFinal(t, a)
	if a.State() != Failed {
		t.Fatalf("state %d: a host that can't be reached must not mean missing", a.State())
	}
	r.fetch.setBroken(nil)
	b := r.m.Get(sprite("c/e"), Now)
	waitFinal(t, b)
	if !b.Ready() {
		t.Fatalf("state %d after the host came back", b.State())
	}
}

func TestDiskIndexHealsAndScans(t *testing.T) {
	root := t.TempDir()
	disk, err := cache.NewDiskCache(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	src := NewDiskSource(disk)
	if src.Has("http://x/a") != No {
		t.Fatal("empty index answered yes")
	}
	src.Keep("http://x/a", []byte("data"))
	if src.Has("http://x/a") != Yes {
		t.Fatal("kept file not indexed")
	}
	waitUntil(t, func() bool {
		d, derr := src.Data("http://x/a")
		return derr == nil && string(d) == "data"
	})
	src.Observe("http://x/a", nil, ErrAbsent)
	if src.Has("http://x/a") != No {
		t.Fatal("a failed read left the entry")
	}
	keys, err := ScanDiskIndex(root)
	if err != nil || len(keys) != 1 {
		t.Fatalf("scan %v %v", keys, err)
	}
	src.Adopt(keys)
	if src.Has("http://x/a") != Yes {
		t.Fatal("a scanned file is not in the index")
	}
}

func TestScanOfAMissingFolderIsEmpty(t *testing.T) {
	keys, err := ScanDiskIndex(filepath.Join(t.TempDir(), "nothing"))
	if err != nil || len(keys) != 0 {
		t.Fatalf("%v %v", keys, err)
	}
}

func TestMemorySourceKeepsWithinBudget(t *testing.T) {
	src, err := NewMemorySource(testBudget)
	if err != nil {
		t.Fatal(err)
	}
	src.Keep("a", []byte("x"))
	if src.Has("a") != Yes || src.Size().Entries != 1 {
		t.Fatal("not kept")
	}
	if _, err := src.Data("b"); !IsAbsent(err) {
		t.Fatalf("err %v", err)
	}
}

func heldRig(t *testing.T) (*Manager, *fakeFormat, []*Asset) {
	t.Helper()
	src := newFakeSource("memory")
	format := &fakeFormat{hold: make(chan struct{})}
	const later = testWorkers * testLaterPerWorker
	for i := range later {
		src.put(File(fmt.Sprintf("l%d", i)), []byte("later"))
	}
	m := newRig(t, format, Entry{src, nil})
	var all []*Asset
	for i := range later {
		all = append(all, m.Get(image(fmt.Sprintf("l%d", i)), Later))
	}
	waitUntil(t, func() bool { return format.started.Load() == testWorkers-1 })
	return m, format, all
}

func TestNowRaisesAWaitingLaterRequest(t *testing.T) {
	m, format, all := heldRig(t)
	last := all[len(all)-1]
	// Asking again with Now moves the last waiting one onto the kept worker.
	if m.Get(last.Key(), Now) != last {
		t.Fatal("a raise made a second Asset")
	}
	waitUntil(t, func() bool { return format.started.Load() == testWorkers })
	close(format.hold)
	for _, a := range all {
		waitFinal(t, a)
	}
	if n := format.started.Load(); n != int64(len(all)) {
		t.Fatalf("%d decodes for %d files: a raised request ran twice or not at all", n, len(all))
	}
}

func TestWithdrawnWaitingRequestNeverStarts(t *testing.T) {
	m, format, all := heldRig(t)
	last := all[len(all)-1]
	m.Withdraw(last.Key())
	waitFinal(t, last)
	if last.State() != Withdrawn {
		t.Fatalf("state %d", last.State())
	}
	close(format.hold)
	for _, a := range all {
		waitFinal(t, a)
	}
	if n, want := format.started.Load(), int64(len(all)-1); n != want {
		t.Fatalf("%d decodes, want %d", n, want)
	}
}

func TestWithdrawDuringAStepEndsWhenItReports(t *testing.T) {
	src := newFakeSource("memory")
	src.put("a", []byte("x"))
	format := &fakeFormat{hold: make(chan struct{})}
	m := newRig(t, format, Entry{src, nil})
	a := m.Get(image("a"), Now)
	waitUntil(t, func() bool { return format.started.Load() == 1 })
	m.Withdraw(a.Key())
	time.Sleep(testSettle)
	close(format.hold)
	waitFinal(t, a)
	if a.State() != Withdrawn {
		t.Fatalf("state %d", a.State())
	}
}

func TestWithdrawOriginLeavesOtherServersAlone(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{})
	hold := make(chan struct{})
	r.fetch.setHang(hold)
	gone := r.m.Get(sprite("b"), Later)
	other := Key{Place: PlaceServer, Origin: "http://other.test/", Path: "c", Kind: KindImage, Class: ClassEmote}
	stays := r.m.Get(other, Later)
	r.m.WithdrawOrigin(testOrigin)
	time.Sleep(testSettle)
	r.fetch.setHang(nil)
	close(hold)
	waitFinal(t, gone)
	waitFinal(t, stays)
	if gone.State() != Withdrawn || stays.State() != Missing {
		t.Fatalf("gone %d stays %d", gone.State(), stays.State())
	}
	if r.m.Get(sprite("b"), Later) == gone {
		t.Fatal("a withdrawn Asset came back")
	}
}

func TestRunDoesWorkAndReportsOnTheManager(t *testing.T) {
	pool := NewPool("long", testWorkers, 0)
	defer pool.Close()
	m := newRig(t, &fakeFormat{}, Entry{newFakeSource("disk"), nil})
	got := make(chan any, 1)
	m.Run(pool, Later, func() any { return "done" }, func(v any) { got <- v })
	select {
	case v := <-got:
		if v != "done" {
			t.Fatalf("got %v", v)
		}
	case <-time.After(testTimeout):
		t.Fatal("work did not finish")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(testPollEvery)
	}
}
