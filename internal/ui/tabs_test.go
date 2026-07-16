package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// testTabApp builds a headless App with just enough wiring for the tab
// machinery (real prefs for callword checks; no SDL, no network).
func testTabApp(t *testing.T) *App {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatalf("prefs: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{ctx: &Ctx{}, activeTab: -1}
	a.d.Prefs = prefs
	a.resetSessionState()
	return a
}

// TestDoNotDisturbSilencesCallword pins that DND short-circuits checkCallwords
// before any side effect: with DND on, a matching callword sets no toast and
// never reaches the (nil-in-test) Audio — so the gate must stay above the alert
// calls (a nil-Audio panic here would catch it being moved below them). The
// DND-off path fires real SDL audio, so it's covered by inspection, not here.
func TestDoNotDisturbSilencesCallword(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetCallWords([]string{"phoenix"})
	a.dndOn = true
	a.checkCallwords("objection, phoenix!", nil, false) // must early-return, not touch Audio
	if a.warnLine != "" {
		t.Errorf("DND must suppress the callword toast, got warnLine=%q", a.warnLine)
	}
}

// TestHotkeyConflictKeys pins the M7 conflict detector: distinct defaults never
// clash, but rebinding two actions onto the same key flags that key (the
// dispatch switch would otherwise silently drop the later action).
func TestHotkeyConflictKeys(t *testing.T) {
	a := testTabApp(t)
	if got := a.hotkeyConflictKeys(); len(got) != 0 {
		t.Fatalf("default hotkeys must not conflict, got %v", got)
	}
	a.d.Prefs.SetHotkey(hotkeyPosCycle, "z")
	a.d.Prefs.SetHotkey(hotkeyMusicStop, "z") // two actions, one key
	got := a.hotkeyConflictKeys()
	if !got["z"] || len(got) != 1 {
		t.Errorf("only 'z' should conflict after the double-bind, got %v", got)
	}
}

// TestToggleServerFriend pins the player-list "+ Friend" toggle: the first call
// adds the name to this server's friend list, the second removes it (matched by
// name, case-insensitive, ignoring any =colour suffix).
func TestToggleServerFriend(t *testing.T) {
	a := testTabApp(t)
	a.serverKey = "wss://srv:2096"
	a.toggleServerFriend("Alice")
	if ok, _ := a.d.Prefs.ServerFriendMatch(a.serverKey, "alice"); !ok {
		t.Fatal("first toggle should add Alice as a friend")
	}
	a.toggleServerFriend("ALICE") // case-insensitive remove
	if ok, _ := a.d.Prefs.ServerFriendMatch(a.serverKey, "Alice"); ok {
		t.Error("second toggle should remove Alice")
	}
}

// TestFunColor pins the M61 message-colour modes + the extended colours (#98)
// + the free hex pick (v1.52.0): neither leaves text+colour alone, random
// swaps the colour, rainbow prefixes \cr and wins over random, an extended
// colour prepends \c<code> and reports the nearest-standard wire colour, a
// custom hex prepends \c#RRGGBB with the nearest wire index and beats ext,
// and a blank/space send is never decorated.
func TestFunColor(t *testing.T) {
	if txt, col := funColor("hi", 4, -1, -1, false, false, 7); txt != "hi" || col != 4 {
		t.Errorf("neither: got (%q,%d), want (hi,4)", txt, col)
	}
	if txt, col := funColor("hi", 4, -1, -1, false, true, 7); txt != "hi" || col != 7 {
		t.Errorf("random: got (%q,%d), want (hi,7)", txt, col)
	}
	if txt, col := funColor("hi", 4, -1, -1, true, true, 7); txt != "\\crhi" || col != 4 {
		t.Errorf("rainbow wins: got (%q,%d), want (\\crhi,4)", txt, col)
	}
	// Extended colour: exact colour rides as inline \c<code>; wire = nearest standard.
	e := render.ExtColorAt(0)
	if txt, col := funColor("hi", 2, 0, -1, false, false, 7); txt != "\\c"+string(e.Code)+"hi" || col != e.Wire {
		t.Errorf("ext: got (%q,%d), want (\\c%shi,%d)", txt, col, string(e.Code), e.Wire)
	}
	// ext = -1 is "none" even with a non-default base colour.
	if txt, col := funColor("hi", 2, -1, -1, false, false, 7); txt != "hi" || col != 2 {
		t.Errorf("ext none: got (%q,%d), want (hi,2)", txt, col)
	}
	// Custom hex: exact colour rides as \c#RRGGBB; the wire falls back to the
	// nearest standard palette index (pure green → Green, index 1) — and the
	// custom pick beats a set ext colour.
	if txt, col := funColor("hi", 4, 0, 0x00ff00, false, false, 7); txt != "\\c#00ff00hi" || col != render.NearestTextColorIndex(0, 255, 0) {
		t.Errorf("custom hex: got (%q,%d), want (\\c#00ff00hi,%d)", txt, col, render.NearestTextColorIndex(0, 255, 0))
	}
	if txt, _ := funColor(" ", 4, -1, 0x00ff00, true, true, 7); txt != " " {
		t.Error("a blank/space send must be left undecorated")
	}
}

// TestExtColorCodesConsistent pins the parser's gate set (courtroom.ExtColorCodes)
// to the render palette that owns the RGB (#98): every dropdown colour must be a
// gated letter the parser will render, and every gated letter must resolve to a
// colour. The cross-AsyncAO guarantee depends on both clients agreeing letter→
// colour, and these tables live in different packages, so this can't drift.
func TestExtColorCodesConsistent(t *testing.T) {
	if len(courtroom.ExtColorCodes) != render.ExtColorCount() {
		t.Fatalf("courtroom.ExtColorCodes has %d letters, render has %d ext colours — must match 1:1",
			len(courtroom.ExtColorCodes), render.ExtColorCount())
	}
	for i := 0; i < render.ExtColorCount(); i++ {
		e := render.ExtColorAt(i)
		if strings.IndexByte(courtroom.ExtColorCodes, e.Code) < 0 {
			t.Errorf("render ext colour %q (%c) is not parser-gated — it would never render", e.Name, e.Code)
		}
		if e.Wire < 0 || e.Wire >= render.TextColorCount {
			t.Errorf("ext colour %q wire fallback %d out of standard range 0..%d", e.Name, e.Wire, render.TextColorCount-1)
		}
	}
	for i := 0; i < len(courtroom.ExtColorCodes); i++ {
		if _, ok := render.ExtColorByCode(courtroom.ExtColorCodes[i]); !ok {
			t.Errorf("gated code %c has no render colour", courtroom.ExtColorCodes[i])
		}
	}
}

// TestTabParkActivateRoundTrip pins the core invariant: parking moves the
// WHOLE session out (live state pristine afterwards), activating moves it
// back bit-for-bit (logs, seqs, identity).
func TestTabParkActivateRoundTrip(t *testing.T) {
	a := testTabApp(t)
	if !a.allocateTab() {
		t.Fatal("first allocate must succeed")
	}
	a.serverName = "Skrapegropen"
	a.serverKey = "wss://skra.example:2096"
	// A session must exist or activation treats the tab as dead. The
	// rehearsal constructor gives a real offline session without a conn.
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	a.icInput = "half-typed message"
	a.icLog = append(a.icLog, icEntry{text: "Phoenix: hold it", color: 2})
	a.icLogSeq = 7
	a.oocLog = append(a.oocLog, "mod: welcome")
	a.oocSeq = 3
	// The three settings that used to LEAK across tabs (playtest reports): the
	// pair placement (was App-global), the iniswap override, and the /pos side.
	a.pairOffX, a.pairOffY, a.pairFlip = 30, -20, true
	a.iniChar = "SwappedChar"
	a.sidePref = "wit"
	a.logSelActive = true // a stale log-text highlight must not survive the switch

	a.parkActive()
	if a.activeTab != -1 || a.serverName != "" || len(a.icLog) != 0 || a.icInput != "" {
		t.Fatalf("park must leave a pristine live session: name=%q log=%d input=%q",
			a.serverName, len(a.icLog), a.icInput)
	}
	if a.spriteOv == nil || a.pairWith == 0 {
		t.Fatal("reset state must re-init maps and sentinels")
	}
	// Park must hand the live session a PRISTINE pair/iniswap/pos — not the parked
	// tab's values (that shared-live-field bleed was the bug). Pair placement reseeds
	// from prefs (default 0/0/false in this fresh test config).
	if a.iniChar != "" || a.sidePref != "" || a.pairOffX != 0 || a.pairOffY != 0 || a.pairFlip {
		t.Fatalf("park must reset per-session pair/iniswap/pos: ini=%q side=%q off=%d/%d flip=%v",
			a.iniChar, a.sidePref, a.pairOffX, a.pairOffY, a.pairFlip)
	}
	if a.logSelActive {
		t.Error("park must clear the log text selection (its anchors point into the old log)")
	}
	parked := &a.tabs[0].state
	if parked.serverName != "Skrapegropen" || parked.icLogSeq != 7 || len(parked.oocLog) != 1 {
		t.Fatalf("parked state lost data: %+v", parked.serverName)
	}

	a.activateTab(0)
	if a.activeTab != 0 || a.serverName != "Skrapegropen" || a.icInput != "half-typed message" {
		t.Fatalf("activate must restore the session: name=%q input=%q", a.serverName, a.icInput)
	}
	if a.icLogSeq != 7 || len(a.icLog) != 1 || a.icLog[0].color != 2 {
		t.Fatal("activate must restore logs and seqs bit-for-bit")
	}
	// The previously-leaking settings come back with THIS tab's session.
	if a.iniChar != "SwappedChar" || a.sidePref != "wit" || a.pairOffX != 30 || a.pairOffY != -20 || !a.pairFlip {
		t.Fatalf("activate must restore per-session pair/iniswap/pos: ini=%q side=%q off=%d/%d flip=%v",
			a.iniChar, a.sidePref, a.pairOffX, a.pairOffY, a.pairFlip)
	}
	if a.tabs[0].state.serverName != "" {
		t.Fatal("the active tab's parked slot must be zeroed")
	}
}

// TestStageRestoreMsg pins buildRoom's viewport-restore gate: the session's
// LastIC is the seed for the settled re-stage (RestoreMessage), an ignored
// speaker is dropped exactly like the live #81 filter (which `continue`s past
// room.HandleEvent), and no session / no message means leave the stage blank.
func TestStageRestoreMsg(t *testing.T) {
	a := testTabApp(t)
	if a.stageRestoreMsg() != nil {
		t.Fatal("no session: want nil")
	}
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	if a.stageRestoreMsg() != nil {
		t.Fatal("no LastIC: want nil")
	}
	msg := &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Message: "Hello!"}
	a.sess.LastIC = msg
	if a.stageRestoreMsg() != msg {
		t.Fatal("want the session's LastIC back")
	}
	a.serverKey = "wss://test.example/ws"
	a.d.Prefs.SetServerIgnored(a.serverKey, []string{"Phoenix"})
	if a.stageRestoreMsg() != nil {
		t.Fatal("an ignored speaker must not be re-staged")
	}
	a.d.Prefs.SetServerIgnored(a.serverKey, nil)
	if a.stageRestoreMsg() != msg {
		t.Fatal("clearing the ignore list must restore the seed")
	}
}

