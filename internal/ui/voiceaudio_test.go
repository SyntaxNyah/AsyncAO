//go:build !novoice

package ui

import "testing"

func TestMixFrames(t *testing.T) {
	acc := make([]int32, 4)
	out := make([]int16, 4)

	// No frames → silent (returns false, out untouched).
	if mixFrames(out, acc, nil, 100) {
		t.Error("empty mix should report false")
	}

	// Two speakers sum at full volume.
	a := []int16{100, -100, 200, 0}
	b := []int16{50, -50, 300, 0}
	if !mixFrames(out, acc, [][]int16{a, b}, 100) {
		t.Fatal("mix of two frames should report true")
	}
	if out[0] != 150 || out[1] != -150 || out[2] != 500 {
		t.Errorf("sum = %v, want [150 -150 500 0]", out)
	}

	// Volume scaling (50%).
	mixFrames(out, acc, [][]int16{{1000, -1000, 0, 0}}, 50)
	if out[0] != 500 || out[1] != -500 {
		t.Errorf("50%% scale = %v, want [500 -500 0 0]", out)
	}

	// Clamp on overflow (two near-max samples must not wrap).
	mixFrames(out, acc, [][]int16{{30000, -30000, 0, 0}, {30000, -30000, 0, 0}}, 100)
	if out[0] != 32767 || out[1] != -32768 {
		t.Errorf("clamp = %v, want [32767 -32768 0 0]", out)
	}

	// Muted (vol 0) yields silence but still reports mixed.
	if !mixFrames(out, acc, [][]int16{{9999, 9999, 9999, 9999}}, 0) {
		t.Error("muted mix of real audio should still report true")
	}
	if out[0] != 0 {
		t.Errorf("muted out = %v, want zeros", out)
	}
}
