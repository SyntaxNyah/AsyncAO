package assets

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// waitRecalled waits until a Manager has read its learned.json.
func waitRecalled(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(managerWait)
	for {
		recalled := false
		m.bridge.media.Call(func() { recalled = m.bridge.recalled })
		if recalled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("learned.json never read")
		}
		time.Sleep(time.Millisecond)
	}
}

// A host learned in one session is asked in its format first in the next.
func TestLearnedFormatOutlivesTheSession(t *testing.T) {
	const side = 4
	png := encodePNG(t, side, side, color.White)
	cs := newCountingServer(t, map[string][]byte{
		"/characters/a/(a)x.png": png,
		"/characters/a/(a)y.png": png,
	})
	learned := filepath.Join(t.TempDir(), "learned.json")

	first := newRigAt(t, network.NewClientNotFoundTTL(0, netproxy.Direct()), false, learned)
	waitRecalled(t, first.manager)
	first.manager.Media().Get(ChainKey(cs.srv.URL+"/characters/a/(a)x", AssetTypeCharSprite), media.Now, 0) // AssetType: CharSprite
	waitDecoded(t, first).Asset.Release()
	first.manager.CloseMedia()
	if _, err := os.Stat(learned); err != nil {
		t.Fatalf("learned.json not written: %v", err)
	}

	next := newRigAt(t, network.NewClientNotFoundTTL(0, netproxy.Direct()), false, learned)
	waitRecalled(t, next.manager)
	before := cs.total()
	next.manager.Media().Get(ChainKey(cs.srv.URL+"/characters/a/(a)y", AssetTypeCharSprite), media.Now, 0) // AssetType: CharSprite
	waitDecoded(t, next).Asset.Release()
	if n := cs.total() - before; n != 1 {
		t.Errorf("the next session needed %d requests, want 1", n)
	}
}
