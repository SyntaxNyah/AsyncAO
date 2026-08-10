package courtroom

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	// missingSpritesCap bounds the conclusively-404'd base set the wait gate
	// consults (spec §17.4 — no unbounded caches). A session sees only a handful
	// of genuinely-missing sprites; past the cap we stop recording and the
	// SpriteWaitTimeout still bounds any un-recorded miss.
	missingSpritesCap = 512
	// missingDesksCap bounds the conclusively-404'd DESK base set (spec §17.4 —
	// no unbounded caches), sized like missingSpritesCap: a session visits a
	// handful of backgrounds, each contributing at most one desk per position.
	// Past the cap we stop recording and the desk simply keeps the pre-#44
	// behaviour (the render-side release still covers the on-screen layer).
	missingDesksCap = 512
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

	// sfxDelayUnit is the wire SFX_DELAY time unit (#12). AO2-Client multiplies
	// the wire value by time_mod = 40ms to start sfx_delay_timer, so a whip-crack
	// lands mid-preanim rather than at message start (../AO2-Client/src/
	// courtroom.cpp:4052 `sfx_delay = m_chatmessage[SFX_DELAY].toInt() * time_mod`,
	// time_mod defined at courtroom.h:428). msg.SFXDelay is the raw wire int, so
	// the deadline is SFXDelay × sfxDelayUnit.
	sfxDelayUnit = 40 * time.Millisecond
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
	// PlayOnce marks the active animation as one-shot (preanim). It means EXACTLY
	// "this is the speaker's preanimation" — set true only for the speaker's own
	// preanim (enterAfterShout), never on the pair layer or an idle/talk sprite.
	// The render miss-branch relies on that: viewport.drawSprite exempts any
	// PlayOnce layer from the missingno placeholder (a missing preanim must never
	// flash a placeholder). Setting PlayOnce anywhere else would silently suppress
	// the WANTED idle/talk placeholder for a conclusively-missing base.
	PlayOnce bool
	Flip     bool
	// OffsetX/Y are percent of viewport dimensions (−100..100).
	OffsetX, OffsetY int
	Visible          bool
	// Style is this layer's transmitted sprite customization (recolour / glow /
	// opacity / motion): the speaker's is decoded from this message's text, a
	// pair partner's is recalled by char id (the wire carries no partner style —
	// see begin()). Zero value = none; the renderer leaves the blit
	// byte-identical when it's inactive.
	Style SpriteStyle
	// Scaling is this layer's texture filter, per AO2's per-sprite model (each
	// AnimationLayer carries its own resize mode, so a pixel-art speaker and a
	// smooth pair partner each get what they asked for). Zero value is
	// ScalingAuto, which is both the default and the case the renderer settles
	// with the geometry rule — see scaling.go.
	Scaling ScalingMode
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

	// Overlay is the active AO2 screen-effect overlay (effects.ini): the art
	// base plus everything do_effect precomputes for it. Zero value = nothing on
	// screen. Set only by fireOverlay, cleared at exactly three sites (see
	// overlayfx.go) — deliberately NOT at message end, matching canon.
	Overlay SceneOverlay
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
	// PlayMusic streams a track from a full URL. loop=false plays it once
	// (2.9 NO_REPEAT / looping=0); effects carries the MUSIC_EFFECT bit flags
	// (fade in/out) — see the musicEffect* constants (#15).
	PlayMusic(url string, loop bool, effects int)
	// StopMusic halts playback now (the ~stop sentinel; also on disconnect).
	StopMusic()
}

// NopAudio discards all triggers (headless tests, muted client).
type NopAudio struct{}

func (NopAudio) PlayShout(string)              {}
func (NopAudio) PlaySFX(string, time.Duration) {}
func (NopAudio) PlayBlip(string)               {}
func (NopAudio) SetBlipScale(int)              {}
func (NopAudio) PlayMusic(string, bool, int)   {}
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

	// ScreenEffects gates the AO2 inline \s/\f codes and the field-based
	// screenshake / realization flash — ON by default, a dedicated off-switch
	// distinct from the accessibility ReduceMotion. Effects render only when
	// ScreenEffects && !ReduceMotion (see effectsVisible). Defaults ON in
	// NewCourtroom so authored/export contexts show effects; the App pushes the
	// user's pref onto the live room.
	ScreenEffects bool

	// AdditiveText honors the 2.8 ADDITIVE flag: an incoming message with
	// ADDITIVE=1 APPENDS to the previous line (the typewriter starts with the prior
	// text pre-revealed; pacing/blips run only on the appended tail) — narration-style
	// RP relies on it. Default ON (set by the App from prefs). OFF = every additive
	// fragment replaces the previous, the old behavior. additivePrevText tracks the
	// last message's accumulated text, exactly like AO2's single additive_previous
	// accumulator (courtroom.cpp:4225-4330 append whenever ADDITIVE=1, with NO char-id
	// gate — a continuation can even come from a different speaker; the reset at
	// :4229 wipes it only when a line is NOT additive).
	AdditiveText     bool
	additivePrevText string
	additivePrefix   string // this message's pre-revealed additive prefix ("" = replace); set in begin, consumed in startTalking

	// HideSpriteStyles ignores other speakers' transmitted SpriteStyle entirely
	// (every character renders normally) — a viewer off-switch. Set by the App
	// from prefs; default off (zero value) = show received styles.
	HideSpriteStyles bool

	// ForceCharNames shows every speaker's CHARACTER name instead of their
	// custom showname (true-roleplay / anti-impersonation for casing). Set by
	// the App from prefs (default off); the IC log mirrors it App-side.
	ForceCharNames bool

	// StreamMusic is set by the App from prefs (default ON). false = never
	// fetch a /play track; the room still tracks what's "playing" for
	// Now-Playing display, it just isn't audible on this client. Seeded ON in
	// NewCourtroom (like ScreenEffects/AdditiveText) so direct callers
	// (tests/export/replay) keep playing music; the App pushes the user's pref.
	StreamMusic bool

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

	// IniShownameFor, when set, is AOApplication::get_showname for a speaker —
	// the char.ini [Options] showname (with the needs_showname opt-out),
	// resolved from the SAME per-URL char.ini cache BlipNameFor answers from,
	// so the name plate costs no extra network work.
	//
	// It is the MIDDLE rung of canon's three-step name chain and the one AsyncAO
	// shipped without: a wire showname wins, then this, then the character folder
	// (courtroom.cpp:3283-3292 initialize_chatbox, :2528-2538 log_chatmessage).
	// AO2 can always answer because it reads the ini off disk; a streaming client
	// cannot, which is what `known` is for — false means "no char.ini yet"
	// (or none is wired) and displayName falls back to the folder name, exactly
	// as canon does for an unreadable ini. A known-but-EMPTY answer is
	// needs_showname=false, a deliberate blank plate, and is honoured as one.
	IniShownameFor func(char string) (showname string, known bool)

	// EffectsFolderFor resolves a speaker's screen-effect misc folder (char.ini
	// [Options] effects, AO2-Client get_effect's p_folder default,
	// text_file_functions.cpp:836-839). It rides the SAME per-URL char.ini cache
	// as BlipNameFor / ChatSkinFor, so honouring a character's own effects pack
	// costs zero extra network work. Consulted only for the 1- and 2-part wire
	// shapes, which omit the folder. "" = the theme's own effects only.
	EffectsFolderFor func(char string) string

	// CustomRealizationFor resolves a speaker's OWN realization sound to a full
	// SFX url — char.ini [Options] realization, AO2's get_custom_realization
	// (text_file_functions.cpp:896-903). Canon calls it from the legacy
	// REALIZATION=1 path (courtroom.cpp:4175) INSTEAD of the theme's court sfx, so
	// a character shipping its own realization sound is heard everywhere. It rides
	// the same per-URL char.ini cache as EffectsFolderFor — zero extra network
	// work. "" (or nil) = the character declares none; RealizationSFX, the theme's
	// courtroom_sounds.ini value, is the fallback exactly as it is in canon.
	CustomRealizationFor func(char string) string

	// OverlayFor resolves an effect NAME + misc FOLDER to its merged effects.ini
	// properties and the T1 key / URL its art lives under. The App owns it: the
	// theme tier is disk (theme.FindOverlayElement) and the misc tier is an origin
	// chain, and neither belongs in an SDL-free state machine. It also OWNS the
	// demand — the App kicks off the read/prefetch, this side only records the
	// base. nil = no overlay engine (headless tests, the maker preview), which
	// leaves the legacy flash/shake fallback in charge.
	//
	// IT IS NOT A PURE LOOKUP, and the sentence above reads like one, so the three
	// clauses a substitute has to honour are written out here — a substitute that
	// meets only the signature is a map, and a map is wrong on all three:
	//
	//  1. RENDER THREAD ONLY. The shipped implementation reads and writes App state
	//     (the roster cache, the in-flight set, the T1 store). Calling it from a
	//     goroutine is a -race failure, not a slow path.
	//  2. IT IS STATEFUL. The first call for an unseen (name, folder) KICKS a resolve
	//     and reports what it knows so far; later calls answer from the landed
	//     roster. Two calls with the same arguments may legitimately differ.
	//  3. ok=true DOES NOT MEAN THE ART IS RESIDENT. The resolved value carries a
	//     pending state — a promise that the base is being fetched — and the caller
	//     must tolerate a frame (or many) with nothing to draw yet.
	//
	// Substitutes in tests therefore pin BEHAVIOUR, not just shape: a stub that
	// always answers instantly hides every ordering bug this contract exists around.
	OverlayFor func(name, folder string) (OverlayResolved, bool)

	// LocalSide, when set, reports OUR OWN side — the pos dropdown's value, with
	// AO2's default when it is unset. It mirrors AO2-Client's
	// current_or_default_side(), which is what set_background's re-stage passes to
	// set_scene. It is consulted ONLY on a wiping BN that carried no pos (the
	// default form on akashi and on KFO's remote-listen path): without it the
	// freshly-wiped stage would re-scene at Scene.Position, which tracks the last
	// SPEAKER's side, and an area jump would drop you at whoever talked last.
	// nil degrades to that old behaviour, so tests and the split pane are safe.
	LocalSide func() string

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
	// SpriteScaling reports a character's char.ini [Options] scaling= request
	// (get_scaling). Same shape as SpriteReady — a nil hook means "nobody asked",
	// which resolves to ScalingAuto and lets the renderer's geometry rule decide.
	//
	// The App answers it from the same per-speaker char.ini cache that already
	// backs BlipNameFor / ChatSkinFor (ui/charmeta.go), so every speaker's filter
	// is honoured for no extra network probe. Called once per message when the
	// sprite layers are built, never per render frame.
	SpriteScaling func(charName string) ScalingMode
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
	// missingSprites remembers bases the asset manager conclusively 404'd,
	// recorded from the SAME NotifyAssetMissing warning relay the play path uses.
	// The wait gate consults it so a declared-but-absent preanim — live packs
	// fill the char.ini preanim field with a dummy "-<n>" on every emote — releases
	// the hold on the miss signal instead of burning the full SpriteWaitTimeout
	// (the play path already skips such a preanim via NotifyAssetMissing; the gate
	// now matches it). Bounded (missingSpritesCap); lazily allocated; game-thread only.
	missingSprites map[string]struct{}
	// missingDesks remembers DESK bases the asset manager conclusively 404'd, fed
	// by the same warning relay (NotifyDeskMissing). AO2-Client's set_scene calls
	// file_exists on the position's desk image and forces show_desk = false when it
	// is absent — with NO fallback, unlike the background arm right above it
	// (../AO2-Client/src/courtroom.cpp:4613-4634). A streaming client can only learn
	// absence asynchronously, and this set is that knowledge. Deliberately NOT the
	// missingSprites map: that one is read by the preanim wait gate, where a desk
	// base would be a semantic lie. Bounded (missingDesksCap); lazily allocated;
	// game-thread only.
	missingDesks map[string]struct{}

	queue []*protocol.ChatMessage
	phase MessagePhase
	timer time.Duration

	// Pending SFX deadline (#12): AO2 starts sfx_delay_timer at SFX_DELAY × 40ms
	// after the shout so the emote sound lands mid-preanim, and fires the preanim
	// screenshake at that same moment (../AO2-Client/src/courtroom.cpp:4052-4054,
	// play_sfx 4590-4596). armSFXDelay sets these in enterAfterShout; the Update
	// tick counts sfxLeft down and fires at zero. begin() clears them so a
	// superseded message's SFX can't fire late.
	sfxArmed bool          // a delayed SFX/shake is waiting to fire
	sfxLeft  time.Duration // countdown to the fire moment (SFXDelay × sfxDelayUnit)
	sfxBase  string        // resolved SFX URL to play at the deadline ("" = shake only)
	sfxShake bool          // also fire the screenshake at the deadline (preanim mods)

	// overlayPending is the ONE deferred screen-effect arm: an effect whose misc
	// folder's roster had not resolved when the message fired, re-asked on the
	// Update tick until it lands or its TTL expires (overlayfx.go). Zero = none.
	overlayPending overlayPendingFX

	// frameTriggers holds THIS message's networked frame-synced effects (#17):
	// the FRAME_* wire fields parsed once at message-begin into a bounded,
	// per-phase-group sorted table with forward-only cursors. NotifyFrameShown
	// (called from the render side as the speaker sprite advances) fires them.
	// Empty for a message with no FRAME_* data (the common case) — an inert table.
	frameTriggers frameTriggerTable

	current *protocol.ChatMessage
	// currentText is current.Message with any transmitted SpriteStyle marker
	// decoded out — the visible-only text the typewriter/blankpost use. The raw
	// current.Message keeps the marker (recordings share that pointer, so replays
	// re-decode the style); we never mutate it.
	currentText string
	// blipRef is THIS message's blip chain, minted once in begin() by the one
	// guarded mint (blipurl.go BlipRef): Base is the identity the blip player and
	// the texture/audio tiers key on, Alts are the further AO2 spellings the
	// prefetch chain walks on a miss.
	blipRef AssetRef
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

	// imPreLeft bounds an IMMEDIATE-mode preanim, the one AO2 plays as pure
	// DECORATION over a message that is already ticking (anim_state 4 — see
	// play_preanim(true), ../AO2-Client/src/courtroom.cpp:4093-4096). It is a
	// SEPARATE countdown from c.timer on purpose: c.timer belongs to the phase,
	// and this animation deliberately outlives the phase that started it. >0 means
	// a decoration is running; 0 means none. Cleared per message in begin().
	imPreLeft time.Duration

	// preanimEnd records why the last one-shot preanimation ended (preanimend.go).
	// Diagnostic only — nothing in the machine branches on it.
	preanimEnd PreanimEndReason

	// OnPreanimEnd, when set, is called with the reason every time a one-shot
	// preanimation is released. Optional and nil by default: the always-on cost
	// of the instrumentation is one nil check per release, and a release happens
	// at most once per message. Set it to stream "who ended the preanim" to a log
	// or the debug panel while chasing an interrupt report.
	OnPreanimEnd func(PreanimEndReason)

	// restoring is set for the span of RestoreMessage: begin() then skips the
	// prefetches for art the settled form can never draw (shout bubbles, the
	// preanimation) — a restore whose message landed while the tab was
	// backgrounded would otherwise spend cold HIGH-priority probes on
	// never-shown assets (the one-probe doctrine, spec §4).
	restoring bool

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
		// AO2 screen effects (\s/\f + field shake/flash) default ON; the App
		// pushes the user's pref onto the live room, and authored/export/replay
		// contexts keep them on.
		ScreenEffects: true,
		// 2.8 additive text default ON (honor ADDITIVE=1 append); the App pushes the
		// user's pref. additivePrevText starts empty so the first message appends to
		// nothing (an empty prefix is a no-op — identical to replace).
		AdditiveText: true,
		// Custom /play music streaming default ON; the App pushes the user's pref.
		// Seeded here so direct callers (tests/export/replay) keep playing music
		// (the zero value false would silently swallow every /play track).
		StreamMusic: true,
		TextStay:    DefaultTextStayTime,
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

