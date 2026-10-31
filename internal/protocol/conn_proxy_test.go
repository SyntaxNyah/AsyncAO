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
	sockets := netproxy.New(nil, func(string) string { return "" })
	sockets.Apply(netproxy.ModeManual, proxy.URL)

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

// The game connection must take the proxy: a user relying on one would otherwise show their real address to the server.
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
	sockets := netproxy.New(nil, func(string) string { return "" })
	sockets.Apply(netproxy.ModeManual, proxy.URL)

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
