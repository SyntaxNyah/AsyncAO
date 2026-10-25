package ui

import (
	"math"
	"testing"
)

// TestResumeSeek pins the wave-13 exact, loop-aware cross-tab resume math
// (resumeSeek), the code that ACTUALLY ships in resumeActiveTabMusic. It covers:
// loop wrapping (including multi-wrap of a short loop parked a long time), a
// non-looping one-shot that ended while parked (play=false, don't restart),
// unknown duration (dur<=0 → seek 0, honest restart-from-top), and a negative
// elapsed (clock skew) clamping so it never produces a negative seek.
func TestResumeSeek(t *testing.T) {
	const eps = 1e-9
	cases := []struct {
		name             string
		snapPos, snapDur float64
		elapsed          float64
		loop             bool
		wantSeek         float64
		wantPlay         bool
	}{
		{
			name: "loop within one cycle", snapPos: 30, snapDur: 180, elapsed: 20,
			loop: true, wantSeek: 50, wantPlay: true, // 30+20 = 50, < 180
		},
		{
			name: "loop single wrap", snapPos: 170, snapDur: 180, elapsed: 20,
			loop: true, wantSeek: 10, wantPlay: true, // 190 mod 180 = 10
		},
		{
			name: "loop multi-wrap (3-min loop parked 7 min)", snapPos: 0, snapDur: 180, elapsed: 420,
			loop: true, wantSeek: 60, wantPlay: true, // 420 mod 180 = 60 (1:00 into the loop)
		},
		{
			name: "loop exact boundary wraps to 0", snapPos: 0, snapDur: 180, elapsed: 180,
			loop: true, wantSeek: 0, wantPlay: true, // 180 mod 180 = 0
		},
		{
			name: "non-loop still within track", snapPos: 30, snapDur: 180, elapsed: 20,
			loop: false, wantSeek: 50, wantPlay: true,
		},
		{
			name: "non-loop ended while parked", snapPos: 170, snapDur: 180, elapsed: 20,
			loop: false, wantSeek: 0, wantPlay: false, // 190 >= 180: finished, don't restart
		},
		{
			name: "non-loop exact end is ended", snapPos: 90, snapDur: 180, elapsed: 90,
			loop: false, wantSeek: 0, wantPlay: false, // 180 >= 180
		},
		{
			name: "unknown duration restarts from top (loop)", snapPos: 42, snapDur: -1, elapsed: 100,
			loop: true, wantSeek: 0, wantPlay: true,
		},
		{
			name: "unknown duration restarts from top (non-loop)", snapPos: 42, snapDur: 0, elapsed: 100,
			loop: false, wantSeek: 0, wantPlay: true,
		},
		{
			name: "negative elapsed clamps (loop)", snapPos: 30, snapDur: 180, elapsed: -5,
			loop: true, wantSeek: 30, wantPlay: true, // clamps to elapsed 0 → base 30
		},
		{
			name: "negative elapsed clamps (non-loop)", snapPos: 30, snapDur: 180, elapsed: -5,
			loop: false, wantSeek: 30, wantPlay: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seek, play := resumeSeek(c.snapPos, c.snapDur, c.elapsed, c.loop)
			if play != c.wantPlay {
				t.Errorf("play = %v, want %v", play, c.wantPlay)
			}
			if math.Abs(seek-c.wantSeek) > eps {
				t.Errorf("seek = %v, want %v", seek, c.wantSeek)
			}
			// Invariant: a resumed seek is never negative (a negative
			// Mix_SetMusicPosition would fail and silently restart).
			if seek < 0 {
				t.Errorf("seek = %v is negative", seek)
			}
		})
	}
}