// TestTabSwitchNoLogCrossover pins the multi-tab log-crossover fix (playtest:
// switching tabs showed the OTHER server's IC/OOC lines with the wrong colours).
// The IC/OOC wrap+filter caches live on App and were keyed only by the per-session
// log seq — which starts at 0 in EVERY tab — so two tabs at the same seq collided
// on the shared cache and a switch served tab A's wrapped lines under tab B. The
// fix folds a per-view epoch (bumped on every session swap) into every log cache
// key. This test sets up two tabs whose logs carry DISTINCT lines at the SAME seq
// (the exact collision the bug needed), switches back and forth, and asserts each
// active tab's wrapped IC + OOC output contains ONLY its own lines both directions,
// plus that the epoch actually advanced across the switch (the fixed seam).
func TestTabSwitchNoLogCrossover(t *testing.T) {
	a := testTabApp(t)
	a.logPct, a.oocPct = DefaultScalePct, DefaultScalePct // nil-font 8 px/char path

	// Tab A: distinctive IC + OOC content at seq 1.
	if !a.allocateTab() {
		t.Fatal("allocate tab A")
	}
	a.serverName, a.serverKey = "Alpha", "wss://alpha:2096"
	a.sess = courtroom.NewRehearsalSession("", []string{"Apollo"})
	a.icLog = []icEntry{{text: "ALPHA-IC-only line", color: 2}}
	a.icLogSeq = 1
	a.oocLog, a.oocSpeakers = []string{"ALPHA-OOC-only line"}, []string{"amod"}
	a.oocSeq = 1
	// Prime the caches under tab A.
	aICseen := a.icWrapped(800, false)
	aOOCseen := a.oocWrapped(800)
	if !rowsContain(aICseen, "ALPHA-IC-only") || !linesContain(aOOCseen, "ALPHA-OOC-only") {
		t.Fatalf("tab A must render its own lines, IC=%v OOC=%v", aICseen, aOOCseen)
	}
	epochA := a.logViewEpoch

	// Tab B: DIFFERENT content but the SAME seqs (1) — the collision the bug needed.
	a.parkActive()
	if !a.allocateTab() {
		t.Fatal("allocate tab B")
	}
	a.serverName, a.serverKey = "Beta", "wss://beta:2096"
	a.sess = courtroom.NewRehearsalSession("", []string{"Klavier"})
	a.icLog = []icEntry{{text: "BETA-IC-only line", color: 4}}
	a.icLogSeq = 1
	a.oocLog, a.oocSpeakers = []string{"BETA-OOC-only line"}, []string{"bmod"}
	a.oocSeq = 1

	bIC := a.icWrapped(800, false)
	bOOC := a.oocWrapped(800)
	if rowsContain(bIC, "ALPHA-IC") || !rowsContain(bIC, "BETA-IC") {
		t.Fatalf("tab B IC crossover: got %v (must be only BETA)", bIC)
	}
	if linesContain(bOOC, "ALPHA-OOC") || !linesContain(bOOC, "BETA-OOC") {
		t.Fatalf("tab B OOC crossover: got %v (must be only BETA)", bOOC)
	}
	if a.logViewEpoch == epochA {
		t.Fatalf("the log-view epoch must advance across a switch (was %d)", epochA)
	}

	// Switch BACK to tab A (index 0) and assert its own lines return, not tab B's.
	a.activateTab(0)
	if a.serverName != "Alpha" {
		t.Fatalf("switch back must restore tab A, got %q", a.serverName)
	}
	aIC := a.icWrapped(800, false)
	aOOC := a.oocWrapped(800)
	if rowsContain(aIC, "BETA-IC") || !rowsContain(aIC, "ALPHA-IC") {
		t.Fatalf("tab A IC crossover on return: got %v (must be only ALPHA)", aIC)
	}
	if linesContain(aOOC, "BETA-OOC") || !linesContain(aOOC, "ALPHA-OOC") {
		t.Fatalf("tab A OOC crossover on return: got %v (must be only ALPHA)", aOOC)
	}
	// The colour rides the entry, so a crossover would also mis-tint: pin the source
	// entry's colour is tab A's (2), never tab B's (4).
	if len(a.icLog) != 1 || a.icLog[0].color != 2 {
		t.Fatalf("tab A log colour must be its own (2), got %v", a.icLog)
	}
}

