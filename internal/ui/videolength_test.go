package ui

import "testing"

// TestVideoMaxFrames pins the duration wedge-brake math: the video frame cap is
// maxVideoHours worth of frames at the CHOSEN fps (24 h × 3600 s × fps), so the brake
// scales with cadence and a 24 h film is 24 h at any preset. Pure + headless.
func TestVideoMaxFrames(t *testing.T) {
	for _, fps := range []int{8, 12, 15, 24} {
		want := maxVideoHours * 3600 * fps
		if got := videoMaxFrames(fps); got != want {
			t.Errorf("videoMaxFrames(%d) = %d, want %d", fps, got, want)
		}
	}
	// The true maximum-length case is the CONFIG clamp's ceiling (maxExportFPS=30 —
	// the UI presets top out at 24, but the stored pref may be anything in [6,30]):
	// 24*3600*30 = 2,592,000 frames. Pin both it and the top preset, and confirm
	// both dwarf the old flat 18000 brake so hours-long exports aren't cut short.
	if got := videoMaxFrames(30); got != 2_592_000 {
		t.Errorf("videoMaxFrames(30) = %d, want 2592000 (24h @ the 30fps config ceiling)", got)
	}
	if got := videoMaxFrames(24); got != 2_073_600 {
		t.Errorf("videoMaxFrames(24) = %d, want 2073600 (24h @ 24fps)", got)
	}
	if videoMaxFrames(24) <= 18000 {
		t.Errorf("the raised brake (%d) must far exceed the old 18000-frame cap", videoMaxFrames(24))
	}
	// A bogus fps clamps to 1 (never a divide-by-zero or a zero cap).
	if got := videoMaxFrames(0); got != maxVideoHours*3600 {
		t.Errorf("videoMaxFrames(0) = %d, want the fps=1 clamp %d", got, maxVideoHours*3600)
	}
}

// TestFrameToMs is the drift-fix guard: cue offsets MUST be computed multiply-first
// (frame*1000/fps), not frame*(1000/fps). At 24 fps a truncated per-frame constant
// (1000/24 = 41 ms) drifts 0.667 ms every frame — ≈23 minutes of audio/subtitle
// desync by the end of a 24 h export. Multiply-first stays exact to the millisecond.
func TestFrameToMs(t *testing.T) {
	// Exact-divisor fps: identical either way (sanity anchor).
	if got := frameToMs(10, 20); got != 500 {
		t.Errorf("frameToMs(10,20) = %d, want 500", got)
	}
	// 24 fps, the drift case. Truncate-first (frame*41) would give 984 / 2952 /
	// 85,017,600 — early and worsening; multiply-first is exact: 1000 ms, 3000 ms,
	// and EXACTLY 24 h (86,400,000 ms) at the full-length final frame.
	cases := []struct {
		frame, fps, wantMs int
	}{
		{24, 24, 1000},
		{72, 24, 3000},
		{2_073_600, 24, 86_400_000},
	}
	for _, c := range cases {
		if got := frameToMs(c.frame, c.fps); got != c.wantMs {
			t.Errorf("frameToMs(%d,%d) = %d, want %d", c.frame, c.fps, got, c.wantMs)
		}
	}
	// The last-frame drift a truncated constant would accumulate must be enormous
	// (many minutes), proving why the pre-truncated form was unshippable at scale.
	const lastFrame, fps = 2_073_600, 24
	truncated := lastFrame * (1000 / fps) // the OLD, wrong math
	exact := frameToMs(lastFrame, fps)
	if drift := exact - truncated; drift < 20*60*1000 { // expect >20 minutes
		t.Errorf("expected the old truncated math to drift >20 min at the 24h/24fps end; drift=%d ms", drift)
	}
	// fps clamps to 1 rather than dividing by zero.
	if got := frameToMs(5, 0); got != 5000 {
		t.Errorf("frameToMs(5,0) = %d, want the fps=1 clamp 5000", got)
	}
}
