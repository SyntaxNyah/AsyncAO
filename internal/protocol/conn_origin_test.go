package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// originCaptureServer accepts one WebSocket client and reports the Origin header
// the handshake carried. OriginPatterns "*" so a cross-origin dial isn't refused
// by the library itself (the whole point is observing what we sent).
func originCaptureServer(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Origin")
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		ctx := r.Context()
		_ = ws.Write(ctx, websocket.MessageText, []byte("decryptor#34#%"))
		for { // keep reading so the client's close handshake completes promptly
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// TestDialWSOriginHeader pins the power-user WS Origin override: set, the exact
// value rides the handshake; unset, NO Origin header is sent (the default
// handshake stays byte-identical for every normal server).
func TestDialWSOriginHeader(t *testing.T) {
	dial := func(srv *httptest.Server, opts ...DialOptions) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := Dial(ctx, wsURL(srv), opts...)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		conn.Close()
	}

	srv, got := originCaptureServer(t)
	const want = "https://webao.example"
	dial(srv, DialOptions{Origin: want})
	select {
	case o := <-got:
		if o != want {
			t.Errorf("handshake Origin = %q, want %q", o, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no handshake captured")
	}

	srv2, got2 := originCaptureServer(t)
	dial(srv2) // no options: the default dial must send NO Origin header
	select {
	case o := <-got2:
		if o != "" {
			t.Errorf("default handshake sent Origin %q, want none", o)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no default handshake captured")
	}
}
