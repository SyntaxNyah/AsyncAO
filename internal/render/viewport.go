package render

import (
	"math"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	offsetPercentDivisor = 100
	// shakeAmplitudeDivisor scales the screenshake wobble to the viewport width.
	shakeAmplitudeDivisor = 48
	// shakePeriod is fast enough to read as a shake, slow enough that 60 Hz sampling doesn't alias it away.
	shakePeriod = 90 * time.Millisecond

	// minRainbowCycle must stay above 0: phase%cycle and rainbowMod divide by the period, so 0 panics the render loop.
	minRainbowCycle = 300 * time.Millisecond  // fastest (Speed=100)
	maxRainbowCycle = 8000 * time.Millisecond // slowest (Speed=1)

	// spriteFloorMax is the floor at Vividness=0; it stops a saturated hue crushing channels to a silhouette.
	spriteFloorMax = 200
)

// animState tracks a layer's playback and caches its page by store generation, avoiding LRU lookups.
type animState struct {
	base     string
	frame    int
	elapsed  time.Duration
	finished bool
	// startReported guards the one-shot OnPreanimStart report; reset clears it on a base change.
	startReported bool
	// loopReported makes OnPreanimDone fire once when the loop-preanim pref keeps the clip wrapping; reset clears it.
	loopReported bool
	// shownSrc is the last source frame reported through OnFrameShown; -1 is none, because frame 0 is a valid trigger.
	shownSrc int

	page    *TexturePage
	pageGen uint64

	// coldFor is how long this layer's base has been unresolved; the hold-previous max age reads it.
	coldFor time.Duration
	// fadeLeft counts down a crossfade, ticking only while the new sprite is resident.
	fadeLeft time.Duration
	// thumbKey is the precomputed thumb:// key ("" when no base), so the cold-load miss path doesn't allocate.
	thumbKey string
	// heldKey is the precomputed held:// key the store parks a scenery frame under when the LRU evicts it on screen.
	heldKey string

	// lastGood is the base last drawn; reset keeps it, and a string because a page pointer could be evicted.
	lastGood string

	// scaleName/scaleIdleH hold the idle page height; reset keeps them so a character shares one filter height.
	scaleName  string
	scaleIdleH int32
}

// reset rebinds the state to a new asset base, keeping lastGood for the cold-load hold.
func (a *animState) reset(base string) {
	a.base = base
	a.frame = 0
	a.elapsed = 0
	a.finished = false
	a.startReported = false
	a.loopReported = false
	a.shownSrc = -1 // nothing reported for the new base yet (frame 0 is a valid trigger)
	a.page = nil
	a.pageGen = 0
	a.thumbKey = ""
	a.heldKey = ""
	if base != "" {
		a.thumbKey = ThumbKeyPrefix + base
		a.heldKey = HeldKeyPrefix + base
	}
}

// restart resets playback for a replaced page, as the frame cursor must not outlive the page it indexes.
func (a *animState) restart() {
	a.frame = 0
	a.elapsed = 0
	a.finished = false
	a.startReported = false
	a.loopReported = false
	a.shownSrc = -1
}

// spriteNativeH is the idle page height, which AO2's per-sprite filter rule uses (m_frame_size.height()).
func (a *animState) spriteNativeH(layer *courtroom.SpriteLayer, page *TexturePage) int32 {
	if a.scaleName != layer.Name {
		a.scaleName, a.scaleIdleH = layer.Name, 0
	}
	if layer.Active == layer.IdleBase && page.H > 0 {
		a.scaleIdleH = page.H
	}
	if a.scaleIdleH > 0 {
		return a.scaleIdleH
	}
	return page.H
}

// resolve returns the cached page, querying the store only when its generation or the base changed.
func (a *animState) resolve(store *TextureStore) (*TexturePage, bool) {
	if a.base == "" {
		return nil, false
	}
	gen := store.Generation()
	if a.page != nil && a.pageGen == gen {
		return a.page, true
	}
	page, ok := store.Get(a.base)
	if !ok {
		a.page = nil
		a.pageGen = gen
		return nil, false
	}
	if a.page != page {
		// The page was replaced under the same base, so restart or a.frame can run past the new frame count.
		// This must fire even when a.page is nil: a miss can leave the cursor on a longer, evicted page.
		a.restart()
		a.page = page
	}
	a.pageGen = gen
	return page, true
}

// advance steps frame timing; a one-shot stops on its last frame and reports completion exactly once.
func (a *animState) advance(page *TexturePage, dt time.Duration, playOnce bool) (justFinished bool) {
	return a.advanceMax(page, dt, playOnce, 0)
}

// advanceMax caps each frame's delay, not the clip (setMaximumDurationPerFrame, animationlayer.cpp:281-286).
func (a *animState) advanceMax(page *TexturePage, dt time.Duration, playOnce bool, maxDelay time.Duration) (justFinished bool) {
	if page == nil || len(page.Frames) <= 1 {
		// A partial page is not a real single-frame animation: finishing here would fire OnPreanimDone early.
		if playOnce && !a.finished && (page == nil || !page.Partial) {
			// INVARIANT: finished is set only under playOnce; the speaker block's restart guard in Update relies on it.
			a.finished = true
			return true
		}
		return false
	}
	if a.finished {
		return false
	}
	if a.frame >= len(page.Frames) {
		// A re-decode can swap in a shorter page between resolves; never index a missing frame.
		a.frame = 0
		a.elapsed = 0
	}
	a.elapsed += dt
	for {
		delay := page.Delays[a.frame]
		if maxDelay > 0 && delay > maxDelay {
			delay = maxDelay
		}
		if delay <= 0 || a.elapsed < delay {
			return false
		}
		a.elapsed -= delay
		if a.frame == len(page.Frames)-1 {
			if page.Partial {
				// A still-growing page holds its last frame until the next chunk arrives; never finish or wrap it.
				return false
			}
			if playOnce {
				// INVARIANT: finished is set only under playOnce; Update's speaker restart guard relies on it.
				a.finished = true
				return true
			}
			a.frame = 0
		} else {
			a.frame++
		}
	}
}

// advanceSpeaker steps the speaker layer and reports whether OnPreanimDone should fire.
func (v *Viewport) advanceSpeaker(page *TexturePage, dt time.Duration, playOnce bool) (firePreanimDone bool) {
	a := &v.speakerAnim
	if !playOnce && a.finished {
		// A finished one-shot has become a loop, so restart it clean.
		a.finished = false
		a.frame = 0
		a.elapsed = 0
	}
	if playOnce && v.preanimLoop {
		if a.loopReported {
			// OnPreanimDone already fired; keep wrapping with finished clear so NextAnimDue keeps scheduling.
			a.advance(page, dt, false)
			return false
		}
		// Clear finished here: one frame set stops NextAnimDue scheduling and stalls the loop.
		if a.advance(page, dt, true) {
			a.loopReported = true
			a.finished = false
			a.frame = 0
			a.elapsed = 0
			return true
		}
		return false
	}
	return a.advance(page, dt, playOnce) && playOnce
}

// reportSpeakerFrame fires OnFrameShown with the source frame index when it changes.
func (v *Viewport) reportSpeakerFrame(page *TexturePage) {
	if v.OnFrameShown == nil || page == nil || len(page.Frames) == 0 {
		return
	}
	a := &v.speakerAnim
	src := a.frame
	if !page.Partial {
		src = assets.FrameKeepIndex(a.frame, page.SourceFrames, len(page.Frames))
	}
	if src == a.shownSrc {
		return // same source frame still on screen — nothing new to fire
	}
	a.shownSrc = src
	v.OnFrameShown(src)
}

// pageDuration is the sum of a page's frame delays, zero for a single-frame page.
func pageDuration(page *TexturePage) time.Duration {
	var total time.Duration
	for _, d := range page.Delays {
		total += d
	}
	return total
}

// SpeakerAnimInfo is the speaker sprite's frame counts and delay statistics, for the debug panel.
type SpeakerAnimInfo struct {
	Base     string
	Kept     int
	Source   int
	MinDelay time.Duration
	MaxDelay time.Duration
	AvgDelay time.Duration
}

// SpeakerAnimInfo reports the speaker sprite's decoded animation stats; ok=false when no page is resident.
func (v *Viewport) SpeakerAnimInfo() (SpeakerAnimInfo, bool) {
	a := &v.speakerAnim
	if a.page == nil || len(a.page.Frames) == 0 || len(a.page.Delays) == 0 {
		return SpeakerAnimInfo{}, false
	}
	info := SpeakerAnimInfo{
		Base:   a.base,
		Kept:   len(a.page.Frames),
		Source: a.page.SourceFrames,
	}
	var sum time.Duration
	info.MinDelay = a.page.Delays[0]
	for _, d := range a.page.Delays {
		sum += d
		if d < info.MinDelay {
			info.MinDelay = d
		}
		if d > info.MaxDelay {
			info.MaxDelay = d
		}
	}
	info.AvgDelay = sum / time.Duration(len(a.page.Delays))
	return info, true
}

// SpriteFX is viewer-local wash and motion; it has no pointers, so setting it each frame doesn't allocate.
type SpriteFX struct {
	Rainbow    bool // cycling-hue wash
	Solid      bool // fixed-colour wash (used only when !Rainbow)
	Glow       bool // additive blend (neon) instead of multiplying tint
	PairDesync bool // offset the pair layer's hue half a period
	PerCharHue bool // rainbow: offset each character's hue by a hash of its name
	Wobble     bool // gentle continuous position sway
	Spin       bool // slow continuous rotation
	ShoutPunch bool // #12: quick scale-pop of the whole stage when a shout appears
	Entrance   bool // #9: slide the speaker in when a new character takes the stage
	DoF        bool // #11: soft-focus + dim the background behind the sharp speaker
	Spotlight  bool // #121: dim the non-speaker layers (pair partner + desk) so the talker pops
	// Local idle breathing: IdleBreath is the master switch; ReduceMotion suppresses it.
	IdleBreath  bool
	BreathBob   bool // the vertical-bob component
	BreathScale bool // the breathing scale-pulse component
	Reflection  bool // #123: a flipped, faded "glass floor" mirror of the sprites below the floor line

	Speed           int // rainbow hue speed slider [1,100] (higher = faster)
	Vividness       int // rainbow saturation slider [0,100] (higher = more neon)
	SpotlightLevel  int // spotlight dim intensity [0,100] (higher = darker non-speakers)
	BreathAmp       int // idle-breathing amplitude [1,100] (higher = more motion)
	BreathSpeed     int // idle-breathing speed [1,100] (higher = faster)
	ReflectStrength int // reflection opacity [0,100] (higher = more visible)

	SolidR, SolidG, SolidB uint8 // fixed tint colour (Solid)

	// UserScaling is the viewer's texture filter; it lives here because SpriteFX already reaches all seven draw paths.
	UserScaling courtroom.ScalingMode
}

