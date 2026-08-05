package render

import (
	"image"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// overlayPage builds a bare multi-frame page. Frame textures are never
// dereferenced by anything under test here, so no SDL init is needed.
func overlayPage(frames int, delay time.Duration, w, h int32) *TexturePage {
	p := &TexturePage{Frames: make([]*sdl.Texture, frames), W: w, H: h}
	for i := 0; i < frames; i++ {
		p.Delays = append(p.Delays, delay)
	}
	return p
}

// TestOverlayStretchTrueFillsViewport / …FalseFitsHeightAndCentres
// (animationlayer.cpp:16, 246-256).
func TestOverlayStretchGeometry(t *testing.T) {
	vp := sdl.Rect{X: 10, Y: 20, W: 800, H: 600}

	fill := overlayDstRect(&courtroom.SceneOverlay{Stretch: true}, vp, 100, 100)
	if fill != vp {
		t.Errorf("stretch=true dst = %+v, want the whole viewport %+v", fill, vp)
	}

	// 2:1 art in a 800x600 box: height wins, width follows the aspect, centred.
	fit := overlayDstRect(&courtroom.SceneOverlay{}, vp, 200, 100)
	if fit.H != vp.H {
		t.Errorf("dst.H = %d, want the viewport height %d", fit.H, vp.H)
	}
	if fit.W != 1200 {
		t.Errorf("dst.W = %d, want 1200 (aspect preserved)", fit.W)
	}
	if want := vp.X + (vp.W-fit.W)/2; fit.X != want {
		t.Errorf("dst.X = %d, want %d (centred horizontally)", fit.X, want)
	}
	if fit.Y != vp.Y {
		t.Errorf("dst.Y = %d, want the viewport top %d", fit.Y, vp.Y)
	}

	// A page with no dimensions cannot be fitted: fall back to the viewport rect
	// rather than dividing by zero.
	if got := overlayDstRect(&courtroom.SceneOverlay{}, vp, 0, 0); got != vp {
		t.Errorf("dimensionless page dst = %+v, want %+v", got, vp)
	}
}

// TestOverlayRespectOffsetIsPercentOfViewport (courtroom.cpp:3264-3266).
func TestOverlayRespectOffsetIsPercentOfViewport(t *testing.T) {
	vp := sdl.Rect{X: 0, Y: 0, W: 800, H: 600}
	got := overlayDstRect(&courtroom.SceneOverlay{Stretch: true, OffsetX: 25, OffsetY: -10}, vp, 100, 100)
	if got.X != 200 {
		t.Errorf("dst.X = %d, want 200 (25%% of 800)", got.X)
	}
	if got.Y != -60 {
		t.Errorf("dst.Y = %d, want -60 (-10%% of 600)", got.Y)
	}
	if got.W != vp.W || got.H != vp.H {
		t.Error("the layer is NEVER resized by the offset — only moved")
	}
}

// TestMaxDurationIsPerFrameNotTotal — the render half of the parser's contract:
// advanceMax clamps EACH FRAME's delay (animationlayer.cpp:281-286), so a
// 3-frame clip authored at 200 ms with max_duration = 60 takes 3x60 ms to reach
// its end, not 60 ms total.
func TestOverlayMaxFrameDelayClampsPerFrame(t *testing.T) {
	page := overlayPage(3, 200*time.Millisecond, 64, 64)
	const clamp = 60 * time.Millisecond

	var a animState
	a.reset("fx://x")
	// One clamped frame's worth of time must advance exactly one frame.
	a.advanceMax(page, clamp, true, clamp)
	if a.frame != 1 {
		t.Fatalf("frame = %d after one clamped delay, want 1", a.frame)
	}
	a.advanceMax(page, clamp, true, clamp)
	a.advanceMax(page, clamp, true, clamp)
	if !a.finished {
		t.Error("3 clamped frames must finish the clip")
	}

	// Without the clamp the SAME elapsed time is not even one frame in.
	var b animState
	b.reset("fx://x")
	b.advanceMax(page, 3*clamp, true, 0)
	if b.frame != 0 || b.finished {
		t.Errorf("unclamped: frame=%d finished=%v — 180ms is under one authored 200ms frame", b.frame, b.finished)
	}

	// A clamp LONGER than the authored delay changes nothing (min(), not set()).
	var c animState
	c.reset("fx://x")
	c.advanceMax(page, 200*time.Millisecond, false, time.Second)
	if c.frame != 1 {
		t.Errorf("frame = %d, want 1 — a clamp above the authored delay is inert", c.frame)
	}
}

// TestOverlayAdvanceIsUnchangedWithoutAClamp — advance() delegates to advanceMax
// with maxDelay = 0, so every existing caller is byte-identical.
func TestOverlayAdvanceIsUnchangedWithoutAClamp(t *testing.T) {
	page := overlayPage(2, 50*time.Millisecond, 8, 8)
	var a, b animState
	a.reset("x")
	b.reset("x")
	a.advance(page, 120*time.Millisecond, false)
	b.advanceMax(page, 120*time.Millisecond, false, 0)
	if a.frame != b.frame || a.elapsed != b.elapsed || a.finished != b.finished {
		t.Errorf("advance and advanceMax(…,0) diverged: %+v vs %+v", a, b)
	}
}

// TestSameEffectTwiceRestartsViaGen — syncOverlay watches the generation as well
// as the base, because animState.reset only fires on a base change.
func TestOverlaySyncRestartsOnGenBump(t *testing.T) {
	v := NewViewport(nil)
	o := courtroom.SceneOverlay{Base: "fx://wash", Gen: 1}
	v.syncOverlay(&o)
	if v.overlayAnim.base != "fx://wash" {
		t.Fatalf("base = %q, want fx://wash", v.overlayAnim.base)
	}
	v.overlayAnim.frame = 4
	v.overlayAnim.finished = true

	// Same base, same gen: no restart (a redraw must not rewind the clip).
	v.syncOverlay(&o)
	if v.overlayAnim.frame != 4 {
		t.Error("an unchanged overlay must not restart every frame")
	}
	// Same base, NEW gen: restart.
	o.Gen = 2
	v.syncOverlay(&o)
	if v.overlayAnim.frame != 0 || v.overlayAnim.finished {
		t.Error("a Gen bump must restart the clip even when the base is identical")
	}
	// Cleared.
	o = courtroom.SceneOverlay{Gen: 3}
	v.syncOverlay(&o)
	if v.overlayAnim.base != "" {
		t.Errorf("a cleared overlay must unbind the layer, base = %q", v.overlayAnim.base)
	}
}

// TestAmbientAnimatingReportsALiveOverlay + the two states that must NOT hold the
// pacer up: a finished non-looping clip, and a resolved SINGLE-frame overlay
// (a still picture with no next deadline).
func TestAmbientAnimatingReportsALiveOverlay(t *testing.T) {
	v := NewViewport(nil)
	scene := &courtroom.Scene{}
	if v.AmbientAnimating(scene) {
		t.Fatal("an empty stage must not report animating")
	}
	scene.Overlay = courtroom.SceneOverlay{Base: "fx://wash", Loop: true, Gen: 1}
	if v.AmbientAnimating(scene) {
		t.Fatal("a COLD overlay (no page yet) has nothing to animate")
	}
	v.overlayAnim.base = "fx://wash"
	v.overlayAnim.page = overlayPage(1, 100*time.Millisecond, 8, 8)
	if v.AmbientAnimating(scene) {
		t.Fatal("a single-frame overlay is a still picture — it must not hold the frame pacer")
	}
	v.overlayAnim.page = overlayPage(3, 100*time.Millisecond, 8, 8)
	if !v.AmbientAnimating(scene) {
		t.Fatal("a live looping overlay must report animating")
	}
	// A finished one-shot stops reporting; a looping one never finishes.
	scene.Overlay.Loop = false
	v.overlayAnim.finished = true
	if v.AmbientAnimating(scene) {
		t.Fatal("a finished one-shot overlay must stop holding the pacer")
	}
	scene.Overlay.Loop = true
	if !v.AmbientAnimating(scene) {
		t.Fatal("cull/finished are inert while looping")
	}
}

// TestNextAnimDueIncludesTheOverlayDeadline — the ROADMAP's named pacing trap:
// an idle-capped client must wake exactly when the next overlay frame is due.
func TestNextAnimDueIncludesTheOverlayDeadline(t *testing.T) {
	v := NewViewport(nil)
	scene := &courtroom.Scene{}
	if _, ok := v.NextAnimDue(scene); ok {
		t.Fatal("an empty stage schedules nothing")
	}
	scene.Overlay = courtroom.SceneOverlay{Base: "fx://wash", Loop: true, Gen: 1}
	v.overlayAnim.base = "fx://wash"
	v.overlayAnim.page = overlayPage(3, 100*time.Millisecond, 8, 8)
	v.overlayAnim.elapsed = 40 * time.Millisecond
	due, ok := v.NextAnimDue(scene)
	if !ok || due != 60*time.Millisecond {
		t.Fatalf("overlay deadline: due=%v ok=%v, want 60ms true", due, ok)
	}
	// The per-frame clamp shortens the DEADLINE too, or the pacer would sleep
	// through frames advanceMax is about to spend.
	scene.Overlay.MaxFrameDelay = 50 * time.Millisecond
	if due, ok = v.NextAnimDue(scene); !ok || due != minAnimFrameDelay {
		t.Fatalf("clamped deadline: due=%v ok=%v, want the schedule floor %v", due, ok, minAnimFrameDelay)
	}
	// A cleared overlay drops off the schedule.
	scene.Overlay = courtroom.SceneOverlay{}
	if _, ok = v.NextAnimDue(scene); ok {
		t.Fatal("a cleared overlay must not schedule redraws")
	}
}

// TestFinishedCulledOverlayStopsHoldingFrames — cull hides a finished PLAY-ONCE
// clip; without cull the last frame is HELD (animationlayer.cpp:660-666).
func TestFinishedCulledOverlayStopsHoldingFrames(t *testing.T) {
	v := NewViewport(nil)
	culled := &courtroom.SceneOverlay{Base: "fx://x", Cull: true}
	plain := &courtroom.SceneOverlay{Base: "fx://x"}
	v.overlayAnim.finished = false
	if v.overlayHidden(culled) || v.overlayHidden(plain) {
		t.Fatal("a running clip is never hidden")
	}
	v.overlayAnim.finished = true
	if !v.overlayHidden(culled) {
		t.Error("cull + finished must hide the layer")
	}
	if v.overlayHidden(plain) {
		t.Error("without cull a finished one-shot HOLDS its last frame")
	}
	// A looping clip never latches finished, so cull is inert by construction —
	// pinned here so a future refactor cannot make cull kill a loop.
	var a animState
	a.reset("fx://x")
	page := overlayPage(2, 10*time.Millisecond, 8, 8)
	for i := 0; i < 20; i++ {
		a.advanceMax(page, 10*time.Millisecond, false, 0)
	}
	if a.finished {
		t.Error("a looping clip must never latch finished")
	}
}

// TestOverlayScaleModePrecedence — the user's explicit mode beats the effect's
// own request, which beats the geometry rule (internal/courtroom/scaling.go).
func TestOverlayScaleModePrecedence(t *testing.T) {
	v := NewViewport(nil)
	o := &courtroom.SceneOverlay{Scaling: courtroom.ScalingSmooth}
	if got := v.overlayScaleMode(o, 64, 600); got != sdl.ScaleModeLinear {
		t.Errorf("effect asked smooth, got %v", got)
	}
	o.Scaling = courtroom.ScalingPixel
	if got := v.overlayScaleMode(o, 64, 600); got != sdl.ScaleModeNearest {
		t.Errorf("effect asked pixel, got %v", got)
	}
	v.SetSpriteFX(SpriteFX{UserScaling: courtroom.ScalingSmooth})
	if got := v.overlayScaleMode(o, 64, 600); got != sdl.ScaleModeLinear {
		t.Error("an explicit USER mode is not overridable by an asset")
	}
	v.SetSpriteFX(SpriteFX{})
	o.Scaling = courtroom.ScalingAuto
	if got := v.overlayScaleMode(o, 64, 600); got != sdl.ScaleModeNearest {
		t.Error("auto + art being ENLARGED must point-sample (the geometry rule)")
	}
	if got := v.overlayScaleMode(o, 1200, 600); got != sdl.ScaleModeLinear {
		t.Error("auto + art being shrunk must filter")
	}
}

// TestStretchedOverlayKeepsSmoothScaling — canon scopes the enlarge→nearest rule
// to the NON-stretch branch: AnimationLayer::calculateFrameGeometry sets
// FastTransformation on `m_frame_size.height() < widget_size.height()` INSIDE the
// else of `if (m_stretch_to_fit)` (animationlayer.cpp:236-262). A `stretch=true`
// wash — fire, filmgrain, a small gradient blown up to the whole viewport — is
// authored to be smooth, and point-sampling it is a visible defect. An explicit
// `scaling` key still wins in both branches, as it does in canon (the
// m_resize_mode block runs after the geometry rule).
func TestStretchedOverlayKeepsSmoothScaling(t *testing.T) {
	v := NewViewport(nil)
	stretched := &courtroom.SceneOverlay{Stretch: true} // Scaling auto
	if got := v.overlayScaleMode(stretched, 64, 600); got != sdl.ScaleModeLinear {
		t.Errorf("a STRETCHED overlay scaled %v — canon's enlarge→nearest rule lives inside the "+
			"non-stretch branch (animationlayer.cpp:246-260)", got)
	}
	// The same art, not stretched, still point-samples: the rule did not vanish.
	if got := v.overlayScaleMode(&courtroom.SceneOverlay{}, 64, 600); got != sdl.ScaleModeNearest {
		t.Errorf("a fit-to-height overlay being ENLARGED scaled %v, want nearest", got)
	}
	// And an explicit request still overrides, stretched or not.
	stretched.Scaling = courtroom.ScalingPixel
	if got := v.overlayScaleMode(stretched, 64, 600); got != sdl.ScaleModeNearest {
		t.Errorf("a stretched overlay asking for pixel scaled %v", got)
	}
}

// TestOverlayLayerDrawOrder — spec §4.4. The four rungs of do_effect
// (courtroom.cpp:3216-3247) are positions in a stack, and the ONLY thing that
// puts an AsyncAO overlay at the right depth is where its drawOverlay call sits
// inside Viewport.Render. A recording renderer would prove the same thing one
// blit at a time; the call ORDER in the source is that proof without a GPU, and
// it fails loudly if someone hoists a call while refactoring the pass.
//
// DEPTH ONLY — where a rung lands is TestOverlayIsClippedToTheStage's job, and it
// reads real pixels back. This census being the only §4.4 render test is what let
// an unclipped, off-stage destination rect ship unnoticed.
//
//	behind    → stackUnder(ui_vp_player_char): after the background, BEFORE any sprite
//	character → stackUnder(ui_vp_desk):        after the sprites, BEFORE the desk
//	over      → raise() in the viewport:       after the desk, BEFORE the shout splash
//	chat      → reparented OUT of the viewport entirely (DrawOverlayChat, UI-side)
func TestOverlayLayerDrawOrder(t *testing.T) {
	src, err := os.ReadFile("viewport.go")
	if err != nil {
		t.Fatalf("read viewport.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func (v *Viewport) Render(")
	end := strings.Index(body, "func effectiveShoutBase(")
	if start < 0 || end <= start {
		t.Fatal("could not isolate Viewport.Render — this census needs updating with the refactor")
	}
	body = body[start:end]

	at := func(marker string) int {
		i := strings.Index(body, marker)
		if i < 0 {
			t.Fatalf("Viewport.Render no longer contains %q — the overlay rung it anchors is gone", marker)
		}
		return i
	}
	behind := at("courtroom.OverlayLayerBehind")
	firstSprite := at("v.drawSprite(")
	lastSprite := strings.LastIndex(body, "v.drawSprite(")
	character := at("courtroom.OverlayLayerCharacter")
	desk := at("scene.DeskBase")
	over := at("courtroom.OverlayLayerOver")
	shout := at("effectiveShoutBase(scene, v.store)")

	if behind >= firstSprite {
		t.Error("the `behind` rung must blit BEFORE every character sprite (courtroom.cpp:3218-3222)")
	}
	if character <= lastSprite {
		t.Error("the `character` rung must blit AFTER the character sprites (courtroom.cpp:3223-3227)")
	}
	if character >= desk {
		t.Error("the `character` rung must blit UNDER the desk overlay (stackUnder(ui_vp_desk))")
	}
	if over <= desk {
		t.Error("the `over` rung must blit ABOVE the desk (courtroom.cpp:3228-3232)")
	}
	if over >= shout {
		t.Error("the `over` rung must blit BELOW the shout splash")
	}
	if strings.Contains(body, "courtroom.OverlayLayerChat") {
		t.Error("the `chat` rung is reparented OUT of the viewport (courtroom.cpp:3234-3237) — it belongs " +
			"to DrawOverlayChat, called after the chatbox from internal/ui, never inside Render")
	}
	// Depth is only half the stack contract: the three in-viewport rungs are
	// CHILDREN of ui_viewport (setParent, courtroom.cpp:3218-3232), so each must
	// go through the containment wrapper. A refactor that reaches for the raw
	// blit inside Render silently restores the off-stage spill —
	// TestOverlayIsClippedToTheStage proves the wrapper works, this proves the
	// pass still uses it.
	if n := strings.Count(body, "v.drawOverlayInStage("); n != 3 {
		t.Errorf("Render makes %d contained overlay calls, want 3 (behind/character/over)", n)
	}
	if strings.Contains(body, "v.drawOverlay(") {
		t.Error("Render blits an overlay UNCONTAINED — every in-viewport rung is a child of " +
			"ui_viewport and must clip to the stage (drawOverlayInStage)")
	}
}

// TestNoOverlayFrameIsByteIdentical — spec §3/§4.4. Every frame of normal play
// carries no screen effect, and Viewport.Render now calls drawOverlay three
// times per frame regardless. A function that issues NO SDL call and touches NO
// state cannot change a pixel, so the byte-identity claim is proved by running
// all four rungs against a NIL renderer and a NIL store: anything that reached
// either would panic here, and anything that scheduled a blit could not.
func TestNoOverlayFrameIsByteIdentical(t *testing.T) {
	v := NewViewport(nil) // nil store: resolve() would deref it
	scene := &courtroom.Scene{}
	vp := sdl.Rect{X: 0, Y: 0, W: 800, H: 600}
	layers := []uint8{
		courtroom.OverlayLayerChat, courtroom.OverlayLayerBehind,
		courtroom.OverlayLayerCharacter, courtroom.OverlayLayerOver,
	}
	draw := func() {
		for _, l := range layers {
			v.drawOverlay(nil, scene, vp, l) // nil renderer: any draw call panics
			// The contained rungs must be just as free: their clip bracket has to
			// sit BEHIND the "is this my rung" gate, or an idle frame would pay two
			// cgo calls per rung (and deref this nil renderer).
			v.drawOverlayInStage(nil, scene, vp, vp, l)
		}
	}
	draw()
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a no-overlay frame allocates %.1f/op in drawOverlay, want 0", n)
	}
	// The census hook must agree: nothing on stage is nothing to animate, so the
	// idle pacer is not woken by an overlay that does not exist.
	if v.AmbientAnimating(scene) {
		t.Error("an empty scene reported a live overlay — the frame pacer would never idle")
	}
	// And a CLEARED overlay (an effect that played and was taken down) is the same
	// no-op: the residue in overlayAnim must not resurrect a blit.
	v.syncOverlay(&courtroom.SceneOverlay{Base: "fx://played", Gen: 1})
	scene.Overlay = courtroom.SceneOverlay{Gen: 1} // clearOverlay's shape: Base "", Gen kept
	draw()
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a frame after a cleared overlay allocates %.1f/op, want 0", n)
	}
}

