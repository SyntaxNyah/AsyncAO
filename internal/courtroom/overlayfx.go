package courtroom

// AO2 screen-effect OVERLAYS — the wire parse, the scene model and the trigger /
// clear lifecycle. Canon is ../AO2-Client (do_effect, courtroom.cpp:3182-3273 and
// start_chat_ticking, :4154-4177); it wins every conflict.
//
// NAMING FENCE: this package already owns TextEffect* / EffectSpan / EffectMark
// (effectwire.go — animated chat TEXT, a different feature entirely), so every
// identifier here is prefixed Overlay… / overlay….

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// OverlayFXFileCap is theme.OverlayFXFileCap under the name the spec gives it —
// how many effects.ini files the PROPERTY pass merges. One constant, two names,
// because the merge is split across the two packages: internal/theme walks the
// LOCAL ladder and internal/ui appends the one ORIGIN file.
const OverlayFXFileCap = theme.OverlayFXFileCap

// Overlay layer, mirroring theme.OverlayLayer. Scene is plain data consumed by
// internal/render, which has no business importing internal/theme just to read a
// switch value, so the layer crosses as a uint8 — with the same ordering, and
// overlayLayerFromTheme as the one conversion point.
const (
	// OverlayLayerChat is do_effect's ELSE branch: the overlay is reparented OUT
	// of the viewport and stacked under the shout bubble. Zero value on purpose —
	// an absent, empty, "under" or "beho" layer lands here (courtroom.cpp:3233).
	OverlayLayerChat uint8 = iota
	// OverlayLayerBehind stacks under the speaker sprite.
	OverlayLayerBehind
	// OverlayLayerCharacter stacks over the characters, under the desk.
	OverlayLayerCharacter
	// OverlayLayerOver raises the overlay inside the viewport.
	OverlayLayerOver
)

// overlayLayerFromTheme is the ONE place theme.OverlayLayer becomes the wire-free
// uint8 the renderer reads. A compile-time-checked switch rather than a cast, so
// a layer added on either side cannot silently mean something else here.
func overlayLayerFromTheme(l theme.OverlayLayer) uint8 {
	switch l {
	case theme.OverlayLayerBehind:
		return OverlayLayerBehind
	case theme.OverlayLayerCharacter:
		return OverlayLayerCharacter
	case theme.OverlayLayerOver:
		return OverlayLayerOver
	default:
		return OverlayLayerChat
	}
}

// SceneOverlay is the active screen-effect overlay: everything the renderer needs
// to draw it, precomputed once per trigger so the draw is pointer math.
type SceneOverlay struct {
	// Base is the T1 key / URL the art lives under ("" = no overlay). A cold base
	// simply draws nothing until the page lands — pixel-identical to canon's
	// clear, and the same shape as a cold shout bubble.
	Base string
	// Layer is where in the stack the overlay blits (courtroom.cpp:3216-3237).
	Layer uint8
	// Loop wraps forever (cull is then inert, animationlayer.cpp:660-666);
	// Cull hides a finished PLAY-ONCE clip instead of holding its last frame;
	// Stretch fills the viewport instead of fitting its height; Flip mirrors it.
	Loop, Cull, Stretch, Flip bool
	// OffsetX/Y are PERCENT of the viewport, copied from the speaker's own
	// self-offset when the effect declares respect_offset (courtroom.cpp:3251-3267).
	// Zero otherwise — the layer is never resized, it always covers the viewport.
	OffsetX, OffsetY int
	// MaxFrameDelay clamps EACH FRAME's delay (animationlayer.cpp:281-286).
	// 0 = no clamp. Named for what it does, unlike the ini key it comes from.
	MaxFrameDelay time.Duration
	// Scaling is the effect's own texture filter request, resolved against the
	// user's global mode at the draw site (ResolveScalingMode).
	Scaling ScalingMode
	// Gen bumps on EVERY trigger so the renderer restarts a clip even when the
	// same effect fires twice in a row: animState.reset only fires on a base
	// change, so a repeat of the identical base would otherwise keep playing
	// (or stay frozen on its held last frame).
	Gen uint32
}

// Active reports whether anything is on screen.
func (o SceneOverlay) Active() bool { return o.Base != "" }

// OverlayResolved is what the App hands back for an effect name + folder: the
// merged properties and the T1 key / URL its art will live under.
type OverlayResolved struct {
	FX   theme.OverlayFX
	Base string
	// Pending says "ask again": the folder's roster — the LOCAL ladder's resolved
	// art paths AND the merged properties — has not landed yet, so the resolver
	// cannot answer without either probing the network for art the theme may
	// already hold on disk (spec §1.f: rung 8 is the ONLY network rung, reached
	// only when the whole local ladder misses) or arming with a zero-valued
	// OverlayFX that nothing would ever correct. fireOverlay defers instead; the
	// resolver's own async work is already in flight.
	Pending bool
}