// tinted reports whether any wash is active, so drawSprite can skip colour-mod work.
func (f SpriteFX) tinted() bool { return f.Rainbow || f.Solid }

// Sprite load modes pick what a character layer draws while its new sprite loads; values must match config.SpriteLoad*.
const (
	// SpriteLoadBlank draws nothing until the sprite lands.
	SpriteLoadBlank = 0
	// SpriteLoadHoldPrev keeps the layer's last drawn sprite on screen until the new one lands.
	SpriteLoadHoldPrev = 1
)

// Viewport renders a courtroom.Scene into a destination rect without heap allocations in steady state.
type Viewport struct {
	store *TextureStore

	speakerAnim   animState
	pairAnim      animState
	groupAnims    []animState
	shoutAnim     animState
	bgAnim        animState
	deskAnim      animState
	speedlineAnim animState
	// overlayGen mirrors Scene.Overlay.Gen so the same effect fired twice restarts; reset only fires on a base change.
	overlayAnim animState
	overlayGen  uint32

	// prevActive* are last frame's speaker bases, so ReduceAnimatedExcept runs only when the active set changes.
	prevActiveIdle, prevActiveTalk, prevActivePreanim string

	// OnPreanimDone forwards one-shot completion to the courtroom.
	OnPreanimDone func()

	// OnPreanimStart reports a multi-frame preanim's duration once, so the fallback timeout doesn't cut it short.
	OnPreanimStart func(time.Duration)

	// OnFrameShown reports the speaker's source frame index, for networked effects (AO2-Client FRAME_* fields).
	OnFrameShown func(src int)

	// curCycle is derived from fx.Speed each frame and kept above 0 so the rainbowPhase modulo can't panic.
	fx           SpriteFX
	rainbowPhase time.Duration
	curCycle     time.Duration
	// fxClock free-runs; each motion effect takes it modulo its own period, so there is no shared wrap glitch.
	fxClock time.Duration

	// spriteLoadMode chooses what a layer draws while its new sprite loads; drawSprite's miss path only.
	spriteLoadMode int

	// clipSprites masks character sprites to the stage so a big offset can't spill over the chatbox or log.
	clipSprites bool

	// holdMaxAge caps hold-previous (0 = forever), and tintHeld washes stand-in sprites amber for diagnostics.
	holdMaxAge time.Duration
	tintHeld   bool

	// thumbSprites shows the right character's thumbnail during a cold load, before spriteLoadMode.
	thumbSprites bool

	// missingnoEnabled draws AO2's placeholder for a base that 404'd; hold-previous would keep another character.
	missingnoEnabled bool

	// preanimLoop wraps an active preanim, deliberately unlike AO2 (animationlayer.cpp forces play-once).
	preanimLoop bool

	// crossfade is the speaker-swap blend duration (0 = hard swap).
	crossfade time.Duration

	// punchLeft counts down a shout's screen punch; prevShoutOn fires it once per shout.
	punchLeft   time.Duration
	prevShoutOn bool

	// entranceLeft counts down a speaker slide-in; prevSpeaker fires it once per new character.
	entranceLeft time.Duration
	prevSpeaker  string

	// Scratch rects live here because passing a stack value's address to cgo forces a heap escape per draw.
	dstRect   sdl.Rect
	fillRect  sdl.Rect
	fxRect    sdl.Rect // #8 outline/shadow offset-blit destination (kept off dstRect)
	srcRect   sdl.Rect // torn-glitch band source (texture coords; kept off the dst scratches)
	reflRect  sdl.Rect // #123 reflection blit destination
	reflClip  sdl.Rect // #123 reflection clip rect (confine to the stage when not already clipped)
	maskClip  sdl.Rect // viewport sprite mask: clip character sprites to the stage so an offset can't spill out
	stageClip sdl.Rect // #80: clip the full-viewport fills (bg / speedlines / desk / flash) to the UN-shaken stage so a shake/punch can't spill them over the UI
	ovRect    sdl.Rect // screen-effect overlay blit destination (kept off fillRect: both can draw in one frame)
	ovClip    sdl.Rect // screen-effect containment clip (the in-viewport rungs are children of the stage)
	// splashClip is separate from ovClip: both can run in a frame, and SDL keeps the pointer live during the call.
	splashClip sdl.Rect

	// Post-processing overlays: cached, size-stable textures blended over the stage.
	postFX               PostFX
	vignetteTex          *sdl.Texture
	scanlineTex          *sdl.Texture
	scanlineW, scanlineH int32
	grainTex             [grainFrames]*sdl.Texture
	grainIdx             int
	postRect             sdl.Rect // post-FX blit destination scratch
	// crtMaskTex is an RGB stripe mask blended MOD over the stage, rebuilt only on resize.
	crtMaskTex         *sdl.Texture
	crtMaskW, crtMaskH int32

	particles particleField

	// Style preview state: its own animState loops apart from the stage; a stack address passed to cgo escapes.
	prevAnim      animState
	prevLayer     courtroom.SpriteLayer
	prevClockMark time.Duration
	prevVPClip    sdl.Rect
	prevClipSave  sdl.Rect
}

// previewMaxStep caps the preview's step, so a long gap between draws doesn't spin the frame loop.
const previewMaxStep = 250 * time.Millisecond

// RenderStylePreview uses the live drawSprite path, so it can't drift from a real message; render thread only.
func (v *Viewport) RenderStylePreview(ren *sdl.Renderer, base string, st courtroom.SpriteStyle, vp sdl.Rect) {
	dt := v.fxClock - v.prevClockMark
	v.prevClockMark = v.fxClock
	if dt < 0 || dt > previewMaxStep {
		dt = 0
	}
	v.syncAnim(&v.prevAnim, base)
	if page, ok := v.prevAnim.resolve(v.store); ok {
		v.prevAnim.advance(page, dt, false)
	}
	v.prevClipSave = ren.GetClipRect()
	v.prevVPClip = vp
	_ = ren.SetClipRect(&v.prevVPClip)
	v.prevLayer = courtroom.SpriteLayer{Visible: true, Active: base, Style: st}
	v.drawSprite(ren, &v.prevLayer, &v.prevAnim, vp, 0, percentMax /* dimPct 100 = no spotlight dim */)
	if v.prevClipSave.W > 0 || v.prevClipSave.H > 0 {
		_ = ren.SetClipRect(&v.prevClipSave)
	} else {
		_ = ren.SetClipRect(nil)
	}
}