// AudioActive reports whether the live message is actively streaming audio-timed
// events — the typewriter is still revealing text, so blips fire as it advances.
// The main loop reads it to advance the courtroom (and play its blips) at a fine
// cadence even while the PRESENT rate is capped low, so audio never batches to the
// frame rate ("blips only every screen refresh at a 1 fps cap"). One-shot sounds
// (shout cries, emote SFX) fire on their own arrival and don't need this; only the
// continuous blip stream does. False the instant the text finishes (PhaseLinger).
func (c *Courtroom) AudioActive() bool {
	return c.phase == PhaseTalking && !c.Typewriter.Done()
}

// QueueLen exposes the pending message count.
func (c *Courtroom) QueueLen() int { return len(c.queue) }

// CurrentShout is the shout stem of the message being presented right now —
// "holdit", "objection", "takethat", "custom" — or "" when the stage is idle or
// the message carried no shout. It is ShoutName over the live message's
// objection modifier, i.e. exactly the stem the bubble and the cry resolve from.
//
// The whole MESSAGE, not just the bubble's ~800 ms: c.phase leaves PhaseShout
// long before the line finishes, and a theme element gated on "an objection is
// happening" means the turn, not the frame the bubble is on screen. Idle is the
// boundary because c.current deliberately outlives its message (a settled stage
// still needs the last speaker), so gating on it alone would leave the last
// shout latched forever.
//
// Read-only, allocation-free (ShoutName returns constants) and called at most
// once per frame — it is the source for a free element's `visible_when =
// shout:<name>` condition (docs/THEME-FORMAT.md §3).
func (c *Courtroom) CurrentShout() string {
	if c.phase == PhaseIdle || c.current == nil {
		return ""
	}
	return ShoutName(c.current.Objection)
}

// HandleEvent consumes a session event.
func (c *Courtroom) HandleEvent(ev Event) {
	switch ev.Kind {
	case EventMessage:
		c.enqueue(ev.Message)
	case EventBackground:
		// Name carries the BN's pos; Wipe is its arity flag (#23). Both are false/""
		// for every client-side background change, which is what keeps buildRoom's
		// re-seed from erasing the message it is about to restore.
		c.setBackground(ev.Text, ev.Name, ev.Wipe)
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
			// Streaming toggle (default ON): when off, never fetch the /play
			// track — but still record what the room intends to play so
			// Now-Playing stays accurate to the room, just silent here.
			if c.StreamMusic {
				c.audio.PlayMusic(c.urls.MusicURL(ev.Text), ev.Loop, ev.MusicEffects) // AssetType: Music
			}
			c.Scene.MusicTrack = ev.Text
		}
	}
}

// prefetchScenery warms the position background for bg, walking AO2's
// witness-view fallback ladder when the position ships no art of its own.
//
// It is the one place THIS package demands a background — begin (a message's
// position) and setBackground (a BN / area change) both come through here. It is
// the area-change case that motivated it: a custom position that exists in one
// room and not the next used to leave the PREVIOUS room's background on screen
// indefinitely, because the sticky scenery gate holds until the new base becomes
// resident and a 404'd base never does. AO2 never reaches that state:
// get_pos_path has already fallen back to the witness view before set_scene
// draws anything (../AO2-Client/src/path_functions.cpp:108-159).
//
// The chain's identity stays the position's own base, so Scene.BackgroundBase —
// the T1 key the viewport draws and the desk logic compares against — is
// unchanged whichever stem answers. // AssetType: Background
//
// KNOWN GAP, do not read this as "the ladder cannot be half applied": the APP
// has two further demand sites for the same live base, both outside this
// package and both still single-stem. internal/ui healScenery and the
// scene-warm helper re-demand Scene.BackgroundBase with a plain
// Manager.Prefetch and no alts, so after a T1 eviction a position that only
// ever resolved through the witness fallback is re-probed under its PRIMARY
// stem alone — which is 404-cached — the pass reports a total miss, and the
// stage goes black. The failure this ladder exists to prevent is therefore
// deferred to the first eviction rather than fixed. Both sites want
// PrefetchChain(base, <these alts>, …); BackgroundFallbacks and PositionScene
// are exported for exactly that, and internal/ui already holds a URLBuilder.
func (c *Courtroom) prefetchScenery(bg, bgPart string) {
	c.mgr.PrefetchChain(c.Scene.BackgroundBase, backgroundAltURLs(c.urls, bg, bgPart), assets.AssetTypeBackground, network.PriorityHigh)
}

