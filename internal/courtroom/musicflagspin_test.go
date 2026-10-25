package courtroom

import (
	"strconv"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// The AO2 MUSIC_EFFECT bitmask (../AO2-Client/src/datatypes.h:95-101) is spelled
// out twice in this repo, once per direction, and the two spellings can never be
// merged: the RECEIVE side is musicEffect* in session.go, the SEND side is
// config.MusicFlag*, and internal/config deliberately imports none of the
// packages that consume it (same rule as SpriteLoad*). Nothing in the type
// system stops the copies from drifting apart, so this file is the pin — the
// discipline TestEverySavedPreferenceIsAlsoLoaded already applies to the other
// cross-package duplication here.
//
// The pin lives on the courtroom side because that is the side with unexported
// constants; a test in internal/ui could only compare exported names, which is
// exactly what an unused exported alias block would be worth.

// TestMusicEffectBitsMatchTheSendSidePreference holds the two copies of the
// MUSIC_EFFECT bit values numerically equal. Both the individual bits and the
// mask are asserted: the mask catches the drift a per-bit table can't see, i.e.
// AO2 defining a fifth flag that lands in one copy only.
func TestMusicEffectBitsMatchTheSendSidePreference(t *testing.T) {
	for _, tc := range []struct {
		name    string
		receive int // session.go, MC field 5 as parsed
		send    int // internal/config, the preference the Music header writes
	}{
		{"FADE_IN", musicEffectFadeIn, config.MusicFlagFadeIn},
		{"FADE_OUT", musicEffectFadeOut, config.MusicFlagFadeOut},
		{"SYNC_POS", musicEffectSyncPos, config.MusicFlagSyncPos},
		{"NO_REPEAT", musicEffectNoRepeat, config.MusicFlagNoRepeat},
	} {
		if tc.receive != tc.send {
			t.Errorf("%s: receive-side musicEffect* = %d, send-side config.MusicFlag* = %d — the two copies of the MUSIC_EFFECT wire values have drifted", tc.name, tc.receive, tc.send)
		}
	}

	wantMask := musicEffectFadeIn | musicEffectFadeOut | musicEffectSyncPos | musicEffectNoRepeat
	if config.MusicFlagsMask != wantMask {
		t.Errorf("config.MusicFlagsMask = %d, want %d (every bit this package knows) — a flag was added to one side only, and the mask now drops it off the wire", config.MusicFlagsMask, wantMask)
	}
	// AO2-Client's own initial value (src/courtroom.h:551 music_flags = FADE_OUT).
	// Pinned here too because it is a receive-side constant expressed on the send
	// side: a default of, say, SYNC_POS would silently change what every unset
	// install transmits.
	if config.DefaultMusicFlags != musicEffectFadeOut {
		t.Errorf("config.DefaultMusicFlags = %d, want FADE_OUT (%d)", config.DefaultMusicFlags, musicEffectFadeOut)
	}
}

// TestMusicFlagsSurviveTheWireRoundTrip is the behavioral half: the number the
// preference puts on the wire has to mean the same thing when it comes back.
//
// The two directions do NOT share a field index — outgoing MC is
// MC#<song>#<cid>#<showname>#<flags> (RequestMusicWithFlags) while the server's
// echo is MC#<song>#<cid>#<showname>#<looping>#<channel>#<flags> — so the echo
// is rebuilt here field by field rather than replayed, and only the integer
// crosses over. That integer is what the NO_REPEAT-overrides-looping rule reads.
func TestMusicFlagsSurviveTheWireRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		flag     int
		wantLoop bool // with the server's looping field set to "1"
	}{
		{"FADE_IN", config.MusicFlagFadeIn, true},
		{"FADE_OUT", config.MusicFlagFadeOut, true},
		{"SYNC_POS", config.MusicFlagSyncPos, true},
		// NO_REPEAT is the one flag with receive-side behavior: it beats the
		// looping field (AO2-Client aomusicplayer.cpp:31).
		{"NO_REPEAT", config.MusicFlagNoRepeat, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent []protocol.Packet
			s := NewSession(func(p protocol.Packet) error {
				sent = append(sent, p)
				return nil
			}, "h")
			s.Features = protocol.ParseFeatures([]string{protocol.FeatureEffects})
			s.MyCharID = 2

			s.RequestMusicWithFlags("Trial.opus", "DJ", tc.flag)
			if len(sent) != 1 {
				t.Fatalf("packets sent = %d, want 1", len(sent))
			}
			// Field 3 on the OUTGOING shape (song, cid, showname, flags).
			onWire := sent[0].Field(3)
			if onWire != strconv.Itoa(tc.flag) {
				t.Fatalf("flags on the wire = %q, want %q", onWire, strconv.Itoa(tc.flag))
			}

			// The server echoes the play back in the six-field 2.9 shape, with
			// looping=1: only NO_REPEAT may override it.
			ev := feed(t, s, "MC#Trial.opus#2#DJ#1#0#"+onWire+"#%")
			if len(ev) != 1 || ev[0].Kind != EventMusic {
				t.Fatalf("echo events = %+v, want one EventMusic", ev)
			}
			if ev[0].MusicEffects != tc.flag {
				t.Errorf("effects parsed back = %d, want %d — the bit the preference sent is not the bit the receive path read", ev[0].MusicEffects, tc.flag)
			}
			if ev[0].Loop != tc.wantLoop {
				t.Errorf("Loop = %v, want %v", ev[0].Loop, tc.wantLoop)
			}
		})
	}
}