// Outline and shadow tuning: the outline is 8 offset silhouette blits, the shadow one offset down-right.
var (
	outlineDirs = [8][2]int32{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
	shadowDir   = [1][2]int32{{1, 1}} // down-right
)

const (
	outlineAlpha     = 235 // near-solid white outline
	shadowAlpha      = 110 // soft dark shadow
	outlineDivisor   = 170 // outline width = vp.H / divisor (scales with the stage), floored
	outlineWidthMin  = 2
	shadowMultiple   = 3 // shadow offset = outline width * this
	outlineCardinals = 4 // outlineDirs[:4] are the cardinals, the rest diagonals
	// screenShakeYFreqMul shakes y at an off ratio of x, so the shake is not a line.
	screenShakeYFreqMul = 1.3
)

func NewViewport(store *TextureStore) *Viewport {
	return &Viewport{store: store}
}

// SetSpriteFX sets the sprite colour and motion effects; the App calls it once per frame before Update.
func (v *Viewport) SetSpriteFX(fx SpriteFX) { v.fx = fx }

// AnimClock returns the motion clock Update advances, so text and sprite motion stay in step.
func (v *Viewport) AnimClock() time.Duration { return v.fxClock }

func (v *Viewport) Update(scene *courtroom.Scene, dt time.Duration) {
	// Advance the rainbow clock unconditionally; the modulo bounds it, and cycleForSpeed's floor can't panic.
	v.curCycle = cycleForSpeed(v.fx.Speed)
	v.rainbowPhase = (v.rainbowPhase + dt) % v.curCycle
	v.fxClock += dt        // free-running clock for the motion effects (wobble/spin)
	v.particles.update(dt) // #124 advance the weather pool (no-op when off)

	// Fire the punch once when a shout appears; Render applies it only when fx.ShoutPunch is on.
	shoutOn := scene.ShoutBase != ""
	if shoutOn && !v.prevShoutOn {
		v.punchLeft = shoutPunchDuration
	}
	v.prevShoutOn = shoutOn
	if v.punchLeft -= dt; v.punchLeft < 0 {
		v.punchLeft = 0
	}

	// A new speaker name arms the slide-in; Render applies it only when fx.Entrance is on.
	if sp := scene.Speaker.Name; sp != "" && sp != v.prevSpeaker {
		if v.prevSpeaker != "" { // don't slide the very first speaker of a fresh session
			v.entranceLeft = entranceDuration
		}
		v.prevSpeaker = sp
	}
	if v.entranceLeft -= dt; v.entranceLeft < 0 {
		v.entranceLeft = 0
	}

	shoutBase := effectiveShoutBase(scene, v.store)
	v.syncAnimSticky(&v.bgAnim, scene.BackgroundBase)
	// Desk after background: syncAnimDesk reads the bg's fresh base, so both swap in the same frame.
	v.syncAnimDesk(scene.DeskBase, scene.BackgroundBase)
	v.syncAnim(&v.shoutAnim, shoutBase)
	v.syncAnim(&v.speakerAnim, scene.Speaker.Active)
	v.syncAnim(&v.pairAnim, scene.Pair.Active)
	if len(v.groupAnims) != len(scene.Group) {
		v.groupAnims = make([]animState, len(scene.Group))
	}
	for i := range scene.Group {
		v.syncAnim(&v.groupAnims[i], scene.Group[i].Active)
	}
	v.syncAnim(&v.speedlineAnim, effectiveSpeedlineBase(scene, v.store))
	v.reduceInactiveAnimations(scene)
	// Hold-previous max-age clock.
	v.tickCold(&v.speakerAnim, dt)
	v.tickCold(&v.pairAnim, dt)
	for i := range scene.Group {
		v.tickCold(&v.groupAnims[i], dt)
	}

	if page, ok := v.bgAnim.resolve(v.store); ok {
		v.bgAnim.advance(page, dt, false)
	}
	if page, ok := v.deskAnim.resolve(v.store); ok {
		v.deskAnim.advance(page, dt, false)
	}
	if page, ok := v.speedlineAnim.resolve(v.store); ok {
		v.speedlineAnim.advance(page, dt, false)
	}
	if shoutBase != "" {
		if page, ok := v.shoutAnim.resolve(v.store); ok {
			v.shoutAnim.advance(page, dt, true)
		}
	}
	if scene.Speaker.Visible {
		if page, ok := v.speakerAnim.resolve(v.store); ok {
			// Report a multi-frame preanim's duration once, so the fallback timeout can't cut it short.
			if scene.Speaker.PlayOnce && !v.speakerAnim.startReported && v.OnPreanimStart != nil && !page.Partial && len(page.Frames) > 1 {
				v.speakerAnim.startReported = true
				v.OnPreanimStart(pageDuration(page))
			}
			if v.advanceSpeaker(page, dt, scene.Speaker.PlayOnce) {
				if v.OnPreanimDone != nil {
					v.OnPreanimDone()
				}
			}
			// Report after advance so it reflects this step's landing frame.
			v.reportSpeakerFrame(page)
		}
	}
	if scene.PairActive {
		if page, ok := v.pairAnim.resolve(v.store); ok {
			v.pairAnim.advance(page, dt, false)
		}
	}
	for i := range scene.Group {
		if page, ok := v.groupAnims[i].resolve(v.store); ok {
			v.groupAnims[i].advance(page, dt, false)
		}
	}
	// loop=true makes cull inert; loop=false holds its last frame unless culled (animationlayer.cpp:342-348, 660-666).
	v.syncOverlay(&scene.Overlay)
	if scene.Overlay.Active() {
		if page, ok := v.overlayAnim.resolve(v.store); ok {
			v.overlayAnim.advanceMax(page, dt, !scene.Overlay.Loop, scene.Overlay.MaxFrameDelay)
		}
	}
}

// syncOverlay rebinds the overlay and also watches Gen, so the same effect twice in a row restarts.
func (v *Viewport) syncOverlay(o *courtroom.SceneOverlay) {
	if v.overlayAnim.base == o.Base && v.overlayGen == o.Gen {
		return
	}
	v.overlayGen = o.Gen
	v.overlayAnim.reset(o.Base)
}

// overlayAnimating reports a live multi-frame overlay; a single frame must not hold the frame pacer up.
func (v *Viewport) overlayAnimating(scene *courtroom.Scene) bool {
	if !scene.Overlay.Active() {
		return false
	}
	page := v.overlayAnim.page
	if page == nil || len(page.Frames) <= 1 {
		return false
	}
	return scene.Overlay.Loop || !v.overlayAnim.finished
}

// NextAnimDue returns the next animation deadline: 0 during a ramp, ok=false when nothing is scheduled.
func (v *Viewport) NextAnimDue(scene *courtroom.Scene) (time.Duration, bool) {
	// Each ramp checks its enabling option, so a countdown tracked in Update but invisible doesn't hold the rate up.
	if v.fx.ShoutPunch && v.punchLeft > 0 {
		return 0, true
	}
	if v.fx.Entrance && v.entranceLeft > 0 {
		return 0, true
	}
	if v.crossfade > 0 && (fadeRunning(&v.speakerAnim) || fadeRunning(&v.pairAnim)) {
		return 0, true
	}
	due := time.Duration(-1)
	considerMax := func(a *animState, visible bool, maxDelay time.Duration) {
		if !visible || a.finished || a.page == nil || len(a.page.Frames) <= 1 {
			return
		}
		// The pacer reads cached state, so clamp: a re-decode can swap in a shorter page between resolves.
		delay := a.page.Delays[clampFrame(a.frame, len(a.page.Frames))]
		if maxDelay > 0 && delay > maxDelay {
			delay = maxDelay // the overlay's per-frame clamp shortens its own deadline too
		}
		if delay <= 0 {
			return // advance() treats a non-positive delay as frozen — not animating
		}
		d := delay - a.elapsed
		if d < 0 {
			d = 0
		}
		if due < 0 || d < due {
			due = d
		}
	}
	consider := func(a *animState, visible bool) { considerMax(a, visible, 0) }
	consider(&v.bgAnim, true)
	consider(&v.deskAnim, true)
	// The zoom speedline is scheduled too; without a deadline its frames flip only when something else forces a redraw.
	consider(&v.speedlineAnim, v.speedlineAnim.base != "")
	consider(&v.shoutAnim, v.shoutAnim.base != "")
	consider(&v.speakerAnim, scene.Speaker.Visible)
	consider(&v.pairAnim, scene.PairActive)
	// The overlay's next frame, so an idle-capped client wakes for it instead of sleeping through the clip.
	considerMax(&v.overlayAnim, scene.Overlay.Active(), scene.Overlay.MaxFrameDelay)
	if due < 0 {
		return 0, false
	}
	// Decoders keep any positive delay, so a 1-10 ms asset would stop the pacing sleep and free-run the loop.
	if due < minAnimFrameDelay {
		due = minAnimFrameDelay
	}
	return due, true
}

// minAnimFrameDelay floors NextAnimDue's deadlines (~60 fps); transitions keep their unfloored path above.
const minAnimFrameDelay = 16667 * time.Microsecond

// fadeRunning reports an advancing crossfade; one armed toward a 404 sprite must not hold uncapped redraws.
func fadeRunning(a *animState) bool {
	return a.fadeLeft > 0 && a.coldFor == 0
}

// AmbientAnimating reports stage animation on the FX clock, which NextAnimDue can't see: it has no deadline.
func (v *Viewport) AmbientAnimating(scene *courtroom.Scene) bool {
	if scene == nil {
		return false
	}
	if v.particles.weather != WeatherNone && v.particles.n > 0 {
		return true // live weather rides the clock across the whole stage
	}
	breath := v.fx.IdleBreath && (v.fx.BreathBob || v.fx.BreathScale)
	layerAnimates := func(layer *courtroom.SpriteLayer) bool {
		if breath {
			return true // breathing applies to every drawn sprite, styled or not
		}
		if st := layer.Style; st.Active() {
			// A transmitted style wins over the local wash, as in drawSprite.
			return st.HueCycle || st.Wobble || st.Spin || st.Glitch ||
				st.Motion != 0 || st.PathLen >= 2
		}
		return v.fx.Rainbow || v.fx.Wobble || v.fx.Spin
	}
	if scene.Speaker.Visible && layerAnimates(&scene.Speaker) {
		return true
	}
	if scene.PairActive && layerAnimates(&scene.Pair) {
		return true
	}
	// A live overlay also keeps the chrome-anim tier awake through a looping wash under the idle cap.
	return v.overlayAnimating(scene)
}

func (v *Viewport) syncAnim(a *animState, base string) {
	if a.base != base {
		a.reset(base)
		// lastGood is only set by drawSprite, so shout-bubble layers (drawFill) can never arm a fade.
		if v.crossfade > 0 && a.lastGood != "" && a.lastGood != base {
			a.fadeLeft = v.crossfade
		} else {
			a.fadeLeft = 0
		}
	}
}

// reduceInactiveAnimations collapses non-speaker animations to frame 0; they re-stream when active.
func (v *Viewport) reduceInactiveAnimations(scene *courtroom.Scene) {
	if scene.Speaker.IdleBase == v.prevActiveIdle &&
		scene.Speaker.TalkBase == v.prevActiveTalk &&
		scene.Speaker.PreanimBase == v.prevActivePreanim {
		return
	}
	v.prevActiveIdle = scene.Speaker.IdleBase
	v.prevActiveTalk = scene.Speaker.TalkBase
	v.prevActivePreanim = scene.Speaker.PreanimBase
	const speakerSprites = 3 // idle, talk, preanim
	active := make(map[string]bool, speakerSprites)
	if scene.Speaker.IdleBase != "" {
		active[scene.Speaker.IdleBase] = true
	}
	if scene.Speaker.TalkBase != "" {
		active[scene.Speaker.TalkBase] = true
	}
	if scene.Speaker.PreanimBase != "" {
		active[scene.Speaker.PreanimBase] = true
	}
	v.store.ReduceAnimatedExcept(active)
}

// syncAnimSticky rebinds scenery once the new base is resident, so a position flip doesn't blank it.
func (v *Viewport) syncAnimSticky(a *animState, base string) {
	if a.base == base {
		return
	}
	if base == "" || v.store.Contains(base) {
		a.reset(base)
	}
}

// syncAnimDesk is syncAnimSticky for the desk, plus the two release conditions the background doesn't need.
func (v *Viewport) syncAnimDesk(deskBase, bgBase string) {
	if v.deskAnim.base == deskBase {
		return
	}
	if deskBase == "" || v.store.Contains(deskBase) || v.store.IsMissing(deskBase) ||
		v.bgAnim.base == bgBase {
		v.deskAnim.reset(deskBase)
	}
}

// RebindScenery binds at once, as the residency check would keep the previous server's bg after a tab switch.
func (v *Viewport) RebindScenery(bgBase, deskBase string) {
	if v.bgAnim.base != bgBase {
		v.bgAnim.reset(bgBase)
	}
	if v.deskAnim.base != deskBase {
		v.deskAnim.reset(deskBase)
	}
}

// Render draws in AO order: background, characters (pair z-order), desk, shout; the flash paints over all.
func (v *Viewport) Render(ren *sdl.Renderer, scene *courtroom.Scene, vp sdl.Rect) {
	stage := vp // the unmodified stage frame, for the #10 post-FX overlays (not the punched art)
	// Inflate around the centre so every blit scales together; before the shake so the two compose.
	if v.fx.ShoutPunch && v.punchLeft > 0 {
		if s := punchScale(v.punchLeft); s > 0 {
			dw := int32(float64(vp.W) * s)
			dh := int32(float64(vp.H) * s)
			vp.X -= dw / 2
			vp.Y -= dh / 2
			vp.W += dw
			vp.H += dh
		}
	}
	if scene.ShakeLeft > 0 {
		// Amplitude follows the remaining time, so the shake settles instead of cutting off.
		strength := float64(scene.ShakeLeft) / float64(courtroom.ScreenshakeDuration)
		amp := float64(vp.W) / shakeAmplitudeDivisor * strength
		phase := 2 * math.Pi * float64(scene.ShakeLeft) / float64(shakePeriod)
		vp.X += int32(amp * math.Sin(phase))
		vp.Y += int32(amp / 2 * math.Cos(phase*screenShakeYFreqMul))
	}

	// Contain full-viewport fills to the unshaken stage, deferring to an active camera-zoom clip.
	contain := !ren.IsClipEnabled()
	if contain {
		v.stageClip = stage
		_ = ren.SetClipRect(&v.stageClip)
	}
	if v.fx.DoF {
		v.drawBackgroundDoF(ren, scene.BackgroundBase, &v.bgAnim, vp) // #11 soft-focus + dim
	} else {
		v.drawFill(ren, scene.BackgroundBase, &v.bgAnim, vp, percentMax) // bg full-bright (spotlight dims pair+desk, not bg)
	}
	// Speaker's speedlines during a zoom emote: over the background, under the characters.
	if base := effectiveSpeedlineBase(scene, v.store); base != "" {
		v.drawFill(ren, base, &v.speedlineAnim, vp, percentMax)
	}
	if contain {
		_ = ren.SetClipRect(nil)
	}

	// Half a period gives the pair opposite hues; the speaker always passes 0.
	var pairShift time.Duration
	if v.fx.Rainbow && v.fx.PairDesync {
		pairShift = v.curCycle / 2
	}

	// Spotlight dims pair and desk, not the background, which depth of field dims, so they don't double up.
	const noDim = 100
	spotPct := noDim
	if v.fx.Spotlight {
		spotPct = spotlightBrightness(v.fx.SpotlightLevel)
	}
	// The slide rect is the speaker's only; the pair stays put.
	spkVP := vp
	if v.fx.Entrance && v.entranceLeft > 0 {
		spkVP.X += entranceSlide(v.entranceLeft, vp.W)
	}
	// layer = behind: stackUnder(ui_vp_player_char) (courtroom.cpp:3218-3222), under every sprite.
	v.drawOverlayInStage(ren, scene, vp, stage, courtroom.OverlayLayerBehind)
	// Clip sprites to the original stage, only when no clip is active, so the reflection's own clip isn't overwritten.
	spriteClip := v.clipSprites && !ren.IsClipEnabled()
	if spriteClip {
		v.maskClip = stage
		_ = ren.SetClipRect(&v.maskClip)
	}
	// Group pairing (JSON-only GP roster): draw other members first, front-most last, so the roster's z-order holds.
	for i := range scene.Group {
		v.drawSprite(ren, &scene.Group[i], &v.groupAnims[i], vp, 0, spotPct)
	}
	if scene.PairActive && !scene.SpeakerInFront {
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, spkVP, 0, noDim)
		v.drawSprite(ren, &scene.Pair, &v.pairAnim, vp, pairShift, spotPct)
	} else if scene.PairActive {
		v.drawSprite(ren, &scene.Pair, &v.pairAnim, vp, pairShift, spotPct)
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, spkVP, 0, noDim)
	} else {
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, spkVP, 0, noDim)
	}
	if spriteClip {
		_ = ren.SetClipRect(nil)
	}

	// Drawn after the sprites and before the desk, so the desk occludes the reflection.
	if v.fx.Reflection {
		v.drawReflections(ren, scene, vp, spkVP)
	}
	// layer = character: stackUnder(ui_vp_desk) (courtroom.cpp:3223-3227), over the characters and under the desk.
	v.drawOverlayInStage(ren, scene, vp, stage, courtroom.OverlayLayerCharacter)
	if scene.ShowDesk {
		// Clip the desk to the unshaken stage, deferring to an active camera-zoom clip.
		contain := !ren.IsClipEnabled()
		if contain {
			v.stageClip = stage
			_ = ren.SetClipRect(&v.stageClip)
		}
		v.drawFill(ren, scene.DeskBase, &v.deskAnim, vp, spotPct)
		if contain {
			_ = ren.SetClipRect(nil)
		}
	}
	// layer = over: raise() inside the viewport (courtroom.cpp:3228-3232), above the desk and below the shout.
	v.drawOverlayInStage(ren, scene, vp, stage, courtroom.OverlayLayerOver)
	if shoutBase := effectiveShoutBase(scene, v.store); shoutBase != "" {
		// Not drawFill: AO2's splash layer fits the bubble by height and centres it, never stretching.
		v.drawSplash(ren, shoutBase, &v.shoutAnim, vp)
	}

	if scene.FlashLeft > 0 {
		frac := float64(scene.FlashLeft) / float64(courtroom.RealizationFlashDuration)
		if frac > 1 {
			frac = 1
		}
		// Clip the flash to the unshaken stage, deferring to an active camera-zoom clip.
		contain := !ren.IsClipEnabled()
		if contain {
			v.stageClip = stage
			_ = ren.SetClipRect(&v.stageClip)
		}
		v.fillRect = vp
		_ = ren.SetDrawColor(channelMax, channelMax, channelMax, uint8(channelMax*frac))
		_ = ren.FillRect(&v.fillRect)
		if contain {
			_ = ren.SetClipRect(nil)
		}
	}

	v.particles.draw(ren, stage) // #124 ambient weather over the stage (free when off), under the post-FX
	v.applyPostFX(ren, stage)    // #10 retro overlays over the whole stage frame (free when off)
}