// TestOverlayDrawZeroAlloc — spec §4.4's alloc gate with an overlay LIVE. The
// draw is pointer math over a Scene.Overlay the courtroom precomputed: no string
// building, no map lookup, and the destination rect goes through the persistent
// v.ovRect scratch, because taking &localRect heap-escapes on every cgo call
// (the Ctx.cgoRect lesson). A looping fire/filmgrain effect runs this every frame
// for the whole message.
func TestOverlayDrawZeroAlloc(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatalf("texture store: %v", err)
	}
	defer store.Purge()
	const base = "theme://fx/live"
	if err := store.Upload(base, decodedFixture()); err != nil {
		t.Fatalf("upload: %v", err)
	}
	v := NewViewport(store)
	scene := &courtroom.Scene{Overlay: courtroom.SceneOverlay{
		Base: base, Layer: courtroom.OverlayLayerOver, Loop: true, Gen: 1,
	}}
	v.syncOverlay(&scene.Overlay)
	vp := sdl.Rect{X: 0, Y: 0, W: 800, H: 600}
	draw := func() { v.drawOverlay(ren, scene, vp, courtroom.OverlayLayerOver) }
	draw() // warm the page pointer and latch the page's scale mode
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a live overlay allocates %.1f/frame, want 0", n)
	}
	// The flipped path is the other cgo call site (CopyEx) and must be just as free.
	scene.Overlay.Flip = true
	draw()
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a flipped live overlay allocates %.1f/frame, want 0", n)
	}
	// And so must the contained path the three in-viewport rungs actually use: the
	// clip rect rides the persistent v.ovClip scratch for the same reason ovRect
	// exists — &localRect would heap-escape into SetClipRect every frame.
	scene.Overlay.Flip = false
	stage := vp
	drawIn := func() { v.drawOverlayInStage(ren, scene, vp, stage, courtroom.OverlayLayerOver) }
	drawIn()
	if n := testing.AllocsPerRun(200, drawIn); n != 0 {
		t.Fatalf("a contained live overlay allocates %.1f/frame, want 0", n)
	}
}

