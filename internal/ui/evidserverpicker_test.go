package ui

import "testing"

// TestServerEvidenceEmptyLabel pins the empty-grid message selection: while the
// server index is still loading we say so, a typed filter that matches nothing
// says so, and only a genuinely empty (finished) index says "none found". Busy
// wins over a filter — the user shouldn't see "no match" before the index loads.
func TestServerEvidenceEmptyLabel(t *testing.T) {
	cases := []struct {
		busy, hasFilter bool
		want            string
	}{
		{true, false, "Loading server evidence…"},
		{true, true, "Loading server evidence…"},
		{false, true, "No server evidence images match."},
		{false, false, "No server evidence images found."},
	}
	for _, tc := range cases {
		if got := serverEvidenceEmptyLabel(tc.busy, tc.hasFilter); got != tc.want {
			t.Errorf("serverEvidenceEmptyLabel(%v,%v) = %q, want %q", tc.busy, tc.hasFilter, got, tc.want)
		}
	}
}

// TestPollServerEvidencePickerResetsBusy verifies the retry lifecycle: once the
// index fetch's result is consumed, evidServerBusy clears so a later open (after
// a timeout or error) re-runs the fetch instead of dead-ending empty forever.
func TestPollServerEvidencePickerResetsBusy(t *testing.T) {
	a := &App{}
	a.evidServerRes = make(chan []string, 4)
	a.evidServerBusy = true
	a.evidServerRes <- []string{"Knife.png", "Gun.png"}
	a.pollServerEvidencePicker()
	if a.evidServerBusy {
		t.Fatal("poll must clear evidServerBusy once the result is consumed")
	}
	if len(a.evidServerNames) != 2 {
		t.Fatalf("merged %d names, want 2", len(a.evidServerNames))
	}
}
