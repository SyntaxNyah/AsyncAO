package courtroom

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// --- helpers -------------------------------------------------------------------

type sentRecorder struct {
	packets []protocol.Packet
}

func (r *sentRecorder) send(p protocol.Packet) error {
	r.packets = append(r.packets, p)
	return nil
}

func (r *sentRecorder) headers() []string {
	out := make([]string, len(r.packets))
	for i, p := range r.packets {
		out[i] = p.Header
	}
	return out
}

type audioRecorder struct {
	shouts, sfx, blips, music []string
	stops                     int
	blipScale                 int
	// musicLoop/musicEffects record the last PlayMusic call's 2.9 semantics (#15).
	musicLoop    []bool
	musicEffects []int
	// sfxDelays records the delay passed to each PlaySFX (#12 deadline math).
	sfxDelays []time.Duration
}

func (a *audioRecorder) PlayShout(base string) { a.shouts = append(a.shouts, base) }
func (a *audioRecorder) PlaySFX(b string, d time.Duration) {
	a.sfx = append(a.sfx, b)
	a.sfxDelays = append(a.sfxDelays, d)
}
func (a *audioRecorder) PlayBlip(base string) { a.blips = append(a.blips, base) }
func (a *audioRecorder) SetBlipScale(pct int) { a.blipScale = pct }
func (a *audioRecorder) PlayMusic(url string, loop bool, effects int) {
	a.music = append(a.music, url)
	a.musicLoop = append(a.musicLoop, loop)
	a.musicEffects = append(a.musicEffects, effects)
}
func (a *audioRecorder) StopMusic() { a.stops++ }

// newCourtroomRig builds a courtroom over a no-network manager (local mounts
// pointing at an empty dir — prefetches resolve to warnings, which the
// lifecycle ignores).
func newCourtroomRig(t *testing.T) (*Courtroom, *Session, *sentRecorder, *audioRecorder) {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	resolver := assets.NewResolver(prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	local := assets.NewLocalFetcher([]string{t.TempDir()})
	mgr := assets.NewManager(assets.ManagerDeps{
		Resolver: resolver, Prefs: prefs, T2: t2, Disk: disk,
		Source: local, LocalMode: true, Pool: pool, Decoder: decoder,
	})

	rec := &sentRecorder{}
	sess := NewSession(rec.send, "test-hdid")
	audio := &audioRecorder{}
	room := NewCourtroom(NewURLBuilder(local.BaseURL()), mgr, sess, audio)
	return room, sess, rec, audio
}

func feed(t *testing.T, s *Session, raw string) []Event {
	t.Helper()
	p, err := protocol.ParsePacket(raw)
	if err != nil {
		t.Fatalf("bad fixture packet %q: %v", raw, err)
	}
	return s.HandlePacket(p)
}

// TestSyntheticMessageRendersSpeaker pins the load-bearing assumption behind the
// Scene Maker and replay: a HAND-BUILT message — CharName + Emote only, with no
// CharID and no populated char list — drives a renderable speaker. The courtroom
// builds the sprite URL straight from CharName/Emote (speakerName := msg.CharName
// → urls.Emote), so it does NOT need a CharID→char-list lookup. That's why a
// "build a scene from scratch" line, and a nil-session replay, both render. If
// this ever regresses to a char-list lookup, from-scratch scenes go blank.
func TestSyntheticMessageRendersSpeaker(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	msg := &protocol.ChatMessage{
		CharName: "Phoenix",
		Emote:    "normal",
		Message:  "Objection!",
		Side:     "wit",
		EmoteMod: protocol.EmoteModIdle, // the Scene Maker's default — no preanim stall
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})

	if !room.Scene.Speaker.Visible {
		t.Fatal("synthetic message produced no visible speaker")
	}
	if room.Scene.Speaker.Name != "Phoenix" {
		t.Errorf("speaker name = %q, want Phoenix (msg.CharName)", room.Scene.Speaker.Name)
	}
	if room.Scene.Speaker.IdleBase == "" {
		t.Fatal("speaker has no idle sprite URL — a from-scratch line would render blank")
	}
	low := strings.ToLower(room.Scene.Speaker.IdleBase)
	if !strings.Contains(low, "phoenix") || !strings.Contains(low, "normal") {
		t.Errorf("sprite URL %q not built from CharName/Emote — render must not depend on a char list", room.Scene.Speaker.IdleBase)
	}
}

// TestSkipToIdle pins the replay player's "next message" fast-forward: a message
// mid-play is driven straight to idle (so the next event can be fed).
func TestSkipToIdle(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.HandleEvent(Event{Kind: EventMessage, Message: &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "wit",
		Message: "A fairly long line that would take a while to type out at normal speed.",
	}})
	room.Update(10 * time.Millisecond) // a beat in — still typing
	if room.Phase() == PhaseIdle {
		t.Fatal("message should be on stage right after it begins, not idle")
	}
	room.SkipToIdle()
	if room.Phase() != PhaseIdle {
		t.Fatalf("SkipToIdle did not reach idle, phase = %v", room.Phase())
	}
}

// TestReduceMotionGatesEffects pins the M14 accessibility gate: ReduceMotion
// suppresses the screen shake + realization flash but KEEPS the feedback sound;
// with it off, the visuals fire. Covers all three trigger paths (the 2.8 Effects
// field, plain Realization, and the emote-mod screenshake).
func TestReduceMotionGatesEffects(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)

	// Reduce-motion ON: visuals gated, the audio cue still plays.
	room.ReduceMotion = true
	room.Scene.ShakeLeft, room.Scene.FlashLeft = 0, 0
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "screenshake|boom"})
	if room.Scene.ShakeLeft != 0 {
		t.Errorf("reduce-motion must suppress the Effects screenshake, ShakeLeft=%v", room.Scene.ShakeLeft)
	}
	if len(audio.sfx) == 0 {
		t.Error("reduce-motion must KEEP the effect sound — only the visual is gated")
	}
	room.fireMessageEffects(&protocol.ChatMessage{Realization: true})
	if room.Scene.FlashLeft != 0 {
		t.Errorf("reduce-motion must suppress the realization flash, FlashLeft=%v", room.Scene.FlashLeft)
	}
	room.fireMessageEffects(&protocol.ChatMessage{Screenshake: true, EmoteMod: protocol.EmoteModIdle})
	if room.Scene.ShakeLeft != 0 {
		t.Errorf("reduce-motion must suppress the emote-mod screenshake, ShakeLeft=%v", room.Scene.ShakeLeft)
	}

	// Reduce-motion OFF: the effects fire.
	room.ReduceMotion = false
	room.Scene.ShakeLeft, room.Scene.FlashLeft = 0, 0
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "screenshake"})
	if room.Scene.ShakeLeft != ScreenshakeDuration {
		t.Errorf("screenshake must fire without reduce-motion, ShakeLeft=%v", room.Scene.ShakeLeft)
	}
	room.fireMessageEffects(&protocol.ChatMessage{Realization: true})
	if room.Scene.FlashLeft != RealizationFlashDuration {
		t.Errorf("flash must fire without reduce-motion, FlashLeft=%v", room.Scene.FlashLeft)
	}
	room.Scene.ShakeLeft = 0
	room.fireMessageEffects(&protocol.ChatMessage{Screenshake: true, EmoteMod: protocol.EmoteModZoom})
	if room.Scene.ShakeLeft != ScreenshakeDuration {
		t.Errorf("emote-mod screenshake must fire without reduce-motion, ShakeLeft=%v", room.Scene.ShakeLeft)
	}
}

// TestReduceMotionStripsAllTransmittedMotion pins the #6 accessibility strip: a
// speaker's transmitted style loses EVERY continuously-animating field under the
// viewer's ReduceMotion — the named Wobble/Spin/Motion, a CUSTOM drawn Path (which
// OVERRIDES Motion), the HueCycle rainbow, and Glitch (+GlitchStatic's flicker, a
// photosensitivity hazard) — while the static recolour survives. Mirrors the
// animated-text doctrine (everything pinned static under reduce-motion).
func TestReduceMotionStripsAllTransmittedMotion(t *testing.T) {
	// A style that exercises every animating field at once, plus a static tint that
	// must survive. A valid path needs PathLen>=2 real bytes so it round-trips the codec.
	styled := SpriteStyle{
		Tint: true, R: 255, G: 128, B: 0, // static recolour — stays
		Wobble: true, Spin: true, Motion: MotionOrbit,
		Path: [maxPathPoints]uint8{0x11, 0x88, 0xEE}, PathLen: 3, // custom path OVERRIDES Motion
		HueCycle: true,
		Glitch:   true, GlitchMode: GlitchStatic, // the flicker look
	}
	msg := &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "def", CharID: 0,
		Message: styled.EncodeChangeMarker(SpriteStyle{}) + "Objection!",
	}

	room, _, _, _ := newCourtroomRig(t)
	room.ReduceMotion = true
	room.begin(msg)
	st := room.Scene.Speaker.Style
	if st.Wobble || st.Spin || st.Motion != 0 || st.PathLen != 0 || st.HueCycle || st.Glitch || st.GlitchMode != 0 {
		t.Errorf("ReduceMotion left a transmitted-motion field set: %+v", st)
	}
	if !st.Tint || st.R != 255 || st.G != 128 || st.B != 0 {
		t.Errorf("ReduceMotion must strip only motion — the static recolour must survive, got %+v", st)
	}

	// Sanity: with ReduceMotion OFF the same message keeps every field, so the
	// strip is what removed them above (not a decode failure).
	room2, _, _, _ := newCourtroomRig(t)
	room2.ReduceMotion = false
	room2.begin(msg)
	if st2 := room2.Scene.Speaker.Style; !st2.Wobble || !st2.Spin || st2.Motion != MotionOrbit ||
		st2.PathLen != 3 || !st2.HueCycle || !st2.Glitch || st2.GlitchMode != GlitchStatic {
		t.Errorf("without ReduceMotion the transmitted motion must survive intact, got %+v", st2)
	}
}

// TestDeskVisiblePhaseMatrix pins #16: the per-phase desk table against AO2's two
// switch statements (courtroom.cpp:4075-4091 preanim, :4134-4152 talk/idle). The
// old collapse showed the desk during a mod-2 preanim and hid it during mod-3.
func TestDeskVisiblePhaseMatrix(t *testing.T) {
	// {mod, preanim-visible, talk-visible}
	cases := []struct {
		mod           int
		preanim, talk bool
	}{
		{protocol.DeskHide, false, false},
		{protocol.DeskShow, true, true},
		{protocol.DeskEmoteOnly, false, true},   // desk only while talking
		{protocol.DeskPreOnly, true, false},     // desk only during the preanim
		{protocol.DeskEmoteOnlyEx, false, true}, // EX mirrors its base for desk visibility
		{protocol.DeskPreOnlyEx, true, false},
	}
	for _, tc := range cases {
		if got := deskVisible(tc.mod, true); got != tc.preanim {
			t.Errorf("deskVisible(mod=%d, preanim) = %v, want %v", tc.mod, got, tc.preanim)
		}
		if got := deskVisible(tc.mod, false); got != tc.talk {
			t.Errorf("deskVisible(mod=%d, talk) = %v, want %v", tc.mod, got, tc.talk)
		}
	}
}

// TestDeskModExHidesPairPerPhase pins the #16 mods 4/5 behaviour against AO2's two
// switch statements (play_preanim courtroom.cpp:4076-4082 / start_chat_ticking
// :4135-4146). Pair-hide and offset-zero are DECOUPLED and asymmetric.
//
// Mod 4 (EMOTE_ONLY_EX): preanim hides the sideplayer + move(0,0); talk restores ONLY
// the offset (set_self_offset) and never re-shows the sideplayer — so the pair stays
// HIDDEN through both phases, offset zeroed in preanim, restored in talk.
//
// Mod 5 (PRE_ONLY_EX): the inverse for the offset — pair+offset shown in the preanim,
// both hidden/zeroed in talk.
func TestDeskModExHidesPairPerPhase(t *testing.T) {
	mk := func(mod int) *protocol.ChatMessage {
		return &protocol.ChatMessage{
			CharName: "Phoenix", Emote: "normal", Side: "def", DeskMod: mod,
			SelfOffsetX: 20, SelfOffsetY: -8,
			Pair: protocol.ParsePair("4", "Edgeworth", "thinking", "0", "0"),
		}
	}

	// Mod 4 (EMOTE_ONLY_EX): pair hidden in BOTH phases; offset zeroed in the preanim,
	// restored in talk (AO2 start_chat_ticking restores set_self_offset only).
	room, _, _, _ := newCourtroomRig(t)
	room.current = mk(protocol.DeskEmoteOnlyEx)
	room.Scene.Speaker = SpriteLayer{}
	room.applyDeskMods(true) // preanim
	if room.Scene.PairActive || room.Scene.Speaker.OffsetX != 0 || room.Scene.Speaker.OffsetY != 0 {
		t.Errorf("mod4 preanim: want pair hidden + offset zeroed, got PairActive=%v off=(%d,%d)",
			room.Scene.PairActive, room.Scene.Speaker.OffsetX, room.Scene.Speaker.OffsetY)
	}
	room.applyDeskMods(false) // talk restores the offset but keeps the pair hidden
	if room.Scene.PairActive || room.Scene.Speaker.OffsetX != 20 || room.Scene.Speaker.OffsetY != -8 {
		t.Errorf("mod4 talk: want pair STILL hidden + offset restored, got PairActive=%v off=(%d,%d)",
			room.Scene.PairActive, room.Scene.Speaker.OffsetX, room.Scene.Speaker.OffsetY)
	}

	// Mod 5 (PRE_ONLY_EX): the inverse — solo during TALK, paired during the preanim.
	room2, _, _, _ := newCourtroomRig(t)
	room2.current = mk(protocol.DeskPreOnlyEx)
	room2.Scene.Speaker = SpriteLayer{}
	room2.applyDeskMods(false) // talk
	if room2.Scene.PairActive || room2.Scene.Speaker.OffsetX != 0 {
		t.Errorf("mod5 talk: want pair hidden + offset zeroed, got PairActive=%v offX=%d",
			room2.Scene.PairActive, room2.Scene.Speaker.OffsetX)
	}
	room2.applyDeskMods(true) // preanim restores
	if !room2.Scene.PairActive || room2.Scene.Speaker.OffsetX != 20 {
		t.Errorf("mod5 preanim: want pair + offset restored, got PairActive=%v offX=%d",
			room2.Scene.PairActive, room2.Scene.Speaker.OffsetX)
	}
}

