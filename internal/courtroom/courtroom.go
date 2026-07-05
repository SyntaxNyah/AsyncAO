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
	// not yet known (asset still decoding); NotifyPreanimDone (the one-shot
	// finished) or NotifyAssetMissing (it can never start — conclusive 404)
	// cuts it short, and once the decoded preanim plays NotifyPreanimStarted
	// EXTENDS it to the real duration — so a live wait only ever runs this long
	// while the asset is genuinely still downloading, never as a cap on a long
	// but decoded preanimation (the "long preanims skip to the end" report).
	DefaultPreanimTimeout = 2500 * time.Millisecond
	// preanimPlaybackSlack pads the extended timeout past a decoded preanim's
	// real duration (last-frame delay + the one-shot done report's latency), so
	// the natural NotifyPreanimDone always wins the race, not the fallback.
	preanimPlaybackSlack = 250 * time.Millisecond
	// DefaultTextStayTime holds a finished message on screen before the
	// queue advances (AO2-Client text_stay_time flavor).
	DefaultTextStayTime = 200 * time.Millisecond
	// messageQueueCap bounds the IC message queue (spec §17.4).
	messageQueueCap = 64
	// DefaultQueueCap is the exported canonical queue depth (= messageQueueCap):
	// what QueueCap seeds to, and what the App restores when the power-user pref
	// is back at its 0 = default sentinel.
	DefaultQueueCap = messageQueueCap
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
	// ChatSkinBase is the speaker's per-character chatbox art (char.ini
	// chat=<misc> → misc/<misc>/chatbox), "" for the client's normal box. Set
	// per message in begin(); the ui draws it when the texture is resident.
	ChatSkinBase string
	// MessageStyles colors runs of MessageText (inline \cN markup). MessageRaw
	// is the pre-strip message — the raster cache keys on it, since two
	// differently-colored messages can share the same stripped MessageText.
	MessageStyles []StyleRun
	MessageRaw    string
	// MessageEffects tags spans of MessageText with an animated effect (#M5:
	// shake / wave / rainbow), decoded from the speaker's zero-width effects
	// frame. Rune indices into MessageText. Empty (the common case) → the plain
	// raster fast path; the UI maps these to render.EffectSpan only when present.
	MessageEffects []TextEffectSpan
	// Centered renders the chatbox text centre-aligned (the webAO "~~" prefix
	// convention). Set per message in begin; the "~~" marker is stripped from the
	// display text. Off (the common case) = the untouched left-aligned raster.
	Centered bool
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

	// BlipNameFor, when set, resolves a speaker's blip set from their char.ini
	// ([Options] blips / legacy gender) — the webAO-parity fallback for
	// messages whose wire Blipname field is empty (pre-2.10.2 senders, short
	// packets). The App answers from its per-URL char.ini cache and fires the
	// fetch on a miss; "" = unknown (the AO default set plays this message,
	// the speaker's next message picks the fetched value up).
	BlipNameFor func(char string) string

	// ChatSkinFor, when set, resolves a speaker's chatbox-skin misc folder
	// (char.ini [Options] chat, AO2-Client get_chat) — same cache/fetch
	// behaviour as BlipNameFor. "" = no per-character skin.
	ChatSkinFor func(char string) string

	// InlineEmote, when set, resolves a :shortcode: stem to its emoji (#18). begin()
	// substitutes known shortcodes in the speaker's text so the chatbox renders them like
	// the IC log; nil = leave the text as-is. Set by the App (the registry lives in ui).
	InlineEmote func(stem string) (string, bool)

	// Cold-load "wait" mode (the third SpriteLoadMode; client-AO flavour): hold a
	// message OFF-STAGE until its speaker's idle sprite has decoded, so the stage
	// never shows the message with a missing sprite. SpriteWait turns it on;
	// SpriteReady reports texture residency (the App wires render.TextureStore
	// .Contains — same-thread, a plain map probe; nil = gate off, so courtroom
	// stays SDL-free); SpriteWaitTimeout caps one message's hold so a 404 or a
	// decode failure can only ever DELAY a message, never hang the queue (a zero
	// timeout deliberately never holds — a wiring bug degrades to mode-off).
	// Shouts bypass (AO2 parity: they nuke the queue and play NOW) and packed-room
	// catch-up wins (a backlog never waits — beginCaughtUp doesn't redraw the
	// sprite anyway).
	SpriteWait        bool
	SpriteWaitTimeout time.Duration
	SpriteReady       func(base string) bool
	// SpriteWaitPair / SpriteWaitPreanim widen the gate (power-user strictness
	// knobs, both default off): also hold until the pair partner's idle sprite /
	// the message's preanimation have decoded. The timeout caps the whole hold
	// either way.
	SpriteWaitPair    bool
	SpriteWaitPreanim bool

	// ShoutDuration / PreanimTimeout are the core message-ceremony timings,
	// exposed as power-user knobs (defaults = the canonical AO2-flavoured
	// DefaultShoutDuration / DefaultPreanimTimeout, seeded in NewCourtroom):
	// how long an interjection bubble holds the stage, and how long a preanim
	// may play before the text starts anyway when its real length is unknown
	// (asset still decoding — NotifyPreanimDone / NotifyAssetMissing cut it
	// short the moment the animation finishes / proves unresolvable).
	ShoutDuration  time.Duration
	PreanimTimeout time.Duration

	// QueueCap bounds the IC message queue (power-user; seeded to
	// messageQueueCap in NewCourtroom and ALWAYS ≥ 1, so the queue stays
	// bounded whatever the pref says — §17.4). Past it the oldest unplayed
	// message drops; the IC log still records everything.
	QueueCap int
	// CatchUpLinger holds each fast-forwarded backlog message on screen before
	// the next dequeues (power-user; the canonical default is catchUpLinger =
	// zero — drain one per frame). A small value lets you actually read the
	// backlog flashing past, trading catch-up speed for legibility.
	CatchUpLinger time.Duration

	// waitLeft counts down an armed hold; waitFor is the exact message it was
	// armed for (pointer identity — queue entries are stable), so a new head
	// re-arms fresh and begin() clears it.
	waitLeft time.Duration
	waitFor  *protocol.ChatMessage

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
	// pendingEffects holds the current message's decoded animated-text spans (#M5),
	// set in begin() and copied onto Scene.MessageEffects when the text shows
	// (startTalking) — effects are per-message content, never recalled.
	pendingEffects []TextEffectSpan

	// styleByChar remembers each speaker's last TRANSMITTED sprite style, keyed by
	// msg.CharID. Senders transmit the marker only when their style CHANGES
	// (send-on-change keeps the invisible run off most messages — EncodeChangeMarker),
	// so a message with no marker reuses its speaker's remembered style. A clear (an
	// inactive style) frees the entry; the map is bounded by maxRememberedStyles.
	styleByChar map[int]SpriteStyle

	// profileByName remembers each speaker's transmitted WireProfile (#101 slice 2),
	// keyed by the bare character name (the player list rows key by character too). Like
	// the style memory it's send-on-change: a message carries the profile marker only when
	// it changed; an empty profile (a clear) frees the entry. Bounded by
	// maxRememberedProfiles.
	profileByName map[string]WireProfile

	// asyncAOByName flags characters seen emitting the cross-client zero-width channel
	// — i.e. driven by an AsyncAO client (AO2 / webAO never emit it). The player list
	// badges them; detection is passive (after they speak / react). Bounded by
	// maxDetectedAsyncAO.
	asyncAOByName map[string]struct{}

	// statusByName remembers each speaker's transmitted presence Status (#M1), keyed by
	// bare character name — the same send-on-change zero-width channel as the profile and
	// sprite style. A clear (StatusNone) frees the entry; bounded by maxRememberedStatuses.
	statusByName map[string]Status

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
		// Power-user core-timing knobs, seeded to the canonical AO2 values so
		// direct callers behave exactly as before; the App overrides from prefs.
		ShoutDuration:  DefaultShoutDuration,
		PreanimTimeout: DefaultPreanimTimeout,
		QueueCap:       messageQueueCap,
		CatchUpLinger:  catchUpLinger,
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

