package assets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
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

	rig := newRig(t, network.NewClient(), false)
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

// TestMusicFallbackChainReachesMP3 pins the second half: when a music asset DOES
// walk a chain — the diagnostic/export pass, the only music caller that ever
// does — mp3 is in it, after opus and ogg.
//
// The order is the contract, not just the membership: opus is the modern AO
// default and the zero-fallback single probe, ogg is the legacy bulk of live
// mirrors, and mp3 is the long tail. Putting mp3 earlier would spend a probe per
// track on servers that have none. Reading it through fullProbeChain rather than
// re-listing config's table is deliberate — this asserts what the manager will
// actually walk, so moving the table without moving the behavior still fails.
func TestMusicFallbackChainReachesMP3(t *testing.T) {
	got := fullProbeChain(nil, AssetTypeMusic)
	want := []string{config.ExtOpus, config.ExtOgg, config.ExtMP3}
	if len(got) != len(want) {
		t.Fatalf("music probe chain = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("music probe chain = %v, want %v", got, want)
		}
	}

	// The three chain formats must all be ones the mixer can actually decode.
	// A chain entry the audio backend never initialised is the v1.71 INIT_FLAC
	// ghost: the fetch succeeds, the decode silently fails, and the track is
	// "just broken". internal/render asks Mix_Init for OGG|MP3|OPUS|FLAC
	// (render/audio.go), so opus/ogg/mp3 are all covered — this pin fails the day
	// someone adds a fourth format here without checking that.
	for _, ext := range got {
		switch ext {
		case config.ExtOpus, config.ExtOgg, config.ExtMP3, config.ExtWAV:
		default:
			t.Errorf("music chain offers %s, which the SDL_mixer Init list does not cover", ext)
		}
	}
}