// TestAdditiveText pins #14: an ADDITIVE=1 line pre-reveals the prior accumulated
// text (typewriter starts at the prefix's rune count, so only the tail crawls),
// matching AO2's additive_previous accumulator — which appends on ANY additive
// line with NO char-id gate (courtroom.cpp:4225-4330) and resets only on a
// non-additive line. A non-additive line, or the pref OFF, both fall back to replace.
func TestAdditiveText(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.AdditiveText = true

	// First line: no additive, plain replace — begins from empty.
	room.begin(&protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", CharID: 3, Message: "Hello"})
	if room.additivePrefix != "" {
		t.Fatalf("first line must not append, prefix=%q", room.additivePrefix)
	}
	if room.Scene.MessageText != "Hello" || room.Scene.VisibleRunes != 0 {
		t.Fatalf("first line: text=%q visible=%d, want Hello/0", room.Scene.MessageText, room.Scene.VisibleRunes)
	}

	// Same speaker, ADDITIVE=1 → appends. The prior "Hello" (5 runes) is pre-revealed.
	room.begin(&protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", CharID: 3, Additive: true, Message: " world"})
	if room.additivePrefix != "Hello" {
		t.Fatalf("append prefix=%q, want Hello", room.additivePrefix)
	}
	if room.Scene.MessageText != "Hello world" {
		t.Errorf("appended text=%q, want 'Hello world'", room.Scene.MessageText)
	}
	if room.Scene.VisibleRunes != 5 {
		t.Errorf("pre-revealed runes=%d, want 5 (the prior 'Hello')", room.Scene.VisibleRunes)
	}

	// A DIFFERENT speaker with ADDITIVE=1 STILL appends — AO2 has no char-id gate on
	// the accumulator, so "Hello world" (11 runes) pre-reveals ahead of "Objection".
	room.begin(&protocol.ChatMessage{CharName: "Edgeworth", Emote: "normal", CharID: 4, Additive: true, Message: "Objection"})
	if room.additivePrefix != "Hello world" {
		t.Errorf("cross-speaker additive must still append (AO2 has no char gate): prefix=%q, want 'Hello world'", room.additivePrefix)
	}
	if room.Scene.MessageText != "Hello worldObjection" || room.Scene.VisibleRunes != 11 {
		t.Errorf("cross-speaker append: text=%q visible=%d, want 'Hello worldObjection'/11", room.Scene.MessageText, room.Scene.VisibleRunes)
	}

	// A NON-additive line resets the accumulator (AO2 additive_previous = "" at :4229).
	room.begin(&protocol.ChatMessage{CharName: "Edgeworth", Emote: "normal", CharID: 4, Message: "Take that!"})
	if room.additivePrefix != "" || room.Scene.MessageText != "Take that!" || room.Scene.VisibleRunes != 0 {
		t.Errorf("non-additive line must reset: prefix=%q text=%q visible=%d", room.additivePrefix, room.Scene.MessageText, room.Scene.VisibleRunes)
	}

	// Pref OFF: additive is ignored entirely (replace behavior), even from the same speaker.
	room.AdditiveText = false
	room.begin(&protocol.ChatMessage{CharName: "Edgeworth", Emote: "normal", CharID: 4, Additive: true, Message: "!"})
	if room.additivePrefix != "" || room.Scene.MessageText != "!" || room.Scene.VisibleRunes != 0 {
		t.Errorf("pref-off additive still appended: prefix=%q text=%q visible=%d",
			room.additivePrefix, room.Scene.MessageText, room.Scene.VisibleRunes)
	}
}

// TestMuteHandling pins #13: MU/UM set/clear Session.Muted only when the target
// cid is OURS or -1 (mute-all), emit EventMuted on a real change, and a char
// change (PV) defensively clears the mute (a mute is keyed to a cid).
func TestMuteHandling(t *testing.T) {
	_, sess, _, _ := newCourtroomRig(t)
	sess.MyCharID = 5

	// A mute for a DIFFERENT cid is ignored.
	if evs := feed(t, sess, "MU#9#%"); len(evs) != 0 || sess.Muted {
		t.Fatalf("MU for another cid must not mute us: muted=%v evs=%v", sess.Muted, evs)
	}

	// A mute for OUR cid mutes + fires EventMuted(1).
	evs := feed(t, sess, "MU#5#%")
	if !sess.Muted {
		t.Fatal("MU for our cid must set Muted")
	}
	if len(evs) != 1 || evs[0].Kind != EventMuted || evs[0].Int != 1 {
		t.Fatalf("MU must emit EventMuted(1), got %v", evs)
	}

	// A redundant MU is a no-op (no chip spam).
	if evs := feed(t, sess, "MU#5#%"); len(evs) != 0 {
		t.Fatalf("redundant MU must emit nothing, got %v", evs)
	}

	// UM for our cid unmutes + fires EventMuted(0).
	evs = feed(t, sess, "UM#5#%")
	if sess.Muted {
		t.Fatal("UM for our cid must clear Muted")
	}
	if len(evs) != 1 || evs[0].Kind != EventMuted || evs[0].Int != 0 {
		t.Fatalf("UM must emit EventMuted(0), got %v", evs)
	}

	// Mute-all (cid -1) always applies.
	if feed(t, sess, "MU#-1#%"); !sess.Muted {
		t.Fatal("MU#-1 (mute-all) must mute us")
	}

	// A char change (PV) clears the stale mute.
	feed(t, sess, "PV#0#CID#7#%")
	if sess.Muted {
		t.Fatal("a char change must clear the mute (cid-keyed)")
	}
	if sess.MyCharID != 7 {
		t.Fatalf("PV must set MyCharID, got %d", sess.MyCharID)
	}
}

// TestScreenEffectsGatesEffects pins the dedicated ScreenEffects toggle (v1.55.7,
// ON by default): with it OFF, both the field-based shake/flash and the inline
// \s/\f codes are suppressed even when reduce-motion is off, while the effect
// SOUND still plays; with it on, they fire.
func TestScreenEffectsGatesEffects(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	room.ReduceMotion = false // isolate the ScreenEffects gate from accessibility

	// ScreenEffects OFF: field-based visual gated, the audio cue still plays.
	room.ScreenEffects = false
	room.Scene.ShakeLeft, room.Scene.FlashLeft = 0, 0
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "screenshake|boom"})
	if room.Scene.ShakeLeft != 0 {
		t.Errorf("ScreenEffects off must suppress the screenshake, ShakeLeft=%v", room.Scene.ShakeLeft)
	}
	if len(audio.sfx) == 0 {
		t.Error("ScreenEffects off must KEEP the effect sound — only the visual is gated")
	}
	room.fireMessageEffects(&protocol.ChatMessage{Realization: true})
	if room.Scene.FlashLeft != 0 {
		t.Errorf("ScreenEffects off must suppress the realization flash, FlashLeft=%v", room.Scene.FlashLeft)
	}
	// Inline \s/\f share the same gate (effectsVisible), so they're suppressed too.
	room.fireInlineEffect(EffectMark{Kind: EffectShake})
	room.fireInlineEffect(EffectMark{Kind: EffectFlash})
	if room.Scene.ShakeLeft != 0 || room.Scene.FlashLeft != 0 {
		t.Errorf("ScreenEffects off must suppress inline \\s/\\f, shake=%v flash=%v", room.Scene.ShakeLeft, room.Scene.FlashLeft)
	}

	// ScreenEffects ON: the field path and the inline codes fire.
	room.ScreenEffects = true
	room.Scene.ShakeLeft, room.Scene.FlashLeft = 0, 0
	room.fireMessageEffects(&protocol.ChatMessage{Effects: "flash"})
	if room.Scene.FlashLeft != RealizationFlashDuration {
		t.Errorf("flash must fire with ScreenEffects on, FlashLeft=%v", room.Scene.FlashLeft)
	}
	room.fireInlineEffect(EffectMark{Kind: EffectShake})
	if room.Scene.ShakeLeft != ScreenshakeDuration {
		t.Errorf("inline \\s must fire with ScreenEffects on, ShakeLeft=%v", room.Scene.ShakeLeft)
	}
}

// --- URL builder ----------------------------------------------------------------

func TestURLBuilderConventions(t *testing.T) {
	u := NewURLBuilder("http://cdn.example.com/base/")
	cases := map[string]string{
		u.CharIcon("Phoenix"):                         "http://cdn.example.com/base/characters/phoenix/char_icon",
		u.Emote("Phoenix", "Normal", EmoteIdle):       "http://cdn.example.com/base/characters/phoenix/(a)normal",
		u.Emote("Phoenix", "Normal", EmoteTalk):       "http://cdn.example.com/base/characters/phoenix/(b)normal",
		u.Emote("Phoenix", "zoom", EmotePreanim):      "http://cdn.example.com/base/characters/phoenix/zoom",
		u.EmoteFolder("Phoenix", "Normal", EmoteIdle): "http://cdn.example.com/base/characters/phoenix/(a)/normal",
		// Nested emotes (umineko-style packs: "emotes/Witch Standard Normal/normal1",
		// often with a leading slash) keep their slashes as separators — webAO's
		// encodeURI leaves '/' literal; the old whole-value escape emitted %2F and
		// only worked where the edge normalized it back.
		u.Emote("beatrice", "/emotes/Witch Standard Normal/normal1", EmoteIdle): "http://cdn.example.com/base/characters/beatrice/(a)/emotes/witch%20standard%20normal/normal1",
		u.EmoteBare("kanon neo", "lazy/7"):                                      "http://cdn.example.com/base/characters/kanon%20neo/lazy/7",
		u.SFX("folder/Boom 1"):                                                  "http://cdn.example.com/base/sounds/general/folder/boom%201",
		u.ShoutBubble("Maya", "objection", false):                               "http://cdn.example.com/base/characters/maya/objection_bubble",
		u.ShoutBubble("Maya", "custom", true):                                   "http://cdn.example.com/base/characters/maya/custom",
		u.DefaultShoutBubble("holdit"):                                          "http://cdn.example.com/base/misc/default/holdit_bubble",
		u.ShoutSFX("Maya", "objection"):                                         "http://cdn.example.com/base/characters/maya/objection",
		u.Background("Courtroom 1", "defenseempty"):                             "http://cdn.example.com/base/background/courtroom%201/defenseempty",
		u.SFX("sfx-Stab"):                                                       "http://cdn.example.com/base/sounds/general/sfx-stab",
		u.Blip("Male"):                                                          "http://cdn.example.com/base/sounds/blips/male",
		u.BlipAuthored("YTTD"):                                                  "http://cdn.example.com/base/sounds/blips/YTTD",
		u.MusicURL("Objection.opus"):                                            "http://cdn.example.com/base/sounds/music/objection.opus",
		u.MusicURL("https://radio.example.com/x.opus"):                          "https://radio.example.com/x.opus",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("url = %q, want %q", got, want)
		}
	}
}

