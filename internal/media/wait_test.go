package media

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

const waitTestShort = 20 * time.Millisecond

func TestWaitReturnsOnceTheAssetFinishes(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{testOrigin + "a" + config.ExtPNG: []byte("x")})
	a := r.m.Get(sprite("a"), Now, 0)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := a.Wait(ctx); err != nil || !a.Ready() {
		t.Fatalf("wait %v, state %v", err, a.State())
	}
	if h := a.Hit(); h.File != File(testOrigin+"a"+config.ExtPNG) || h.Source != Source(r.remote) {
		t.Fatalf("hit %+v, want the png from the remote source", h)
	}
}

func TestWaitGivesUpWithItsContext(t *testing.T) {
	r := newRemoteRig(t, nil)
	hold := make(chan struct{})
	r.fetch.setHang(hold)
	a := r.m.Get(sprite("a"), Now, 0)
	ctx, cancel := context.WithTimeout(context.Background(), waitTestShort)
	defer cancel()
	if err := a.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait %v, want the context's deadline", err)
	}
	if h := a.Hit(); h != (Hit{}) {
		t.Fatalf("an unfinished asset has a hit: %+v", h)
	}
}

func TestUnlistedFindsAFormatTheHostLeavesOut(t *testing.T) {
	files := map[string][]byte{testOrigin + "a" + config.ExtPNG: []byte("x")}
	listedOnly := newRemoteRig(t, files)
	listedOnly.m.Call(func() { listedOnly.builder.Allow(testOrigin, map[Class][]string{ClassEmote: {config.ExtWebP}}) })
	a := listedOnly.m.Get(sprite("a"), Now, 0)
	waitFinal(t, a)
	if a.Ready() {
		t.Fatal("a png the host's list leaves out was found without asking for it")
	}

	unlisted := newRemoteRig(t, files)
	unlisted.m.Call(func() { unlisted.builder.Allow(testOrigin, map[Class][]string{ClassEmote: {config.ExtWebP}}) })
	b := unlisted.m.Get(sprite("a"), Now, Unlisted)
	waitFinal(t, b)
	if !b.Ready() || b.Hit().File != File(testOrigin+"a"+config.ExtPNG) {
		t.Fatalf("state %v hit %+v, want the png", b.State(), b.Hit())
	}
}

func TestWaitReturnsWhenTheMediaSystemStops(t *testing.T) {
	r := newRemoteRig(t, nil)
	r.fetch.setHang(make(chan struct{}))
	a := r.m.Get(sprite("a"), Now, 0)
	got := make(chan error, 1)
	go func() { got <- a.Wait(context.Background()) }()
	r.m.Close()
	select {
	case err := <-got:
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("wait %v, want ErrStopped", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("a wait outlived the media system")
	}
}
