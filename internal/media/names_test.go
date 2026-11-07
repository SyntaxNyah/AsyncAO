package media

import (
	"fmt"
	"testing"
)

const (
	testPrimary  = "background/court/defenseempty"
	testFallback = "background/court/witnessempty"
	testLast     = "background/court/wit"
)

// absentSource is a fakeSource whose missing files are absent rather than failing.
type absentSource struct{ *fakeSource }

func (s absentSource) Data(f File) ([]byte, error) {
	s.mu.Lock()
	_, ok := s.data[f]
	s.mu.Unlock()
	if !ok {
		s.calls.Add(1)
		return nil, fmt.Errorf("%w: %s", ErrAbsent, f)
	}
	return s.fakeSource.Data(f)
}

func newNamesRig(t *testing.T, entries ...Entry) *Manager {
	t.Helper()
	compute := NewPool("compute", testWorkers, 1)
	m := NewManager(Config{
		Chains:     map[Place]*Chain{PlaceServer: NewChain(entries...)},
		Formats:    map[Kind]Format{KindImage: &fakeFormat{}},
		Candidates: oneFile,
		Compute:    compute,
	})
	t.Cleanup(func() {
		m.Close()
		compute.Close()
	})
	return m
}

func TestAFallbackInMemoryDoesNotBeatThePrimaryOnDisk(t *testing.T) {
	memory := newFakeSource("memory")
	memory.put(testFallback, []byte("fallback"))
	disk := newFakeSource("disk")
	disk.put(testPrimary, []byte("primary"))
	pool := NewPool("disk", testWorkers, 0)
	defer pool.Close()
	m := newNamesRig(t, Entry{Source: memory}, Entry{Source: disk, Pool: pool})

	a := m.Get(image(testPrimary), Now, 0, testFallback, testLast)
	waitFinal(t, a)
	if a.Result() != "primary" {
		t.Fatalf("got %v, state %d: a later name counted before the first was known absent", a.Result(), a.State())
	}
}

func TestANameEverySourceKnowsIsAbsentCostsNothing(t *testing.T) {
	memory := newFakeSource("memory")
	memory.put(testFallback, []byte("fallback"))
	disk := newFakeSource("disk")
	disk.answer(testPrimary, No)
	pool := NewPool("disk", testWorkers, 0)
	defer pool.Close()
	m := newNamesRig(t, Entry{Source: memory}, Entry{Source: disk, Pool: pool})

	a := m.Get(image(testPrimary), Now, 0, testFallback, testLast)
	waitFinal(t, a)
	if a.Result() != "fallback" || disk.calls.Load() != 0 {
		t.Fatalf("got %v with %d disk reads, want the fallback with none", a.Result(), disk.calls.Load())
	}
}

func TestAPrimaryThatFailedFailsInsteadOfFallingBack(t *testing.T) {
	memory := newFakeSource("memory")
	memory.put(testFallback, []byte("fallback"))
	server := newFakeSource("server")
	server.answer(testPrimary, Unknown)
	pool := NewPool("network", testWorkers, 0)
	defer pool.Close()
	m := newNamesRig(t, Entry{Source: memory}, Entry{Source: server, Pool: pool})

	a := m.Get(image(testPrimary), Now, 0, testFallback, testLast)
	waitFinal(t, a)
	if a.State() != Failed {
		t.Fatalf("state %d result %v: a transport failure is not proof the primary is absent", a.State(), a.Result())
	}
}

func TestAKeyIsAskedAsTheFileItWasFoundAsBeforeItsNames(t *testing.T) {
	server := absentSource{newFakeSource("server")}
	server.answer(testPrimary, Unknown)
	server.put(testFallback, []byte("fallback"))
	pool := NewPool("network", testWorkers, 0)
	defer pool.Close()
	m := newNamesRig(t, Entry{Source: server, Pool: pool})

	first := m.Get(image(testPrimary), Now, 0, testFallback, testLast)
	waitFinal(t, first)
	if first.Result() != "fallback" {
		t.Fatalf("got %v, state %d", first.Result(), first.State())
	}
	// The primary's missing answer has run out, and the delivered Asset was dropped, as after a texture eviction.
	server.answer(testPrimary, Unknown)
	m.Forget(image(testPrimary))
	before := server.calls.Load()

	again := m.Get(image(testPrimary), Now, 0, testFallback, testLast)
	waitFinal(t, again)
	if again.Result() != "fallback" || server.calls.Load() != before+1 {
		t.Fatalf("got %v with %d requests, want the same file with one", again.Result(), server.calls.Load()-before)
	}
}

func TestEveryAskAddsItsFlags(t *testing.T) {
	src := newFakeSource("disk")
	src.put("a", []byte("x"))
	pool := NewPool("disk", testWorkers, 0)
	defer pool.Close()
	m := newRig(t, &fakeFormat{}, Entry{Source: src, Pool: pool})

	a := m.Get(image("a"), Later, Quiet)
	if !a.Quiet() || a.Sticky() {
		t.Fatalf("quiet %v sticky %v after the first ask", a.Quiet(), a.Sticky())
	}
	m.Get(image("a"), Later, Sticky)
	if a.Quiet() || !a.Sticky() {
		t.Fatalf("quiet %v sticky %v: a loud ask must end quiet and a sticky one must stick", a.Quiet(), a.Sticky())
	}
	waitFinal(t, a)
}

func TestAskingWithMoreNamesSearchesAMissingKeyAgain(t *testing.T) {
	server := absentSource{newFakeSource("server")}
	server.answer(testPrimary, Unknown)
	server.put(testFallback, []byte("fallback"))
	pool := NewPool("network", testWorkers, 0)
	defer pool.Close()
	m := newNamesRig(t, Entry{Source: server, Pool: pool})

	bare := m.Get(image(testPrimary), Now, 0)
	waitFinal(t, bare)
	if bare.State() != Missing {
		t.Fatalf("state %d asked without names", bare.State())
	}
	named := m.Get(image(testPrimary), Now, 0, testFallback)
	waitFinal(t, named)
	if named.Result() != "fallback" {
		t.Fatalf("got %v, state %d: the names a later ask brought were not searched", named.Result(), named.State())
	}
}
