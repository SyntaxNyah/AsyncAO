package render

import (
	"image"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestSpriteLoadHoldPrevious pins the power-user cold-load renderer setting: with
// SpriteLoadHoldPrev the layer's PREVIOUS sprite stays on screen while a NEW,
// uncached sprite is still streaming (webAO-style), instead of flashing empty
// (SpriteLoadBlank, the default). It also pins the isolation guarantees the design
// leans on — lastGood survives the base swap, the held path never advances the
// held animation (so OnPreanimDone can't fire for the wrong sprite and corrupt the
// message state machine), and holding stays 0-alloc.
func TestSpriteLoadHoldPrevious(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	if err := store.Upload("spk", decodedFixture()); err != nil { // the cached sprite (uniform red)
		t.Fatal(err)
	}

	ct, err := NewCaptureTarget(ren, 512, 384) // a well-defined, black-cleared readback target
	if err != nil {
		t.Skipf("capture target unavailable headlessly: %v", err)
	}
	defer ct.Close()

	vp := NewViewport(store)
	preanims := 0
	vp.OnPreanimDone = func() { preanims++ }

	scene := &courtroom.Scene{} // speaker-only: no background/desk/pair, so the stage centre is sprite-or-black
	scene.Speaker.Visible = true
	scene.Speaker.Active = "spk"

	// spriteAtCentre captures a frame and reports whether the bottom-centered sprite
	// drew at the stage centre — its uniform red fixture (R>0) over the black clear.
	spriteAtCentre := func() bool {
		img, err := ct.Capture(ren, func(dst sdl.Rect) {
			vp.Update(scene, 16*time.Millisecond)
			vp.Render(ren, scene, dst)
		})
		if err != nil {
			t.Skipf("ReadPixels unavailable headlessly: %v", err)
		}
		return img.RGBAAt(256, 192).R > 0
	}

	// 1) A cached speaker draws, and the layer remembers it.
	if !spriteAtCentre() {
		t.Fatal("cached speaker sprite did not draw")
	}
	if vp.speakerAnim.lastGood != "spk" {
		t.Fatalf("lastGood = %q, want \"spk\"", vp.speakerAnim.lastGood)
	}

	// 2) Switch to an UNCACHED sprite. Blank (default) draws nothing in the gap, and
	//    lastGood must survive the base swap for hold-previous to have something to show.
	scene.Speaker.Active = "ghost"
	vp.SetSpriteLoadMode(SpriteLoadBlank)
	if spriteAtCentre() {
		t.Error("SpriteLoadBlank drew a sprite for an uncached base (want the empty cold-load gap)")
	}
	if vp.speakerAnim.lastGood != "spk" {
		t.Errorf("lastGood must survive the base swap, got %q", vp.speakerAnim.lastGood)
	}

	// 3) Hold-previous keeps the last sprite (spk) on screen through the gap.
	vp.SetSpriteLoadMode(SpriteLoadHoldPrev)
	if !spriteAtCentre() {
		t.Error("SpriteLoadHoldPrev did not hold the previous sprite during the cold-load gap")
	}

	// Isolation: an uncached sprite is never resolved by Update, so it's never
	// advanced and no preanim can complete — the message state machine is untouched.
	if preanims != 0 {
		t.Errorf("held/uncached path fired OnPreanimDone %d times, want 0", preanims)
	}

	// Zero performance degradation: actively holding (miss → store.Get → blit) allocates nothing.
	allocs := testing.AllocsPerRun(200, func() {
		vp.Render(ren, scene, sdl.Rect{X: 0, Y: 0, W: 512, H: 384})
	})
	if allocs != 0 {
		t.Errorf("hold-previous render allocates %.1f/op, want 0", allocs)
	}
}

// TestSpeakerSwapCrossfade pins the crossfade knob: mid-fade, BOTH the old
// (red) and new (green) sprites contribute to the frame; after the fade only
// the new one does; off (the default) the swap is a hard cut. 0-alloc during
// an active fade.
func TestSpeakerSwapCrossfade(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	if err := store.Upload("red", decodedFixture()); err != nil { // red fixture
		t.Fatal(err)
	}
	green := &assets.Decoded{Width: 64, Height: 64}
	gimg := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for p := 0; p < len(gimg.Pix); p += 4 {
		gimg.Pix[p+1], gimg.Pix[p+3] = 0xFF, 0xFF
	}
	green.Frames = append(green.Frames, gimg)
	green.Delays = append(green.Delays, 50*time.Millisecond)
	if err := store.Upload("green", green); err != nil {
		t.Fatal(err)
	}
	ct, err := NewCaptureTarget(ren, 512, 384)
	if err != nil {
		t.Skipf("capture target unavailable headlessly: %v", err)
	}
	defer ct.Close()

	vp := NewViewport(store)
	vp.SetCrossfade(400 * time.Millisecond)
	scene := &courtroom.Scene{}
	scene.Speaker.Visible = true
	scene.Speaker.Active = "red"
	centre := func() (r, g uint8) {
		img, err := ct.Capture(ren, func(dst sdl.Rect) { vp.Render(ren, scene, dst) })
		if err != nil {
			t.Skipf("ReadPixels unavailable headlessly: %v", err)
		}
		px := img.RGBAAt(256, 192)
		return px.R, px.G
	}
	vp.Update(scene, 16*time.Millisecond) // draw + remember red
	if r, _ := centre(); r == 0 {
		t.Fatal("setup: red sprite did not draw")
	}

	scene.Speaker.Active = "green"         // swap: both resident → the fade arms
	vp.Update(scene, 200*time.Millisecond) // halfway through the 400ms fade
	if r, g := centre(); r == 0 || g == 0 {
		t.Fatalf("mid-fade both sprites must contribute, got r=%d g=%d", r, g)
	}
	allocs := testing.AllocsPerRun(100, func() {
		vp.Render(ren, scene, sdl.Rect{X: 0, Y: 0, W: 512, H: 384})
	})
	if allocs != 0 {
		t.Errorf("crossfade render allocates %.1f/op, want 0", allocs)
	}
	vp.Update(scene, 400*time.Millisecond) // fade done
	if r, g := centre(); r != 0 || g == 0 {
		t.Errorf("after the fade only the new sprite draws, got r=%d g=%d", r, g)
	}

	// Off = the default hard cut: swap back with the knob off.
	vp.SetCrossfade(0)
	scene.Speaker.Active = "red"
	vp.Update(scene, 16*time.Millisecond)
	if r, g := centre(); r == 0 || g != 0 {
		t.Errorf("crossfade off must hard-swap, got r=%d g=%d", r, g)
	}
}

// TestThumbStandIn pins the opt-in thumbnail stand-in: with SetThumbSprites on,
// a COLD sprite whose thumb:// page is resident draws that low-q stand-in — and
// it WINS over hold-previous (the right character at low quality beats the
// previous character at full quality). Off (the default), the miss path is
// exactly the mode's own behaviour.
func TestThumbStandIn(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	if err := store.Upload("spk", decodedFixture()); err != nil { // red: the "previous" sprite
		t.Fatal(err)
	}
	// The ghost's thumbnail: a GREEN page under its thumb:// key, so the readback
	// tells the three cases (blank / red held / green thumb) apart.
	green := &assets.Decoded{Width: 64, Height: 64}
	gimg := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for p := 0; p < len(gimg.Pix); p += 4 {
		gimg.Pix[p+1], gimg.Pix[p+3] = 0xFF, 0xFF
	}
	green.Frames = append(green.Frames, gimg)
	green.Delays = append(green.Delays, 50*time.Millisecond)
	if err := store.Upload(ThumbKeyPrefix+"ghost", green); err != nil {
		t.Fatal(err)
	}
	ct, err := NewCaptureTarget(ren, 512, 384)
	if err != nil {
		t.Skipf("capture target unavailable headlessly: %v", err)
	}
	defer ct.Close()

	vp := NewViewport(store)
	vp.SetSpriteLoadMode(SpriteLoadHoldPrev)
	scene := &courtroom.Scene{}
	scene.Speaker.Visible = true
	scene.Speaker.Active = "spk"
	centre := func() (r, g uint8) {
		img, err := ct.Capture(ren, func(dst sdl.Rect) { vp.Render(ren, scene, dst) })
		if err != nil {
			t.Skipf("ReadPixels unavailable headlessly: %v", err)
		}
		px := img.RGBAAt(256, 192)
		return px.R, px.G
	}
	vp.Update(scene, 16*time.Millisecond) // remember the red sprite
	if r, _ := centre(); r == 0 {
		t.Fatal("setup: cached sprite did not draw")
	}
	scene.Speaker.Active = "ghost" // cold swap
	vp.Update(scene, 16*time.Millisecond)

	// Thumbs OFF (default): hold-previous bridges with the RED previous sprite.
	if r, g := centre(); r == 0 || g != 0 {
		t.Fatalf("thumbs off: expected the red held sprite, got r=%d g=%d", r, g)
	}
	// Thumbs ON: the GREEN thumbnail wins over the held sprite.
	vp.SetThumbSprites(true)
	if r, g := centre(); g == 0 || r != 0 {
		t.Errorf("thumbs on: expected the green thumb stand-in, got r=%d g=%d", r, g)
	}
	// 0-alloc while a thumb stand-in is actively drawing.
	allocs := testing.AllocsPerRun(200, func() {
		vp.Render(ren, scene, sdl.Rect{X: 0, Y: 0, W: 512, H: 384})
	})
	if allocs != 0 {
		t.Errorf("thumb stand-in render allocates %.1f/op, want 0", allocs)
	}
}

// TestHoldMaxAgeAndTint pins the two hold-previous power knobs: the max-age cap
// (a stand-in past its age gives up to blank; 0 = forever) and the diagnostic
// amber tint's restore discipline (the wash may never bleed onto the shared T1
// page — the next plain draw of the same texture must be untouched).
func TestHoldMaxAgeAndTint(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	if err := store.Upload("spk", decodedFixture()); err != nil {
		t.Fatal(err)
	}
	ct, err := NewCaptureTarget(ren, 512, 384)
	if err != nil {
		t.Skipf("capture target unavailable headlessly: %v", err)
	}
	defer ct.Close()

	// A WHITE fixture: the amber tint cuts G (255→190) and B, so both the tint
	// itself and a failed restore are measurable (the red-only shared fixture
	// would mask them — the tint keeps R at 255).
	white := &assets.Decoded{Width: 64, Height: 64}
	img0 := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for p := 0; p < len(img0.Pix); p++ {
		img0.Pix[p] = 0xFF
	}
	white.Frames = append(white.Frames, img0)
	white.Delays = append(white.Delays, 50*time.Millisecond)
	if err := store.Upload("white", white); err != nil {
		t.Fatal(err)
	}

	vp := NewViewport(store)
	vp.SetSpriteLoadMode(SpriteLoadHoldPrev)
	scene := &courtroom.Scene{}
	scene.Speaker.Visible = true
	scene.Speaker.Active = "white"

	centreG := func() uint8 {
		img, err := ct.Capture(ren, func(dst sdl.Rect) { vp.Render(ren, scene, dst) })
		if err != nil {
			t.Skipf("ReadPixels unavailable headlessly: %v", err)
		}
		return img.RGBAAt(256, 192).G
	}

	vp.Update(scene, 16*time.Millisecond) // resolve + remember the cached sprite
	plainG := centreG()
	if plainG != 0xFF {
		t.Fatalf("setup: the white sprite should draw G=255, got %d", plainG)
	}

	scene.Speaker.Active = "ghost" // swap to an uncached base → the hold engages
	vp.Update(scene, 16*time.Millisecond)
	if centreG() == 0 {
		t.Fatal("setup: hold-previous should be bridging")
	}

	// Max age: once the layer has been cold past the cap, the stand-in gives up.
	vp.SetHoldMaxAge(50 * time.Millisecond)
	vp.Update(scene, 100*time.Millisecond) // cold time accrues past the cap
	if centreG() != 0 {
		t.Error("a stand-in past its max age must give up to blank")
	}
	vp.SetHoldMaxAge(0) // 0 = forever → bridging resumes
	if centreG() == 0 {
		t.Error("max age 0 must bridge forever")
	}

	// Debug tint: the amber wash must show while holding (G cut to heldTintG),
	// and must RESTORE — the next plain (resolved) draw keeps its exact colour.
	vp.SetHoldDebugTint(true)
	if got := centreG(); got != heldTintG {
		t.Errorf("tinted stand-in G = %d, want the amber wash %d", got, heldTintG)
	}
	scene.Speaker.Active = "white" // back to the resolved sprite: a normal draw
	vp.Update(scene, 16*time.Millisecond)
	if got := centreG(); got != plainG {
		t.Errorf("tint bled onto the shared page: plain draw G=%d, want %d", got, plainG)
	}
}

// TestSpriteMaskClipsToStage pins the viewport sprite mask (default ON): a sprite
// that extends past the stage (a big offset, or here just a sprite wider than the
// stage) is clipped to the stage rect when the mask is on, and spills past it when
// off. Also pins 0-alloc with the mask engaged.
func TestSpriteMaskClipsToStage(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	if err := store.Upload("spk", decodedFixture()); err != nil {
		t.Fatal(err)
	}
	ct, err := NewCaptureTarget(ren, 512, 384)
	if err != nil {
		t.Skipf("capture target unavailable headlessly: %v", err)
	}
	defer ct.Close()

	vp := NewViewport(store)
	scene := &courtroom.Scene{}
	scene.Speaker.Visible = true
	scene.Speaker.Active = "spk"

	// A 256-wide stage centred in the 512 target. The 64×64 sprite scales to the
	// stage HEIGHT (384) → 384 wide, so it naturally overflows the 256-wide stage on
	// both sides — no offset needed to exercise the mask.
	stage := sdl.Rect{X: 128, Y: 0, W: 256, H: 384}
	const outsideX, insideX, y = 90, 256, 192 // 90 is inside the sprite but LEFT of the stage (X=128)

	spillsPastStage := func() bool {
		img, err := ct.Capture(ren, func(sdl.Rect) {
			vp.Update(scene, 16*time.Millisecond)
			vp.Render(ren, scene, stage)
		})
		if err != nil {
			t.Skipf("ReadPixels unavailable headlessly: %v", err)
		}
		if img.RGBAAt(insideX, y).R == 0 {
			t.Fatal("the on-stage sprite did not draw (test setup wrong)")
		}
		return img.RGBAAt(outsideX, y).R > 0 // sprite pixels to the LEFT of the stage?
	}

	vp.SetClipSprites(false)
	if !spillsPastStage() {
		t.Fatal("mask OFF: the wide sprite should spill past the stage (setup check)")
	}
	vp.SetClipSprites(true)
	if spillsPastStage() {
		t.Error("the sprite mask did not clip an overflowing sprite to the stage")
	}

	allocs := testing.AllocsPerRun(200, func() { vp.Render(ren, scene, stage) })
	if allocs != 0 {
		t.Errorf("sprite-mask render allocates %.1f/op, want 0", allocs)
	}
}
