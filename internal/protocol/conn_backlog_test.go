package protocol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestReaderKeepsPongingWhileConsumerStalled is the regression test for the
// hours-minimized disconnect (v1.81.6): with the render loop stalled (the
// client NEVER drains Incoming), the server fills the old 256-slot queue and
// beyond, then WS-pings. Before the fix readLoop was parked on the full
// channel send, no goroutine sat in ws.Read, coder/websocket never answered
// the pings, and the server's ping-timeout killed the healthy link. With the
// backlog + deliverLoop split the reader always returns to ws.Read, so every
// ping must succeed — and once draining resumes, every packet must arrive in
// order with none lost.
func TestReaderKeepsPongingWhileConsumerStalled(t *testing.T) {
	const frames = 400 // > incomingQueueCap(256): guarantees the old code wedged here

	pingErrs := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := context.Background()
		_ = ws.Write(ctx, websocket.MessageText, []byte("decryptor#34#%"))
		// The server must keep reading to process the client's pong control
		// frames (same rule the client lives by).
		go func() {
			for {
				if _, _, err := ws.Read(ctx); err != nil {
					return
				}
			}
		}()
		for i := 0; i < frames; i++ {
			if err := ws.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf("SEQ#%d#%%", i))); err != nil {
				pingErrs <- fmt.Errorf("frame %d write: %w", i, err)
				return
			}
		}
		// All frames are in flight and the client is not draining. These pings
		// only succeed if the client's read goroutine is still inside ws.Read.
		for i := 0; i < 3; i++ {
			pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				pingErrs <- fmt.Errorf("ping %d: %w", i, err)
				return
			}
		}
		pingErrs <- nil
		// Hold the socket open until the test finishes draining.
		_, _, _ = ws.Read(ctx)
	}))
	t.Cleanup(srv.Close)

	conn, err := Dial(context.Background(), wsURL(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	select {
	case p := <-conn.Incoming():
		if p.Header != "decryptor" {
			t.Fatalf("greeting = %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no greeting")
	}

	// Stalled render loop: do not touch Incoming until the server reports.
	select {
	case err := <-pingErrs:
		if err != nil {
			t.Fatalf("server-side failure while client stalled (reader must keep ponging): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server never finished its ping round")
	}
	if err := conn.Err(); err != nil {
		t.Fatalf("healthy stalled client must not error, got %v", err)
	}

	// Render loop "resumes": every frame arrives, in order, none lost.
	deadline := time.After(10 * time.Second)
	for i := 0; i < frames; i++ {
		select {
		case p, ok := <-conn.Incoming():
			if !ok {
				t.Fatalf("Incoming closed after %d/%d packets: %v", i, frames, conn.Err())
			}
			if p.Header != "SEQ" || p.Field(0) != fmt.Sprint(i) {
				t.Fatalf("packet %d = %+v (FIFO order must survive the backlog)", i, p)
			}
		case <-deadline:
			t.Fatalf("drained only %d/%d packets after resume", i, frames)
		}
	}
}

// TestReadBacklogOverflowDisconnects pins the wedged-client contract: when the
// app stops draining for the ENTIRE backlog bound while packets keep arriving,
// the connection is torn down deliberately with errReadBacklogOverflow instead
// of buffering without bound.
func TestReadBacklogOverflowDisconnects(t *testing.T) {
	// incomingQueueCap(256) + tiny backlog(8) + the one packet deliverLoop may
	// hold in hand: 300 frames overflow it with margin.
	const frames = 300

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := context.Background()
		_ = ws.Write(ctx, websocket.MessageText, []byte("decryptor#34#%"))
		for i := 0; i < frames; i++ {
			if err := ws.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf("SEQ#%d#%%", i))); err != nil {
				return // client already tore down — expected
			}
		}
		_, _, _ = ws.Read(ctx) // hold until the client closes
	}))
	t.Cleanup(srv.Close)

	conn, err := Dial(context.Background(), wsURL(srv), DialOptions{ReadBacklogCap: 8})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Never drain while the flood is in flight — touching Incoming here would
	// relieve the very backpressure under test. Wait for the overflow verdict.
	deadline := time.Now().Add(10 * time.Second)
	for conn.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("overflow never tripped: Err() still nil with the client not draining")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := conn.Err(); !errors.Is(err, errReadBacklogOverflow) {
		t.Fatalf("Err() = %v, want errReadBacklogOverflow", err)
	}
	// The teardown must also close Incoming (after any still-buffered packets).
	for {
		select {
		case _, ok := <-conn.Incoming():
			if !ok {
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("overflowed connection never closed Incoming")
		}
	}
}

// TestIdleOnlyPingsNeverWedge pins the pure-idle baseline: with no data frames
// at all (only server WS pings) the reader stays inside ws.Read and answers
// every ping — the path that was already correct before the fix must stay so.
func TestIdleOnlyPingsNeverWedge(t *testing.T) {
	pingErrs := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := context.Background()
		_ = ws.Write(ctx, websocket.MessageText, []byte("decryptor#34#%"))
		go func() {
			for {
				if _, _, err := ws.Read(ctx); err != nil {
					return
				}
			}
		}()
		for i := 0; i < 5; i++ {
			pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				pingErrs <- fmt.Errorf("ping %d: %w", i, err)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		pingErrs <- nil
		_, _, _ = ws.Read(ctx)
	}))
	t.Cleanup(srv.Close)

	conn, err := Dial(context.Background(), wsURL(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Never drain anything — even the greeting sits buffered.
	select {
	case err := <-pingErrs:
		if err != nil {
			t.Fatalf("idle pings must all be answered: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server never finished its ping round")
	}
	if err := conn.Err(); err != nil {
		t.Errorf("idle client must not error, got %v", err)
	}
}
