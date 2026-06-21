package courtroom

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

const (
	// DefaultShoutDuration approximates the canonical AO objection bubble
	// length; the render side may finish earlier when the bubble animation
	// reports completion.
	DefaultShoutDuration = 724 * time.Millisecond
	// DefaultPreanimTimeout bounds a preanim wait when its real duration is
	// not yet known (asset still decoding); NotifyPreanimDone cuts it short.
	DefaultPreanimTimeout = 2500 * time.Millisecond
	// DefaultTextStayTime holds a finished message on screen before the
	// queue advances (AO2-Client text_stay_time flavor).
	DefaultTextStayTime = 200 * time.Millisecond
	// messageQueueCap bounds the IC message queue (spec §17.4).
	messageQueueCap = 64
	// blipVolumeFull is the unattenuated per-character blip scale (M11): 100%,
	// used when no BlipVolumeFor callback is wired (tests/embedders).
	blipVolumeFull = 100
	// emptyPreanim values AO uses for "no preanimation".
	emptyPreanimDash = "-"

	// catchUpDefaultThreshold is the queue depth at which packed-room catch-up
	// fast-forwards backlog messages — the floor (1) so the stage stays on the
	// newest message (a message catches up the moment one or more wait behind
	// it). The App overrides it from prefs; direct NewCourtroom callers
	// (tests/embedders) get catch-up OFF regardless.
	catchUpDefaultThreshold = 1
	// catchUpLinger holds a fast-forwarded backlog message on screen before
	// the next dequeues — zero so a deep backlog drains one message per frame
	// (the IC log keeps every message regardless; only the on-stage ceremony
	// is skipped, mirroring AO2-Client's "catch up when behind").
	catchUpLinger = 0 * time.Millisecond

	// RealizationFlashDuration approximates AO2-Client's realization flash
	// (do_flash plays the theme's one-shot flash animation, ~a quarter
	// second on stock themes).
	RealizationFlashDuration = 250 * time.Millisecond
	// ScreenshakeDuration approximates do_screenshake's elastic UI wobble.
	ScreenshakeDuration = 350 * time.Millisecond
)

// MessagePhase is the IC message lifecycle: shout → preanim → talking →
// linger → idle (AO2-Client handle_chatmessage ordering).
type MessagePhase int

const (
	PhaseIdle MessagePhase = iota
	PhaseShout
	PhasePreanim
	PhaseTalking
	PhaseLinger
)

// SpriteLayer describes one character layer for the renderer: which sprite
// base to show and how to place it. Bases are extension-less URL bases.
type SpriteLayer struct {
	// Name is the character folder this layer shows — the key client-side
	// position overrides attach to (drag-to-move in the viewport).
	Name        string
	IdleBase    string
	TalkBase    string
	PreanimBase string
	// Active selects which base renders right now.
	Active string
	// PlayOnce marks the active animation as one-shot (preanim).
	PlayOnce bool
	Flip     bool
	// OffsetX/Y are percent of viewport dimensions (−100..100).
	OffsetX, OffsetY int
	Visible          bool
	// Style is the speaker's transmitted sprite customization (recolour / glow /
	// opacity / motion), decoded from this message's text. Zero value = none; the
	// renderer leaves the blit byte-identical when it's inactive.
	Style SpriteStyle
}

// Scene is the renderer's entire input: plain data, no SDL types, mutated
// only by Courtroom on the game thread.
type Scene struct {
	Position       string
	BackgroundBase string
	DeskBase       string
	ShowDesk       bool

	Speaker        SpriteLayer
	Pair           SpriteLayer
	PairActive     bool
	SpeakerInFront bool

	ShoutBase string // active shout bubble base ("" = none)
	// ShoutFallbackBase is the default (misc/default) bubble the render draws
	// when the character ships no custom interjection art (most don't) —
	// AO2-Client falls back the same way. Prefetched alongside ShoutBase.
	ShoutFallbackBase string
	ShoutCustom       bool

	// MusicTrack is the currently-playing track (raw MC text; "" = nothing,
	// stopped, or an area transfer) — the courtroom Now-Playing display reads it.
	MusicTrack string

	// Chat box state.
	ShownameText string
	MessageText  string // full text (markup stripped); VisibleRunes gates the reveal
	VisibleRunes int
	TextColor    int
	// MessageStyles colors runs of MessageText (inline \cN markup). MessageRaw
	// is the pre-strip message — the raster cache keys on it, since two
	// differently-colored messages can share the same stripped MessageText.
	MessageStyles []StyleRun
	MessageRaw    string
	// IsBlankPost is set when the message's text is empty or whitespace-only
	// (an AO "blankpost": animate the sprite, say nothing). The UI hides the
	// whole chatbox — frame, showname and text — so only the sprite shows.
	// Decided in begin() from the raw message, so an animated blankpost never
	// flashes an empty box during its preanim.
	IsBlankPost bool

	// Full-viewport effects: Update counts these down, the renderer reads
	// the remainders (flash alpha, shake amplitude). Plain data — the
	// effect art pipeline stays render-side.
	FlashLeft time.Duration
	ShakeLeft time.Duration
}

