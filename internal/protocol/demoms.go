package protocol

import (
	"fmt"
	"strconv"
)

// BuildServerMS serializes a ChatMessage back into the SERVER→client MS field
// shape — the form AO2 demo files store (one raw packet per line). It is the
// inverse of ParseMS for every field ParseMS keeps: TestBuildServerMSRoundTrip
// pins ParseMS(BuildServerMS(m)) == m. Legacy wire spellings don't round-trip
// by design (ParseMS normalizes them): emote-mod 2/4 serialize as their live
// values and a non-numeric desk mod as its parsed number — the same message an
// AO2 client would DISPLAY. Offsets always emit the 2.9 "x&y" form (AO2's
// split("&") reads the 2.6 single-x form out of it unchanged).
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
