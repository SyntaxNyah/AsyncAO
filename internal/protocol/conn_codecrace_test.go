package protocol

import (
	"context"
	"sync"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
)

// TestConcurrentDialCodecRegistrationIsSafe hammers the Dial path while a live
// session's read loop stays up decoding server packets. Before the sync.Once
// fix, every Dial re-ran RegisterVoiceCodecs + RegisterGroupPairCodec, which
// re-write packetutil.customCodecs and aolib's packet registry (plain maps)
// while a previous session's readLoop (receiveCustom) reads them — a concurrent
// map read/write fatal (the two-tab connect crash). The fix registers once, so
// this hammer is race-free.
func TestConcurrentDialCodecRegistrationIsSafe(t *testing.T) {
	srv := echoAOServer(t)

	// A live session whose read loop keeps decoding the server's packets.
	live, err := Dial(context.Background(), netproxy.Direct(), wsURL(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer live.Close()
	<-live.Incoming()

	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := Dial(context.Background(), netproxy.Direct(), wsURL(srv))
			if err == nil {
				<-c.Incoming()
				c.Close()
			}
		}()
	}
	wg.Wait()
}
