package assets

import (
	"strconv"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	pinRoomA = "http://host.test/base/background/a/"
	pinRoomB = "http://host.test/base/background/b/"
	pinRoomC = "http://host.test/base/background/c/"
)

func TestPinnedSceneryOutlivesTheSharedCacheUntilTwoOtherRoomsArePinned(t *testing.T) {
	p := newSceneryPin()
	p.pin(pinRoomA)
	p.keep(pinRoomA+"defenseempty.webp", []byte("a"))
	p.keep("http://host.test/base/characters/x/(a)e.webp", []byte("sprite"))
	if !p.has(pinRoomA+"defenseempty.webp") || p.has("http://host.test/base/characters/x/(a)e.webp") {
		t.Fatal("only files under a pinned folder may be kept")
	}
	p.pin(pinRoomB)
	if !p.has(pinRoomA + "defenseempty.webp") {
		t.Fatal("a split view's room must not unpin the active room")
	}
	p.pin(pinRoomC)
	if p.has(pinRoomA + "defenseempty.webp") {
		t.Fatal("the room pinned longest ago must be released")
	}
}

func TestPinnedSceneryStaysWithinItsCaps(t *testing.T) {
	p := newSceneryPin()
	p.pin(pinRoomA)
	p.keep(pinRoomA+"huge.webp", make([]byte, sceneryPinMaxBytes+1))
	if p.has(pinRoomA + "huge.webp") {
		t.Fatal("a file over the byte cap was kept")
	}
	for i := range sceneryPinMaxFiles + 1 {
		p.keep(pinRoomA+strconv.Itoa(i)+".webp", []byte("x"))
	}
	if n := len(p.files); n != sceneryPinMaxFiles {
		t.Fatalf("kept %d files, cap %d", n, sceneryPinMaxFiles)
	}
}

func TestAFetchedBackgroundIsServedFromThePinAfterTheSharedCacheDropsIt(t *testing.T) {
	webp := fixture(t, "sprite_256x192.webp")
	base := bridgeTestOrigin + "background/court/defenseempty"
	host := &bridgeFakeHost{files: map[string][]byte{base + ".webp": webp}}
	r := newBridgeRig(t, host)
	r.manager.PinScenery(bridgeTestOrigin + "background/court/")
	r.manager.PrefetchChain(base, nil, AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	nextDecoded(t, r.manager, base)
	r.t2.Purge()
	if (bridgeMemory{r.t2, r.manager.bridge.pin}).Has(media.File(base+".webp")) != media.Yes {
		t.Fatal("the room's background left memory with the shared cache")
	}
}
