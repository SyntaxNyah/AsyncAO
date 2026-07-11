package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestConnStaleWatchdogClosesSilentLink pins the read-staleness watchdog (#7b):
// a link that goes silent past the (test-shortened) threshold AND does not answer
// the probe ping is declared dead — Incoming closes and Err reports why. The
// server here accepts the socket, greets once, then goes completely silent and
// refuses to pong (its read loop is parked, so coder/websocket never dispatches
// the pong), so the watchdog's ping times out and it tears the conn down.
func TestConnStaleWatchdogClosesSilentLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		_ = ws.Write(ctx, websocket.MessageText, []byte("decryptor#34#%"))
		// Go silent: never Read (so no pong is ever sent back) and never write.
		// The client's watchdog must notice the silence and give up.
		<-ctx.Done()
	}))
	t.Cleanup(srv.Close)

	// Short threshold so the watchdog fires in well under a second: the probe
	// ping's timeout is capped at the threshold (60 ms here), so a silent,
	// non-ponging server is declared dead within ~2 probe intervals.
	conn, err := Dial(context.Background(), wsURL(srv), DialOptions{StaleReadTimeout: 60 * time.Millisecond})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	<-conn.Incoming() // greeting resets the staleness clock; silence begins now

	deadline := time.After(15 * time.Second)
	for {
		select {
		case _, open := <-conn.Incoming():
			if !open {
				if conn.Err() == nil {
					t.Error("a stale silent link must surface a read error")
				}
				return
			}
		case <-deadline:
			t.Fatal("watchdog never closed the stale connection")
		}
	}
}

// TestConnStaleWatchdogKeepsLiveLinkOpen pins that the watchdog does NOT tear
// down a link that keeps answering: the server pongs (coder/websocket auto-pongs
// on Read), so even with a short threshold the probe succeeds and Incoming stays
// open. A false-positive teardown here would break every idle-but-healthy
// session.
func TestConnStaleWatchdogKeepsLiveLinkOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		_ = ws.Write(ctx, websocket.MessageText, []byte("decryptor#34#%"))
		// Keep reading so coder/websocket auto-responds to control pings with a
		// pong (the watchdog's probe then succeeds and the link stays up).
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	// 120 ms threshold → the watchdog probes ~every 120 ms with a 120 ms ping
	// budget, comfortably above loopback RTT so a reading server's pong always
	// lands in time (no false positive), yet fast enough to run several cycles.
	conn, err := Dial(context.Background(), wsURL(srv), DialOptions{StaleReadTimeout: 120 * time.Millisecond})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	<-conn.Incoming() // greeting

	// Wait several watchdog probe cycles; the link must stay open the whole time.
	select {
	case _, open := <-conn.Incoming():
		if !open {
			t.Fatalf("watchdog wrongly closed a live (pong-answering) link: %v", conn.Err())
		}
	case <-time.After(700 * time.Millisecond):
		// No spurious close after many probe cycles — the link is healthy.
	}
}

// TestConnNoGoroutineLeakAcrossReconnects proves the read loop + watchdog are
// tied to the connection's lifetime (rule §17.4): dialing and closing many
// connections must not leak goroutines. Without the watchdog's <-c.closed exit
// this count would climb by one per cycle.
func TestConnNoGoroutineLeakAcrossReconnects(t *testing.T) {
	srv := echoAOServer(t)

	// Warm up one cycle so any one-time runtime goroutines exist before we
	// baseline (avoids counting lazily-started pool/timer goroutines as leaks).
	warm, err := Dial(context.Background(), wsURL(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	<-warm.Incoming()
	warm.Close()
	for range warm.Incoming() { // drain until closed so its goroutines exit
	}
	waitGoroutinesSettle()
	base := runtime.NumGoroutine()

	const cycles = 25
	for i := 0; i < cycles; i++ {
		conn, err := Dial(context.Background(), wsURL(srv))
		if err != nil {
			t.Fatalf("Dial %d: %v", i, err)
		}
		<-conn.Incoming() // greeting (starts readLoop + watchStale)
		conn.Close()
		for range conn.Incoming() { // let both goroutines wind down
		}
	}
	waitGoroutinesSettle()
	// A real per-connection leak (readLoop or watchStale not exiting on close)
	// would add ~1-2 goroutines PER cycle, i.e. ~25-50 here. The small slack
	// tolerates a couple of in-flight server-side handler goroutines still
	// winding down; it is far below what a genuine leak would show.
	if got := runtime.NumGoroutine(); got > base+4 {
		t.Errorf("goroutine leak across %d reconnects: baseline %d, now %d", cycles, base, got)
	}
}

// waitGoroutinesSettle gives closing goroutines a moment to exit before a count
// is taken — the closed channel unblocks them, but the scheduler needs a beat.
func waitGoroutinesSettle() {
	for i := 0; i < 50; i++ {
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
}
