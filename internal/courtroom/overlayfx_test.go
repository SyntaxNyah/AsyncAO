package courtroom

import (
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// overlayStub wires a room's OverlayFor to resolve exactly the named effects.
func overlayStub(room *Courtroom, table map[string]theme.OverlayFX) {
	room.OverlayFor = func(name, folder string) (OverlayResolved, bool) {
		fx, ok := table[strings.ToLower(name)]
		if !ok {
			return OverlayResolved{}, false
		}
		return OverlayResolved{FX: fx, Base: "fx://" + folder + "/" + strings.ToLower(name)}, true
	}
}

// TestParseEffectsFieldThreeShapes — courtroom.cpp:4154-4172 verbatim, INCLUDING
// the >=4-part case the previous parts[len-1] implementation got wrong.
func TestParseEffectsFieldThreeShapes(t *testing.T) {
	for _, tc := range []struct {
		raw               string
		fx, folder, sound string
		why               string
	}{
		{"realization", "realization", "", "", "1 part: folder falls back to char.ini, no sound"},
		{"realization|sfx-realization", "realization", "", "sfx-realization", "2 parts: legacy name|sound"},
		{"tv on|transitions|switch tv", "tv on", "transitions", "switch tv", "3 parts: AO2 >= 2.8"},
		{"a|b|c|d|e", "a", "b", "c", "only [0],[1],[2] are read — the tail is IGNORED, not re-indexed"},
		{"", "", "", "", "an empty field yields an empty name"},
	} {
		fx, folder, sound := parseEffectsField(tc.raw)
		if fx != tc.fx || folder != tc.folder || sound != tc.sound {
			t.Errorf("parseEffectsField(%q) = (%q,%q,%q), want (%q,%q,%q) — %s",
				tc.raw, fx, folder, sound, tc.fx, tc.folder, tc.sound, tc.why)
		}
	}
}

// TestEmptyEffectNameIsNoOpNotClear (courtroom.cpp:3184-3187): do_effect returns
// immediately on an empty name WITHOUT clearing, so a message with no effect
// cannot kill a looping overlay a previous message started mid-stage.
func TestEmptyEffectNameIsNoOpNotClear(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"wash": {Loop: true}})
	if !room.fireOverlay("wash", "pack", false, 0, 0) {
		t.Fatal("the stub effect did not arm")
	}
	before := room.Scene.Overlay
	if !room.fireOverlay("", "pack", false, 0, 0) {
		t.Error("an empty name must report the overlay still on stage")
	}
	if room.Scene.Overlay != before {
		t.Errorf("an empty effect name CLEARED the overlay; courtroom.cpp:3184-3187 returns without touching it")
	}
}

// TestUnresolvableEffectClearsTheOverlay (courtroom.cpp:3188-3194): an unknown
// name is an explicit clear, not a no-op. This is also how every localised "None"
// row lands — AO2 sends the TRANSLATED word (:5600), so no sentinel list can
// catch them all and the unresolvable path has to.
func TestUnresolvableEffectClearsTheOverlay(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"wash": {}})
	room.fireOverlay("wash", "pack", false, 0, 0)
	if !room.Scene.Overlay.Active() {
		t.Fatal("the stub effect did not arm")
	}
	room.fireOverlay("nosuchthing", "pack", false, 0, 0)
	if room.Scene.Overlay.Active() {
		t.Error("an unknown effect name must CLEAR the overlay (courtroom.cpp:3188-3194)")
	}
	// The same for the explicit sentinels and for a room with no engine wired.
	for _, name := range []string{"none", "None", "-"} {
		room.fireOverlay("wash", "pack", false, 0, 0)
		room.fireOverlay(name, "pack", false, 0, 0)
		if room.Scene.Overlay.Active() {
			t.Errorf("%q must clear the overlay", name)
		}
	}
	room.fireOverlay("wash", "pack", false, 0, 0)
	room.OverlayFor = nil
	room.fireOverlay("wash", "pack", false, 0, 0)
	if room.Scene.Overlay.Active() {
		t.Error("with no overlay engine wired the effect must clear, never linger")
	}
}

