package host

import (
	"testing"
	"time"
)

const (
	testStallLimit = 30 * time.Millisecond
	testStallEvery = 2 * time.Millisecond
	// testStallWait is far past the limit, so a missed report is a real miss and not a slow machine.
	testStallWait = 2 * time.Second
)

// TestStallWatchReportsAStuckFrame: a frame left open past the limit is reported once, with how long it was stuck.
func TestStallWatchReportsAStuckFrame(t *testing.T) {
	reports := make(chan time.Duration, 1)
	w := NewStallWatch(testStallLimit, testStallEvery, time.Now, func(stuck time.Duration) { reports <- stuck })
	defer w.Close()

	w.FrameStart()
	select {
	case stuck := <-reports:
		if stuck < testStallLimit {
			t.Errorf("reported after %s, before the %s limit", stuck, testStallLimit)
		}
	case <-time.After(testStallWait):
		t.Fatal("a frame stuck past the limit was never reported")
	}
}

// TestStallWatchIgnoresFinishedFrames: frames that finish never count, however long the client runs.
func TestStallWatchIgnoresFinishedFrames(t *testing.T) {
	reports := make(chan time.Duration, 1)
	w := NewStallWatch(testStallLimit, testStallEvery, time.Now, func(stuck time.Duration) { reports <- stuck })
	defer w.Close()

	deadline := time.Now().Add(testStallLimit * sleepGapTicks)
	for time.Now().Before(deadline) {
		w.FrameStart()
		w.FrameDone()
		time.Sleep(testStallEvery)
	}
	select {
	case stuck := <-reports:
		t.Errorf("finished frames were reported as stuck for %s", stuck)
	case <-time.After(testStallLimit * sleepGapTicks):
	}
}

// TestStallWatchDoesNotCountSleep: a machine that sleeps mid-frame wakes with the clock far ahead; that is not a stall,
// but the same frame still stuck a full limit after waking is.
func TestStallWatchDoesNotCountSleep(t *testing.T) {
	const asleep = time.Hour
	start := time.Unix(0, 0)
	clock := start
	w := &StallWatch{limit: testStallLimit, every: testStallEvery, now: func() time.Time { return clock }, base: start, lastCheck: start}

	w.FrameStart()
	clock = clock.Add(asleep)
	if stuck, stalled := w.check(clock); stalled {
		t.Fatalf("waking from sleep counted as a %s stall", stuck)
	}
	for elapsed := time.Duration(0); elapsed < testStallLimit; elapsed += testStallEvery {
		clock = clock.Add(testStallEvery)
		if stuck, stalled := w.check(clock); stalled && stuck < testStallLimit {
			t.Fatalf("reported %s after waking, before the limit", stuck)
		}
	}
	clock = clock.Add(testStallEvery)
	if _, stalled := w.check(clock); !stalled {
		t.Error("the frame stayed stuck a full limit after waking, but no stall was reported")
	}
}
