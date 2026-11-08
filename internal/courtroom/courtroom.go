package courtroom

import (
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// DefaultShoutDuration approximates the AO objection bubble; the render side may finish earlier.
	DefaultShoutDuration = 724 * time.Millisecond
	// DefaultPreanimTimeout bounds a preanim wait only while the asset decodes; NotifyPreanimDone or NotifyAssetMissing
	// cut it short, and NotifyPreanimStarted extends it to the real duration.
	DefaultPreanimTimeout = 2500 * time.Millisecond
	// preanimPlaybackSlack pads past a preanim's duration, so NotifyPreanimDone wins the race, not the fallback.
	preanimPlaybackSlack = 250 * time.Millisecond
	// DefaultTextStayTime holds a finished message before the queue advances (AO2 text_stay_time).
	DefaultTextStayTime = 200 * time.Millisecond
	// messageQueueCap bounds the IC message queue (hard rule 4).
	messageQueueCap = 64
	// DefaultQueueCap is what QueueCap seeds to and what a 0 pref restores.
	DefaultQueueCap = messageQueueCap
	// missingSpritesCap bounds the 404'd base set (hard rule 4); past it SpriteWaitTimeout bounds any unrecorded miss.
	// sequentialWaitLimit releases a strict-sequencing hold: a transient error or dropped warning never settles an asset.
	sequentialWaitLimit = 10 * time.Second
	missingSpritesCap   = 512
	// missingDesksCap bounds the 404'd desk set (hard rule 4); past it the render-side release covers the layer.
	missingDesksCap = 512
	// blipVolumeFull is the unattenuated blip scale, used with no BlipVolumeFor callback.
	blipVolumeFull = 100
	// emptyPreanim values AO uses for "no preanimation".
	emptyPreanimDash = "-"

	// catchUpDefaultThreshold is the floor: a message catches up once one waits behind it. Off for direct callers.
	catchUpDefaultThreshold = 1
	// catchUpLinger is zero so a backlog drains one per frame; the IC log keeps every message.
	catchUpLinger = 0 * time.Millisecond

	// RealizationFlashDuration approximates do_flash's one-shot animation on stock themes.
	RealizationFlashDuration = 250 * time.Millisecond
	// ScreenshakeDuration approximates do_screenshake's elastic UI wobble.
	ScreenshakeDuration = 350 * time.Millisecond

	// sfxDelayUnit is AO2's time_mod (courtroom.cpp:4052, courtroom.h:428), so a sound lands mid-preanim.
	sfxDelayUnit = 40 * time.Millisecond
)

// MessagePhase is the IC lifecycle: shout, preanim, talking, linger, idle (handle_chatmessage).
type MessagePhase int

const (
	PhaseIdle MessagePhase = iota
	PhaseShout
	PhasePreanim
	PhaseTalking
	PhaseLinger
	// PhaseSlide pans a panorama to the message's position after the shout, before the emote (do_transition).
	PhaseSlide
)

// SlideScene is a pan across a panorama from the last position to the message's, with the last speaker and pair held
// where they stood (courtroom.cpp:3007-3155); the stage eases from origin From to To over Total, and every character
// is paused, as AO2 pauses them.
type SlideScene struct {
	Active      bool
	From, To    int
	Left, Total time.Duration
	Previous    SpriteLayer
	// PreviousPair is the last message's pair, drawn when PreviousPaired, in front of Previous unless PreviousInFront.
	PreviousPair    SpriteLayer
	PreviousPaired  bool
	PreviousInFront bool
}

// SpriteLayer describes one character layer; bases are extension-less URL bases.
type SpriteLayer struct {
	// Name is the character folder, the key client-side position overrides attach to.
	Name        string
	IdleBase    string
	TalkBase    string
	PreanimBase string
	// Active selects which base renders right now.
	Active string
	// PlayOnce means this is the speaker's preanimation, nothing else: drawSprite exempts such layers from the
	// missing-sprite placeholder, so setting it elsewhere would suppress a wanted one.
	PlayOnce bool
	Flip     bool
	// OffsetX/Y are percent of viewport dimensions (-100..100).
	OffsetX, OffsetY int
	// Side is a group member's position, for their own bench; speaker and pair inherit scene.Position.
	Side    string
	Visible bool
	// Style is the transmitted look: the speaker's from this message, a partner's recalled by char id.
	Style SpriteStyle
	// Scaling is this layer's filter, per AO2's per-sprite model; ScalingAuto is settled by the geometry rule.
	Scaling ScalingMode
}

// Scene is the renderer's whole input: plain data, mutated only by Courtroom on the game thread.
type Scene struct {
	Position       string
	BackgroundBase string
	// BackgroundAlts are the background's other names in AO2's order, so a repair asks for the same names.
	BackgroundAlts []string
	DeskBase       string
	ShowDesk       bool
	// SceneryStretch and SceneryScaling are the background design.ini's stretch and scaling; SceneryPanorama says the
	// position sits on a panorama at x SceneryOrigin. They place the background and desk as set_scene does.
	SceneryStretch  bool
	SceneryScaling  ScalingMode
	SceneryPanorama bool
	SceneryOrigin   int
	Slide           SlideScene

	Speaker SpriteLayer
	Pair    SpriteLayer
	// Group holds the extra group-pair members, drawn behind speaker and pair in roster z-order.
	Group          []SpriteLayer
	PairActive     bool
	SpeakerInFront bool

	ShoutBase string // active shout bubble base ("" = none)
	// ShoutFallbackBase is the misc/default bubble for characters with no custom art, as AO2 falls back.
	ShoutFallbackBase string
	ShoutCustom       bool
	// SpeedlinesChain is the zoom overlay candidates, most specific first; the renderer draws the first resident one.
	SpeedlinesChain []string
	// SpeedlinesSide is the zoom side, "" only when not zooming; it maps to the bundled fallback.
	SpeedlinesSide string

	// MusicTrack is the playing track (raw MC text; "" = nothing, stopped or an area transfer).
	MusicTrack string

	// Chat box state.
	ShownameText string
	MessageText  string // full text (markup stripped); VisibleRunes gates the reveal
	VisibleRunes int
	TextColor    int
	// ChatSkinBase is the speaker's chatbox art (char.ini chat=<misc>), "" for the normal box.
	ChatSkinBase string
	// ChatSkinMsgColor and ChatSkinNameColor come from chat_config.ini c0 and courtroom_fonts.ini; false means theme.
	ChatSkinMsgColor  theme.RGB
	ChatSkinMsgHas    bool
	ChatSkinNameColor theme.RGB
	ChatSkinNameHas   bool
	// MessageStyles colors runs of MessageText. MessageRaw keys the raster cache: colors can differ over equal text.
	MessageStyles []StyleRun
	MessageRaw    string
	// MessageEffects tags rune spans of MessageText with an animated effect; empty keeps the plain raster fast path.
	MessageEffects []TextEffectSpan
	// Centered centre-aligns the text (webAO's "~~" prefix, stripped from display).
	Centered bool
	// IsBlankPost marks empty or whitespace text; the UI hides the chatbox. Set in begin() so the box never flashes.
	IsBlankPost bool

	// Full-viewport effects: Update counts these down and the renderer reads the remainders.
	FlashLeft time.Duration
	ShakeLeft time.Duration

	// Overlay is the active effects.ini overlay; fireOverlay sets it, three sites clear it, not message end (canon).
	Overlay SceneOverlay
}

// AudioSink receives playback triggers.
type AudioSink interface {
	// PlayShout plays a character's shout cry (base lacks extension).
	PlayShout(base string)
	// PlaySFX plays an emote sound effect after delay.
	PlaySFX(base string, delay time.Duration)
	// PlayBlip fires one chat blip.
	PlayBlip(base string)
	// SetBlipScale sets the speaker's blip attenuation (0-100, 100 = none) for later blips.
	SetBlipScale(pct int)
	// PlayMusic streams a full URL; loop=false plays once, effects carries the musicEffect* flags.
	PlayMusic(url string, loop bool, effects int)
	// StopMusic halts playback now (the ~stop sentinel; also on disconnect).
	StopMusic()
}

type NopAudio struct{}

func (NopAudio) PlayShout(string)              {}
func (NopAudio) PlaySFX(string, time.Duration) {}
func (NopAudio) PlayBlip(string)               {}
func (NopAudio) SetBlipScale(int)              {}
func (NopAudio) PlayMusic(string, bool, int)   {}
func (NopAudio) StopMusic()                    {}

// BlipResolver is the audio lookup: per-SFX mute, blip attenuation, char.ini blip-set fallback. nil = AO defaults.
type BlipResolver interface {
	SFXMuted(name string) bool
	BlipVolumeFor(char string) int
	BlipNameFor(char string) (name string, known bool)
}

// CharResolver is the char.ini lookup; nil = AO defaults. The App answers all four from one fetch.
type CharResolver interface {
	// ChatSkinFor resolves the chatbox-skin misc folder (get_chat); "" = none.
	ChatSkinFor(char string) string
	// ChatInkFor resolves courtroom_fonts.ini colours; false flags let the theme win.
	ChatInkFor(char string) (msg theme.RGB, msgOK bool, name theme.RGB, nameOK bool)
	// IniShownameFor is get_showname; known=false means no char.ini yet, known and empty is a deliberate blank plate.
	IniShownameFor(char string) (showname string, known bool)
	// CustomRealizationFor resolves the character's realization sound to a URL; "" falls back to RealizationSFX.
	CustomRealizationFor(char string) string
}

// AssetResolver covers effects folder, overlay, residency and scaling. nil = no overlay, no wait gating, auto scaling.
type AssetResolver interface {
	// EffectsFolderFor resolves [Options] effects; "" = the theme's effects only.
	EffectsFolderFor(char string) string
	// OverlayFor resolves an effect name and folder. Render-thread only; ok=true doesn't mean the art is resident.
	OverlayFor(name, folder string) (OverlayResolved, bool)
	// SpriteReady reports texture residency; same thread, a plain map probe.
	SpriteReady(base string) bool
	// SceneryFile returns the file a resident page was decoded from, "" when not resident; same thread.
	SceneryFile(base string) string
	// BackgroundDesign returns a background's parsed design.ini, nil when it has none; ok is false while it is read.
	BackgroundDesign(url string) (design *theme.INI, ok bool)
	// SpritePlayable reports every frame resident, or nothing more to wait for; same thread.
	SpritePlayable(base string) bool
	// AudioReady reports a cached decoded chunk; same thread, a plain map probe.
	AudioReady(base string) bool
	SpriteScaling(charName string) ScalingMode
}

