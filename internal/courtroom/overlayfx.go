package courtroom

// AO2 screen-effect overlays (do_effect, courtroom.cpp:3182-3273; start_chat_ticking, courtroom.cpp:4154-4177).
// TextEffect* in effectwire.go is a different feature, hence the Overlay prefix.

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// OverlayFXFileCap is how many effects.ini files merge: internal/theme walks local ones, internal/ui adds the origin's.
const OverlayFXFileCap = theme.OverlayFXFileCap

// Overlay layer, mirroring theme.OverlayLayer as a uint8 so internal/render need not import internal/theme.
const (
	// OverlayLayerChat is do_effect's else branch; zero so absent, "under" or "beho" land here (courtroom.cpp:3233).
	OverlayLayerChat uint8 = iota
	// OverlayLayerBehind stacks under the speaker sprite.
	OverlayLayerBehind
	// OverlayLayerCharacter stacks over the characters, under the desk.
	OverlayLayerCharacter
	// OverlayLayerOver raises the overlay inside the viewport.
	OverlayLayerOver
)

// overlayLayerFromTheme uses a switch, not a cast, so a layer added on either side can't change meaning.
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

// SceneOverlay is the active overlay, precomputed once per trigger.
type SceneOverlay struct {
	// Base is the T1 key or URL ("" = no overlay); a cold base draws nothing until its page lands.
	Base string
	// Layer is the overlay's place in the stack (courtroom.cpp:3216-3237).
	Layer uint8
	// Loop wraps forever (cull is then inert, animationlayer.cpp:660-666); Cull hides a finished play-once clip.
	Loop, Cull, Stretch, Flip bool
	// OffsetX/Y are percent of the viewport, copied when the effect declares respect_offset (courtroom.cpp:3251-3267).
	OffsetX, OffsetY int
	// MaxFrameDelay clamps each frame's delay (animationlayer.cpp:281-286); 0 = no clamp.
	MaxFrameDelay time.Duration
	// Scaling is resolved against the user's mode at the draw site (ResolveScalingMode).
	Scaling ScalingMode
	// Gen bumps on every trigger so a repeated effect restarts: animState.reset only fires on a base change.
	Gen uint32
}

func (o SceneOverlay) Active() bool { return o.Base != "" }

// OverlayResolved is the merged properties and T1 key for an effect name and folder.
type OverlayResolved struct {
	FX   theme.OverlayFX
	Base string
	// Pending means ask again: answering now would probe for art the theme may hold, or arm with zero properties.
	Pending bool
}

// overlayPendingFX is the one deferred overlay request; a new message supersedes it and every clear cancels it.
type overlayPendingFX struct {
	name, folder string
	flip         bool
	offX, offY   int
	left         time.Duration // TTL remaining; <= 0 = nothing deferred
	next         time.Duration // until the next re-ask
}

const (
	// overlayPendingTTL outlives the 8s manifest fetch, then settles as a miss, so nothing waits forever (hard rule 4).
	overlayPendingTTL = 10 * time.Second
	// overlayPendingRetryInterval is not per-frame: the resolver memoises one folder, so per-frame asks would allocate.
	overlayPendingRetryInterval = 200 * time.Millisecond
)

// overlayMaxDurationUnit: AO2 feeds max_duration to a QTimer as milliseconds (animationlayer.cpp:281-286).
const overlayMaxDurationUnit = time.Millisecond

// overlayNameIsNone: "" is canon's early return, which does not clear (courtroom.cpp:3184-3187); "-" is webAO's
// BAD_EFFECTS (handleICSpeaking.ts:28). AO2 translates "None" (courtroom.cpp:5600); other languages clear as unknown.
func overlayNameIsNone(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "-", "none":
		return true
	}
	return false
}

// OverlaySoundPlayable filters sound sentinels; canon tests non-empty (courtroom.cpp:3196). Shared with the picker.
func OverlaySoundPlayable(sound string) bool {
	switch strings.ToLower(strings.TrimSpace(sound)) {
	case "", "0", "-", "none":
		return false
	}
	return true
}

// parseEffectsField splits EFFECTS per courtroom.cpp:4154-4172; parts[len-1] isn't the sound, as padding shifts it.
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

// BuildEffectsField assembles "<name>|<folder>|<sound>" (courtroom.cpp:2296-2308); the None row sends "".
func BuildEffectsField(name, folder, sound string) string {
	if name == "" {
		return ""
	}
	return name + "|" + folder + "|" + sound
}

// clearOverlay is AO2's stopPlayback and hide; Gen stays so a retrigger restarts, and a deferred request is cancelled.
func (c *Courtroom) clearOverlay() {
	gen := c.Scene.Overlay.Gen
	c.Scene.Overlay = SceneOverlay{Gen: gen}
	c.overlayPending = overlayPendingFX{}
}

