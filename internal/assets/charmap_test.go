package assets

import (
	"image/color"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// Once a character's folder is mapped, a sprite named only "X" costs one request, not the misses for "(a)X".
func TestMappedCharacterSkipsMissingSpellings(t *testing.T) {
	const side = 4
	png := encodePNG(t, side, side, color.White)
	cs := newCountingServer(t, map[string][]byte{
		"/characters/c/":           []byte(`<pre><a href="../">../</a><a href="first.png">x</a><a href="second.png">x</a></pre>`),
		"/characters/c/first.png":  png,
		"/characters/c/second.png": png,
	})
	rig := newRig(t, network.NewClientNotFoundTTL(0, netproxy.Direct()), false)
	ask := func(name string) {
		t.Helper()
		base := cs.srv.URL + "/characters/c/"
		rig.manager.PrefetchChain(base+"(a)"+name, []string{base + name}, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
		waitDecoded(t, rig).Asset.Release()
	}
	ask("first")
	root := media.File(cs.srv.URL + "/characters/c/")
	deadline := time.Now().Add(managerWait)
	for mapped := false; !mapped; {
		rig.manager.bridge.media.Call(func() { mapped = rig.manager.bridge.remote.Mapped(root) })
		if time.Now().After(deadline) {
			t.Fatal("the character was never mapped")
		}
		time.Sleep(time.Millisecond)
	}
	before := cs.total()
	ask("second")
	if n := cs.total() - before; n != 1 {
		t.Errorf("a mapped character's bare-named sprite cost %d requests, want 1", n)
	}
}