// TestEffectSoundSentinelsAreFiltered — "0" is KFO's stock "no sound" idiom and
// would otherwise burn a real probe (and a 404-cache entry) per effect; "-" is
// webAO's BAD_EFFECTS marker and "none" the ASCII None row. Canon gets away with
// only testing != "" because it stats a local file.
func TestEffectSoundSentinelsAreFiltered(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	for _, sound := range []string{"", "0", "-", "none", "NONE", " 0 "} {
		audio.sfx = nil
		room.fireMessageEffects(&protocol.ChatMessage{Effects: "realization|" + sound})
		if len(audio.sfx) != 0 {
			t.Errorf("effect sound %q reached PlaySFX as %v — sentinels never mint a URL", sound, audio.sfx)
		}
	}
	audio.sfx = nil
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "realization|realizationsound"})
	if len(audio.sfx) != 1 {
		t.Errorf("a real sound must play, got %v", audio.sfx)
	}
}

// TestEffectSoundPlaysWithEffectsDisabled — canon's ordering: the sound is played
// BEFORE the effectsEnabled test (courtroom.cpp:3196 precedes :3202), and before
// the sprite would even be known to resolve for us.
func TestEffectSoundPlaysWithEffectsDisabled(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		screenEffects, reduceMot bool
	}{
		{"screen effects off", false, false},
		{"reduce motion on", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			room, _, _, audio := newCourtroomRig(t)
			overlayStub(room, map[string]theme.OverlayFX{"wash": {}})
			room.ScreenEffects, room.ReduceMotion = tc.screenEffects, tc.reduceMot
			room.fireMessageEffects(&protocol.ChatMessage{Effects: "wash|packpack|boom"})
			if len(audio.sfx) != 1 {
				t.Errorf("the effect sound must play regardless of the visual gate, got %v", audio.sfx)
			}
			if room.Scene.Overlay.Active() {
				t.Error("no overlay may be on screen with the visual gate off — that is what reduce-motion is for")
			}
		})
	}
}

// TestReduceMotionForbidsALoopingOverlay — the looping wash is precisely what
// reduce-motion exists to kill, and it falls out of the ONE gate rather than
// needing a second knob.
func TestReduceMotionForbidsALoopingOverlay(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"filmgrain": {Loop: true}})
	room.ReduceMotion = true
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "filmgrain"})
	if room.Scene.Overlay.Active() {
		t.Error("a looping overlay must never arm under reduce motion")
	}
}

// TestEffectsFieldSuppressesLegacyRealization (courtroom.cpp:4173-4177 is an
// `else if`).
func TestEffectsFieldSuppressesLegacyRealization(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.Scene.FlashLeft = 0
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "somethingelse", Realization: true})
	if room.Scene.FlashLeft != 0 {
		t.Error("a non-empty EFFECTS field must suppress the legacy REALIZATION path entirely")
	}
}

// TestResolvedOverlayReplacesTheLegacyFlash — the flash/shake name mapping is a
// FALLBACK for installs with no effects.ini. Once a real overlay resolves it owns
// the visual, so a theme shipping its own realization art is not also washed white.
func TestResolvedOverlayReplacesTheLegacyFlash(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.Scene.FlashLeft = 0
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "realization"})
	if room.Scene.FlashLeft == 0 {
		t.Fatal("with no overlay engine the legacy flash must still fire")
	}
	overlayStub(room, map[string]theme.OverlayFX{"realization": {Stretch: true}})
	room.Scene.FlashLeft = 0
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "realization"})
	if room.Scene.FlashLeft != 0 {
		t.Error("a resolved overlay must not ALSO fire the legacy white flash")
	}
	if !room.Scene.Overlay.Active() {
		t.Error("the overlay did not arm")
	}
}

