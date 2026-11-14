package assets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// TestMP3MusicFetchesExactlyTheURLItWasGiven pins the first half of the MP3
// music contract: a music track — a server-relative name or a DJ /play link —
// carries its own extension, so it is fetched EXACTLY and is never re-probed
// into another format.
//
// This is not a property of the URL builder alone: it is a property of the
// pipeline pass PrefetchExact runs (resolveExact, which has no candidate ladder
// at all). The test proves it at the wire, by counting what the origin actually
// sees: one request, for the .mp3, and no .opus/.ogg speculation beside it —
// which is also the zero-fallback pillar (spec §4, one probe per asset).
func TestMP3MusicFetchesExactlyTheURLItWasGiven(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, ".mp3") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ID3\x03\x00\x00\x00fake-mp3-payload"))
	}))
	defer srv.Close()

	rig := newRig(t, network.NewClientNotFoundTTL(0, netproxy.Direct()), false)
	url := srv.URL + "/sounds/music/cornered.mp3"
	rig.manager.PrefetchExact(url, AssetTypeMusic, network.PriorityHigh) // AssetType: Music (Exact)

	select {
	case a := <-rig.manager.Audio():
		if a.URL != url {
			t.Errorf("delivered URL = %q, want the exact %q", a.URL, url)
		}
		if a.Type != AssetTypeMusic {
			t.Errorf("delivered type = %v, want Music", a.Type)
		}
		if !strings.HasPrefix(string(a.Data), "ID3") {
			t.Errorf("delivered %d bytes that are not the mp3 payload", len(a.Data))
		}
	case <-time.After(managerWait):
		t.Fatal("exact mp3 music fetch never delivered")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "/sounds/music/cornered.mp3" {
		t.Errorf("origin saw %v, want exactly one request for the .mp3 (no format probing)", seen)
	}
}