// Bundled speedline keys, pinned by the UI at startup, for a speaker with no <side>_speedlines of their own.
const (
	SpeedlineDefenseKey     = "theme://speedline/defense"
	SpeedlineProsecutionKey = "theme://speedline/prosecution"
)

// SpeedlineFallbackKey maps a ZoomSide ("defense"/"prosecution") to its bundled key, "" for anything else.
func SpeedlineFallbackKey(side string) string {
	switch side {
	case "defense":
		return SpeedlineDefenseKey
	case "prosecution":
		return SpeedlineProsecutionKey
	}
	return ""
}

// effectiveSpeedlineBase picks the first resident speedline, else the bundled burst; "" means no zoom emote.
func effectiveSpeedlineBase(scene *courtroom.Scene, store *TextureStore) string {
	if scene.SpeedlinesSide == "" {
		return ""
	}
	for _, cand := range scene.SpeedlinesChain {
		if cand != "" && store.Contains(cand) {
			return cand
		}
	}
	return SpeedlineFallbackKey(scene.SpeedlinesSide)
}

// effectiveShoutBase picks the character's resident bubble, else the default, as most ship none (as AO2-Client).
func effectiveShoutBase(scene *courtroom.Scene, store *TextureStore) string {
	if scene.ShoutBase == "" {
		return ""
	}
	if scene.ShoutFallbackBase != "" && !store.Contains(scene.ShoutBase) {
		return scene.ShoutFallbackBase
	}
	return scene.ShoutBase
}

// drawFill stretches a layer across the viewport; dimPct's ColorMod is restored as the T1 page is shared.
func (v *Viewport) drawFill(ren *sdl.Renderer, base string, anim *animState, vp sdl.Rect, dimPct int) {
	if base == "" {
		return
	}
	tex, _, _, ok := v.layerFrame(anim)
	if !ok {
		return
	}
	v.fillRect = vp
	if dimPct < percentMax {
		d := scaleChannel(channelMax, dimPct)
		_ = tex.SetColorMod(d, d, d)
	}
	_ = ren.Copy(tex, nil, &v.fillRect)
	if dimPct < percentMax {
		_ = tex.SetColorMod(channelMax, channelMax, channelMax) // restore the shared page
	}
}

// DrawOverlayChat draws the chat-layer overlay, which AO2 reparents over the chatbox (courtroom.cpp:3234-3237).
func (v *Viewport) DrawOverlayChat(ren *sdl.Renderer, scene *courtroom.Scene, vp sdl.Rect) {
	if scene == nil {
		return
	}
	v.drawOverlay(ren, scene, vp, courtroom.OverlayLayerChat)
}

// drawOverlay blits the overlay for layer; the caller contains it, except chat, which AO2 reparents.
func (v *Viewport) drawOverlay(ren *sdl.Renderer, scene *courtroom.Scene, vp sdl.Rect, layer uint8) {
	o := &scene.Overlay
	if o.Base == "" || o.Layer != layer {
		return
	}
	page, ok := v.overlayAnim.resolve(v.store)
	if !ok || len(page.Frames) == 0 {
		// Cold art draws nothing, like AO2's clear; no blocking.
		return
	}
	if v.overlayHidden(o) {
		return
	}
	tex := page.Frames[clampFrame(v.overlayAnim.frame, len(page.Frames))]
	dst := overlayDstRect(o, vp, page.W, page.H)
	page.applyScaleMode(ren, v.overlayScaleMode(o, page.H, dst.H))
	v.ovRect = dst
	if o.Flip {
		_ = ren.CopyEx(tex, nil, &v.ovRect, 0, nil, sdl.FLIP_HORIZONTAL)
		return
	}
	_ = ren.Copy(tex, nil, &v.ovRect)
}

// drawOverlayInStage clips to the stage, as Qt clips a child to ui_viewport (courtroom.cpp:3218-3232).
func (v *Viewport) drawOverlayInStage(ren *sdl.Renderer, scene *courtroom.Scene, vp, stage sdl.Rect, layer uint8) {
	o := &scene.Overlay
	if o.Base == "" || o.Layer != layer || v.overlayHidden(o) {
		return
	}
	clip := !ren.IsClipEnabled()
	if clip {
		v.ovClip = stage
		_ = ren.SetClipRect(&v.ovClip)
	}
	v.drawOverlay(ren, scene, vp, layer)
	if clip {
		_ = ren.SetClipRect(nil)
	}
}

// overlayHidden applies setHideWhenStopped(cull), for play-once clips only (animationlayer.cpp:660-666).
func (v *Viewport) overlayHidden(o *courtroom.SceneOverlay) bool {
	return o.Cull && v.overlayAnim.finished
}

// overlayDstRect computes the rect, which may pass the viewport as in AO2; drawOverlayInStage clips it.
func overlayDstRect(o *courtroom.SceneOverlay, vp sdl.Rect, pageW, pageH int32) sdl.Rect {
	dst := vp
	if !o.Stretch && pageW > 0 && pageH > 0 {
		dst.H = vp.H
		dst.W = int32(int64(pageW) * int64(vp.H) / int64(pageH))
		dst.X = vp.X + (vp.W-dst.W)/2
	}
	if o.OffsetX != 0 {
		dst.X += vp.W * int32(o.OffsetX) / offsetPercentDivisor
	}
	if o.OffsetY != 0 {
		dst.Y += vp.H * int32(o.OffsetY) / offsetPercentDivisor
	}
	return dst
}

// overlayScaleMode follows AO2's sprite precedence: user mode, effect's scaling key, point-sampled if enlarged.
func (v *Viewport) overlayScaleMode(o *courtroom.SceneOverlay, nativeH, targetH int32) sdl.ScaleMode {
	switch courtroom.ResolveScalingMode(v.fx.UserScaling, o.Scaling) {
	case courtroom.ScalingPixel:
		return sdl.ScaleModeNearest
	case courtroom.ScalingSmooth:
		return sdl.ScaleModeLinear
	}
	if !o.Stretch && nativeH > 0 && nativeH < targetH {
		return sdl.ScaleModeNearest
	}
	return sdl.ScaleModeLinear
}

// resolveHeld probes the single parked frame for a scenery layer whose base was evicted; callers draw frame 0.
func (v *Viewport) resolveHeld(a *animState) (*TexturePage, bool) {
	if a.heldKey == "" {
		return nil, false
	}
	page, ok := v.store.Get(a.heldKey)
	if !ok || len(page.Frames) == 0 || page.Frames[0] == nil {
		return nil, false
	}
	return page, true
}

