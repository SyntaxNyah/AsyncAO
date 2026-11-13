package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/coder/websocket"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
)

// BenchmarkConnSend times what the caller waits for; each packet is written before the next, so a queue can't overflow.
func BenchmarkConnSend(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	conn, err := Dial(context.Background(), netproxy.Direct(), wsURL(srv))
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	p := NewPacket("CT", "Phoenix", "Hold it! That contradicts the evidence.")
	b.ResetTimer()
	for i := range b.N {
		if err := conn.Send(p); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		for conn.Stats().Sent <= int64(i) {
			runtime.Gosched()
		}
		b.StartTimer()
	}
}
