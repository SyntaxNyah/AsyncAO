package ui

// themeFrame's half of the animation contract (L22).
//
// themeFrame and elementFrame were documented as twins and had already stopped
// being twins: elementFrame honoured the ReduceMotion latch and themeFrame did
// not. On a themed install with the accessibility option ON, the free elements
// went still while the theme's own chrome — chatbox, buttons, backdrops — kept
// animating, and themeFrame's census kept the pacer at the animation cadence.
// The person who turned it on got neither the stillness nor the idle frame rate.
//
// The suite RECORDED that divergence instead of failing on it: two gates in
// themeclock_test.go t.Skip when the fixture's own chrome notes the census under
// ReduceMotion, which is exactly the symptom. These gates drive themeFrame
// itself, so there is nothing to skip.

import (
	"image"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// animatedThemePage uploads a four-frame page and hands back the store entry, so
// the gates below run against a real *render.TexturePage rather than a struct
// literal that no uploader ever produced.
func animatedThemePage(t *testing.T, a *App, key string) *render.TexturePage {
	t.Helper()
	const frames = 4
	// Animated: true is the flag pageFrameLoop gates on, not the frame count —
	// a multi-frame page that never declared itself animated holds frame 0.
	dec := &assets.Decoded{Width: 2, Height: 2, Animated: true}
	for i := 0; i < frames; i++ {
		dec.Frames = append(dec.Frames, image.NewRGBA(image.Rect(0, 0, 2, 2)))
		dec.Delays = append(dec.Delays, 40*time.Millisecond)
	}
	if err := a.d.Store.UploadPinned(key, dec); err != nil {
		t.Fatalf("upload: %v", err)
	}
	page, ok := a.d.Store.Get(key)
	if !ok || page == nil || len(page.Frames) != frames {
		t.Fatalf("the uploaded page came back with %v frames, want %d", page, frames)
	}
	if !page.Animated {
		t.Fatal("the uploaded page is not marked animated — pageFrameLoop would hold frame 0 and every " +
			"assertion below would be measuring a still picture")
	}
	return page
}

// TestThemeChromeGoesStillUnderReduceMotion is the accessibility promise stated
// where it is kept: with ReduceMotion on, an animated theme page holds frame 0
// AND stops noting the animation census, so the client can fall to the idle
// cadence instead of spinning at the animation rate for a picture that is not
// moving.
func TestThemeChromeGoesStillUnderReduceMotion(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()
	page := animatedThemePage(t, a, themeTexKey("gatechatbox"))

	// The theme clock well past frame 0, so a moving page would be somewhere else.
	a.toolbox.themeAt = a.now().Add(-time.Second)

	// Motion ALLOWED: the page steps and the census fires. This half is the
	// non-vacuity check — without it a themeFrame hardwired to frame 0 would pass
	// the accessibility half perfectly.
	a.d.Prefs.Update(func(sn *config.Settings) { sn.ReduceMotionOn = false })
	a.themeFXFrozen = a.reduceMotionNow()
	a.frameAnimChrome = false
	moving := a.themeFrame(page)
	if moving == page.Frames[0] {
		t.Error("an animated theme page held frame 0 a whole second in — the chrome never steps")
	}
	if !a.frameAnimChrome {
		t.Error("an animated theme page did not note the census — the pacer would idle and freeze it")
	}

	// Motion SUPPRESSED.
	a.d.Prefs.Update(func(sn *config.Settings) { sn.ReduceMotionOn = true })
	defer a.d.Prefs.Update(func(sn *config.Settings) { sn.ReduceMotionOn = false })
	a.themeFXFrozen = a.reduceMotionNow()
	a.frameAnimChrome = false
	still := a.themeFrame(page)
	if still != page.Frames[0] {
		t.Error("theme chrome kept animating under ReduceMotion — a person who turns it on because motion " +
			"makes them ill is not helped by a client that stills the elements and not the chatbox")
	}
	if a.frameAnimChrome {
		t.Error("theme chrome noted the animation census under ReduceMotion — the client holds the animation " +
			"frame rate for a picture that is not moving, so the option delivers neither stillness nor the idle rate")
	}
}

// TestThemeFrameAndElementFrameShareOneIndexer keeps the two from drifting apart
// again. They differ in WHOSE CLOCK they read and in nothing else, so given the
// same elapsed and a looping page they must select the same frame.
//
// Stated as an identity over the shared pure core rather than as two hardcoded
// numbers: elementFrameIndex is what both call, and the day one of them stops
// calling it this is what says so.
func TestThemeFrameAndElementFrameShareOneIndexer(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()
	page := animatedThemePage(t, a, themeTexKey("gatebutton"))
	a.d.Prefs.Update(func(sn *config.Settings) { sn.ReduceMotionOn = false })
	a.themeFXFrozen = false

	for _, el := range []time.Duration{0, 45 * time.Millisecond, 130 * time.Millisecond, time.Second} {
		a.toolbox.themeAt = a.now().Add(-el)
		want, _ := elementFrameIndex(page, a.themeElapsed(), true)
		got := a.themeFrame(page)
		if got != page.Frames[want] {
			t.Errorf("at elapsed %v themeFrame chose a different frame from elementFrameIndex (want index %d) — "+
				"the two are one contract with two clocks, not two implementations", el, want)
		}
	}
}