// Mirror line and default strength of the reflection.
const (
	reflectFloorPct   = 85  // mirror axis at vp.H * pct/100 down (≈ where feet land)
	reflectMinAlpha   = 10  // floor so "on" always shows something
	reflectMaxAlpha   = 200 // ceiling so a reflection never reads as a second solid sprite
	reflectDefaultPct = 30  // default opacity when unset
)

// drawReflections mirrors the speaker and pair below the floor line, between the sprite and desk draws.
func (v *Viewport) drawReflections(ren *sdl.Renderer, scene *courtroom.Scene, vp, spkVP sdl.Rect) {
	floorY := vp.Y + vp.H*reflectFloorPct/percentMax
	alpha := reflectAlpha(v.fx.ReflectStrength)
	// Confine to the stage, unless a clip is already active (the zoom path), so the camera-zoom clip isn't overwritten.
	needClip := !ren.IsClipEnabled()
	if needClip {
		v.reflClip = vp
		_ = ren.SetClipRect(&v.reflClip)
	}
	v.drawReflection(ren, &scene.Speaker, &v.speakerAnim, spkVP, floorY, alpha)
	if scene.PairActive {
		v.drawReflection(ren, &scene.Pair, &v.pairAnim, vp, floorY, alpha)
	}
	if needClip {
		_ = ren.SetClipRect(nil)
	}
}

// drawReflection blits one sprite flipped around floorY, restoring alpha afterwards because the T1 page is shared.
func (v *Viewport) drawReflection(ren *sdl.Renderer, layer *courtroom.SpriteLayer, anim *animState, vp sdl.Rect, floorY int32, alpha uint8) {
	if !layer.Visible || layer.Active == "" {
		return
	}
	page, ok := anim.resolve(v.store)
	if !ok || len(page.Frames) == 0 {
		return
	}
	frame := clampFrame(anim.frame, len(page.Frames))
	scaledW := vp.H * page.W / page.H
	v.reflRect.W = scaledW
	v.reflRect.H = vp.H
	v.reflRect.X = vp.X + (vp.W-scaledW)/2 + vp.W*int32(layer.OffsetX)/offsetPercentDivisor
	v.reflRect.Y = floorY // flipped: the texture's bottom (feet) lands at the mirror axis
	var flip sdl.RendererFlip = sdl.FLIP_VERTICAL
	if layer.Flip {
		flip |= sdl.FLIP_HORIZONTAL
	}
	tex := page.Frames[frame]
	_ = tex.SetAlphaMod(alpha)
	_ = ren.CopyEx(tex, nil, &v.reflRect, 0, nil, flip)
	_ = tex.SetAlphaMod(channelMax) // restore the shared page
}

// reflectAlpha maps ReflectStrength [0,100] to an opacity clamped so a reflection never reads as a second solid sprite.
func reflectAlpha(strength int) uint8 {
	if strength <= 0 {
		strength = reflectDefaultPct
	}
	a := strength * channelMax / percentMax
	a = value.Clamp(a, reflectMinAlpha, reflectMaxAlpha)
	return uint8(a)
}

// Depth of field: with no blur shader, the bg is smeared by eight low-alpha offset ghosts, then dimmed.
const (
	dofBlurDivisor = 90  // ghost offset = vp.H / divisor (floored)
	dofBlurMin     = 3   //
	dofAlphaCard   = 120 // cardinal ghost strength
	dofAlphaDiag   = 80  // diagonal ghost strength
	dofDimAlpha    = 95  // darkening over the blurred bg
)

// drawBackgroundDoF draws the bg soft-focused and dimmed, restoring alpha as the T1 page is shared.
func (v *Viewport) drawBackgroundDoF(ren *sdl.Renderer, base string, anim *animState, vp sdl.Rect) {
	if base == "" {
		return
	}
	frame := 0
	page, ok := anim.resolve(v.store)
	if ok && len(page.Frames) > 0 {
		frame = clampFrame(anim.frame, len(page.Frames))
	} else if page, ok = v.resolveHeld(anim); !ok {
		return // no page, no held bridge frame — see drawFill
	}
	tex := page.Frames[frame]
	r := vp.H / dofBlurDivisor
	if r < dofBlurMin {
		r = dofBlurMin
	}
	v.fillRect = vp // the sharp base
	_ = ren.Copy(tex, nil, &v.fillRect)
	for i := range outlineDirs { // eight offset ghosts → a smear blur
		a := uint8(dofAlphaCard)
		if i >= outlineCardinals {
			a = dofAlphaDiag
		}
		_ = tex.SetAlphaMod(a)
		v.fillRect = vp
		v.fillRect.X += outlineDirs[i][0] * r
		v.fillRect.Y += outlineDirs[i][1] * r
		_ = ren.Copy(tex, nil, &v.fillRect)
	}
	_ = tex.SetAlphaMod(channelMax)
	v.fillRect = vp // dim overlay, pushing the bg behind the sharp speaker
	_ = ren.SetDrawColor(0, 0, 0, dofDimAlpha)
	_ = ren.FillRect(&v.fillRect)
}

// SetSpriteLoadMode picks what a character layer draws while its new sprite loads.
func (v *Viewport) SetSpriteLoadMode(mode int) { v.spriteLoadMode = mode }

// SetClipSprites toggles confining character sprites to the stage.
func (v *Viewport) SetClipSprites(on bool) { v.clipSprites = on }

func (v *Viewport) SetHoldMaxAge(d time.Duration) { v.holdMaxAge = d }

// SetHoldDebugTint washes held stand-in sprites amber, as a diagnostic.
func (v *Viewport) SetHoldDebugTint(on bool) { v.tintHeld = on }

// SetThumbSprites toggles the thumbnail stand-in on the cold-load miss path.
func (v *Viewport) SetThumbSprites(on bool) { v.thumbSprites = on }

// SetMissingno toggles AO2's placeholder for a sprite confirmed missing.
func (v *Viewport) SetMissingno(on bool) { v.missingnoEnabled = on }

// SetCrossfade sets the speaker-swap blend duration (0 = hard swap); the App passes 0 under Reduce motion.
func (v *Viewport) SetCrossfade(d time.Duration) { v.crossfade = d }

// SetPreanimLoop toggles looping preanims, deliberately unlike AO2; OnPreanimDone still fires once.
func (v *Viewport) SetPreanimLoop(on bool) { v.preanimLoop = on }

// ThumbKeyPrefix namespaces thumbnails in T1; a scheme prefix can't collide with an asset URL.
const ThumbKeyPrefix = "thumb://"

// tickCold tracks a layer's cold time and ticks a crossfade only while resident.
func (v *Viewport) tickCold(a *animState, dt time.Duration) {
	if a.base == "" {
		a.coldFor = 0
		return
	}
	if page, ok := a.resolve(v.store); ok && !page.Partial {
		a.coldFor = 0
		if a.fadeLeft > 0 {
			a.fadeLeft -= dt
		}
		return
	}
	a.coldFor += dt
}

// drawHeldSprite blits a stand-in during a cold load, deliberately unstyled, at the incoming offset and flip.
func (v *Viewport) drawHeldSprite(ren *sdl.Renderer, layer *courtroom.SpriteLayer, page *TexturePage, vp sdl.Rect) {
	if page.H == 0 {
		return
	}
	scaledW := vp.H * page.W / page.H
	v.dstRect.W = scaledW
	v.dstRect.H = vp.H
	v.dstRect.X = vp.X + (vp.W-scaledW)/2 + vp.W*int32(layer.OffsetX)/offsetPercentDivisor
	v.dstRect.Y = vp.Y + vp.H*int32(layer.OffsetY)/offsetPercentDivisor
	flip := sdl.FLIP_NONE
	if layer.Flip {
		flip = sdl.FLIP_HORIZONTAL
	}
	tex := page.Frames[0]
	if v.tintHeld {
		// Restored right after because the T1 page is shared.
		_ = tex.SetColorMod(heldTintR, heldTintG, heldTintB)
	}
	_ = ren.CopyEx(tex, nil, &v.dstRect, 0, nil, flip)
	if v.tintHeld {
		_ = tex.SetColorMod(channelMax, channelMax, channelMax)
	}
}

// heldTint* is the amber debug wash for stand-in sprites.
const (
	heldTintR = 255
	heldTintG = 190
	heldTintB = 120
)

