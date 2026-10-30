package protocol

import (
	"fmt"
	"strconv"
)

// BuildServerMS serializes a ChatMessage into the server-to-client MS shape of AO2 demo files, inverting ParseMS.
// Legacy spellings don't round-trip, as ParseMS normalizes them; offsets use "x&y", which AO2's split("&") reads.
func BuildServerMS(m *ChatMessage) Packet {
	b := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	f := make([]string, MSMaximum)
	f[MSDeskMod] = strconv.Itoa(m.DeskMod)
	f[MSPreEmote] = m.PreEmote
	f[MSCharName] = m.CharName
	f[MSEmote] = m.Emote
	f[MSMessage] = m.Message
	f[MSSide] = m.Side
	f[MSSFXName] = m.SFXName
	f[MSEmoteMod] = strconv.Itoa(m.EmoteMod)
	f[MSCharID] = strconv.Itoa(m.CharID)
	f[MSSFXDelay] = strconv.Itoa(m.SFXDelay)
	obj := strconv.Itoa(m.Objection)
	if m.CustomShout != "" { // 2.8 custom shout name rides "4&<name>"
		obj += "&" + m.CustomShout
	}
	f[MSObjectionMod] = obj
	f[MSEvidenceID] = strconv.Itoa(m.EvidenceID)
	f[MSFlip] = b(m.Flip)
	f[MSRealization] = b(m.Realization)
	f[MSTextColor] = strconv.Itoa(m.TextColor)
	f[MSShowname] = m.Showname
	oc := strconv.Itoa(m.Pair.CharID)
	if m.Pair.HasOrder {
		oc += "^" + strconv.Itoa(m.Pair.Order)
	}
	f[MSOtherCharID] = oc
	f[MSOtherName] = m.Pair.Name
	f[MSOtherEmote] = m.Pair.Emote
	f[MSSelfOffset] = fmt.Sprintf("%d&%d", m.SelfOffsetX, m.SelfOffsetY)
	f[MSOtherOffset] = fmt.Sprintf("%d&%d", m.Pair.OffsetX, m.Pair.OffsetY)
	f[MSOtherFlip] = b(m.Pair.Flip)
	f[MSImmediate] = b(m.Immediate)
	f[MSLoopingSFX] = b(m.LoopingSFX)
	f[MSScreenshake] = b(m.Screenshake)
	f[MSFrameScreenshake] = m.FrameShake
	f[MSFrameRealization] = m.FrameRealize
	f[MSFrameSFX] = m.FrameSFX
	f[MSAdditive] = b(m.Additive)
	f[MSEffects] = m.Effects
	f[MSBlipname] = m.Blipname
	f[MSSlide] = b(m.Slide)
	return Packet{Header: "MS", Fields: f}
}
