package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// The sprite preview's PIN. previewPinned already existed, but the only writer was
// a right-click on a CLASSIC emote-grid cell — undiscoverable, absent from every
// other trigger surface, and unreachable entirely on an AO2 theme. These pin the
// visible control, its persistence, and the five close paths that ignored the pin.

// TestPreviewChromeRectsAreDisjointAndInsideTheFrame pins the shared rect helpers.
// The close button's rect used to be written literally TWICE — once in the draw
// and once in the input handler — which is one edit away from a control whose
// pixels and hit box disagree. Extended for the pop-out button (v1.93.0
// preview-window-wire) rather than left behind it: a third chrome slot is
// exactly the kind of addition that silently overlaps an existing one if
// nothing re-checks the whole row.
func TestPreviewChromeRectsAreDisjointAndInsideTheFrame(t *testing.T) {
	for _, frame := range []sdl.Rect{
		{X: 0, Y: 0, W: 200, H: 240},
		{X: 613, Y: 87, W: 331, H: 420},
		{X: 4, Y: 4, W: 80, H: 100}, // the narrowest the box ever gets
	} {
		detach, pin, closeB := previewDetachRect(frame), previewPinRect(frame), previewCloseRect(frame)
		slots := map[string]sdl.Rect{"detach": detach, "pin": pin, "close": closeB}
		for aName, aR := range slots {
			for bName, bR := range slots {
				if aName == bName {
					continue
				}
				if _, hit := aR.Intersect(&bR); hit {
					t.Errorf("frame %+v: %s %+v overlaps %s %+v — one click would fire both", frame, aName, aR, bName, bR)
				}
			}
			if aR.X < frame.X || aR.X+aR.W > frame.X+frame.W || aR.Y < frame.Y || aR.Y+aR.H > frame.Y+frame.H {
				t.Errorf("frame %+v: %s %+v is outside the box", frame, aName, aR)
			}
		}
		// The name strip's reserve must actually cover all three slots, or a long
		// emote name runs under the chrome.
		if got := previewChromeW; got < (closeB.X+closeB.W)-detach.X {
			t.Errorf("previewChromeW = %d, too small for the three slots spanning %d px", got, (closeB.X+closeB.W)-detach.X)
		}
	}
}

// TestPreviewPinLatchAndPrefBothHoldTheBox pins previewIsPinned: the per-session
// latch (a right-click open) OR the sticky preference keeps the box up.
func TestPreviewPinLatchAndPrefBothHoldTheBox(t *testing.T) {
	a := testTabApp(t)
	if a.previewIsPinned() {
		t.Fatal("a fresh session must not be pinned (the pref defaults OFF)")
	}
	a.previewPinned = true
	if !a.previewIsPinned() {
		t.Error("the session latch alone must pin")
	}
	a.previewPinned = false
	a.d.Prefs.SetPreviewPinned(true)
	if !a.previewIsPinned() {
		t.Error("the preference alone must pin")
	}
}

// TestTogglePreviewPinClearsBoth pins the reason the toggle writes BOTH: with the
// pref on, clearing only the latch would leave the pref holding the box up and the
// pin button would look broken.
func TestTogglePreviewPinClearsBoth(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetPreviewPinned(true)
	a.previewPinned = false
	a.togglePreviewPin()
	if a.previewIsPinned() || a.previewPinned || a.d.Prefs.PreviewPinnedOn() {
		t.Fatalf("unpinning left it pinned: latch=%v pref=%v", a.previewPinned, a.d.Prefs.PreviewPinnedOn())
	}
	a.togglePreviewPin()
	if !a.previewPinned || !a.d.Prefs.PreviewPinnedOn() {
		t.Fatalf("pinning must set both: latch=%v pref=%v", a.previewPinned, a.d.Prefs.PreviewPinnedOn())
	}
}

// TestDismissPreviewOnClickRespectsThePinAndDisarmsTheDwell pins the five close
// paths the recon found: the background pickers, the two wardrobe grids and the
// AO2 THEMED emote grid all dismissed with a raw `a.previewBase = ""`, which
// ignored the pin AND skipped closeSpritePreview — so the trigger's dwell id was
// never cleared and the already-elapsed hover re-opened the box the very next
// frame (the trap closeSpritePreview documents).
func TestDismissPreviewOnClickRespectsThePinAndDisarmsTheDwell(t *testing.T) {
	a := testTabApp(t)
	a.previewBase = "char/x/(a)normal"
	a.ctx.hoverID = "emote:normal"
	a.ctx.clicked = true

	a.previewPinned = true
	a.dismissPreviewOnClick()
	if a.previewBase == "" {
		t.Fatal("a PINNED box must survive a click elsewhere — its x is the only way out")
	}

	a.previewPinned = false
	a.dismissPreviewOnClick()
	if a.previewBase != "" {
		t.Fatal("an unpinned box must dismiss on a click")
	}
	if a.ctx.hoverID != "" {
		t.Fatal("the dismiss must route through closeSpritePreview so the dwell id is disarmed")
	}
}

// TestClosePreviewClearsTheLatchNotThePreference pins the split: closing one box
// is not "stop pinning previews". The screen-switch orphan drop (which calls
// closeSpritePreview) must not silently wipe a setting the user chose.
func TestClosePreviewClearsTheLatchNotThePreference(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetPreviewPinned(true)
	a.previewBase, a.previewPinned = "x", true
	a.closeSpritePreview()
	if a.previewPinned {
		t.Error("closing must clear the session latch")
	}
	if !a.d.Prefs.PreviewPinnedOn() {
		t.Error("closing must NOT clear the sticky preference")
	}
}
