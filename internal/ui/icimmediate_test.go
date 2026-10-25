package ui

import "testing"

// TestOutgoingImmediateRequiresPre pins the #122 fix: the IMMEDIATE wire flag must
// be off whenever the "Pre" toggle is off, no matter what the Immediate toggle says.
// Sending IMMEDIATE=1 on its own still read as an immediate preanimation on the
// receiving end, which is the reported bug.
func TestOutgoingImmediateRequiresPre(t *testing.T) {
	cases := []struct {
		name               string
		immediate, preanim bool
		want               bool
	}{
		{"both off", false, false, false},
		{"immediate on, pre off — the bug", true, false, false},
		{"pre on, immediate off", false, true, false},
		{"both on", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := outgoingImmediate(tc.immediate, tc.preanim); got != tc.want {
				t.Errorf("outgoingImmediate(%v, %v) = %v, want %v", tc.immediate, tc.preanim, got, tc.want)
			}
		})
	}
}