// drawSprite draws a layer at viewport height, bottom-centred, offset; hueShift offsets its rainbow phase.
func (v *Viewport) drawSprite(ren *sdl.Renderer, layer *courtroom.SpriteLayer, anim *animState, vp sdl.Rect, hueShift time.Duration, dimPct int) {
	if !layer.Visible || layer.Active == "" {
		return
	}
	page, ok := anim.resolve(v.store)
	// A partial must not displace the hold-previous stand-in, or the old animation freezes during the decode.
	if ok && page.Partial && v.spriteLoadMode == SpriteLoadHoldPrev &&
		anim.lastGood != "" && anim.lastGood != anim.base &&
		(v.holdMaxAge <= 0 || anim.coldFor <= v.holdMaxAge) {
		if held, ok2 := v.store.Get(anim.lastGood); ok2 && len(held.Frames) > 0 {
			v.drawHeldSprite(ren, layer, held, vp)
			return
		}
	}
	if !ok || len(page.Frames) == 0 {
		// A missing base has no held or thumb page, and lastGood survives swaps: hold-previous would pin forever.
		// Only still-loading sprites take the chain below; loading and confirmed missing are disjoint.
		if v.missingnoEnabled && !layer.PlayOnce && v.store.IsMissing(anim.base) {
			if mp, ok2 := v.store.Get(MissingKey); ok2 && len(mp.Frames) > 0 {
				v.drawHeldSprite(ren, layer, mp, vp)
				return
			}
		}
		// A parked frame of this same sprite at full quality beats the thumbnail and a different character.
		if held, ok2 := v.resolveHeld(anim); ok2 {
			v.drawHeldSprite(ren, layer, held, vp)
			return
		}
		// The thumbnail wins next: the right character at low quality beats the previous one at full quality.
		if v.thumbSprites && anim.thumbKey != "" {
			if tp, ok2 := v.store.Get(anim.thumbKey); ok2 && len(tp.Frames) > 0 {
				v.drawHeldSprite(ren, layer, tp, vp)
				return
			}
		}
		// Hold by string as the LRU owns lifetime; draw path only, so the preanim lifecycle never sees it.
		if v.spriteLoadMode == SpriteLoadHoldPrev && anim.lastGood != "" && anim.lastGood != anim.base &&
			(v.holdMaxAge <= 0 || anim.coldFor <= v.holdMaxAge) { // max-age knob: 0 = bridge forever
			if held, ok2 := v.store.Get(anim.lastGood); ok2 && len(held.Frames) > 0 {
				v.drawHeldSprite(ren, layer, held, vp)
			}
		}
		return
	}
	// During a crossfade lastGood must keep naming the old sprite while it draws under the ramp.
	if anim.fadeLeft <= 0 && anim.lastGood != anim.base {
		anim.lastGood = anim.base
	}
	frame := clampFrame(anim.frame, len(page.Frames))

	// AO2 sizes a layer to the viewport once (courtroom.cpp:778); a zoom emote doesn't rescale it (:3456-3477).
	h := vp.H
	w := h * page.W / page.H
	v.dstRect.W = w
	v.dstRect.H = h
	v.dstRect.X = vp.X + (vp.W-w)/2 + vp.W*int32(layer.OffsetX)/offsetPercentDivisor
	v.dstRect.Y = vp.Y + (vp.H - h) + vp.H*int32(layer.OffsetY)/offsetPercentDivisor

	flip := sdl.FLIP_NONE
	if layer.Flip {
		flip = sdl.FLIP_HORIZONTAL
	}
	tex := page.Frames[frame]
	// drawPage owns the blitted texture, so the scale mode lands on variant pages with their own filter state.
	drawPage := page
	// Hue paint uses a luma-preserving variant, as a multiply only removes light; huePainted skips the tint.
	huePainted := false
	if st := layer.Style; st.Tint && st.Grayscale && !st.HueCycle &&
		st.Variant() == courtroom.VariantGrayscale {
		if pp, ok := v.store.PaintPage(layer.Active, st.R, st.G, st.B,
			st.Paint2R, st.Paint2G, st.Paint2B, st.PaintSplit); ok && frame < len(pp.Frames) {
			tex, drawPage = pp.Frames[frame], pp
			huePainted = true
		}
	}
	// Invert and grayscale need a pixel variant, which SetColorMod can't do; the mod still applies on top.
	if eff := layer.Style.Variant(); eff != courtroom.VariantNone && !huePainted {
		if vpg, ok := v.store.VariantPage(layer.Active, eff); ok && frame < len(vpg.Frames) {
			tex, drawPage = vpg.Frames[frame], vpg
		}
	}
	// AO2 filters per sprite, from frame height against the widget height (calculateFrameGeometry, courtroom.cpp:778).
	// Must be vp.H, not v.dstRect.H: dstRect grows with a transmitted scale or zoom, changing the filter.
	drawPage.applyScaleMode(ren, v.spriteScaleMode(layer, anim.spriteNativeH(layer, page), vp.H))

	// A transmitted style takes precedence over the viewer's local wash for this layer.
	var (
		modR, modG, modB                    uint8 = 255, 255, 255
		alphaMod                            uint8 = 255
		doColorMod, glow, wob, spin, glitch bool
		motion                              uint8     // #34 transmitted movement path (0 = none)
		pathLen                             uint8     // #34 custom-path point count (0 = none, else 2..16)
		pathPts                             [16]uint8 // #34 custom-path waypoints (size MUST match courtroom maxPathPoints)
		scalePct                            = 100
		rotDeg                              float64
		glitchMode                          uint8 // transmitted glitch look (courtroom.Glitch*; 0 = classic)
		gAR, gAG, gAB, gBR, gBG, gBB        uint8 // fringe ghost colour pair (all-zero = classic red/blue)
	)
	if st := layer.Style; st.Active() {
		// Hue-cycle or tint, optionally dimmed by brightness, all collapse into one ColorMod.
		if st.HueCycle {
			phase := v.rainbowPhase + hueShift
			if v.curCycle > 0 {
				phase %= v.curCycle
			}
			modR, modG, modB = rainbowMod(phase, v.curCycle, floorForVividness(styleHueVividness))
			doColorMod = true
		} else if st.Tint && !huePainted {
			// Skipped for hue paint: its pixels already carry the colour, and multiplying would darken them again.
			modR, modG, modB, doColorMod = st.R, st.G, st.B, true
		}
		if b := st.BrightnessPct(); b != percentMax {
			if !doColorMod {
				modR, modG, modB, doColorMod = 255, 255, 255, true
			}
			modR, modG, modB = scaleChannel(modR, b), scaleChannel(modG, b), scaleChannel(modB, b)
		}
		alphaMod = st.AlphaMod() // opacity (floored so a received style can't go invisible)
		glow, wob, spin = st.Glow, st.Wobble, st.Spin
		motion = st.Motion
		pathPts, pathLen = st.Path, st.PathLen // #34 custom path (a cheap fixed-size array copy)
		glitch = st.Glitch
		glitchMode = st.GlitchMode
		gAR, gAG, gAB = st.GlitchAR, st.GlitchAG, st.GlitchAB
		gBR, gBG, gBB = st.GlitchBR, st.GlitchBG, st.GlitchBB
		scalePct = st.ScalePct()
		rotDeg = st.RotationDeg()
		if st.FlipH { // transmitted mirror toggles the layer's own flip
			if flip == sdl.FLIP_HORIZONTAL {
				flip = sdl.FLIP_NONE
			} else {
				flip = sdl.FLIP_HORIZONTAL
			}
		}
	} else {
		fx := v.fx
		if fx.tinted() {
			doColorMod = true
			if fx.Rainbow {
				phase := v.rainbowPhase + hueShift
				if fx.PerCharHue { // each character a different hue at once
					phase += charHueOffset(layer.Active, v.curCycle)
				}
				if v.curCycle > 0 { // defensive: curCycle is floored > 0 in Update
					phase %= v.curCycle
				}
				modR, modG, modB = rainbowMod(phase, v.curCycle, floorForVividness(fx.Vividness))
			} else {
				modR, modG, modB = fx.SolidR, fx.SolidG, fx.SolidB
			}
			glow = fx.Glow // local glow rides the local tint (unchanged)
		}
		wob, spin = fx.Wobble, fx.Spin
	}

	// Dim on top of any resolved tint, so a dimmed pair keeps its style; the speaker passes noDim.
	if dimPct < percentMax {
		modR, modG, modB = scaleChannel(modR, dimPct), scaleChannel(modG, dimPct), scaleChannel(modB, dimPct)
		doColorMod = true
	}

	// The old sprite draws underneath as this one ramps in; if evicted, it plays over the background.
	if anim.fadeLeft > 0 && v.crossfade > 0 && anim.lastGood != "" && anim.lastGood != anim.base {
		if under, okU := v.store.Get(anim.lastGood); okU && len(under.Frames) > 0 {
			v.drawHeldSprite(ren, layer, under, vp)
		}
		ramp := channelMax - int(channelMax*anim.fadeLeft/v.crossfade) // 0 → 255 across the fade
		if ramp < 0 {
			ramp = 0
		}
		alphaMod = uint8(int(alphaMod) * ramp / channelMax)
	}

	// Fold the breathing pulse into scalePct and keep the bob to add after the wobble.
	var breathBobY int32
	if v.fx.IdleBreath {
		bobY, addScale := breathTransform(v.fxClock, vp, v.fx.BreathAmp, v.fx.BreathSpeed)
		if v.fx.BreathBob {
			breathBobY = bobY
		}
		if v.fx.BreathScale {
			scalePct += addScale
		}
	}

	// Fold the flicker into alpha before the mods are applied; the jolt and fringe run after them.
	if glitch && glitchMode == courtroom.GlitchStatic {
		alphaMod = uint8(int(alphaMod) * glitchFlickerPct(v.fxClock) / percentMax)
	}

	// Restore each mod after the blit so it doesn't bleed onto the next user of this shared T1 page.
	if doColorMod {
		_ = tex.SetColorMod(modR, modG, modB)
	}
	if alphaMod != channelMax {
		_ = tex.SetAlphaMod(alphaMod)
	}
	if glow {
		_ = tex.SetBlendMode(sdl.BLENDMODE_ADD)
	}
	// Scale around the sprite's centre, not a corner.
	if scalePct != percentMax {
		nw := v.dstRect.W * int32(scalePct) / percentMax
		nh := v.dstRect.H * int32(scalePct) / percentMax
		v.dstRect.X += (v.dstRect.W - nw) / 2
		v.dstRect.Y += (v.dstRect.H - nh) / 2
		v.dstRect.W, v.dstRect.H = nw, nh
	}
	if wob {
		dx, dy := wobbleOffset(v.fxClock, vp)
		v.dstRect.X += dx
		v.dstRect.Y += dy
	}
	// A custom path wins over a predefined motion.
	switch {
	case pathLen >= 2:
		mdx, mdy := pathOffset(pathPts[:], pathLen, v.fxClock, vp)
		v.dstRect.X += mdx
		v.dstRect.Y += mdy
	case motion != 0:
		mdx, mdy := motionOffset(motion, v.fxClock, vp)
		v.dstRect.X += mdx
		v.dstRect.Y += mdy
	}
	if breathBobY != 0 { // #122 idle breathing's vertical bob (folded scale was applied above)
		v.dstRect.Y += breathBobY
	}
	// A jolt moves the whole sprite (solid, outline and fringe) together; glitchSplit is the fringe's chromatic offset.
	var glitchSplit int32
	if glitch {
		var jolt int32
		glitchSplit, jolt = glitchParamsMode(glitchMode, v.fxClock, vp.H)
		v.dstRect.X += jolt
	}
	angle := rotDeg // transmitted fixed tilt (spin adds to it)
	if spin {
		angle += spinDegrees(v.fxClock)
	}
	// Drawn behind the sprite with the final rect, angle and flip, so it tracks scale, wobble and rotation.
	if st := layer.Style; st.Outline || st.DropShadow {
		if sil, ok := v.store.VariantPage(layer.Active, courtroom.VariantSilhouette); ok && frame < len(sil.Frames) {
			silTex := sil.Frames[frame]
			ow := vp.H / outlineDivisor
			if ow < outlineWidthMin {
				ow = outlineWidthMin
			}
			if st.DropShadow {
				v.drawSilhouette(ren, silTex, angle, flip, shadowDir[:], ow*shadowMultiple, 0, 0, 0, shadowAlpha)
			}
			if st.Outline {
				or, og, ob := uint8(channelMax), uint8(channelMax), uint8(channelMax) // white by default
				if st.OutlineR != 0 || st.OutlineG != 0 || st.OutlineB != 0 {
					or, og, ob = st.OutlineR, st.OutlineG, st.OutlineB // #M5+ custom outline colour
				}
				v.drawSilhouette(ren, silTex, angle, flip, outlineDirs[:], ow, or, og, ob, outlineAlpha)
			}
		}
	}
	// Rotated sprites keep the plain blit: CopyEx rotates each band around its own centre, which shreds the art.
	if glitch && glitchMode == courtroom.GlitchTorn && angle == 0 &&
		v.fxClock%glitchTornPeriod < glitchTornWindow {
		v.drawTornSprite(ren, tex, page, flip, glitchSplit)
	} else {
		_ = ren.CopyEx(tex, nil, &v.dstRect, angle, nil, flip)
	}
	if doColorMod {
		_ = tex.SetColorMod(channelMax, channelMax, channelMax)
	}
	if alphaMod != channelMax {
		_ = tex.SetAlphaMod(channelMax)
	}
	if glow {
		_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	}
	// The mods above are already restored, so the fringe sets and restores its own.
	if glitch {
		v.drawGlitchFringe(ren, tex, angle, flip, glitchSplit, glitchMode, gAR, gAG, gAB, gBR, gBG, gBB)
	}
}

