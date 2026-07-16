package render

import (
	"image"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// greenPage builds a uniform-green single-frame decoded fixture (distinct from the
// red decodedFixture) so a readback can tell the missingno page apart from a held
// previous sprite or blank.
func greenPage() *assets.Decoded {
	d := &assets.Decoded{Width: 64, Height: 64}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for p := 0; p < len(img.Pix); p += 4 {
		img.Pix[p+1], img.Pix[p+3] = 0xFF, 0xFF // G, A
	}
	d.Frames = append(d.Frames, img)
	d.Delays = append(d.Delays, 50*time.Millisecond)
	return d
}

// TestMissingnoOnlyOnConfirmedMissing pins the disjoint-state contract that keeps
// the placeholder from ever flashing over a still-loading sprite:
//
//	(a) an UNCACHED base with NO MarkMissing (a still-loading "ghost") draws
//	    hold-previous/blank EXACTLY as today — the pref-only regression trap: a
//	    pref-only missingno would fire here and regress hold-previous;
//	(b) after store.MarkMissing(base) the SAME render draws the shared MissingKey
//	    page (readback its green colour) instead of the previous character;
//	(c) after uploadTier(base) lands the real sprite the flag clears and the real
//	    sprite draws again.
//
// It uses a SYNTHETIC MissingKey page (green), not the embedded art, so it builds
// and runs without the //go:embed asset present (that lives in internal/ui).
func TestMissingnoOnlyOnConfirmedMissing(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	if err := store.Upload("spk", decodedFixture()); err != nil { // the "previous" sprite: uniform RED
		t.Fatal(err)
	}
	if err := store.UploadPinned(MissingKey, greenPage()); err != nil { // the placeholder: GREEN
		t.Fatal(err)
	}

	ct, err := NewCaptureTarget(ren, 512, 384)
	if err != nil {
		t.Skipf("capture target unavailable headlessly: %v", err)
	}
	defer ct.Close()

	vp := NewViewport(store)
	vp.SetSpriteLoadMode(SpriteLoadHoldPrev)
	vp.SetMissingno(true)

	scene := &courtroom.Scene{}
	scene.Speaker.Visible = true
	scene.Speaker.Active = "spk"

	centre := func() (r, g uint8) {
		img, err := ct.Capture(ren, func(dst sdl.Rect) {
			vp.Update(scene, 16*time.Millisecond)
			vp.Render(ren, scene, dst)
		})
		if err != nil {
			t.Skipf("ReadPixels unavailable headlessly: %v", err)
		}
		px := img.RGBAAt(256, 192)
		return px.R, px.G
	}

	// Setup: the cached red speaker draws and is remembered.
	if r, _ := centre(); r == 0 {
		t.Fatal("setup: cached speaker sprite did not draw")
	}

	// (a) Swap to an UNCACHED, NOT-yet-missing base. This is a still-loading ghost:
	//     hold-previous must bridge with the RED previous sprite; missingno (green)
	//     must NOT appear. This is the regression trap the disjoint-state gate exists
	//     for — a pref-only placeholder would draw green here.
	scene.Speaker.Active = "ghost"
	vp.Update(scene, 16*time.Millisecond)
	if r, g := centre(); r == 0 || g != 0 {
		t.Fatalf("still-loading ghost must hold the red previous sprite, not missingno; got r=%d g=%d", r, g)
	}

	// (b) Now mark the base conclusively missing (as drainWarnings would). The SAME
	//     render draws the GREEN MissingKey page, winning over hold-previous.
	store.MarkMissing("ghost")
	if !store.IsMissing("ghost") {
		t.Fatal("IsMissing should be true after MarkMissing")
	}
	if r, g := centre(); g == 0 || r != 0 {
		t.Errorf("confirmed-missing base must draw the missingno placeholder (green), got r=%d g=%d", r, g)
	}

	// The 0-alloc contract on the missingno DRAWING path specifically (not inherited
	// from the found-path bench): actively drawing the placeholder allocates nothing.
	allocs := testing.AllocsPerRun(200, func() {
		vp.Render(ren, scene, sdl.Rect{X: 0, Y: 0, W: 512, H: 384})
	})
	if allocs != 0 {
		t.Errorf("missingno render allocates %.1f/op, want 0", allocs)
	}

	// (c) The real sprite finally lands: uploadTier clears the missing flag and the
	//     real (its own colour) sprite draws. Upload a BLUE page so the readback is
	//     neither the red previous nor the green placeholder.
	blue := &assets.Decoded{Width: 64, Height: 64}
	bimg := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for p := 0; p < len(bimg.Pix); p += 4 {
		bimg.Pix[p+2], bimg.Pix[p+3] = 0xFF, 0xFF // B, A
	}
	blue.Frames = append(blue.Frames, bimg)
	blue.Delays = append(blue.Delays, 50*time.Millisecond)
	if err := store.Upload("ghost", blue); err != nil {
		t.Fatal(err)
	}
	if store.IsMissing("ghost") {
		t.Error("uploadTier must clear the missing flag when the real page lands")
	}
	if img, err := ct.Capture(ren, func(dst sdl.Rect) {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, dst)
	}); err == nil {
		px := img.RGBAAt(256, 192)
		if px.B == 0 || px.R != 0 || px.G != 0 {
			t.Errorf("after the real sprite lands it must draw (blue), got r=%d g=%d b=%d", px.R, px.G, px.B)
		}
	}
}

// TestMissingnoDisabledHoldsPrevious pins the opt-out: with the pref OFF, a
// confirmed-missing base falls back to the normal hold-previous chain (the red
// previous sprite), never the placeholder — the missingno probe is fully gated by
// the bool the App pushes each frame.
func TestMissingnoDisabledHoldsPrevious(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	if err := store.Upload("spk", decodedFixture()); err != nil { // red previous
		t.Fatal(err)
	}
	if err := store.UploadPinned(MissingKey, greenPage()); err != nil { // green placeholder
		t.Fatal(err)
	}
	ct, err := NewCaptureTarget(ren, 512, 384)
	if err != nil {
		t.Skipf("capture target unavailable headlessly: %v", err)
	}
	defer ct.Close()

	vp := NewViewport(store)
	vp.SetSpriteLoadMode(SpriteLoadHoldPrev)
	vp.SetMissingno(false) // OFF
	scene := &courtroom.Scene{}
	scene.Speaker.Visible = true
	scene.Speaker.Active = "spk"
	centre := func() (r, g uint8) {
		img, err := ct.Capture(ren, func(dst sdl.Rect) {
			vp.Update(scene, 16*time.Millisecond)
			vp.Render(ren, scene, dst)
		})
		if err != nil {
			t.Skipf("ReadPixels unavailable headlessly: %v", err)
		}
		px := img.RGBAAt(256, 192)
		return px.R, px.G
	}
	if r, _ := centre(); r == 0 {
		t.Fatal("setup: cached speaker did not draw")
	}
	scene.Speaker.Active = "ghost"
	vp.Update(scene, 16*time.Millisecond)
	store.MarkMissing("ghost")
	// Pref OFF: hold-previous (red) wins; the green placeholder must NOT draw.
	if r, g := centre(); r == 0 || g != 0 {
		t.Errorf("with the placeholder pref OFF a missing base must hold the previous sprite (red), got r=%d g=%d", r, g)
	}
}
