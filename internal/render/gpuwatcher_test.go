package render

import (
	"errors"
	"testing"
	"time"
)

var errPresentFailed = errors.New("present failed")

// fakeClock is a clock a test moves by hand.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// loseDevice fails presents until the watcher reports the loss, returning its
// recovery.
func loseDevice(w *GPUWatcher) GPURecovery {
	got := GPUWorking
	for i := 0; i < presentFailuresForLoss; i++ {
		got = w.Observe(errPresentFailed)
	}
	return got
}

// TestGPUWatcherDetectsALostDevice pins the loss signal: presents failing several
// frames in a row mean the device is gone, while failures broken by a success — a
// swapchain the driver is rebuilding — do not, and the loss is reported once.
// A real device loss cannot be caused on demand, so the failures are injected.
func TestGPUWatcherDetectsALostDevice(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	w := NewGPUWatcher(0, false, clock.now)
	for i := 1; i < presentFailuresForLoss; i++ {
		if got := w.Observe(errPresentFailed); got != GPUWorking {
			t.Fatalf("after %d failed presents: %v, want %v", i, got, GPUWorking)
		}
	}
	if got := w.Observe(nil); got != GPUWorking {
		t.Fatalf("a successful present: %v, want %v", got, GPUWorking)
	}
	for i := 1; i < presentFailuresForLoss; i++ {
		if got := w.Observe(errPresentFailed); got != GPUWorking {
			t.Fatalf("failures before a success counted toward the loss: %v", got)
		}
	}
	if got := w.Observe(errPresentFailed); got != GPURestart {
		t.Errorf("%d failed presents in a row: %v, want %v", presentFailuresForLoss, got, GPURestart)
	}
	if got := w.Observe(errPresentFailed); got != GPUWorking {
		t.Errorf("the loss was reported twice: %v", got)
	}
}

// TestGPUWatcherEscalates pins the recovery for each loss in a row: restart, then
// restart on the software driver, then stop instead of restarting forever — but
// only for a loss soon after the restart. A loss after lossRepeatWindow is a new
// first loss, so a client that ran for days after one restart stays on hardware.
func TestGPUWatcherEscalates(t *testing.T) {
	cases := []struct {
		name       string
		recoveries int
		software   bool
		after      time.Duration
		want       GPURecovery
	}{
		{"first loss", 0, false, 0, GPURestart},
		{"first loss, already on software", 0, true, 0, GPURestart},
		{"lost again right after a restart", 1, false, time.Second, GPURestartSoftware},
		{"lost again on software", 1, true, time.Second, GPUGiveUp},
		{"lost again after the software restart", 2, true, time.Second, GPUGiveUp},
		{"lost long after a restart", 1, false, lossRepeatWindow, GPURestart},
		{"lost long after the software restart", 2, true, lossRepeatWindow, GPURestart},
	}
	for _, tc := range cases {
		clock := &fakeClock{t: time.Unix(0, 0)}
		w := NewGPUWatcher(tc.recoveries, tc.software, clock.now)
		clock.t = clock.t.Add(tc.after)
		if got := loseDevice(w); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