// fireOverlay is do_effect, returning whether an overlay is on stage. Canon clears at display_character
// (courtroom.cpp:2755-2761), set_background (:1427-1435) and an unresolvable name (:3188-3194), never at message end.
func (c *Courtroom) fireOverlay(name, folder string, flip bool, offX, offY int) bool {
	// Empty name returns without clearing (courtroom.cpp:3184-3187), so a looping overlay survives.
	if name == "" {
		return c.Scene.Overlay.Active()
	}
	if overlayNameIsNone(name) || c.Assets == nil {
		c.clearOverlay()
		return false
	}
	// Tested before the resolve, unlike canon (courtroom.cpp:3188, :3202): OverlayFor streams, so hidden art would pay.
	if !c.effectsVisible() {
		// Canon returns without clearing (courtroom.cpp:3202-3205), leaving the loop reduce-motion exists to stop.
		c.clearOverlay()
		return false
	}
	res, ok := c.Assets.OverlayFor(name, folder)
	if ok && res.Pending {
		// Defer rather than arm: arming would spend the network probe and stage default properties.
		if c.overlayPending.left <= 0 || c.overlayPending.name != name || c.overlayPending.folder != folder {
			c.overlayPending = overlayPendingFX{
				name: name, folder: folder, flip: flip, offX: offX, offY: offY,
				left: overlayPendingTTL, next: overlayPendingRetryInterval,
			}
		}
		// Report on stage: tickOverlayPending runs the legacy fallback if this settles to a miss.
		return true
	}
	if !ok || res.Base == "" {
		// An unknown effect name is an explicit clear in canon, not a no-op.
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
		// respect_flip is ANDed with the message's FLIP field (:3208).
		Flip:          fx.RespectFlip && flip,
		Scaling:       ParseScalingMode(fx.Scaling),
		MaxFrameDelay: time.Duration(fx.MaxFrameMS) * overlayMaxDurationUnit,
		Gen:           c.Scene.Overlay.Gen + 1,
	}
	if fx.RespectOffset {
		// SpriteLayer.OffsetX/Y is already percent of viewport, what :3264-3266 computes.
		ov.OffsetX, ov.OffsetY = offX, offY
	}
	c.Scene.Overlay = ov
	c.overlayPending = overlayPendingFX{} // settled: nothing left to re-ask for
	return true
}

// applyEffectField is the visual half of EFFECTS, so a deferred retry settles a message as the first attempt would.
func (c *Courtroom) applyEffectField(name, folder string, flip bool, offX, offY int) {
	if c.fireOverlay(name, folder, flip, offX, offY) {
		return
	}
	if !c.effectsVisible() {
		return
	}
	c.fireLegacyEffectFallback(name)
}

// fireLegacyEffectFallback handles the two effect names we realize ourselves for installs with no effects.ini.
func (c *Courtroom) fireLegacyEffectFallback(name string) {
	switch strings.ToLower(name) {
	case "screenshake":
		c.Scene.ShakeLeft = ScreenshakeDuration
	case "flash", "realization", "realizationflash":
		c.Scene.FlashLeft = RealizationFlashDuration
	}
}

// tickOverlayPending re-asks for a deferred overlay until its roster lands or the TTL ends.
func (c *Courtroom) tickOverlayPending(dt time.Duration) {
	if c.overlayPending.left <= 0 {
		return
	}
	c.overlayPending.left -= dt
	if c.overlayPending.left <= 0 {
		// Settle as an unresolvable name does, so a resolve that never returns can't swallow a "realization".
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
	// fireOverlay re-arms the same request preserving this countdown, so the retry can't outlive its TTL.
	c.applyEffectField(p.name, p.folder, p.flip, p.offX, p.offY)
}

// OverlayRealizationEffect is the name canon special-cases (text_file_functions.cpp:889-892); also used by internal/ui.
const OverlayRealizationEffect = "realization"

// realizationSFXFor is get_custom_realization (text_file_functions.cpp:896-903): char.ini, else the theme's.
func (c *Courtroom) realizationSFXFor(char string) string {
	if c.Chars != nil && char != "" {
		if sfx := strings.TrimSpace(c.Chars.CustomRealizationFor(char)); sfx != "" {
			return sfx
		}
	}
	return c.RealizationSFX
}

// OverlayEffectsFolderFor defaults to the sender's char.ini effects (text_file_functions.cpp:836-839); already cached.
func (c *Courtroom) OverlayEffectsFolderFor(char string) string {
	if c.Assets == nil || char == "" {
		return ""
	}
	return strings.TrimSpace(c.Assets.EffectsFolderFor(char))
}
