package ui

import "testing"

// TestMayoPetFlavor pins the #234 pet-the-gopher captions: milestones fire at the
// EXACT pet count (not >=), and every other pet shows the running counter.
func TestMayoPetFlavor(t *testing.T) {
	cases := map[int]string{
		1:    "You petted Mayo!",
		30:   "Mayo shares her burger (30)",
		50:   "Mayo loves you! (50)",
		200:  "OBJECTION! ...to you stopping (200)",
		1000: "Mayo eternal. You win. (1000)",
		3:    "*pet pet*  (3)",
		51:   "*pet pet*  (51)", // just past a milestone reverts to the counter
	}
	for n, want := range cases {
		if got := mayoPetFlavor(n); got != want {
			t.Errorf("mayoPetFlavor(%d) = %q, want %q", n, got, want)
		}
	}
}