// TestOverlayCarriesEveryCoercedProperty.
func TestOverlayCarriesEveryCoercedProperty(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"tv on": {
		Layer: theme.OverlayLayerCharacter, Loop: true, Cull: true, Stretch: true,
		RespectFlip: true, RespectOffset: true, Scaling: "pixel", MaxFrameMS: 60,
	}})
	room.fireOverlay("tv on", "transitions", true, 12, -7)
	o := room.Scene.Overlay
	if o.Layer != OverlayLayerCharacter {
		t.Errorf("Layer = %d, want character", o.Layer)
	}
	if !o.Loop || !o.Cull || !o.Stretch || !o.Flip {
		t.Errorf("booleans lost: %+v", o)
	}
	if o.OffsetX != 12 || o.OffsetY != -7 {
		t.Errorf("offsets = %d,%d — respect_offset copies the SPEAKER's own percent-of-viewport offset (courtroom.cpp:3264-3266)", o.OffsetX, o.OffsetY)
	}
	if o.MaxFrameDelay != 60*time.Millisecond {
		t.Errorf("MaxFrameDelay = %v, want 60ms", o.MaxFrameDelay)
	}
	if o.Scaling != ScalingPixel {
		t.Errorf("Scaling = %v, want pixel", o.Scaling)
	}
}

// TestOverlayRespectFlipFollowsTheFlipField — respect_flip is ANDed with FLIP==1
// (courtroom.cpp:3208), so a declaring effect on an unflipped message is unflipped.
func TestOverlayRespectFlipFollowsTheFlipField(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"a": {RespectFlip: true}, "b": {}})
	room.fireOverlay("a", "p", false, 0, 0)
	if room.Scene.Overlay.Flip {
		t.Error("respect_flip with FLIP=0 must not flip")
	}
	room.fireOverlay("a", "p", true, 0, 0)
	if !room.Scene.Overlay.Flip {
		t.Error("respect_flip with FLIP=1 must flip")
	}
	room.fireOverlay("b", "p", true, 0, 0)
	if room.Scene.Overlay.Flip {
		t.Error("an effect that does not declare respect_flip must never flip")
	}
}

// TestOverlayIgnoresOffsetWithoutRespectOffset.
func TestOverlayIgnoresOffsetWithoutRespectOffset(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"a": {}})
	room.fireOverlay("a", "p", false, 40, 40)
	if room.Scene.Overlay.OffsetX != 0 || room.Scene.Overlay.OffsetY != 0 {
		t.Error("without respect_offset the layer always covers the viewport rect")
	}
}

// TestSameEffectTwiceRestartsViaGen — animState.reset only fires on a base
// change, so the retrigger has to ride a generation.
func TestSameEffectTwiceRestartsViaGen(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"a": {}})
	room.fireOverlay("a", "p", false, 0, 0)
	first := room.Scene.Overlay
	room.fireOverlay("a", "p", false, 0, 0)
	second := room.Scene.Overlay
	if second.Base != first.Base {
		t.Fatalf("base changed unexpectedly: %q vs %q", second.Base, first.Base)
	}
	if second.Gen == first.Gen {
		t.Error("Gen must bump on EVERY trigger — the renderer restarts the clip off it")
	}
	// A CLEAR must not reset the counter, or the next retrigger looks like a repeat.
	room.clearOverlay()
	room.fireOverlay("a", "p", false, 0, 0)
	if room.Scene.Overlay.Gen <= second.Gen {
		t.Error("Gen went backwards across a clear")
	}
}