// drawGlitchFringe overlays two tinted ghosts (all-zero = red/blue), restoring mods on the shared texture.
func (v *Viewport) drawGlitchFringe(ren *sdl.Renderer, tex *sdl.Texture, angle float64, flip sdl.RendererFlip, split int32, mode uint8, aR, aG, aB, bR, bG, bB uint8) {
	if aR == 0 && aG == 0 && aB == 0 && bR == 0 && bG == 0 && bB == 0 {
		aR, bB = 255, 255 // the classic pair: red left, blue right
	}
	adx, ady := -split, int32(0)
	bdx, bdy := split, int32(0)
	if mode == courtroom.GlitchStatic { // signal loss: the ghosts jitter around the sprite
		h := glitchHash(uint32(v.fxClock / glitchStaticBucket))
		amp := split * glitchStaticAmpMul
		// Four nibbles of the hash, one per offset, each scaled to 0..amp.
		const nibbleBits, nibbleMax = 4, 0xF
		adx -= int32(h&nibbleMax) * amp / nibbleMax
		h >>= nibbleBits
		ady = int32(h&nibbleMax)*amp/nibbleMax - amp/2
		h >>= nibbleBits
		bdx += int32(h&nibbleMax) * amp / nibbleMax
		h >>= nibbleBits
		bdy = int32(h&nibbleMax)*amp/nibbleMax - amp/2
	}
	if mode == courtroom.GlitchEcho { // a far, faint echo pair behind the main fringe
		_ = tex.SetAlphaMod(glitchFringeAlpha / glitchEchoFadeDiv)
		v.blitFringeGhost(ren, tex, angle, flip, adx*glitchEchoFarMul, 0, aR, aG, aB)
		v.blitFringeGhost(ren, tex, angle, flip, bdx*glitchEchoFarMul, 0, bR, bG, bB)
	}
	_ = tex.SetAlphaMod(glitchFringeAlpha)
	v.blitFringeGhost(ren, tex, angle, flip, adx, ady, aR, aG, aB)
	v.blitFringeGhost(ren, tex, angle, flip, bdx, bdy, bR, bG, bB)
	_ = tex.SetColorMod(channelMax, channelMax, channelMax)
	_ = tex.SetAlphaMod(channelMax)
}

// blitFringeGhost draws one tinted ghost; the caller owns the alpha bracket and the final restore.
func (v *Viewport) blitFringeGhost(ren *sdl.Renderer, tex *sdl.Texture, angle float64, flip sdl.RendererFlip, dx, dy int32, r, g, b uint8) {
	_ = tex.SetColorMod(r, g, b)
	v.fxRect = v.dstRect
	v.fxRect.X += dx
	v.fxRect.Y += dy
	_ = ren.CopyEx(tex, nil, &v.fxRect, angle, nil, flip)
}

// drawTornSprite draws bands, two thirds shoved by a (band, bucket) hash; rows map 1:1, so flip stays correct.
func (v *Viewport) drawTornSprite(ren *sdl.Renderer, tex *sdl.Texture, page *TexturePage, flip sdl.RendererFlip, split int32) {
	bucket := glitchHash(uint32(v.fxClock / glitchTornBucket))
	maxOff := split * glitchTornOffMul
	for i := int32(0); i < glitchTornBands; i++ {
		sy0 := page.H * i / glitchTornBands
		sy1 := page.H * (i + 1) / glitchTornBands
		dy0 := v.dstRect.H * i / glitchTornBands
		dy1 := v.dstRect.H * (i + 1) / glitchTornBands
		var off int32
		if h := glitchHash(bucket + uint32(i)*glitchTornBandSalt); h%3 != 0 { // ~2/3 of the bands tear
			off = int32(h%uint32(2*maxOff+1)) - maxOff
		}
		v.srcRect = sdl.Rect{X: 0, Y: sy0, W: page.W, H: sy1 - sy0}
		v.fxRect = sdl.Rect{X: v.dstRect.X + off, Y: v.dstRect.Y + dy0, W: v.dstRect.W, H: dy1 - dy0}
		_ = ren.CopyEx(tex, &v.srcRect, &v.fxRect, 0, nil, flip)
	}
}

// Entrance slide-in tuning.
const (
	entranceDuration  = 320 * time.Millisecond // slide settle time
	entranceSlideFrac = 0.22                   // start offset = vp.W * this (slides from the left)
)

// entranceSlide returns the speaker's negative horizontal offset for the remaining time, easing to 0.
func entranceSlide(left time.Duration, vpW int32) int32 {
	frac := float64(left) / float64(entranceDuration)
	if frac > 1 {
		frac = 1
	}
	return -int32(float64(vpW) * entranceSlideFrac * frac * frac)
}

// Glitch tuning: a shimmering chromatic split and a periodic horizontal jolt.
const (
	glitchSplitDivisor = 220                     // base chromatic offset = vp.H / divisor (floored)
	glitchSplitMin     = 2                       //
	glitchFringeAlpha  = 150                     // chromatic ghost strength
	glitchShimmerHz    = 7.0                     // how fast the split pulses
	glitchJoltPeriod   = 1300 * time.Millisecond // a jolt every period
	glitchJoltWindow   = 90 * time.Millisecond   // …lasting this long
	glitchJoltMultiple = 3                       // jolt distance = split * this
)

// glitchParams returns the chromatic split and horizontal jolt in pixels for a stage of height vpH.
func glitchParams(clock time.Duration, vpH int32) (split, jolt int32) {
	split = vpH / glitchSplitDivisor
	if split < glitchSplitMin {
		split = glitchSplitMin
	}
	split += int32(math.Abs(math.Sin(clock.Seconds()*glitchShimmerHz)) * float64(split)) // shimmer
	if clock%glitchJoltPeriod < glitchJoltWindow {                                       // periodic jolt
		jolt = split * glitchJoltMultiple
		if int64(clock/glitchJoltPeriod)%2 == 0 {
			jolt = -jolt
		}
	}
	return split, jolt
}

// Per-mode glitch tuning.
const (
	glitchHeavySplitMul   = 2                      // Heavy: fringe distance × this
	glitchHeavyJoltPeriod = 700 * time.Millisecond // Heavy: jolts come almost twice as often
	glitchHeavyJoltWindow = 150 * time.Millisecond // …and hold longer
	glitchHeavyJoltMul    = 4                      // …and shove harder
	glitchEchoSplitMul    = 2                      // Echo: main fringe distance × this
	glitchEchoFarMul      = 3                      // Echo: the faint pair sits this × further out
	glitchEchoFadeDiv     = 3                      // Echo: the far pair's alpha = fringe / this
	glitchStaticBucket    = 50 * time.Millisecond  // Static: jitter/flicker re-roll cadence
	glitchStaticAmpMul    = 2                      // Static: ghost jitter amplitude = split × this
	glitchStaticFloorPct  = 70                     // Static: normal flicker floor (percent alpha)
	glitchStaticDropPct   = 35                     // Static: the hard dropout's alpha
	glitchStaticDropMod   = 24                     // Static: ~1 bucket in this many dips hard
	glitchTornBands       = int32(8)               // Torn: horizontal band count
	glitchTornPeriod      = 900 * time.Millisecond // Torn: a tear every period
	glitchTornWindow      = 140 * time.Millisecond // …lasting this long
	glitchTornBucket      = 45 * time.Millisecond  // …re-rolling the band offsets this often
	glitchTornOffMul      = 3                      // Torn: max band shove = split × this
	glitchTornBandSalt    = 0x9E3779B9             // decorrelates per-band hashes within a bucket
)

// glitchParamsMode: Heavy widens and jolts harder, Echo only widens, Torn and Static keep the classic split.
func glitchParamsMode(mode uint8, clock time.Duration, vpH int32) (split, jolt int32) {
	split, jolt = glitchParams(clock, vpH)
	switch mode {
	case courtroom.GlitchHeavy:
		split *= glitchHeavySplitMul
		jolt = 0
		if clock%glitchHeavyJoltPeriod < glitchHeavyJoltWindow {
			jolt = split * glitchHeavyJoltMul
			if int64(clock/glitchHeavyJoltPeriod)%2 == 0 {
				jolt = -jolt
			}
		}
	case courtroom.GlitchEcho:
		split *= glitchEchoSplitMul
	}
	return split, jolt
}

// glitchFlickerPct is Static's alpha percentage for this time bucket, with an occasional hard dropout.
func glitchFlickerPct(clock time.Duration) int {
	h := glitchHash(uint32(clock / glitchStaticBucket))
	if h%glitchStaticDropMod == 0 {
		return glitchStaticDropPct
	}
	const flickerBitsShift = 8 // the low byte already chose the dropout
	return glitchStaticFloorPct + int(h>>flickerBitsShift)%(percentMax-glitchStaticFloorPct)
}

// glitchHash is a stateless integer scrambler (xorshift-multiply) for the glitch modes' offsets.
func glitchHash(n uint32) uint32 {
	// lowbias32 (Chris Wellons' hash-prospector): its published shifts and multipliers.
	const (
		shiftA = 16
		mulA   = 0x7feb352d
		shiftB = 15
		mulB   = 0x846ca68b
		shiftC = 16
	)
	n ^= n >> shiftA
	n *= mulA
	n ^= n >> shiftB
	n *= mulB
	n ^= n >> shiftC
	return n
}

// drawSilhouette blits the silhouette at each dir offset, restoring mods as the page is shared.
func (v *Viewport) drawSilhouette(ren *sdl.Renderer, tex *sdl.Texture, angle float64, flip sdl.RendererFlip, dirs [][2]int32, width int32, r, g, b, a uint8) {
	_ = tex.SetColorMod(r, g, b)
	_ = tex.SetAlphaMod(a)
	for _, d := range dirs {
		v.fxRect = v.dstRect
		v.fxRect.X += d[0] * width
		v.fxRect.Y += d[1] * width
		_ = ren.CopyEx(tex, nil, &v.fxRect, angle, nil, flip)
	}
	_ = tex.SetColorMod(channelMax, channelMax, channelMax)
	_ = tex.SetAlphaMod(channelMax)
}

