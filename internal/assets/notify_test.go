package assets

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/media"
)

// TestManagerDeliveryNotify pins the decode-delivery wake hook (the
// experimental event-driven render loop's doorbell): the callback fires when a
// prefetched asset lands on the Decoded channel, so a parked loop uploads it
// immediately instead of waiting out an idle tick.
func TestManagerDeliveryNotify(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "characters", "witch", "emotions", "button1_off.png")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, encodePNG(t, 4, 4, color.White), 0o644); err != nil {
		t.Fatal(err)
	}

	local := NewLocalFetcher([]string{dir})
	rig := newRig(t, local, true)
	wakes := make(chan struct{}, 8)
	rig.manager.SetDeliveryNotify(func() {
		select {
		case wakes <- struct{}{}:
		default:
		}
	})

	base := local.BaseURL() + "characters/witch/emotions/button1_off"
	rig.manager.AllowFormats(local.BaseURL(), map[AssetType]string{AssetTypeEmoteButton: config.ExtPNG})
	rig.manager.Media().Get(ChainKey(base, AssetTypeEmoteButton), media.Now, 0) // AssetType: EmoteButton (test)

	deadline := time.After(managerWait)
	select {
	case <-wakes:
	case <-deadline:
		t.Fatal("no delivery wake after the decode landed")
	}
	d, ok := rig.takeDecoded(managerWait)
	if !ok {
		t.Fatal("the decode itself never delivered")
	}
	if d.Err != nil || d.Asset == nil {
		t.Fatalf("decode delivery broken: %+v", d)
	}
	d.Asset.Release()
}