// waitHolds reports whether the cold-load "wait" gate holds msg off-stage this
// tick (SpriteWait mode): the speaker's idle sprite — the first thing the stage
// would show — hasn't decoded yet and the timeout hasn't expired. behind is how
// many messages are queued BEHIND msg (matching begin()'s post-pop catch-up
// check, so the two triggers can never disagree); dt advances the countdown
// (pass 0 at an arm-only site). Arming fires the SAME idle/talk prefetch begin()
// would — at HIGH, with the bare-spelling fallback — so the wait can actually
// end (singleflight makes begin()'s repeat a no-op). The readiness key is the
// PREFIXED base: PrefetchWithFallback keeps it the asset's identity whichever
// spelling the server ships (CLAUDE.md).
func (c *Courtroom) waitHolds(msg *protocol.ChatMessage, behind int, dt time.Duration) bool {
	if !c.SpriteWait || c.SpriteReady == nil || msg == nil || msg.IsShout() {
		return false
	}
	if c.CatchUp && behind >= c.CatchUpThreshold {
		c.waitFor = nil // catch-up wins: this message fast-forwards, waiting would only add lag
		return false
	}
	idle := c.urls.Emote(msg.CharName, msg.Emote, EmoteIdle)
	ready := c.SpriteReady(idle)
	if ready && c.SpriteWaitPair && msg.Pair.Active() { // strictness knob: the pair partner's idle too
		ready = c.SpriteReady(c.urls.Emote(msg.Pair.Name, msg.Pair.Emote, EmoteIdle))
	}
	if ready && c.SpriteWaitPreanim && hasPreanim(msg) { // strictness knob: the preanimation too
		ready = c.SpriteReady(c.urls.Emote(msg.CharName, msg.PreEmote, EmotePreanim))
	}
	if ready {
		c.waitFor = nil
		return false
	}
	if c.waitFor != msg { // a new head arms: start the countdown + warm its sprites
		c.waitFor = msg
		c.waitLeft = c.SpriteWaitTimeout
		c.mgr.PrefetchChain(idle, c.spriteAlts(msg.CharName, msg.Emote, EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh)                                             // AssetType: CharSprite (wait-gate warm)
		c.mgr.PrefetchChain(c.urls.Emote(msg.CharName, msg.Emote, EmoteTalk), c.spriteAlts(msg.CharName, msg.Emote, EmoteTalk), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (wait-gate warm)
		if c.SpriteWaitPair && msg.Pair.Active() {
			c.mgr.PrefetchChain(c.urls.Emote(msg.Pair.Name, msg.Pair.Emote, EmoteIdle), c.spriteAlts(msg.Pair.Name, msg.Pair.Emote, EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (wait-gate warm, pair)
		}
		if c.SpriteWaitPreanim && hasPreanim(msg) {
			c.mgr.Prefetch(c.urls.Emote(msg.CharName, msg.PreEmote, EmotePreanim), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (wait-gate warm, preanim)
		}
	}
	c.waitLeft -= dt
	if c.waitLeft <= 0 {
		c.waitFor = nil // timed out: play anyway (the renderer's own cold-load mode covers the gap)
		return false
	}
	return true
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
		// Wait mode: a cold sprite parks the message in the queue instead of
		// beginning — Update's PhaseIdle case drains it once ready / timed out.
		if c.waitHolds(msg, 0, 0) {
			c.queue = append(c.queue, msg)
			return
		}
		c.begin(msg)
		return
	}
	// The cap is the power-user QueueCap, hard-floored ≥ 1 so a bad pref can
	// never unbound the queue (rule §17.4).
	qcap := c.QueueCap
	if qcap < 1 {
		qcap = 1
	}
	if len(c.queue) < qcap {
		c.queue = append(c.queue, msg)
	}
	// Beyond the cap the oldest unplayed message drops — bounded queues
	// beat unbounded lag (rule §17.4); IC history still records via logs.
	if len(c.queue) == qcap {
		copy(c.queue, c.queue[1:])
		c.queue = c.queue[:qcap-1]
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
// applyCenterPrefix implements webAO's "~~" convention: a message whose visible text
// starts with "~~" is centred in the chatbox. Strip the 2-rune marker from the display
// text, raise Scene.Centered, and shift any transmitted effect spans left by 2 so they
// stay aligned (the sender computed them over the text that still carried the "~~").
// Operates on c.currentText (already sprite-style-stripped); a no-prefix message just
// clears Centered, so the common path is one HasPrefix check.
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

func (c *Courtroom) begin(msg *protocol.ChatMessage) {
	c.current = msg
	c.waitFor = nil // beginning ends any armed wait-gate hold
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
	// Send-on-change: a message carries the style marker only when this speaker's
	// style CHANGED. With a STYLE marker, remember it (an update — or a clear that frees
	// the entry); without one, reuse the speaker's last remembered style so a styled
	// character stays styled across the messages that omit the marker. Gate on
	// HasStyleMarker (not any zero-width run): a profile-only message (#101 shares this
	// invisible channel) must NOT be misread as a style clear that wipes the style.
	if HasStyleMarker(msg.Message) {
		c.rememberStyle(msg.CharID, style)
	} else {
		style = c.RecalledStyle(msg.CharID)
	}
	// Remember this speaker's transmitted character profile (#101 slice 2) — the same
	// invisible channel, told apart by frame magic. The player list reads it per
	// character; an empty profile (a clear) frees the entry.
	if hasMarker(msg.Message) {
		c.rememberAsyncAO(msg.CharName) // any cross-client frame ⇒ this speaker is on AsyncAO
	}
	if prof, ok := DecodeProfileMarker(msg.Message); ok {
		c.rememberProfile(msg.CharName, prof)
	}
	// And this speaker's presence status (#M1) — same channel, told apart by frame magic.
	if st, ok := DecodeStatusMarker(msg.Message); ok {
		c.rememberStatus(msg.CharName, st)
	}
	// Animated-text spans (#M5) — same channel, distinct magic. Per-message content (not
	// recalled): decode straight from this message, kept until the text shows (startTalking).
	c.pendingEffects = c.pendingEffects[:0]
	if spans, ok := DecodeEffectsMarker(msg.Message); ok {
		c.pendingEffects = append(c.pendingEffects, spans...)
	}
	// "~~" prefix → centre the chatbox text (webAO). Strip the marker (and realign any
	// transmitted effect spans) BEFORE the blankpost test / inline-emote expand / reveal.
	c.applyCenterPrefix()
	// #18 inline emotes: expand known :shortcode: tokens in the speaker's visible text to
	// their emoji, so the chatbox renders them like the IC log. GATED to messages with NO
	// effect spans: the wire span indices were computed over the literal text and a
	// substitution shifts rune counts, so expanding would misalign them (the colour runs are
	// parsed from currentText just below, so those stay aligned for free). This MUST run here
	// — after the pendingEffects decode (so the gate can read it) and BEFORE IsBlankPost and
	// Typewriter.Start — so the reveal, the blankpost test, and the raster all consume the
	// same substituted text. The wire msg.Message is untouched, so the reaction ref and the
	// recording stay literal/cross-client-stable.
	if c.InlineEmote != nil && len(c.pendingEffects) == 0 {
		c.currentText = ExpandInlineEmotes(c.currentText, c.InlineEmote)
	}
	if c.HideSpriteStyles {
		style = SpriteStyle{} // viewer opted out of others' styles
	} else if c.ReduceMotion {
		style.Wobble, style.Spin, style.Motion = false, false, 0 // accessibility: drop transmitted motion
	}
	speakerName := msg.CharName

	// --- prefetch fan-out (all HIGH, all parallel on the pool) ---
	// Idle/talk walk the full sprite spelling chain — the glued "(a)X", the
	// bare file name, then the "(a)/X" prefix FOLDER — packs ship any of the
	// three (AO2-Client animationlayer.cpp:422-444; order note in EmoteAlts).
	idle := c.urls.Emote(speakerName, msg.Emote, EmoteIdle)
	talk := c.urls.Emote(speakerName, msg.Emote, EmoteTalk)
	c.mgr.PrefetchChain(idle, c.spriteAlts(speakerName, msg.Emote, EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	c.mgr.PrefetchChain(talk, c.spriteAlts(speakerName, msg.Emote, EmoteTalk), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite

	pre := ""
	if hasPreanim(msg) {
		pre = c.urls.Emote(speakerName, msg.PreEmote, EmotePreanim)
		c.mgr.Prefetch(pre, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	}

	pairIdle := ""
	if msg.Pair.Active() {
		pairIdle = c.urls.Emote(msg.Pair.Name, msg.Pair.Emote, EmoteIdle)
		c.mgr.PrefetchChain(pairIdle, c.spriteAlts(msg.Pair.Name, msg.Pair.Emote, EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (pair partner)
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
	if blip == "" && c.BlipNameFor != nil {
		// webAO parity: senders that omit the wire field (pre-2.10.2 clients,
		// short packets) still blip with THEIR char.ini set, not the default.
		blip = c.BlipNameFor(speakerName)
	}
	if blip == "" {
		blip = "male" // AO default blip set
	}
	c.blipBase = c.urls.Blip(blip)
	// Same case chain as the chatbox skin: the lowercase identity leads,
	// the authored spelling follows for case-preserving mirrors (the chain
	// dedups when they match). // AssetType: Blip
	c.mgr.PrefetchChain(c.blipBase, []string{c.urls.BlipAuthored(blip)}, assets.AssetTypeBlip, network.PriorityHigh)

	// Per-character chatbox skin (char.ini chat=<misc>, AO2 get_chat): the scene
	// carries the misc art's base; the ui draws it as the chatbox background
	// when resident (and the client's normal box until then / when absent).
	// The spellings — two stems × two casings, see MiscChatboxCandidates —
	// walk in order; the first candidate stays the asset's identity (same
	// shape as the (a)-sprite / bare-sprite chain).
	c.Scene.ChatSkinBase = ""
	if c.ChatSkinFor != nil {
		if misc := c.ChatSkinFor(speakerName); misc != "" {
			cands := c.urls.MiscChatboxCandidates(misc)
			c.Scene.ChatSkinBase = cands[0]
			c.mgr.PrefetchChain(cands[0], cands[1:], assets.AssetTypeMisc, network.PriorityHigh) // AssetType: Misc (chatbox skin)
		}
	}

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
	c.Scene.MessageEffects = c.Scene.MessageEffects[:0]
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
		c.timer = c.ShoutDuration // power-user knob; seeded to DefaultShoutDuration
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
	caStyle, caClean := DecodeSpriteStyle(msg.Message) // strip the style marker (catch-up never redraws the sprite)
	c.currentText = caClean
	// "~~" centre prefix (webAO): strip it so the one-frame flash doesn't show the marker.
	if c.Scene.Centered = strings.HasPrefix(c.currentText, "~~"); c.Scene.Centered {
		c.currentText = c.currentText[2:]
	}
	// #18 inline emotes: mirror begin()'s gated expansion so a backlog flash agrees with the
	// (already-expanded) IC log. Same gate — no expansion when the message carries effect spans.
	if c.InlineEmote != nil {
		if _, hasEffects := DecodeEffectsMarker(msg.Message); !hasEffects {
			c.currentText = ExpandInlineEmotes(c.currentText, c.InlineEmote)
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
	// Same blankpost rule as begin(); this path doesn't route through it.
	c.Scene.IsBlankPost = strings.TrimSpace(c.Scene.MessageText) == ""
	c.preanimDone = false
	c.phase = PhaseLinger
	c.timer = c.CatchUpLinger // power-user knob; the canonical default is zero (drain one per frame)
}

// enterAfterShout picks preanim vs talking, mirroring handle_emote_mod:
// preanim plays first unless absent; IDLE/ZOOM with immediate plays preanim
// alongside the text.
func (c *Courtroom) enterAfterShout() {
	c.Scene.ShoutBase = ""
	c.Scene.ShoutFallbackBase = ""
	msg := c.current
	c.fireMessageEffects(msg)
	// !preanimDone: begin() resets the flag, so it can only be true here when
	// NotifyAssetMissing landed while the shout bubble held the stage — the
	// preanim conclusively 404'd and hijacking Active with it would draw a
	// blank speaker. Skip straight to the talk loop, exactly where a played
	// preanim would have ended up.
	playPre := hasPreanim(msg) && !c.preanimDone &&
		(msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom || msg.Immediate)
	blockOnPre := playPre && !msg.Immediate &&
		(msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom)

	if playPre {
		c.Scene.Speaker.Active = c.Scene.Speaker.PreanimBase
		c.Scene.Speaker.PlayOnce = true
	}
	if blockOnPre {
		c.phase = PhasePreanim
		c.timer = c.PreanimTimeout // power-user knob; seeded to DefaultPreanimTimeout
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
	c.Scene.MessageEffects = append(c.Scene.MessageEffects[:0], c.pendingEffects...)    // #M5 spans, decoded in begin
	c.Scene.VisibleRunes = 0
	if !c.Scene.Speaker.PlayOnce {
		c.Scene.Speaker.Active = c.Scene.Speaker.TalkBase
	} else {
		// Immediate mode: the preanim keeps playing over the text (Active stays on
		// PreanimBase). Bound how long PhaseTalking waits for it after the text is
		// done so a slow-decoding / missing preanim can't freeze the message —
		// NotifyPreanimStarted extends this to the real duration once the decoded
		// preanim plays, exactly like the blocking PhasePreanim path.
		c.timer = c.PreanimTimeout
	}
	if c.Typewriter.Done() && !c.Scene.Speaker.PlayOnce { // blank post, no pending preanim
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

// NotifyPreanimStarted is called by the render side the first frame a decoded,
// multi-frame preanimation actually plays, reporting its real total duration.
// PreanimTimeout is only a fallback for a preanim whose length isn't known yet
// (still decoding); once we're playing it we know the truth, so extend the
// phase timer to cover the full playback. Without this a preanim longer than
// PreanimTimeout was cut short AT the timeout ("plays a second or two, then
// skips to the end"). NotifyPreanimDone still ends the phase exactly at the
// natural finish — this only stops the fallback pre-empting it. Extend-only and
// phase-guarded, so a stale callback from another room while a replay/maker
// preview drives the same shared viewport is a safe no-op.
func (c *Courtroom) NotifyPreanimStarted(total time.Duration) {
	// PhasePreanim = a blocking preanim wait; PhaseTalking + PlayOnce = an
	// IMMEDIATE-mode preanim playing over the text. Both bound the wait on
	// c.timer, and both must let a long DECODED preanim play in full.
	if c.phase != PhasePreanim && !(c.phase == PhaseTalking && c.Scene.Speaker.PlayOnce) {
		return
	}
	if want := total + preanimPlaybackSlack; want > c.timer {
		c.timer = want
	}
}

// NotifyAssetMissing reports that the asset manager conclusively failed to
// resolve base: every spelling and format 404'd (the §4 warning lane — the
// App relays char-sprite warnings here; wrong-room and wrong-message bases
// simply don't match). Only the CURRENT message's preanimation reacts.
//
// AO2-Client skips a preanim it cannot find the moment it looks it up
// (courtroom.cpp play_preanim: a missing file emits done immediately); a
// streaming client can only learn absence asynchronously, and this is that
// moment. Without it, a char.ini that fills the preanim field with a dummy
// name on every emote (live packs ship "-<n>") froze EVERY message from that
// character for the full PreanimTimeout with a blank speaker — the negative
// cache made the re-probes free, but nothing told the phase machine, so the
// stall survived caching entirely.
func (c *Courtroom) NotifyAssetMissing(base string) {
	if base == "" || c.current == nil || base != c.Scene.Speaker.PreanimBase {
		return
	}
	// Exactly NotifyPreanimDone: the preanim "finished" (it can never start).
	// PhasePreanim exits on the next Update; enterAfterShout reads the flag
	// too, so a miss learned during the shout bubble skips the phase outright.
	c.preanimDone = true
	// Immediate mode (playPre without blockOnPre) parks Active on the preanim
	// with PlayOnce while the text types; a missing one would leave the
	// speaker invisible for the whole message (no one-shot ever completes, so
	// OnPreanimDone never fires). Restore the talk loop exactly as
	// startTalking would have without the hijack.
	if c.Scene.Speaker.PlayOnce {
		c.Scene.Speaker.PlayOnce = false
		if c.phase == PhaseTalking {
			c.Scene.Speaker.Active = c.Scene.Speaker.TalkBase
		}
	}
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
		textDone := c.Typewriter.Done()
		// Immediate mode: a preanim (PlayOnce) is playing over the text crawl.
		if c.Scene.Speaker.PlayOnce {
			if textDone {
				c.timer -= dt // bound the post-text wait (armed in startTalking, extended by NotifyPreanimStarted)
			}
			if c.preanimDone || (textDone && c.timer <= 0) {
				c.Scene.Speaker.PlayOnce = false
				if !textDone {
					// Preanim finished while the text is still crawling: flap the
					// talk sprite for the rest (it used to freeze on the last
					// preanim frame).
					c.Scene.Speaker.Active = c.Scene.Speaker.TalkBase
				}
			}
		}
		// Linger only once the text AND any immediate preanim are done — finishing
		// the text alone used to snap straight to idle mid-preanim.
		if textDone && !c.Scene.Speaker.PlayOnce {
			c.enterLinger()
		}

	case PhaseLinger:
		c.timer -= dt
		if c.timer <= 0 {
			c.phase = PhaseIdle
			// Wait mode: a cold next sprite holds in the queue (the PhaseIdle case
			// below keeps ticking it). Gate off → waitHolds is false immediately and
			// the next message dequeues THIS tick, exactly as before.
			if len(c.queue) > 0 && c.waitHolds(c.queue[0], len(c.queue)-1, 0) {
				break
			}
			c.dequeue()
		}

	case PhaseIdle:
		// Only the wait gate can leave a message queued while idle (every other
		// path drains on arrival / linger-end), so this case is a no-op unless a
		// hold is in flight: tick it and begin the moment the sprite lands or the
		// timeout expires.
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

// hasPreanim mirrors webAO's emptiness checks for the preanim field.
func hasPreanim(msg *protocol.ChatMessage) bool {
	return msg.PreEmote != "" && msg.PreEmote != emptyPreanimDash
}

// spriteAlts is URLBuilder.EmoteAlts through the room's builder (the chain
// every sprite prefetch in this file feeds to PrefetchChain).
func (c *Courtroom) spriteAlts(character, emote string, kind EmoteKind) []string {
	return c.urls.EmoteAlts(character, emote, kind)
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