// TestOverlayClearsOnNextMessageWithEmote — clear site 1 of 3
// (display_character, courtroom.cpp:2755-2761).
func TestOverlayClearsOnNextMessageWithEmote(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"wash": {Loop: true}})
	room.HandleEvent(Event{Kind: EventMessage, Message: &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "wit", Message: "hi",
		EmoteMod: protocol.EmoteModIdle, Effects: "wash",
	}})
	if !room.Scene.Overlay.Active() {
		t.Fatal("the effect did not arm on the message that carried it")
	}
	// Drain message 1 first. The clear lives in begin (courtroom.go:1315), and
	// enqueue only calls begin when the room is PhaseIdle with an empty queue
	// (courtroom.go:786-794) — feeding the second message while the first is still
	// typing would merely QUEUE it and never reach the clear site at all. Same
	// drain the sibling TestOverlayDoesNotClearAtMessageEnd uses.
	room.SkipToIdle()
	for i := 0; i < 200; i++ {
		room.Update(50 * time.Millisecond)
	}
	if !room.Scene.Overlay.Active() {
		t.Fatal("fixture: the looping wash must outlive its own message — there is no clear at message end")
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: &protocol.ChatMessage{
		CharName: "Edgeworth", Emote: "normal", Side: "wit", Message: "hi again",
		EmoteMod: protocol.EmoteModIdle,
	}})
	if room.Scene.Overlay.Active() {
		t.Error("the NEXT message staging its emote must clear the overlay (courtroom.cpp:2755-2761)")
	}
}

// TestOverlayDoesNotClearAtMessageEnd — the one non-site. Canon has NO clear when
// a message finishes; a looping wash outlives the line that started it.
func TestOverlayDoesNotClearAtMessageEnd(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"wash": {Loop: true}})
	room.HandleEvent(Event{Kind: EventMessage, Message: &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "wit", Message: "hi",
		EmoteMod: protocol.EmoteModIdle, Effects: "wash",
	}})
	room.SkipToIdle()
	for i := 0; i < 200; i++ {
		room.Update(50 * time.Millisecond)
	}
	if !room.Scene.Overlay.Active() {
		t.Error("the overlay was cleared at message end — canon has no such clear site")
	}
}

// TestOverlayClearsOnBackgroundChange — clear site 2 of 3 (set_background with
// display=true, courtroom.cpp:1427-1435).
func TestOverlayClearsOnBackgroundChange(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	overlayStub(room, map[string]theme.OverlayFX{"wash": {Loop: true}})
	room.fireOverlay("wash", "p", false, 0, 0)
	room.WipeStage()
	if room.Scene.Overlay.Active() {
		t.Error("an area swap must not carry the previous room's wash across")
	}
}

// TestOverlayFolderFallsBackToCharINIWhenFieldOmitsIt
// (text_file_functions.cpp:836-839).
func TestOverlayFolderFallsBackToCharINIWhenFieldOmitsIt(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.EffectsFolderFor = func(char string) string {
		if char == "Phoenix" {
			return "transitions"
		}
		return ""
	}
	var gotFolder string
	room.OverlayFor = func(name, folder string) (OverlayResolved, bool) {
		gotFolder = folder
		return OverlayResolved{Base: "fx://x"}, true
	}
	room.fireMessageEffects(&protocol.ChatMessage{CharName: "Phoenix", Effects: "tv on"})
	if gotFolder != "transitions" {
		t.Errorf("folder = %q, want the SENDER's char.ini [Options] effects", gotFolder)
	}
	// A 3-part field carries the folder on the wire; the fallback must not override it.
	room.fireMessageEffects(&protocol.ChatMessage{CharName: "Phoenix", Effects: "tv on|otherpack|snd"})
	if gotFolder != "otherpack" {
		t.Errorf("folder = %q, want the wire's own folder", gotFolder)
	}
}

