package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestSfxBrowserHidesTheExtrasBox: opened from the Extras box, the SFX Browser shows in front, as the Wardrobe and
// Jukebox do; the box comes back when it closes (#32).
func TestSfxBrowserHidesTheExtrasBox(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	a.showWidgets = true

	a.toggleSfxBrowser()
	if a.extrasSurfaceLive() {
		t.Error("the Extras box still draws over the open SFX Browser")
	}
	a.toggleSfxBrowser()
	if !a.extrasSurfaceLive() {
		t.Error("the Extras box did not come back after the SFX Browser closed")
	}
}
