package ui

import "testing"

// TestEvidDescViewH pins Issue #15's description viewport sizing: it takes
// whatever room is left above the fixed trailing block (image line +
// buttons), and never collapses below evidDescMinViewH even in a
// small/mid-resize panel.
func TestEvidDescViewH(t *testing.T) {
	// evidPanelMinH (240) geometry: contentTop = r.Y+38, descTop = +22 more,
	// panelBottom = r.Y+240-pad(8) = r.Y+232. At the panel's documented floor
	// this must still be comfortably above evidDescMinViewH.
	const rY = int32(1000)
	descTop := rY + 38 + 22
	panelBottom := rY + evidPanelMinH - pad
	got := evidDescViewH(panelBottom, descTop)
	if got < evidDescMinViewH {
		t.Fatalf("evidDescViewH = %d, must never go below the floor %d", got, evidDescMinViewH)
	}
	if got <= evidDescMinViewH {
		t.Fatalf("evidDescViewH = %d, expected room above the floor at the panel's default minimum height", got)
	}

	// A degenerate/tiny gap must clamp to the floor, not go negative.
	if got := evidDescViewH(descTop+5, descTop); got != evidDescMinViewH {
		t.Fatalf("evidDescViewH with almost no room = %d, want the floor %d", got, evidDescMinViewH)
	}

	// Monotonic: a taller panel gives a taller viewport.
	tall := evidDescViewH(panelBottom+200, descTop)
	if tall <= got {
		t.Fatalf("evidDescViewH must grow with panel height: tall=%d, base=%d", tall, got)
	}
}
