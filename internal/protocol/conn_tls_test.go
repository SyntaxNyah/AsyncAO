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

// tlsGreetServer is a wss AO server backed by httptest's generated self-signed
// cert (trusted by no store), so it exercises the wss certificate-verify path.
// It greets with decryptor and then idles until the connection or test ends.
func tlsGreetServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = ws.Write(r.Context(), websocket.MessageText, []byte("decryptor#34#%"))
		<-r.Context().Done() // hold the conn open until the client/test goes away
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wssURL(srv *httptest.Server) string {
	return "wss" + strings.TrimPrefix(srv.URL, "https")
}

// TestDialTLSVerifyDefault: the secure default (the "Validate server
// certificates" toggle ON → no SkipTLSVerify) rejects a self-signed wss server.
func TestDialTLSVerifyDefault(t *testing.T) {
	srv := tlsGreetServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, wssURL(srv)) // zero options = verify
	if err == nil {
		conn.Close()
		t.Fatal("Dial accepted a self-signed cert with verification on; want failure")
	}
}

// TestDialTLSSkipVerify: the app default (Security toggle OFF →
// SkipTLSVerify=true) connects to a self-signed wss server, the common case for
// community AO servers without a CA-signed cert.
func TestDialTLSSkipVerify(t *testing.T) {
	srv := tlsGreetServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, wssURL(srv), DialOptions{SkipTLSVerify: true})
	if err != nil {
		t.Fatalf("Dial with SkipTLSVerify against self-signed wss: %v", err)
	}
	defer conn.Close()
	select {
	case p := <-conn.Incoming():
		if p.Header != "decryptor" {
			t.Errorf("greeting = %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no greeting over self-signed wss")
	}
}
