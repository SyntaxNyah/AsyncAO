package protocol

import (
	"strings"
	"testing"
)

// Fuzz targets for the two hostile-input parsers on the wire's front line:
// every byte here arrives from an untrusted server. The contract these pin is
// ROBUSTNESS, not round-trip purity — the parsers must never panic, hang, or
// allocate unboundedly on malformed input (CLAUDE.md rules #4, #7). The
// deliberate lossy behaviours (SC/LE double-decode, the ^order/offset splits)
// are protocol law and are NOT asserted here.

// FuzzParsePacket exercises the wire framing/unescape path (packet.go): the
// #-split, terminator handling, and the <num>/<percent>/<dollar>/<and> decode.
// It must return cleanly (packet or error) for any bytes.
func FuzzParsePacket(f *testing.F) {
	// Real wire shapes and the escapes, plus degenerate cases: a bare terminator,
	// no terminator, an empty field, and a header stuffed with empty fields.
	seeds := []string{
		"MS#1#hello#%",
		"CT#user#a <num> b <percent> c <dollar> d <and> e#%",
		NewPacket("MS", "50% off #1 & more $$", "wit").String(),
		"#%",
		"",
		"NOTERMINATOR",
		"##%",
		"<num><percent>#%",
		strings.Repeat("#", 64) + "#%",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		p, err := ParsePacket(raw)
		if err != nil {
			return // a rejected packet is a valid outcome, not a crash
		}
		// Field access must stay in-bounds for any parsed shape.
		_ = p.Field(-1)
		_ = p.Field(len(p.Fields))
		for i := range p.Fields {
			_ = p.Field(i)
		}
	})
}

// FuzzParseMS drives the MS parser (ms.go) with a #-split field list, mirroring
// how a live MS packet reaches it (ParsePacket then ParseMS). Go native fuzzing
// can only vary a string, so the target splits on the field separator itself —
// which also fuzzes the objection/offset/pair sub-parsers ParseMS calls.
func FuzzParseMS(f *testing.F) {
	// The three MS fixtures plus degenerate field lists: empty (too short), a
	// short packet, and hostile pair/offset sub-fields ("4^1", "a&b", "-1^x").
	seeds := []string{
		strings.Join(fixture28Paired, "#"),
		strings.Join(fixture26Paired, "#"),
		strings.Join(fixtureVanilla15, "#"),
		"",
		"1#2#3",
		"4^1#a&b#-1^x",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	feats := allFeatures()
	f.Fuzz(func(t *testing.T, raw string) {
		fields := strings.Split(raw, "#")
		// charListSize varies the CHAR_ID bound; 0 skips the upper bound.
		for _, size := range []int{0, 10} {
			msg, err := ParseMS(fields, feats, size)
			if err != nil {
				continue // short/out-of-range packets are rejected, not a crash
			}
			// A parsed message must be internally consistent for its accessors.
			_ = msg.IsShout()
			_ = msg.Pair.Active()
			_ = msg.Pair.SpeakerInFront()
		}
	})
}
