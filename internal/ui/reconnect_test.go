package ui

import (
	"testing"
	"time"
)

// TestAutoReconnectDelay pins the backoff: base on the first attempt, doubling,
// then clamped at the max — clamped for negatives and overflow-safe for large
// counts, and monotonic non-decreasing up to the try cap.
func TestAutoReconnectDelay(t *testing.T) {
	cases := []struct {
		tries int
		want  time.Duration
	}{
		{-1, autoReconnectBase},    // clamped to 0
		{0, autoReconnectBase},     // 2s
		{1, 2 * autoReconnectBase}, // 4s
		{2, 4 * autoReconnectBase}, // 8s
		{3, 8 * autoReconnectBase}, // 16s
		{4, autoReconnectMax},      // 32s > 30s → capped
		{8, autoReconnectMax},      // capped
		{99, autoReconnectMax},     // overflow-safe (early return)
	}
	for _, tc := range cases {
		if got := autoReconnectDelay(tc.tries); got != tc.want {
			t.Errorf("autoReconnectDelay(%d) = %v, want %v", tc.tries, got, tc.want)
		}
	}
	prev := time.Duration(0)
	for i := 0; i <= 20; i++ { // sample well past the point autoReconnectDelay clamps at autoReconnectMax
		d := autoReconnectDelay(i)
		if d < prev {
			t.Errorf("backoff decreased at attempt %d: %v < %v", i, d, prev)
		}
		if d > autoReconnectMax {
			t.Errorf("backoff %v exceeds max %v at attempt %d", d, autoReconnectMax, i)
		}
		prev = d
	}
}