// overlayPendingFX is the ONE deferred overlay arm: the request a message made
// while its folder's roster was still resolving. A single slot, exactly like the
// single overlay on stage — a new message supersedes it, and every clear site
// cancels it.
type overlayPendingFX struct {
	name, folder string
	flip         bool
	offX, offY   int
	left         time.Duration // TTL remaining; <= 0 = nothing deferred
	next         time.Duration // until the next re-ask
}

const (
	// overlayPendingTTL bounds the deferred arm. It outlives the resolver's own
	// manifest fetch (internal/ui overlayFetchTimeout = 8s) so a slow but
	// successful resolve still arms; past it the request settles as a miss, so a
	// resolve whose result was dropped on channel overflow can never leave an
	// effect waiting forever (hard rule 4).
	overlayPendingTTL = 10 * time.Second
	// overlayPendingRetryInterval paces the re-ask. Deliberately NOT every frame:
	// the resolver keys its roster cache by "origin|folder", a concatenation it can
	// memoise for only ONE folder at a time, and the picker asks for OUR OWN folder
	// every frame — so a per-frame re-ask for a different folder would allocate on
	// the zero-alloc courtroom frame.
	overlayPendingRetryInterval = 200 * time.Millisecond
)

// overlayMaxDurationUnit is the unit of the effects.ini max_duration value.
// AO2 feeds it straight to a QTimer as milliseconds (animationlayer.cpp:281-286).
const overlayMaxDurationUnit = time.Millisecond

// overlayNameSentinels are wire effect NAMES that mean "no overlay". "" is the
// canon early-return (courtroom.cpp:3184-3187, which does NOT clear); "-" is
// webAO's BAD_EFFECTS marker (handleICSpeaking.ts:28) and "none" is the ASCII
// spelling of the row AO2 prepends to its dropdown. AO2 sends the TRANSLATED word
// for that row (courtroom.cpp:5600), so this list can never be complete — the
// unresolvable path covers every other language, and does exactly what canon does
// with an unknown name: clear.
func overlayNameIsNone(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "-", "none":
		return true
	}
	return false
}

// OverlaySoundPlayable filters the effect SOUND sentinels.
//
// Canon only tests `fx_sound != ""` (courtroom.cpp:3196). The extras are earned:
// KFO's stock effects.ini writes `sound = 0` as its "no sound" idiom, which AO2
// gets away with because the local file 0.* simply never exists — a streaming
// client would burn a real probe (and a 404 cache entry) per effect on it, which
// is the zero-fallback rule's whole subject. "-" is webAO's BAD_EFFECTS marker
// and "none" the ASCII "None" row; both are the same class of never-a-file.
//
// EXPORTED because the message path is not the only place an effect sound is
// played: the picker's right-click PREVIEW (internal/ui/effectspicker.go) plays
// the same value, and a second copy of this list there would drift — reintroducing
// the exact probe this filter exists to stop.
func OverlaySoundPlayable(sound string) bool {
	switch strings.ToLower(strings.TrimSpace(sound)) {
	case "", "0", "-", "none":
		return false
	}
	return true
}

// parseEffectsField splits the 2.8 EFFECTS wire field, verbatim per
// start_chat_ticking (courtroom.cpp:4154-4172):
//
//	1 part   "name"                → folder falls back to the SENDER's char.ini
//	2 parts  "name|sound"          → legacy; folder still falls back
//	3 parts  "name|folder|sound"   → AO2 ≥ 2.8
//	≥4 parts: only [0], [1] and [2] are read — the tail is ignored
//
// The ≥4 case is why this cannot take parts[len-1] as the sound: a sender that
// pads the field would have its FOLDER read as the sound and its sound dropped.
func parseEffectsField(raw string) (fx, folder, sound string) {
	parts := strings.Split(raw, "|")
	fx = parts[0]
	if len(parts) > 1 {
		sound = parts[1]
	}
	if len(parts) > 2 {
		folder = parts[1]
		sound = parts[2]
	}
	return fx, folder, sound
}