// backgroundAltURLs turns BackgroundFallbacks' stems into URLs under bg. One
// owner for "the witness ladder, as URLs": the live prefetch above and the
// archive enumeration in SceneAssets both call it, so the exporter bundles
// whichever stem the live path would have accepted instead of only the primary.
func backgroundAltURLs(urls URLBuilder, bg, bgPart string) []string {
	stems := BackgroundFallbacks(bgPart)
	alts := make([]string, 0, len(stems))
	for _, stem := range stems {
		alts = append(alts, urls.Background(bg, stem))
	}
	return alts
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
	return "has played a song", MusicDisplayName(track), true
}

// pathSeparators are the characters that end a track's directory part. AO2 only
// splits on '/' (list_music / QUrl), but a Windows-authored list entry can carry
// a '\'; treating it as a separator too is a strict superset — it can never
// change the result for a '/'-only (or separator-free) name.
const pathSeparators = `/\`

// MusicDisplayName is the SHORT display name of a music-list entry or MC track —
// what the music list, the Now-Playing line and the IC "has played a song" line
// show (issue #43: a nested entry like "999/songs/that/slap/[999] Tranquility.ogg"
// used to be printed whole). The WIRE name is never touched: callers keep the raw
// string for the MC packet (Session.RequestMusic) and for URLBuilder.MusicURL.
//
// Two shapes, in this precedence order:
//
//  1. A direct http(s):// track — isMusicURL (urlbuilder.go): such a link is
//     ALWAYS real music and its audio extension can sit BEFORE a signed query
//     (a Discord CDN /play link ends in ?ex=&is=&hm=&). AO2-Client shows those
//     through QUrl(f_song).fileName() (courtroom.cpp handle_song), so the
//     query/fragment goes FIRST, then the directory, then the extension —
//     the other order would cut the name at a '.' inside the query string.
//  2. A server music-list name — exactly AO2-Client Courtroom::list_music()
//     (courtroom.cpp:1738-1782):
//     listname = i_song.left(i_song.lastIndexOf("."));
//     listname = listname.right(listname.length() - (listname.lastIndexOf("/") + 1));
//     i.e. cut at the LAST '.', THEN take everything after the LAST '/'. That
//     ORDER is the canonical one and differs from basename-first for oddities
//     like "vol.2/name" (AO2 → "vol"); for any name that actually ends in an
//     extension the two agree. QString::left(-1) returns the whole string, so an
//     entry with NO '.' comes back unchanged — which is how AO2 recognises a
//     CATEGORY header (listname == i_song ⇒ top-level tree item). This client
//     classifies headers with HasAudioExt instead (the same rule the SM split
//     uses, and it can't mis-cut a header that happens to contain a dot).
//
// The result is always a SUBSTRING of track — pure slicing, zero allocations,
// because this runs per visible row on the music-list draw. That also means a
// search over the RAW name matches everything the display shows.
//
// One deliberate divergence: Qt's left(0) yields "" for a name that is nothing
// but an extension (".mp3"), which would draw a blank, unclickable row. An empty
// result falls back to the raw name so every row always shows something.
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
	return !HasAudioExt(track)
}

// spriteSettled reports whether base is done resolving one way or the other:
// resident in T1 (SpriteReady is the render store's uploaded-texture probe, so
// this really is "decoded AND uploaded"), or conclusively 404'd. Both are
// answers, and the wait gate must never hold for one it has already received —
// the "never wait past the conclusively-missing rules" bound.
func (c *Courtroom) spriteSettled(base string) bool {
	return c.SpriteReady(base) || c.spriteConfirmedMissing(base)
}

// preanimWillPlay reports whether this message's preanimation is the thing the
// stage shows FIRST — the selection enterAfterShout makes (playPre), lifted out
// so the wait gate cannot drift from the play path. AO2's handle_emote_mod
// reaches play_preanim only for PREANIM / PREANIM_ZOOM, or for IDLE / ZOOM with
// immediate ticked (../AO2-Client/src/courtroom.cpp:2889-2910).
//
// It says nothing about whether the ART exists — the callers own that (the play
// path checks spriteConfirmedMissing before parking Active on it; the gate
// treats a confirmed miss as settled).
func preanimWillPlay(msg *protocol.ChatMessage) bool {
	return hasPreanim(msg) &&
		(msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom || msg.Immediate)
}

// waitHolds reports whether the cold-load "wait" gate holds msg off-stage this
// tick (SpriteWait mode): the sprite the stage would show FIRST hasn't decoded
// yet and the timeout hasn't expired. behind is how
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
	ready := c.spriteSettled(idle)
	// ...and the TALK sprite, which for a message with no preanim is the one the
	// stage draws FIRST: enterAfterShout falls straight through to startTalking,
	// which parks Active on TalkBase in the same tick begin() runs — the idle base
	// is only what the message SETTLES onto, a second or more later. Waiting for
	// idle alone therefore opened the gate onto a cold talk sprite and flashed the
	// placeholder for exactly the pre-message-stall window this audit is about.
	//
	// It is not a widening in cost: the arm below already warms talk beside idle
	// (it always has), so this waits for a fetch that was already in flight, and a
	// conclusively-404'd talk sprite counts as settled like every other — the gate
	// still never waits past the missing rules. The (a)/(b) spellings are often the
	// SAME file on legacy packs, in which case this is one extra map probe.
	if ready {
		ready = c.spriteSettled(c.urls.Emote(msg.CharName, msg.Emote, EmoteTalk))
	}
	if ready && c.SpriteWaitPair && msg.Pair.Active() { // strictness knob: the pair partner's idle too
		ready = c.spriteSettled(c.urls.Emote(msg.Pair.Name, msg.Pair.Emote, EmoteIdle))
	}
	// The preanimation is what the stage SHOWS first whenever it will actually
	// play (enterAfterShout parks Active on it before a single character is
	// revealed), so the base gate covers that case — waiting for the idle sprite
	// and then opening onto a cold preanim is the pre-message stall this audit was
	// asked to close. It is deliberately preanimWillPlay, not hasPreanim: live
	// packs fill the char.ini preanim field with a dummy on EVERY emote, and a
	// declared-but-never-played preanim is not something the first frame uses.
	// SpriteWaitPreanim keeps widening the gate to any declared preanim for the
	// power user who wants that.
	if ready && hasPreanim(msg) && (preanimWillPlay(msg) || c.SpriteWaitPreanim) {
		pre := c.urls.Emote(msg.CharName, msg.PreEmote, EmotePreanim)
		// A preanim that has CONCLUSIVELY 404'd is nothing to wait for — release on
		// the miss signal instead of burning the full timeout. Live packs fill the
		// char.ini preanim field with a dummy "-<n>" on every emote (and idle
		// emote-mod upgrades to preanim on the wire when a preanim name is present,
		// so the gate would otherwise wait for a sprite that never exists — the
		// "flower sprites always wait the whole delay" report). Mirrors the play
		// path's NotifyAssetMissing skip.
		ready = c.spriteSettled(pre)
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
		if hasPreanim(msg) && (preanimWillPlay(msg) || c.SpriteWaitPreanim) {
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

// setBackground prefetches the new room's scenery for the current position, and
// on a WIPING BN (one that carried a pos field — see Event.Wipe) also tears the
// viewport down the way AO2's set_background(bg, display=true) does.
//
// pos is the BN's pos field ("" when it carried none, or on any client-side
// background change). It is honoured only on a wipe, because that is the only
// path AO2 re-scenes on: `set_scene(true, current_or_default_side())` at the end
// of the display block. When the server sends a wiping BN with an EMPTY pos —
// the DEFAULT on akashi (its area side is unset until someone runs /side) and on
// KFO's remote-listen form — AO2 still re-scenes at the LOCAL user's own side, so
// we fall back to LocalSide rather than to Scene.Position: Scene.Position holds
// the last SPEAKER's side, and using it would re-stage the freshly-wiped stage at
// whoever happened to talk last in the room we just left.
// AssetType: Background
func (c *Courtroom) setBackground(bg, pos string, wipe bool) {
	if wipe {
		if pos == "" && c.LocalSide != nil {
			pos = c.LocalSide()
		}
		if pos != "" {
			c.Scene.Position = pos
		}
	}
	bgPart, deskPart := PositionScene(c.Scene.Position)
	c.Scene.BackgroundBase = c.urls.Background(bg, bgPart)
	c.Scene.DeskBase = c.urls.Background(bg, deskPart)
	c.prefetchScenery(bg, bgPart)
	c.mgr.Prefetch(c.Scene.DeskBase, assets.AssetTypeDeskOverlay, network.PriorityHigh) // AssetType: DeskOverlay
	// ShowDesk is derived from the DESK BASE's residency, and that base just
	// changed — so re-derive it here or the new room inherits the old room's
	// answer. This is the reported "the background is missing its desk on purpose,
	// but the default desk appears anyway": a room whose desk 404s kept drawing the
	// PREVIOUS room's desk until the next phase edge, and a settled message has no
	// further edge.
	//
	// Passing the CURRENT phase is what makes this safe. applyDeskMods also
	// recomputes PairActive and the speaker offsets from c.current, so a wrong
	// phase argument would flip pair state mid-preanim; at the current phase it
	// recomputes exactly what is already there. Same call, and the same "no further
	// edge" reasoning, as NotifyDeskMissing.
	if wipe {
		c.wipeStage()
		return
	}
	if c.current != nil {
		c.applyDeskMods(c.phase == PhasePreanim)
	}
}

// wipeStage ports AO2-Client's set_background(bg, display=true) teardown
// (../AO2-Client/src/courtroom.cpp — the `if (display)` block) so an AREA SWAP
// leaves a clean stage instead of the previous area's frozen sprite and chatbox
// (#23). It runs ONLY from a server BN that carried a pos field; every
// client-side background change (buildRoom's re-seed, the bg picker, replay, the
// scene maker) goes through the wipe=false path, which is unchanged.
//
// Deliberately NOT cleared, because AO2's block does not touch them and the
// server re-sends them itself: the IC log, music/ambience, the evidence LIST, HP,
// missingSprites/missingDesks, and the send-on-change memories. Stopping the
// music here would fight the music-resume that buildRoom depends on.
// WipeStage tears the viewport down without a background change, for the ONE
// client-initiated case the BN arity cannot cover: our own area jump against a
// server family that sends a one-field BN (Athena/Nyathena). Render-thread only,
// like every other exported Courtroom mutator.
func (c *Courtroom) WipeStage() { c.wipeStage() }

func (c *Courtroom) wipeStage() {
	// Report a one-shot the wipe is about to destroy BEFORE the layer is zeroed —
	// past that assignment there is no evidence a preanimation was ever playing.
	// Idempotent when nothing was.
	c.endPreanim(PreanimEndStageWiped)
	// Sprites: stopPlayback + hide on the speaker and the pair layer. drawSprite
	// early-returns on `!Visible || Active == ""` ABOVE the missingno/held/
	// hold-previous chain, so a zeroed layer cannot leak the previous frame.
	// SpeakerInFront returns to NewCourtroom's default rather than keeping the
	// departed pair's order.
	c.Scene.Speaker = SpriteLayer{}
	c.Scene.Pair = SpriteLayer{}
	c.Scene.PairActive = false
	c.Scene.SpeakerInFront = true

	// Chatbox: ui_vp_message->hide() + the chatbox visibility reset. Clearing both
	// texts IS the hide — drawChatOverlay returns early when they are both empty,
	// so no new draw-side flag is needed.
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

	// Shout bubble (ui_vp_objection->stopPlayback) and the screen effects the
	// speedlines/effect hides cover.
	c.Scene.ShoutBase = ""
	c.Scene.ShoutFallbackBase = ""
	c.Scene.ShoutCustom = false
	c.Scene.FlashLeft = 0
	c.Scene.ShakeLeft = 0
	// CLEAR SITE 2 of 3: set_background(bg, display=true) stops and hides the
	// effect layer with the rest of the stage (courtroom.cpp:1427-1435). An area
	// swap must not carry the previous room's looping wash across.
	c.clearOverlay()

	// text_queue_timer->stop() + skip_chatmessage_queue(). This loses NO IC log
	// line: AsyncAO appends to the log at INGEST, before the room ever sees the
	// message — the same split AO2 achieves by re-logging each dequeued message
	// DISPLAY_ONLY.
	c.queue = c.queue[:0]

	// text_state = 2; anim_state = 3 — i.e. "nothing is playing". Nilling current
	// is load-bearing beyond the phase reset: applyDeskMods and the asynchronous
	// NotifyDeskMissing both early-return on a nil current, which is what stops a
	// late desk-404 from recomputing PairActive off the message we just wiped and
	// resurrecting the pair sprite.
	c.phase, c.timer = PhaseIdle, 0
	c.current, c.currentText = nil, ""
	c.Typewriter.Start("") // chat_tick_timer->stop(); in place, keeps Interval/BlipRate

	// Per-message latches with no AO2 analogue: a Go port has to clear them or a
	// message held for a cold sprite is stranded, and the next area's first line
	// inherits this one's delayed SFX, frame triggers or 2.8 additive prefix.
	c.preanimDone = false
	c.imPreLeft = 0
	c.waitFor, c.waitLeft = nil, 0
	c.sfxArmed, c.sfxLeft, c.sfxBase, c.sfxShake = false, 0, "", false
	c.frameTriggers = frameTriggerTable{}
	c.pendingEffects = c.pendingEffects[:0]
	c.additivePrevText, c.additivePrefix = "", ""

	// set_scene(true, …): show_desk is hard-true there, and only a missing file
	// suppresses it.
	c.Scene.ShowDesk = c.deskResolution() != DeskAbsent
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

// AdditiveOutgoingLeadingSpace applies AO2's sender-side additive convention to
// an outgoing IC message: when the additive box is on, AO2-Client keeps a literal
// leading space in the IC input so each appended fragment lands after a separator
// (courtroom.cpp:2320-2326 re-inserts " " into ui_ic_chat_message after every
// send; on_additive_clicked at :6563-6573 prefaces the message with whitespace).
// We diverge on WHERE: AO2 mutates the visible input field, which is intrusive;
// we prepend at packet build so the user's typed line stays clean (recordSentIC /
// icPendingSent still read the untouched a.icInput). The result on the wire is
// identical, so other clients (and AO2) see the space and don't glue sentences.
//
// The check runs on the STRIPPED text because funColor may have prepended colour
// markup (\cr, \c#RRGGBB) — the raw first byte would then be '\', not the visible
// rune. A blankpost (" ", or a colourised "\cr ") strips to a leading space and is
// left untouched. AsyncAO receivers additionally de-glue on display (the joiner in
// begin()), and that dedup skips a fragment that already starts with a space, so
// AsyncAO->AsyncAO never double-spaces.
func AdditiveOutgoingLeadingSpace(text string) string {
	if !AdditiveWantsLeadingSpace(text) {
		return text // already separated (real leading space, or a blankpost)
	}
	return " " + text
}

// AdditiveWantsLeadingSpace reports whether AdditiveOutgoingLeadingSpace would
// actually prepend a space to text: true iff the STRIPPED text is non-empty, its
// first visible rune is not a space, and that rune is not from a no-space script.
// The send path needs this as a separate
// predicate because it must shift the transmitted effect spans by +1 in lockstep
// with the prepend — and the spans are baked into an invisible frame BEFORE the
// prepend happens (EncodeEffectsMarker runs first), so a single shared predicate
// is the only way the shift decision and the prepend can never disagree. Splitting
// it out (rather than duplicating the test at the call site) keeps that one source
// of truth. Stripping first matters: funColor may have prepended \cr/\c# markup, so
// the raw first byte would be '\', not the visible rune.
//
// The no-space suppression mirrors additiveNeedsJoiner's CJK decision on the one
// boundary rune the sender can see (its own fragment's first rune; the previous
// message's ending is only known to receivers): a half-width space glued to a
// fullwidth glyph reads wrong in JP/CJK RP on EVERY client, including the AO2
// ones this space exists to serve — so a CJK-leading fragment ships unspaced,
// and receivers with mixed boundaries decide with the full two-sided rule.
func AdditiveWantsLeadingSpace(text string) bool {
	stripped := StripChatMarkup(text)
	if stripped == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(stripped)
	return !unicode.IsSpace(first) && !isNoSpaceRune(first)
}

// additiveNeedsJoiner reports whether a single space should be inserted between
// the accumulated additive prefix and an incoming fragment. Both arguments are
// already StripChatMarkup'd (markup at the boundary must not hide the real
// visible rune). A joiner is wanted iff the prefix is non-empty, the prefix ends
// with a non-space rune, and the fragment starts with a non-space rune — i.e.
// neither side already supplies the separator (so an AO2 sender who DID insert
// the leading space never gets a doubled one).
//
// CJK/no-space scripts are suppressed: a space after "。" or "、" is
// typographically wrong in Japanese/Chinese RP (this client has JP-heavy users),
// and CJK scripts don't word-separate with spaces at all. If EITHER boundary rune
// is a no-space rune the joiner is dropped — a single half-width space glued to a
// fullwidth glyph on either side reads worse than the glue it would fix.
func additiveNeedsJoiner(prev, frag string) bool {
	if prev == "" || frag == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(prev)
	first, _ := utf8.DecodeRuneInString(frag)
	// utf8.RuneError guards: DecodeRune on "" returns RuneError, but the ""
	// checks above already excluded that. A genuine RuneError (invalid UTF-8)
	// counts as non-space, non-CJK — glue it, matching a raw byte join.
	if unicode.IsSpace(last) || unicode.IsSpace(first) {
		return false
	}
	if isNoSpaceRune(last) || isNoSpaceRune(first) {
		return false
	}
	return true
}

// isNoSpaceRune reports whether r belongs to a script or punctuation block that
// does not use inter-word spaces, so an inserted joiner would be wrong there.
// Covers the CJK Han/Hiragana/Katakana/Hangul scripts PLUS the two Unicode
// punctuation blocks AO2's JP users actually hit — CJK Symbols & Punctuation
// (U+3000-303F: 。、「」…, which are script=Common and so NOT in unicode.Han)
// and Halfwidth/Fullwidth Forms (U+FF00-FFEF: ！？，．… and fullwidth letters).
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
	// A new message takes the stage before anything below re-assigns the speaker
	// layers, so this is the last moment a still-running one-shot can be reported
	// as ended by THIS cause (AO2's unpack_chatmessage resets anim_state to 0).
	// Idempotent — nothing to report when no preanim was playing.
	c.endPreanim(PreanimEndNewMessage)
	c.current = msg
	c.waitFor = nil // beginning ends any armed wait-gate hold
	// Drop any SFX/shake still armed from the previous message so a superseded
	// one can't fire late over this one (#12). enterAfterShout re-arms below.
	c.sfxArmed, c.sfxLeft, c.sfxBase, c.sfxShake = false, 0, "", false
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
	// Filter the speaker's style through the viewer's accessibility opt-outs. The
	// SAME helper filters the pair partner's recalled style below, so a paired player
	// can never impose motion/glitch a viewer opted out of on either sprite.
	style = c.filterStyleForViewer(style)

	// 2.8 additive text (#14): when honored and this line is ADDITIVE=1, the prior
	// accumulated text becomes a pre-revealed prefix (typewriter crawls only the
	// tail). This mirrors AO2 exactly: additive_previous appends on ANY ADDITIVE=1
	// line with NO char-id gate (courtroom.cpp:4225-4330), and resets only when a
	// line is NOT additive (:4229) — a continuation may even come from a different
	// speaker. c.currentText is final here (center prefix stripped, inline emotes
	// expanded), so the accumulation matches what displays. Any per-message effect
	// spans are indexed over THIS message's text, so shift them past the prefix so
	// they still align in the concatenated MessageText. On a non-additive line the
	// prefix is "" and prev resets to this message — the old replace behavior. Off
	// (pref/restore) → also "" (RestoreMessage settles one message, so it must never
	// inherit a stale accumulation).
	c.additivePrefix = ""
	if c.AdditiveText && !c.restoring && msg.Additive {
		c.additivePrefix = c.additivePrevText
		// Auto-joiner: real AO senders glue the incoming fragment onto the prior
		// accumulated text with no separator, so two additive sentences read
		// "First.Second" instead of "First. Second". AO2's own client works around
		// this by inserting a leading space into the SENDER's input whenever the
		// additive box is on (courtroom.cpp:2320-2326, 6563-6573); clients that
		// don't (older AO2, webAO, ours before this fix) produce the glued form.
		// Fix it on the DISPLAY side too, for ANY sender: when the boundary visually
		// lacks a space, insert exactly one. Appending to additivePrefix HERE — before
		// the shift below — makes the joiner ride into the span shift, additivePrevText
		// accumulation, IsBlankPost, StartAppend pre-reveal, and MessageRaw with no
		// further edits. Guard on the STRIPPED text so markup at the boundary
		// (e.g. a "\c0" colour code) doesn't hide the real last/first visible rune.
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
			if !c.restoring { // settled form never shows the bubble — don't probe for it
				c.mgr.Prefetch(c.ShoutDefaultBase, assets.AssetTypeShoutBubble, network.PriorityHigh) // AssetType: ShoutBubble
			}
		}
		if !c.restoring {
			c.mgr.Prefetch(c.ShoutCharBase, assets.AssetTypeShoutBubble, network.PriorityHigh) // AssetType: ShoutBubble
		}
		c.audio.PlayShout(shoutSFX) // AssetType: SFX
	}

	// Predictive prefetch: warm the likely next speaker — and their likely
	// next emote (§10 step 3 + per-character emote chains).
	if c.Predictor != nil {
		c.Predictor.OnMessage(speakerName, msg.Pair.Name, msg.Emote)
	}

	blip := msg.Blipname
	if !validBlipName(blip) {
		blip = "" // sentinel / path-unsafe: fall through to char.ini, then the default
	}
	if blip == "" && c.BlipNameFor != nil {
		// webAO parity: senders that omit the wire field (pre-2.10.2 clients,
		// short packets) still blip with THEIR char.ini set, not the default.
		blip = c.BlipNameFor(speakerName)
		if !validBlipName(blip) {
			blip = "" // a char.ini is server bytes too — same one guard, not a second copy
		}
	}
	if blip == "" {
		blip = defaultBlipSet // get_blipname's last resort (text_file_functions.cpp:510)
	}
	// ONE mint for every blip URL in the client (blipurl.go): the guard that
	// rejects sentinels, and AO2's get_blips ladder — the modern sounds/blips/
	// set, AO1's sounds/general/sfx-blip<name>, and the plain general sound —
	// each in the lowercase identity casing then the authored one.
	// AssetType: Blip
	c.blipRef = c.urls.BlipRef(blip)
	c.mgr.PrefetchChain(c.blipRef.Base, c.blipRef.Alts, c.blipRef.Type, network.PriorityHigh)

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

	// The emote SFX is NOT played here — it's armed on a deadline in
	// enterAfterShout so it lands SFX_DELAY × 40ms into the message (mid-preanim),
	// matching AO2's sfx_delay_timer (#12; ../AO2-Client/src/courtroom.cpp:4052).

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
		c.prefetchScenery(c.sess.Background, bgPart)
		c.mgr.Prefetch(c.Scene.DeskBase, assets.AssetTypeDeskOverlay, network.PriorityHigh) // AssetType: DeskOverlay
	}
	// Initial desk state = the talk/idle column (a message with no preanim goes
	// straight to talk; a shout holds the stage without touching the desk). The
	// phase transitions (enterAfterShout preanim entry, startTalking) call
	// applyDeskMods to flip it per phase — AO2's set_scene at preanim_start /
	// start_chat_ticking. Pair/offset are set below from the message.
	// DeskDrawn (not bare deskVisible) so a background known to ship no desk for
	// this position stays hidden — AO2's set_scene existence check (#44).
	c.Scene.ShowDesk = DeskDrawn(msg.DeskMod, false, c.deskResolution())

	// CLEAR SITE 1 of 3 (overlayfx.go): AO2 stops and hides ui_vp_effect inside
	// display_character (courtroom.cpp:2755-2761), i.e. the moment the next IC
	// message stages its emote — which is exactly here. fireMessageEffects runs
	// LATER (enterAfterShout), so a message that carries an effect re-arms it
	// after this clear, and one that does not leaves the stage clean. There is
	// deliberately no clear at message END.
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

	// #17 networked frame effects: parse this message's FRAME_* fields into the
	// per-frame trigger table ONCE here (never per render frame — the render loop
	// is zero-allocation). Built after the Speaker bases above so a later
	// NotifyFrameShown can bind the active layer to its group. A message with no
	// FRAME_* data yields an inert (empty) table. A restore/settled replay never
	// animates a preanim, but a live talk/idle loop still can, so the table is
	// built the same way; the cursors start at 0 and only NotifyFrameShown moves
	// them.
	c.frameTriggers = c.buildFrameTriggers(msg)

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
			// The wire never carries the partner's style (protocol.PairInfo has no
			// style field — pairing.go), so recall it by the partner's char id, the
			// SAME send-on-change memory the speaker path uses. Without this only the
			// current speaker showed a style, flipping between the two as the turn
			// passed. RecalledStyle returns the zero (inactive) style for a negative
			// char id, and filterStyleForViewer honours the viewer's opt-outs for the
			// pair sprite too.
			Style:   c.filterStyleForViewer(c.RecalledStyle(msg.Pair.CharID)),
			Scaling: c.scalingFor(msg.Pair.Name),
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
	// animated blankpost's preanim. Under additive (#14) the DISPLAYED text is the
	// accumulated prefix + tail, so a continuation with real prior text is never a
	// blankpost even when its own tail is whitespace.
	c.Scene.IsBlankPost = strings.TrimSpace(StripChatMarkup(c.additivePrefix+c.currentText)) == ""
	c.preanimDone = false
	// A new message takes the stage: any previous message's immediate-mode
	// decoration is over (AO2's unpack_chatmessage resets anim_state to 0). The
	// speaker layers are re-assigned above, so only the countdown needs clearing.
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

// beginCaughtUp shows a backlog message's text for ~one frame with no
// shout/preanim/typewriter/effects/sfx/prefetch, then lingers briefly so the
// next dequeues. It drains a deep queue toward real-time; the newest message
// (played in full by begin) sets the real scene, and the IC log already has
// every message's text. Speaker sprite is intentionally left as-is — a
// one-frame backlog flash is never seen.
func (c *Courtroom) beginCaughtUp(msg *protocol.ChatMessage) {
	// A fast-forwarded backlog message plays no ceremony (no preanim/sfx/effects),
	// so it must carry no frame triggers either — clear any left from the prior
	// message so a later NotifyFrameShown can't fire them over this flash (#17).
	c.frameTriggers = frameTriggerTable{}
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
	// A caught-up (fast-forwarded) message breaks the additive chain: treat it as a
	// replace (empty prefix) and seed the accumulator with its own text so a later
	// ADDITIVE=1 line continues from THIS message, not pre-catch-up text (#14).
	c.additivePrefix, c.additivePrevText = "", c.currentText
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
	// The emote SFX arms HERE (AO2 starts sfx_delay_timer inside play_preanim,
	// courtroom.cpp:4054 — before the preanim's own art is even checked), but the
	// message EFFECTS do not: do_effect lives in start_chat_ticking (:4154-4172), which
	// a blocking preanim only reaches through preanim_done → handle_ic_speaking
	// (:4108-4118 → :3506). So the overlay, its sound and the legacy flash/shake fire
	// when the TEXT starts — see startTalking.
	if preanimSFXPlays(msg) {
		c.armSFXDelay(msg)
	}
	// !preanimDone: begin() resets the flag, so it can only be true here when
	// NotifyAssetMissing landed while the shout bubble held the stage — the
	// preanim conclusively 404'd and hijacking Active with it would draw a
	// blank speaker. Skip straight to the talk loop, exactly where a played
	// preanim would have ended up.
	//
	// !spriteConfirmedMissing: preanimDone is per-message (begin() clears it), so
	// it does NOT catch a preanim that 404'd on an EARLIER play of the same emote —
	// its base is in the session negative cache, so re-selecting it here would park
	// Active on a base the render side has already marked missing (store.IsMissing),
	// and the viewport would FLASH the missingno placeholder for the frames before
	// the async re-probe's Warning drains and Update advances the phase. AO2-Client
	// stats the preanim locally and skips a missing one before it ever draws
	// (courtroom.cpp play_preanim); a streaming client can't stat, but a base we
	// ALREADY know is absent is the same certainty — skip it synchronously here so
	// the placeholder never paints for a preanim we're about to skip anyway. IDLE/
	// TALK bases keep their (wanted) missingno; only the preanim selection is gated.
	playPre := preanimWillPlay(msg) && !c.preanimDone && !c.spriteConfirmedMissing(c.Scene.Speaker.PreanimBase)
	blockOnPre := playPre && !msg.Immediate &&
		(msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom)

	if playPre {
		c.Scene.Speaker.Active = c.Scene.Speaker.PreanimBase
		c.Scene.Speaker.PlayOnce = true
	}
	if blockOnPre {
		// A blocking preanim holds the stage — apply the PREANIM desk column
		// (AO2 set_scene at preanim_start, courtroom.cpp:4075-4091). Immediate-mode
		// preanims fall through to startTalking, where the talk column wins (AO2
		// calls preanim_start then start_chat_ticking).
		c.applyDeskMods(true)
		c.phase = PhasePreanim
		c.timer = c.PreanimTimeout // power-user knob; seeded to DefaultPreanimTimeout
		return
	}
	c.startTalking()
}

// preanimSFXPlays reports whether this message's emote SFX may play at all — the
// gate AO2 puts on the whole sfx_delay_timer, not on the sound itself.
//
// CANON. play_sfx (courtroom.cpp:4590-4607) is wired to exactly one trigger,
// sfx_delay_timer (:432), and that timer is started in exactly one place:
// play_preanim (:4054). handle_emote_mod reaches play_preanim only for
//
//	PREANIM / PREANIM_ZOOM              → play_preanim(false)   (:2892-2896)
//	IDLE / ZOOM *with* immediate ticked → play_preanim(true)    (:2897-2910)
//
// so a plain IDLE/ZOOM message NEVER plays its SFX_NAME in AO2, whatever the field
// carries. That is the receive half of the field report: AsyncAO armed the delay for
// every message, so a "SFX: auto" line sent with Pre unchecked still fired the emote's
// char.ini sound.
//
// Note where the start sits: :4054 is BEFORE the `file_exists(anim_to_find)` check at
// :4056, so a preanim whose ART is missing still plays the sound. The gate is the emote
// MOD, never whether the sprite resolved — which is why this deliberately does not reuse
// enterAfterShout's `playPre` (that one also consults hasPreanim / the missing-sprite
// cache, both of which are about what to DRAW).
func preanimSFXPlays(msg *protocol.ChatMessage) bool {
	return msg.EmoteMod == protocol.EmoteModPreanim ||
		msg.EmoteMod == protocol.EmoteModPreanimZoom ||
		msg.Immediate
}

// armSFXDelay schedules the message's emote SFX and (for preanim mods) its
// screenshake to fire SFX_DELAY × 40ms into the message, matching AO2's
// sfx_delay_timer started after the shout (#12; ../AO2-Client/src/courtroom.cpp
// :4052-4054). AO2's play_sfx does the screenshake for a preanim message BEFORE
// the sfx_name=="1" early-return (courtroom.cpp:4593-4596 do_screenshake), so
// the shake fires at the deadline even when there's no audible SFX (name "0"/
// "1"/empty). Idle/zoom shakes already fire immediately in fireMessageEffects
// and are a DISJOINT emote-mod set, so there's no double-fire. Nothing is armed
// when there is neither an SFX to play nor a preanim shake to fire — a zero-
// delay SFX still routes through the deadline (fires on the first Update tick).
func (c *Courtroom) armSFXDelay(msg *protocol.ChatMessage) {
	base := ""
	if msg.SFXName != "" && msg.SFXName != "0" && msg.SFXName != "1" &&
		(c.SFXMuted == nil || !c.SFXMuted(msg.SFXName)) { // M11: per-SFX mute
		base = c.urls.SFX(msg.SFXName)
	}
	// The preanim screenshake (AO2 play_sfx) fires for preanim-mod messages when
	// SCREENSHAKE=1; effectsVisible gates the visual (reduce-motion / toggle).
	preanimMod := msg.EmoteMod == protocol.EmoteModPreanim || msg.EmoteMod == protocol.EmoteModPreanimZoom
	shake := msg.Screenshake && preanimMod && c.effectsVisible()
	if base == "" && !shake {
		return // nothing to schedule
	}
	c.sfxArmed = true
	c.sfxLeft = time.Duration(msg.SFXDelay) * sfxDelayUnit
	c.sfxBase = base
	c.sfxShake = shake
}

// fireSFXDelay plays the armed SFX and fires the armed preanim screenshake, then
// disarms. Called from Update when the deadline elapses.
func (c *Courtroom) fireSFXDelay() {
	if c.sfxBase != "" {
		c.audio.PlaySFX(c.sfxBase, 0) // AssetType: SFX (deadline reached; delay already elapsed)
	}
	if c.sfxShake {
		c.Scene.ShakeLeft = ScreenshakeDuration
	}
	c.sfxArmed, c.sfxLeft, c.sfxBase, c.sfxShake = false, 0, "", false
}

// fireMessageEffects triggers the message-display effects exactly where
// AO2-Client's start_chat_ticking does (courtroom.cpp:4154): the 2.8 Effects
// field wins ("fx", "fx|sound" or "fx|folder|sound") and SUPPRESSES the legacy
// realization path entirely (it is an `else if`, :4173-4177); plain
// REALIZATION=1 is that fallback flash; SCREENSHAKE=1 shakes for IDLE/ZOOM
// emote mods.
//
// The named effect now drives a real OVERLAY (do_effect). The flash/shake
// name mapping below survives as the fallback for when the overlay does NOT
// resolve — no theme effects.ini on disk, no origin pack, no engine wired — so a
// bare install still reacts to "realization"/"screenshake" the way it always
// has. When the overlay DOES resolve it owns the visual, and the white flash is
// not fired on top of the theme's own realization art.
func (c *Courtroom) fireMessageEffects(msg *protocol.ChatMessage) {
	// Reduce-motion gates the VISUAL effects only — the feedback sounds still
	// play (accessibility: kill the shake/flash, keep the audio cue).
	if msg.Effects != "" {
		fx, folder, sound := parseEffectsField(msg.Effects)
		if folder == "" {
			// 1- and 2-part fields omit it: fall back to the SENDER's own char.ini
			// (text_file_functions.cpp:836-839). Free — charMeta already has it.
			folder = c.OverlayEffectsFolderFor(msg.CharName)
		}
		// The overlay when it resolves, the legacy flash/shake mapping when it does
		// not — one helper (overlayfx.go) so a resolve that had to WAIT for its
		// roster settles the message identically (tickOverlayPending re-asks).
		c.applyEffectField(fx, folder, msg.Flip, msg.SelfOffsetX, msg.SelfOffsetY)
		// The sound plays whenever the field names one, INCLUDING when the art did
		// not resolve and when the visual gate is off — canon's own ordering
		// (courtroom.cpp:3196 precedes the effectsEnabled test at :3202).
		if OverlaySoundPlayable(sound) {
			c.audio.PlaySFX(c.urls.SFX(sound), 0) // AssetType: SFX (2.8 effect sound)
		}
	} else if msg.Realization {
		if c.effectsVisible() {
			c.Scene.FlashLeft = RealizationFlashDuration
		}
		// The SENDER's own realization sound wins over the theme's, exactly as canon
		// plays get_custom_realization(CHAR_NAME) here rather than get_court_sfx
		// (courtroom.cpp:4175 → text_file_functions.cpp:896-903).
		if sfx := c.realizationSFXFor(msg.CharName); sfx != "" {
			c.audio.PlaySFX(sfx, 0) // AssetType: SFX (realization)
		}
	}
	if c.effectsVisible() && msg.Screenshake && (msg.EmoteMod == protocol.EmoteModIdle || msg.EmoteMod == protocol.EmoteModZoom) {
		c.Scene.ShakeLeft = ScreenshakeDuration
	}
}

// effectsVisible reports whether authored screen effects (shake/flash) should
// render: the user's ScreenEffects toggle is on AND accessibility ReduceMotion
// isn't suppressing motion. Effect SOUNDS ignore this — they always play.
func (c *Courtroom) effectsVisible() bool { return c.ScreenEffects && !c.ReduceMotion }

// fireInlineEffect fires a \s/\f screen effect the typewriter reached during the
// crawl. Kept courtroom-side (Scene mutation) so the pure typewriter stays
// SDL-free; the ScreenEffects toggle and reduce-motion both gate the visual,
// matching fireMessageEffects' field-based path (purely visual, no sound to keep).
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

// startTalking begins the typewriter reveal. It IS AO2's start_chat_ticking
// (courtroom.cpp:4120-4261), so everything canon does there happens here.
func (c *Courtroom) startTalking() {
	// Talk/idle desk column (AO2 set_scene at start_chat_ticking,
	// courtroom.cpp:4134-4152). Restores the pair/offset a preanim's mod-4 hide
	// may have zeroed, and flips mod-2/3 desk visibility for the talk phase.
	c.applyDeskMods(false)
	// The 2.8 EFFECTS field — the overlay, its sound, and the legacy
	// realization/screenshake fallback — fires HERE, not at message start.
	// start_chat_ticking runs do_effect at :4154-4172, immediately after that same
	// set_scene block, and a BLOCKING preanim only reaches start_chat_ticking through
	// preanim_done → handle_ic_speaking (:4108-4118 → :3506). Firing it in
	// enterAfterShout instead put a "realization" flash and the effect's sound at the
	// START of a preanim, one whole preanim ahead of where AO2 puts them. Idle
	// messages are unaffected: for them enterAfterShout falls straight through to here.
	if c.current != nil {
		c.fireMessageEffects(c.current)
	}
	// 2.8 additive (#14): an ADDITIVE=1 continuation starts with the prior
	// accumulated text pre-revealed (StartAppend), so only the appended tail crawls
	// + blips; a plain message starts from empty. MessageRaw is the concatenation so
	// the raster cache keys on the full displayed text.
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
		// Immediate mode: the preanim keeps playing over the text (Active stays on
		// PreanimBase) on its OWN countdown, not the phase timer — the message is
		// free to finish and hand the stage to the next one while this animation is
		// still running (see tickImmediatePreanim). Bounded so a slow-decoding or
		// never-arriving preanim can't leave Active parked on it forever;
		// NotifyPreanimStarted extends it to the real duration once the decoded
		// preanim plays, exactly like the blocking PhasePreanim path.
		c.imPreLeft = c.PreanimTimeout
	}
	// Skip the talking phase only when there is genuinely nothing left to do:
	// no text still to reveal and no inline \s/\f mark still to fire. The effects
	// clause is load-bearing for a message that is ONLY effect codes ("\f",
	// "\s\f"): it strips to zero runes, so Done() is true from the first instant,
	// and without EffectsPending this jumped straight to linger — PhaseTalking (the
	// only place NextEffect is drained, see Update) never ran, so a bare effect
	// silently did nothing while the SAME code attached to text worked. AO2 has
	// no such hole because its emptiness test reads the RAW wire text
	// (start_chat_ticking, ../AO2-Client/src/courtroom.cpp:4183) and a raw "\f"
	// is not empty, so chat_tick walks the escape and calls do_flash (:4442-4448).
	// One extra Update tick is all this costs: that tick drains every mark at
	// position 0 and then falls through to enterLinger below, so the message
	// still settles immediately. Purely a lifecycle decision — the visual is
	// still gated by effectsVisible inside fireInlineEffect, and Scene.IsBlankPost
	// is untouched, so an effect-only message keeps hiding the chatbox exactly
	// like any other markup-only message (see begin).
	//
	// An immediate-mode preanim does NOT keep this open: AO2's blank-post arm of
	// start_chat_ticking starts the queue timer right there (courtroom.cpp:
	// 4208-4213) with the preanim still playing behind it.
	if c.Typewriter.Done() && !c.Typewriter.EffectsPending() {
		c.enterLinger()
		return
	}
	c.phase = PhaseTalking
}

// enterLinger settles the message: text done, TextStay counting down to the
// queue advance.
//
// It leaves an IMMEDIATE-mode preanim alone (imPreLeft > 0). That animation is
// decoration AO2 plays over an already-ticking message — anim_state 4 — and its
// lifetime is owned by tickImmediatePreanim, which parks the speaker on the idle
// sprite when it ends. Clearing PlayOnce here instead would cut the animation
// off at text completion, which is neither AO2's behavior nor the point of
// ticking "immediate".
func (c *Courtroom) enterLinger() {
	if c.imPreLeft <= 0 {
		c.Scene.Speaker.Active = c.Scene.Speaker.IdleBase
		c.endPreanim(PreanimEndTextSettled)
	}
	c.phase = PhaseLinger
	c.timer = c.TextStay
}

// tickImmediatePreanim advances the decoration an immediate-mode message started
// and ends it when the animation finishes (NotifyPreanimDone) or its bound
// expires. It runs OUTSIDE the phase switch because the animation deliberately
// outlives its phase: AO2 leaves anim_state at 4 and lets the layer play on
// while text_state reaches 2 and the queue timer runs.
//
// The speaker lands wherever the message currently is: still crawling → the talk
// loop; settled → idle. Both are what the next Update of that phase would have
// drawn anyway.
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
	// preanimDone here means the render reported the last frame; otherwise the
	// bound above ran out first — the two reasons the interrupt audit needs to
	// tell apart.
	if c.preanimDone {
		c.endPreanim(PreanimEndFinished)
	} else {
		c.endPreanim(PreanimEndTimeout)
	}
	if c.phase == PhaseTalking {
		// Text still crawling: flap the talk sprite for the rest (it used to
		// freeze on the last preanim frame).
		c.Scene.Speaker.Active = c.Scene.Speaker.TalkBase
		return
	}
	c.Scene.Speaker.Active = c.Scene.Speaker.IdleBase
}

// NotifyPreanimDone is called by the render side when a one-shot animation
// finishes (or by tests). The next Update releases the one-shot and flips the
// speaker to the talk loop.
//
// GUARDED, exactly like its sibling NotifyPreanimStarted below, and for the same
// reason: `a.d.Viewport.OnPreanimDone` is ONE callback slot on a SHARED viewport
// that internal/ui re-points at whichever room currently drives it — the live
// room, the split room, the replay room, the scene-maker preview (ui/replay.go,
// ui/scenemaker.go, ui/gifexport.go each swap it and swap it back). A completion
// fired across one of those swaps used to land on a room that had no one-shot on
// stage at all and set the flag anyway; because preanimDone is sticky until the
// next begin(), the CURRENT message then inherited it — enterAfterShout reads it
// and skips the preanim outright (its `!c.preanimDone` term), PhasePreanim exits
// on the first Update, and tickImmediatePreanim ends the decoration on tick one.
// That is a "the preanimation is randomly interrupted" with no local cause, which
// is why it never reproduced on demand.
//
// PlayOnce is the precise test: it is set synchronously by enterAfterShout the
// moment this room selects a preanim to play and cleared by endPreanim the moment
// the animation is over, so "PlayOnce is up" is exactly "this room has a one-shot
// on stage that a done-report can be about". Every in-tree caller reports while it
// is up, so no existing expectation moves.
func (c *Courtroom) NotifyPreanimDone() {
	if !c.Scene.Speaker.PlayOnce {
		return // no one-shot on stage here — a stale report from a shared viewport
	}
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
	want := total + preanimPlaybackSlack
	// An IMMEDIATE-mode preanim runs on its own countdown (imPreLeft), because it
	// is allowed to outlive the message. Extend THAT, not the phase timer — the
	// phase timer is by then the linger/queue-advance clock, and lengthening it
	// would re-introduce the very stall this decoupling removed.
	if c.imPreLeft > 0 {
		if want > c.imPreLeft {
			c.imPreLeft = want
		}
		return
	}
	// PhasePreanim = a BLOCKING preanim wait: the phase timer IS the bound, and a
	// long decoded preanim must play in full.
	if c.phase != PhasePreanim {
		return
	}
	if want > c.timer {
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
	if base == "" {
		return
	}
	// Remember every conclusively-missing base FIRST: the wait gate (waitHolds)
	// holds a message BEFORE it is current, so a dummy preanim's 404 lands here
	// while c.current is still the previous message and would be dropped by the
	// guard below. Recording it lets the gate release on the miss signal.
	c.recordMissing(base)
	if c.current == nil || base != c.Scene.Speaker.PreanimBase {
		return
	}
	// Exactly NotifyPreanimDone: the preanim "finished" (it can never start).
	// PhasePreanim exits on the next Update; enterAfterShout reads the flag
	// too, so a miss learned during the shout bubble skips the phase outright.
	c.preanimDone = true
	// Swap Active OFF the missing preanim base SYNCHRONOUSLY, in the same drain in
	// which the render side marks it missing (store.MarkMissing runs beside this in
	// drainWarnings). Leaving Active on the preanim base and relying on "the next
	// Update" opens a one-frame placeholder flash on any draw driver that draws
	// WITHOUT calling this room's Update first — the audio-paced present cycle
	// (roomPreAdvanced skips Update but still draws), a minimized→restored frame, or
	// a split/replay/maker room advanced on its own cadence. Both the blocking
	// preanim (PhasePreanim, Active parked on the preanim, PlayOnce set) and the
	// immediate-mode preanim (PhaseTalking, Active on the preanim over the crawl,
	// PlayOnce set) must drop to the talk sprite here — exactly what their next
	// Update would do (PhasePreanim → startTalking sets Active=TalkBase since
	// PlayOnce is now false; PhaseTalking's PlayOnce branch already flaps to
	// TalkBase). A leftover missingno flash is never useful for a preanim we're
	// skipping anyway. The draw-side guard (viewport drawSprite) is the belt-and-
	// braces for the residual frame before ANY Update on paths this can't reach.
	if c.Scene.Speaker.PlayOnce {
		c.endPreanim(PreanimEndMissing)
		c.imPreLeft = 0 // an immediate-mode decoration that can never play
		if c.phase == PhaseTalking || c.phase == PhasePreanim {
			c.Scene.Speaker.Active = c.Scene.Speaker.TalkBase
		}
		// A blocking preanim also parked the PREANIM desk column at entry
		// (applyDeskMods(true), :1220). Its deferred exit is PhasePreanim →
		// startTalking → applyDeskMods(false), which flips ShowDesk/PairActive/
		// offset back to the talk column. Mirror that flip HERE so the synchronous
		// frame is identical to the deferred one for ALL desk mods — otherwise mods
		// 2-5 (DESK/PRE-ONLY[+EX]) render one frame with Active correctly on
		// TalkBase but the desk/pair-hide/offset still on the preanim column on the
		// very Update-skipping draw drivers this swap exists for. Idempotent
		// (recomputes from c.current, non-nil past the :1432 guard). The immediate-
		// mode (PhaseTalking) arm is already on the talk column — its startTalking
		// ran applyDeskMods(false) at entry — so only PhasePreanim needs it.
		if c.phase == PhasePreanim {
			c.applyDeskMods(false)
		}
	}
}

// recordMissing remembers a conclusively-404'd base so the wait gate can treat a
// declared-but-absent sprite as "nothing to wait for". Bounded (missingSpritesCap)
// and game-thread only (called from the App's warning drain, like the rest of
// NotifyAssetMissing).
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

// spriteConfirmedMissing reports whether base has been conclusively 404'd this
// session. The wait gate reads it so a never-arriving preanim releases the hold
// on the miss signal instead of the full timeout.
func (c *Courtroom) spriteConfirmedMissing(base string) bool {
	_, ok := c.missingSprites[base]
	return ok
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

// RestoreMessage re-stages a previously-played message in its SETTLED form:
// idle sprite, full text revealed, phase back at idle — the state a live
// watcher would have ended on. A rebuilt room (tab reactivation, re-entering
// court, pinning a background tab) seeds it from Session.LastIC so the stage
// isn't blank until the next MS. The ceremony is skipped wholesale: audio is
// diverted to NopAudio for the replay (the shout/SFX/blips already played when
// the message was live — or landed off screen, where they must stay silent),
// catch-up is bypassed (beginCaughtUp leaves the sprite as-is, which on a
// fresh room means no speaker at all), and the message's one-shot screen
// effects (flash/shake) are cleared rather than re-fired. Prefetches for the
// art the settled scene DOES show (idle/talk/pair sprites, scenery, chat skin)
// still fan out at HIGH — they're on screen NOW — while the never-drawn shout
// bubble and preanimation probes are skipped (c.restoring). Callers push the
// viewer knobs (ForceCharNames / HideSpriteStyles / ReduceMotion) BEFORE
// calling: begin() bakes them into the scene. Intended for a freshly built
// room; on a mid-message room it replaces the current message like any begin.
func (c *Courtroom) RestoreMessage(msg *protocol.ChatMessage) {
	if msg == nil {
		return
	}
	savedAudio, savedCatchUp := c.audio, c.CatchUp
	c.audio, c.CatchUp, c.restoring = NopAudio{}, false, true
	c.begin(msg)
	c.SkipToIdle()
	c.audio, c.CatchUp, c.restoring = savedAudio, savedCatchUp, false
	// SkipToIdle's hour-sized steps already count any begin-fired flash/shake
	// down to zero; clear explicitly so the no-replayed-effects invariant
	// doesn't hinge on Update's countdown-before-phase ordering.
	c.Scene.FlashLeft, c.Scene.ShakeLeft = 0, 0
	// begin() rebuilt frameTriggers (#17) with cursors at 0, but a restore is a
	// SETTLED replay: the idle sprite now loops on the real (restored) audio sink,
	// so any idle-phase FRAME_* trigger would fire its SFX/flash/shake on tab-back /
	// court re-entry / pin — a replay violating the no-sound/flash-replay restore
	// invariant. Clear the table like beginCaughtUp does so NotifyFrameShown stays
	// inert for the restored message.
	c.frameTriggers = frameTriggerTable{}
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
	// A screen effect whose misc roster was still resolving when the message fired
	// re-asks here until it lands (overlayfx.go). Inert otherwise.
	c.tickOverlayPending(dt)
	// Delayed emote SFX + preanim screenshake (#12): fire at SFX_DELAY × 40ms.
	// Runs independent of the phase machine (armed in enterAfterShout, cleared in
	// begin), so a delay that outlasts the preanim still lands during the talk
	// loop, exactly as AO2's sfx_delay_timer does.
	if c.sfxArmed {
		c.sfxLeft -= dt
		if c.sfxLeft <= 0 {
			c.fireSFXDelay()
		}
	}
	// An immediate-mode preanim is decoration, not occupancy: it advances outside
	// the phase switch so it can keep playing across linger and into idle, exactly
	// as AO2 leaves anim_state at 4 while the message settles.
	c.tickImmediatePreanim(dt)
	switch c.phase {
	case PhaseShout:
		c.timer -= dt
		if c.timer <= 0 {
			c.enterAfterShout()
		}

	case PhasePreanim:
		c.timer -= dt
		if c.preanimDone || c.timer <= 0 {
			// Which of the two fired is exactly the interrupt audit's question: a
			// TIMEOUT on a preanim that decoded means the bound pre-empted a real
			// animation, a FINISHED means it played out.
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
		// Inline \s/\f codes fire the instant the crawl reaches them (AO2-Client
		// chat_tick parity); a skip drops any it hasn't reached (SkipToEnd).
		for {
			m, ok := c.Typewriter.NextEffect()
			if !ok {
				break
			}
			c.fireInlineEffect(m)
		}
		for i := 0; i < blips; i++ {
			c.audio.PlayBlip(c.blipRef.Base) // AssetType: Blip
		}
		// Text completion ends the message's OCCUPANCY, full stop — an
		// immediate-mode preanim still playing over the box does not extend it.
		// AO2 starts text_queue_timer (the queue advance) from chat_tick the
		// instant the text finishes (../AO2-Client/src/courtroom.cpp:4332-4338, and
		// :4208-4213 for a blank post) with anim_state still 4, i.e. with the
		// immediate preanim mid-playback. This used to hold the whole queue for the
		// preanim's length, which is precisely what ticking "immediate" asks the
		// client NOT to do; tickImmediatePreanim now owns the animation's lifetime.
		if c.Typewriter.Done() {
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

// deskVisible reports whether the desk is drawn for a desk mod in a given phase.
// AO2 flips the EX/pre/emote modes between the preanim and the talk/idle phase
// (courtroom.cpp:4075-4091 preanim vs :4134-4152 talk); this table is exactly
// those two switch statements:
//
//	mod                         preanim   talk/idle
//	0 DESK_HIDE                 hidden    hidden
//	1 DESK_SHOW                 shown     shown
//	2 DESK_EMOTE_ONLY           hidden    shown    (desk only while talking)
//	3 DESK_PRE_ONLY             shown     hidden   (desk only during the preanim)
//	4 DESK_EMOTE_ONLY_EX        hidden    shown
//	5 DESK_PRE_ONLY_EX          shown     hidden
//
// The old collapse (one static bool per message) wrongly showed the desk during a
// mod-2 preanim and hid it during a mod-3 preanim.
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

// DeskResolution is what a streaming client currently knows about the desk image
// for the live background+position pair. AO2-Client answers this synchronously
// with file_exists (../AO2-Client/src/courtroom.cpp:4628); we can only answer it
// as the one prefetch we already issued resolves, so "not yet known" is a state.
type DeskResolution int

const (
	// DeskUnresolved: the desk image has neither landed nor been declared absent
	// (still streaming, or never asked for). The scenery already on screen is
	// held exactly as before — a real desk must never blink off mid-load.
	DeskUnresolved DeskResolution = iota
	// DeskResolved: the desk image for THIS background+position is resident.
	DeskResolved
	// DeskAbsent: every configured format for the desk 404'd
	// (assets.Manager.reportMissing → the App's warning drain → NotifyDeskMissing).
	DeskAbsent
)

// DeskDrawn is the whole desk-visibility decision: the char.ini desk_mod phase
// table AND the streamed availability of the desk image.
//
// AO2-Client's set_scene computes show_desk from desk_mod (courtroom.cpp:4075-4091
// preanim / :4134-4152 talk) and then CLEARS it outright when the position's desk
// file does not exist (:4628-4634), hiding the layer (:4656-4663). Note the
// asymmetry with the background arm immediately above it (:4613-4626), which DOES
// fall back to "wit": the desk deliberately has no fallback, because an author
// suppresses a desk by simply not shipping the file. So a background that ships no
// desk for a position must draw NO desk — never a substitute, never the previous
// room's (#44).
func DeskDrawn(deskMod int, preanim bool, res DeskResolution) bool {
	if res == DeskAbsent {
		return false
	}
	return deskVisible(deskMod, preanim)
}

// deskResolution reports what this room knows about the live Scene.DeskBase.
// Only DeskAbsent changes the outcome of DeskDrawn, so a base we have not been
// told about is simply DeskUnresolved. Game-thread only: a map read plus, where
// the room was wired one, the same T1 residency probe the sprite wait-gate uses.
func (c *Courtroom) deskResolution() DeskResolution {
	if c.Scene.DeskBase == "" {
		return DeskUnresolved
	}
	// RESIDENT beats a remembered miss, and clears it. missingDesks used to be
	// insert-only, so a desk that 404'd once stayed absent for the life of the
	// Courtroom even after it actually arrived — a server repack, a local mount
	// added, or a format learned. The render side already self-heals
	// (TextureStore.clearMissing on upload), so the two disagreed: the texture was
	// there and ShowDesk still said no. This is also what makes DeskResolved a
	// real state rather than a declared-but-unreachable one.
	//
	// SpriteReady is the same same-thread T1 map probe the sprite wait-gate uses,
	// and it is nil in rigs that never wired it — an unwired room simply keeps the
	// old two-state answer.
	if c.SpriteReady != nil && c.SpriteReady(c.Scene.DeskBase) {
		delete(c.missingDesks, c.Scene.DeskBase)
		return DeskResolved
	}
	if _, ok := c.missingDesks[c.Scene.DeskBase]; ok {
		return DeskAbsent
	}
	return DeskUnresolved
}

// NotifyDeskMissing reports that the desk image for a background+position
// conclusively 404'd (every configured format probed). Called from the App's
// warning drain on the game/render thread, exactly like NotifyAssetMissing, and
// costs no extra probe — it rides the Warning the desk prefetch already emitted.
//
// One Manager serves every room, so bases for OTHER rooms arrive here too; the
// Scene.DeskBase compare makes those a string-compare no-op (the wrong-room
// contract NotifyAssetMissing already follows).
func (c *Courtroom) NotifyDeskMissing(base string) {
	if base == "" {
		return
	}
	c.recordMissingDesk(base)
	// Re-derive the LIVE scene so the current message reacts immediately instead
	// of waiting for the next phase edge (a settled message has no further edge —
	// the desk would stay wrongly visible for the rest of the message).
	// applyDeskMods recomputes from c.current and is idempotent.
	if c.current != nil && base == c.Scene.DeskBase {
		c.applyDeskMods(c.phase == PhasePreanim)
	}
}

// recordMissingDesk remembers a conclusively-404'd desk base. Bounded
// (missingDesksCap) and game-thread only, mirroring recordMissing's
// stop-inserting-when-full policy.
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

// applyDeskMods re-derives the phase-dependent desk state from the CURRENT
// message for the given phase (preanim vs talk/idle). It always recomputes from
// c.current — never cumulatively mutating Scene — so a phase that shows the pair
// or restores an offset does so from the message, not from prior Scene state.
//
// Pair-hide and offset-zero are DECOUPLED because AO2 handles them independently
// (they are NOT symmetric across the two EX modes).
//
// Mod 4 (DESK_EMOTE_ONLY_EX): play_preanim hides the sideplayer + move(0,0)
// (courtroom.cpp:4076-4082); start_chat_ticking then calls set_self_offset ONLY —
// it restores the speaker offset but never re-shows ui_vp_sideplayer_char
// (courtroom.cpp:4135-4139). So the pair is hidden through BOTH phases and only the
// offset comes back in talk.
//
// Mod 5 (DESK_PRE_ONLY_EX): the sideplayer/offset stay put through the preanim
// (set_scene(true) fall-through, courtroom.cpp:4085-4089), and only talk hides the
// sideplayer + move(0,0) (courtroom.cpp:4145-4146). So the pair is hidden and the
// offset zeroed in TALK only, both restored in the preanim.
func (c *Courtroom) applyDeskMods(preanim bool) {
	if c.current == nil {
		return
	}
	msg := c.current
	c.Scene.ShowDesk = DeskDrawn(msg.DeskMod, preanim, c.deskResolution())
	// Pair visibility. Mod 4 hides it in both phases (never re-shown by AO2); mod 5
	// hides it in talk only. Otherwise it comes straight from the message.
	hidePair := msg.DeskMod == protocol.DeskEmoteOnlyEx ||
		(!preanim && msg.DeskMod == protocol.DeskPreOnlyEx)
	c.Scene.PairActive = msg.Pair.Active() && !hidePair
	// Speaker offset. Zeroed only in the phase AO2 does move(0,0): mod 4 during the
	// PREANIM (restored to the message offset in talk), mod 5 during TALK/idle.
	zeroOffset := (preanim && msg.DeskMod == protocol.DeskEmoteOnlyEx) ||
		(!preanim && msg.DeskMod == protocol.DeskPreOnlyEx)
	if zeroOffset {
		c.Scene.Speaker.OffsetX, c.Scene.Speaker.OffsetY = 0, 0
	} else {
		c.Scene.Speaker.OffsetX, c.Scene.Speaker.OffsetY = msg.SelfOffsetX, msg.SelfOffsetY
	}
}

// displayName is the chatbox name plate for a message — the shared
// DisplayName chain (displayname.go) bound to this room's viewer settings.
// The IC log drives the SAME function, which is the whole point of it being a
// function: the plate and the log used to reach the same answer only because
// they happened to implement the same two of canon's three rungs.
func (c *Courtroom) displayName(msg *protocol.ChatMessage) string {
	return DisplayName(msg, c.ForceCharNames, c.IniShownameFor)
}