// EnvResolver covers our side, shortcode emoji and the preanim-end notice. nil side degrades to Scene.Position.
type EnvResolver interface {
	// LocalSide reports our side, with AO2's default when unset (current_or_default_side).
	LocalSide() string
	// InlineEmote resolves a :shortcode: stem; ok=false = unknown.
	InlineEmote(stem string) (string, bool)
	// OnPreanimEnd is called with the reason at each release; diagnostic only.
	OnPreanimEnd(reason PreanimEndReason)
}

type Courtroom struct {
	urls  URLBuilder
	mgr   *assets.Manager
	sess  *Session
	audio AudioSink

	Scene      Scene
	Typewriter Typewriter
	// TextStay holds a finished message before the queue advances.
	TextStay time.Duration

	// CatchUp fast-forwards backlog ceremony once the queue reaches CatchUpThreshold; the IC log records every message.
	CatchUp          bool
	CatchUpThreshold int

	// ReduceMotion suppresses shake, flash and received sprite motion; effect sounds still play.
	ReduceMotion bool

	// ScreenEffects gates \s/\f and field effects, drawn only when ScreenEffects && !ReduceMotion; on for exports.
	ScreenEffects bool

	// AdditiveText honors 2.8 ADDITIVE=1. additivePrevText matches AO2's accumulator (courtroom.cpp:4225-4330), with no
	// check, so another speaker can continue; courtroom.cpp:4229 resets it on a non-additive line.
	AdditiveText     bool
	additivePrevText string
	additivePrefix   string // this message's pre-revealed additive prefix ("" = replace); set in begin, consumed in startTalking

	// HideSpriteStyles ignores other speakers' transmitted SpriteStyle.
	HideSpriteStyles bool

	// ForceCharNames shows character names rather than shownames (anti-impersonation); the IC log mirrors it App-side.
	ForceCharNames bool

	// StreamMusic false never fetches a /play track, while Now-Playing still tracks it.
	StreamMusic bool

	// Blips is the audio lookup (see BlipResolver).
	Blips BlipResolver

	// Chars is the char.ini lookup (see CharResolver).
	Chars CharResolver

	// Assets is the asset lookup (see AssetResolver).
	Assets AssetResolver
	// followBg, followFile and followDesk are the background, its file and the desk followBackground last settled on.
	followBg, followFile, followDesk string
	// slideFrom and the slideSpeaker, slidePair, slidePaired and slideInFront are the last message's position and
	// characters, which a slide starts from.
	slideFrom    string
	slideSpeaker SpriteLayer
	slidePair    SpriteLayer
	slidePaired  bool
	slideInFront bool
	// design is designBg's design.ini once read, nil when absent; designURL is asked until designRead.
	design     *theme.INI
	designBg   string
	designURL  string
	designRead bool

	// Env is the local state lookup (see EnvResolver).
	Env EnvResolver

	// SpriteWait holds a message off-stage until its idle sprite decodes; SpriteWaitTimeout means a 404 only delays it
	// (zero never holds). Shouts bypass, as AO2 plays them at once, and catch-up wins.
	SpriteWait        bool
	SpriteWaitTimeout time.Duration
	// SpriteWaitPair and SpriteWaitPreanim also hold for the partner's idle or the preanim, under the same timeout.
	SpriteWaitPair    bool
	SpriteWaitPreanim bool
	// SequentialWait is the "Default" loading mode: every asset the message presents settles before it begins.
	SequentialWait bool
	// ShoutBypassQueue is AO2's default: a shout clears the queue and plays at once; off, shouts queue normally.
	ShoutBypassQueue bool

	// ShoutDuration and PreanimTimeout are the ceremony timings; Notify callbacks cut the preanim wait short.
	ShoutDuration  time.Duration
	PreanimTimeout time.Duration

	// QueueCap bounds the queue, always >= 1 (hard rule 4); past it the oldest unplayed message drops, not its log.
	QueueCap int
	// CatchUpLinger holds each fast-forwarded message before the next; default zero drains one per frame.
	CatchUpLinger time.Duration

	// waitLeft counts down an armed hold; waitFor is its message by pointer, so a new head re-arms.
	waitLeft time.Duration
	waitFor  *protocol.ChatMessage
	// waitBlocker is the need holding the message, needs messageNeeds' reused list.
	waitBlocker messageNeed
	needs       []messageNeed
	// missingSprites holds 404'd bases of every kind, not just sprites, so a miss releases the wait gate's hold.
	// A dummy "-<n>" preanim releases this way. Bounded; game thread only.
	missingSprites map[string]struct{}
	// missingDesks is the 404'd desk set: set_scene hides the desk with no fallback (courtroom.cpp:4613-4634).
	// Separate because the preanim gate reads missingSprites. Bounded; game thread only.
	missingDesks map[string]struct{}

	queue []*protocol.ChatMessage
	phase MessagePhase
	timer time.Duration

	// Pending SFX deadline: AO2's sfx_delay_timer, with the preanim shake (courtroom.cpp:4052-4054, 4590-4596).
	// begin() clears these so a superseded message's SFX can't fire late.
	sfxArmed bool          // a delayed SFX/shake is waiting to fire
	sfxLeft  time.Duration // countdown to the fire moment (SFXDelay × sfxDelayUnit)
	sfxBase  string        // resolved SFX URL to play at the deadline ("" = shake only)
	sfxShake bool          // also fire the screenshake at the deadline (preanim mods)

	// overlayPending is the one deferred screen-effect request, re-asked on Update until it lands or expires.
	overlayPending overlayPendingFX

	// frameTriggers holds this message's FRAME_* effects, fired by NotifyFrameShown; usually empty.
	frameTriggers frameTriggerTable

	current *protocol.ChatMessage
	// currentText has style markers decoded out; the raw Message keeps them for recordings, so never mutate it.
	currentText string
	// blipRef is this message's blip chain from BlipRef; it may stay empty while the speaker's char.ini is in flight.
	blipRef AssetRef
	// blipWire is the validated wire blip ("" if absent or a sentinel). blipPending means char.ini is in flight
	// and talk ticks re-ask, so the first message never sounds the wrong default.
	blipWire    string
	blipSpeaker string
	blipPending bool
	// pendingEffects holds this message's animated-text spans until startTalking; per-message content, never recalled.
	pendingEffects []TextEffectSpan

	// styleByChar remembers each speaker's last style by CharID; a clear frees the entry. Bounded.
	styleByChar map[int]SpriteStyle

	// profileByName remembers each speaker's WireProfile by bare character name; a clear frees the entry. Bounded.
	profileByName map[string]WireProfile

	// asyncAOByName flags characters seen on the zero-width channel. Bounded by maxDetectedAsyncAO.
	asyncAOByName map[string]struct{}

	// statusByName remembers each speaker's Status by bare character name; a clear frees the entry. Bounded.
	statusByName map[string]Status

	// Predictor warms the predicted next speaker's sprite (optional).
	Predictor *assets.Prefetcher

	// preanimDone is set by NotifyPreanimDone.
	preanimDone bool

	// imPreLeft bounds an immediate-mode preanim, decoration over a ticking message (courtroom.cpp:4093-4096);
	// separate from c.timer because it outlives its phase. >0 means running.
	imPreLeft time.Duration

	// preanimEnd records why the last preanimation ended; diagnostic only.
	preanimEnd PreanimEndReason

	// restoring makes begin() skip prefetches for art the settled form never draws, saving cold high-priority probes.
	restoring bool

	// ShoutUses tracks whether the per-character bubble resolved; else the renderer uses the default.
	ShoutCharBase    string
	ShoutDefaultBase string

	// RealizationSFX is the theme's realization sound ("" = silent), for REALIZATION=1 without an Effects override.
	RealizationSFX string
}

// NewCourtroom wires the state machine. audio may be nil (NopAudio).
func NewCourtroom(urls URLBuilder, mgr *assets.Manager, sess *Session, audio AudioSink) *Courtroom {
	if audio == nil {
		audio = NopAudio{}
	}
	c := &Courtroom{
		urls:       urls,
		mgr:        mgr,
		sess:       sess,
		audio:      audio,
		Typewriter: NewTypewriter(),
		// Default on; the App pushes the pref, and export and replay contexts keep them on.
		ScreenEffects: true,
		// Default on; the App pushes the pref. An empty prefix appends as a plain replace.
		AdditiveText: true,
		// Default on: the zero value would silently swallow every /play track for direct callers.
		StreamMusic: true,
		TextStay:    DefaultTextStayTime,
		// Catch-up is off here so direct callers keep the full lifecycle; the App enables it.
		CatchUpThreshold: catchUpDefaultThreshold,
		// Seeded to AO2's values; the App overrides from prefs.
		ShoutDuration:  DefaultShoutDuration,
		PreanimTimeout: DefaultPreanimTimeout,
		QueueCap:       messageQueueCap,
		CatchUpLinger:  catchUpLinger,
		// Shouts skip the queue by default (AO2); the App pushes the pref.
		ShoutBypassQueue: true,
	}
	c.Scene.SpeakerInFront = true
	return c
}

func (c *Courtroom) Phase() MessagePhase { return c.phase }

// AudioActive reports the typewriter is revealing text; the main loop then ticks finely so blips never batch to frames.
func (c *Courtroom) AudioActive() bool {
	return c.phase == PhaseTalking && !c.Typewriter.Done()
}

func (c *Courtroom) QueueLen() int { return len(c.queue) }

// CurrentShout is the live message's shout stem, "" when idle or none.
func (c *Courtroom) CurrentShout() string {
	if c.phase == PhaseIdle || c.current == nil {
		return ""
	}
	return ShoutName(c.current.Objection)
}