// rowsContain reports whether any wrapped IC row's text contains sub.
func rowsContain(rows []icWrapLine, sub string) bool {
	for _, r := range rows {
		if strings.Contains(r.text, sub) {
			return true
		}
	}
	return false
}

// linesContain reports whether any wrapped OOC line contains sub.
func linesContain(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// TestBuildRoomRebindsSceneryPerTab pins the session→scene half of the background
// leak fix: each tab's own background name maps to a DISTINCT scenery base — the
// value buildRoom re-stages into the room's Scene.BackgroundBase and then hands to
// the shared viewport via RebindScenery. The leak was that both tabs ended up on
// one background; the fix relies on each tab's base being its own. This asserts the
// base computation setBackground uses (URLBuilder.Background over the position's bg
// part) yields two different bases for two different backgrounds — headless, no
// Manager/SDL. The render-side hold that actually surfaced the bug (the shared
// viewport's sticky scenery gate) is pinned in render's
// TestRebindSceneryForcesNewBaseAcrossSwitch.
func TestBuildRoomRebindsSceneryPerTab(t *testing.T) {
	u := courtroom.URLBuilder{}
	bgPart, _ := courtroom.PositionScene("") // fresh room starts at the default position
	baseA := u.Background("gs4", bgPart)
	baseB := u.Background("prosecutorlobby", bgPart)
	if baseA == "" || baseB == "" {
		t.Fatalf("each background must map to a non-empty base, A=%q B=%q", baseA, baseB)
	}
	if baseA == baseB {
		t.Fatalf("two tabs on different backgrounds must map to DISTINCT bases, both = %q", baseA)
	}
}

// TestEnsureSFXChoices pins the IC-bar SFX picker list: "auto" first, then the
// character's DISTINCT emote sounds (char.ini [SoundN]); the AO silence values
// ("0"/"1"/empty) and duplicates are dropped, and an out-of-range pick clamps to auto.
func TestEnsureSFXChoices(t *testing.T) {
	a := testTabApp(t)
	a.iniChar = "TestChar" // activeCharName() → this, no session needed
	a.emotes = []courtroom.Emote{
		{Anim: "normal", SFXName: "0"},       // silence → skipped
		{Anim: "slam", SFXName: "deskslam"},  // sound
		{Anim: "point", SFXName: "deskslam"}, // duplicate → skipped
		{Anim: "gavel", SFXName: "gavel"},    // sound
		{Anim: "idle", SFXName: ""},          // silence → skipped
		{Anim: "shout", SFXName: "1"},        // silence → skipped
	}
	a.ensureSFXChoices()
	want := []string{sfxAutoLabel, "deskslam", "gavel"}
	if len(a.sfxChoices) != len(want) {
		t.Fatalf("choices = %v, want %v", a.sfxChoices, want)
	}
	for i, w := range want {
		if a.sfxChoices[i] != w {
			t.Errorf("choice %d = %q, want %q", i, a.sfxChoices[i], w)
		}
	}
	a.sfxChoiceIdx, a.sfxChoicesForName = 5, "\x00stale" // out-of-range pick + a never-matching name forces a rebuild
	a.ensureSFXChoices()
	if a.sfxChoiceIdx != 0 {
		t.Errorf("out-of-range pick must clamp to auto (0), got %d", a.sfxChoiceIdx)
	}
}

// TestControlPinnedClientSwaps pins click-to-control: making the floating (pinned)
// server the live courtroom promotes it to the active tab and demotes the old primary
// into the float — the two trade places, both stay open.
func TestControlPinnedClientSwaps(t *testing.T) {
	a := testTabApp(t)
	if !a.allocateTab() {
		t.Fatal("allocate primary tab")
	}
	a.serverName, a.serverKey = "Primary", "wss://primary"
	a.sess = courtroom.NewRehearsalSession("", []string{"P"})
	a.tabs = append(a.tabs, &courtTab{state: sessionState{
		serverName: "Pinned", serverKey: "wss://pinned",
		sess: courtroom.NewRehearsalSession("", []string{"Q"}),
	}})
	a.activeTab, a.tabDragFrom = 0, -1
	a.pinToSplit(a.tabs[1])
	if !a.splitActive() || a.splitTab != a.tabs[1] {
		t.Fatal("setup: tab 1 must be pinned as the float")
	}

	a.controlPinnedClient()

	if a.activeTab != 1 || a.serverName != "Pinned" {
		t.Fatalf("the pinned server must become the live primary: activeTab=%d name=%q", a.activeTab, a.serverName)
	}
	if !a.splitActive() || a.splitTab != a.tabs[0] {
		t.Fatal("the old primary must become the new float")
	}
}

// TestClientControlClick pins the gesture filter: a plain click in the client view
// requests a control swap, but a drag (cursor travel) does not (that's a pan).
func TestClientControlClick(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	view := sdl.Rect{X: 0, Y: 0, W: 200, H: 200}

	a.pendingControlSwap, a.clientPanning = false, false
	c.clicked = true
	c.mouseX, c.mouseY, c.downX, c.downY = 50, 50, 50, 50 // released where pressed → a click
	a.clientControlClick(view)
	if !a.pendingControlSwap {
		t.Error("a click in the view must request control")
	}

	a.pendingControlSwap = false
	c.mouseX, c.mouseY, c.downX, c.downY = 150, 150, 50, 50 // travelled far → a drag, not a click
	a.clientControlClick(view)
	if a.pendingControlSwap {
		t.Error("a drag must not request control (it pans)")
	}
}

// TestClientViewSrcZoom pins the full-view zoom/pan source-rect math: zoom 1 shows the
// whole texture (fit), zooming in shows a centred window, and panning clamps so the
// window never leaves the texture.
func TestClientViewSrcZoom(t *testing.T) {
	a := testTabApp(t)
	a.clientTexW, a.clientTexH = 1000, 800
	a.clientZoom, a.clientPanX, a.clientPanY = 1, 0.5, 0.5
	if s := a.clientViewSrc(); s.X != 0 || s.Y != 0 || s.W != 1000 || s.H != 800 {
		t.Fatalf("zoom 1 must show the whole texture, got %+v", s)
	}
	a.clientZoom = 2 // centred: middle 500×400 at (250,200)
	if s := a.clientViewSrc(); s.W != 500 || s.H != 400 || s.X != 250 || s.Y != 200 {
		t.Fatalf("zoom 2 centred = 500x400 at (250,200), got %+v", s)
	}
	a.clientPanX, a.clientPanY = 0, 0 // pan past the corner → clamps to (0,0)
	a.clampClientPan()
	if s := a.clientViewSrc(); s.X != 0 || s.Y != 0 {
		t.Fatalf("pan to corner must clamp src to (0,0), got %+v", s)
	}
	a.clientPanX, a.clientPanY = 1, 1 // pan past the far corner → clamps to (tw-sw, th-sh)
	a.clampClientPan()
	if s := a.clientViewSrc(); s.X != 500 || s.Y != 400 {
		t.Fatalf("pan to far corner must clamp src to (500,400), got %+v", s)
	}
}

// TestClientZoomWheel pins wheel-zoom: scrolling up over the view zooms in and consumes
// the wheel; scrolling all the way out clamps back to the fit floor.
func TestClientZoomWheel(t *testing.T) {
	a := testTabApp(t)
	a.clientTexW, a.clientTexH = 1000, 800
	a.clientZoom, a.clientPanX, a.clientPanY = 1, 0.5, 0.5
	c := a.ctx
	view := sdl.Rect{X: 0, Y: 0, W: 400, H: 300}
	pressed := false
	c.mouseX, c.mouseY, c.wheelY = 200, 150, 1
	a.handleClientZoomPan(view, &pressed)
	if a.clientZoom <= 1 {
		t.Fatalf("wheel up must zoom in, got %v", a.clientZoom)
	}
	if c.wheelY != 0 {
		t.Error("zoom must consume the wheel so panels behind don't also scroll")
	}
	for i := 0; i < 50; i++ { // scroll all the way back out
		c.mouseX, c.mouseY, c.wheelY = 200, 150, -1
		a.handleClientZoomPan(view, &pressed)
	}
	if a.clientZoom != clientZoomMin {
		t.Fatalf("zoom out must clamp to the fit floor %v, got %v", clientZoomMin, a.clientZoom)
	}
}

// TestRenderFullClientTextureRestores is a headless smoke test of the full-theme
// view's swap/restore plumbing — the path with the most ways to SILENTLY corrupt the
// primary (half-restored sessionState/room/viewport, render target left bound, scale
// not reset). The pinned tab's swapped-in session is nil, so the inner drawCourtroom
// hits its sess==nil early-return (no fonts/room/theme harness needed); the test then
// asserts every piece of primary state — and the renderer — was restored.
func TestRenderFullClientTextureRestores(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	a := testTabApp(t)
	a.ctx.Ren = ren
	a.uiScalePct = 100
	// Distinctive primary state that must survive the pinned pass untouched.
	a.serverName = "PrimaryServer"
	a.screen = ScreenCourtroom
	primaryVP := render.NewViewport(nil)
	a.d.Viewport = primaryVP
	// A pinned client whose swapped-in session is nil → the inner drawCourtroom
	// early-returns (and sets a.screen=lobby, which the snapshot must undo).
	a.splitVP = render.NewViewport(nil)
	a.splitRoom = courtroom.NewCourtroom(courtroom.URLBuilder{}, nil, nil, courtroom.NopAudio{})
	a.splitTab = &courtTab{state: sessionState{serverName: "PinnedServer"}} // sess stays nil

	a.renderFullClientTexture(640, 480)

	if a.serverName != "PrimaryServer" {
		t.Errorf("primary sessionState not restored: serverName=%q", a.serverName)
	}
	if a.d.Viewport != primaryVP {
		t.Error("primary viewport (a.d.Viewport) not restored")
	}
	if a.screen != ScreenCourtroom {
		t.Errorf("primary screen not restored: got %v (the pinned pass set it to lobby)", a.screen)
	}
	if a.room != nil {
		t.Error("primary room not restored (must stay nil here, not the splitRoom)")
	}
	if a.pinnedPass {
		t.Error("pinnedPass left set — primary polls would stay suppressed")
	}
	if a.ctx.Ren.GetRenderTarget() != nil {
		t.Error("render target left bound to the texture — the screen would render into it")
	}
	if a.clientTex == nil {
		t.Error("clientTex should have been created")
	}
}

// BenchmarkClientInputSnapshot pins that the per-frame glue the full-theme view adds
// AROUND the second drawCourtroom render — snapshotting, neutralizing, then restoring
// the Ctx input so the view-only pass handles nothing — is allocation-free. The second
// render itself is the inherent, opt-in ~2× cost of a live second client; this proves
// we add no per-frame GARBAGE on top of it (the avoidable sin). The default/compact
// path never runs any of this (gated on clientFull && splitActive).
func BenchmarkClientInputSnapshot(b *testing.B) {
	a := &App{ctx: &Ctx{}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		in := a.snapshotInput()
		a.restoreInput(in)
	}
}

// TestTabTearOffToSplit pins the pass-4 gesture: dragging a BACKGROUND chip below
// the strip enters tear-off mode (suspending reorder), and releasing there pins
// that tab as the split's right pane — while the ACTIVE tab can never tear off
// (it is the left pane already). handleTabDrag takes the chip rects directly, so
// the gesture drives without the font-dependent tabBarRects.
func TestTabTearOffToSplit(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	a.tabs = []*courtTab{
		{}, // index 0: the active (left) tab — its live session lives in a.sessionState
		{state: sessionState{serverName: "Pinned", sess: courtroom.NewRehearsalSession("", []string{"X"})}},
	}
	a.activeTab = 0
	a.tabDragFrom = -1 // NewApp seeds this idle sentinel; the raw test App needs it too
	rects := []sdl.Rect{{X: 0, Y: 0, W: 60, H: tabBarH}, {X: 64, Y: 0, W: 60, H: tabBarH}}

	// Press on the background chip's body (index 1), inside the strip.
	c.mouseDown, c.mouseX, c.mouseY = true, 80, 10
	a.handleTabDrag(rects, true)
	// Pull it down past the threshold: promotes to a drag and enters tear-off.
	c.mouseX, c.mouseY = 80, tabTearOffY+20
	if a.handleTabDrag(rects, false) {
		t.Fatal("an in-progress drag must not report completion")
	}
	if !a.tabTearingOff() {
		t.Fatal("a background chip dragged below the strip must be in tear-off mode")
	}
	// Release below the strip → the background tab pins to the split's right pane.
	c.mouseDown = false
	if !a.handleTabDrag(rects, false) {
		t.Fatal("releasing a drag must report completion so the click is swallowed")
	}
	if a.splitTab != a.tabs[1] || !a.splitActive() {
		t.Fatalf("tear-off release must pop the background tab out as a floating client")
	}
	if !a.clientWin.placed {
		t.Error("tear-off drop must place the floating client window at the cursor")
	}

	// Re-tearing the ALREADY-pinned tab repositions its window instead of toggling
	// it closed (the drop guard): drag tab 1 again and drop elsewhere — it stays up.
	c.mouseDown, c.mouseX, c.mouseY = true, 80, 10
	a.handleTabDrag(rects, true)
	c.mouseX, c.mouseY = 300, tabTearOffY+40
	a.handleTabDrag(rects, false)
	c.mouseDown = false
	a.handleTabDrag(rects, false)
	if !a.splitActive() || a.splitTab != a.tabs[1] {
		t.Fatal("re-dropping the pinned tab must reposition its window, not close it")
	}

	// The active tab can't tear off: a drag on it below the strip never enters
	// tear-off mode and never creates a split.
	a.clearSplit()
	c.mouseDown, c.mouseX, c.mouseY = true, 20, 10
	a.handleTabDrag(rects, true) // arm on index 0 (the active tab)
	c.mouseX, c.mouseY = 20, tabTearOffY+20
	a.handleTabDrag(rects, false)
	if a.tabTearingOff() {
		t.Error("the active tab must never enter tear-off mode")
	}
	c.mouseDown = false
	a.handleTabDrag(rects, false)
	if a.splitActive() {
		t.Error("dragging the active tab below the strip must not create a split")
	}
}

// TestSendICSplitTargetsPinnedSession pins the dual-input invariant (pass 2c):
// typing into the right pane sends on the PINNED server (its conn, its char id)
// via a momentary sessionState swap, marks the PINNED input pending (keep-until-
// echo), and leaves the primary session bit-for-bit untouched. The swap reuses
// the unmodified sendIC, so a regression that forgets to restore the primary —
// or that sends on the wrong conn/identity — fails here.
func TestSendICSplitTargetsPinnedSession(t *testing.T) {
	a := testTabApp(t)
	// Primary (left) session: a distinct identity that must survive the send.
	a.serverName = "PrimaryServer"
	a.icInput = "primary draft"
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	a.sess.MyCharID = 1

	// Pinned (right) session on a send-capturing conn, with a distinctive char id
	// (4242) so "the pinned identity was used" is unambiguous against MS's many
	// small numeric fields.
	var sent []protocol.Packet
	pinnedSess := courtroom.NewSession(func(p protocol.Packet) error { sent = append(sent, p); return nil }, "")
	pinnedSess.MyCharID = 4242
	pin := &courtTab{state: sessionState{
		serverName: "PinnedServer",
		sess:       pinnedSess,
		sidePref:   "wit",
		evidIdx:    -1,
		pairWith:   protocol.UnpairedCharID,
		icInput:    "hello from the right pane",
	}}
	a.tabs = append(a.tabs, pin)
	a.splitTab = pin
	a.ctx.focusID = "ic-split"

	a.sendICSplit(0)

	// 1. Exactly one MS went out, and on the PINNED conn (the primary's send is a
	//    no-op rehearsal func, so capturing it here proves the routing).
	if len(sent) != 1 || sent[0].Header != "MS" {
		t.Fatalf("want one MS on the pinned conn, got %d packets: %+v", len(sent), sent)
	}
	if !slices.Contains(sent[0].Fields, "hello from the right pane") {
		t.Errorf("the pinned input must ride the wire, fields=%v", sent[0].Fields)
	}
	if !slices.Contains(sent[0].Fields, "4242") {
		t.Errorf("the message must carry the PINNED char id 4242 (swap fed the right identity), fields=%v", sent[0].Fields)
	}
	// 2. The pinned input is KEPT until the pinned server echoes it back
	//    (keep-until-echo: a send the server swallows must not cost the typed
	//    line) — the send only snapshots it as pending, in the PINNED state.
	if pin.state.icInput != "hello from the right pane" {
		t.Errorf("pinned input must be kept until the echo, got %q", pin.state.icInput)
	}
	if pin.state.icPendingSent != "hello from the right pane" {
		t.Errorf("pinned pending snapshot = %q, want the sent line", pin.state.icPendingSent)
	}
	if a.icPendingSent != "" {
		t.Errorf("the PRIMARY session must not inherit the pinned pending, got %q", a.icPendingSent)
	}
	// 3. The primary session is fully restored — name and its own draft intact.
	if a.serverName != "PrimaryServer" || a.icInput != "primary draft" {
		t.Errorf("primary must be untouched: name=%q input=%q", a.serverName, a.icInput)
	}
	if a.sess.MyCharID != 1 {
		t.Errorf("primary session pointer must be restored (MyCharID 1), got %d", a.sess.MyCharID)
	}
	// 4. Focus stays in the right pane so you can keep typing there.
	if a.ctx.focusNext != "ic-split" {
		t.Errorf("focus must stay on the split field, focusNext=%q", a.ctx.focusNext)
	}
}

// TestResetSessionStatePairIsSessionOnly pins that pair placement neither
// persists nor leaks: a fresh session always starts centered/unflipped
// (playtest: offsets were "inexplicably saved" across client restarts), and
// one tab's live placement never seeds another's.
func TestResetSessionStatePairIsSessionOnly(t *testing.T) {
	a := testTabApp(t)
	a.pairOffX, a.pairOffY, a.pairFlip = 15, 25, true // a live tab's placement
	a.resetSessionState()                             // new tab / disconnect / park path
	if a.pairOffX != 0 || a.pairOffY != 0 || a.pairFlip {
		t.Errorf("fresh session pair = %d/%d/%v, want 0/0/false (session-only)", a.pairOffX, a.pairOffY, a.pairFlip)
	}
}

// TestOOCNamePerTab pins the tab isolation (playtest): a name typed in one
// tab stays in that tab — a fresh session seeds from the SAVED default, the
// typed name never rewrites that default, and the parked tab keeps its own.
func TestOOCNamePerTab(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetOOCName("DefaultName")
	// The connect flow: park → allocate → fresh session (connectWith order).
	a.parkActive()
	if !a.allocateTab() {
		t.Fatal("allocateTab must succeed")
	}
	a.resetSessionState()
	if a.oocName != "DefaultName" {
		t.Fatalf("fresh tab must seed the saved default, got %q", a.oocName)
	}
	a.oocName = "TabOnlyName" // typed into the courtroom field (tab-local)
	a.parkActive()
	if !a.allocateTab() {
		t.Fatal("second allocateTab must succeed")
	}
	a.resetSessionState()
	if a.oocName != "DefaultName" {
		t.Errorf("a new tab must NOT inherit another tab's typed name, got %q", a.oocName)
	}
	if got := a.d.Prefs.SavedOOCName(); got != "DefaultName" {
		t.Errorf("typing in the courtroom field must not rewrite the saved default, got %q", got)
	}
	if got := a.tabs[0].state.oocName; got != "TabOnlyName" {
		t.Errorf("the parked tab must keep its own typed name, got %q", got)
	}
}

// TestResetSessionStateSeedsPlayerSort pins that a fresh session seeds the
// Players-tab sorts from prefs, clamped to the valid mode range.
func TestResetSessionStateSeedsPlayerSort(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetPlayerListSort(playerSortName)
	a.d.Prefs.SetPlayerListAreaSort(areaSortPop)
	a.resetSessionState()
	if a.playerSort != playerSortName || a.playerAreaSort != areaSortPop {
		t.Errorf("seed = %d/%d, want %d/%d", a.playerSort, a.playerAreaSort, playerSortName, areaSortPop)
	}
	a.d.Prefs.SetPlayerListSort(99) // a stale/out-of-range value clamps to 0
	a.resetSessionState()
	if a.playerSort != 0 {
		t.Errorf("out-of-range sort must clamp to 0, got %d", a.playerSort)
	}
}

// TestTabCap pins the bound: maxTabs sessions, the next connect refuses
// with a visible reason and leaves the active session untouched.
func TestTabCap(t *testing.T) {
	a := testTabApp(t)
	for i := 0; i < maxTabs; i++ {
		if !a.allocateTab() {
			t.Fatalf("allocate %d must succeed", i)
		}
		a.serverName = "srv"
		a.parkActive()
	}
	if a.allocateTab() {
		t.Fatalf("allocate beyond maxTabs=%d must refuse", maxTabs)
	}
	if a.connErr == "" {
		t.Fatal("the refusal must explain itself on connErr")
	}
}

// TestTabBackgroundRouting pins what parked tabs accumulate: chat into
// their own logs with caps and unread counts; disconnects mark dead; and
// the dead slot reaps on the next allocate.
func TestTabBackgroundRouting(t *testing.T) {
	a := testTabApp(t)
	if !a.allocateTab() {
		t.Fatal("allocate")
	}
	a.serverName = "bg"
	a.serverKey = "wss://bg.example"
	a.parkActive()

	tab := a.tabs[0]
	// OOC by default LOGS but does NOT bump the unread badge: server auto-messages
	// (hourly reminders, etc.) shouldn't light up a "(1)" when nobody chatted.
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventOOC, Name: "mod", Text: "hi"})
	if tab.unread != 0 || len(tab.state.oocLog) != 1 {
		t.Fatalf("OOC must log but not badge by default: unread=%d ooc=%d", tab.unread, len(tab.state.oocLog))
	}
	// IC chat always bumps the badge.
	msg := &protocol.ChatMessage{Message: "objection!", TextColor: 2}
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventMessage, Message: msg})
	if tab.unread != 1 || len(tab.state.icLog) != 1 {
		t.Fatalf("IC chat must badge: unread=%d ic=%d", tab.unread, len(tab.state.icLog))
	}
	// With the opt-in pref ON, OOC counts toward the badge too.
	a.d.Prefs.SetNotifyOnOOC(true)
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventOOC, Name: "mod", Text: "yo"})
	if tab.unread != 2 {
		t.Fatalf("with NotifyOnOOC on, OOC must badge: unread=%d", tab.unread)
	}
	if tab.state.icLogSeq == 0 || tab.state.oocSeq == 0 {
		t.Fatal("background appends must bump the cache-key seqs")
	}

	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventDisconnect, Text: "kicked"})
	if !tab.dead {
		t.Fatal("disconnect must mark the tab dead")
	}
	// Dead tabs reap on allocate: the slot frees up.
	if !a.allocateTab() {
		t.Fatal("allocate must reap the dead tab and succeed")
	}
	if len(a.tabs) != 1 {
		t.Fatalf("dead tab must be gone, have %d tabs", len(a.tabs))
	}
}

