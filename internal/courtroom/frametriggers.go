package courtroom

import (
	"sort"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// FRAME_* fields fire shake, flash or sfx at sprite frames (courtroom.cpp:2779-2787, animationlayer.cpp:472-569).
// Sections are in fixed order preanim, (b), (a) (courtroom.cpp:2268-2269), so we bind by position, not by name.
// Deliberate divergence: courtroom.cpp:2778 gates the parse on SFX; we parse all three, so shake fires without sfx.

const (
	frameGroupPreanim = iota // section 0: the preanim emote
	frameGroupTalk           // section 1: the "(b)" talk emote
	frameGroupIdle           // section 2: the "(a)" idle emote
	frameGroupCount
)

// maxFrameTriggers caps one message's table; a hostile server can pack thousands of pairs (real preanims: ~150 frames).
const maxFrameTriggers = 256

// frameTrigger is one effect in the sender's raw frame space; sfxURL is resolved at build time, so firing does no work.
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

// frameTriggerTable holds one sorted slice per phase group with a forward-only cursor; usually empty.
type frameTriggerTable struct {
	groups  [frameGroupCount][]frameTrigger
	cursors [frameGroupCount]int
	total   int // triggers across all groups (0 = table inert)
}

// buildFrameTriggers parses FRAME_* once per message in begin(), resolving SFX with armSFXDelay's rules.
func (c *Courtroom) buildFrameTriggers(msg *protocol.ChatMessage) frameTriggerTable {
	var t frameTriggerTable
	// Order pins effect kind to wire field, as AO2's netstrings do (courtroom.cpp:2782).
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

// parseFrameField decodes "<emote>[|<f>=<v>]...^" into t, section i to group i, as setFrameEffects does
// (animationlayer.cpp:472-505): a part without "=" is skipped and only SFX keeps the value.
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

// resolveFrameSFX returns a playable URL; "", "0" and "1" are silent, and "" still fires to advance the cursor.
func (c *Courtroom) resolveFrameSFX(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "0" || name == "1" {
		return ""
	}
	if c.Blips != nil && c.Blips.SFXMuted(name) {
		return ""
	}
	return c.urls.SFX(name)
}

// activeFrameGroup derives the group from the phase, then confirms Active holds its base (cf. animationlayer.cpp:558).
func (c *Courtroom) activeFrameGroup() int {
	sp := &c.Scene.Speaker
	switch {
	case sp.PlayOnce:
		// One-shot preanim playing; an empty PreanimBase never resolves a real layer.
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

// NotifyFrameShown fires every unfired trigger up to src, as a decimated jump can skip frames. Game thread only.
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

// fireFrameTrigger applies effectsVisible to shake and realization; SFX plays regardless, surviving reduce-motion.
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
