//go:build !novoice

package ui

import "testing"

// TestMicLevel pins the mic-test meter math (#84): peak amplitude as 0..1, safe on
// empty input.
func TestMicLevel(t *testing.T) {
	if got := micLevel(make([]int16, 960)); got != 0 {
		t.Errorf("silence level = %v, want 0", got)
	}
	if got := micLevel(nil); got != 0 {
		t.Errorf("nil level = %v, want 0", got)
	}
	// Full-scale negative peak → ~1.0 (abs(-32768)/32768).
	if got := micLevel([]int16{0, 100, -32768, 50}); got < 0.99 || got > 1.0 {
		t.Errorf("full-scale level = %v, want ~1.0", got)
	}
	// Half-scale → ~0.5.
	if got := micLevel([]int16{16384}); got < 0.49 || got > 0.51 {
		t.Errorf("half-scale level = %v, want ~0.5", got)
	}
}