// TestMiscChatboxCandidates pins the chatbox-skin spelling chain: lowercase
// identity first in AO2's stem order (chat before chatbox,
// courtroom.cpp:3328), then the authored casing for case-preserving mirrors —
// and nothing more when the value is already lowercase. Nested values keep
// their slashes as path separators; spaces escape per segment. The live
// grounding: miku.pizza serves misc/yttd/chat.png for chat=YTTD (webAO-style
// lowercase mirror), a raw content mirror serves misc/HallA/chat.png as
// authored.
func TestMiscChatboxCandidates(t *testing.T) {
	u := NewURLBuilder("http://cdn.example.com/base/")

	got := u.MiscChatboxCandidates("YTTD")
	want := []string{
		"http://cdn.example.com/base/misc/yttd/chat",
		"http://cdn.example.com/base/misc/yttd/chatbox",
		"http://cdn.example.com/base/misc/YTTD/chat",
		"http://cdn.example.com/base/misc/YTTD/chatbox",
	}
	if len(got) != len(want) {
		t.Fatalf("cased candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Already-lowercase value: the authored pair would duplicate — omitted.
	got = u.MiscChatboxCandidates("chatdr")
	if len(got) != 2 || got[0] != "http://cdn.example.com/base/misc/chatdr/chat" ||
		got[1] != "http://cdn.example.com/base/misc/chatdr/chatbox" {
		t.Errorf("lowercase candidates = %v, want the two-stem pair only", got)
	}

	// Nested + spaced: slashes are separators, spaces escape, case chains.
	got = u.MiscChatboxCandidates("VA-11 HallA/Jill")
	if got[0] != "http://cdn.example.com/base/misc/va-11%20halla/jill/chat" {
		t.Errorf("nested identity = %q, want the lowercase chat stem", got[0])
	}
	if len(got) != 4 || got[2] != "http://cdn.example.com/base/misc/VA-11%20HallA/Jill/chat" {
		t.Errorf("nested authored alt = %v, want the authored chat stem third", got)
	}
}

// TestPlayMusicURLWithQueryString pins the DJ /play fix: an MC carrying a full
// http(s):// URL whose audio extension sits before a query string (Discord CDN
// links end in a signed ?ex=&is=&hm=& suffix) must reach PlayMusic, not be
// swallowed as an area transfer — while real area names still are.
func TestPlayMusicURLWithQueryString(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	const url = "https://cdn.discordapp.com/attachments/1/2/Song.click.opus?ex=6a307a65&is=6a2f28e5&hm=deadbeef&"
	room.HandleEvent(Event{Kind: EventMusic, Text: url})
	if len(audio.music) != 1 || audio.music[0] != url {
		t.Fatalf("PlayMusic(URL) = %v, want [%q]", audio.music, url)
	}
	room.HandleEvent(Event{Kind: EventMusic, Text: "Basement"}) // area name, no audio ext
	if len(audio.music) != 1 {
		t.Errorf("area transfer wrongly played as music: %v", audio.music)
	}
}

func TestIsAreaTransfer(t *testing.T) {
	cases := []struct {
		track string
		area  bool
	}{
		{"Objection.opus", false}, // server track
		{"Basement", true},        // area name
		{"https://cdn.discordapp.com/a/b/x.click.opus?ex=1&is=2&hm=3&", false}, // signed CDN link
		{"https://radio.example.com/stream", false},                            // extensionless stream URL
		{"HTTP://X/Y.MP3", false},                                              // case-insensitive scheme
	}
	for _, c := range cases {
		if got := isAreaTransfer(c.track); got != c.area {
			t.Errorf("isAreaTransfer(%q) = %v, want %v", c.track, got, c.area)
		}
	}
}

// TestNowPlayingTrack pins Scene.MusicTrack: a real track sets it, an area
// transfer leaves it, a ~stop sentinel clears it.
func TestNowPlayingTrack(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.HandleEvent(Event{Kind: EventMusic, Text: "Objection.opus"})
	if room.Scene.MusicTrack != "Objection.opus" {
		t.Errorf("MusicTrack = %q, want Objection.opus", room.Scene.MusicTrack)
	}
	room.HandleEvent(Event{Kind: EventMusic, Text: "Basement"}) // area name, no audio ext
	if room.Scene.MusicTrack != "Objection.opus" {
		t.Errorf("area transfer must not change the track, got %q", room.Scene.MusicTrack)
	}
	room.HandleEvent(Event{Kind: EventMusic, Text: "~stop.mp3"}) // stop sentinel
	if room.Scene.MusicTrack != "" {
		t.Errorf("stop must clear the track, got %q", room.Scene.MusicTrack)
	}
}

// TestStopMusicHalts pins that the ~stop sentinel HALTS playback (StopMusic)
// rather than trying to fetch+play "~stop.mp3" as a track — PlayMusic is async,
// so a 404 on the sentinel would otherwise leave the current song running.
func TestStopMusicHalts(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	room.HandleEvent(Event{Kind: EventMusic, Text: "Objection.opus"})
	if audio.stops != 0 || len(audio.music) != 1 {
		t.Fatalf("a real song must play, not stop (stops=%d music=%v)", audio.stops, audio.music)
	}
	room.HandleEvent(Event{Kind: EventMusic, Text: "~stop.mp3"})
	if audio.stops != 1 {
		t.Errorf("~stop must halt playback once, stops=%d", audio.stops)
	}
	if len(audio.music) != 1 {
		t.Errorf("~stop must NOT enqueue a fetch for the sentinel, music=%v", audio.music)
	}
	if room.Scene.MusicTrack != "" {
		t.Errorf("~stop must clear Now-Playing, got %q", room.Scene.MusicTrack)
	}
}

// TestMusicAction pins the IC "has played a song" classifier (AO2 handle_song):
// a real song → the action + a clean name (dir/query/ext stripped), the ~stop
// sentinel → "has stopped the music", an area-name transfer → not a song.
func TestMusicAction(t *testing.T) {
	cases := []struct {
		track, wantAction, wantSong string
		wantOK                      bool
	}{
		{"Cross_Examination.opus", "has played a song", "Cross_Examination", true},
		{"songs/intro.mp3", "has played a song", "intro", true}, // subdir stripped
		{"https://miku.pizza/base/sounds/music/Trial.opus?ex=1&is=2&hm=3&", "has played a song", "Trial", true},
		{"~stop.mp3", "has stopped the music", "", true},
		{"Pizza Room 3", "", "", false}, // an area name (no audio ext) — not a song
		{"", "", "", false},
	}
	for _, c := range cases {
		action, song, ok := MusicAction(c.track)
		if ok != c.wantOK || action != c.wantAction || song != c.wantSong {
			t.Errorf("MusicAction(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.track, action, song, ok, c.wantAction, c.wantSong, c.wantOK)
		}
	}
}

// TestMCEventCarriesPlayer pins that the MC parse keeps the charID (field 1) and
// showname (field 2) that name who played the song, tolerating a short legacy
// MC with neither.
func TestMCEventCarriesPlayer(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "h")
	ev := feed(t, s, "MC#Cross_Examination.opus#2#Cocoa Bean#%")
	if len(ev) != 1 || ev[0].Kind != EventMusic {
		t.Fatalf("MC events = %+v, want one EventMusic", ev)
	}
	if ev[0].Text != "Cross_Examination.opus" || ev[0].Int != 2 || ev[0].Name != "Cocoa Bean" {
		t.Errorf("EventMusic = {Text:%q Int:%d Name:%q}, want {Cross_Examination.opus 2 Cocoa Bean}",
			ev[0].Text, ev[0].Int, ev[0].Name)
	}
	ev = feed(t, s, "MC#Trial.opus#5#%") // short MC: no showname
	if ev[0].Int != 5 || ev[0].Name != "" {
		t.Errorf("short MC = {Int:%d Name:%q}, want {5 \"\"}", ev[0].Int, ev[0].Name)
	}
}

// TestKFOCompatDetect pins that KFOCompat keys off the ID packet's software name
// (KFO-Server's tsuserver.py sets self.software = "KFO-Server"), and that other
// server families do NOT trip it — the KFO MS workaround must stay KFO-only.
func TestKFOCompatDetect(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "h")
	if s.KFOCompat() {
		t.Error("a fresh session (no ID yet) must not report KFO")
	}
	feed(t, s, "ID#0#KFO-Server#1.0.0#%")
	if !s.KFOCompat() {
		t.Errorf("ID software %q must enable KFO compat", s.Software)
	}
	other := NewSession(func(protocol.Packet) error { return nil }, "h")
	feed(t, other, "ID#0#Athena#1.8.0#%")
	if other.KFOCompat() {
		t.Errorf("a non-KFO server (%q) must NOT enable KFO compat", other.Software)
	}
}

func TestPositionSceneTable(t *testing.T) {
	cases := map[string][2]string{
		"def":    {"defenseempty", "defensedesk"},
		"pro":    {"prosecutorempty", "prosecutiondesk"},
		"wit":    {"witnessempty", "stand"},
		"jud":    {"judgestand", "judgedesk"},
		"hld":    {"helperstand", "helperdesk"},
		"hlp":    {"prohelperstand", "prohelperdesk"},
		"jur":    {"jurystand", "jurydesk"},
		"sea":    {"seancestand", "seancedesk"},
		"":       {"witnessempty", "stand"},
		"podium": {"podium", "podium_overlay"}, // 2.8 unique pos
	}
	for pos, want := range cases {
		bg, desk := PositionScene(pos)
		if bg != want[0] || desk != want[1] {
			t.Errorf("PositionScene(%q) = %s/%s, want %s/%s", pos, bg, desk, want[0], want[1])
		}
	}
}

// --- session handshake ------------------------------------------------------------

func TestSessionHandshakeFlow(t *testing.T) {
	rec := &sentRecorder{}
	s := NewSession(rec.send, "hdid-123")

	feed(t, s, "decryptor#34#%")
	feed(t, s, "ID#1#tsuserver3#%")
	feed(t, s, "PN#5#100#%")
	feed(t, s, "FL#yellowtext#flipping#cccc_ic_support#fastloading#y_offset#effects#%")
	if ev := feed(t, s, "ASS#http%3A%2F%2Fcdn.example.com%2Fbase%2F#%"); len(ev) != 1 || ev[0].Kind != EventAssetURL {
		t.Fatalf("ASS events = %+v", ev)
	}
	if s.AssetURL != "http://cdn.example.com/base/" {
		t.Errorf("asset url = %q (percent-decoding broken)", s.AssetURL)
	}

	feed(t, s, "SI#3#0#10#%")
	// Population re-broadcast mid-load: must not re-send askchaa (the
	// exact reply-ladder check below would flag a duplicate).
	feed(t, s, "PN#6#100#%")
	ev := feed(t, s, "SC#Phoenix&Ace Attorney#Edgeworth#Maya#%")
	if len(ev) != 1 || ev[0].Kind != EventCharsUpdated {
		t.Fatalf("SC events = %+v", ev)
	}
	if len(s.Chars) != 3 || s.Chars[0].Name != "Phoenix" || s.Chars[0].Description != "Ace Attorney" {
		t.Fatalf("chars = %+v", s.Chars)
	}

	feed(t, s, "CharsCheck#-1#0#0#%")
	if !s.Chars[0].Taken || s.Chars[1].Taken {
		t.Error("taken flags wrong")
	}

	// fix_last_area: real AO music lists wrap songs in a category header, and
	// the header sits between the areas and the first song. It must land in
	// music, not leak into the Areas tab (and AreaInfo stays parallel).
	feed(t, s, "SM#Basement#Courtroom 1#==Cave Story OST==#Objection.opus#Trial.mp3#%")
	if len(s.Areas) != 2 || len(s.Music) != 3 {
		t.Errorf("areas=%v music=%v", s.Areas, s.Music)
	}
	if len(s.AreaInfo) != len(s.Areas) {
		t.Errorf("AreaInfo (%d) must stay parallel to Areas (%d)", len(s.AreaInfo), len(s.Areas))
	}
	if s.Music[0] != "==Cave Story OST==" {
		t.Errorf("category header must be the first music entry, got music=%v", s.Music)
	}

	ev = feed(t, s, "DONE#%")
	if len(ev) != 1 || ev[0].Kind != EventReady || s.Phase() != PhaseReady {
		t.Fatalf("DONE → %+v phase=%v", ev, s.Phase())
	}

	// Reply ladder must be exactly the fast-loading sequence. askchaa on
	// PN is what triggers SI — drop it and every server waits forever
	// (this shipped once: "handshaking" hung on all servers).
	want := []string{"HI", "ID", "askchaa", "RC", "RM", "RD"}
	got := rec.headers()
	if len(got) != len(want) {
		t.Fatalf("sent %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sent %v, want %v", got, want)
		}
	}

	if !s.Features.Has(protocol.FeatureCCCCIC) || !s.Features.Has(protocol.FeatureYOffset) {
		t.Error("features lost")
	}
}

// TestSplitAreasAndMusic pins the fix_last_area boundary (AO2-Client
// packet_distribution.cpp SM handler + courtroom.cpp:613): the category
// header preceding the first song belongs to music, never the Areas tab.
func TestSplitAreasAndMusic(t *testing.T) {
	cases := []struct {
		name              string
		fields            []string
		wantAreas, wantMx []string
	}{
		{
			"category header before songs moves to music",
			[]string{"Basement", "Courtroom 1", "==Cave Story OST==", "a.opus", "b.mp3"},
			[]string{"Basement", "Courtroom 1"},
			[]string{"==Cave Story OST==", "a.opus", "b.mp3"},
		},
		{
			"all areas, no songs: nothing moves",
			[]string{"Area 1", "Area 2", "Area 3"},
			[]string{"Area 1", "Area 2", "Area 3"},
			nil,
		},
		{
			"song first: no areas, no header to move",
			[]string{"theme.mp3", "battle.ogg"},
			nil,
			[]string{"theme.mp3", "battle.ogg"},
		},
		{
			"only the last pre-song entry moves (extra header stays an area)",
			[]string{"Area", "==A==", "==B==", "song.wav"},
			[]string{"Area", "==A=="},
			[]string{"==B==", "song.wav"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			areas, music := splitAreasAndMusic(tc.fields)
			if !slices.Equal(areas, tc.wantAreas) {
				t.Errorf("areas = %v, want %v", areas, tc.wantAreas)
			}
			if !slices.Equal(music, tc.wantMx) {
				t.Errorf("music = %v, want %v", music, tc.wantMx)
			}
		})
	}
}

func TestSessionKeepaliveAndPick(t *testing.T) {
	rec := &sentRecorder{}
	s := NewSession(rec.send, "h")
	feed(t, s, "checkconnection#%")
	if last := rec.packets[len(rec.packets)-1]; last.Header != "CH" {
		t.Errorf("keepalive reply = %s, want CH", last.Header)
	}
	if ev := feed(t, s, "PV#1#CID#4#%"); len(ev) != 1 || ev[0].Kind != EventCharPicked || s.MyCharID != 4 {
		t.Errorf("PV → %+v mychar=%d", ev, s.MyCharID)
	}
	s.PickCharacter(2)
	if last := rec.packets[len(rec.packets)-1]; last.Header != "CC" || last.Field(1) != "2" {
		t.Errorf("CC packet = %+v", rec.packets[len(rec.packets)-1])
	}
}

// TestSessionDebugEvents pins the debug lane: headers the client doesn't
// implement and malformed MS packets surface as EventDebug (never silently
// vanish), and known no-event headers stay quiet.
func TestSessionDebugEvents(t *testing.T) {
	rec := &sentRecorder{}
	s := NewSession(rec.send, "h")

	if ev := feed(t, s, "NOSUCHPACKET#1#2#%"); len(ev) != 1 || ev[0].Kind != EventDebug ||
		!strings.Contains(ev[0].Text, "NOSUCHPACKET") {
		t.Fatalf("unknown header → %+v, want one EventDebug naming it", ev)
	}
	// Malformed MS (too few fields) → dropped with a debug trace.
	if ev := feed(t, s, "MS#1#-#Phoenix#%"); len(ev) != 1 || ev[0].Kind != EventDebug ||
		!strings.Contains(ev[0].Text, "MS dropped") {
		t.Fatalf("malformed MS → %+v, want one EventDebug", ev)
	}
	// Handled headers that legitimately return no events must NOT trip the
	// debug lane (FL is reduced into Features).
	if ev := feed(t, s, "FL#flipping#%"); len(ev) != 0 {
		t.Fatalf("FL → %+v, want no events", ev)
	}
}

