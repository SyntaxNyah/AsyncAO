package network

import (
	"testing"
	"time"
)

// TestAdaptiveLatencyMultipleKnob pins the power-user deadline multiple: the
// default ×8, an override, the clamps, and 0 = back to default. Samples are
// chosen so the results sit between the 2 s floor and the 5 s global timeout.
func TestAdaptiveLatencyMultipleKnob(t *testing.T) {
	c := newClient(DefaultRequestTimeout, NotFoundCacheTTL)
	const host = "assets.example"
	c.observeLatency(host, 500*time.Millisecond)

	if got := c.adaptiveTimeout(host); got != 4*time.Second {
		t.Fatalf("default multiple: deadline = %v, want 4s (8 × 500ms)", got)
	}
	c.SetAdaptiveLatencyMultiple(5)
	if got := c.adaptiveTimeout(host); got != 2500*time.Millisecond {
		t.Errorf("×5: deadline = %v, want 2.5s", got)
	}
	c.SetAdaptiveLatencyMultiple(16) // 8s caps at the global timeout
	if got := c.adaptiveTimeout(host); got != DefaultRequestTimeout {
		t.Errorf("×16: deadline = %v, want the %v global cap", got, DefaultRequestTimeout)
	}
	c.SetAdaptiveLatencyMultiple(0) // back to the built-in default
	if got := c.adaptiveTimeout(host); got != 4*time.Second {
		t.Errorf("reset: deadline = %v, want 4s again", got)
	}
	c.SetAdaptiveLatencyMultiple(1000) // defensive ceiling
	if got := c.adaptiveTimeout(host); got != DefaultRequestTimeout {
		t.Errorf("runaway multiple must clamp: deadline = %v", got)
	}

	// The global TTFB EWMA (cold-load profiling) folded the sample too.
	if got := c.AvgTTFB(); got != 500*time.Millisecond {
		t.Errorf("AvgTTFB = %v, want the single 500ms sample", got)
	}
	// NewClientNotFoundTTL(0) must fall back to the default TTL, not a zero-TTL cache.
	if c2 := NewClientNotFoundTTL(0); c2 == nil {
		t.Fatal("NewClientNotFoundTTL(0) returned nil")
	}
}