// TestCharINIParsesEffectsFolder.
func TestCharINIParsesEffectsFolder(t *testing.T) {
	ini, err := ParseCharINI([]byte("[Options]\nchat = yttd\neffects = transitions\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ini.Effects != "transitions" {
		t.Errorf("Effects = %q, want transitions", ini.Effects)
	}
	if ini.Chat != "yttd" {
		t.Errorf("Chat = %q — the neighbouring key must not be disturbed", ini.Chat)
	}
}

// TestCharINIParsesRealization — [Options] realization is the source of AO2's
// get_custom_realization (text_file_functions.cpp:896-903), which overrides BOTH
// the effects.ini sound of the "realization" effect (:889-892) and the legacy
// REALIZATION=1 sound (courtroom.cpp:4175).
func TestCharINIParsesRealization(t *testing.T) {
	ini, err := ParseCharINI([]byte("[Options]\neffects = transitions\nrealization = sfx-mine\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ini.Realization != "sfx-mine" {
		t.Errorf("Realization = %q, want sfx-mine", ini.Realization)
	}
	if ini.Effects != "transitions" {
		t.Errorf("Effects = %q — the neighbouring key must not be disturbed", ini.Effects)
	}
	// Absent is empty, which is "the character declares none" — the theme's own
	// courtroom_sounds.ini realization is then the fallback, exactly as at :900.
	bare, err := ParseCharINI([]byte("[Options]\nshowname = Nick\n"))
	if err != nil {
		t.Fatal(err)
	}
	if bare.Realization != "" {
		t.Errorf("Realization = %q with no key, want empty", bare.Realization)
	}
}

// TestCustomRealizationOverridesTheThemeSound — the RECEIVE half of canon's
// realization special case. do_flash's companion line plays
// get_custom_realization(CHAR_NAME) (courtroom.cpp:4175), i.e. the SENDER's own
// char.ini [Options] realization, NOT the theme's court sfx. A character shipping
// its own realization sound must be heard on every client, not only its owner's.
func TestCustomRealizationOverridesTheThemeSound(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	room.RealizationSFX = "https://x.test/base/sounds/general/themerealize"

	// No hook wired (headless / the maker preview): the theme's sound plays, which
	// is exactly the behaviour this replaced.
	room.fireMessageEffects(&protocol.ChatMessage{Realization: true, CharName: "Phoenix"})
	if len(audio.sfx) != 1 || audio.sfx[0] != room.RealizationSFX {
		t.Fatalf("with no hook the theme's realization must play, got %v", audio.sfx)
	}

	// The speaker declares one: it wins.
	audio.sfx = nil
	room.CustomRealizationFor = func(char string) string {
		if char == "Phoenix" {
			return "https://x.test/base/sounds/general/nickrealize"
		}
		return ""
	}
	room.fireMessageEffects(&protocol.ChatMessage{Realization: true, CharName: "Phoenix"})
	if len(audio.sfx) != 1 || audio.sfx[0] != "https://x.test/base/sounds/general/nickrealize" {
		t.Fatalf("the SENDER's own realization sound must win (courtroom.cpp:4175), got %v", audio.sfx)
	}

	// A speaker that declares none falls back to the theme's, not to silence.
	audio.sfx = nil
	room.fireMessageEffects(&protocol.ChatMessage{Realization: true, CharName: "Edgeworth"})
	if len(audio.sfx) != 1 || audio.sfx[0] != room.RealizationSFX {
		t.Fatalf("an undeclared realization must fall back to the theme's (text_file_functions.cpp:900), got %v", audio.sfx)
	}

	// And the 2.8 EFFECTS field still suppresses this path entirely (it is an
	// else-if, courtroom.cpp:4173-4177) — the override must not resurrect it.
	audio.sfx = nil
	room.fireMessageEffects(&protocol.ChatMessage{Realization: true, Effects: "realization||", CharName: "Phoenix"})
	if len(audio.sfx) != 0 {
		t.Errorf("an EFFECTS field must suppress the legacy realization sound, got %v", audio.sfx)
	}
}

// TestOutgoingEffectsTriple (courtroom.cpp:2296-2308).
func TestOutgoingEffectsTriple(t *testing.T) {
	if got := BuildEffectsField("tv on", "transitions", "switch tv"); got != "tv on|transitions|switch tv" {
		t.Errorf("field = %q", got)
	}
	if got := BuildEffectsField("flash", "", ""); got != "flash||" {
		t.Errorf("field = %q, want the empty folder/sound slots preserved (AO2 always sends three)", got)
	}
	if got := BuildEffectsField("", "transitions", "x"); got != "" {
		t.Errorf("the None row must send %q, not %q — an empty name can never be mistaken for a real effect the way a localised word can", "", got)
	}
}

// TestOverlayEffectMiscCandidates — rung 8, the ONE network rung. Two casings,
// slashes preserved, spaces escaped per SEGMENT (a whole-value PathEscape mints a
// dead %2F URL, #40).
func TestOverlayEffectMiscCandidates(t *testing.T) {
	u := NewURLBuilder("https://example.test/base")
	got := u.OverlayEffectMisc("Transitions", "fade to black")
	if len(got) != 2 {
		t.Fatalf("candidates = %v, want 2 (lowercase identity + authored casing)", got)
	}
	if got[0] != "https://example.test/base/misc/transitions/fade%20to%20black" {
		t.Errorf("candidate[0] = %q", got[0])
	}
	if got[1] != "https://example.test/base/misc/Transitions/fade%20to%20black" {
		t.Errorf("candidate[1] = %q — the authored spelling is the chain alt", got[1])
	}
	if same := u.OverlayEffectMisc("transitions", "flash"); len(same) != 1 {
		t.Errorf("an already-lowercase folder+name must yield ONE candidate, got %v", same)
	}
	icons := u.OverlayEffectMiscIcon("transitions", "tv on")
	if len(icons) != 1 || icons[0] != "https://example.test/base/misc/transitions/icons/tv%20on" {
		t.Errorf("icon candidates = %v (courtroom.cpp:5608 asks for the element \"icons/<name>\")", icons)
	}
	if got := u.OverlayEffectManifest("Transitions"); got != "https://example.test/base/misc/transitions/effects.ini" {
		t.Errorf("manifest URL = %q", got)
	}
	if got := u.OverlayEffectManifest(""); got != "" {
		t.Errorf("no folder means no manifest URL, got %q", got)
	}
	if got := u.OverlayEffectMisc("", "flash"); got != nil {
		t.Errorf("no folder means no network rung at all, got %v", got)
	}
}

// TestOverlayLayerMappingIsExhaustive — the theme→scene layer conversion must
// carry every value; a silent default here would put every effect on chat.
func TestOverlayLayerMappingIsExhaustive(t *testing.T) {
	for _, tc := range []struct {
		in   theme.OverlayLayer
		want uint8
	}{
		{theme.OverlayLayerChat, OverlayLayerChat},
		{theme.OverlayLayerBehind, OverlayLayerBehind},
		{theme.OverlayLayerCharacter, OverlayLayerCharacter},
		{theme.OverlayLayerOver, OverlayLayerOver},
	} {
		if got := overlayLayerFromTheme(tc.in); got != tc.want {
			t.Errorf("overlayLayerFromTheme(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestVisualGateOffCostsNoResolve — the streaming seam. c.OverlayFor is not a
// lookup: it fetches a misc manifest, reads+decodes a theme file into T1, or
// probes the origin. With the screen-effects toggle off (or reduce motion on) the
// art can never be drawn, so the gate has to come BEFORE the resolve — canon
// affords the other order because get_effect is a local stat (courtroom.cpp:3188
// precedes :3202); we cannot. The precedent is remoteChatSkinFor, which gates on
// its pref so turning skins off also stops the misc art fetches.
func TestVisualGateOffCostsNoResolve(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		screenEffects, reduceMot bool
	}{
		{"screen effects off", false, false},
		{"reduce motion on", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			room, _, _, audio := newCourtroomRig(t)
			resolves := 0
			room.OverlayFor = func(name, folder string) (OverlayResolved, bool) {
				resolves++
				return OverlayResolved{Base: "fx://" + name}, true
			}
			room.ScreenEffects, room.ReduceMotion = tc.screenEffects, tc.reduceMot
			room.fireMessageEffects(&protocol.ChatMessage{Effects: "wash|packpack|boom"})
			if resolves != 0 {
				t.Errorf("the resolver ran %d time(s) with the visual gate off — that is a manifest fetch, a decode and a VRAM page for art that is cleared on the next line", resolves)
			}
			if room.Scene.Overlay.Active() {
				t.Error("no overlay may be on screen with the visual gate off")
			}
			if len(audio.sfx) != 1 {
				t.Errorf("the effect sound must still play (it is fired outside fireOverlay), got %v", audio.sfx)
			}
		})
	}
}

// TestPendingRosterDefersInsteadOfArmingWithDefaults — a resolve that has not
// landed carries BOTH the local ladder's art paths (rungs 1-7) and the merged
// properties, so answering it early would probe the network for art the theme may
// already have on disk (spec §1.f: rung 8 is the ONLY network rung) and stage the
// effect with a zero-valued OverlayFX. It defers and re-asks instead.
func TestPendingRosterDefersInsteadOfArmingWithDefaults(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	inFlight := true
	room.OverlayFor = func(name, folder string) (OverlayResolved, bool) {
		if inFlight {
			return OverlayResolved{Pending: true}, true
		}
		return OverlayResolved{
			FX:   theme.OverlayFX{Loop: true, Stretch: true, Layer: theme.OverlayLayerOver},
			Base: "theme://fx/tv on",
		}, true
	}
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "tv on|transitions|"})
	if room.Scene.Overlay.Active() {
		t.Fatal("a deferred effect must NOT arm before its roster lands — it would play at the wrong layer with default geometry")
	}
	inFlight = false
	room.Update(overlayPendingRetryInterval)
	o := room.Scene.Overlay
	if !o.Active() {
		t.Fatal("the deferred effect never armed after the roster landed")
	}
	if !o.Loop || !o.Stretch || o.Layer != OverlayLayerOver {
		t.Errorf("the deferred arm used DEFAULT properties, not the landed file's: %+v", o)
	}
	// A clear (a superseding message, an area swap) cancels the deferred arm, or a
	// dead request would pop up over a later line.
	inFlight = true
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "tv on|transitions|"})
	room.clearOverlay()
	inFlight = false
	room.Update(overlayPendingRetryInterval)
	if room.Scene.Overlay.Active() {
		t.Error("a cancelled deferred arm came back to life")
	}
}