// TestSessionCourtPackets pins the 2.6–2.8 court-state reducers: HP, RT,
// ZZ, ARUP, TI, JD, AUTH, SD, SP, LE, CASEA — wire shapes and bounds per
// AO2-Client packet_distribution.cpp.
func TestSessionCourtPackets(t *testing.T) {
	rec := &sentRecorder{}
	s := NewSession(rec.send, "h")
	feed(t, s, "SM#Area1#Area2#==Music==#x.opus#%") // header before songs (fix_last_area)

	// HP: bar 1 = def, range-guarded like set_hp_bar.
	if ev := feed(t, s, "HP#1#3#%"); len(ev) != 1 || ev[0].Kind != EventHP || ev[0].Int != 1 || ev[0].Int2 != 3 || s.HPDef != 3 {
		t.Fatalf("HP → %+v def=%d", ev, s.HPDef)
	}
	if ev := feed(t, s, "HP#1#11#%"); ev != nil || s.HPDef != 3 {
		t.Fatalf("out-of-range HP accepted: %+v def=%d", ev, s.HPDef)
	}

	// RT: name + variant (judgeruling 1 = guilty).
	if ev := feed(t, s, "RT#judgeruling#1#%"); len(ev) != 1 || ev[0].Kind != EventWTCE || ev[0].Text != "judgeruling" || ev[0].Int != 1 {
		t.Fatalf("RT → %+v", ev)
	}

	// ZZ in: pre-formatted modcall line.
	if ev := feed(t, s, "ZZ#[101] Maya called a mod#%"); len(ev) != 1 || ev[0].Kind != EventModcall {
		t.Fatalf("ZZ → %+v", ev)
	}

	// ARUP: type 0 players, type 3 locks; field n → area n−1; overflow drops.
	feed(t, s, "ARUP#0#5#2#9#%")
	feed(t, s, "ARUP#3#FREE#LOCKED#%")
	if s.AreaInfo[0].Players != 5 || s.AreaInfo[1].Players != 2 || s.AreaInfo[1].Lock != "LOCKED" {
		t.Fatalf("ARUP state = %+v", s.AreaInfo)
	}

	// TI: show, run, pause; ids ≥ TimerCount drop.
	feed(t, s, "TI#0#2#%")
	feed(t, s, "TI#0#0#60000#%")
	if !s.Timers[0].Visible || !s.Timers[0].Running || s.Timers[0].Remaining(time.Now()) <= 50*time.Second {
		t.Fatalf("TI start state = %+v", s.Timers[0])
	}
	feed(t, s, "TI#0#1#30000#%")
	if s.Timers[0].Running || s.Timers[0].Remaining(time.Now()) != 30*time.Second {
		t.Fatalf("TI pause state = %+v", s.Timers[0])
	}
	if ev := feed(t, s, "TI#7#0#1000#%"); ev != nil {
		t.Fatalf("out-of-range timer id accepted: %+v", ev)
	}

	// JD: numeric states apply, junk ignored.
	if ev := feed(t, s, "JD#1#%"); len(ev) != 1 || s.Judge != JudgeShow {
		t.Fatalf("JD → %+v judge=%d", ev, s.Judge)
	}
	if ev := feed(t, s, "JD#nope#%"); ev != nil || s.Judge != JudgeShow {
		t.Fatalf("malformed JD accepted: %+v", ev)
	}

	// AUTH: gated on the auth_packet feature.
	if ev := feed(t, s, "AUTH#1#%"); ev != nil || s.ModGranted {
		t.Fatalf("AUTH honored without feature: %+v", ev)
	}
	feed(t, s, "FL#auth_packet#%")
	if ev := feed(t, s, "AUTH#1#%"); len(ev) != 1 || ev[0].Kind != EventAuth || !s.ModGranted {
		t.Fatalf("AUTH → %+v granted=%v", ev, s.ModGranted)
	}

	// SD splits on '*'; SP forces the side.
	feed(t, s, "SD#wit*def*pro#%")
	if len(s.PosList) != 3 || s.PosList[1] != "def" {
		t.Fatalf("SD → %v", s.PosList)
	}
	if ev := feed(t, s, "SP#def#%"); len(ev) != 1 || ev[0].Kind != EventSetPos || ev[0].Text != "def" {
		t.Fatalf("SP → %+v", ev)
	}

	// LE: '&'-nested triples. NOTE the parse-time field decode means a
	// "<and>" arrives here as a literal '&' and splits — the same lossy
	// legacy double-decode SC has (see CLAUDE.md gotchas); extra parts
	// past the triple drop.
	feed(t, s, "LE#Knife&A bloody knife&knife.png#Badge&Shiny badge&badge.png#%")
	if len(s.Evidence) != 2 || s.Evidence[0].Name != "Knife" || s.Evidence[1].Description != "Shiny badge" {
		t.Fatalf("LE → %+v", s.Evidence)
	}

	// CASEA: role bits in wire order (field 3 = judge).
	if ev := feed(t, s, "CASEA#Need a judge!#0#0#1#0#0#%"); len(ev) != 1 || ev[0].Kind != EventCase || ev[0].Int != CaseRoleJudge {
		t.Fatalf("CASEA → %+v", ev)
	}
}

// TestSessionLegacyModLogin pins the auth emulation for servers without
// auth_packet: the exact OOC confirmation line grants mod state.
func TestSessionLegacyModLogin(t *testing.T) {
	rec := &sentRecorder{}
	s := NewSession(rec.send, "h")
	ev := feed(t, s, "CT#server#Logged in as a moderator.#%")
	if len(ev) != 2 || ev[1].Kind != EventAuth || ev[1].Int != 1 || !s.ModGranted {
		t.Fatalf("legacy login → %+v granted=%v", ev, s.ModGranted)
	}
}

// TestSessionCourtSends pins the outgoing court packets' wire shapes.
func TestSessionCourtSends(t *testing.T) {
	rec := &sentRecorder{}
	s := NewSession(rec.send, "h")
	feed(t, s, "FL#modcall_reason#%")

	s.SendHP(1, 5)
	s.SendHP(2, 99) // out of range: dropped
	s.SendWTCE("testimony1", 0)
	s.SendWTCE("judgeruling", 1)
	s.CallMod("spam")
	s.AddEvidence("Knife", "Bloody", "knife.png")
	s.DeleteEvidence(2)
	s.EditEvidence(1, "Knife", "Clean", "knife.png")
	s.SetCasingPrefs(true, false, true, false, false)

	want := [][]string{
		{"HP", "1", "5"},
		{"RT", "testimony1"},
		{"RT", "judgeruling", "1"},
		{"ZZ", "spam", "-1"},
		{"PE", "Knife", "Bloody", "knife.png"},
		{"DE", "2"},
		{"EE", "1", "Knife", "Clean", "knife.png"},
		{"SETCASE", "", "1", "0", "1", "0", "0"},
	}
	if len(rec.packets) != len(want) {
		t.Fatalf("sent %d packets, want %d: %+v", len(rec.packets), len(want), rec.packets)
	}
	for i, w := range want {
		p := rec.packets[i]
		if p.Header != w[0] {
			t.Fatalf("packet %d header = %s, want %s", i, p.Header, w[0])
		}
		for j, f := range w[1:] {
			if p.Field(j) != f {
				t.Fatalf("packet %d (%s) field %d = %q, want %q", i, p.Header, j, p.Field(j), f)
			}
		}
	}
}

// TestParseCharINIDeskMod pins the desk_mod default: an emote that OMITS the
// optional 5th field shows the desk (AO2 default — get_desk_mod returns -1, a
// non-hide value), an explicit 0 hides it, and an explicit 1 shows it. Regression
// guard for the bug where the OUTGOING desk_mod was hardcoded to 1, so a no-desk
// emote (char.ini desk_mod 0) never hid the desk for the room.
func TestParseCharINIDeskMod(t *testing.T) {
	ini := []byte(`[Emotions]
number = 3
1 = plain#-#plain#1
2 = nodesk#-#nodesk#1#0
3 = hasdesk#-#hasdesk#1#1
`)
	out, err := ParseCharINI(ini)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Emotes) != 3 {
		t.Fatalf("emotes = %d, want 3", len(out.Emotes))
	}
	if got := out.Emotes[0].DeskMod; got != protocol.DeskShow {
		t.Errorf("emote 1 (no desk_mod field) DeskMod = %d, want %d (show, default)", got, protocol.DeskShow)
	}
	if got := out.Emotes[1].DeskMod; got != protocol.DeskHide {
		t.Errorf("emote 2 (explicit 0) DeskMod = %d, want %d (hide)", got, protocol.DeskHide)
	}
	if got := out.Emotes[2].DeskMod; got != protocol.DeskShow {
		t.Errorf("emote 3 (explicit 1) DeskMod = %d, want %d (show)", got, protocol.DeskShow)
	}
}

// TestParseCharINIFull pins the full char.ini coverage: per-emote audio
// sections ([SoundN]/[SoundT]/[SoundL]/[Blips]) and the [Shouts] custom
// interjection discovery (the streaming-compatible 2.10 source).
func TestParseCharINIFull(t *testing.T) {
	ini := []byte(`[Options]
showname = Nick
side = def
blips = male

[Emotions]
number = 2
1 = normal#-#normal#0#
2 = slam#slam_pre#slam#1#

[SoundN]
2 = sfx-deskslam

[SoundT]
2 = 4

[SoundL]
2 = 1

[Blips]
2 = typewriter

[Shouts]
custom_name = Eureka
wiggle_name = WIGGLE
gotcha_name = Gotcha!
`)
	out, err := ParseCharINI(ini)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Emotes) != 2 {
		t.Fatalf("emotes = %d, want 2", len(out.Emotes))
	}
	if e := out.Emotes[0]; e.SFXName != "" || e.SFXLoop || e.Blip != "" {
		t.Errorf("emote 1 audio should be empty: %+v", e)
	}
	if e := out.Emotes[1]; e.SFXName != "sfx-deskslam" || e.SFXDelay != 4 || !e.SFXLoop || e.Blip != "typewriter" {
		t.Errorf("emote 2 audio = %+v", e)
	}
	if out.CustomName != "Eureka" {
		t.Errorf("custom_name = %q", out.CustomName)
	}
	if len(out.CustomShouts) != 2 {
		t.Fatalf("custom shouts = %+v, want 2", out.CustomShouts)
	}
	names := map[string]string{}
	for _, cs := range out.CustomShouts {
		names[cs.File] = cs.Name
	}
	if names["wiggle"] != "WIGGLE" || names["gotcha"] != "Gotcha!" {
		t.Errorf("custom shouts = %v", names)
	}
}

// TestNamedCustomShoutURL pins the 2.10 custom_objections path + the
// defensive extension strip (older clients send "name.gif").
func TestNamedCustomShoutURL(t *testing.T) {
	u := NewURLBuilder("http://cdn.example.com/base/")
	want := "http://cdn.example.com/base/characters/maya/custom_objections/wiggle"
	if got := u.NamedCustomShout("Maya", "wiggle"); got != want {
		t.Errorf("plain = %q, want %q", got, want)
	}
	if got := u.NamedCustomShout("Maya", "wiggle.gif"); got != want {
		t.Errorf("with ext = %q, want %q", got, want)
	}
}

// TestTypewriter144HzZeroMiss is the frame-pacing gate: at a simulated
// 144 Hz cadence every rune must reveal within ONE frame of its schedule
// (the accumulator carries remainders perfectly — no drift, no drops),
// and the whole message lands inside one frame of its ideal duration.
func TestTypewriter144HzZeroMiss(t *testing.T) {
	const framePeriod = time.Second / 144
	tw := NewTypewriter()
	msg := strings.Repeat("The court finds the defendant... ", 8) // ~264 runes
	tw.Start(msg)

	// Ideal reveal times: rune i completes at Σ intervals[0..i].
	deadlines := make([]time.Duration, len(tw.runes))
	var acc time.Duration
	for i, iv := range tw.intervals {
		acc += iv
		deadlines[i] = acc
	}

	elapsed := time.Duration(0)
	revealedAt := make([]time.Duration, 0, len(tw.runes))
	for !tw.Done() {
		elapsed += framePeriod
		n, _ := tw.Update(framePeriod)
		for i := 0; i < n; i++ {
			revealedAt = append(revealedAt, elapsed)
		}
		if elapsed > 30*time.Second {
			t.Fatal("typewriter never finished")
		}
	}

	for i, at := range revealedAt {
		if late := at - deadlines[i]; late > framePeriod {
			t.Fatalf("rune %d revealed %v late (deadline %v, at %v)", i, late, deadlines[i], at)
		}
	}
	ideal := deadlines[len(deadlines)-1]
	if drift := elapsed - ideal; drift > framePeriod {
		t.Errorf("total duration drifted %v past the ideal %v", drift, ideal)
	}
}

// --- message lifecycle ---------------------------------------------------------------

// pairedMS builds a paired 2.8 message through the real wire shape.
func pairedMS(t *testing.T, s *Session, msgText string) *protocol.ChatMessage {
	t.Helper()
	fields := []string{
		"1", "-", "Phoenix", "normal", msgText, "def", "0", "0", "0", "0",
		"0", "0", "0", "0", "0",
		"Nick", "1^1", "Edgeworth", "thinking", "5&-10", "12&8", "1", "0",
	}
	msg, err := protocol.ParseMS(fields, s.Features, 0)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func setupReadySession(t *testing.T, s *Session) {
	feed(t, s, "FL#cccc_ic_support#flipping#y_offset#effects#%")
	feed(t, s, "SI#2#0#1#%")
	feed(t, s, "SC#Phoenix#Edgeworth#%")
	feed(t, s, "SM#x.opus#%")
	feed(t, s, "BN#gs4#%")
	feed(t, s, "DONE#%")
}

func TestPairedMessageSceneState(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)
	room.HandleEvent(Event{Kind: EventBackground, Text: sess.Background})

	msg := pairedMS(t, sess, "Take that!")
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})

	sc := &room.Scene
	if !sc.PairActive {
		t.Fatal("pair not active")
	}
	// ^1 → speaker renders BEHIND the pair (golden z-order).
	if sc.SpeakerInFront {
		t.Error("z-order: ^1 must put the speaker behind")
	}
	if !sc.Pair.Flip {
		t.Error("pair flip lost")
	}
	if sc.Pair.OffsetX != 12 || sc.Pair.OffsetY != 8 {
		t.Errorf("pair offsets = %d,%d", sc.Pair.OffsetX, sc.Pair.OffsetY)
	}
	if sc.Speaker.OffsetX != 5 || sc.Speaker.OffsetY != -10 {
		t.Errorf("speaker offsets = %d,%d", sc.Speaker.OffsetX, sc.Speaker.OffsetY)
	}
	if !strings.HasSuffix(sc.Pair.IdleBase, "characters/edgeworth/(a)thinking") {
		t.Errorf("pair idle base = %q", sc.Pair.IdleBase)
	}
	if !strings.HasSuffix(sc.BackgroundBase, "background/gs4/defenseempty") {
		t.Errorf("background base = %q", sc.BackgroundBase)
	}
	if sc.ShownameText != "Nick" {
		t.Errorf("showname = %q", sc.ShownameText)
	}
}