// AudioSink receives playback triggers; the SDL_mixer implementation lives
// render-side, tests use a recorder.
type AudioSink interface {
	// PlayShout plays a character's shout cry (base lacks extension).
	PlayShout(base string)
	// PlaySFX plays an emote sound effect after delay.
	PlaySFX(base string, delay time.Duration)
	// PlayBlip fires one chat blip.
	PlayBlip(base string)
	// SetBlipScale sets the current speaker's per-character blip attenuation
	// (0–100, 100 = none; M11), applied to subsequent blips.
	SetBlipScale(pct int)
	// PlayMusic streams a track from a full URL.
	PlayMusic(url string)
	// StopMusic halts playback now (the ~stop sentinel; also on disconnect).
	StopMusic()
}

// NopAudio discards all triggers (headless tests, muted client).
type NopAudio struct{}

func (NopAudio) PlayShout(string)              {}
func (NopAudio) PlaySFX(string, time.Duration) {}
func (NopAudio) PlayBlip(string)               {}
func (NopAudio) SetBlipScale(int)              {}
func (NopAudio) PlayMusic(string)              {}
func (NopAudio) StopMusic()                    {}

// Courtroom drives the courtroom state machine: it consumes session events,
// prefetches every asset a message needs, and advances Scene each tick. No
// SDL calls anywhere in this type (spec §17.1).
type Courtroom struct {
	urls  URLBuilder
	mgr   *assets.Manager
	sess  *Session
	audio AudioSink

	Scene      Scene
	Typewriter Typewriter
	// TextStay holds a finished message before the queue advances
	// (user-tunable; AO2-Client "text stay time").
	TextStay time.Duration

	// CatchUp fast-forwards the on-stage ceremony (shout/preanim/typewriter/
	// stay) of backlog messages once the queue is deeper than CatchUpThreshold,
	// so a packed room tracks near real-time instead of crawling through every
	// preanim. The IC log still records every message. Off by default here; the
	// App turns it on from prefs.
	CatchUp          bool
	CatchUpThreshold int

	// ReduceMotion suppresses the jarring visual effects (screen shake,
	// realization flash) — accessibility. Effect SOUNDS still play; only the
	// motion is skipped. Set by the App from prefs (default off). It also
	// suppresses a RECEIVED sprite style's wobble/spin (transmitted motion).
	ReduceMotion bool

	// HideSpriteStyles ignores other speakers' transmitted SpriteStyle entirely
	// (every character renders normally) — a viewer off-switch. Set by the App
	// from prefs; default off (zero value) = show received styles.
	HideSpriteStyles bool

	// ForceCharNames shows every speaker's CHARACTER name instead of their
	// custom showname (true-roleplay / anti-impersonation for casing). Set by
	// the App from prefs (default off); the IC log mirrors it App-side.
	ForceCharNames bool

	// SFXMuted, when set, reports whether an emote SFX name should be silenced
	// (M11 per-SFX mute). Set by the App to read live prefs; nil = play everything.
	SFXMuted func(name string) bool

	// BlipVolumeFor, when set, returns the per-character blip attenuation
	// (0–100, 100 = no attenuation; M11 per-character blip volume) for a
	// character folder name. Set by the App to read live prefs; nil = full.
	BlipVolumeFor func(char string) int

	queue []*protocol.ChatMessage
	phase MessagePhase
	timer time.Duration

	current *protocol.ChatMessage
	// currentText is current.Message with any transmitted SpriteStyle marker
	// decoded out — the visible-only text the typewriter/blankpost use. The raw
	// current.Message keeps the marker (recordings share that pointer, so replays
	// re-decode the style); we never mutate it.
	currentText string
	blipBase    string

	// Predictor warms the predicted next speaker's sprite (optional).
	Predictor *assets.Prefetcher

	// preanimDone is flipped by NotifyPreanimDone (render reports one-shot
	// animation completion).
	preanimDone bool

	// ShoutUses tracks whether the per-character bubble resolved; the
	// renderer falls back to the default bubble base if not.
	ShoutCharBase    string
	ShoutDefaultBase string

	// RealizationSFX is the full URL base of the realization sound (theme
	// courtroom_sounds.ini "realization", resolved UI-side where the theme
	// lives; "" = silent). Played when a message carries REALIZATION=1
	// without a 2.8 Effects override.
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
		TextStay:   DefaultTextStayTime,
		// Catch-up defaults OFF so direct callers (tests/embedders) keep the
		// full lifecycle; the App enables it from prefs (default ON there).
		CatchUpThreshold: catchUpDefaultThreshold,
	}
	c.Scene.SpeakerInFront = true
	return c
}