// opaqueDecoded builds a single-frame w×h page of solid white — ink a pixel
// readback can find against a black clear (bigDecoded's zeroed RGBA is fully
// TRANSPARENT and would draw nothing).
func opaqueDecoded(w, h int) *assets.Decoded {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	return &assets.Decoded{
		Width:  w,
		Height: h,
		Frames: []*image.RGBA{img},
		Delays: []time.Duration{50 * time.Millisecond},
	}
}

// TestOverlayIsClippedToTheStage — the containment gate. do_effect parents the
// behind/character/over layers to ui_viewport (courtroom.cpp:3218-3232) and Qt
// clips a child to its parent's rect, so in canon those three CANNOT paint
// outside the stage. Our destination rect is free to leave it, and does so by
// default: the classic stage is 4:3 while effect packs ship ~16:9 art (fit-to-
// height is then ~32% too wide), and respect_offset moves the whole layer by the
// speaker's own offset. Unclipped, that painted over the IC log and the chatbox.
//
// This is the observation the spec's §4.4 "recording renderer" line was asking
// for, done against real pixels: nothing in the suite had ever looked at where an
// overlay actually landed, which is why the spill was invisible. Each case first
// asserts the geometry really does leave the stage — otherwise the readback would
// pass vacuously — then reads the framebuffer back and demands every inked pixel
// be inside it.
func TestOverlayIsClippedToTheStage(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatalf("texture store: %v", err)
	}
	defer store.Purge()

	// 2:1 art, i.e. wider than the 4:3 stage below — the shipping effect-pack shape.
	const base = "theme://fx/wide"
	if err := store.Upload(base, opaqueDecoded(400, 200)); err != nil {
		t.Fatalf("upload: %v", err)
	}
	page, ok := store.Get(base)
	if !ok {
		t.Fatal("the overlay page did not survive the upload")
	}
	// The window newHeadlessRenderer opens is 640×480; the stage is a 4:3 box well
	// inside it, so every direction an overlay could spill is READABLE.
	const winW, winH = 640, 480
	stage := sdl.Rect{X: 160, Y: 120, W: 320, H: 240}

	// The three shapes that leave the stage: art wider than a 4:3 box, a
	// respect_offset shift, and the flipped (CopyEx) blit — one per rung.
	fitWide := courtroom.SceneOverlay{Base: base, Layer: courtroom.OverlayLayerBehind, Gen: 1}
	offset := courtroom.SceneOverlay{Base: base, Layer: courtroom.OverlayLayerCharacter, Stretch: true, OffsetX: 25, OffsetY: -10, Gen: 1}
	flipped := courtroom.SceneOverlay{Base: base, Layer: courtroom.OverlayLayerOver, Flip: true, Gen: 1}

	for _, tc := range []struct {
		name string
		o    courtroom.SceneOverlay
	}{
		{"fit-to-height art wider than the stage", fitWide},
		{"respect_offset moves the layer off-stage", offset},
		{"flipped blit (CopyEx) is contained too", flipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scene := &courtroom.Scene{Overlay: tc.o}
			v := NewViewport(store)
			v.syncOverlay(&scene.Overlay)

			dst := overlayDstRect(&scene.Overlay, stage, page.W, page.H)
			if dst.X >= stage.X && dst.Y >= stage.Y &&
				dst.X+dst.W <= stage.X+stage.W && dst.Y+dst.H <= stage.Y+stage.H {
				t.Fatalf("dst %+v is already inside stage %+v — this case proves nothing", dst, stage)
			}

			_ = ren.SetClipRect(nil)
			_ = ren.SetDrawColor(0, 0, 0, 255)
			_ = ren.Clear()
			v.drawOverlayInStage(ren, scene, stage, stage, scene.Overlay.Layer)

			pix := make([]byte, winW*winH*4)
			if err := ren.ReadPixels(&sdl.Rect{X: 0, Y: 0, W: winW, H: winH},
				uint32(sdl.PIXELFORMAT_ARGB8888), unsafe.Pointer(&pix[0]), winW*4); err != nil {
				t.Skipf("ReadPixels unavailable on this backend: %v", err)
			}
			minX, minY, maxX, maxY := winW, winH, -1, -1
			for y := 0; y < winH; y++ {
				for x := 0; x < winW; x++ {
					p := (y*winW + x) * 4
					if pix[p] == 0 && pix[p+1] == 0 && pix[p+2] == 0 {
						continue
					}
					if x < minX {
						minX = x
					}
					if x > maxX {
						maxX = x
					}
					if y < minY {
						minY = y
					}
					if y > maxY {
						maxY = y
					}
				}
			}
			if maxX < 0 {
				t.Fatal("the overlay drew NOTHING — a clip that eats the whole layer is not containment")
			}
			if int32(minX) < stage.X || int32(minY) < stage.Y ||
				int32(maxX) >= stage.X+stage.W || int32(maxY) >= stage.Y+stage.H {
				t.Errorf("overlay ink spans (%d,%d)-(%d,%d), outside the stage %+v — an in-viewport rung "+
					"painted over the log/chatbox (dst was %+v)", minX, minY, maxX, maxY, stage, dst)
			}
		})
	}

	// The zoom path already owns a clip (internal/ui/vpzoom.go) and it IS the
	// stage: containment must defer to it rather than stomping it, exactly like
	// the sprite mask and the reflection.
	t.Run("an existing clip is neither stomped nor restored away", func(t *testing.T) {
		scene := &courtroom.Scene{Overlay: courtroom.SceneOverlay{
			Base: base, Layer: courtroom.OverlayLayerBehind, Loop: true, Gen: 4,
		}}
		v := NewViewport(store)
		v.syncOverlay(&scene.Overlay)
		outer := sdl.Rect{X: 8, Y: 9, W: 100, H: 101}
		_ = ren.SetClipRect(&outer)
		v.drawOverlayInStage(ren, scene, stage, stage, courtroom.OverlayLayerBehind)
		if got := ren.GetClipRect(); got != outer {
			t.Errorf("the caller's clip came back as %+v, want %+v", got, outer)
		}
		_ = ren.SetClipRect(nil)
	})
}