// TestPairedPartnerKeepsOwnStyle pins §3.7: a paired partner renders with ITS OWN
// remembered restyle, not the speaker's, and not nothing. The wire never carries a
// partner's style (protocol.PairInfo has no style field), so begin() recalls it by the
// partner's char id — the same send-on-change memory the speaker path uses. Before the
// fix Scene.Pair.Style stayed the zero value, so only whoever currently spoke showed a
// style and it flipped between the two as the turn passed (the reported symptom). Seed
// the two recalls directly (the speak→remember path is already pinned by
// spritestyle_test.go) and feed one no-marker paired message so each style comes purely
// from recall. In pairedMS the SPEAKER is char id 0 (MSCharID field 8) and the PAIR
// partner is char id 1 (MSOtherCharID field 16 = "1^1").
func TestPairedPartnerKeepsOwnStyle(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)
	room.HandleEvent(Event{Kind: EventBackground, Text: sess.Background})

	// Distinct non-zero styles (A blue, B red) so a speaker/pair swap can't pass
	// vacuously (SpriteStyle is ==-comparable). A is the speaker (char id 0), B the
	// pair partner (char id 1); the fed message carries no marker, so both styles come
	// purely from recall.
	speakerStyle := SpriteStyle{Tint: true, R: 20, G: 40, B: 255}
	pairStyle := SpriteStyle{Tint: true, R: 255, G: 40, B: 20}
	room.rememberStyle(0, speakerStyle)
	room.rememberStyle(1, pairStyle)

	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "Take that!")})

	sc := &room.Scene
	if !sc.PairActive {
		t.Fatal("pair not active")
	}
	if sc.Speaker.Style != speakerStyle {
		t.Errorf("speaker style = %+v, want its own %+v", sc.Speaker.Style, speakerStyle)
	}
	if sc.Pair.Style != pairStyle {
		t.Errorf("pair style = %+v, want the partner's remembered %+v", sc.Pair.Style, pairStyle)
	}
}

// TestPairedPartnerStyleHonorsHideSpriteStyles pins the accessibility half of §3.7: the
// viewer's HideSpriteStyles opt-out must suppress the PAIR partner's style too, not just
// the speaker's — the same filterStyleForViewer helper runs on both paths. Without the
// shared filter the fix could silently reintroduce an accessibility regression for the
// pair partner specifically (a paired player imposing a style a viewer opted out of).
func TestPairedPartnerStyleHonorsHideSpriteStyles(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)
	room.HandleEvent(Event{Kind: EventBackground, Text: sess.Background})
	room.HideSpriteStyles = true // viewer opted out of others' styles

	room.rememberStyle(0, SpriteStyle{Tint: true, R: 20, G: 40, B: 255}) // speaker
	room.rememberStyle(1, SpriteStyle{Tint: true, R: 255, G: 40, B: 20}) // pair partner

	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "Take that!")})

	sc := &room.Scene
	if !sc.PairActive {
		t.Fatal("pair not active")
	}
	if sc.Speaker.Style.Active() {
		t.Errorf("HideSpriteStyles left the speaker style active: %+v", sc.Speaker.Style)
	}
	if sc.Pair.Style.Active() {
		t.Errorf("HideSpriteStyles left the PAIR style active: %+v", sc.Pair.Style)
	}
}

// TestBlankPostDetection pins Scene.IsBlankPost: a whitespace-only or
// markup-only message is a blankpost (the UI hides the whole chatbox so only
// the sprite shows), real text is not. Decided in begin() before the phase
// switch — known from frame 1 so an animated blankpost never flashes an empty
// box during its preanim. A fresh rig per case keeps each at queue depth 1.
func TestBlankPostDetection(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"", true},       // truly empty
		{" ", true},      // AO single-space blankpost (one space rune)
		{"   ", true},    // any whitespace run
		{`\c1`, true},    // inline markup with no visible glyphs
		{"Hello", false}, // real text
		{`\c1Hi`, false}, // colored real text
	}
	for _, tc := range cases {
		room, sess, _, _ := newCourtroomRig(t)
		setupReadySession(t, sess)
		msg := pairedMS(t, sess, tc.text)
		room.HandleEvent(Event{Kind: EventMessage, Message: msg})
		if got := room.Scene.IsBlankPost; got != tc.want {
			t.Errorf("text %q: IsBlankPost = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestMessageLifecyclePhases(t *testing.T) {
	room, sess, _, audio := newCourtroomRig(t)
	setupReadySession(t, sess)

	msg := pairedMS(t, sess, "Hello!")
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if room.Phase() != PhaseTalking {
		t.Fatalf("phase = %v, want talking (no shout, no preanim)", room.Phase())
	}

	// Reveal everything: 6 runes at one rune per base interval.
	for i := 0; i < len([]rune("Hello!")); i++ {
		room.Update(DefaultCharInterval)
	}
	if room.Scene.VisibleRunes != len([]rune("Hello!")) {
		t.Errorf("visible = %d", room.Scene.VisibleRunes)
	}
	if len(audio.blips) == 0 {
		t.Error("no blips fired during reveal")
	}
	if room.Phase() != PhaseLinger {
		t.Fatalf("phase = %v, want linger", room.Phase())
	}
	room.Update(DefaultTextStayTime + time.Millisecond)
	if room.Phase() != PhaseIdle {
		t.Errorf("phase = %v, want idle", room.Phase())
	}
	if room.Scene.Speaker.Active != room.Scene.Speaker.IdleBase {
		t.Error("speaker must end on the idle loop")
	}
}

// TestBlipVolumeScalesSpeaker pins M11 per-character blip volume: begin()
// consults BlipVolumeFor with the speaker's char name and pushes that scale to
// the audio once per message. With no callback wired it defaults to full.
func TestBlipVolumeScalesSpeaker(t *testing.T) {
	room, sess, _, audio := newCourtroomRig(t)
	setupReadySession(t, sess)

	var gotChar string
	room.BlipVolumeFor = func(char string) int {
		gotChar = char
		return 30
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "Quiet")})
	if gotChar != "Phoenix" {
		t.Errorf("BlipVolumeFor got char %q, want the speaker %q", gotChar, "Phoenix")
	}
	if audio.blipScale != 30 {
		t.Errorf("blip scale = %d, want the callback's 30", audio.blipScale)
	}
}

// TestBlipVolumeDefaultsFull pins that with no callback the speaker plays at the
// full (unattenuated) blip scale.
func TestBlipVolumeDefaultsFull(t *testing.T) {
	room, sess, _, audio := newCourtroomRig(t)
	setupReadySession(t, sess)
	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "Hi")})
	if audio.blipScale != blipVolumeFull {
		t.Errorf("default blip scale = %d, want %d", audio.blipScale, blipVolumeFull)
	}
}

func TestShoutNukesQueueAndPlaysFirst(t *testing.T) {
	room, sess, _, audio := newCourtroomRig(t)
	setupReadySession(t, sess)

	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "First")})
	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "Queued")})
	if room.QueueLen() != 1 {
		t.Fatalf("queue = %d", room.QueueLen())
	}

	shout := pairedMS(t, sess, "OBJECTION!")
	shout.Objection = protocol.ShoutObjection
	room.HandleEvent(Event{Kind: EventMessage, Message: shout})

	if room.QueueLen() != 0 {
		t.Error("shout must nuke the queue")
	}
	if room.Phase() != PhaseShout {
		t.Fatalf("phase = %v, want shout", room.Phase())
	}
	if room.Scene.ShoutBase == "" {
		t.Error("shout bubble base missing")
	}
	// The default (misc/default) bubble must be set as the fallback so a
	// character without its own interjection art (most) still shows a bubble —
	// the render uses it when the char-specific base isn't resident.
	if !strings.HasSuffix(room.Scene.ShoutFallbackBase, "misc/default/objection_bubble") {
		t.Errorf("shout fallback base = %q, want .../misc/default/objection_bubble", room.Scene.ShoutFallbackBase)
	}
	if len(audio.shouts) != 1 || !strings.HasSuffix(audio.shouts[0], "characters/phoenix/objection") {
		t.Errorf("shout sfx = %v", audio.shouts)
	}

	room.Update(DefaultShoutDuration)
	if room.Phase() != PhaseTalking {
		t.Errorf("post-shout phase = %v, want talking", room.Phase())
	}
	if room.Scene.ShoutBase != "" {
		t.Error("shout bubble must clear after the shout phase")
	}
}

// TestCatchUpDrainsBacklog pins packed-room catch-up: with it on, a deep IC
// backlog fast-forwards to <= threshold within a handful of frames; with it
// off (the courtroom default) the same flood still crawls through each
// message's typewriter + stay, so the queue stays deep. Only the on-stage
// ceremony is skipped — the IC log records every message regardless.
func TestCatchUpDrainsBacklog(t *testing.T) {
	flood := func(room *Courtroom, sess *Session) {
		// The first message plays normally (queue empty at its begin); the
		// rest pile up behind it.
		room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "first")})
		for i := 0; i < 12; i++ {
			room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "x")})
		}
	}
	const frames = 60

	// Catch-up ON: the backlog drains to <= threshold well within the budget.
	on, sess1, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess1)
	on.CatchUp, on.CatchUpThreshold = true, 2
	flood(on, sess1)
	for i := 0; i < frames; i++ {
		on.Update(DefaultCharInterval)
	}
	if on.QueueLen() > on.CatchUpThreshold {
		t.Errorf("catch-up ON: queue = %d after %d frames, want <= %d", on.QueueLen(), frames, on.CatchUpThreshold)
	}

	// Catch-up OFF (courtroom default): each message plays its full typewriter
	// + stay, so the same flood is far from drained in the same budget.
	off, sess2, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess2)
	flood(off, sess2)
	for i := 0; i < frames; i++ {
		off.Update(DefaultCharInterval)
	}
	if off.QueueLen() <= on.CatchUpThreshold {
		t.Errorf("catch-up OFF: queue = %d after %d frames, expected the backlog still deep", off.QueueLen(), frames)
	}
}

// TestCatchUpDefaultEngagesAtOneBehind pins the default catch-up trigger
// (threshold 1, the floor): a message catches up the moment ONE message waits
// behind it, so only the newest line (nothing behind it) ever plays in full.
// This is the boundary the >= comparison sets — under the old > it engaged only
// at two-behind, leaving the textbox a message behind real-time.
func TestCatchUpDefaultEngagesAtOneBehind(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)
	room.CatchUp, room.CatchUpThreshold = true, 1

	// "a" plays at idle (nothing behind). While it's on stage "b" and "c" pile
	// up, so when "b" is dequeued one message ("c") still waits behind it — that
	// one-behind case must catch up.
	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "a")})
	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "b")})
	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "c")})

	bPhase := MessagePhase(-1) // capture "b"'s phase the first frame it's on stage
	for i := 0; i < 300 && bPhase < 0; i++ {
		room.Update(DefaultCharInterval)
		if room.Scene.MessageRaw == "b" {
			bPhase = room.Phase()
		}
	}
	if bPhase != PhaseLinger {
		t.Fatalf("one-behind message must catch up (PhaseLinger, typewriter skipped), got phase %v", bPhase)
	}
}

func TestPreanimBlocksUntilDone(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)

	fields := []string{
		"1", "intro", "Phoenix", "normal", "Watch this", "def", "0",
		"1", // EMOTE_MOD preanim
		"0", "0", "0", "0", "0", "0", "0",
		"", "-1", "", "", "0", "0", "0",
		"0", // IMMEDIATE off
	}
	msg, err := protocol.ParseMS(fields, sess.Features, 0)
	if err != nil {
		t.Fatal(err)
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if room.Phase() != PhasePreanim {
		t.Fatalf("phase = %v, want preanim", room.Phase())
	}
	if !strings.HasSuffix(room.Scene.Speaker.Active, "characters/phoenix/intro") {
		t.Errorf("active sprite = %q, want the preanim", room.Scene.Speaker.Active)
	}
	if !room.Scene.Speaker.PlayOnce {
		t.Error("preanim must be one-shot")
	}

	room.Update(10 * time.Millisecond) // not done yet
	if room.Phase() != PhasePreanim {
		t.Fatal("preanim ended early")
	}
	room.NotifyPreanimDone()
	room.Update(time.Millisecond)
	if room.Phase() != PhaseTalking {
		t.Errorf("phase after preanim = %v, want talking", room.Phase())
	}
	if !strings.HasSuffix(room.Scene.Speaker.Active, "(b)normal") {
		t.Errorf("active = %q, want talk loop", room.Scene.Speaker.Active)
	}
}

// TestMissingPreanimSkipsInsteadOfTimeout pins the streaming answer to
// AO2-Client's missing-preanim skip (courtroom.cpp play_preanim: file absent
// → done immediately): when the manager conclusively 404s the preanim, the
// App relays the §4 warning as NotifyAssetMissing and the ceremony moves on
// NOW — not at PreanimTimeout. The live trigger: packs whose char.ini fills
// the preanim field with a dummy name on every emote ("-<n>"), which froze
// every Pre-checked/objection message for the full 2.5 s with a blank
// speaker, cached or not.
func TestMissingPreanimSkipsInsteadOfTimeout(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)

	room.NotifyAssetMissing("http://mirror/base/characters/x/(a)y") // no message on stage: must be a no-op

	fields := []string{
		"1", "-80", "Erika", "80", "Guilty!", "jud", "0",
		"1", // EMOTE_MOD preanim (the Pre checkbox upgrades an ini 0 on send)
		"0", "0", "0", "0", "0", "0", "0",
		"", "-1", "", "", "0", "0", "0",
		"0", // IMMEDIATE off
	}
	msg, err := protocol.ParseMS(fields, sess.Features, 0)
	if err != nil {
		t.Fatal(err)
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if room.Phase() != PhasePreanim {
		t.Fatalf("phase = %v, want preanim", room.Phase())
	}

	pre := room.Scene.Speaker.PreanimBase
	room.NotifyAssetMissing("http://elsewhere/other") // a foreign miss must not end the wait
	room.Update(time.Millisecond)
	if room.Phase() != PhasePreanim {
		t.Fatal("a foreign missing asset ended the preanim wait")
	}

	room.NotifyAssetMissing(pre)
	room.Update(time.Millisecond)
	if room.Phase() != PhaseTalking {
		t.Errorf("phase after the miss = %v, want talking well before the %v timeout", room.Phase(), room.PreanimTimeout)
	}
	if room.Scene.Speaker.PlayOnce {
		t.Error("one-shot flag must clear when the preanim is skipped")
	}
	if !strings.HasSuffix(room.Scene.Speaker.Active, "(b)80") {
		t.Errorf("active = %q, want the talk loop", room.Scene.Speaker.Active)
	}
}