func (c *Courtroom) HandleEvent(ev Event) {
	switch ev.Kind {
	case EventMessage:
		c.enqueue(ev.Message)
	case EventBackground:
		// Name carries the BN's pos, Wipe its arity; both unset for client changes, so re-seeds can't erase a restore.
		c.setBackground(ev.Text, ev.Name, ev.Wipe)
	case EventMusic:
		switch {
		case ev.Text == "" || isAreaTransfer(ev.Text):
			// empty, or an area-name transfer (unified music/area list): not a song
		case isMusicStop(ev.Text):
			// ~stop isn't a track: halt now, as PlayMusic is async and a 404 would leave music running.
			c.audio.StopMusic()
			c.Scene.MusicTrack = "" // clear Now-Playing
		default:
			// When off, never fetch, but still record the track so Now-Playing stays accurate.
			if c.StreamMusic {
				c.audio.PlayMusic(c.urls.MusicURL(ev.Text), ev.Loop, ev.MusicEffects) // AssetType: Music
			}
			c.Scene.MusicTrack = ev.Text
		}
	}
}

// setScenery points the scene at pos in bg and asks for its background under every name AO2 tries and its likely desk,
// HIGH like every live-message asset; the desk follows the name that loads.
func (c *Courtroom) setScenery(bg, pos string) {
	s := c.urls.PositionScenery(bg, pos, c.design)
	c.Scene.BackgroundBase, c.Scene.BackgroundAlts, c.Scene.DeskBase = s.Background, s.Alts, s.Desk
	c.placeScenery(fileStem(s.Background))
	c.mgr.PinScenery(s.Background[:strings.LastIndexByte(s.Background, '/')+1])
	c.mgr.PrefetchChain(s.Background, s.Alts, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	c.mgr.Prefetch(s.Desk, assets.AssetTypeDeskOverlay, network.PriorityHigh)                   // AssetType: DeskOverlay
	c.followBackground()
}

// isMusicStop reports AO's ~stop sentinel.
func isMusicStop(track string) bool {
	return strings.HasPrefix(strings.ToLower(track), "~stop")
}

// MusicAction classifies an MC track for the IC log like handle_song; ok=false for an area-name transfer.
func MusicAction(track string) (action, song string, ok bool) {
	if track == "" {
		return "", "", false
	}
	if isMusicStop(track) {
		return "has stopped the music", "", true
	}
	if isAreaTransfer(track) {
		return "", "", false
	}
	return "has played a song", MusicDisplayName(track), true
}

// pathSeparators ends a track's directory part; AO2 splits only on '/', but Windows-authored lists can carry '\'.
const pathSeparators = `/\`

// MusicDisplayName is an entry's short name; the wire name is untouched. An extension-only name falls back to raw.
func MusicDisplayName(track string) string {
	s := track
	if isMusicURL(track) {
		if i := strings.IndexAny(s, "?#"); i >= 0 { // a Discord CDN /play link's signed query
			s = s[:i]
		}
		s = strings.TrimRight(s, pathSeparators)
		if i := strings.LastIndexAny(s, pathSeparators); i >= 0 { // basename
			s = s[i+1:]
		}
		if i := strings.LastIndexByte(s, '.'); i > 0 { // strip the extension
			s = s[:i]
		}
	} else {
		if i := strings.LastIndexByte(s, '.'); i >= 0 { // left(lastIndexOf("."))
			s = s[:i]
		}
		if i := strings.LastIndexAny(s, pathSeparators); i >= 0 { // right(... lastIndexOf("/") + 1)
			s = s[i+1:]
		}
	}
	if s == "" {
		return track // never draw a blank row (see the divergence note above)
	}
	return s
}

// isAreaTransfer filters area names; an http(s) URL is always music, as its extension can precede a query string.
func isAreaTransfer(track string) bool {
	if isMusicURL(track) {
		return false
	}
	return !HasAudioExt(track)
}

// spriteSettled reports resident or conclusively 404'd; the wait gate must never hold for an answer it has.
func (c *Courtroom) spriteSettled(base string) bool {
	if c.Assets != nil && c.Assets.SpriteReady(base) {
		return true
	}
	return c.spriteConfirmedMissing(base)
}

// speakerSpriteSettled also rejects a partial or reduced page: the stage would hold the old sprite or skip a one-shot.
func (c *Courtroom) speakerSpriteSettled(base string) bool {
	if c.Assets != nil && c.Assets.SpritePlayable(base) {
		return true
	}
	return c.spriteConfirmedMissing(base)
}

// audioSettled is spriteSettled for audio: decoded and cached, or in the shared 404 set.
func (c *Courtroom) audioSettled(base string) bool {
	if c.Assets != nil && c.Assets.AudioReady(base) {
		return true
	}
	return c.spriteConfirmedMissing(base)
}

// preanimWillPlay reports that the preanim shows first; AO2 reaches play_preanim only for PREANIM, PREANIM_ZOOM,
// or immediate IDLE and ZOOM (courtroom.cpp:2889-2910).
func preanimWillPlay(msg *protocol.ChatMessage) bool {
	return hasPreanim(msg) &&
		(msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom || msg.Immediate)
}

// waitHolds reports whether the gate holds msg this tick; behind matches begin()'s catch-up check, dt 0 only arms.
func (c *Courtroom) waitHolds(msg *protocol.ChatMessage, behind int, dt time.Duration) bool {
	if c.Assets == nil || msg == nil {
		return false
	}
	if !c.SpriteWait && !c.SequentialWait {
		return false
	}
	// Catch-up wins, independent of strict sequencing; the two are separate knobs, as in AO2.
	if c.CatchUp && behind >= c.CatchUpThreshold {
		c.waitFor = nil // catch-up wins: this message fast-forwards, waiting would only add lag
		return false
	}
	// This runs every frame of a hold: the need that stopped the last check is one lookup, the full list 174 allocations.
	// A release is always decided on a fresh list.
	armed := c.waitFor == msg
	if !armed || c.needSettled(c.waitBlocker) {
		needs := c.messageNeeds(msg)
		blocker, blocked := c.firstUnsettled(needs)
		if !blocked {
			c.waitFor = nil
			return false
		}
		c.waitBlocker = blocker
		if !armed { // a new head arms: start the countdown + warm its sprites
			c.waitFor = msg
			c.waitLeft = c.SpriteWaitTimeout
			if c.SequentialWait {
				c.waitLeft = sequentialWaitLimit
			}
			c.requestNeeds(needs)
		}
	}
	c.waitLeft -= dt
	if c.waitLeft <= 0 {
		c.waitFor = nil // timed out: play anyway (the renderer's own cold-load mode covers the gap)
		return false
	}
	return true
}

// enqueue mirrors chatmessage_enqueue: shouts clear the queue and play at once; others run in order.
func (c *Courtroom) enqueue(msg *protocol.ChatMessage) {
	if msg == nil {
		return
	}
	if msg.IsShout() && c.ShoutBypassQueue {
		c.queue = c.queue[:0]
		c.begin(msg)
		return
	}
	if c.phase == PhaseIdle && len(c.queue) == 0 {
		// A cold sprite parks the message in the queue; Update's PhaseIdle case drains it.
		if c.waitHolds(msg, 0, 0) {
			c.queue = append(c.queue, msg)
			return
		}
		c.begin(msg)
		return
	}
	// Floored at 1 so a bad pref can't unbound the queue (hard rule 4).
	qcap := c.QueueCap
	if qcap < 1 {
		qcap = 1
	}
	if len(c.queue) < qcap {
		c.queue = append(c.queue, msg)
	}
	// Beyond the cap the oldest unplayed message drops (hard rule 4); logs still record it.
	if len(c.queue) == qcap {
		copy(c.queue, c.queue[1:])
		c.queue = c.queue[:qcap-1]
		c.queue = append(c.queue, msg)
	}
}

// setBackground prefetches the scenery and, on a wiping BN, tears down as set_background(bg, display=true) does.
func (c *Courtroom) setBackground(bg, pos string, wipe bool) {
	if wipe {
		if pos == "" && c.Env != nil {
			pos = c.Env.LocalSide()
		}
		if pos != "" {
			c.Scene.Position = pos
		}
	}
	if bg != c.designBg {
		c.design, c.designBg, c.designURL, c.designRead = nil, bg, c.urls.BackgroundDesign(bg), false
	}
	c.setScenery(bg, c.Scene.Position)
	// Re-derive from the new desk base, or the new room draws the old desk: a settled message has no phase edge.
	if wipe {
		c.wipeStage()
		return
	}
	if c.current != nil {
		c.applyDeskMods(c.phase == PhasePreanim)
	}
}

// WipeStage ports set_background's `if (display)` teardown (courtroom.cpp), only for a server BN with a pos.
// Not cleared, as AO2 leaves them: IC log, music, evidence list, HP, missing sets, remembered styles.
func (c *Courtroom) WipeStage() { c.wipeStage() }

func (c *Courtroom) wipeStage() {
	// Report a one-shot before the layer is zeroed, as nothing shows one was playing after that.
	c.endPreanim(PreanimEndStageWiped)
	// drawSprite returns early on `!Visible || Active == ""`, so a zeroed layer can't leak a frame.
	c.Scene.Speaker = SpriteLayer{}
	c.Scene.Pair = SpriteLayer{}
	c.Scene.Slide = SlideScene{}
	c.Scene.Group = nil
	c.Scene.PairActive = false
	c.Scene.SpeakerInFront = true

	// Clearing both texts is the hide: drawChatOverlay returns early when both are empty.
	c.Scene.ShownameText = ""
	c.Scene.MessageText = ""
	c.Scene.MessageRaw = ""
	c.Scene.MessageStyles = c.Scene.MessageStyles[:0]
	c.Scene.MessageEffects = c.Scene.MessageEffects[:0]
	c.Scene.VisibleRunes = 0
	c.Scene.TextColor = 0
	c.Scene.ChatSkinBase = ""
	c.Scene.Centered = false
	c.Scene.IsBlankPost = false

	// Shout bubble (ui_vp_objection->stopPlayback) and the screen effects.
	c.Scene.ShoutBase = ""
	c.Scene.ShoutFallbackBase = ""
	c.Scene.ShoutCustom = false
	c.Scene.FlashLeft = 0
	c.Scene.ShakeLeft = 0
	// Clear site 2 of 3: set_background(bg, display=true) hides the effect layer (courtroom.cpp:1427-1435).
	c.clearOverlay()

	// This loses no IC log line: we append at ingest, before the room sees the message.
	c.queue = c.queue[:0]

	// text_state = 2; anim_state = 3. Nil current stops a late desk 404 resurrecting the pair sprite.
	c.phase, c.timer = PhaseIdle, 0
	c.current, c.currentText = nil, ""
	c.Typewriter.Start("") // chat_tick_timer->stop(); in place, keeps Interval/BlipRate

	// No AO2 analogue: else the next area's first line inherits delayed SFX, frame triggers or the additive prefix.
	c.preanimDone = false
	c.imPreLeft = 0
	c.waitFor, c.waitLeft = nil, 0
	c.sfxArmed, c.sfxLeft, c.sfxBase, c.sfxShake = false, 0, "", false
	c.frameTriggers = frameTriggerTable{}
	c.pendingEffects = c.pendingEffects[:0]
	c.additivePrevText, c.additivePrefix = "", ""

	// set_scene(true, ...): show_desk is hard-true, and only a missing file suppresses it.
	c.Scene.ShowDesk = c.deskResolution() != DeskAbsent
}

// applyCenterPrefix implements webAO's "~~": strip it, set Scene.Centered, and shift effect spans left by 2.
func (c *Courtroom) applyCenterPrefix() {
	if !strings.HasPrefix(c.currentText, "~~") {
		c.Scene.Centered = false
		return
	}
	c.currentText = c.currentText[2:]
	c.Scene.Centered = true
	for i := range c.pendingEffects {
		if c.pendingEffects[i].Start >= 2 {
			c.pendingEffects[i].Start -= 2
			continue
		}
		c.pendingEffects[i].Len -= 2 - c.pendingEffects[i].Start // span began inside the marker: clip it
		c.pendingEffects[i].Start = 0
		if c.pendingEffects[i].Len < 0 {
			c.pendingEffects[i].Len = 0
		}
	}
}

// AdditiveOutgoingLeadingSpace keeps AO2's leading space on additive sends (courtroom.cpp:2320-2326, :6563-6573).
// It checks stripped text because funColor may have prepended markup; a blankpost strips to a leading space.
func AdditiveOutgoingLeadingSpace(text string) string {
	if !AdditiveWantsLeadingSpace(text) {
		return text // already separated (real leading space, or a blankpost)
	}
	return " " + text
}

// AdditiveWantsLeadingSpace is the shared test: spans are encoded before the prepend, so both must decide alike.
func AdditiveWantsLeadingSpace(text string) bool {
	stripped := StripChatMarkup(text)
	if stripped == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(stripped)
	return !unicode.IsSpace(first) && !isNoSpaceRune(first)
}

// additiveNeedsJoiner reports whether a space belongs between prefix and fragment (both stripped):
// neither side supplies one, and neither boundary rune is from a no-space script.
func additiveNeedsJoiner(prev, frag string) bool {
	if prev == "" || frag == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(prev)
	first, _ := utf8.DecodeRuneInString(frag)
	// A genuine RuneError (invalid UTF-8) counts as non-space, non-CJK, so glue it like a raw byte join.
	if unicode.IsSpace(last) || unicode.IsSpace(first) {
		return false
	}
	if isNoSpaceRune(last) || isNoSpaceRune(first) {
		return false
	}
	return true
}

// isNoSpaceRune reports a block without inter-word spaces; CJK punctuation is script=Common, so not in unicode.Han.
func isNoSpaceRune(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x303F: // CJK Symbols and Punctuation (。、「」〜…)
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // Halfwidth and Fullwidth Forms (！？，．＆)
		return true
	}
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func (c *Courtroom) begin(msg *protocol.ChatMessage) {
	// Last moment to report a one-shot as ended by this cause (unpack_chatmessage resets anim_state to 0).
	c.endPreanim(PreanimEndNewMessage)
	c.current = msg
	c.waitFor = nil // beginning ends any armed wait-gate hold
	// Drop any armed SFX or shake so a superseded message can't fire late; enterAfterShout re-arms.
	c.sfxArmed, c.sfxLeft, c.sfxBase, c.sfxShake = false, 0, "", false
	// A backlog means the stage is behind: fast-forward. At threshold 1, only an unqueued message plays in full.
	if c.CatchUp && len(c.queue) >= c.CatchUpThreshold {
		c.beginCaughtUp(msg)
		return
	}
	// Never mutate msg.Message: the recording shares that pointer and a replay re-decodes the marker.
	style, clean := DecodeSpriteStyle(msg.Message)
	c.currentText = clean
	// Sent only on change: remember it, else reuse the last style. A profile-only message must not read as a clear.
	if HasStyleMarker(msg.Message) {
		c.rememberStyle(msg.CharID, style)
	} else {
		style = c.RecalledStyle(msg.CharID)
	}
	// The speaker's profile, on the same channel; an empty one frees the entry.
	if hasMarker(msg.Message) {
		c.rememberAsyncAO(msg.CharName) // any cross-client frame ⇒ this speaker is on AsyncAO
	}
	if prof, ok := DecodeProfileMarker(msg.Message); ok {
		c.rememberProfile(msg.CharName, prof)
	}
	// The speaker's status, on the same channel.
	if st, ok := DecodeStatusMarker(msg.Message); ok {
		c.rememberStatus(msg.CharName, st)
	}
	// Animated-text spans are per-message, kept until the text shows (startTalking).
	c.pendingEffects = c.pendingEffects[:0]
	if spans, ok := DecodeEffectsMarker(msg.Message); ok {
		c.pendingEffects = append(c.pendingEffects, spans...)
	}
	// Strip "~~" and realign effect spans before the blankpost test, emote expansion and reveal.
	c.applyCenterPrefix()
	// Expand only with no effect spans, as substitution shifts rune counts. Must run after the pendingEffects decode
	// and before IsBlankPost and Typewriter.Start; msg.Message stays literal, keeping reaction refs stable.
	if c.Env != nil && len(c.pendingEffects) == 0 {
		c.currentText = ExpandInlineEmotes(c.currentText, c.Env.InlineEmote)
	}
	// Same filter as the pair partner's style below, so no one imposes motion a viewer opted out of.
	style = c.filterStyleForViewer(style)

	// ADDITIVE=1 makes the prior text a pre-revealed prefix, with no char-id check (courtroom.cpp:4225-4330);
	// a non-additive line resets it (courtroom.cpp:4229). Restoring gives "", so it can't inherit a stale prefix.
	c.additivePrefix = ""
	if c.AdditiveText && !c.restoring && msg.Additive {
		c.additivePrefix = c.additivePrevText
		// Senders glue fragments; AO2 works around it when sending (courtroom.cpp:2320-2326, 6563-6573), so join here.
		if additiveNeedsJoiner(StripChatMarkup(c.additivePrefix), StripChatMarkup(c.currentText)) {
			c.additivePrefix += " "
		}
		if shift := len([]rune(StripChatMarkup(c.additivePrefix))); shift > 0 {
			for i := range c.pendingEffects {
				c.pendingEffects[i].Start += shift // spans ride the appended tail (past prefix + any joiner)
			}
		}
	}
	c.additivePrevText = c.additivePrefix + c.currentText

	speakerName := msg.CharName

	// Prefetch fan-out, all HIGH. Sprites walk "(a)X", bare, "(a)/X" (animationlayer.cpp:422-444).
	idle := c.urls.Emote(speakerName, msg.Emote, EmoteIdle)
	talk := c.urls.Emote(speakerName, msg.Emote, EmoteTalk)
	c.mgr.PrefetchChain(idle, c.spriteAlts(speakerName, msg.Emote, EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	c.mgr.PrefetchChain(talk, c.spriteAlts(speakerName, msg.Emote, EmoteTalk), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite

	pre := ""
	if hasPreanim(msg) {
		pre = c.urls.Emote(speakerName, msg.PreEmote, EmotePreanim)
		if !c.restoring { // settled form never plays the preanim — don't probe for it
			c.mgr.Prefetch(pre, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
		}
	}

	pairIdle := ""
	if msg.Pair.Active() {
		pairIdle = c.urls.Emote(msg.Pair.Name, msg.Pair.Emote, EmoteIdle)
		c.mgr.PrefetchChain(pairIdle, c.spriteAlts(msg.Pair.Name, msg.Pair.Emote, EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (pair partner)
	}

	if msg.IsShout() {
		charBase, defaultBase, shoutSFX := c.shoutBases(msg)
		c.ShoutCharBase = charBase
		c.ShoutDefaultBase = defaultBase
		if !c.restoring { // settled form never shows the bubble — don't probe for it
			if defaultBase != "" {
				c.mgr.Prefetch(defaultBase, assets.AssetTypeShoutBubble, network.PriorityHigh) // AssetType: ShoutBubble
			}
			c.mgr.Prefetch(charBase, assets.AssetTypeShoutBubble, network.PriorityHigh) // AssetType: ShoutBubble
		}
		c.audio.PlayShout(shoutSFX) // AssetType: SFX
	}

	// Warm the likely next speaker and their likely next emote.
	if c.Predictor != nil {
		c.Predictor.OnMessage(speakerName, msg.Pair.Name, msg.Emote)
	}

	// Blip set: wire first, then char.ini (webAO); in-flight stays pending, and only known-but-empty uses AO's default.
	c.blipWire = msg.Blipname
	if !validBlipName(c.blipWire) {
		c.blipWire = "" // sentinel / path-unsafe: fall through to char.ini
	}
	c.blipSpeaker = speakerName
	c.resolveBlip()

	// Per-character chatbox skin (chat=<misc>, get_chat); candidates walk in order, the first being the identity.
	c.Scene.ChatSkinBase = ""
	if c.Chars != nil {
		if misc := c.Chars.ChatSkinFor(speakerName); misc != "" {
			cands := c.urls.MiscChatboxCandidates(misc)
			c.Scene.ChatSkinBase = cands[0]
			c.mgr.PrefetchChain(cands[0], cands[1:], assets.AssetTypeMisc, network.PriorityHigh) // AssetType: Misc (chatbox skin)
		}
	}
	// The speaker's chatbox colours (misc/<chat>/courtroom_fonts.ini, get_chat), falling back to the theme.
	c.Scene.ChatSkinMsgColor, c.Scene.ChatSkinMsgHas = theme.RGB{}, false
	c.Scene.ChatSkinNameColor, c.Scene.ChatSkinNameHas = theme.RGB{}, false
	if c.Chars != nil {
		if msg, mOK, name, nOK := c.Chars.ChatInkFor(speakerName); mOK || nOK {
			c.Scene.ChatSkinMsgColor, c.Scene.ChatSkinMsgHas = msg, mOK
			c.Scene.ChatSkinNameColor, c.Scene.ChatSkinNameHas = name, nOK
		}
	}

	// Attenuate this speaker's blips; one int-set per message, so the render loop is untouched.
	blipScale := blipVolumeFull
	if c.Blips != nil {
		blipScale = c.Blips.BlipVolumeFor(speakerName)
	}
	c.audio.SetBlipScale(blipScale)

	// The emote SFX is armed in enterAfterShout, to land SFX_DELAY x 40ms in (sfx_delay_timer, courtroom.cpp:4052).

	// --- scene state ---
	c.slideFrom, c.slideSpeaker = c.Scene.Position, c.Scene.Speaker
	c.slidePair, c.slidePaired, c.slideInFront = c.Scene.Pair, c.Scene.PairActive, c.Scene.SpeakerInFront
	c.Scene.Position = msg.Side
	if c.sess != nil && c.sess.Background != "" {
		c.setScenery(c.sess.Background, msg.Side)
	}
	// Start on the talk column; phase changes call applyDeskMods (set_scene at preanim_start, start_chat_ticking).
	// A zoom's talk side hides the desk, and a missing desk stays hidden.
	c.Scene.ShowDesk = DeskDrawn(msg.DeskMod, false, c.deskResolution()) && !zoomEmote(msg.EmoteMod)

	// Clear site 1 of 3: display_character hides ui_vp_effect (courtroom.cpp:2755-2761); no clear at message end.
	c.clearOverlay()

	c.Scene.Speaker = SpriteLayer{
		Name:        speakerName,
		IdleBase:    idle,
		TalkBase:    talk,
		PreanimBase: pre,
		Active:      idle,
		Flip:        msg.Flip,
		OffsetX:     msg.SelfOffsetX,
		OffsetY:     msg.SelfOffsetY,
		Visible:     true,
		Style:       style, // transmitted recolour / glow / opacity / motion
		Scaling:     c.scalingFor(speakerName),
	}

	// Zoom speedlines go behind the speaker. No magnification: AO2 only hides desk and pair (courtroom.cpp:3456-3477).
	c.Scene.SpeedlinesChain = nil
	c.Scene.SpeedlinesSide = ""
	if msg.EmoteMod == protocol.EmoteModZoom || msg.EmoteMod == protocol.EmoteModPreanimZoom {
		c.Scene.SpeedlinesSide = ZoomSide(msg.Side)
		c.Scene.SpeedlinesChain = c.urls.SpeedlinesCandidates(speakerName, msg.Side)
		c.mgr.PrefetchChain(c.Scene.SpeedlinesChain[0], c.Scene.SpeedlinesChain[1:],
			assets.AssetTypeMisc, network.PriorityHigh) // AssetType: Misc (zoom speedlines)
	}

	// Parse FRAME_* once here, never per frame, after the Speaker bases so NotifyFrameShown can bind the group.
	c.frameTriggers = c.buildFrameTriggers(msg)

	// A zoom is a solo shot, so the pair layer stays zeroed. A group roster (additional_chars) supersedes the pair,
	// which would re-draw the first partner.
	hasGroup := len(msg.Additional) >= 2
	c.Scene.PairActive = msg.Pair.Active() && !zoomEmote(msg.EmoteMod) && !hasGroup
	c.Scene.SpeakerInFront = msg.Pair.SpeakerInFront()
	if c.Scene.PairActive {
		c.Scene.Pair = SpriteLayer{
			Name:     msg.Pair.Name,
			IdleBase: pairIdle,
			Active:   pairIdle,
			Flip:     msg.Pair.Flip,
			OffsetX:  msg.Pair.OffsetX,
			OffsetY:  msg.Pair.OffsetY,
			Visible:  true,
			// The wire carries no partner style, so recall it by char id; a negative id gets the zero style.
			Style:   c.filterStyleForViewer(c.RecalledStyle(msg.Pair.CharID)),
			Scaling: c.scalingFor(msg.Pair.Name),
		}
	} else {
		c.Scene.Pair = SpriteLayer{}
	}

	// Stage other group members behind speaker and pair, in roster z-order.
	c.Scene.Group = c.Scene.Group[:0]
	for _, m := range msg.Additional {
		if m.CharID == msg.CharID {
			continue // the speaker is already on the Speaker layer
		}
		base := c.urls.Emote(m.Name, m.Emote, EmoteIdle)
		c.mgr.PrefetchChain(base, c.spriteAlts(m.Name, m.Emote, EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh)
		c.Scene.Group = append(c.Scene.Group, SpriteLayer{
			Name:     m.Name,
			IdleBase: base,
			Active:   base,
			Flip:     m.FlipH(),
			OffsetX:  m.OffsetX,
			OffsetY:  m.OffsetY,
			Side:     m.Side,
			Visible:  true,
			Scaling:  c.scalingFor(m.Name),
		})
	}

	c.Scene.ShownameText = c.displayName(msg)
	c.Scene.TextColor = msg.TextColor
	c.Scene.MessageText = ""
	c.Scene.MessageRaw = ""
	c.Scene.MessageStyles = c.Scene.MessageStyles[:0]
	c.Scene.MessageEffects = c.Scene.MessageEffects[:0]
	c.Scene.VisibleRunes = 0
	// Set up front so the box never flashes in a preanim; a continuation with real prior text is never a blankpost.
	c.Scene.IsBlankPost = strings.TrimSpace(StripChatMarkup(c.additivePrefix+c.currentText)) == ""
	c.preanimDone = false
	// Any previous immediate-mode decoration is over (unpack_chatmessage resets anim_state to 0).
	c.imPreLeft = 0

	// --- phase entry ---
	switch {
	case msg.IsShout():
		c.Scene.ShoutBase = c.ShoutCharBase
		c.Scene.ShoutFallbackBase = c.ShoutDefaultBase // misc/default when the char has none
		c.Scene.ShoutCustom = msg.Objection == protocol.ShoutCustom
		c.phase = PhaseShout
		c.timer = c.ShoutDuration // power-user knob; seeded to DefaultShoutDuration
	default:
		c.enterAfterShout()
	}
}

// resolveBlip sets c.blipRef, holding while char.ini is in flight so the first message never sounds the wrong default.
func (c *Courtroom) resolveBlip() {
	c.blipPending = false
	ref, pending := c.blipRefFor(c.blipWire, c.blipSpeaker)
	if pending {
		// In-flight: hold, don't default.
		c.blipPending = true
		c.blipRef = AssetRef{}
		return
	}
	c.blipRef = ref
	c.mgr.PrefetchChain(ref.Base, ref.Alts, ref.Type, network.PriorityHigh)
}

// blipRefFor builds the chain for the wait gate; pending means char.ini is in flight, so skip the blip, don't hold.
func (c *Courtroom) blipRefFor(blipWire, blipSpeaker string) (AssetRef, bool) {
	if blipWire != "" {
		return c.urls.BlipRef(blipWire), false
	}
	if c.Blips != nil {
		name, known := c.Blips.BlipNameFor(blipSpeaker)
		if known {
			if !validBlipName(name) {
				name = "" // a char.ini is server bytes too — same one guard, not a second copy
			}
			if name == "" {
				name = defaultBlipSet // get_blipname's last resort (text_file_functions.cpp:510)
			}
			return c.urls.BlipRef(name), false
		}
		return AssetRef{}, true // char.ini still in flight
	}
	return c.urls.BlipRef(defaultBlipSet), false
}

// shoutBases returns bubble, default bubble and cry, shared by begin() and the gate so they can't disagree.
func (c *Courtroom) shoutBases(msg *protocol.ChatMessage) (charBase, defaultBase, sfx string) {
	shout := ShoutName(msg.Objection)
	custom := msg.Objection == protocol.ShoutCustom
	charBase = c.urls.ShoutBubble(msg.CharName, shout, custom)
	sfx = c.urls.ShoutSFX(msg.CharName, shout)
	if custom && msg.CustomShout != "" {
		// 2.10 named interjection under custom_objections/<name> (courtroom.cpp objection_custom).
		charBase = c.urls.NamedCustomShout(msg.CharName, msg.CustomShout)
		sfx = charBase
	}
	if !custom {
		defaultBase = c.urls.DefaultShoutBubble(shout)
	}
	return charBase, defaultBase, sfx
}

// beginCaughtUp shows a backlog message for about a frame with no ceremony; the sprite stays, as it is never seen.
func (c *Courtroom) beginCaughtUp(msg *protocol.ChatMessage) {
	// No ceremony means no frame triggers: clear the prior message's so NotifyFrameShown can't fire them.
	c.frameTriggers = frameTriggerTable{}
	c.Scene.ShoutBase = ""
	c.Scene.ShoutFallbackBase = ""
	c.Scene.ShownameText = c.displayName(msg)
	c.Scene.TextColor = msg.TextColor
	caStyle, caClean := DecodeSpriteStyle(msg.Message) // strip the style marker (catch-up never redraws the sprite)
	c.currentText = caClean
	// Strip "~~" so the flash doesn't show it.
	if c.Scene.Centered = strings.HasPrefix(c.currentText, "~~"); c.Scene.Centered {
		c.currentText = c.currentText[2:]
	}
	// Mirror begin()'s expansion so the flash agrees with the IC log; none with effect spans.
	if c.Env != nil {
		if _, hasEffects := DecodeEffectsMarker(msg.Message); !hasEffects {
			c.currentText = ExpandInlineEmotes(c.currentText, c.Env.InlineEmote)
		}
	}
	if HasStyleMarker(msg.Message) {
		c.rememberStyle(msg.CharID, caStyle) // keep the per-speaker style memory consistent through a catch-up
	}
	if hasMarker(msg.Message) {
		c.rememberAsyncAO(msg.CharName) // any cross-client frame ⇒ this speaker is on AsyncAO
	}
	if prof, ok := DecodeProfileMarker(msg.Message); ok {
		c.rememberProfile(msg.CharName, prof) // and the per-character profile memory (#101)
	}
	if st, ok := DecodeStatusMarker(msg.Message); ok {
		c.rememberStatus(msg.CharName, st) // and the per-character status (#M1)
	}
	c.Typewriter.Start(c.currentText)
	c.Typewriter.SkipToEnd()
	c.Scene.MessageText = c.Typewriter.Text()
	c.Scene.MessageRaw = c.currentText
	c.Scene.MessageStyles = append(c.Scene.MessageStyles[:0], c.Typewriter.Styles()...)
	if spans, ok := DecodeEffectsMarker(msg.Message); ok { // #M5 spans (one-frame flash still shows them)
		c.Scene.MessageEffects = append(c.Scene.MessageEffects[:0], spans...)
	} else {
		c.Scene.MessageEffects = c.Scene.MessageEffects[:0]
	}
	c.Scene.VisibleRunes = c.Typewriter.Visible()
	// Same blankpost rule as begin(); this path skips it.
	c.Scene.IsBlankPost = strings.TrimSpace(c.Scene.MessageText) == ""
	c.preanimDone = false
	// A caught-up message breaks the additive chain, so a later ADDITIVE=1 continues from this one.
	c.additivePrefix, c.additivePrevText = "", c.currentText
	c.phase = PhaseLinger
	c.timer = c.CatchUpLinger // power-user knob; the canonical default is zero (drain one per frame)
}

// enterAfterShout picks preanim or talking as handle_emote_mod does; immediate IDLE and ZOOM preanims run with text.
func (c *Courtroom) enterAfterShout() {
	c.Scene.ShoutBase = ""
	c.Scene.ShoutFallbackBase = ""
	if c.startSlide() {
		return
	}
	c.playEmote()
}

// startSlide starts the pan when do_transition would (courtroom.cpp:3032): a new position, both on the loaded court with
// an origin, the message asking for a slide, a duration in design.ini, no zoom and motion not reduced. Only with the
// AO2 queue: webAO pacing has no such slide.
func (c *Courtroom) startSlide() bool {
	msg := c.current
	if !msg.Slide || c.ReduceMotion || c.CatchUp || msg.Emote == "" || zoomEmote(msg.EmoteMod) || c.design == nil ||
		c.slideFrom == "" || c.slideFrom == c.Scene.Position ||
		fileStem(c.sceneryFile(c.Scene.BackgroundBase)) != courtPanorama {
		return false
	}
	from, to := PanoramaPosition(c.slideFrom, c.design), PanoramaPosition(c.Scene.Position, c.design)
	_, sub, toOnPanorama := strings.Cut(to, panoramaSep)
	if !strings.Contains(from, panoramaSep) || !toOnPanorama {
		return false
	}
	fromOrigin, fromOK := c.designInt(from, designOriginKey)
	toOrigin, toOK := c.designInt(to, designOriginKey)
	ms, msOK := c.designInt(from, designSlidePrefix+sub)
	if !fromOK || !toOK || !msOK || ms < 0 {
		return false
	}
	total := time.Duration(ms) * time.Millisecond
	previous, previousPair := c.slideSpeaker, c.slidePair
	previous.Active, previous.PlayOnce = previous.IdleBase, false
	previousPair.Active, previousPair.PlayOnce = previousPair.IdleBase, false
	c.Scene.Slide = SlideScene{Active: true, From: fromOrigin, To: toOrigin, Left: total, Total: total,
		Previous: previous, PreviousPair: previousPair, PreviousPaired: c.slidePaired, PreviousInFront: c.slideInFront}
	c.Scene.Speaker.Active = c.Scene.Speaker.IdleBase
	c.Scene.Pair.Active = c.Scene.Pair.IdleBase
	c.phase, c.timer = PhaseSlide, total
	return true
}

// designInt reads a whole number from the background design.ini.
func (c *Courtroom) designInt(section, key string) (int, bool) {
	raw, ok := c.design.GetSection(section, key)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	return n, err == nil
}

// playEmote shows the message's emote once any shout and slide are over: the preanim, or straight to talking.
func (c *Courtroom) playEmote() {
	msg := c.current
	// The SFX arms here (play_preanim, courtroom.cpp:4054); effects wait for start_chat_ticking
	// (courtroom.cpp:4154-4172), reached through preanim_done (courtroom.cpp:4108-4118, :3506). See startTalking.
	if preanimSFXPlays(msg) {
		c.armSFXDelay(msg)
	}
	// preanimDone here means NotifyAssetMissing landed during the shout; a 404'd preanim would draw a blank speaker.
	playPre := preanimWillPlay(msg) && !c.preanimDone && !c.spriteConfirmedMissing(c.Scene.Speaker.PreanimBase)
	blockOnPre := playPre && !msg.Immediate &&
		(msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom)

	if playPre {
		c.Scene.Speaker.Active = c.Scene.Speaker.PreanimBase
		c.Scene.Speaker.PlayOnce = true
	}
	if blockOnPre {
		// A blocking preanim applies the preanim desk column (courtroom.cpp:4075-4091); immediate ones get the talk's.
		c.applyDeskMods(true)
		c.phase = PhasePreanim
		c.timer = c.PreanimTimeout // power-user knob; seeded to DefaultPreanimTimeout
		return
	}
	c.startTalking()
}

// preanimSFXPlays gates sfx_delay_timer, whose start at courtroom.cpp:4054 precedes the file_exists at :4056,
// so a preanim with missing art still plays the sound.
func preanimSFXPlays(msg *protocol.ChatMessage) bool {
	if msg.EmoteMod == protocol.EmoteModPreanim ||
		msg.EmoteMod == protocol.EmoteModPreanimZoom ||
		msg.Immediate {
		return true
	}
	// A non-silent SFX_NAME on IDLE or ZOOM is hand-picked, so play it; silent sentinels stay silent.
	return msg.SFXName != "" && msg.SFXName != "0" && msg.SFXName != "1"
}

// sfxBaseFor resolves the emote SFX base, "" for no sound. Armed for SFX_DELAY x 40ms (courtroom.cpp:4052-4054);
// play_sfx shakes before the sfx_name=="1" return (courtroom.cpp:4593-4596), so silent preanims still shake.
func (c *Courtroom) sfxBaseFor(msg *protocol.ChatMessage) string {
	if msg.SFXName == "" || msg.SFXName == "0" || msg.SFXName == "1" {
		return ""
	}
	if c.Blips != nil && c.Blips.SFXMuted(msg.SFXName) {
		return ""
	}
	return c.urls.SFX(msg.SFXName)
}

func (c *Courtroom) armSFXDelay(msg *protocol.ChatMessage) {
	base := c.sfxBaseFor(msg)
	// Preanim shake (play_sfx) on SCREENSHAKE=1; effectsVisible gates the visual.
	preanimMod := msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom
	shake := msg.Screenshake && preanimMod && c.effectsVisible()
	if base == "" && !shake {
		return // nothing to schedule
	}
	// Warm now so a first play adds no fetch latency on top of SFX_DELAY.
	if base != "" && c.mgr != nil {
		c.mgr.Prefetch(base, assets.AssetTypeSFX, network.PriorityHigh) // AssetType: SFX
	}
	c.sfxArmed = true
	c.sfxLeft = time.Duration(msg.SFXDelay) * sfxDelayUnit
	c.sfxBase = base
	c.sfxShake = shake
}

// fireSFXDelay plays the armed SFX and shake, then disarms.
func (c *Courtroom) fireSFXDelay() {
	if c.sfxBase != "" {
		c.audio.PlaySFX(c.sfxBase, 0) // AssetType: SFX (deadline reached; delay already elapsed)
	}
	if c.sfxShake {
		c.Scene.ShakeLeft = ScreenshakeDuration
	}
	c.sfxArmed, c.sfxLeft, c.sfxBase, c.sfxShake = false, 0, "", false
}

// fireMessageEffects follows start_chat_ticking (courtroom.cpp:4154): Effects suppresses realization (:4173-4177).
func (c *Courtroom) fireMessageEffects(msg *protocol.ChatMessage) {
	// Reduce-motion gates only the visuals; sounds still play.
	if msg.Effects != "" {
		fx, folder, sound := parseEffectsField(msg.Effects)
		if folder == "" {
			// 1- and 2-part fields omit it: use the sender's char.ini (text_file_functions.cpp:836-839).
			folder = c.OverlayEffectsFolderFor(msg.CharName)
		}
		// One helper, so a resolve that had to wait settles the message identically.
		c.applyEffectField(fx, folder, msg.Flip, msg.SelfOffsetX, msg.SelfOffsetY)
		// The sound plays even if art is unresolved or visuals are off (courtroom.cpp:3196 precedes :3202).
		if OverlaySoundPlayable(sound) {
			c.audio.PlaySFX(c.urls.SFX(sound), 0) // AssetType: SFX (2.8 effect sound)
		}
	} else if msg.Realization {
		if c.effectsVisible() {
			c.Scene.FlashLeft = RealizationFlashDuration
		}
		// The sender's realization wins over the theme's (courtroom.cpp:4175, text_file_functions.cpp:896-903).
		if sfx := c.realizationSFXFor(msg.CharName); sfx != "" {
			c.audio.PlaySFX(sfx, 0) // AssetType: SFX (realization)
		}
	}
	if c.effectsVisible() && msg.Screenshake && (msg.EmoteMod == protocol.EmoteModIdle || msg.EmoteMod == protocol.EmoteModZoom) {
		c.Scene.ShakeLeft = ScreenshakeDuration
	}
}

// effectsVisible gates shake and flash; effect sounds always play.
func (c *Courtroom) effectsVisible() bool { return c.ScreenEffects && !c.ReduceMotion }

// fireInlineEffect fires a \s or \f the crawl reached, gated like fireMessageEffects.
func (c *Courtroom) fireInlineEffect(m EffectMark) {
	if !c.effectsVisible() {
		return
	}
	switch m.Kind {
	case EffectShake:
		c.Scene.ShakeLeft = ScreenshakeDuration
	case EffectFlash:
		c.Scene.FlashLeft = RealizationFlashDuration
	}
}

// startTalking is AO2's start_chat_ticking (courtroom.cpp:4120-4261).
func (c *Courtroom) startTalking() {
	// Talk/idle desk column (courtroom.cpp:4134-4152), restoring what a preanim's mod-4 hide zeroed.
	c.applyDeskMods(false)
	// EFFECTS fires here, not at message start: do_effect is at courtroom.cpp:4154-4172, after preanim_done.
	if c.current != nil {
		c.fireMessageEffects(c.current)
	}
	// MessageRaw is the concatenation, so the raster cache keys on the full text.
	if c.additivePrefix != "" {
		c.Typewriter.StartAppend(c.additivePrefix, c.currentText)
	} else {
		c.Typewriter.Start(c.currentText) // marker-stripped (sprite style decoded out in begin)
	}
	c.Scene.MessageText = c.Typewriter.Text()
	c.Scene.MessageRaw = c.additivePrefix + c.currentText
	c.Scene.MessageStyles = append(c.Scene.MessageStyles[:0], c.Typewriter.Styles()...) // copy: Start reuses its slice
	c.Scene.MessageEffects = append(c.Scene.MessageEffects[:0], c.pendingEffects...)    // #M5 spans (shifted past the additive prefix in begin)
	c.Scene.VisibleRunes = c.Typewriter.Visible()                                       // additive pre-reveals the prefix; 0 otherwise
	if !c.Scene.Speaker.PlayOnce {
		c.Scene.Speaker.Active = c.Scene.Speaker.TalkBase
	} else {
		// Immediate mode: the preanim has its own countdown so the message can finish; bounded so it can't park Active.
		c.imPreLeft = c.PreanimTimeout
	}
	// Skip talking only with no text and no marks: PhaseTalking alone drains NextEffect, and "\f" strips to zero runes.
	// AO2 tests the raw text (courtroom.cpp:4183), so chat_tick calls do_flash (courtroom.cpp:4442-4448).
	if c.Typewriter.Done() && !c.Typewriter.EffectsPending() {
		c.enterLinger()
		return
	}
	c.phase = PhaseTalking
}

// enterLinger starts TextStay, leaving an immediate preanim (anim_state 4) to tickImmediatePreanim.
func (c *Courtroom) enterLinger() {
	if c.imPreLeft <= 0 {
		c.Scene.Speaker.Active = c.Scene.Speaker.IdleBase
		c.endPreanim(PreanimEndTextSettled)
	}
	c.phase = PhaseLinger
	c.timer = c.TextStay
}

// tickImmediatePreanim ends the decoration; it runs outside the switch because AO2 lets the layer outlive text_state 2.
func (c *Courtroom) tickImmediatePreanim(dt time.Duration) {
	if c.imPreLeft <= 0 {
		return
	}
	c.imPreLeft -= dt
	if !c.preanimDone && c.imPreLeft > 0 {
		return
	}
	c.imPreLeft = 0
	if !c.Scene.Speaker.PlayOnce {
		return // already released (NotifyAssetMissing, a new message, a wipe)
	}
	// preanimDone means the render reported the last frame; otherwise the bound ran out.
	if c.preanimDone {
		c.endPreanim(PreanimEndFinished)
	} else {
		c.endPreanim(PreanimEndTimeout)
	}
	if c.phase == PhaseTalking {
		// Text still crawling: flap the talk sprite for the rest.
		c.Scene.Speaker.Active = c.Scene.Speaker.TalkBase
		return
	}
	c.Scene.Speaker.Active = c.Scene.Speaker.IdleBase
}

// NotifyPreanimDone reports a finished one-shot from the render side; PlayOnce shows this room selected it.
func (c *Courtroom) NotifyPreanimDone() {
	if !c.Scene.Speaker.PlayOnce {
		return // no one-shot on stage here — a stale report from a shared viewport
	}
	c.preanimDone = true
}

// NotifyPreanimStarted extends the phase timer to the real duration, as PreanimTimeout is only a fallback.
// Extend-only and phase-guarded, so a stale callback from a shared viewport is a no-op.
func (c *Courtroom) NotifyPreanimStarted(total time.Duration) {
	want := total + preanimPlaybackSlack
	// Extend imPreLeft, not the phase timer, which is by then the linger clock; lengthening it would stall the queue.
	if c.imPreLeft > 0 {
		if want > c.imPreLeft {
			c.imPreLeft = want
		}
		return
	}
	// PhasePreanim is a blocking wait: the phase timer is the bound, and a long preanim must play in full.
	if c.phase != PhasePreanim {
		return
	}
	if want > c.timer {
		c.timer = want
	}
}

// NotifyAssetMissing reports every spelling and format of base 404'd; only the current message's preanim reacts.
func (c *Courtroom) NotifyAssetMissing(base string) {
	if base == "" {
		return
	}
	// Record first: the gate holds a message before it is current, and the guard below would drop this 404.
	c.recordMissing(base)
	if c.current == nil || base != c.Scene.Speaker.PreanimBase {
		return
	}
	// As NotifyPreanimDone: PhasePreanim exits next Update, and enterAfterShout reads the flag too.
	c.preanimDone = true
	// Move Active off the missing base now: draws can skip this room's Update (audio-paced present, restored window,
	// split or replay rooms), which would flash a placeholder for one frame.
	if c.Scene.Speaker.PlayOnce {
		c.endPreanim(PreanimEndMissing)
		c.imPreLeft = 0 // an immediate-mode decoration that can never play
		if c.phase == PhaseTalking || c.phase == PhasePreanim {
			c.Scene.Speaker.Active = c.Scene.Speaker.TalkBase
		}
		// Mirror PhasePreanim's exit so this frame matches the deferred one for all desk mods; idempotent.
		if c.phase == PhasePreanim {
			c.applyDeskMods(false)
		}
	}
}

// recordMissing remembers a 404'd base for the wait gate. Bounded; game thread only.
func (c *Courtroom) recordMissing(base string) {
	if c.missingSprites == nil {
		c.missingSprites = make(map[string]struct{})
	}
	if _, ok := c.missingSprites[base]; ok {
		return
	}
	if len(c.missingSprites) >= missingSpritesCap {
		return // full: the SpriteWaitTimeout still bounds any un-recorded miss
	}
	c.missingSprites[base] = struct{}{}
}

// spriteConfirmedMissing reports a base 404'd this session, so the gate releases on the miss.
func (c *Courtroom) spriteConfirmedMissing(base string) bool {
	_, ok := c.missingSprites[base]
	return ok
}

// SkipToIdle fast-forwards the current message to idle, so a replay "next" can feed the following event at once.
func (c *Courtroom) SkipToIdle() {
	const bigStep = time.Hour // collapse any phase timer in a single Update
	for i := 0; i < 8 && c.phase != PhaseIdle; i++ {
		c.Typewriter.SkipToEnd()
		c.preanimDone = true
		c.Update(bigStep)
	}
}

// RestoreMessage re-stages a message settled, for a rebuilt room. Audio goes to NopAudio, as sounds must not replay;
// catch-up is bypassed because it leaves the sprite as-is; one-shot effects are cleared.
func (c *Courtroom) RestoreMessage(msg *protocol.ChatMessage) {
	if msg == nil {
		return
	}
	savedAudio, savedCatchUp := c.audio, c.CatchUp
	c.audio, c.CatchUp, c.restoring = NopAudio{}, false, true
	c.begin(msg)
	c.SkipToIdle()
	c.audio, c.CatchUp, c.restoring = savedAudio, savedCatchUp, false
	// Clear explicitly so no-replayed-effects doesn't rely on Update's countdown ordering.
	c.Scene.FlashLeft, c.Scene.ShakeLeft = 0, 0
	// The idle loop runs on the real audio sink, so an idle trigger would replay sound or flash on tab-back.
	c.frameTriggers = frameTriggerTable{}
}

func (c *Courtroom) Update(dt time.Duration) {
	c.readDesign()
	c.followBackground()
	// Effect countdowns run independent of the phase machine.
	if c.Scene.FlashLeft > 0 {
		c.Scene.FlashLeft -= dt
		if c.Scene.FlashLeft < 0 {
			c.Scene.FlashLeft = 0
		}
	}
	if c.Scene.ShakeLeft > 0 {
		c.Scene.ShakeLeft -= dt
		if c.Scene.ShakeLeft < 0 {
			c.Scene.ShakeLeft = 0
		}
	}
	// A screen effect whose roster was resolving re-asks here.
	c.tickOverlayPending(dt)
	// Independent of the phase machine, so a delay outlasting the preanim lands during talk, as sfx_delay_timer does.
	if c.sfxArmed {
		c.sfxLeft -= dt
		if c.sfxLeft <= 0 {
			c.fireSFXDelay()
		}
	}
	// An immediate preanim advances outside the switch so it plays across linger, as AO2 leaves anim_state at 4.
	c.tickImmediatePreanim(dt)
	switch c.phase {
	case PhaseShout:
		c.timer -= dt
		if c.timer <= 0 {
			c.enterAfterShout()
		}

	case PhaseSlide:
		c.timer -= dt
		c.Scene.Slide.Left = max(c.timer, 0)
		if c.timer <= 0 {
			c.Scene.Slide = SlideScene{}
			c.playEmote()
		}

	case PhasePreanim:
		c.timer -= dt
		if c.preanimDone || c.timer <= 0 {
			// TIMEOUT on a decoded preanim means the bound cut a real animation short; FINISHED means it played out.
			if c.preanimDone {
				c.endPreanim(PreanimEndFinished)
			} else {
				c.endPreanim(PreanimEndTimeout)
			}
			c.startTalking()
		}

	case PhaseTalking:
		_, blips := c.Typewriter.Update(dt)
		c.Scene.VisibleRunes = c.Typewriter.Visible()
		// Inline codes fire as the crawl reaches them (chat_tick); a skip drops any unreached.
		for {
			m, ok := c.Typewriter.NextEffect()
			if !ok {
				break
			}
			c.fireInlineEffect(m)
		}
		if c.blipPending {
			c.resolveBlip() // the speaker's char.ini may have landed since begin()
		}
		for i := 0; i < blips; i++ {
			if c.blipRef.Base != "" { // held while in flight — never the wrong set
				c.audio.PlayBlip(c.blipRef.Base) // AssetType: Blip
			}
		}
		// Text completion ends occupancy: AO2 starts text_queue_timer here (courtroom.cpp:4332-4338, :4208-4213).
		if c.Typewriter.Done() {
			c.enterLinger()
		}

	case PhaseLinger:
		c.timer -= dt
		if c.timer <= 0 {
			c.phase = PhaseIdle
			// A cold next sprite holds in the queue; with the gate off the next message dequeues this tick.
			if len(c.queue) > 0 && c.waitHolds(c.queue[0], len(c.queue)-1, 0) {
				break
			}
			c.dequeue()
		}

	case PhaseIdle:
		// Only the wait gate leaves a message queued while idle, so this is a no-op without a hold.
		if len(c.queue) > 0 && !c.waitHolds(c.queue[0], len(c.queue)-1, dt) {
			c.dequeue()
		}
	}
}

func (c *Courtroom) dequeue() {
	if len(c.queue) == 0 {
		return
	}
	next := c.queue[0]
	copy(c.queue, c.queue[1:])
	c.queue = c.queue[:len(c.queue)-1]
	c.begin(next)
}

// hasPreanim mirrors webAO's emptiness checks.
func hasPreanim(msg *protocol.ChatMessage) bool {
	return msg.PreEmote != "" && msg.PreEmote != emptyPreanimDash
}

// spriteAlts is URLBuilder.EmoteAlts through the room's builder.
func (c *Courtroom) spriteAlts(character, emote string, kind EmoteKind) []string {
	return c.urls.EmoteAlts(character, emote, kind)
}

// deskVisible is AO2's two desk_mod switches (courtroom.cpp:4075-4091 preanim, :4134-4152 talk).
func deskVisible(deskMod int, preanim bool) bool {
	switch deskMod {
	case protocol.DeskShow:
		return true
	case protocol.DeskHide:
		return false
	case protocol.DeskPreOnly, protocol.DeskPreOnlyEx:
		return preanim // desk only during the preanim
	case protocol.DeskEmoteOnly, protocol.DeskEmoteOnlyEx:
		return !preanim // desk only during talk/idle
	default:
		return true
	}
}

// DeskResolution is what we know of the desk; AO2 uses file_exists (courtroom.cpp:4628), and we learn asynchronously.
type DeskResolution int

const (
	// DeskUnresolved: neither landed nor absent; held so a real desk never blinks off mid-load.
	DeskUnresolved DeskResolution = iota
	// DeskResolved: the desk for this background and position is resident.
	DeskResolved
	// DeskAbsent: every format 404'd (reportMissing, then NotifyDeskMissing).
	DeskAbsent
)

// DeskDrawn is the desk decision: the desk_mod table and the streamed availability.
func DeskDrawn(deskMod int, preanim bool, res DeskResolution) bool {
	if res == DeskAbsent {
		return false
	}
	return deskVisible(deskMod, preanim)
}

// deskResolutionOf reports what this room knows of a desk base; only DeskAbsent changes DeskDrawn. Game thread only.
func (c *Courtroom) deskResolutionOf(base string) DeskResolution {
	if base == "" {
		return DeskUnresolved
	}
	if c.Assets != nil && c.Assets.SpriteReady(base) {
		delete(c.missingDesks, base)
		return DeskResolved
	}
	if _, ok := c.missingDesks[base]; ok {
		return DeskAbsent
	}
	return DeskUnresolved
}

func (c *Courtroom) deskResolution() DeskResolution {
	return c.deskResolutionOf(c.Scene.DeskBase)
}

// NotifyDeskMissing reports a desk 404 from the warning drain; the Scene.DeskBase compare ignores other rooms' bases.
func (c *Courtroom) NotifyDeskMissing(base string) {
	if base == "" {
		return
	}
	c.recordMissingDesk(base)
	// Re-derive now, as a settled message has no further phase edge; applyDeskMods is idempotent.
	if c.current != nil && base == c.Scene.DeskBase {
		c.applyDeskMods(c.phase == PhasePreanim)
	}
}

// recordMissingDesk remembers a 404'd desk base. Bounded; game thread only.
func (c *Courtroom) recordMissingDesk(base string) {
	if c.missingDesks == nil {
		c.missingDesks = make(map[string]struct{})
	}
	if _, ok := c.missingDesks[base]; ok {
		return
	}
	if len(c.missingDesks) >= missingDesksCap {
		return // full: the render-side release (Store.IsMissing) still covers the draw
	}
	c.missingDesks[base] = struct{}{}
}

// followBackground gives the scene the desk of the background name that loaded, which may be a fallback, as AO2's
// get_pos_path pairs them (path_functions.cpp:108-153); until the background is resident the likely desk stays.
func (c *Courtroom) followBackground() {
	bg := c.Scene.BackgroundBase
	file := c.sceneryFile(bg)
	if file == "" || (bg == c.followBg && file == c.followFile && c.Scene.DeskBase == c.followDesk) {
		return
	}
	desk := deskBeside(bg, file, c.design)
	c.followBg, c.followFile, c.followDesk = bg, file, desk
	c.placeScenery(fileStem(file))
	if desk == c.Scene.DeskBase {
		return
	}
	c.Scene.DeskBase = desk
	c.mgr.Prefetch(desk, assets.AssetTypeDeskOverlay, network.PriorityHigh) // AssetType: DeskOverlay
	if c.current != nil {
		c.applyDeskMods(c.phase == PhasePreanim)
	}
}

// deskBeside returns the desk in base's folder that AO2 pairs with the name of the file base loaded as.
func deskBeside(base, file string, design *theme.INI) string {
	return base[:strings.LastIndexByte(base, '/')+1] + DeskOf(fileStem(file), design)
}

// fileStem returns the last path segment of file without its extension: the background name it was asked as.
func fileStem(file string) string {
	name := file[strings.LastIndexByte(file, '/')+1:]
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		name = name[:dot]
	}
	return name
}

// placeScenery takes the background design.ini's geometry for the scene's position shown as name: stretch, scaling,
// and the origin AO2 reads from "court:<pos>" when name is the court and from "<pos>" otherwise
// (path_functions.cpp:86-103, animationlayer.cpp:614-616).
func (c *Courtroom) placeScenery(name string) {
	c.Scene.SceneryStretch, c.Scene.SceneryScaling, c.Scene.SceneryPanorama, c.Scene.SceneryOrigin = false, ScalingAuto, false, 0
	if c.design == nil {
		return
	}
	stretch, _ := c.design.Get(designStretchKey)
	scaling, _ := c.design.Get(designScalingKey)
	c.Scene.SceneryStretch = strings.HasPrefix(stretch, designTrue)
	c.Scene.SceneryScaling = ParseScalingMode(scaling)
	section := c.Scene.Position
	if name == courtPanorama {
		section = PanoramaPosition(section, c.design)
	}
	c.Scene.SceneryOrigin, c.Scene.SceneryPanorama = c.designInt(section, designOriginKey)
}

// sceneryFile returns the file base's resident page was decoded from, "" when unknown.
func (c *Courtroom) sceneryFile(base string) string {
	if c.Assets == nil || base == "" {
		return ""
	}
	return c.Assets.SceneryFile(base)
}

// readDesign takes the background's design.ini once it has been read and asks for the scenery again under it, as
// design.ini can put the position on the court panorama or give its desk.
func (c *Courtroom) readDesign() {
	if c.designRead || c.designURL == "" || c.Assets == nil {
		return
	}
	design, ok := c.Assets.BackgroundDesign(c.designURL)
	if !ok {
		return
	}
	c.design, c.designRead = design, true
	if design != nil && c.Scene.BackgroundBase != "" {
		c.followBg = ""
		c.setScenery(c.designBg, c.Scene.Position)
	}
}

// zoomEmote reports a mod that makes a solo shot, as AO2's zoom does (handle_ic_speaking).
func zoomEmote(mod int) bool {
	return mod == protocol.EmoteModZoom || mod == protocol.EmoteModPreanimZoom
}

// applyDeskMods recomputes from c.current, never Scene. Pair-hide and offset-zero are separate because AO2's EX modes
// differ: mod 5 hides and zeroes in talk only (courtroom.cpp:4085-4089, courtroom.cpp:4145-4146).
func (c *Courtroom) applyDeskMods(preanim bool) {
	if c.current == nil {
		return
	}
	msg := c.current
	zoom := zoomEmote(msg.EmoteMod)
	// A zoom shows the desk through the preanim (set_scene), hidden once talking (handle_ic_speaking).
	c.Scene.ShowDesk = DeskDrawn(msg.DeskMod, preanim, c.deskResolution()) && !(zoom && !preanim)
	// Mod 4 hides the pair in both phases, mod 5 in talk only, a zoom in both.
	hidePair := msg.DeskMod == protocol.DeskEmoteOnlyEx ||
		(!preanim && msg.DeskMod == protocol.DeskPreOnlyEx) || zoom
	c.Scene.PairActive = msg.Pair.Active() && !hidePair
	// Zeroed only where AO2 does move(0,0): mod 4 in preanim, mod 5 in talk and idle.
	zeroOffset := (preanim && msg.DeskMod == protocol.DeskEmoteOnlyEx) ||
		(!preanim && msg.DeskMod == protocol.DeskPreOnlyEx)
	if zeroOffset {
		c.Scene.Speaker.OffsetX, c.Scene.Speaker.OffsetY = 0, 0
	} else {
		c.Scene.Speaker.OffsetX, c.Scene.Speaker.OffsetY = msg.SelfOffsetX, msg.SelfOffsetY
	}
}

// displayName is the name plate, DisplayName bound to this room's settings; the IC log uses the same function.
func (c *Courtroom) displayName(msg *protocol.ChatMessage) string {
	var ini IniShowname
	if c.Chars != nil {
		ini = c.Chars.IniShownameFor
	}
	return DisplayName(msg, c.ForceCharNames, ini)
}
