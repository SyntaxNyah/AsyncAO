package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy/netproxytest"
)

func TestGameConnectionGoesThroughTheProxyForWSS(t *testing.T) {
	const (
		testTimeout = 5 * time.Second
		asked       = "asyncao-test.invalid:443"
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		<-r.Context().Done()
	}))
	defer srv.Close()
	proxy := netproxytest.Start(t, strings.TrimPrefix(srv.URL, "https://"))
	sockets := netproxy.New(nil)
	sockets.Apply(netproxy.ModeManual, proxy.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	// The test server's certificate is self-signed, so this also covers the skip-verification client.
	conn, err := Dial(ctx, sockets, "wss://"+asked, DialOptions{SkipTLSVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := proxy.Asked(); !slices.Equal(got, []string{asked}) {
		t.Errorf("proxy was asked for %v", got)
	}
}

// A forward proxy cannot carry a WebSocket, so ws:// must tunnel too.
func TestGameConnectionGoesThroughTheProxyForWS(t *testing.T) {
	const (
		testTimeout = 5 * time.Second
		asked       = "asyncao-test.invalid:27016"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		<-r.Context().Done()
	}))
	defer srv.Close()
	proxy := netproxytest.Start(t, strings.TrimPrefix(srv.URL, "http://"))
	sockets := netproxy.New(nil)
	sockets.Apply(netproxy.ModeManual, proxy.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	conn, err := Dial(ctx, sockets, "ws://"+asked)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := proxy.Asked(); !slices.Equal(got, []string{asked}) {
		t.Errorf("proxy was asked for %v", got)
	}
}

// An open game connection keeps its route when the setting changes.
func TestGameConnectionKeepsItsRouteAcrossAChange(t *testing.T) {
	const (
		testTimeout = 5 * time.Second
		message     = "still here"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		kind, data, err := ws.Read(r.Context())
		if err != nil {
			return
		}
		_ = ws.Write(r.Context(), kind, data)
		<-r.Context().Done()
	}))
	defer srv.Close()
	proxy := netproxytest.Start(t, strings.TrimPrefix(srv.URL, "http://"))
	sockets := netproxy.New(nil)
	sockets.Apply(netproxy.ModeManual, proxy.URL, "")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://asyncao-test.invalid:27016", &websocket.DialOptions{HTTPClient: sockets.Current().Client(netproxy.Game, 0)})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	sockets.Apply(netproxy.ModeDirect, "", "")
	if err := ws.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
		t.Fatal(err)
	}
	if _, data, err := ws.Read(ctx); err != nil || string(data) != message {
		t.Errorf("after the change: %q, %v", data, err)
	}
}
