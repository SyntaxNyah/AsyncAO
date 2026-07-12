package courtroom

import (
	"sort"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Networked frame-synced effects (#17): a speaker's message can carry three
// FRAME_* wire fields that fire a screenshake / realization flash / sound effect
// as their sprite reaches specific animation frames. AO2-Client decodes
// "<emote>|<frame>=<value>|…^<emote>|…^<emote>|…^" into a per-frame effect map and
// fires each effect when the layer displaying that emote crosses the frame
// (src/courtroom.cpp:2779-2787 decode, src/animationlayer.cpp:472-569 map+fire).
//
// The three "^"-separated sections are, in fixed send order, the PREANIM emote,
// the "(b)" TALK emote, and the "(a)" IDLE emote (courtroom.cpp:2268-2269
// emotes_to_check = {pre_emote, "(b)"+emote, "(a)"+emote}). AO2 matches an
// effect's stored emote name against the layer's resolved file name; because the
// section ORDER already identifies which of our three sprite bases plays, we bind
// each section to a phase group by position instead of reconstructing the resolved
// spelling across our URL-encoding/lowercasing boundary — identical behaviour for
// a well-formed packet, simpler and encoding-proof.
//
// Divergence from AO2, kept deliberately (see the cluster trap "at most once per
// message"): AO2 re-fires a trigger every time a looping talk/idle animation wraps
// past the frame. We use a forward-only cursor per group, so each in-range trigger
// spends on the first ascent and never re-fires on a loop. That matches the
// once-per-playback contract and avoids a talk loop machine-gunning an SFX.
//
// Second, minor divergence: AO2 only calls setFrameEffects when FRAME_SFX is
// non-empty (courtroom.cpp:2778 gates the whole parse on realization[SFX]), so a
// message carrying only FRAME_SCREENSHAKE / FRAME_REALIZATION with an empty
// FRAME_SFX drops shake+realize entirely there. buildFrameTriggers parses all
// three FRAME_* fields independently, so shake/realize still fire without an SFX
// field — the more complete reading of the wire, not a regression: the shake/
// realize data was authored and sent; honoring it is strictly a superset.

// frameGroup indexes the three "^" sections of a FRAME_* field, in AO2 send order.
const (
	frameGroupPreanim = iota // section 0: the preanim emote
	frameGroupTalk           // section 1: the "(b)" talk emote
	frameGroupIdle           // section 2: the "(a)" idle emote
	frameGroupCount
)

// maxFrameTriggers caps how many frame triggers one message's table may hold,
// summed across every group and effect type. A malicious server can pack a huge
// FRAME_* string (thousands of "<n>=<sfx>" pairs); the table is built once at
// message-begin, but it's still bounded memory + parse work per message. 256 is
// far above any real preanim's frame count (long dramatic clips run ~150 frames)
// yet a hard ceiling on abuse. Excess triggers past the cap are dropped.
const maxFrameTriggers = 256

// frameTrigger is one authored effect at a source frame index (the sender's raw,
// un-decimated frame space — the render side maps our decimated cursor back into
// it before calling NotifyFrameShown). sfxURL is pre-resolved at build time for
// the SFX kind ("" for shake/flash, and "" for a silenced/muted sfx), so the
// per-frame fire path does zero string work.
type frameTrigger struct {
	frame  int
	kind   frameEffectKind
	sfxURL string
}

type frameEffectKind uint8

const (
	frameEffectShake frameEffectKind = iota
	frameEffectRealize
	frameEffectSFX
)

// frameTriggerTable holds a message's parsed triggers, one sorted slice per phase
// group with a forward-only cursor. Empty (the overwhelmingly common case — almost
// no char.ini authors frame effects) means NotifyFrameShown returns immediately.
type frameTriggerTable struct {
	groups  [frameGroupCount][]frameTrigger
	cursors [frameGroupCount]int
	total   int // triggers across all groups (0 = table inert)
}

// buildFrameTriggers parses the three incoming FRAME_* fields into a bounded,
// per-group sorted table, resolving each SFX name to its URL up front (honoring
// the same silent-value + M11 mute rules as armSFXDelay so a firing frame does no
// string work). Built once per message in begin(); never touched per frame.
func (c *Courtroom) buildFrameTriggers(msg *protocol.ChatMessage) frameTriggerTable {
	var t frameTriggerTable
	// ORDER pins effect kind to wire field, matching AO2's netstrings order
	// (courtroom.cpp:2782: {FRAME_SCREENSHAKE, FRAME_REALIZATION, FRAME_SFX}).
	c.parseFrameField(&t, msg.FrameShake, frameEffectShake)
	c.parseFrameField(&t, msg.FrameRealize, frameEffectRealize)
	c.parseFrameField(&t, msg.FrameSFX, frameEffectSFX)
	for g := range t.groups {
		if len(t.groups[g]) > 1 {
			sort.Slice(t.groups[g], func(i, j int) bool {
				return t.groups[g][i].frame < t.groups[g][j].frame
			})
		}
	}
	return t
}

// parseFrameField decodes one FRAME_* wire string of the form
// "<emote>[|<f>=<v>]…^<emote>[|…]^<emote>[|…]^" into t, binding section i to phase
// group i (preanim / talk / idle). The leading token of each "^" section is the
// emote NAME (dropped — position identifies the group; see the file header); the
// remaining "|"-parts are "<frame>=<value>" pairs. Mirrors AO2-Client
// setFrameEffects (animationlayer.cpp:472-505): a part lacking "=" is skipped, the
// frame is atoi'd, and only the SFX kind keeps the value.
func (c *Courtroom) parseFrameField(t *frameTriggerTable, field string, kind frameEffectKind) {
	if field == "" {
		return
	}
	sections := strings.Split(field, "^")
	for g, section := range sections {
		if g >= frameGroupCount {
			break // extra sections (malformed / future) ignored
		}
		parts := strings.Split(section, "|")
		if len(parts) < 2 {
			continue // just the emote name, no "<frame>=<value>" pairs
		}
		for _, raw := range parts[1:] { // parts[0] is the emote name (position binds the group)
			if t.total >= maxFrameTriggers {
				return // hostile oversized input: clamp (rule §17.4)
			}
			numStr, val, found := strings.Cut(raw, "=")
			if !found {
				continue // AO2 skips a part without "=" (frame_data.size() < 2)
			}
			frame, err := strconv.Atoi(strings.TrimSpace(numStr))
			if err != nil || frame < 0 {
				continue // non-numeric / negative frame: ignore, never panic
			}
			tr := frameTrigger{frame: frame, kind: kind}
			if kind == frameEffectSFX {
				tr.sfxURL = c.resolveFrameSFX(val)
			}
			t.groups[g] = append(t.groups[g], tr)
			t.total++
		}
	}
}

// resolveFrameSFX turns a frame-SFX value into a playable URL, applying the same
// silent + M11-mute rules as armSFXDelay: "", "0" and "1" are AO's silent values,
// and a per-SFX mute drops it too. "" means the trigger still fires (advancing the
// cursor) but plays nothing.
func (c *Courtroom) resolveFrameSFX(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "0" || name == "1" {
		return ""
	}
	if c.SFXMuted != nil && c.SFXMuted(name) {
		return ""
	}
	return c.urls.SFX(name)
}

// activeFrameGroup reports which trigger group the speaker layer's Active base
// belongs to (preanim / talk / idle), or -1 when it matches none (a shout bubble,
// a blank layer). Pure comparison against the bases begin() set — no allocation.
//
// The three sprite bases can COLLIDE: a char that reuses one file for talk+idle
// has TalkBase == IdleBase, and one whose preanim resolves to the same file as its
// talk/idle sprite has PreanimBase == TalkBase or PreanimBase == IdleBase. A switch
// on Active alone would silently pick whichever case lists first, mis-binding the
// group (AO2 avoids this by matching effect.emote_name against the layer's resolved
// file, animationlayer.cpp:558). So we DERIVE the group from the phase machine —
// the authoritative record of which sprite phase is playing — and only then confirm
// Active actually holds that group's base (a shout/blank layer confirms none → -1):
//   - PlayOnce marks the one-shot preanim, spanning BOTH the blocking PhasePreanim
//     wait and an IMMEDIATE-mode preanim that plays over the text during
//     PhaseTalking (courtroom.go:1259 pins that exact state) — so PlayOnce, not
//     c.phase == PhasePreanim, is the correct preanim discriminator.
//   - Otherwise PhaseTalking means the talk loop; any settled non-talk phase that
//     still shows a sprite is idle.
func (c *Courtroom) activeFrameGroup() int {
	sp := &c.Scene.Speaker
	switch {
	case sp.PlayOnce:
		// One-shot preanim playing. Confirm Active is the (non-empty) preanim base;
		// an empty PreanimBase never resolves a real layer.
		if sp.PreanimBase != "" && sp.Active == sp.PreanimBase {
			return frameGroupPreanim
		}
		return -1
	case c.phase == PhaseTalking:
		if sp.Active == sp.TalkBase {
			return frameGroupTalk
		}
		return -1
	default:
		if sp.Active == sp.IdleBase {
			return frameGroupIdle
		}
		return -1
	}
}

// NotifyFrameShown is called by the render side each time the SPEAKER layer draws
// a new frame, with src the frame index in the SENDER's raw (un-decimated) space.
// It fires every not-yet-fired trigger in the active group up to and including src
// (a decimated jump can skip past several source frames in one step, so the cursor
// sweeps the whole [cursor, src] range). Game-thread only, like the other Notify*
// callbacks; a message with no frame triggers returns on the first guard.
func (c *Courtroom) NotifyFrameShown(src int) {
	if c.frameTriggers.total == 0 {
		return
	}
	g := c.activeFrameGroup()
	if g < 0 {
		return
	}
	group := c.frameTriggers.groups[g]
	cursor := c.frameTriggers.cursors[g]
	for cursor < len(group) && group[cursor].frame <= src {
		c.fireFrameTrigger(group[cursor])
		cursor++
	}
	c.frameTriggers.cursors[g] = cursor
}

// fireFrameTrigger dispatches one trigger through the existing effect paths: shake
// and realization go to the Scene countdowns (gated by effectsVisible, exactly
// like fireMessageEffects), and SFX plays through the audio sink unconditionally
// (feedback sound survives reduce-motion, same as every other effect sound).
func (c *Courtroom) fireFrameTrigger(tr frameTrigger) {
	switch tr.kind {
	case frameEffectShake:
		if c.effectsVisible() {
			c.Scene.ShakeLeft = ScreenshakeDuration
		}
	case frameEffectRealize:
		if c.effectsVisible() {
			c.Scene.FlashLeft = RealizationFlashDuration
		}
	case frameEffectSFX:
		if tr.sfxURL != "" {
			c.audio.PlaySFX(tr.sfxURL, 0) // AssetType: SFX (#17 frame-synced)
		}
	}
}
