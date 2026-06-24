package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestPingQuality pins the latency→bars mapping: unknown is 0 bars, then 4/3/2/1 as the ms
// crosses the good/ok/fair thresholds.
func TestPingQuality(t *testing.T) {
	cases := []struct {
		ms      int
		unknown bool
		bars    int32
	}{
		{0, true, 0},
		{40, false, 4},
		{pingGoodMs, false, 3}, // exactly the good threshold falls to the next tier
		{100, false, 3},
		{pingOkMs, false, 2},
		{200, false, 2},
		{pingFairMs, false, 1},
		{900, false, 1},
	}
	for _, tc := range cases {
		if b, _ := pingQuality(tc.ms, tc.unknown); b != tc.bars {
			t.Errorf("pingQuality(%d, unknown=%v) bars = %d, want %d", tc.ms, tc.unknown, b, tc.bars)
		}
	}
}

// TestUpdatePingLoopOff pins the zero-cost default: with the chip off (default) and/or no
// connection, updatePingLoop starts no loop — no goroutine, no pinging.
func TestUpdatePingLoopOff(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}} // chip off (default), no conn

	a.updatePingLoop()
	if a.pingConn != nil || a.pingCancel != nil {
		t.Fatal("chip off started a ping loop")
	}
	// Even with the chip ON, no connection means no loop.
	prefs.SetPingChip(true)
	a.updatePingLoop()
	if a.pingConn != nil {
		t.Error("chip on with no connection started a ping loop")
	}
}
