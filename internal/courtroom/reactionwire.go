package courtroom

// Real reactions (#2): a player reacts to a PRIOR message with an emoji — like a
// Discord/Slack reaction — and every AsyncAO viewer sees the emoji float up from that
// message, while AO2/webAO render clean text. AO has no immediate side-channel (the
// same wall that killed M2 slice 2), so the reaction PIGGYBACKS on the reactor's NEXT
// IC message via the SAME invisible zero-width channel as the sprite style / profile /
// status / effects, told apart by a leading magic byte.
//
// The hard part is naming the target across clients: a reaction frame can't carry a
// message index (clients join at different times, so indices don't line up), so it
// carries a CONTENT-STABLE ref — a hash of the target's character name + clean text,
// which every client computes identically for the same wire message. The receiver scans
// its recent IC log for a message with the matching ref and floats the emoji from there.
// A stray/old ref simply matches nothing (benign). The emoji itself is a 1-byte index
// into a fixed, append-only palette (reactionSet), so the wire stays tiny — other clients
// typewriter the invisible run and blip on each rune, so a short tail matters.

const (
	// reactionFrameMagic is the first payload byte of a reaction frame, distinct from the
	// sprite-style version (1), profile (0x70), status (0x71) and effects (0x72) magics so
	// the shared zero-width frame scanner tells the codecs apart by their leading byte.
	reactionFrameMagic  = 0x73
	reactionWireVersion = 1

	// reactionFrameBytes is the fixed payload size: magic(1) + version(1) + ref(4, big-endian)
	// + palette index(1) = 7 bytes ≈ 19 invisible runes (3 bits/rune). Per-message content
	// (a reaction targets one message), so it rides only the message that carries a reaction —
	// not every line.
	reactionFrameBytes = 7
)

// reactionSet is the fixed palette of reaction emoji. Only the 1-byte INDEX into this
// slice travels on the wire, so the slice is APPEND-ONLY — never reorder or remove an
// entry, or an older client would map an index to the wrong (or no) emoji. Each entry is
// the full display string: astral-plane emoji route to the colour-emoji font on their
// own, and the one BMP symbol (the heart) carries a U+FE0F variation selector so it
// renders in colour rather than as a black glyph / tofu (see the emoji-font fallback).
// The courtroom package owns this list because the index→emoji mapping IS the cross-client
// contract; the UI just renders ReactionEmoji(i).
var reactionSet = []string{
	"\U0001F44D", // thumbs up
	"\U0001F44E", // thumbs down
	"❤️",         // red heart: BMP symbol + VS16 variation selector -> colour font
	"\U0001F602", // face with tears of joy
	"\U0001F62E", // face with open mouth (surprise)
	"\U0001F622", // crying face
	"\U0001F621", // enraged face
	"\U0001F389", // party popper
	"\U0001F525", // fire
	"\U0001F44F", // clapping hands
	"\U0001F914", // thinking face
	"\U0001F4AF", // hundred points
}

// ReactionCount is the number of palette entries (for building the picker UI).
func ReactionCount() int { return len(reactionSet) }

// ReactionEmoji returns the display string for a palette index, ok=false when the index
// is out of range (an index from a NEWER peer that added entries — benign, no float).
func ReactionEmoji(i uint8) (string, bool) {
	if int(i) >= len(reactionSet) {
		return "", false
	}
	return reactionSet[i], true
}

// WireReaction is a decoded reaction: a content-stable ref to the target message plus the
// palette index of the emoji. SDL-free plain data.
type WireReaction struct {
	Ref   uint32 // MakeReactionRef(target char name, target clean text)
	Index uint8  // index into reactionSet
}

// FNV-1a 32-bit constants (the standard offset basis + prime). Hashing manually — rather
// than via hash/fnv — keeps MakeReactionRef allocation-free (no Writer, no []byte(string)).
const (
	fnvOffset32 = 2166136261
	fnvPrime32  = 16777619
)

// MakeReactionRef computes a content-stable reference to a message from its character name
// and CLEAN display text (the marker-free text, i.e. StripSpriteStyle(msg.Message)) — the
// SAME value on every AsyncAO client, because every client receives the identical wire
// CharName + Message. A reactor stores this ref for the line it reacts to; the receiver
// recomputes it per recent message to find the target. A 0x00 separator between the two
// fields keeps "ab"+"c" from colliding with "a"+"bc". Allocation-free (a byte loop over
// the two strings), so it's safe to call per logged message.
func MakeReactionRef(charName, cleanText string) uint32 {
	h := uint32(fnvOffset32)
	for i := 0; i < len(charName); i++ {
		h = (h ^ uint32(charName[i])) * fnvPrime32
	}
	// Field separator: feed one 0x00 byte through the FNV-1a step so "ab"+"c" can't hash the
	// same as "a"+"bc". FNV-1a of a 0x00 byte is (h ^ 0) * prime = h * prime — the XOR is
	// elided (it's a no-op; staticcheck SA4016 flags `h ^ 0x00`), but this IS that separator
	// step, so the ref value is unchanged and stays cross-client stable.
	h *= fnvPrime32
	for i := 0; i < len(cleanText); i++ {
		h = (h ^ uint32(cleanText[i])) * fnvPrime32
	}
	return h
}

// EncodeMarker returns the invisible zero-width run carrying this reaction, to append to
// the reactor's outgoing IC message. "" when the palette index is out of range (nothing
// valid to send).
func (r WireReaction) EncodeMarker() string {
	if int(r.Index) >= len(reactionSet) {
		return ""
	}
	return packZeroWidth([]byte{
		reactionFrameMagic, reactionWireVersion,
		byte(r.Ref >> 24), byte(r.Ref >> 16), byte(r.Ref >> 8), byte(r.Ref),
		r.Index,
	})
}

// DecodeReactionMarker pulls a WireReaction out of an IC message's text if a well-formed
// reaction frame is present. It scans every zero-width frame for the reaction magic, so it
// coexists with a style / profile / status / effects frame on the same message. ok=false
// means no (valid) reaction frame — a short/corrupt frame, or a palette index this build
// doesn't know, is benign (no float).
func DecodeReactionMarker(text string) (WireReaction, bool) {
	if !hasMarker(text) {
		return WireReaction{}, false
	}
	for _, fr := range scanZeroWidthFrames(text) {
		if len(fr) < reactionFrameBytes || fr[0] != reactionFrameMagic || fr[1] != reactionWireVersion {
			continue
		}
		idx := fr[6]
		if int(idx) >= len(reactionSet) {
			return WireReaction{}, false // unknown emoji from a newer peer
		}
		return WireReaction{
			Ref:   uint32(fr[2])<<24 | uint32(fr[3])<<16 | uint32(fr[4])<<8 | uint32(fr[5]),
			Index: idx,
		}, true
	}
	return WireReaction{}, false
}

// HasReactionMarker reports whether text carries a reaction frame specifically (used by the
// recorder, so a reaction-only line is told apart from a no-marker line).
func HasReactionMarker(text string) bool {
	_, ok := DecodeReactionMarker(text)
	return ok
}
