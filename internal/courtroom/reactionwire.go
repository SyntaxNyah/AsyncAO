package courtroom

import "encoding/binary"

// Reactions ride the reactor's next IC message, as AO has no immediate side channel; a content hash names the target.

const (
	// reactionFrameMagic is the first byte, distinct from style (1), profile (0x70), status (0x71) and effects (0x72).
	reactionFrameMagic  = 0x73
	reactionWireVersion = 1

	// reactionFrameBytes is magic(1) + version(1) + big-endian ref(4) + palette index(1).
	reactionFrameBytes = 7
)

// reactionSet is append-only, as only the index travels. The heart carries U+FE0F to render in colour.
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

func ReactionCount() int { return len(reactionSet) }

// ReactionEmoji returns a palette entry; ok=false for an index from a newer peer.
func ReactionEmoji(i uint8) (string, bool) {
	if int(i) >= len(reactionSet) {
		return "", false
	}
	return reactionSet[i], true
}

// WireReaction is a decoded reaction: a content ref to the target message plus a palette index.
type WireReaction struct {
	Ref   uint32 // MakeReactionRef(target char name, target clean text)
	Index uint8  // index into reactionSet
}

// FNV-1a 32-bit constants; hashing by hand keeps MakeReactionRef allocation-free.
const (
	fnvOffset32 = 2166136261
	fnvPrime32  = 16777619
)

// MakeReactionRef hashes character name and clean text, the same on every client for a wire message; allocation-free.
func MakeReactionRef(charName, cleanText string) uint32 {
	h := uint32(fnvOffset32)
	for i := 0; i < len(charName); i++ {
		h = (h ^ uint32(charName[i])) * fnvPrime32
	}
	// Field separator: one FNV-1a step of a 0x00 byte; the XOR is a no-op (staticcheck SA4016). Must stay stable.
	h *= fnvPrime32
	for i := 0; i < len(cleanText); i++ {
		h = (h ^ uint32(cleanText[i])) * fnvPrime32
	}
	return h
}

// EncodeMarker returns the invisible run for this reaction, "" for an out-of-range index.
func (r WireReaction) EncodeMarker() string {
	if int(r.Index) >= len(reactionSet) {
		return ""
	}
	buf := binary.BigEndian.AppendUint32([]byte{reactionFrameMagic, reactionWireVersion}, r.Ref)
	return packZeroWidth(append(buf, r.Index))
}

// DecodeReactionMarker finds a reaction frame among any others; ok=false for a corrupt frame or unknown palette index.
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
			Ref:   binary.BigEndian.Uint32(fr[2:]),
			Index: idx,
		}, true
	}
	return WireReaction{}, false
}