func clampFrame(frame, count int) int {
	if frame >= count {
		return count - 1
	}
	if frame < 0 {
		return 0
	}
	return frame
}

// spotlightMinBrightness keeps a dimmed layer "in shadow" rather than a black silhouette at the max slider.
const spotlightMinBrightness = 15

// spotlightBrightness maps SpotlightLevel [0,100], higher being darker, to a brightness percent floored above black.
func spotlightBrightness(level int) int {
	b := percentMax - level
	if b < spotlightMinBrightness {
		return spotlightMinBrightness
	}
	if b > percentMax {
		return percentMax
	}
	return b
}

// cycleForSpeed maps Speed [1,100] to a hue period, floored so a hand-edited pref can't divide by zero.
func cycleForSpeed(speed int) time.Duration {
	speed = value.Clamp(speed, 1, percentMax)
	span := maxRainbowCycle - minRainbowCycle
	cyc := maxRainbowCycle - time.Duration(int64(span)*int64(speed-1)/(percentMax-1))
	if cyc < minRainbowCycle {
		cyc = minRainbowCycle
	}
	return cyc
}

// styleHueVividness is the saturation for a transmitted hue cycle; the wire style has no vividness field.
const styleHueVividness = 70

// scaleChannel multiplies a channel by pct/100, clamped to 255.
func scaleChannel(c uint8, pct int) uint8 {
	v := int(c) * pct / percentMax
	if v > channelMax {
		v = 255
	}
	return uint8(v)
}

// floorForVividness maps Vividness [0,100] to the colour-mod floor: higher vividness, lower floor.
func floorForVividness(v int) int {
	v = value.Clamp(v, 0, percentMax)
	return spriteFloorMax * (percentMax - v) / percentMax
}

// Wobble and Spin tuning: slow and small so they read as playful, not nauseating.
const (
	wobblePeriod     = 1700 * time.Millisecond // one full sway cycle
	wobbleAmpDivisor = 40                      // sway amplitude = vp.W / divisor
	spinPeriod       = 4 * time.Second         // one full rotation
)

// Shout-punch tuning: short so it reads as impact, with a small peak so the art barely overspills the stage.
const (
	shoutPunchDuration = 240 * time.Millisecond // how long the pop takes to settle
	shoutPunchPeak     = 0.06                   // peak extra scale (+6%) at the instant of the shout
)

// punchScale is the extra-scale fraction at the given remaining time: instant attack, frac² decay.
func punchScale(left time.Duration) float64 {
	if left <= 0 {
		return 0
	}
	frac := float64(left) / float64(shoutPunchDuration)
	if frac > 1 {
		frac = 1
	}
	return shoutPunchPeak * frac * frac
}

func oscFrac(clock, period time.Duration) float64 {
	if period <= 0 {
		return 0
	}
	return float64(clock%period) / float64(period)
}

// wobbleOffset is a Lissajous sway scaled to the viewport.
func wobbleOffset(clock time.Duration, vp sdl.Rect) (dx, dy int32) {
	amp := float64(vp.W) / wobbleAmpDivisor
	dx = int32(amp * math.Sin(2*math.Pi*oscFrac(clock, wobblePeriod)))
	dy = int32(amp * 0.6 * math.Sin(2*math.Pi*oscFrac(clock, wobblePeriod*3/2)))
	return dx, dy
}

func spinDegrees(clock time.Duration) float64 {
	return oscFrac(clock, spinPeriod) * degreesPerTurn
}

// Sprite-motion tuning: amplitude is a fraction of viewport width, clamped so a sprite can't leave the stage.
const (
	motionAmpDivisor    = 7.0                     // motion amplitude = vp.W / divisor
	motionPeriod        = 3500 * time.Millisecond // one full loop
	motionShakeSpeedup  = 6                       // the shake path loops this many times faster
	motionShakeYFreqMul = 1.7                     // y jitters at an off ratio of x
	spiralRadiusMin     = 0.4                     // the spiral orbit's radius pulses from here to 1
)

// motionOffset returns the offset for a transmitted Motion path; the caller skips MotionNone.
func motionOffset(motion uint8, clock time.Duration, vp sdl.Rect) (dx, dy int32) {
	amp := float64(vp.W) / motionAmpDivisor
	t := 2 * math.Pi * oscFrac(clock, motionPeriod)
	switch motion {
	case courtroom.MotionOrbit:
		dx = int32(amp * math.Cos(t))
		dy = int32(amp * 0.55 * math.Sin(t)) // a flattened ellipse reads as "around the box"
	case courtroom.MotionBounce:
		dy = int32(-amp * 0.8 * math.Abs(math.Sin(t))) // bob UP from the baseline
	case courtroom.MotionSway:
		dx = int32(amp * math.Sin(t))
	case courtroom.MotionDrift:
		dx = int32(amp * math.Sin(t)) // a slow figure-8 roam
		dy = int32(amp * 0.5 * math.Sin(2*t))
	case courtroom.MotionShake:
		ft := 2 * math.Pi * oscFrac(clock, motionPeriod/motionShakeSpeedup) // fast jitter, small amplitude
		dx = int32(amp * 0.25 * math.Sin(ft))
		dy = int32(amp * 0.25 * math.Sin(ft*motionShakeYFreqMul)) // not a clean diagonal
	case courtroom.MotionSpiral:
		rad := spiralRadiusMin + (1-spiralRadiusMin)*math.Abs(math.Sin(t/2)) // orbit whose radius pulses in and out
		dx = int32(amp * rad * math.Cos(t))
		dy = int32(amp * 0.55 * rad * math.Sin(t))
	case courtroom.MotionPendulum:
		dx = int32(amp * math.Sin(t))                   // swing side to side…
		dy = int32(-amp * 0.25 * math.Abs(math.Cos(t))) // …lifting slightly at each end (an arc)
	}
	return dx, dy
}

// pathOffset returns the offset along a custom path; each waypoint is a packed 4-bit X/Y centred at 8,8.
func pathOffset(path []byte, pathLen uint8, clock time.Duration, vp sdl.Rect) (dx, dy int32) {
	n := int(pathLen)
	if n < 2 || n > len(path) {
		return 0, 0
	}
	amp := float64(vp.W) / motionAmpDivisor
	seg := oscFrac(clock, motionPeriod) * float64(n) // 0..n across the whole loop
	i := int(seg)
	if i >= n {
		i = n - 1
	}
	frac := seg - float64(i)
	j := (i + 1) % n // wrap to the first point so the path loops
	x0, y0 := pathPointPx(path[i], amp)
	x1, y1 := pathPointPx(path[j], amp)
	return int32(x0 + (x1-x0)*frac), int32(y0 + (y1-y0)*frac)
}

// pathPointPx maps a packed 4-bit X/Y waypoint to a pixel offset scaled to amp.
func pathPointPx(p byte, amp float64) (x, y float64) {
	const (
		nibbleBits = 4
		nibbleMask = 0x0F
		centre     = 8 // a waypoint nibble's zero offset
	)
	return float64(int(p>>nibbleBits)-centre) / centre * amp, float64(int(p&nibbleMask)-centre) / centre * amp
}

// Idle-breathing tuning: slow and small.
const (
	breathPeriodMin = 1500 * time.Millisecond // fastest breath (speed = 100)
	breathPeriodMax = 5000 * time.Millisecond // slowest breath (speed = 1)
	breathBobDiv    = 50                      // bob amplitude at amp=100 = vp.H / div (px)
	breathScaleMax  = 5                       // scale-pulse adds up to +5% at amp=100
)

// breathPeriod maps BreathSpeed [1,100] to a period; higher is faster.
func breathPeriod(speed int) time.Duration {
	speed = value.Clamp(speed, 1, percentMax)
	span := breathPeriodMax - breathPeriodMin
	return breathPeriodMax - time.Duration(int64(span)*int64(speed-1)/(percentMax-1))
}

// breathTransform returns the idle-breathing bob and a scale pulse that only expands; the caller checks Bob and Scale.
func breathTransform(clock time.Duration, vp sdl.Rect, ampPct, speedPct int) (bobY int32, scaleAddPct int) {
	ampPct = value.Clamp(ampPct, 1, percentMax)
	s := math.Sin(2 * math.Pi * oscFrac(clock, breathPeriod(speedPct)))
	bobY = int32(float64(vp.H) * float64(ampPct) / 100 / breathBobDiv * s)
	scaleAddPct = int(float64(breathScaleMax) * float64(ampPct) / 100 * (1 + s) / 2)
	return bobY, scaleAddPct
}

// charHueOffset hashes a base name (FNV-1a) to a stable hue offset in [0,cycle) for PerCharHue.
func charHueOffset(base string, cycle time.Duration) time.Duration {
	if cycle <= 0 {
		return 0
	}
	const (
		fnvOffset = 2166136261
		fnvPrime  = 16777619
	)
	h := uint32(fnvOffset)
	for i := 0; i < len(base); i++ {
		h ^= uint32(base[i])
		h *= fnvPrime
	}
	return time.Duration(uint64(h) % uint64(cycle))
}

// rainbowMod maps the hue clock to a colour mod in integer math, lifted by floor so art isn't silhouetted.
func rainbowMod(phase, cycle time.Duration, floor int) (r, g, b uint8) {
	const segments = 6  // six edges of the RGB cube
	const segSpan = 256 // hue ticks per edge
	if cycle <= 0 {     // defensive: never divide by zero (Update floors it > 0)
		cycle = minRainbowCycle
	}
	if phase < 0 {
		phase = 0
	}
	hue := int64(phase) * (segments * segSpan) / int64(cycle)
	seg := hue / segSpan % segments
	t := hue % segSpan // 0..255 within the current edge
	var rr, gg, bb int64
	// The six edges of the hue ring, red round to magenta.
	const (
		segRed = iota
		segYellow
		segGreen
		segCyan
		segBlue
	)
	switch seg {
	case segRed:
		rr, gg, bb = channelMax, t, 0
	case segYellow:
		rr, gg, bb = channelMax-t, channelMax, 0
	case segGreen:
		rr, gg, bb = 0, channelMax, t
	case segCyan:
		rr, gg, bb = 0, channelMax-t, channelMax
	case segBlue:
		rr, gg, bb = t, 0, channelMax
	default: // magenta
		rr, gg, bb = channelMax, 0, channelMax-t
	}
	return liftChannel(rr, floor), liftChannel(gg, floor), liftChannel(bb, floor)
}

// liftChannel maps a hue channel into [floor,255].
func liftChannel(c int64, floor int) uint8 {
	return uint8(int64(floor) + c*(channelMax-int64(floor))/channelMax)
}
