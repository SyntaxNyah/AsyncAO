package courtroom

import (
	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// needGate is what the wait gate requires of a need before its message may start.
type needGate int

const (
	// gateNone is requested but never waited on.
	gateNone needGate = iota
	// gateSpeakerSprite waits for every frame: a partial page would hold the old sprite or skip a one-shot.
	gateSpeakerSprite
	// gateSprite waits for the first frame.
	gateSprite
	gateAudio
	gateDesk
	// gateOverlay is waited on but not requested: OverlayFor starts its own resolve.
	gateOverlay
)

// messageNeed is one asset a held message asks for, and what the gate waits to see of it.
type messageNeed struct {
	base string // the overlay's effect name for gateOverlay
	alts []string
	typ  assets.AssetType
	gate needGate
	// overlayFolder is only for gateOverlay.
	overlayFolder string
}

// messageNeeds lists what msg needs, in the order the gate checks it; the result is reused by the next call.
func (c *Courtroom) messageNeeds(msg *protocol.ChatMessage) []messageNeed {
	needs := c.needs[:0]
	add := func(base string, alts []string, typ assets.AssetType, gate needGate) {
		needs = append(needs, messageNeed{base: base, alts: alts, typ: typ, gate: gate})
	}
	sprite := func(name, emoteName string, kind EmoteKind, gate needGate) {
		add(c.urls.Emote(name, emoteName, kind), c.spriteAlts(name, emoteName, kind), assets.AssetTypeCharSprite, gate)
	}

	sprite(msg.CharName, msg.Emote, EmoteIdle, gateSpeakerSprite)
	// Also the talk sprite: with no preanim the stage draws it first, and waiting for idle alone flashes a placeholder.
	sprite(msg.CharName, msg.Emote, EmoteTalk, gateSpeakerSprite)
	// The partner's idle: SpriteWaitPair, or always under strict sequencing.
	if (c.SpriteWaitPair || c.SequentialWait) && msg.Pair.Active() {
		sprite(msg.Pair.Name, msg.Pair.Emote, EmoteIdle, gateSprite)
	}
	// A preanim that will play shows first. preanimWillPlay, not hasPreanim: packs fill the field with dummies.
	// A conclusively 404'd preanim releases on the miss, mirroring the play path's NotifyAssetMissing skip.
	if hasPreanim(msg) && (preanimWillPlay(msg) || c.SpriteWaitPreanim) {
		add(c.urls.Emote(msg.CharName, msg.PreEmote, EmotePreanim), nil, assets.AssetTypeCharSprite, gateSpeakerSprite)
	}
	if !c.SequentialWait {
		c.needs = needs
		return needs
	}

	// Strict sequencing also waits for the group, scenery and audio, computed as begin() does so they can't disagree.
	for _, m := range msg.Additional {
		if m.CharID != msg.CharID {
			sprite(m.Name, m.Emote, EmoteIdle, gateSprite)
		}
	}
	if c.sess != nil && c.sess.Background != "" {
		s := c.urls.PositionScenery(c.sess.Background, msg.Side, c.design)
		add(s.Background, s.Alts, assets.AssetTypeBackground, gateSprite)
		desk := s.Desk
		if file := c.sceneryFile(s.Background); file != "" {
			desk = deskBeside(s.Background, file, c.design)
		}
		deskGate := gateNone
		if deskVisible(msg.DeskMod, false) && !zoomEmote(msg.EmoteMod) {
			deskGate = gateDesk
		}
		add(desk, nil, assets.AssetTypeDeskOverlay, deskGate)
	}
	if c.Chars != nil {
		if misc := c.Chars.ChatSkinFor(msg.CharName); misc != "" {
			cands := c.urls.MiscChatboxCandidates(misc)
			add(cands[0], cands[1:], assets.AssetTypeMisc, gateSprite)
		}
	}
	if zoomEmote(msg.EmoteMod) {
		chain := c.urls.SpeedlinesCandidates(msg.CharName, msg.Side)
		add(chain[0], chain[1:], assets.AssetTypeMisc, gateSprite)
	}
	if preanimSFXPlays(msg) {
		if sfx := c.sfxBaseFor(msg); sfx != "" {
			add(sfx, nil, assets.AssetTypeSFX, gateAudio)
		}
	}
	blipWire := msg.Blipname
	if !validBlipName(blipWire) {
		blipWire = ""
	}
	if ref, pending := c.blipRefFor(blipWire, msg.CharName); !pending && ref.Base != "" {
		add(ref.Base, ref.Alts, ref.Type, gateAudio)
	}
	// A queued shout (bypass off) also waits for its bubble art and cry.
	if msg.IsShout() {
		charBase, defaultBase, shoutSFX := c.shoutBases(msg)
		add(charBase, nil, assets.AssetTypeShoutBubble, gateSprite)
		if defaultBase != "" {
			add(defaultBase, nil, assets.AssetTypeShoutBubble, gateSprite)
		}
		if shoutSFX != "" {
			add(shoutSFX, nil, assets.AssetTypeSFX, gateAudio)
		}
	}
	if msg.Realization {
		if sfx := c.realizationSFXFor(msg.CharName); sfx != "" {
			add(sfx, nil, assets.AssetTypeSFX, gateAudio)
		}
	}
	if msg.Effects != "" {
		fx, folder, sound := parseEffectsField(msg.Effects)
		if folder == "" {
			folder = c.OverlayEffectsFolderFor(msg.CharName)
		}
		if OverlaySoundPlayable(sound) {
			add(c.urls.SFX(sound), nil, assets.AssetTypeSFX, gateAudio)
		}
		needs = append(needs, messageNeed{base: fx, gate: gateOverlay, overlayFolder: folder})
	}
	c.needs = needs
	return needs
}

// needSettled reports whether the gate has its answer for n.
func (c *Courtroom) needSettled(n messageNeed) bool {
	switch n.gate {
	case gateSpeakerSprite:
		return c.speakerSpriteSettled(n.base)
	case gateSprite:
		return c.spriteSettled(n.base)
	case gateAudio:
		return c.audioSettled(n.base)
	case gateDesk:
		return c.deskResolutionOf(n.base) != DeskUnresolved
	case gateOverlay:
		// Best-effort: a pending resolve has no base to wait on, and the deferred overlay re-arms it.
		res, ok := c.Assets.OverlayFor(n.base, n.overlayFolder)
		return !ok || res.Pending || res.Base == "" || c.spriteSettled(res.Base)
	default:
		return true
	}
}

// firstUnsettled checks in order and stops at the first it finds, so a later overlay doesn't start resolving early.
func (c *Courtroom) firstUnsettled(needs []messageNeed) (messageNeed, bool) {
	for _, n := range needs {
		if !c.needSettled(n) {
			return n, true
		}
	}
	return messageNeed{}, false
}

// requestNeeds asks for needs at high priority, which the media system never refuses, so the gate can end.
func (c *Courtroom) requestNeeds(needs []messageNeed) {
	for _, n := range needs {
		if n.gate != gateOverlay {
			c.mgr.PrefetchChain(n.base, n.alts, n.typ, network.PriorityHigh) // AssetType: from the need
		}
	}
}
