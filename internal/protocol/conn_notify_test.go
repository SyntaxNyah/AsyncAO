package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestConnNotify pins the packet-arrival wake hook (the experimental
// event-driven render loop's doorbell): SetNotify fires after a packet is
// queued on Incoming, and once more when the connection dies. The server
// echoes only AFTER our send, so the callback is provably installed before
// any traffic — no races with the read loop.
func TestConnNotify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		// Reply once to the client's first frame; the second frame is the cue
		// to drop the connection server-side (the "server kicked us" path).
		if _, _, err := ws.Read(r.Context()); err != nil {
			return
		}
		_ = ws.Write(r.Context(), websocket.MessageText, []byte("PN#1#10#%"))
		if _, _, err := ws.Read(r.Context()); err != nil {
			return
		}
		_ = ws.Close(websocket.StatusGoingAway, "test over")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	wakes := make(chan struct{}, 8)
	conn.SetNotify(func() {
		select {
		case wakes <- struct{}{}:
		default:
		}
	})

	if err := conn.Send(ctx, Packet{Header: "HI", Fields: []string{"hdid"}}); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case <-wakes:
	case <-time.After(5 * time.Second):
		t.Fatal("no wake after a packet landed on Incoming")
	}
	select {
	case p := <-conn.Incoming():
		if p.Header != "PN" {
			t.Errorf("packet = %+v, want the PN echo", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the packet itself never arrived")
	}

	// The close wake: a server-side drop must surface as fast as a packet.
	if err := conn.Send(ctx, Packet{Header: "CH", Fields: []string{"0"}}); err != nil {
		t.Fatalf("send close cue: %v", err)
	}
	select {
	case <-wakes:
	case <-time.After(5 * time.Second):
		t.Fatal("no wake when the connection died")
	}
	// And Incoming really is closed (the wake announced the drop, not a packet).
	select {
	case _, ok := <-conn.Incoming():
		if ok {
			t.Error("expected Incoming to close after the server drop")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Incoming never closed after the server drop")
	}
}
