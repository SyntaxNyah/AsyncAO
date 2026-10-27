package value_test

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

const (
	low    = 10
	high   = 20
	inside = 15
	below  = 5
	above  = 25
)

func TestClampInts(t *testing.T) {
	cases := []struct {
		name      string
		v, lo, hi int
		want      int
	}{
		{"inside", inside, low, high, inside},
		{"below", below, low, high, low},
		{"above", above, low, high, high},
		{"at low", low, low, high, low},
		{"at high", high, low, high, high},
		{"empty range", inside, low, low, low},
		{"inverted, below both", below, high, low, high},
		{"inverted, between", inside, high, low, high},
		{"inverted, above both", above, high, low, high},
	}
	for _, tc := range cases {
		if got := value.Clamp(tc.v, tc.lo, tc.hi); got != tc.want {
			t.Errorf("%s: Clamp(%d, %d, %d) = %d, want %d", tc.name, tc.v, tc.lo, tc.hi, got, tc.want)
		}
	}
}

func TestClampOtherOrderedTypes(t *testing.T) {
	const (
		fracLow  = 0.25
		fracHigh = 0.75
		fracOver = 0.9
	)
	if got := value.Clamp(fracOver, fracLow, fracHigh); got != fracHigh {
		t.Errorf("float64: got %v, want %v", got, fracHigh)
	}
	if got := value.Clamp(float32(fracOver), fracHigh, fracLow); got != fracHigh {
		t.Errorf("inverted float32: got %v, want the low bound %v", got, fracHigh)
	}
	if got := value.Clamp(int32(below), low, high); got != low {
		t.Errorf("int32: got %v, want %v", got, low)
	}
	if got := value.Clamp(above*time.Millisecond, low*time.Millisecond, high*time.Millisecond); got != high*time.Millisecond {
		t.Errorf("Duration: got %v, want %v", got, high*time.Millisecond)
	}
	if got := value.Clamp("m", "a", "k"); got != "k" {
		t.Errorf("string: got %q, want %q", got, "k")
	}
}

func TestClampSetKeepsUnset(t *testing.T) {
	cases := []struct{ v, want int }{
		{0, 0},
		{below, low},
		{-below, low},
		{inside, inside},
		{above, high},
	}
	for _, tc := range cases {
		if got := value.ClampSet(tc.v, low, high); got != tc.want {
			t.Errorf("ClampSet(%d, %d, %d) = %d, want %d", tc.v, low, high, got, tc.want)
		}
	}
}