// BuildEffectsField assembles the OUTGOING field: "<name>|<folder>|<sound>"
// (courtroom.cpp:2296-2308). The "None" row sends "" rather than canon's
// translated tr("None") — every receiver clears identically on both, and ""
// cannot be mistaken for a real effect name the way a localised word can.
func BuildEffectsField(name, folder, sound string) string {
	if name == "" {
		return ""
	}
	return name + "|" + folder + "|" + sound
}

// clearOverlay is AO2's `ui_vp_effect->stopPlayback(); hide()`. Gen is preserved
// (it only ever counts UP) so a later retrigger of the same base still restarts.
// A clear also cancels a deferred arm: "nothing is on stage" has to mean nothing
// is on its way either, or a superseded request would pop up a message later.
func (c *Courtroom) clearOverlay() {
	gen := c.Scene.Overlay.Gen
	c.Scene.Overlay = SceneOverlay{Gen: gen}
	c.overlayPending = overlayPendingFX{}
}

// fireOverlay is do_effect. It returns whether an overlay is now on stage, so the
// caller can decide about the legacy flash/shake fallback.
//
// The three CLEAR sites in canon are display_character (courtroom.cpp:2755-2761 —
// the next IC message carrying an emote, i.e. our begin), set_background with
// display=true (:1427-1435 — wipeStage), and the unresolvable branch below
// (:3188-3194). There is deliberately NO clear at message end.
func (c *Courtroom) fireOverlay(name, folder string, flip bool, offX, offY int) bool {
	// fx == "" → return immediately, NO clear (courtroom.cpp:3184-3187). The
	// distinction matters: an empty field must not kill a looping overlay the
	// previous message started.
	if name == "" {
		return c.Scene.Overlay.Active()
	}
	if overlayNameIsNone(name) || c.OverlayFor == nil {
		c.clearOverlay()
		return false
	}
	// THE VISUAL GATE COMES FIRST — ahead of the resolve, where canon has it after
	// (:3188 resolves, :3202 tests effectsEnabled). AO2 can afford that order
	// because get_effect is a local stat; c.OverlayFor is a STREAMING resolve that
	// fetches a misc manifest, reads+decodes a theme file into T1, or probes the
	// origin. With "Enable screen effects" off (or Reduce motion on) none of that
	// art may ever be drawn, so paying network, decode and VRAM for it is pure
	// waste — the same reason remoteChatSkinFor gates on its pref before touching
	// the misc art (internal/ui/charmeta.go:127-134).
	//
	// Observably identical to the canon order: the effect SOUND is played by the
	// caller, outside this function (fireMessageEffects), and the clear below is
	// exactly what the gated path leaves behind either way.
	if !c.effectsVisible() {
		// ScreenEffects off / ReduceMotion on: no overlay may be on screen at all.
		// Canon returns here WITHOUT clearing (:3202-3205), which leaves the
		// previous effect playing — the exact permanent wash reduce-motion exists
		// to kill. Clearing also makes "ReduceMotion forbids a looping overlay"
		// fall out of the one gate instead of needing a second knob.
		c.clearOverlay()
		return false
	}
	res, ok := c.OverlayFor(name, folder)
	if ok && res.Pending {
		// The folder's roster is still resolving, so the LOCAL ladder cannot be
		// walked yet. DEFER rather than arm: arming now would spend the one network
		// rung on art the theme may already have on disk and would stage the effect
		// with default properties (chat layer, no loop/stretch) that nothing would
		// correct when the roster lands. The stage is already clear here (clear site
		// 1 ran when this message staged its emote), so holding shows nothing —
		// exactly like a cold base whose page has not landed.
		if c.overlayPending.left <= 0 || c.overlayPending.name != name || c.overlayPending.folder != folder {
			c.overlayPending = overlayPendingFX{
				name: name, folder: folder, flip: flip, offX: offX, offY: offY,
				left: overlayPendingTTL, next: overlayPendingRetryInterval,
			}
		}
		// Report "on stage": the legacy flash/shake fallback is deferred with the
		// rest of the decision, and tickOverlayPending fires it if this settles to
		// a miss.
		return true
	}
	if !ok || res.Base == "" {
		// An unknown effect name is an explicit CLEAR in canon, not a no-op.
		c.clearOverlay()
		return false
	}
	fx := res.FX
	ov := SceneOverlay{
		Base:    res.Base,
		Layer:   overlayLayerFromTheme(fx.Layer),
		Loop:    fx.Loop,
		Cull:    fx.Cull,
		Stretch: fx.Stretch,
		// respect_flip is ANDed with the message's own FLIP field (:3208).
		Flip:          fx.RespectFlip && flip,
		Scaling:       ParseScalingMode(fx.Scaling),
		MaxFrameDelay: time.Duration(fx.MaxFrameMS) * overlayMaxDurationUnit,
		Gen:           c.Scene.Overlay.Gen + 1,
	}
	if fx.RespectOffset {
		// SpriteLayer.OffsetX/Y is already "percent of viewport", which is exactly
		// what :3264-3266 computes (viewport.width() * self_offset / 100), so this
		// is a straight copy rather than an arithmetic port.
		ov.OffsetX, ov.OffsetY = offX, offY
	}
	c.Scene.Overlay = ov
	c.overlayPending = overlayPendingFX{} // settled: nothing left to re-ask for
	return true
}

