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

// TestConnJSONModeFromDecryptorCapability pins the Nyathena JSON negotiation:
// the legacy decryptor greeting carries "JSON" (decryptor#JSON#%) and the
// connection must flip its outbound wire to JSON so the rest of the handshake
// answers the server in kind.
func TestConnJSONModeFromDecryptorCapability(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		_ = ws.Write(ctx, websocket.MessageText, []byte("decryptor#JSON#%"))
		if _, data, err := ws.Read(ctx); err == nil {
			got <- string(data)
		}
		for { // keep reading so the client's close handshake completes promptly
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	conn, err := Dial(context.Background(), wsURL(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	select {
	case p := <-conn.Incoming():
		if p.Header != "decryptor" || p.Field(0) != "JSON" {
			t.Fatalf("greeting = %+v, want decryptor/JSON", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no greeting received")
	}
	if !conn.JSONMode() {
		t.Fatal("JSON mode not detected from decryptor#JSON#%")
	}

	if err := conn.Send(context.Background(), NewPacket("HI", "hdid")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case raw := <-got:
		if !strings.HasPrefix(raw, "{") || !strings.Contains(raw, `"$header":"HI"`) {
			t.Errorf("client packet = %q, want JSON HI", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never captured the client packet")
	}
}

// TestConnJSONModeFromJSONFrame pins the per-frame auto-detection: the first
// inbound JSON frame flips the outbound wire to JSON even with no decryptor
// capability flag.
func TestConnJSONModeFromJSONFrame(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"$header":"decryptor","value":"JSON"}`))
		if _, data, err := ws.Read(ctx); err == nil {
			got <- string(data)
		}
		for { // keep reading so the client's close handshake completes promptly
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	conn, err := Dial(context.Background(), wsURL(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	select {
	case p := <-conn.Incoming():
		if p.Header != "decryptor" || p.Field(0) != "JSON" {
			t.Fatalf("greeting = %+v, want decryptor/JSON (folded from JSON frame)", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no greeting received")
	}
	if !conn.JSONMode() {
		t.Fatal("JSON mode not auto-detected from an inbound JSON frame")
	}

	if err := conn.Send(context.Background(), NewPacket("ID", "AsyncAO", "2.11.0-asyncao")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case raw := <-got:
		if !strings.HasPrefix(raw, "{") || !strings.Contains(raw, `"$header":"ID"`) {
			t.Errorf("client packet = %q, want JSON ID", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never captured the client packet")
	}
}
