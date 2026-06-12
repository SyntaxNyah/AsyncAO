package courtroom

import (
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
	// emptyPreanim values AO uses for "no preanimation".
	emptyPreanimDash = "-"
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

	ShoutBase   string // active shout bubble base ("" = none)
	ShoutCustom bool

	// Chat box state.
	ShownameText string
	MessageText  string // full text; VisibleRunes gates the reveal
	VisibleRunes int
	TextColor    int
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
	// PlayMusic streams a track from a full URL.
	PlayMusic(url string)
}

// NopAudio discards all triggers (headless tests, muted client).
type NopAudio struct{}

func (NopAudio) PlayShout(string)              {}
func (NopAudio) PlaySFX(string, time.Duration) {}
func (NopAudio) PlayBlip(string)               {}
func (NopAudio) PlayMusic(string)              {}

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

	queue []*protocol.ChatMessage
	phase MessagePhase
	timer time.Duration

	current  *protocol.ChatMessage
	blipBase string

	// Predictor warms the predicted next speaker's sprite (optional).
	Predictor *assets.Prefetcher

	// preanimDone is flipped by NotifyPreanimDone (render reports one-shot
	// animation completion).
	preanimDone bool

	// ShoutUses tracks whether the per-character bubble resolved; the
	// renderer falls back to the default bubble base if not.
	ShoutCharBase    string
	ShoutDefaultBase string
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
		if ev.Text != "" && !isAreaTransfer(ev.Text) {
			c.audio.PlayMusic(c.urls.MusicURL(ev.Text)) // AssetType: Music
		}
	}
}

// isAreaTransfer filters MC packets that carry area names (no audio ext).
func isAreaTransfer(track string) bool { return !hasAudioExt(track) }

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
		c.ShoutDefaultBase = ""
		if !custom {
			c.ShoutDefaultBase = c.urls.DefaultShoutBubble(shout)
			c.mgr.Prefetch(c.ShoutDefaultBase, assets.AssetTypeShoutBubble, network.PriorityHigh) // AssetType: ShoutBubble
		}
		c.mgr.Prefetch(c.ShoutCharBase, assets.AssetTypeShoutBubble, network.PriorityHigh) // AssetType: ShoutBubble
		c.audio.PlayShout(c.urls.ShoutSFX(speakerName, shout))                             // AssetType: SFX
	}

	// Predictive prefetch: warm the likely next speaker (§10 step 3).
	if c.Predictor != nil {
		c.Predictor.OnMessage(speakerName, msg.Pair.Name)
	}

	blip := msg.Blipname
	if blip == "" {
		blip = "male" // AO default blip set
	}
	c.blipBase = c.urls.Blip(blip)
	c.mgr.Prefetch(c.blipBase, assets.AssetTypeBlip, network.PriorityHigh) // AssetType: Blip

	if msg.SFXName != "" && msg.SFXName != "0" && msg.SFXName != "1" {
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

	c.Scene.ShownameText = displayName(msg)
	c.Scene.TextColor = msg.TextColor
	c.Scene.MessageText = ""
	c.Scene.VisibleRunes = 0
	c.preanimDone = false

	// --- phase entry ---
	switch {
	case msg.IsShout():
		c.Scene.ShoutBase = c.ShoutCharBase
		c.Scene.ShoutCustom = msg.Objection == protocol.ShoutCustom
		c.phase = PhaseShout
		c.timer = DefaultShoutDuration
	default:
		c.enterAfterShout()
	}
}

// enterAfterShout picks preanim vs talking, mirroring handle_emote_mod:
// preanim plays first unless absent; IDLE/ZOOM with immediate plays preanim
// alongside the text.
func (c *Courtroom) enterAfterShout() {
	c.Scene.ShoutBase = ""
	msg := c.current
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

// startTalking begins the typewriter reveal.
func (c *Courtroom) startTalking() {
	msg := c.current
	c.Typewriter.Start(msg.Message)
	c.Scene.MessageText = c.Typewriter.Text()
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

// Update advances the message lifecycle by dt.
func (c *Courtroom) Update(dt time.Duration) {
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
func displayName(msg *protocol.ChatMessage) string {
	if msg.Showname != "" {
		return msg.Showname
	}
	return msg.CharName
}