// preanimMsg builds a Pre-checked (EMOTE_MOD 1) IC message for the preanim
// timing tests.
func preanimMsg(t *testing.T, sess *Session, text string) *protocol.ChatMessage {
	t.Helper()
	fields := []string{
		"1", "-80", "Erika", "80", text, "jud", "0",
		"1", // EMOTE_MOD preanim
		"0", "0", "0", "0", "0", "0", "0",
		"", "-1", "", "", "0", "0", "0",
		"0", // IMMEDIATE off
	}
	msg, err := protocol.ParseMS(fields, sess.Features, 0)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// TestLongPreanimNotCutByTimeout pins the fix for "long preanims play a second
// or two then skip to the end": once the render reports a decoded preanim's real
// duration (NotifyPreanimStarted), the fallback PreanimTimeout is EXTENDED to
// cover it, so a preanim longer than the 2.5 s default plays to its natural
// NotifyPreanimDone instead of being cut at the timeout. A report outside
// PhasePreanim (a stale callback from a shared viewport) is a safe no-op.
func TestLongPreanimNotCutByTimeout(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)

	room.NotifyPreanimStarted(9 * time.Second) // no preanim on stage yet: phase-guarded no-op

	room.HandleEvent(Event{Kind: EventMessage, Message: preanimMsg(t, sess, "Long one")})
	if room.Phase() != PhasePreanim {
		t.Fatalf("phase = %v, want preanim", room.Phase())
	}

	room.NotifyPreanimStarted(5 * time.Second) // the render is playing a 5 s decoded preanim
	room.Update(DefaultPreanimTimeout + 500*time.Millisecond)
	if room.Phase() != PhasePreanim {
		t.Fatalf("preanim cut at the %v default timeout despite a 5 s reported duration (phase = %v)", DefaultPreanimTimeout, room.Phase())
	}

	room.NotifyPreanimDone()
	room.Update(time.Millisecond)
	if room.Phase() != PhaseTalking {
		t.Errorf("phase after the natural finish = %v, want talking", room.Phase())
	}
}

// TestPreanimTimeoutFallback pins that WITHOUT a duration report (the asset is
// still decoding, so the render can't say how long it is) the fallback timeout
// still ends the wait — the extension is opt-in, it doesn't disable the guard.
func TestPreanimTimeoutFallback(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)

	room.HandleEvent(Event{Kind: EventMessage, Message: preanimMsg(t, sess, "Still decoding")})
	if room.Phase() != PhasePreanim {
		t.Fatalf("phase = %v, want preanim", room.Phase())
	}

	room.Update(DefaultPreanimTimeout + time.Millisecond) // no report, no done: the fallback fires
	if room.Phase() != PhaseTalking {
		t.Errorf("phase after the fallback timeout = %v, want talking", room.Phase())
	}
}

// immediatePreanimMsg is an "immediate" IC message with a preanim: the preanim
// plays OVER the text crawl instead of blocking before it.
func immediatePreanimMsg(text string) *protocol.ChatMessage {
	return &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", PreEmote: "intro", Side: "wit",
		Message: text, Immediate: true,
	}
}

// TestImmediatePreanimTransitions pins immediate-mode preanim handling. The
// preanim plays over the text crawl (Active parked on the preanim). When it
// finishes WHILE text still crawls it must swap to the talk sprite (it used to
// freeze on the last preanim frame); when the TEXT finishes first it must hold
// the preanim to its end instead of snapping straight to idle.
func TestImmediatePreanimTransitions(t *testing.T) {
	// Preanim finishes mid-text → talk sprite.
	room, _, _, _ := newCourtroomRig(t)
	room.HandleEvent(Event{Kind: EventMessage, Message: immediatePreanimMsg(strings.Repeat("long enough that the text keeps crawling ", 5))})
	if room.Phase() != PhaseTalking {
		t.Fatalf("immediate message phase = %v, want talking (preanim plays over the text)", room.Phase())
	}
	if room.Scene.Speaker.Active != room.Scene.Speaker.PreanimBase || !room.Scene.Speaker.PlayOnce {
		t.Fatalf("immediate speaker not on the preanim: active=%q playOnce=%v", room.Scene.Speaker.Active, room.Scene.Speaker.PlayOnce)
	}
	room.Update(10 * time.Millisecond) // still crawling
	room.NotifyPreanimDone()
	room.Update(time.Millisecond)
	if room.Scene.Speaker.PlayOnce || room.Scene.Speaker.Active != room.Scene.Speaker.TalkBase {
		t.Errorf("preanim finishing mid-text must swap to the talk sprite: active=%q playOnce=%v", room.Scene.Speaker.Active, room.Scene.Speaker.PlayOnce)
	}
	if room.Phase() != PhaseTalking {
		t.Errorf("text still crawling: phase = %v, want talking", room.Phase())
	}

	// Text finishes first → hold the preanim, don't snap to idle.
	room2, _, _, _ := newCourtroomRig(t)
	room2.HandleEvent(Event{Kind: EventMessage, Message: immediatePreanimMsg("hi")})
	room2.Typewriter.SkipToEnd()
	room2.Update(time.Millisecond)
	if room2.Phase() != PhaseTalking || room2.Scene.Speaker.Active != room2.Scene.Speaker.PreanimBase {
		t.Fatalf("text done but preanim playing must hold: phase=%v active=%q (want talking + preanim)", room2.Phase(), room2.Scene.Speaker.Active)
	}
	room2.NotifyPreanimDone()
	room2.Update(time.Millisecond)
	if room2.Phase() == PhaseTalking {
		t.Error("preanim done + text done must leave PhaseTalking")
	}
	if room2.Scene.Speaker.Active != room2.Scene.Speaker.IdleBase {
		t.Errorf("after both done, active=%q, want the idle sprite", room2.Scene.Speaker.Active)
	}
}

// TestImmediatePreanimBounded pins the safety bound: an immediate preanim that
// never reports done (still decoding / missing) must NOT freeze the message —
// the post-text wait is bounded by PreanimTimeout — while a decoded long preanim
// that DOES report its duration (NotifyPreanimStarted) still plays in full.
func TestImmediatePreanimBounded(t *testing.T) {
	// Never reports done: bounded, no hang.
	room, _, _, _ := newCourtroomRig(t)
	room.HandleEvent(Event{Kind: EventMessage, Message: immediatePreanimMsg("hi")})
	room.Typewriter.SkipToEnd()
	room.Update(time.Millisecond)
	if room.Phase() != PhaseTalking {
		t.Fatalf("precondition: waiting for the preanim, phase = %v", room.Phase())
	}
	room.Update(DefaultPreanimTimeout + 100*time.Millisecond) // no done report ever
	if room.Phase() == PhaseTalking {
		t.Error("an immediate preanim that never reports done must be bounded, not hang the message")
	}

	// A long DECODED preanim (reports its duration) plays past the plain timeout.
	room2, _, _, _ := newCourtroomRig(t)
	room2.HandleEvent(Event{Kind: EventMessage, Message: immediatePreanimMsg("hi")})
	room2.NotifyPreanimStarted(5 * time.Second) // render reports a 5 s decoded preanim
	room2.Typewriter.SkipToEnd()
	room2.Update(DefaultPreanimTimeout + 500*time.Millisecond) // past the plain bound
	if room2.Phase() != PhaseTalking {
		t.Errorf("a 5 s decoded immediate preanim was cut at the plain timeout (phase = %v)", room2.Phase())
	}
	room2.NotifyPreanimDone()
	room2.Update(time.Millisecond)
	if room2.Phase() == PhaseTalking {
		t.Error("the reported preanim finishing must leave PhaseTalking")
	}
}

// TestMissingImmediatePreanimRestoresTalkLoop covers the non-blocking flavour:
// IMMEDIATE plays the preanim ALONGSIDE the text by parking Active on the
// preanim base with PlayOnce. A conclusively-missing preanim would leave the
// speaker invisible for the whole message (no one-shot ever completes, so
// OnPreanimDone never fires) — the miss must restore the talk loop instead.
func TestMissingImmediatePreanimRestoresTalkLoop(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)

	fields := []string{
		"1", "-80", "Erika", "80", "Guilty!", "jud", "0",
		"0", // EMOTE_MOD idle — immediate alone triggers the side-play
		"0", "0", "0", "0", "0", "0", "0",
		"", "-1", "", "", "0", "0", "0",
		"1", // IMMEDIATE on
	}
	msg, err := protocol.ParseMS(fields, sess.Features, 0)
	if err != nil {
		t.Fatal(err)
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if room.Phase() != PhaseTalking {
		t.Fatalf("phase = %v, want talking (immediate never blocks)", room.Phase())
	}
	if !room.Scene.Speaker.PlayOnce || room.Scene.Speaker.Active != room.Scene.Speaker.PreanimBase {
		t.Fatalf("immediate must park the one-shot preanim as active (got active=%q, once=%v)",
			room.Scene.Speaker.Active, room.Scene.Speaker.PlayOnce)
	}

	room.NotifyAssetMissing(room.Scene.Speaker.PreanimBase)
	if room.Scene.Speaker.PlayOnce {
		t.Error("one-shot flag must clear when the immediate preanim is missing")
	}
	if room.Scene.Speaker.Active != room.Scene.Speaker.TalkBase {
		t.Errorf("active = %q, want the talk loop %q", room.Scene.Speaker.Active, room.Scene.Speaker.TalkBase)
	}
}

// TestPreanimSettlesOnCollidedBase pins the courtroom half of the preanim-loop
// fix: even when a character's preanim FILE resolves to the SAME base string as
// its talk/idle sprite (PreEmote "(b)normal" glues to the talk base "(b)normal" —
// reachable per frametriggers.go:167-172), the phase machine must still drive the
// message all the way through: preanim → talk → linger → idle, clearing PlayOnce
// at each terminator. The render's told-to-loop restart guard (viewport.go) is
// what keeps that collided base from LOOPING as if it were a live preanim; this
// test locks in that the state machine itself settles regardless of the collision.
func TestPreanimSettlesOnCollidedBase(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)

	// PreEmote "(b)normal" == talk emote "(b)"+"normal" → PreanimBase collides with
	// TalkBase. A short text so the crawl finishes quickly.
	msg := &protocol.ChatMessage{
		CharName: "Phoenix",
		Emote:    "normal",
		PreEmote: "(b)normal",
		Side:     "wit",
		Message:  "hi",
		EmoteMod: protocol.EmoteModPreanim, // blocking preanim
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})

	// Precondition: the collision actually holds, and we're blocking on the preanim.
	if room.Scene.Speaker.PreanimBase != room.Scene.Speaker.TalkBase {
		t.Fatalf("test setup: preanim base %q must collide with talk base %q",
			room.Scene.Speaker.PreanimBase, room.Scene.Speaker.TalkBase)
	}
	if room.Phase() != PhasePreanim || !room.Scene.Speaker.PlayOnce {
		t.Fatalf("phase = %v playOnce = %v, want blocking preanim", room.Phase(), room.Scene.Speaker.PlayOnce)
	}

	// The preanim completes → the machine must leave PhasePreanim, clear PlayOnce,
	// and land the (collided) talk base — never freeze in PhasePreanim.
	room.NotifyPreanimDone()
	room.Update(time.Millisecond)
	if room.Phase() != PhaseTalking {
		t.Fatalf("phase after preanim done = %v, want talking (must settle despite the collision)", room.Phase())
	}
	if room.Scene.Speaker.PlayOnce {
		t.Error("PlayOnce must clear when the preanim ends, even on a collided base")
	}
	if room.Scene.Speaker.Active != room.Scene.Speaker.TalkBase {
		t.Errorf("active = %q, want the talk base %q", room.Scene.Speaker.Active, room.Scene.Speaker.TalkBase)
	}

	// The text finishes → linger → idle, still with PlayOnce cleared and the idle
	// base active. Advance past the text crawl and the text-stay linger.
	room.Typewriter.SkipToEnd()
	room.Update(time.Millisecond) // text done → enterLinger
	if room.Phase() != PhaseLinger {
		t.Fatalf("phase after text done = %v, want linger", room.Phase())
	}
	if room.Scene.Speaker.Active != room.Scene.Speaker.IdleBase || room.Scene.Speaker.PlayOnce {
		t.Errorf("linger active = %q playOnce = %v, want idle base + PlayOnce false",
			room.Scene.Speaker.Active, room.Scene.Speaker.PlayOnce)
	}
	room.Update(DefaultTextStayTime + time.Millisecond) // linger elapses → idle
	if room.Phase() != PhaseIdle {
		t.Errorf("phase after linger = %v, want idle (machine fully settled)", room.Phase())
	}
	if room.Scene.Speaker.PlayOnce {
		t.Error("PlayOnce must remain cleared at idle")
	}
}

// TestMissingPreanimLearnedDuringShoutSkipsPhase: begin() prefetches the
// preanim while the interjection bubble still holds the stage, so the miss
// can land mid-shout. enterAfterShout must then skip the preanim hijack
// entirely — straight to the talk loop when the bubble ends, zero blank
// frames on a preanim that can never resolve.
func TestMissingPreanimLearnedDuringShoutSkipsPhase(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)

	fields := []string{
		"1", "-80", "Erika", "80", "Objection!", "jud", "0",
		"2", // legacy EMOTE_MOD 2 (objection+preanim) — normalizes to preanim
		"0", "0",
		"2", // OBJECTION_MOD: objection bubble
		"0", "0", "0", "0",
		"", "-1", "", "", "0", "0", "0",
		"0",
	}
	msg, err := protocol.ParseMS(fields, sess.Features, 0)
	if err != nil {
		t.Fatal(err)
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if room.Phase() != PhaseShout {
		t.Fatalf("phase = %v, want shout", room.Phase())
	}

	room.NotifyAssetMissing(room.Scene.Speaker.PreanimBase) // the 404 lands mid-bubble
	room.Update(room.ShoutDuration + time.Millisecond)      // bubble ends
	if room.Phase() != PhaseTalking {
		t.Errorf("phase after shout = %v, want talking (PhasePreanim skipped)", room.Phase())
	}
	if room.Scene.Speaker.PlayOnce {
		t.Error("skipped preanim must not leave the one-shot flag set")
	}
	if !strings.HasSuffix(room.Scene.Speaker.Active, "(b)80") {
		t.Errorf("active = %q, want the talk loop, never the missing preanim", room.Scene.Speaker.Active)
	}
}

func TestUnpairedMessageCentersSingleSprite(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)

	fields := []string{
		"1", "-", "Maya", "normal", "Hi", "wit", "0", "0", "1", "0",
		"0", "0", "0", "0", "0",
		"", "-1", "", "", "0", "0", "0", "0",
	}
	msg, err := protocol.ParseMS(fields, sess.Features, 0)
	if err != nil {
		t.Fatal(err)
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if room.Scene.PairActive {
		t.Error("unpaired message renders a pair")
	}
	if !room.Scene.SpeakerInFront {
		t.Error("unpaired default z-order broken")
	}
	if room.Scene.Speaker.OffsetX != 0 || room.Scene.Speaker.OffsetY != 0 {
		t.Error("unpaired sprite must sit at origin offset")
	}
}

