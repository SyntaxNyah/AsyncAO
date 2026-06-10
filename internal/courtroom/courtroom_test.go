package courtroom

import (
	"path/filepath"
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
}

func (a *audioRecorder) PlayShout(base string)             { a.shouts = append(a.shouts, base) }
func (a *audioRecorder) PlaySFX(b string, _ time.Duration) { a.sfx = append(a.sfx, b) }
func (a *audioRecorder) PlayBlip(base string)              { a.blips = append(a.blips, base) }
func (a *audioRecorder) PlayMusic(url string)              { a.music = append(a.music, url) }

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
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"))
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

// --- URL builder ----------------------------------------------------------------

func TestURLBuilderConventions(t *testing.T) {
	u := NewURLBuilder("http://cdn.example.com/base/")
	cases := map[string]string{
		u.CharIcon("Phoenix"):                          "http://cdn.example.com/base/characters/phoenix/char_icon",
		u.Emote("Phoenix", "Normal", EmoteIdle):        "http://cdn.example.com/base/characters/phoenix/(a)normal",
		u.Emote("Phoenix", "Normal", EmoteTalk):        "http://cdn.example.com/base/characters/phoenix/(b)normal",
		u.Emote("Phoenix", "zoom", EmotePreanim):       "http://cdn.example.com/base/characters/phoenix/zoom",
		u.ShoutBubble("Maya", "objection", false):      "http://cdn.example.com/base/characters/maya/objection_bubble",
		u.ShoutBubble("Maya", "custom", true):          "http://cdn.example.com/base/characters/maya/custom",
		u.DefaultShoutBubble("holdit"):                 "http://cdn.example.com/base/misc/default/holdit_bubble",
		u.ShoutSFX("Maya", "objection"):                "http://cdn.example.com/base/characters/maya/objection",
		u.Background("Courtroom 1", "defenseempty"):    "http://cdn.example.com/base/background/courtroom%201/defenseempty",
		u.SFX("sfx-Stab"):                              "http://cdn.example.com/base/sounds/general/sfx-stab",
		u.Blip("Male"):                                 "http://cdn.example.com/base/sounds/blips/male",
		u.MusicURL("Objection.opus"):                   "http://cdn.example.com/base/sounds/music/objection.opus",
		u.MusicURL("https://radio.example.com/x.opus"): "https://radio.example.com/x.opus",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("url = %q, want %q", got, want)
		}
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

	feed(t, s, "SM#Basement#Courtroom 1#Objection.opus#Trial.mp3#%")
	if len(s.Areas) != 2 || len(s.Music) != 2 {
		t.Errorf("areas=%v music=%v", s.Areas, s.Music)
	}

	ev = feed(t, s, "DONE#%")
	if len(ev) != 1 || ev[0].Kind != EventReady || s.Phase() != PhaseReady {
		t.Fatalf("DONE → %+v phase=%v", ev, s.Phase())
	}

	// Reply ladder must be exactly the fast-loading sequence.
	want := []string{"HI", "ID", "RC", "RM", "RD"}
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

func TestTypewriterSkipToEnd(t *testing.T) {
	tw := NewTypewriter()
	tw.Start("a long message that should skip")
	tw.SkipToEnd()
	if !tw.Done() {
		t.Error("SkipToEnd did not finish the reveal")
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
