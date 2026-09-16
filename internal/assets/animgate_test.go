package assets

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnimGateLimitsConcurrency(t *testing.T) {
	g := newAnimGate(2)

	var inFlight atomic.Int64
	var peak atomic.Int64
	var wg sync.WaitGroup

	const n = 16
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			g.acquire()
			defer g.release()
			cur := inFlight.Add(1)
			for {
				p := peak.Load()
				if cur <= p || peak.CompareAndSwap(p, cur) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-1)
		}()
	}
	wg.Wait()

	if got := peak.Load(); got > 2 {
		t.Fatalf("peak in-flight %d exceeds gate limit 2", got)
	}
}

func TestAnimGateRaiseLimitWakesWaiter(t *testing.T) {
	g := newAnimGate(1)
	g.acquire()

	acquired := make(chan struct{})
	go func() {
		g.acquire()
		close(acquired)
		g.release()
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire should block at limit 1")
	case <-time.After(20 * time.Millisecond):
	}

	g.SetLimit(2)
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("raising the limit did not wake the blocked waiter")
	}
	g.release()
}

func TestSetAnimatedDecodedAssetBytes(t *testing.T) {
	old := maxAnimatedDecodedAssetBytes.Load()
	t.Cleanup(func() { maxAnimatedDecodedAssetBytes.Store(old) })

	SetAnimatedDecodedAssetBytes(128 << 20)
	if got := maxAnimatedDecodedAssetBytes.Load(); got != 128<<20 {
		t.Fatalf("budget = %d, want %d", got, 128<<20)
	}

	SetAnimatedDecodedAssetBytes(0) // <= 0 resets to the default
	if got := maxAnimatedDecodedAssetBytes.Load(); got != defaultMaxAnimatedDecodedAssetBytes {
		t.Fatalf("reset budget = %d, want default %d", got, defaultMaxAnimatedDecodedAssetBytes)
	}
}