// Phase exposes the current message phase.
func (c *Courtroom) Phase() MessagePhase { return c.phase }

// QueueLen exposes the pending message count.
func (c *Courtroom) QueueLen() int { return len(c.queue) }

// HandleEvent consumes a session event.
func (c *Courtroom) HandleEvent(ev Event) {
	switch ev.Kind {
	case EventMessage:
		c.enqueue(ev.Message)
	case EventBackground:
		c.setBackground(ev.Text)
	case EventMusic:
		switch {
		case ev.Text == "" || isAreaTransfer(ev.Text):
			// empty, or an area-name transfer (unified music/area list) — not a song
		case isMusicStop(ev.Text):
			// The ~stop sentinel isn't a real track: halt now instead of trying to
			// fetch+play it (PlayMusic is async, so a 404 would leave music running).
			c.audio.StopMusic()
			c.Scene.MusicTrack = "" // clear Now-Playing
		default:
			c.audio.PlayMusic(c.urls.MusicURL(ev.Text)) // AssetType: Music
			c.Scene.MusicTrack = ev.Text
		}
	}
}

// isMusicStop reports whether a track is an AO "stop music" sentinel (the fake
// ~stop track AO2-Client sends) rather than a real song — Now-Playing clears.
func isMusicStop(track string) bool {
	return strings.HasPrefix(strings.ToLower(track), "~stop")
}

// MusicAction classifies an MC track for the IC "has played a song" log line,
// mirroring AO2-Client handle_song: a real song → ("has played a song",
// <clean name>, true); the ~stop sentinel → ("has stopped the music", "", true);
// an area-name transfer → ("", "", false), i.e. not a song, don't log.
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
	return "has played a song", cleanSongName(track), true
}