// TestCollectOpenTabs pins the restore-on-launch snapshot (M7): the live active
// tab comes from the session fields, parked tabs from their stored state, in
// order, and dead / rehearsal / blank-URL / duplicate-URL slots are skipped.
func TestCollectOpenTabs(t *testing.T) {
	a := &App{activeTab: 1}
	a.serverName = "Active"
	a.serverKey = "wss://active:2096"
	a.tabs = []*courtTab{
		{state: sessionState{serverName: "Parked", serverKey: "wss://parked:2096"}},
		{}, // index 1 = the active tab (its session lives in a.sessionState)
		{state: sessionState{serverName: "Dead", serverKey: "wss://dead:2096"}, dead: true},
		{state: sessionState{serverName: "Reh", serverKey: "wss://reh:2096", rehearsal: true}},
		{state: sessionState{serverName: "NoURL"}},                               // blank URL → skipped
		{state: sessionState{serverName: "Dup", serverKey: "wss://active:2096"}}, // dup of active
	}
	got := a.collectOpenTabs()
	want := []config.OpenTab{
		{Name: "Parked", URL: "wss://parked:2096"},
		{Name: "Active", URL: "wss://active:2096"},
	}
	if len(got) != len(want) {
		t.Fatalf("collected %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tab %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestPerAreaScrollback pins the opt-in per-area IC log swap: leaving an area
// saves its log, entering a fresh one starts empty, and returning restores it.
// With the toggle OFF (default) switching areas never touches the log.
func TestPerAreaScrollback(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetPerAreaScrollback(true)
	a.icLog = []icEntry{{text: "lobby line"}} // default area (curArea "")

	a.switchAreaScrollback("C1")
	if len(a.icLog) != 0 {
		t.Fatalf("entering a fresh area must start empty, got %d", len(a.icLog))
	}
	a.curArea = "C1"
	a.icLog = append(a.icLog, icEntry{text: "c1 line"})

	a.switchAreaScrollback("C2")
	if len(a.icLog) != 0 {
		t.Fatalf("second fresh area must start empty, got %d", len(a.icLog))
	}
	a.curArea = "C2"

	a.switchAreaScrollback("C1") // back to C1 → its line returns
	if len(a.icLog) != 1 || a.icLog[0].text != "c1 line" {
		t.Fatalf("returning to an area must restore its log, got %v", a.icLog)
	}
	a.curArea = "C1"

	a.switchAreaScrollback("") // back to the default area
	if len(a.icLog) != 1 || a.icLog[0].text != "lobby line" {
		t.Fatalf("default area log lost: %v", a.icLog)
	}

	// OFF (default): switching areas must not touch the log.
	b := testTabApp(t)
	b.icLog = []icEntry{{text: "x"}}
	b.switchAreaScrollback("Other")
	if len(b.icLog) != 1 {
		t.Error("with per-area scrollback off, switching areas must leave the log alone")
	}
}

// TestLoweredCacheMemo pins the per-frame query memo.
func TestLoweredCacheMemo(t *testing.T) {
	var l loweredCache
	if got := l.get("  Phoenix "); got != "phoenix" {
		t.Fatalf("get = %q", got)
	}
	first := l.get("  Phoenix ")
	second := l.get("  Phoenix ")
	if first != second || second != "phoenix" {
		t.Fatal("repeat gets must return the memoized value")
	}
	if got := l.get("EDGEWORTH"); got != "edgeworth" {
		t.Fatalf("changed src must re-lower, got %q", got)
	}
}

// TestRequestCloseTabConfirms pins the §3.2 tab-close gate: a manual ✕ on a LIVE
// background tab with Instant-disconnect OFF opens the confirm and closes NOTHING
// until "Yes" (confirmPendingCloseTab); with it ON it closes at once; a DEAD tab
// always closes at once (nothing live to lose). The active tab has no ✕, so the
// gate guards against being asked to close it anyway.
func TestRequestCloseTabConfirms(t *testing.T) {
	// Instant OFF, a live background tab: the ✕ opens the confirm, tab survives.
	a := testTabApp(t)
	a.tabs = []*courtTab{
		{},                                      // index 0: the active (left) tab — live session in a.sessionState
		{state: sessionState{serverName: "Bg"}}, // index 1: live background tab (dead == false)
	}
	a.activeTab = 0
	a.requestCloseTab(1)
	if a.pendingCloseTab != a.tabs[1] {
		t.Fatalf("Instant OFF must stash the pending target, got %v", a.pendingCloseTab)
	}
	if len(a.tabs) != 2 {
		t.Fatalf("the tab must NOT close before confirm, have %d tabs", len(a.tabs))
	}
	// "Yes" closes it and clears the pending state.
	a.confirmPendingCloseTab()
	if a.pendingCloseTab != nil {
		t.Error("confirm must clear the pending target")
	}
	if len(a.tabs) != 1 {
		t.Fatalf("confirm must close the tab, have %d tabs", len(a.tabs))
	}
	// A manually-closed BACKGROUND tab must never arm the ACTIVE session's
	// auto-reconnect (that logic is App-level, scoped to pumpConnection; closing a
	// parked tab closes its own conn directly and touches neither field).
	if !a.autoReconnectAt.IsZero() {
		t.Error("closing a background tab must not arm auto-reconnect (that's active-session logic)")
	}
	if a.deliberateClose {
		t.Error("closing a background tab must not flip the active session's deliberateClose flag")
	}

	// Instant ON: the ✕ closes immediately, no modal.
	b := testTabApp(t)
	b.tabs = []*courtTab{{}, {state: sessionState{serverName: "Bg"}}}
	b.activeTab = 0
	b.d.Prefs.SetInstantDisconnect(true)
	b.requestCloseTab(1)
	if b.pendingCloseTab != nil {
		t.Error("Instant ON must not open a confirm")
	}
	if len(b.tabs) != 1 {
		t.Fatalf("Instant ON must close immediately, have %d tabs", len(b.tabs))
	}

	// A DEAD tab always closes immediately, even with Instant OFF.
	d := testTabApp(t)
	d.tabs = []*courtTab{{}, {state: sessionState{serverName: "Bg"}, dead: true}}
	d.activeTab = 0
	d.requestCloseTab(1)
	if d.pendingCloseTab != nil {
		t.Error("a dead tab must not open a confirm (nothing live to lose)")
	}
	if len(d.tabs) != 1 {
		t.Fatalf("a dead tab must close immediately, have %d tabs", len(d.tabs))
	}
}

// TestConfirmPendingCloseTabByPointer pins the pointer-target invariant (the
// reason the pending close is a *courtTab, not an index): if tabs reorder between
// the ✕-click and the confirm, "Yes" must still close the SAME tab it was opened
// for, not whatever now sits at the old index. Close another tab in between, then
// confirm, and assert the right tab died and activeTab bookkeeping stayed correct.
func TestConfirmPendingCloseTabByPointer(t *testing.T) {
	a := testTabApp(t)
	keep := &courtTab{state: sessionState{serverName: "Keep"}}
	target := &courtTab{state: sessionState{serverName: "Target"}}
	other := &courtTab{state: sessionState{serverName: "Other"}}
	a.tabs = []*courtTab{{}, keep, target, other} // index 0 active
	a.activeTab = 0

	a.requestCloseTab(2) // arm a close on `target` (index 2 at click time)
	if a.pendingCloseTab != target {
		t.Fatalf("pending target must be the pointer at index 2, got %v", a.pendingCloseTab)
	}
	// The strip reorders under the open modal: `keep` moves to the end. `target`
	// is now at a DIFFERENT index than when the ✕ was clicked.
	a.moveTab(1, 3)
	if a.tabs[2] == target {
		t.Fatal("setup: the reorder must move target off index 2 for this test to mean anything")
	}
	a.confirmPendingCloseTab()
	// The right tab (target) is gone; keep and other survive.
	for _, tab := range a.tabs {
		if tab == target {
			t.Fatal("confirm must close the ORIGINAL target tab, not whatever's at the stale index")
		}
	}
	if !slices.Contains(a.tabs, keep) || !slices.Contains(a.tabs, other) {
		t.Error("confirm must not close the wrong (reordered) tabs")
	}
	if a.activeTab != 0 || a.tabs[0].state.serverName != "" {
		t.Errorf("the active tab bookkeeping must survive the reorder+close, activeTab=%d", a.activeTab)
	}
}

// TestConfirmPendingCloseTabDropsStale pins the stale-pointer guard: if the target
// tab is already GONE from a.tabs (reaped / torn-off / closed another way) by the
// time the user hits "Yes", the confirm silently drops the close instead of acting
// on a dangling pointer — no panic, no wrong tab closed.
func TestConfirmPendingCloseTabDropsStale(t *testing.T) {
	a := testTabApp(t)
	gone := &courtTab{state: sessionState{serverName: "Gone"}}
	survivor := &courtTab{state: sessionState{serverName: "Survivor"}}
	a.tabs = []*courtTab{{}, survivor}
	a.activeTab = 0
	a.pendingCloseTab = gone // a pointer no longer in a.tabs

	a.confirmPendingCloseTab()
	if a.pendingCloseTab != nil {
		t.Error("confirm must clear the pending target even when it's stale")
	}
	if len(a.tabs) != 2 || !slices.Contains(a.tabs, survivor) {
		t.Fatalf("a stale target must drop the close, not touch live tabs, have %d tabs", len(a.tabs))
	}
}

// TestConfirmPendingCloseTabRunsClearSplit pins the torntabs concern: a
// confirm-deferred close of the PINNED split tab still runs clearSplit (closeParkedTab
// tears the split down before closing) — the pointer target survives the pin, and
// the split is gone afterward with the right tab removed.
func TestConfirmPendingCloseTabRunsClearSplit(t *testing.T) {
	a := testTabApp(t)
	pinned := &courtTab{state: sessionState{
		serverName: "Pinned",
		sess:       courtroom.NewRehearsalSession("", []string{"X"}),
	}}
	a.tabs = []*courtTab{{}, pinned}
	a.activeTab = 0
	a.pinToSplit(pinned)
	if a.splitTab != pinned || !a.splitActive() {
		t.Fatal("setup: the pinned tab must be the active split")
	}

	a.requestCloseTab(1) // Instant OFF (default) → deferred confirm
	if a.pendingCloseTab != pinned {
		t.Fatalf("pending target must be the pinned tab, got %v", a.pendingCloseTab)
	}
	a.confirmPendingCloseTab()
	if a.splitTab != nil || a.splitActive() {
		t.Error("closing the pinned tab must clearSplit (splitTab left set)")
	}
	if slices.Contains(a.tabs, pinned) {
		t.Fatal("the pinned tab must be closed after confirm")
	}
}

// TestMoveTab pins the drag-reorder slice math: tabs land in the right order
// and activeTab keeps pointing at whatever session was active across the move
// (the two-step remove-then-insert index shift). Slots are identified by
// pointer, mirroring how the live strip carries each parked session.
func TestMoveTab(t *testing.T) {
	cases := []struct {
		name             string
		from, to, active int
		wantOrder        []int // positions expressed as original indices
		wantActive       int
	}{
		{"forward, active follows", 0, 2, 2, []int{1, 2, 0, 3}, 1},
		{"forward, moved is active", 0, 2, 0, []int{1, 2, 0, 3}, 2},
		{"forward, active past range", 1, 2, 3, []int{0, 2, 1, 3}, 3},
		{"backward to front", 3, 0, 1, []int{3, 0, 1, 2}, 2},
		{"no-op", 2, 2, 1, []int{0, 1, 2, 3}, 1},
		{"to end, lobby (no active)", 0, 3, -1, []int{1, 2, 3, 0}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := []*courtTab{{}, {}, {}, {}}
			a := &App{tabs: append([]*courtTab(nil), orig...), activeTab: tc.active}
			a.moveTab(tc.from, tc.to)
			if a.activeTab != tc.wantActive {
				t.Errorf("activeTab = %d, want %d", a.activeTab, tc.wantActive)
			}
			if len(a.tabs) != len(orig) {
				t.Fatalf("len = %d, want %d", len(a.tabs), len(orig))
			}
			for pos, want := range tc.wantOrder {
				if a.tabs[pos] != orig[want] {
					t.Errorf("position %d holds the wrong tab (want original index %d)", pos, want)
				}
			}
		})
	}
}