// --- typewriter -------------------------------------------------------------------

func TestTypewriterRevealAndBlips(t *testing.T) {
	tw := NewTypewriter()
	tw.Start("abcd")
	if tw.Done() {
		t.Fatal("done before start")
	}
	revealed, blips := tw.Update(4 * DefaultCharInterval)
	if revealed != 4 || !tw.Done() {
		t.Errorf("revealed = %d done=%v", revealed, tw.Done())
	}
	if blips != 2 { // every 2nd char
		t.Errorf("blips = %d, want 2", blips)
	}
}

func TestTypewriterSpeedCodes(t *testing.T) {
	tw := NewTypewriter()
	tw.Start("ab}}}cd{e") // }}} → fastest (0×), then { back one step
	if got := tw.Text(); got != "abcde" {
		t.Fatalf("Text = %q (speed codes must be stripped)", got)
	}
	// First two at 1.0×: need 2 intervals. c,d at 0×: instant. e at 0.25×.
	_, _ = tw.Update(2 * DefaultCharInterval)
	if tw.Visible() < 4 {
		t.Errorf("visible = %d, want ≥4 (instant chars after }}})", tw.Visible())
	}
	_, _ = tw.Update(DefaultCharInterval)
	if !tw.Done() {
		t.Error("not done after slack time")
	}
}

func TestTypewriterInlineColors(t *testing.T) {
	tw := NewTypewriter()

	// No markup → one default run covering the whole message.
	tw.Start("hello world")
	if got := tw.Text(); got != "hello world" {
		t.Fatalf("plain Text = %q", got)
	}
	if s := tw.Styles(); len(s) != 1 || s[0] != (StyleRun{Len: 11, Color: ColorDefault}) {
		t.Fatalf("plain styles = %v, want one default run of 11", s)
	}

	// Palette runs partition the clean text; markup is stripped.
	tw.Start("hi \\c2red\\c0 white")
	if got := tw.Text(); got != "hi red white" {
		t.Fatalf("colored Text = %q (markup must strip)", got)
	}
	want := []StyleRun{{Len: 3, Color: ColorDefault}, {Len: 3, Color: 2}, {Len: 6, Color: 0}}
	if got := tw.Styles(); len(got) != len(want) {
		t.Fatalf("colored styles = %v, want %v", got, want)
	} else {
		total := 0
		for i, r := range got {
			if r != want[i] {
				t.Errorf("style[%d] = %v, want %v", i, r, want[i])
			}
			total += r.Len
		}
		if total != len([]rune(tw.Text())) {
			t.Errorf("style lengths sum to %d, want %d (must cover every rune)", total, len([]rune(tw.Text())))
		}
	}

	// Rainbow tag, no spurious empty default run when color is set before any text.
	tw.Start("\\crwow")
	if got := tw.Styles(); len(got) != 1 || got[0] != (StyleRun{Len: 3, Color: ColorRainbow}) {
		t.Fatalf("rainbow styles = %v, want one rainbow run of 3", got)
	}

	// Bold/italic toggles split runs and tag them; the markup strips out.
	tw.Start("a\\bbold\\b\\ix\\i")
	if got := tw.Text(); got != "aboldx" {
		t.Fatalf("bold/italic Text = %q, want \"aboldx\"", got)
	}
	bi := []StyleRun{
		{Len: 1, Color: ColorDefault},
		{Len: 4, Color: ColorDefault, Bold: true},
		{Len: 1, Color: ColorDefault, Italic: true},
	}
	if got := tw.Styles(); len(got) != len(bi) {
		t.Fatalf("bold/italic styles = %v, want %v", got, bi)
	} else {
		for i, r := range got {
			if r != bi[i] {
				t.Errorf("bi style[%d] = %v, want %v", i, r, bi[i])
			}
		}
	}

	// Extended AsyncAO color (#98): \c<letter> tags a run with ColorExtBase+letter
	// (render resolves the RGB by letter); the markup strips like any other code.
	tw.Start("\\cphi")
	if got := tw.Text(); got != "hi" {
		t.Fatalf("ext-color Text = %q, want \"hi\"", got)
	}
	if got := tw.Styles(); len(got) != 1 || got[0] != (StyleRun{Len: 2, Color: ColorExtBase + int('p')}) {
		t.Fatalf("ext-color styles = %v, want one run of 2 @ ColorExtBase+'p'", got)
	}
	// An undefined \c<letter> (in a-z but not a code) is kept literal, not eaten.
	tw.Start("a\\czb")
	if got := tw.Text(); got != "a\\czb" {
		t.Errorf(`undefined ext code Text = %q, want literal "a\czb"`, got)
	}

	// Escaped backslash collapses to one literal '\'; a lone/unknown escape is kept.
	tw.Start("a\\\\b")
	if got := tw.Text(); got != "a\\b" {
		t.Errorf(`escaped backslash Text = %q, want "a\b"`, got)
	}
	tw.Start("a\\zb")
	if got := tw.Text(); got != "a\\zb" {
		t.Errorf(`unknown escape Text = %q, want "a\zb" (backslash kept)`, got)
	}
}

// TestAO2InlineColors pins the AO2 inline markup (§3.8): toggle delimiters
// (“ ` ~ | º № √ “) colour+consume a span, bracket pairs (`( )`, `[ ]`) colour
// but KEEP the brackets visible, `\`+delimiter escapes to a literal, an
// unterminated span runs to end, nesting follows AO2's stack, and AO2 markup
// composes with the `\c` scheme (an AO2 span nests over the base colour and
// returns to it). Character table + semantics verified against AO2-Client
// filter_ic_text (courtroom.cpp:3532) + the stock default chat_config.ini.
func TestAO2InlineColors(t *testing.T) {
	tw := NewTypewriter()

	// Toggle colour: the delimiter is consumed, the span coloured (c1 green = 1).
	tw.Start("`green`")
	if got := tw.Text(); got != "green" {
		t.Fatalf("toggle Text = %q, want \"green\" (backticks consumed)", got)
	}
	if got := tw.Styles(); len(got) != 1 || got[0] != (StyleRun{Len: 5, Color: 1}) {
		t.Fatalf("toggle styles = %v, want one run of 5 @ colour 1", got)
	}

	// Red (~) and orange (|) toggles, each colouring a distinct middle span.
	tw.Start("a~red~b|or|c")
	if got := tw.Text(); got != "aredborc" {
		t.Fatalf("multi-toggle Text = %q, want \"aredborc\"", got)
	}
	wantMT := []StyleRun{
		{Len: 1, Color: ColorDefault}, {Len: 3, Color: 2}, {Len: 1, Color: ColorDefault},
		{Len: 2, Color: 3}, {Len: 1, Color: ColorDefault},
	}
	if got := tw.Styles(); !equalStyleRuns(got, wantMT) {
		t.Fatalf("multi-toggle styles = %v, want %v", got, wantMT)
	}

	// Bracket pair (c4 blue = 4): the brackets STAY visible and are coloured too.
	tw.Start("x(blue)y")
	if got := tw.Text(); got != "x(blue)y" {
		t.Fatalf("pair Text = %q, want \"x(blue)y\" (brackets kept)", got)
	}
	wantPair := []StyleRun{
		{Len: 1, Color: ColorDefault}, {Len: 6, Color: 4}, {Len: 1, Color: ColorDefault},
	}
	if got := tw.Styles(); !equalStyleRuns(got, wantPair) {
		t.Fatalf("pair styles = %v, want %v (brackets in the span)", got, wantPair)
	}

	// Escape: `\`+delimiter yields the literal delimiter, uncoloured.
	tw.Start("a\\`b\\~c")
	if got := tw.Text(); got != "a`b~c" {
		t.Fatalf("escape Text = %q, want \"a`b~c\" (delimiters literal)", got)
	}
	if got := tw.Styles(); len(got) != 1 || got[0].Color != ColorDefault {
		t.Fatalf("escaped delimiters must stay default colour, styles = %v", got)
	}

	// Unterminated toggle: colours to end of message (AO2 appends the closing font).
	tw.Start("hi ~unclosed")
	if got := tw.Text(); got != "hi unclosed" {
		t.Fatalf("unterminated Text = %q, want \"hi unclosed\"", got)
	}
	wantUnc := []StyleRun{{Len: 3, Color: ColorDefault}, {Len: 8, Color: 2}}
	if got := tw.Styles(); !equalStyleRuns(got, wantUnc) {
		t.Fatalf("unterminated styles = %v, want %v (runs to end)", got, wantUnc)
	}

	// Nesting: an inner toggle nests over an outer one and pops back to it.
	tw.Start("`a~b~c`")
	if got := tw.Text(); got != "abc" {
		t.Fatalf("nested Text = %q, want \"abc\"", got)
	}
	wantNest := []StyleRun{{Len: 1, Color: 1}, {Len: 1, Color: 2}, {Len: 1, Color: 1}}
	if got := tw.Styles(); !equalStyleRuns(got, wantNest) {
		t.Fatalf("nested styles = %v, want %v (green→red→green)", got, wantNest)
	}

	// A stray closing bracket with no open is an ordinary character.
	tw.Start("plain) text")
	if got := tw.Text(); got != "plain) text" {
		t.Fatalf("stray close Text = %q, want it kept literally", got)
	}
	if got := tw.Styles(); len(got) != 1 || got[0].Color != ColorDefault {
		t.Fatalf("stray close must not colour, styles = %v", got)
	}

	// Composition with the `\c` scheme: an AO2 span nests over the \c base colour
	// and returns to it when it closes.
	tw.Start("\\c2red`green`red")
	if got := tw.Text(); got != "redgreenred" {
		t.Fatalf("compose Text = %q, want \"redgreenred\"", got)
	}
	wantComp := []StyleRun{{Len: 3, Color: 2}, {Len: 5, Color: 1}, {Len: 3, Color: 2}}
	if got := tw.Styles(); !equalStyleRuns(got, wantComp) {
		t.Fatalf("compose styles = %v, want %v (red base, green span, back to red)", got, wantComp)
	}

	// Hostile deep nesting must NOT panic (the stack over-fills past ao2StackMax):
	// alternating distinct toggles each open a new colour. A peer can send this, so
	// Start/StripChatMarkup must survive it (bounds are clamped, not indexed raw).
	deep := strings.Repeat("`~", 300) + "x"
	tw.Start(deep) // would panic on an unguarded stack read
	if got := StripChatMarkup(deep); got != tw.Text() {
		t.Fatalf("deep-nest strip/typewriter diverged: %q vs %q", got, tw.Text())
	}

	// Leading "~~" AO2 alignment prefix (centre, courtroom.cpp:3543): we don't
	// implement alignment, so it parses as an empty red toggle — no spurious run,
	// text intact. Pinned so the benign result is documented.
	tw.Start("~~hello")
	if got := tw.Text(); got != "hello" {
		t.Fatalf("leading ~~ Text = %q, want \"hello\" (empty toggle, no crash)", got)
	}
	if got := tw.Styles(); len(got) != 1 || got[0] != (StyleRun{Len: 5, Color: ColorDefault}) {
		t.Fatalf("leading ~~ styles = %v, want one default run of 5 (no spurious red span)", got)
	}
}

// TestAO2ParseNoExtraAlloc pins that the AO2 markup state machine adds NO
// allocation over the baseline parse: a plain message and an equal-length
// AO2-marked-up message must have the same allocs/op on a reused typewriter (both
// dominated by the []rune conversion Start already did pre-§3.8). The fixed-size
// ao2ColorStack must stay on the stack — no per-message heap object (hard rule
// §4 / the zero-alloc render path; the message-raster feeds off Start).
func TestAO2ParseNoExtraAlloc(t *testing.T) {
	tw := NewTypewriter()
	plain := "the quick brown fox jumps over it"
	marked := "the `quick` ~brown~ (fox) jumps over it"
	tw.Start(plain) // warm the reused output slices so re-growth doesn't skew the count
	tw.Start(marked)

	plainAllocs := testing.AllocsPerRun(200, func() { tw.Start(plain) })
	markedAllocs := testing.AllocsPerRun(200, func() { tw.Start(marked) })
	if markedAllocs > plainAllocs {
		t.Errorf("AO2 markup added %.0f alloc(s)/op over plain (%.0f vs %.0f) — the stack escaped or a code path allocates",
			markedAllocs-plainAllocs, markedAllocs, plainAllocs)
	}
}

// equalStyleRuns compares two StyleRun slices element-wise (test helper).
func equalStyleRuns(a, b []StyleRun) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestStripMatchesTypewriter pins StripChatMarkup to produce exactly the clean
// text the typewriter reveals, so the IC log and the chatbox can never drift.
func TestStripMatchesTypewriter(t *testing.T) {
	tw := NewTypewriter()
	cases := []string{
		"plain message",
		"hi \\c2red\\c0 white",
		"{slow} and }fast{ codes",
		"\\crrainbow tail",
		"a\\\\b literal slash",
		"a\\zb unknown escape",
		"mix {x}\\c4blue\\\\done",
		"\\bbold\\b and \\iitalic\\i text",
		"\\b\\c2 bold red \\inested\\i\\b plain",
		"\\cppurple\\c0 then white and \\cz kept",
		"\\c#ff8800exact hex\\c0 back",
		"\\c#ABCDEFupper hex too",
		"\\c#12345 too short stays literal",
		"\\c#zzzzzz not hex stays literal",
		"tail cut \\c#ff",
		"shake \\s and \\f flash codes",
		"a line\\nbreak here",
		"pause \\p500 then \\p bare",
		"trailing effect code \\s",
		// AO2 inline markup (§3.8): toggles consumed, brackets kept, escape,
		// nesting, unterminated span, stray close, and \c composition.
		"`green` and ~red~ and |orange|",
		"kept (blue) and [gray] brackets",
		"escaped \\` and \\~ and \\( stay literal",
		"nested `a~b~c` markup",
		"unterminated ~span to end",
		"stray ) and ] closes stay",
		"º yellow º and № magenta № and √ cyan √",
		"compose \\c2red`green`red tail",
		"mix {slow}`\\c4both`\\\\end",
		"",
	}
	for _, m := range cases {
		tw.Start(m)
		if got, want := StripChatMarkup(m), tw.Text(); got != want {
			t.Errorf("StripChatMarkup(%q) = %q, typewriter = %q", m, got, want)
		}
	}
}

