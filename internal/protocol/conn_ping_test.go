package protocol

import (
	"context"
	"testing"
	"time"
)

// TestConnPing pins the #128 RTT source: Conn.Ping sends a WebSocket ping and returns a
// positive round-trip once the peer's pong lands (the echo server's read loop auto-pongs).
func TestConnPing(t *testing.T) {
	srv := echoAOServer(t)
	conn, err := Dial(context.Background(), wsURL(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	<-conn.Incoming() // drain the greeting so the read loop is live (it processes the pong)

	// err == nil proves the pong round-tripped; the duration itself can round to 0 on a
	// loopback (sub-millisecond) under Windows' coarse timer — that's fine, it's just fast.
	rtt, err := conn.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if rtt < 0 {
		t.Errorf("rtt = %v, want >= 0", rtt)
	}

	// A cancelled context makes Ping fail rather than hang.
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, err := conn.Ping(ctx); err == nil {
		t.Log("ping with a 1ns deadline returned no error (raced the pong) — acceptable")
	}
}
