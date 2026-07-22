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

// echoAOServer accepts one WebSocket client, greets it with decryptor, and
// echoes every received packet back prefixed with "ECHO".
func echoAOServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		// AO servers greet first.
		_ = ws.Write(ctx, websocket.MessageText, []byte("decryptor#34#%"))
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			_ = ws.Write(ctx, websocket.MessageText, append([]byte("ECHO#"), data...))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestConnDialReceiveSend(t *testing.T) {
	srv := echoAOServer(t)
	conn, err := Dial(context.Background(), wsURL(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Server greeting arrives parsed.
	select {
	case p := <-conn.Incoming():
		if p.Header != "decryptor" || p.Field(0) != "34" {
			t.Errorf("greeting = %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no greeting received")
	}

	// Send HI; the echo comes back with our escaped payload intact.
	hdid := "hd#id&100%"
	if err := conn.Send(context.Background(), NewPacket("HI", hdid)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case p := <-conn.Incoming():
		if p.Header != "ECHO" {
			t.Fatalf("echo header = %q", p.Header)
		}
		// ECHO#HI#<escaped hdid>#% → fields[0] = "HI", fields[1] = hdid.
		if p.Field(0) != "HI" || p.Field(1) != hdid {
			t.Errorf("echo = %+v, want HI/%q (escaping must survive the wire)", p, hdid)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no echo received")
	}

	s := conn.Stats()
	if s.Sent != 1 || s.Received != 2 {
		t.Errorf("stats = %+v", s)
	}
}

// TestKeepaliveGoroutineSends pins the fix for the minimized-app disconnect: the CH
// keepalive is driven by a dedicated goroutine (keepaliveLoop), NOT the caller's
// render loop — so it keeps firing even when Windows stalls that loop (minimized
// behind a fullscreen window). With a short interval and a payload set, the echo
// server receives repeated CH pings although the client NEVER calls Send.
func TestKeepaliveGoroutineSends(t *testing.T) {
	srv := echoAOServer(t)
	conn, err := Dial(context.Background(), wsURL(srv), DialOptions{KeepaliveInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Drain the server greeting.
	select {
	case <-conn.Incoming():
	case <-time.After(5 * time.Second):
		t.Fatal("no greeting")
	}

	// Arm the ping payload; the goroutine now sends CH#7 every ~20ms and the echo
	// server bounces each back as ECHO#CH#7#%. Collect several without ever Send-ing.
	conn.SetKeepalive(NewPacket("CH", "7").String())

	got := 0
	deadline := time.After(5 * time.Second)
	for got < 3 {
		select {
		case p := <-conn.Incoming():
			if p.Header == "ECHO" && p.Field(0) == "CH" && p.Field(1) == "7" {
				got++
			}
		case <-deadline:
			t.Fatalf("keepalive goroutine sent only %d CH pings; it must fire off the render loop", got)
		}
	}
	if s := conn.Stats(); s.Sent < 3 {
		t.Errorf("sent = %d, want ≥3 pings all from the goroutine (client never called Send)", s.Sent)
	}
}

func TestConnCloseEndsIncomingCleanly(t *testing.T) {
	srv := echoAOServer(t)
	conn, err := Dial(context.Background(), wsURL(srv))
	if err != nil {
		t.Fatal(err)
	}
	<-conn.Incoming() // greeting
	conn.Close()

	select {
	case _, open := <-conn.Incoming():
		if open {
			// Drain anything in flight; the channel must close soon.
			for range conn.Incoming() {
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Incoming never closed after Close")
	}
	if err := conn.Err(); err != nil {
		t.Errorf("deliberate Close must not report a read error, got %v", err)
	}
}

func TestConnServerDropReportsError(t *testing.T) {
	// httptest.CloseClientConnections does NOT close hijacked (WebSocket)
	// conns, so the server handler drops the socket itself: it abruptly
	// CloseNows after the first client packet.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = ws.Write(r.Context(), websocket.MessageText, []byte("decryptor#34#%"))
		_, _, _ = ws.Read(r.Context()) // wait for the client's HI
		_ = ws.CloseNow()              // yank the socket, no close frame
	}))
	t.Cleanup(srv.Close)

	conn, err := Dial(context.Background(), wsURL(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-conn.Incoming() // greeting
	if err := conn.Send(context.Background(), NewPacket("HI", "x")); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-conn.Incoming():
			if !open {
				if conn.Err() == nil {
					t.Error("server drop must surface a read error")
				}
				return
			}
		case <-deadline:
			t.Fatal("Incoming never closed after server drop")
		}
	}
}

func TestDialRejectsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead := wsURL(srv)
	srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := Dial(ctx, dead); err == nil {
		t.Error("Dial succeeded against a dead server")
	}
}
