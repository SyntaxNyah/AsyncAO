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

	feed(t, s, "SM#Basement#Courtroom 1#Objection.opus#Trial.mp3#%")
	if len(s.Areas) != 2 || len(s.Music) != 2 {
		t.Errorf("areas=%v music=%v", s.Areas, s.Music)
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
	feed(t, s, "SM#Area1#Area2#x.opus#%")

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
