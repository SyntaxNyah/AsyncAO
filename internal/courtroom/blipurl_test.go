package courtroom

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

const blipTestOrigin = "http://cdn.example.com/base/"

// TestBlipCandidatesWalkGetBlipsLadder pins the whole canon ladder, URL by URL.
// AO2's get_blips checks two spellings of one blip name and falls back to a
// third it never checks (../AO2-Client/src/text_file_functions.cpp:515-527):
// the plain general sound, the sounds/blips/ set folder ("../blips/" + name),
// and — as an UNCHECKED return — the AO1 legacy file "sfx-blip" + name under
// sounds/general/ — one token, a DASH, and no separator, which is why
// gender=male means sfx-blipmale, not sfx-blip_male or sfx-blip/male.
// Each rung splits by casing on a case-sensitive mirror, lowercase first (the
// client-wide identity convention; MiscChatboxCandidates is the precedent).
func TestBlipCandidatesWalkGetBlipsLadder(t *testing.T) {
	u := NewURLBuilder(blipTestOrigin)
	for _, tc := range []struct {
		name string
		want []string
	}{
		{
			// An already-lowercase set spends three of the six slots: the two
			// casings of every rung collapse.
			name: "male",
			want: []string{
				blipTestOrigin + "sounds/blips/male",
				blipTestOrigin + "sounds/general/sfx-blipmale",
				blipTestOrigin + "sounds/general/male",
			},
		},
		{
			// An authored-case set spends all six: webAO-convention mirrors
			// lowercase every path, raw content folders keep the author's case.
			name: "YTTD",
			want: []string{
				blipTestOrigin + "sounds/blips/yttd",
				blipTestOrigin + "sounds/blips/YTTD",
				blipTestOrigin + "sounds/general/sfx-blipyttd",
				blipTestOrigin + "sounds/general/sfx-blipYTTD",
				blipTestOrigin + "sounds/general/yttd",
				blipTestOrigin + "sounds/general/YTTD",
			},
		},
		{
			// Spaces are real in blip-set names and must escape on EVERY rung,
			// including the concatenated legacy stem.
			name: "Deep Voice",
			want: []string{
				blipTestOrigin + "sounds/blips/deep%20voice",
				blipTestOrigin + "sounds/blips/Deep%20Voice",
				blipTestOrigin + "sounds/general/sfx-blipdeep%20voice",
				blipTestOrigin + "sounds/general/sfx-blipDeep%20Voice",
				blipTestOrigin + "sounds/general/deep%20voice",
				blipTestOrigin + "sounds/general/Deep%20Voice",
			},
		},
	} {
		got := u.BlipCandidates(tc.name)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("BlipCandidates(%q):\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// TestBlipChainStaysBoundedAndDeduped pins the named cap and the dedup: a chain
// is a bounded set of spellings of ONE asset (rule §17.4), and a repeated URL
// would spend a second probe on a base the first link already 404-cached.
func TestBlipChainStaysBoundedAndDeduped(t *testing.T) {
	u := NewURLBuilder(blipTestOrigin)
	for _, name := range []string{"male", "YTTD", "Deep Voice", "blip2", "sfx-blip_2", "ﾃｽﾄ"} {
		got := u.BlipCandidates(name)
		if len(got) == 0 {
			t.Errorf("BlipCandidates(%q) is empty — a valid set must mint at least the identity", name)
		}
		if len(got) > blipCandidateCap {
			t.Errorf("BlipCandidates(%q) = %d urls, over the cap of %d", name, len(got), blipCandidateCap)
		}
		seen := map[string]bool{}
		for _, url := range got {
			if seen[url] {
				t.Errorf("BlipCandidates(%q) repeats %q — a duplicate link is a wasted probe", name, url)
			}
			seen[url] = true
			if !strings.HasPrefix(url, blipTestOrigin) {
				t.Errorf("BlipCandidates(%q) minted %q outside the origin", name, url)
			}
		}
	}
}

// TestBlipRefIdentityIsUnchanged is the open-closed evidence for the ladder: the
// asset's identity — the T1/audio key everything else stores — is still exactly
// the lowercase sounds/blips/ spelling this client has always used. The new rungs
// are ALTS behind it, so nothing that remembers a blip base had to change.
func TestBlipRefIdentityIsUnchanged(t *testing.T) {
	u := NewURLBuilder(blipTestOrigin)
	for _, name := range []string{"male", "YTTD", "Deep Voice"} {
		ref := u.BlipRef(name)
		if want := u.Blip(name); ref.Base != want {
			t.Errorf("BlipRef(%q).Base = %q, want the unchanged identity %q", name, ref.Base, want)
		}
		if ref.Type != assets.AssetTypeBlip {
			t.Errorf("BlipRef(%q).Type = %v, want AssetTypeBlip", name, ref.Type)
		}
		if ref.Exact {
			t.Errorf("BlipRef(%q) must NOT be Exact — blip names carry no extension", name)
		}
	}
}

// TestBlipRefRefusesEverythingValidBlipNameRefuses is the encapsulation half of
// the guard: the sentinel check lives INSIDE the mint, so a caller cannot forget
// it. Before this, the Studio content report carried its own copy that knew only
// "" and "-1", and a KFO "<id>^0" or a bare "0" became a probed URL there.
func TestBlipRefRefusesEverythingValidBlipNameRefuses(t *testing.T) {
	u := NewURLBuilder(blipTestOrigin)
	for _, bad := range []string{"", "   ", "-1", "0", "7", "5^0", "../x", "a/b", `a\b`, ".hidden", "a#b", "a&b"} {
		// The zero AssetRef is recognised by its EMPTY base: AssetType's zero value
		// is a real type (AssetTypeCharIcon), so "no ref" can only ever mean "no
		// base", which is exactly what every caller must test before probing.
		ref := u.BlipRef(bad)
		if ref.Base != "" || len(ref.Alts) != 0 {
			t.Errorf("BlipRef(%q) = %+v, want the zero ref (nothing to probe)", bad, ref)
		}
	}
	// A blank name mints nothing at all — not even the identity spelling of "".
	for _, blank := range []string{"", "   "} {
		if cands := u.BlipCandidates(blank); len(cands) != 0 {
			t.Errorf("BlipCandidates(%q) = %q, want none", blank, cands)
		}
	}
}

// TestMessageFunnelMintsExactlyBlipRef drives the REAL message funnel (begin →
// the blip resolution at courtroom.go) and proves it hands the whole chain over
// unaltered: base AND alts equal the one mint's output for the resolved name. A
// funnel that hand-rolled its own candidate list — which is how the client got
// here, with sounds/blips/ as the only path anyone ever probed — fails this.
func TestMessageFunnelMintsExactlyBlipRef(t *testing.T) {
	icMsg := func(char, blip string) *protocol.ChatMessage {
		return &protocol.ChatMessage{
			CharName: char, Emote: "normal", Message: "hello", Side: "wit",
			EmoteMod: protocol.EmoteModIdle, // no preanim stall
			Blipname: blip,
		}
	}
	room, _, _, _ := newCourtroomRig(t)
	for _, tc := range []struct {
		what     string
		char     string
		wire     string
		charINI  string // what BlipNameFor answers for this row's speaker
		resolved string
	}{
		{"the wire field wins", "phoenix", "Typewriter", "", "Typewriter"},
		{"no wire field falls to the speaker's char.ini", "dorothy", "", "Deep Voice", "Deep Voice"},
		{"neither: AO's default set", "stranger", "", "", defaultBlipSet},
		{"a KFO sentinel is not a name", "stranger", "5^0", "", defaultBlipSet},
		{"a path-unsafe char.ini value is not a name either", "trickster", "", "../../etc/passwd", defaultBlipSet},
	} {
		// Per-row, so no row inherits a previous row's override and appending
		// a case can never silently change what an earlier one tested.
		charINI := tc.charINI
		room.BlipNameFor = func(string) string { return charINI }
		room.HandleEvent(Event{Kind: EventMessage, Message: icMsg(tc.char, tc.wire)})
		room.SkipToIdle()
		want := room.urls.BlipRef(tc.resolved)
		if !reflect.DeepEqual(room.blipRef, want) {
			t.Errorf("%s: funnel chain = %+v, want the mint's %+v", tc.what, room.blipRef, want)
		}
	}
}

// TestFunnelAlwaysMintsAPlayableChain pins the funnel's post-condition: whatever
// a server (or a char.ini) puts in the blip slot, the message ends up with a
// playable chain under the origin — never an empty base (silent blips) and never
// a URL built out of the junk itself.
func TestFunnelAlwaysMintsAPlayableChain(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	hostile := []string{"", "   ", "-1", "0", "5^0", "../../etc/passwd", `a\b`, "a#b", "male"}
	for _, junk := range hostile {
		room.BlipNameFor = func(string) string { return junk }
		room.HandleEvent(Event{Kind: EventMessage, Message: &protocol.ChatMessage{
			CharName: "stranger", Emote: "normal", Message: "hi", Side: "wit",
			EmoteMod: protocol.EmoteModIdle, Blipname: junk,
		}})
		room.SkipToIdle()
		if room.blipRef.Base == "" {
			t.Errorf("blipname %q left the message with no blip chain — blips go silent", junk)
			continue
		}
		if !strings.HasPrefix(room.blipRef.Base, room.urls.Origin()) {
			t.Errorf("blipname %q minted %q outside the origin", junk, room.blipRef.Base)
		}
		if strings.Contains(room.blipRef.Base, "..") || strings.Contains(room.blipRef.Base, "^") {
			t.Errorf("blipname %q leaked into the URL: %q", junk, room.blipRef.Base)
		}
	}
}
