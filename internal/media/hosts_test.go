package media

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

const (
	testMirror = "http://mirror.test/base/"
	testBackup = "http://backup.test/ao/"
)

func TestExtraHostsAreAskedInOrderAfterTheServer(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{testBackup + "c/e" + config.ExtPNG: []byte("x")})
	r.m.Do(func() { r.builder.SetHosts(testOrigin, []string{testMirror, testBackup}) })
	a := r.m.Get(sprite("c/e"), Now)
	waitFinal(t, a)
	if !a.Ready() {
		t.Fatalf("state %d err %v", a.State(), a.Err())
	}
	asked := r.fetch.askedFor()
	if asked[0] != testOrigin+"c/e"+config.ExtWebP || asked[len(asked)-1] != testBackup+"c/e"+config.ExtPNG {
		t.Fatalf("asked %v", asked)
	}
	lastServer, firstMirror, lastMirror, firstBackup := -1, -1, -1, -1
	for i, u := range asked {
		switch {
		case strings.HasPrefix(u, testOrigin):
			lastServer = i
		case strings.HasPrefix(u, testMirror):
			if firstMirror < 0 {
				firstMirror = i
			}
			lastMirror = i
		case firstBackup < 0:
			firstBackup = i
		}
	}
	if !(lastServer < firstMirror && lastMirror < firstBackup) {
		t.Fatalf("hosts out of order: %v", asked)
	}
}

func TestNoExtraHostsAskedWhenTheServerHasTheFile(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{testOrigin + "c/e" + config.ExtWebP: []byte("x")})
	r.m.Do(func() { r.builder.SetHosts(testOrigin, []string{testMirror}) })
	waitFinal(t, r.m.Get(sprite("c/e"), Now))
	if asked := r.fetch.askedFor(); !slices.Equal(asked, []string{testOrigin + "c/e" + config.ExtWebP}) {
		t.Fatalf("asked %v", asked)
	}
}

func TestEachExtraHostUsesItsOwnFormatList(t *testing.T) {
	b := NewCandidateBuilder()
	b.SetHosts(testOrigin, []string{testMirror})
	b.Allow(testMirror, map[Class][]string{ClassEmote: {config.ExtPNG}})
	got := b.Build(sprite("e"))
	if got[len(got)-1] != File(testMirror+"e"+config.ExtPNG) || slices.Contains(got, File(testMirror+"e"+config.ExtWebP)) {
		t.Fatalf("got %v: the mirror's list must decide its formats", got)
	}
	if got[0] != File(testOrigin+"e"+config.ExtWebP) {
		t.Fatalf("got %v: the mirror's list must not change the server's", got)
	}
}

func TestWholeURLKeysMoveToExtraHostsByTheirPath(t *testing.T) {
	b := NewCandidateBuilder()
	b.SetHosts(testOrigin, []string{testMirror})
	key := Key{Place: PlaceServer, Path: testOrigin + "c/(a)e", Kind: KindImage, Class: ClassEmote}
	key.Alts[0] = testOrigin + "c/e"
	got := b.Build(key)
	for _, want := range []File{File(testMirror + "c/(a)e" + config.ExtWebP), File(testMirror + "c/e" + config.ExtPNG)} {
		if !slices.Contains(got, want) {
			t.Fatalf("got %v, missing %v", got, want)
		}
	}
}

func TestExactFilesAreAskedOnExtraHostsToo(t *testing.T) {
	b := NewCandidateBuilder()
	b.SetHosts(testOrigin, []string{testMirror})
	got := b.Build(Key{Path: testOrigin + "characters/c/char.ini", Class: ClassExact})
	want := []File{File(testOrigin + "characters/c/char.ini"), File(testMirror + "characters/c/char.ini")}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAFormatFoundOnAnExtraHostIsLearnedForThatHost(t *testing.T) {
	b := NewCandidateBuilder()
	b.SetHosts(testOrigin, []string{testMirror})
	for range testWorkers {
		b.Found(sprite("e"), File(testMirror+"e"+config.ExtPNG))
	}
	got := b.Build(sprite("e"))
	if got[0] != File(testOrigin+"e"+config.ExtWebP) {
		t.Fatalf("got %v: the server's order changed", got)
	}
	if slices.Index(got, File(testMirror+"e"+config.ExtPNG)) > slices.Index(got, File(testMirror+"e"+config.ExtWebP)) {
		t.Fatalf("got %v: png must lead on the mirror", got)
	}
}

func TestClearingExtraHostsStopsAskingThem(t *testing.T) {
	b := NewCandidateBuilder()
	b.SetHosts(testOrigin, []string{testMirror})
	b.SetHosts(testOrigin, nil)
	for _, f := range b.Build(sprite("e")) {
		if strings.HasPrefix(string(f), testMirror) {
			t.Fatalf("still asks %v", f)
		}
	}
}

func TestSettingTheSameHostsAgainChangesNothing(t *testing.T) {
	b := NewCandidateBuilder()
	if !b.SetHosts(testOrigin, []string{testMirror}) {
		t.Fatal("a new list reported no change")
	}
	if b.SetHosts(testOrigin, []string{testMirror}) {
		t.Fatal("the same list reported a change, which would ask known misses again")
	}
}

func joinState(t *testing.T, r *remoteRig) ManifestState {
	t.Helper()
	done := make(chan ManifestState, 1)
	Join(r.m, r.network, r.remote, r.fetch, r.builder, testMirror, func(s ManifestState) { done <- s })
	select {
	case s := <-done:
		return s
	case <-time.After(testTimeout):
		t.Fatal("join did not finish")
		return ManifestUnchecked
	}
}

func TestAnUnreadableListOrADeadHostReportsFailed(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{testMirror + ManifestFile: []byte("not json")})
	if s := joinState(t, r); s != ManifestFailed {
		t.Fatalf("unreadable list reported %d, want failed", s)
	}
	r.fetch.setBroken(errors.New("connection refused"))
	if s := joinState(t, r); s != ManifestFailed {
		t.Fatalf("dead host reported %d, want failed", s)
	}
}