// TestRequestMusicWithFlagsMatchesCanonsShownameCondition pins the outgoing shape
// against Courtroom::on_music_list_double_clicked (courtroom.cpp:5810), whose
// condition is
//
//	(!ui_ic_chat_name->text().isEmpty() && CCCC_IC_SUPPORT) || EFFECTS
//
// The trap is the EMPTY showname on a cccc_ic_support-only server: appending it
// "because flags are positional" is wrong twice over — canon appends nothing, and
// without `effects` there is no flags field behind it for an omission to shift.
// A stray empty field there is a third MC field that a server reading field 3 as a
// showname sees as a deliberate blank name.
func TestRequestMusicWithFlagsMatchesCanonsShownameCondition(t *testing.T) {
	for _, tc := range []struct {
		name     string
		feats    []string
		showname string
		want     []string
	}{
		{"bare server, no showname", nil, "", []string{"Trial.opus", "2"}},
		{"bare server, showname set", nil, "DJ", []string{"Trial.opus", "2"}},
		{"cccc only, EMPTY showname", []string{protocol.FeatureCCCCIC}, "", []string{"Trial.opus", "2"}},
		{"cccc only, showname set", []string{protocol.FeatureCCCCIC}, "DJ", []string{"Trial.opus", "2", "DJ"}},
		// `effects` alone carries the showname slot even empty, because the flags
		// field follows it and the placeholder is what keeps them in place.
		{"effects only, EMPTY showname", []string{protocol.FeatureEffects}, "", []string{"Trial.opus", "2", "", "2"}},
		{"effects only, showname set", []string{protocol.FeatureEffects}, "DJ", []string{"Trial.opus", "2", "DJ", "2"}},
		{"both, EMPTY showname", []string{protocol.FeatureCCCCIC, protocol.FeatureEffects}, "", []string{"Trial.opus", "2", "", "2"}},
		{"both, showname set", []string{protocol.FeatureCCCCIC, protocol.FeatureEffects}, "DJ", []string{"Trial.opus", "2", "DJ", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent []protocol.Packet
			s := NewSession(func(p protocol.Packet) error {
				sent = append(sent, p)
				return nil
			}, "h")
			s.Features = protocol.ParseFeatures(tc.feats)
			s.MyCharID = 2

			s.RequestMusicWithFlags("Trial.opus", tc.showname, config.MusicFlagFadeOut)
			if len(sent) != 1 || sent[0].Header != "MC" {
				t.Fatalf("sent %+v, want one MC packet", sent)
			}
			got := sent[0].Fields
			if len(got) != len(tc.want) {
				t.Fatalf("MC fields = %q, want %q", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("MC fields = %q, want %q", got, tc.want)
				}
			}
		})
	}
}