// cleanSongName is the song's display name — AO2's QUrl(f_song).fileName() minus
// the extension: drop any URL query/fragment, the directory, then the extension.
func cleanSongName(track string) string {
	s := track
	if i := strings.IndexAny(s, "?#"); i >= 0 { // a Discord CDN /play link's signed query
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndexAny(s, `/\`); i >= 0 { // basename
		s = s[i+1:]
	}
	if i := strings.LastIndexByte(s, '.'); i > 0 { // strip the extension
		s = s[:i]
	}
	return s
}

// isAreaTransfer filters MC packets that carry area names (server-relative, no
// audio ext) from real music. A full http(s):// URL is ALWAYS music: its audio
// extension can sit before a query string (a Discord CDN /play link ends in a
// signed ?ex=&is=&hm=& suffix), so the trailing-extension check alone would
// misread it as an area name and silently swallow the /play. Area names are
// never URLs, so the URL test is exact.
func isAreaTransfer(track string) bool {
	if isMusicURL(track) {
		return false
	}
	return !hasAudioExt(track)
}

// enqueue mirrors AO2-Client chatmessage_enqueue: shouts nuke the queue and
// play immediately; otherwise messages process in order.
func (c *Courtroom) enqueue(msg *protocol.ChatMessage) {
	if msg == nil {
		return
	}
	if msg.IsShout() {
		c.queue = c.queue[:0]
		c.begin(msg)
		return
	}
	if c.phase == PhaseIdle && len(c.queue) == 0 {
		c.begin(msg)
		return
	}
	if len(c.queue) < messageQueueCap {
		c.queue = append(c.queue, msg)
	}
	// Beyond the cap the oldest unplayed message drops — bounded queues
	// beat unbounded lag (rule §17.4); IC history still records via logs.
	if len(c.queue) == messageQueueCap {
		copy(c.queue, c.queue[1:])
		c.queue = c.queue[:messageQueueCap-1]
		c.queue = append(c.queue, msg)
	}
}

// setBackground prefetches the new room's scenery for the current position.
// AssetType: Background
func (c *Courtroom) setBackground(bg string) {
	bgPart, deskPart := PositionScene(c.Scene.Position)
	c.Scene.BackgroundBase = c.urls.Background(bg, bgPart)
	c.Scene.DeskBase = c.urls.Background(bg, deskPart)
	c.mgr.Prefetch(c.Scene.BackgroundBase, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	c.mgr.Prefetch(c.Scene.DeskBase, assets.AssetTypeDeskOverlay, network.PriorityHigh)      // AssetType: DeskOverlay
}

// begin starts one message: prefetch everything in parallel at HIGH priority
// (speaker and pair resolve concurrently — the §11 wall-clock gate), then
// enter the first phase.
func (c *Courtroom) begin(msg *protocol.ChatMessage) {
	c.current = msg
	// Packed-room catch-up: a backlog behind this message means the stage is
	// behind real-time. Fast-forward this one (no shout/preanim/typewriter/
	// effects/sfx/prefetch) and linger briefly so the queue drains. The trigger
	// is "threshold or more waiting behind it" — at the default of 1, a message
	// plays in full only when nothing is queued behind it (so calm back-and-forth
	// still plays every line; only a genuine pile-up flashes past). The IC log
	// already holds every message's full text.
	if c.CatchUp && len(c.queue) >= c.CatchUpThreshold {
		c.beginCaughtUp(msg)
		return
	}
	// Decode this speaker's transmitted sprite style out of the text (an invisible
	// zero-width marker). currentText is the visible-only text the typewriter and
	// blankpost test use; we never mutate msg.Message (the recording shares that
	// pointer, so a replay re-decodes the same marker).
	style, clean := DecodeSpriteStyle(msg.Message)
	c.currentText = clean
	if c.HideSpriteStyles {
		style = SpriteStyle{} // viewer opted out of others' styles
	} else if c.ReduceMotion {
		style.Wobble, style.Spin = false, false // accessibility: drop transmitted motion
	}
	speakerName := msg.CharName

	// --- prefetch fan-out (all HIGH, all parallel on the pool) ---
	// Idle/talk fall back to the bare (unprefixed) file name — packs ship
	// either spelling (AO2-Client CharLayer::load_image pathlist).
	idle := c.urls.Emote(speakerName, msg.Emote, EmoteIdle)
	talk := c.urls.Emote(speakerName, msg.Emote, EmoteTalk)
	bare := c.urls.EmoteBare(speakerName, msg.Emote)
	c.mgr.PrefetchWithFallback(idle, bare, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	c.mgr.PrefetchWithFallback(talk, bare, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite

	pre := ""
	if hasPreanim(msg) {
		pre = c.urls.Emote(speakerName, msg.PreEmote, EmotePreanim)
		c.mgr.Prefetch(pre, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	}

	pairIdle := ""
	if msg.Pair.Active() {
		pairIdle = c.urls.Emote(msg.Pair.Name, msg.Pair.Emote, EmoteIdle)
		pairBare := c.urls.EmoteBare(msg.Pair.Name, msg.Pair.Emote)
		c.mgr.PrefetchWithFallback(pairIdle, pairBare, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (pair partner)
	}

	if msg.IsShout() {
		shout := ShoutName(msg.Objection)
		custom := msg.Objection == protocol.ShoutCustom
		c.ShoutCharBase = c.urls.ShoutBubble(speakerName, shout, custom)
		shoutSFX := c.urls.ShoutSFX(speakerName, shout)
		if custom && msg.CustomShout != "" {
			// 2.10 named interjection: art and sound live under
			// custom_objections/<name> (courtroom.cpp objection_custom).
			c.ShoutCharBase = c.urls.NamedCustomShout(speakerName, msg.CustomShout)
			shoutSFX = c.ShoutCharBase
		}
		c.ShoutDefaultBase = ""
		if !custom {
			c.ShoutDefaultBase = c.urls.DefaultShoutBubble(shout)
			c.mgr.Prefetch(c.ShoutDefaultBase, assets.AssetTypeShoutBubble, network.PriorityHigh) // AssetType: ShoutBubble
		}
		c.mgr.Prefetch(c.ShoutCharBase, assets.AssetTypeShoutBubble, network.PriorityHigh) // AssetType: ShoutBubble
		c.audio.PlayShout(shoutSFX)                                                        // AssetType: SFX
	}

	// Predictive prefetch: warm the likely next speaker — and their likely
	// next emote (§10 step 3 + per-character emote chains).
	if c.Predictor != nil {
		c.Predictor.OnMessage(speakerName, msg.Pair.Name, msg.Emote)
	}

	blip := msg.Blipname
	if blip == "" {
		blip = "male" // AO default blip set
	}
	c.blipBase = c.urls.Blip(blip)
	c.mgr.Prefetch(c.blipBase, assets.AssetTypeBlip, network.PriorityHigh) // AssetType: Blip

	// M11 per-character blip volume: attenuate this speaker's blips by their
	// stored scale. One int-set per message — the render loop is untouched.
	blipScale := blipVolumeFull
	if c.BlipVolumeFor != nil {
		blipScale = c.BlipVolumeFor(speakerName)
	}
	c.audio.SetBlipScale(blipScale)

	if msg.SFXName != "" && msg.SFXName != "0" && msg.SFXName != "1" &&
		(c.SFXMuted == nil || !c.SFXMuted(msg.SFXName)) { // M11: per-SFX mute
		c.audio.PlaySFX(c.urls.SFX(msg.SFXName), time.Duration(msg.SFXDelay)*time.Millisecond) // AssetType: SFX
	}

	// --- scene state ---
	c.Scene.Position = msg.Side
	if c.sess != nil && c.sess.Background != "" {
		bgPart, deskPart := PositionScene(msg.Side)
		c.Scene.BackgroundBase = c.urls.Background(c.sess.Background, bgPart)
		c.Scene.DeskBase = c.urls.Background(c.sess.Background, deskPart)
		// HIGH like every other live-message asset: this scenery is on
		// screen NOW. At low priority a busy lane shed these, and the
		// viewport had nothing to draw for the new position (the
		// "background goes black while talking" bug).
		c.mgr.Prefetch(c.Scene.BackgroundBase, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background
		c.mgr.Prefetch(c.Scene.DeskBase, assets.AssetTypeDeskOverlay, network.PriorityHigh)      // AssetType: DeskOverlay
	}
	c.Scene.ShowDesk = deskVisible(msg.DeskMod)

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
	}

	c.Scene.PairActive = msg.Pair.Active()
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
		}
	} else {
		c.Scene.Pair = SpriteLayer{}
	}

	c.Scene.ShownameText = c.displayName(msg)
	c.Scene.TextColor = msg.TextColor
	c.Scene.MessageText = ""
	c.Scene.MessageRaw = ""
	c.Scene.MessageStyles = c.Scene.MessageStyles[:0]
	c.Scene.VisibleRunes = 0
	// Blankpost decided up front from the raw text (StripChatMarkup is pinned
	// to the typewriter by TestStripMatchesTypewriter, so this matches what
	// would type out) — known from frame 1 so the box never flashes during an
	// animated blankpost's preanim.
	c.Scene.IsBlankPost = strings.TrimSpace(StripChatMarkup(c.currentText)) == ""
	c.preanimDone = false

	// --- phase entry ---
	switch {
	case msg.IsShout():
		c.Scene.ShoutBase = c.ShoutCharBase
		c.Scene.ShoutFallbackBase = c.ShoutDefaultBase // misc/default when the char has none
		c.Scene.ShoutCustom = msg.Objection == protocol.ShoutCustom
		c.phase = PhaseShout
		c.timer = DefaultShoutDuration
	default:
		c.enterAfterShout()
	}
}

// beginCaughtUp shows a backlog message's text for ~one frame with no
// shout/preanim/typewriter/effects/sfx/prefetch, then lingers briefly so the
// next dequeues. It drains a deep queue toward real-time; the newest message
// (played in full by begin) sets the real scene, and the IC log already has
// every message's text. Speaker sprite is intentionally left as-is — a
// one-frame backlog flash is never seen.
func (c *Courtroom) beginCaughtUp(msg *protocol.ChatMessage) {
	c.Scene.ShoutBase = ""
	c.Scene.ShoutFallbackBase = ""
	c.Scene.ShownameText = c.displayName(msg)
	c.Scene.TextColor = msg.TextColor
	_, c.currentText = DecodeSpriteStyle(msg.Message) // strip the style marker (catch-up never redraws the sprite)
	c.Typewriter.Start(c.currentText)
	c.Typewriter.SkipToEnd()
	c.Scene.MessageText = c.Typewriter.Text()
	c.Scene.MessageRaw = c.currentText
	c.Scene.MessageStyles = append(c.Scene.MessageStyles[:0], c.Typewriter.Styles()...)
	c.Scene.VisibleRunes = c.Typewriter.Visible()
	// Same blankpost rule as begin(); this path doesn't route through it.
	c.Scene.IsBlankPost = strings.TrimSpace(c.Scene.MessageText) == ""
	c.preanimDone = false
	c.phase = PhaseLinger
	c.timer = catchUpLinger
}

// enterAfterShout picks preanim vs talking, mirroring handle_emote_mod:
// preanim plays first unless absent; IDLE/ZOOM with immediate plays preanim
// alongside the text.
func (c *Courtroom) enterAfterShout() {
	c.Scene.ShoutBase = ""
	c.Scene.ShoutFallbackBase = ""
	msg := c.current
	c.fireMessageEffects(msg)
	playPre := hasPreanim(msg) &&
		(msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom || msg.Immediate)
	blockOnPre := playPre && !msg.Immediate &&
		(msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom)

	if playPre {
		c.Scene.Speaker.Active = c.Scene.Speaker.PreanimBase
		c.Scene.Speaker.PlayOnce = true
	}
	if blockOnPre {
		c.phase = PhasePreanim
		c.timer = DefaultPreanimTimeout
		return
	}
	c.startTalking()
}

// fireMessageEffects triggers the message-display effects exactly where
// AO2-Client's handle_ic_message does (courtroom.cpp:4154): the 2.8
// Effects field wins ("fx|sound" or "fx|folder|sound"), plain REALIZATION=1
// is the fallback flash, and SCREENSHAKE=1 shakes for IDLE/ZOOM emote mods.
// Named theme-overlay effects beyond flash/shake play their sound only —
// the overlay art needs the theme effects engine (frame-synced FRAME_*
// triggers live with the char.ini frame sections, not here).
func (c *Courtroom) fireMessageEffects(msg *protocol.ChatMessage) {
	// Reduce-motion gates the VISUAL effects only — the feedback sounds still
	// play (accessibility: kill the shake/flash, keep the audio cue).
	if msg.Effects != "" {
		fx, sound := parseEffectsField(msg.Effects)
		if !c.ReduceMotion {
			switch strings.ToLower(fx) {
			case "screenshake":
				c.Scene.ShakeLeft = ScreenshakeDuration
			case "flash", "realization", "realizationflash":
				c.Scene.FlashLeft = RealizationFlashDuration
			}
		}
		if sound != "" && sound != "-" {
			c.audio.PlaySFX(c.urls.SFX(sound), 0) // AssetType: SFX (2.8 effect sound)
		}
	} else if msg.Realization {
		if !c.ReduceMotion {
			c.Scene.FlashLeft = RealizationFlashDuration
		}
		if c.RealizationSFX != "" {
			c.audio.PlaySFX(c.RealizationSFX, 0) // AssetType: SFX (realization)
		}
	}
	if !c.ReduceMotion && msg.Screenshake && (msg.EmoteMod == protocol.EmoteModIdle || msg.EmoteMod == protocol.EmoteModZoom) {
		c.Scene.ShakeLeft = ScreenshakeDuration
	}
}

// parseEffectsField splits the 2.8 EFFECTS field: "fx", "fx|sound", or
// "fx|folder|sound" (the folder selects custom effect art — sound is
// always the last element, mirroring courtroom.cpp:4156).
func parseEffectsField(raw string) (fx, sound string) {
	parts := strings.Split(raw, "|")
	fx = parts[0]
	if len(parts) > 1 {
		sound = parts[len(parts)-1]
	}
	return fx, sound
}

// startTalking begins the typewriter reveal.
func (c *Courtroom) startTalking() {
	c.Typewriter.Start(c.currentText) // marker-stripped (sprite style decoded out in begin)
	c.Scene.MessageText = c.Typewriter.Text()
	c.Scene.MessageRaw = c.currentText
	c.Scene.MessageStyles = append(c.Scene.MessageStyles[:0], c.Typewriter.Styles()...) // copy: Start reuses its slice
	c.Scene.VisibleRunes = 0
	if !c.Scene.Speaker.PlayOnce {
		c.Scene.Speaker.Active = c.Scene.Speaker.TalkBase
	}
	if c.Typewriter.Done() { // blank post
		c.enterLinger()
		return
	}
	c.phase = PhaseTalking
}

func (c *Courtroom) enterLinger() {
	c.Scene.Speaker.Active = c.Scene.Speaker.IdleBase
	c.Scene.Speaker.PlayOnce = false
	c.phase = PhaseLinger
	c.timer = c.TextStay
}

// NotifyPreanimDone is called by the render side when a one-shot animation
// finishes (or by tests). It also flips the speaker to the talk loop.
func (c *Courtroom) NotifyPreanimDone() {
	c.preanimDone = true
}

// SkipToIdle fast-forwards the CURRENT message straight to idle: reveal the rest
// of the text, release a preanim that's waiting on the viewport callback, and
// collapse every timed phase (shout / preanim / linger) with a huge step. A
// replay/player "next" uses it so the following event can be fed immediately.
// The loop is bounded — shout→preanim→talking→linger→idle is ≤4 hops — and a
// queued message would begin via dequeue (replay feeds one at a time, so the
// queue is empty here).
func (c *Courtroom) SkipToIdle() {
	const bigStep = time.Hour // collapse any phase timer in a single Update
	for i := 0; i < 8 && c.phase != PhaseIdle; i++ {
		c.Typewriter.SkipToEnd()
		c.preanimDone = true
		c.Update(bigStep)
	}
}

// Update advances the message lifecycle by dt.
func (c *Courtroom) Update(dt time.Duration) {
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
	switch c.phase {
	case PhaseShout:
		c.timer -= dt
		if c.timer <= 0 {
			c.enterAfterShout()
		}

	case PhasePreanim:
		c.timer -= dt
		if c.preanimDone || c.timer <= 0 {
			c.Scene.Speaker.PlayOnce = false
			c.startTalking()
		}

	case PhaseTalking:
		_, blips := c.Typewriter.Update(dt)
		c.Scene.VisibleRunes = c.Typewriter.Visible()
		for i := 0; i < blips; i++ {
			c.audio.PlayBlip(c.blipBase) // AssetType: Blip
		}
		if c.Typewriter.Done() {
			c.enterLinger()
		}

	case PhaseLinger:
		c.timer -= dt
		if c.timer <= 0 {
			c.phase = PhaseIdle
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

// hasPreanim mirrors webAO's emptiness checks for the preanim field.
func hasPreanim(msg *protocol.ChatMessage) bool {
	return msg.PreEmote != "" && msg.PreEmote != emptyPreanimDash
}

// deskVisible collapses desk mods into "draw the desk" for the active
// message (EX modes refine per-phase later; AO2-Client semantics).
func deskVisible(deskMod int) bool {
	switch deskMod {
	case protocol.DeskHide, protocol.DeskPreOnly, protocol.DeskPreOnlyEx:
		return false
	default:
		return true
	}
}

// displayName picks the chat box name: showname overrides the folder name.
// displayName is the chatbox/log name for a message: the custom showname,
// falling back to the character name — UNLESS ForceCharNames is on, which
// always shows the character (ignoring custom shownames).
func (c *Courtroom) displayName(msg *protocol.ChatMessage) string {
	if !c.ForceCharNames && msg.Showname != "" {
		return msg.Showname
	}
	return msg.CharName
}
