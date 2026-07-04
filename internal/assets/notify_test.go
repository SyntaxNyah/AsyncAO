package assets

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
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
	rig.resolver.RecordSuccess(HostOf(base), AssetTypeEmoteButton, config.ExtPNG)
	rig.manager.Prefetch(base, AssetTypeEmoteButton, network.PriorityHigh) // AssetType: EmoteButton (test)

	deadline := time.After(managerWait)
	select {
	case <-wakes:
	case <-deadline:
		t.Fatal("no delivery wake after the decode landed")
	}
	select {
	case d := <-rig.manager.Decoded():
		if d.Err != nil || d.Asset == nil {
			t.Fatalf("decode delivery broken: %+v", d)
		}
		d.Asset.Release()
	case <-time.After(managerWait):
		t.Fatal("the decode itself never delivered")
	}
}