// TestPendingOverlayGivesUpAndFallsBack — the wait is bounded (a resolve whose
// result was dropped on channel overflow never lands), and when it expires the
// request settles exactly as an unresolvable name does: cleared, with the legacy
// flash/shake fallback so a bare install still reacts to "realization".
func TestPendingOverlayGivesUpAndFallsBack(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	asks := 0
	room.OverlayFor = func(name, folder string) (OverlayResolved, bool) {
		asks++
		return OverlayResolved{Pending: true}, true
	}
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "realization|pack|"})
	if room.Scene.FlashLeft != 0 {
		t.Fatal("the legacy fallback must WAIT for the resolve, never fire beside an overlay that may still arm")
	}
	const pendingTestTick = 100 * time.Millisecond
	for i := 0; i < int(overlayPendingTTL/pendingTestTick); i++ {
		room.Update(pendingTestTick)
	}
	if room.Scene.FlashLeft == 0 {
		t.Error("a resolve that never lands must still settle — the legacy flash is the fallback")
	}
	if limit := int(overlayPendingTTL/overlayPendingRetryInterval) + 2; asks > limit {
		t.Errorf("re-asks = %d, limit %d — the retry must be paced and bounded", asks, limit)
	}
	// And it is over: no further re-asks, no late arm.
	before := asks
	room.Scene.FlashLeft = 0
	for i := 0; i < 20; i++ {
		room.Update(pendingTestTick)
	}
	if asks != before || room.Scene.FlashLeft != 0 {
		t.Errorf("the pending slot kept running after it gave up (asks %d → %d)", before, asks)
	}
}