// TestHexColorRun pins the free-hex inline code (v1.52.0): `\c#RRGGBB` tags a
// run with ColorHexBase + the packed RGB (case-insensitive digits), a malformed
// code stays literal (the standing `\X` rule), and the code letters after a
// consumed hex run stay plain text.
func TestHexColorRun(t *testing.T) {
	tw := NewTypewriter()
	tw.Start("\\c#ff8800hi")
	if got := tw.Styles(); len(got) != 1 || got[0] != (StyleRun{Len: 2, Color: ColorHexBase + 0xff8800}) {
		t.Fatalf("hex styles = %v, want one run of 2 @ ColorHexBase+0xff8800", got)
	}
	if tw.Text() != "hi" {
		t.Fatalf("hex code must be consumed, text = %q", tw.Text())
	}
	tw.Start("\\c#AbCdEfhi")
	if got := tw.Styles(); len(got) != 1 || got[0].Color != ColorHexBase+0xabcdef {
		t.Fatalf("mixed-case hex = %v, want ColorHexBase+0xabcdef", got)
	}
	tw.Start("\\c#12g456hi") // 'g' is not hex → the whole thing stays literal
	if tw.Text() != "\\c#12g456hi" {
		t.Fatalf("malformed hex must stay literal, text = %q", tw.Text())
	}
}

// TestExtColorCodesStripped directly pins that every extended-colour code (#98)
// is CONSUMED from the visible text — the literal "\c<letter>" never shows in the
// chatbox or IC log on a current build. (A playtester seeing a literal "\cg" is
// on a stale pre-#98 exe, which doesn't know the code and emits it verbatim.)
func TestExtColorCodesStripped(t *testing.T) {
	for _, code := range ExtColorCodes {
		in := "Objection\\c" + string(code) + "!"
		if got := StripChatMarkup(in); got != "Objection!" {
			t.Errorf("StripChatMarkup(%q) = %q, want %q — ext code \\c%c not stripped", in, got, "Objection!", code)
		}
	}
}

func TestTypewriterSkipToEnd(t *testing.T) {
	tw := NewTypewriter()
	tw.Start("a long message that should skip")
	tw.SkipToEnd()
	if !tw.Done() {
		t.Error("SkipToEnd did not finish the reveal")
	}
}

// TestTypewriterInlineEffectCodes pins the AO2 inline screen-effect codes: \s/\f
// record a mark at their reveal position and leave no glyph, \p (bare = 1 s;
// \p<n> = n ms) folds into the next rune's interval as a pause, \n becomes a real
// newline rune, and a skip drops any unreached marks (no catch-up burst).
func TestTypewriterInlineEffectCodes(t *testing.T) {
	tw := NewTypewriter()

	// \s (after "ab", At=2) and \f (after "abcd", At=4) leave no glyph; the marks
	// fire in order, each only once its position is revealed.
	tw.Start("ab\\scd\\f")
	if got := tw.Text(); got != "abcd" {
		t.Fatalf("effect-code Text = %q, want \"abcd\"", got)
	}
	if _, ok := tw.NextEffect(); ok {
		t.Fatal("no effect should be due before any reveal")
	}
	tw.Update(2 * DefaultCharInterval) // reveal "ab" → the shake (At=2) is due
	if m, ok := tw.NextEffect(); !ok || m.Kind != EffectShake || m.At != 2 {
		t.Fatalf("first mark = %+v ok=%v, want {At:2 Shake}", m, ok)
	}
	if _, ok := tw.NextEffect(); ok {
		t.Fatal("flash must wait until its position is revealed")
	}
	tw.Update(10 * DefaultCharInterval) // reveal the rest → flash (At=4) is due
	if m, ok := tw.NextEffect(); !ok || m.Kind != EffectFlash || m.At != 4 {
		t.Fatalf("second mark = %+v ok=%v, want {At:4 Flash}", m, ok)
	}
	if _, ok := tw.NextEffect(); ok {
		t.Fatal("only two marks expected")
	}

	// \n is a real newline rune in the clean text (wrapText breaks on it).
	tw.Start("a\\nb")
	if got := tw.Text(); got != "a\nb" {
		t.Fatalf("newline Text = %q, want \"a\\nb\"", got)
	}

	// Bare \p folds a one-second pause onto the next rune's interval.
	tw.Start("a\\pb")
	if got := tw.Text(); got != "ab" {
		t.Fatalf("pause Text = %q, want \"ab\"", got)
	}
	tw.Update(3 * DefaultCharInterval) // reveals 'a' but not past the 1 s pause on 'b'
	if tw.Visible() != 1 {
		t.Errorf("bare \\p visible = %d, want 1 ('b' behind the pause)", tw.Visible())
	}
	tw.Update(pauseDefaultMs * time.Millisecond)
	if !tw.Done() {
		t.Error("not done after the pause elapses")
	}

	// \p<n> consumes its digits (no stray "250" shows).
	tw.Start("x\\p250y")
	if got := tw.Text(); got != "xy" {
		t.Fatalf("\\p<n> Text = %q, want \"xy\" (digits consumed)", got)
	}

	// A skip drops every unreached mark — catch-up must not burst the shakes.
	tw.Start("\\sab\\scd\\f")
	tw.SkipToEnd()
	if _, ok := tw.NextEffect(); ok {
		t.Error("SkipToEnd must drop all pending effect marks")
	}
}

func TestTypewriterSpacesDontBlipByDefault(t *testing.T) {
	tw := NewTypewriter()
	tw.Start("a b c d") // 4 letters, 3 spaces
	_, blips := tw.Update(10 * DefaultCharInterval)
	if blips != 2 { // letters only: 4 letters / rate 2
		t.Errorf("blips = %d, want 2 (spaces silent)", blips)
	}
}

// TestSessionPing pins the CH keepalive (AO2-Client keepalive_timer,
// 45 s): servers idle-kick silent clients — minimized sessions died
// before the app pinged on its own.
func TestSessionPing(t *testing.T) {
	rec := &sentRecorder{}
	s := NewSession(rec.send, "h")
	feed(t, s, "PV#1#CID#7#%")
	s.Ping()
	last := rec.packets[len(rec.packets)-1]
	if last.Header != "CH" || last.Field(0) != "7" {
		t.Errorf("ping sent %s#%s, want CH#7", last.Header, last.Field(0))
	}
}

// TestTextStayConfigurable pins the user-tunable linger duration.
func TestTextStayConfigurable(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	if room.TextStay != DefaultTextStayTime {
		t.Fatalf("default stay = %v", room.TextStay)
	}
	room.TextStay = 123 * time.Millisecond
	room.enterLinger()
	if room.timer != 123*time.Millisecond {
		t.Fatalf("linger timer = %v, want the configured 123ms", room.timer)
	}
}

// TestSFXDelayDeadline pins #12: a message's emote SFX fires at SFX_DELAY × 40ms
// (AO2 sfx_delay_timer), not at message start, so a whip-crack lands mid-preanim.
func TestSFXDelayDeadline(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	// Idle-mod message (no preanim block) so it enters PhaseTalking immediately;
	// the SFX deadline runs independent of the phase.
	msg := &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Message: "Take that!", Side: "wit",
		EmoteMod: protocol.EmoteModIdle,
		SFXName:  "whack", SFXDelay: 3, // 3 × 40ms = 120ms
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if !room.sfxArmed {
		t.Fatal("SFX must be armed after begin")
	}
	if want := 3 * sfxDelayUnit; room.sfxLeft != want {
		t.Fatalf("armed sfxLeft = %v, want %v (SFXDelay × 40ms)", room.sfxLeft, want)
	}
	// Before the deadline: nothing played.
	room.Update(100 * time.Millisecond)
	if len(audio.sfx) != 0 {
		t.Fatalf("SFX played early at 100ms (deadline 120ms): %v", audio.sfx)
	}
	// Cross the deadline: it fires exactly once, resolved to the SFX URL.
	room.Update(30 * time.Millisecond) // total 130ms > 120ms
	if len(audio.sfx) != 1 {
		t.Fatalf("SFX plays = %d, want 1 after the deadline", len(audio.sfx))
	}
	if !strings.Contains(strings.ToLower(audio.sfx[0]), "whack") {
		t.Errorf("played SFX %q, want the whack URL", audio.sfx[0])
	}
	if room.sfxArmed {
		t.Error("SFX must disarm after firing (no repeat)")
	}
	// A further tick does not re-fire.
	room.Update(200 * time.Millisecond)
	if len(audio.sfx) != 1 {
		t.Errorf("SFX re-fired: %v", audio.sfx)
	}
}

// TestPreanimScreenshakeAtDeadline pins #12: AO2's play_sfx fires the preanim
// screenshake at the SFX delay moment (courtroom.cpp:4593-4596), and it fires
// even when the message has no audible SFX ("0"/"1"/empty) — the shake is gated
// only on SCREENSHAKE=1 + a preanim emote mod.
func TestPreanimScreenshakeAtDeadline(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	room.ReduceMotion, room.ScreenEffects = false, true // visuals enabled
	msg := &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", PreEmote: "point", Message: "!!!", Side: "wit",
		EmoteMod:    protocol.EmoteModPreanim,
		Screenshake: true,
		SFXName:     "1", // no audible SFX — shake must still fire
		SFXDelay:    2,   // 2 × 40ms = 80ms
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if !room.sfxArmed || !room.sfxShake {
		t.Fatalf("preanim shake must be armed (armed=%v shake=%v)", room.sfxArmed, room.sfxShake)
	}
	if room.sfxBase != "" {
		t.Errorf("SFXName '1' must resolve to no audible SFX, got base %q", room.sfxBase)
	}
	// Before the deadline: no shake.
	room.Update(70 * time.Millisecond)
	if room.Scene.ShakeLeft != 0 {
		t.Fatalf("shake fired early at 70ms (deadline 80ms)")
	}
	// After the deadline: shake fires, no audible SFX.
	room.Update(20 * time.Millisecond) // total 90ms > 80ms
	if room.Scene.ShakeLeft != ScreenshakeDuration {
		t.Errorf("preanim shake must fire at the deadline, ShakeLeft=%v", room.Scene.ShakeLeft)
	}
	if len(audio.sfx) != 0 {
		t.Errorf("SFXName '1' must play no sound, got %v", audio.sfx)
	}
}

// TestPreanimShakeGatedByReduceMotion pins that the deadline shake honors the
// accessibility gate: reduce-motion suppresses it (nothing is armed to shake).
func TestPreanimShakeGatedByReduceMotion(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.ReduceMotion = true
	msg := &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", PreEmote: "point", Message: "!!!", Side: "wit",
		EmoteMod: protocol.EmoteModPreanim, Screenshake: true, SFXName: "1",
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	if room.sfxShake {
		t.Error("reduce-motion must suppress the armed preanim shake")
	}
	// Armed nothing (no SFX, no shake): stays disarmed.
	if room.sfxArmed {
		t.Error("nothing to fire — must not arm")
	}
}

// TestMCLoopingAndEffectsParse pins #15: MC field 3 (looping) and field 5
// (MUSIC_EFFECT flags) parse onto EventMusic, and short/malformed packets
// degrade to the AsyncAO defaults (loop forever, no effects) — old servers send
// only fields 0-2.
func TestMCLoopingAndEffectsParse(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "h")

	// Full 2.9 MC: track, charID, showname, looping=0, channel=0, effects=FADE_IN.
	ev := feed(t, s, "MC#Trial.opus#2#DJ#0#0#1#%")
	if len(ev) != 1 || ev[0].Kind != EventMusic {
		t.Fatalf("MC events = %+v, want one EventMusic", ev)
	}
	if ev[0].Loop {
		t.Error("looping field '0' must map to Loop=false (play once)")
	}
	if ev[0].MusicEffects != musicEffectFadeIn {
		t.Errorf("effects = %d, want FADE_IN (%d)", ev[0].MusicEffects, musicEffectFadeIn)
	}

	// NO_REPEAT overrides the looping field: looping=1 + effects=8 → play once
	// (AO2 isLooping = loopEnabled && !(flags & NO_REPEAT)).
	ev = feed(t, s, "MC#Cornered.opus#2#DJ#1#0#8#%")
	if ev[0].Loop {
		t.Error("NO_REPEAT flag must override looping=1 → Loop=false")
	}
	if ev[0].MusicEffects != musicEffectNoRepeat {
		t.Errorf("effects = %d, want NO_REPEAT (%d)", ev[0].MusicEffects, musicEffectNoRepeat)
	}

	// looping=1, no effects → loop forever.
	ev = feed(t, s, "MC#Pursuit.opus#2#DJ#1#0#0#%")
	if !ev[0].Loop {
		t.Error("looping=1 with no NO_REPEAT must loop")
	}

	// Short legacy MC (no looping/effects fields): default is loop-forever, no
	// effects — DELIBERATELY differs from AO2's looping=false default so legacy
	// tsuservers that rely on client-side looping keep working.
	ev = feed(t, s, "MC#Investigation.opus#2#%")
	if !ev[0].Loop {
		t.Error("absent looping field must default to Loop=true (AsyncAO client-side loop)")
	}
	if ev[0].MusicEffects != 0 {
		t.Errorf("absent effects field must default to 0, got %d", ev[0].MusicEffects)
	}

	// Malformed effects field degrades to 0 (atoiOr fallback), doesn't drop the event.
	ev = feed(t, s, "MC#Reminiscence.opus#2#DJ#1#0#garbage#%")
	if len(ev) != 1 || ev[0].MusicEffects != 0 {
		t.Errorf("malformed effects → events=%+v, want one with Effects=0", ev)
	}
}

// TestEventMusicPlumbsLoopEffects pins #15: EventMusic's Loop/Effects reach the
// audio sink's PlayMusic call.
func TestEventMusicPlumbsLoopEffects(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	room.HandleEvent(Event{Kind: EventMusic, Text: "Trial.opus", Loop: false, MusicEffects: musicEffectFadeIn})
	if len(audio.music) != 1 {
		t.Fatalf("PlayMusic calls = %d, want 1", len(audio.music))
	}
	if audio.musicLoop[0] {
		t.Error("Loop=false must plumb through to PlayMusic(loop=false)")
	}
	if audio.musicEffects[0] != musicEffectFadeIn {
		t.Errorf("effects plumbed = %d, want FADE_IN (%d)", audio.musicEffects[0], musicEffectFadeIn)
	}

	room.HandleEvent(Event{Kind: EventMusic, Text: "Cornered.opus", Loop: true, MusicEffects: 0})
	if !audio.musicLoop[1] || audio.musicEffects[1] != 0 {
		t.Errorf("second play = {loop:%v effects:%d}, want {true 0}", audio.musicLoop[1], audio.musicEffects[1])
	}
}