// applyEffectField is the whole visual half of the 2.8 EFFECTS field: the overlay
// when it resolves, the legacy flash/shake name mapping when it does not. Split
// out of fireMessageEffects so the DEFERRED retry below settles a message exactly
// the way its first attempt would have — a bare install with no effects.ini still
// reacts to "realization"/"screenshake" even when the decision had to wait for a
// roster. The effect SOUND is not here: it plays regardless of this outcome.
func (c *Courtroom) applyEffectField(name, folder string, flip bool, offX, offY int) {
	if c.fireOverlay(name, folder, flip, offX, offY) {
		return
	}
	if !c.effectsVisible() {
		return
	}
	c.fireLegacyEffectFallback(name)
}

// fireLegacyEffectFallback is the pre-overlay behaviour: the two effect NAMES
// AsyncAO has always realized itself, for installs with no effects.ini anywhere.
func (c *Courtroom) fireLegacyEffectFallback(name string) {
	switch strings.ToLower(name) {
	case "screenshake":
		c.Scene.ShakeLeft = ScreenshakeDuration
	case "flash", "realization", "realizationflash":
		c.Scene.FlashLeft = RealizationFlashDuration
	}
}

// tickOverlayPending re-asks for a deferred overlay until its roster lands or the
// TTL runs out. Free when nothing is deferred: one comparison.
func (c *Courtroom) tickOverlayPending(dt time.Duration) {
	if c.overlayPending.left <= 0 {
		return
	}
	c.overlayPending.left -= dt
	if c.overlayPending.left <= 0 {
		// Gave up. Settle it exactly as an unresolvable name settles: clear, then
		// the legacy fallback, so a resolve that never came back cannot swallow a
		// "realization" for good.
		p := c.overlayPending
		c.clearOverlay() // also empties the pending slot
		if c.effectsVisible() {
			c.fireLegacyEffectFallback(p.name)
		}
		return
	}
	c.overlayPending.next -= dt
	if c.overlayPending.next > 0 {
		return
	}
	c.overlayPending.next = overlayPendingRetryInterval
	p := c.overlayPending
	// fireOverlay re-arms the SAME request while the roster is still in flight,
	// preserving this countdown, so the retry can never outlive its TTL.
	c.applyEffectField(p.name, p.folder, p.flip, p.offX, p.offY)
}

// OverlayRealizationEffect is the ONE effect name canon special-cases: for it,
// get_effect_property replaces the merged effects.ini `sound` wholesale with
// get_custom_realization (text_file_functions.cpp:889-892). Exported because the
// SEND side lives in internal/ui (it builds the outgoing field from the roster)
// and a second spelling there would drift.
const OverlayRealizationEffect = "realization"

// realizationSFXFor is AO2's get_custom_realization (text_file_functions.cpp:
// 896-903): the SPEAKER's own char.ini [Options] realization when it declares
// one, else the theme's courtroom_sounds.ini "realization". Canon applies that
// precedence in both places a realization sound is chosen — the legacy
// REALIZATION=1 receive path and the outgoing effects field — so it lives in one
// helper here rather than being re-derived at each.
func (c *Courtroom) realizationSFXFor(char string) string {
	if c.CustomRealizationFor != nil && char != "" {
		if sfx := strings.TrimSpace(c.CustomRealizationFor(char)); sfx != "" {
			return sfx
		}
	}
	return c.RealizationSFX
}

// OverlayEffectsFolderFor answers "which misc folder do this speaker's effects
// live in" for the 1- and 2-part wire shapes, which omit it: AO2 defaults
// p_folder to the SENDER's char.ini [Options] effects
// (text_file_functions.cpp:836-839). Costs no network work — charMeta already
// fetched and cached that char.ini for the blip set and chatbox skin.
func (c *Courtroom) OverlayEffectsFolderFor(char string) string {
	if c.EffectsFolderFor == nil || char == "" {
		return ""
	}
	return strings.TrimSpace(c.EffectsFolderFor(char))
}
