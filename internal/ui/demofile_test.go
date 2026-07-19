package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// demoMS builds a minimal server-shape MS line for the tests.
func demoMS(char, msg string, id int) string {
	m := &protocol.ChatMessage{CharName: char, Emote: "normal", Message: msg, Side: "def", CharID: id, Pair: protocol.PairInfo{CharID: protocol.UnpairedCharID}}
	return protocol.BuildServerMS(m).String()
}

// TestParseDemoRecords pins the line-joining loader: one packet per record,
// multi-line packets (literal newlines in message text) join until the '%'
// terminator — the demoserver.cpp::load_demo loop.
func TestParseDemoRecords(t *testing.T) {
	data := "SC#Phoenix#%\r\nMS#1#-#Phoenix#normal#line one\nline two#def#1#0#0#0#0#0#0#0#0#%\nwait#500#%"
	recs := parseDemoRecords([]byte(data))
	if len(recs) != 3 {
		t.Fatalf("records = %d, want 3: %q", len(recs), recs)
	}
	if !strings.Contains(recs[1], "line one\nline two") {
		t.Errorf("multi-line packet must join with its literal newline: %q", recs[1])
	}
	if recs[2] != "wait#500#%" {
		t.Errorf("last record = %q", recs[2])
	}
}

// TestFixDemoWaitDesync pins the pre-2.9.1 repair: with the SC-first +
// wait-last signature, every wait moves one slot earlier (AO2 PR #496); a
// healthy file is untouched.
func TestFixDemoWaitDesync(t *testing.T) {
	broken := []string{"SC#A#%", "MS#a#%", "wait#100#%", "MS#b#%", "wait#200#%"}
	fixed := fixDemoWaitDesync(broken)
	want := []string{"SC#A#%", "wait#100#%", "MS#a#%", "wait#200#%", "MS#b#%"}
	if len(fixed) != len(want) {
		t.Fatalf("fixed = %v", fixed)
	}
	for i := range want {
		if fixed[i] != want[i] {
			t.Fatalf("fixed[%d] = %q, want %q (all: %v)", i, fixed[i], want[i], fixed)
		}
	}
	healthy := []string{"SC#A#%", "wait#100#%", "MS#a#%"}
	out := fixDemoWaitDesync(healthy)
	for i := range healthy {
		if out[i] != healthy[i] {
			t.Fatal("a healthy demo must pass through unchanged")
		}
	}
}

// TestDemoToRecording pins the conversion: MS/BN/MC become events with
// cumulative capped OffsetMs, the opening BN seeds StartBg, and non-scene
// packets are counted as skipped.
func TestDemoToRecording(t *testing.T) {
	demo := strings.Join([]string{
		"SC#Phoenix#Edgeworth#%",
		"BN#courtroom#wit#%",
		"wait#1000#%",
		demoMS("Phoenix", "hold it", 0),
		"wait#999999#%", // an hour of AFK caps at demoMaxWaitMs
		"CT#server#chatter#%",
		"MC#trial2.opus#0#%",
		demoMS("Edgeworth", "objection", 1),
	}, "\n")
	rec, skipped, truncated, err := demoToRecording([]byte(demo), "https://cdn.example/base/")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 2 { // SC + CT
		t.Errorf("skipped = %d, want 2 (SC + CT)", skipped)
	}
	if truncated != 0 {
		t.Errorf("truncated = %d, want 0 (well under the cap)", truncated)
	}
	if rec.Origin != "https://cdn.example/base/" || rec.StartBg != "courtroom" {
		t.Errorf("origin/StartBg = %q/%q", rec.Origin, rec.StartBg)
	}
	if len(rec.Events) != 4 { // BN + MS + MC + MS
		t.Fatalf("events = %d, want 4: %+v", len(rec.Events), rec.Events)
	}
	if rec.Events[1].OffsetMs != 1000 {
		t.Errorf("first MS offset = %d, want 1000", rec.Events[1].OffsetMs)
	}
	if rec.Events[3].OffsetMs != 1000+demoMaxWaitMs {
		t.Errorf("second MS offset = %d, want the capped %d", rec.Events[3].OffsetMs, 1000+demoMaxWaitMs)
	}
	if m := rec.Events[1].Message; m == nil || m.CharName != "Phoenix" || m.Message != "hold it" {
		t.Errorf("first MS parsed wrong: %+v", m)
	}
}

// TestDemoImportCapEnforced pins the import cap (hard rule §17.4): a demo with
// more valid scene packets than maxRecordedEvents must truncate at the cap so a
// runaway file can't smuggle an over-cap scene past the editor/replay guards.
func TestDemoImportCapEnforced(t *testing.T) {
	// Synthesize exactly maxRecordedEvents valid MS plus a known overflow.
	const overflow = 7
	var b strings.Builder
	b.WriteString("SC#Phoenix#%\n")
	for i := 0; i < maxRecordedEvents+overflow; i++ {
		b.WriteString(demoMS("Phoenix", "line", 0))
		b.WriteString("\n")
	}
	rec, skipped, truncated, err := demoToRecording([]byte(b.String()), "o")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Events) != maxRecordedEvents {
		t.Fatalf("events = %d, want the capped %d", len(rec.Events), maxRecordedEvents)
	}
	if truncated != overflow {
		t.Errorf("truncated = %d, want %d", truncated, overflow)
	}
	if skipped != 1 { // just the opening SC; the cap is enforced via truncated, not skipped
		t.Errorf("skipped = %d, want 1 (the SC; overflow counts as truncated, not skipped)", skipped)
	}
}

// TestDemoRoundTrip pins the interchange: recording → .demo → recording keeps
// the scene (speakers, text, bg, music, timing deltas) and the exported file
// carries a self-consistent synthetic SC with remapped char ids.
func TestDemoRoundTrip(t *testing.T) {
	src := &sceneRecording{
		Version: recordingVersion,
		StartBg: "courtroom",
		Events: []recEvent{
			{OffsetMs: 0, Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{CharName: "Phoenix", Emote: "point", Message: "take that", Side: "def", CharID: 55, Pair: protocol.PairInfo{CharID: protocol.UnpairedCharID}}},
			{OffsetMs: 800, Kind: int(courtroom.EventMusic), Text: "cross.opus"},
			{OffsetMs: 2000, Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{CharName: "Edgeworth", Emote: "desk", Message: "hmph", Side: "pro", CharID: 99, Pair: protocol.PairInfo{CharID: protocol.UnpairedCharID}}},
		},
	}
	data, err := recordingToDemo(src)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "SC#Phoenix#Edgeworth#%") {
		t.Fatalf("demo must open with the synthetic SC, got %q", strings.SplitN(text, "\n", 2)[0])
	}
	if !strings.Contains(text, "wait#800#%") || !strings.Contains(text, "wait#1200#%") {
		t.Errorf("waits must carry the OffsetMs deltas:\n%s", text)
	}

	back, skipped, truncated, err := demoToRecording(data, "o")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 { // just the SC
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if truncated != 0 {
		t.Errorf("truncated = %d, want 0", truncated)
	}
	if back.StartBg != "courtroom" {
		t.Errorf("StartBg = %q", back.StartBg)
	}
	var msgs []*protocol.ChatMessage
	for _, e := range back.Events {
		if courtroom.EventKind(e.Kind) == courtroom.EventMessage {
			msgs = append(msgs, e.Message)
		}
	}
	if len(msgs) != 2 || msgs[0].Message != "take that" || msgs[1].Message != "hmph" {
		t.Fatalf("messages didn't survive: %+v", msgs)
	}
	// Char ids remapped onto the synthetic SC (0, 1) — self-consistent demo.
	if msgs[0].CharID != 0 || msgs[1].CharID != 1 {
		t.Errorf("char ids = %d/%d, want the remapped 0/1", msgs[0].CharID, msgs[1].CharID)
	}
}

// TestDemoImportReciprocalPairing pins pair-field survival through import and the
// synthetic-SC remap on re-export: two speakers pairing with each other (the
// shape the real fixtures carry — a valid partner id, an explicit ^order, and a
// two-axis offset) keep their PairInfo across demoToRecording, and recordingToDemo
// re-emits both with char ids remapped onto the appearance-order SC.
func TestDemoImportReciprocalPairing(t *testing.T) {
	apollo := &protocol.ChatMessage{
		CharName: "Apollo", Emote: "normal", Message: "gotcha", Side: "def", CharID: 452,
		Pair: protocol.PairInfo{CharID: 12, Order: protocol.PairSpeakerInFront, HasOrder: true, Name: "Klavier", Emote: "stand", OffsetX: 15, OffsetY: 0},
	}
	klavier := &protocol.ChatMessage{
		CharName: "Klavier", Emote: "normal", Message: "achtung", Side: "pro", CharID: 108,
		Pair: protocol.PairInfo{CharID: 452, Order: protocol.PairSpeakerBehind, HasOrder: true, Name: "Apollo", Emote: "stand", OffsetX: -11, OffsetY: 0},
	}
	demo := strings.Join([]string{
		"SC#Apollo#Klavier#%",
		protocol.BuildServerMS(apollo).String(),
		protocol.BuildServerMS(klavier).String(),
	}, "\n")

	rec, skipped, truncated, err := demoToRecording([]byte(demo), "o")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || truncated != 0 { // just the SC
		t.Fatalf("skipped/truncated = %d/%d, want 1/0", skipped, truncated)
	}
	if len(rec.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(rec.Events))
	}
	// Pair fields survive parse verbatim (CharID/Order/OffsetX/OffsetY/Flip).
	p0, p1 := rec.Events[0].Message.Pair, rec.Events[1].Message.Pair
	if p0.CharID != 12 || p0.Order != protocol.PairSpeakerInFront || !p0.HasOrder || p0.Name != "Klavier" || p0.OffsetX != 15 || p0.OffsetY != 0 || p0.Flip {
		t.Errorf("apollo pair parsed wrong: %+v", p0)
	}
	if p1.CharID != 452 || p1.Order != protocol.PairSpeakerBehind || !p1.HasOrder || p1.Name != "Apollo" || p1.OffsetX != -11 || p1.OffsetY != 0 {
		t.Errorf("klavier pair parsed wrong: %+v", p1)
	}

	// Re-export remaps ids onto the appearance-order SC: event 0 adopts speaker
	// "Apollo" (0) then partner "Klavier" (1); event 1's names already resolved.
	out, err := recordingToDemo(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "SC#Apollo#Klavier#%") {
		t.Fatalf("synthetic SC wrong: %q", strings.SplitN(string(out), "\n", 2)[0])
	}
	round, _, _, err := demoToRecording(out, "o")
	if err != nil {
		t.Fatal(err)
	}
	m0, m1 := round.Events[0].Message, round.Events[1].Message
	if m0.CharID != 0 || m0.Pair.CharID != 1 {
		t.Errorf("apollo remap = self %d / pair %d, want 0/1", m0.CharID, m0.Pair.CharID)
	}
	if m1.CharID != 1 || m1.Pair.CharID != 0 {
		t.Errorf("klavier remap = self %d / pair %d, want 1/0", m1.CharID, m1.Pair.CharID)
	}
	// Order/offsets ride the remap unchanged (only ids are rewritten).
	if m0.Pair.Order != protocol.PairSpeakerInFront || m0.Pair.OffsetX != 15 {
		t.Errorf("apollo pair order/offset lost on re-export: %+v", m0.Pair)
	}
	if m1.Pair.Order != protocol.PairSpeakerBehind || m1.Pair.OffsetX != -11 {
		t.Errorf("klavier pair order/offset lost on re-export: %+v", m1.Pair)
	}
}

// TestDemoImportLargeRoster pins that a multi-kilobyte SC roster (600+ names, one
// line — File B's shape) imports fine: the SC counts once in skipped, and the MS
// lines around it still convert. A big roster must not choke or double-count.
func TestDemoImportLargeRoster(t *testing.T) {
	const rosterSize = 640
	names := make([]string, rosterSize)
	for i := range names {
		names[i] = fmt.Sprintf("Char%03d", i)
	}
	sc := "SC#" + strings.Join(names, "#") + "#%"
	if len(sc) < 4096 {
		t.Fatalf("roster should be several KB, got %d bytes", len(sc))
	}
	demo := strings.Join([]string{
		sc,
		demoMS("Char000", "hello", 0),
		demoMS("Char500", "world", 500),
	}, "\n")

	rec, skipped, truncated, err := demoToRecording([]byte(demo), "o")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 { // the SC, once
		t.Errorf("skipped = %d, want 1 (the SC, counted once)", skipped)
	}
	if truncated != 0 {
		t.Errorf("truncated = %d, want 0", truncated)
	}
	if len(rec.Events) != 2 || rec.Events[0].Message.Message != "hello" || rec.Events[1].Message.Message != "world" {
		t.Fatalf("MS around the roster didn't import: %+v", rec.Events)
	}
}

// TestDemoImportSkipCountReported pins the skipped tally: a demo mixing every
// non-scene header AO2 emits (HP/LE/TI/CT/MM/CU/JD/PV/SP/TT) plus a couple valid
// MS reports skipped == the count of non-MS/BN/MC packets, truncated == 0.
func TestDemoImportSkipCountReported(t *testing.T) {
	nonScene := []string{
		"HP#1#5#%",
		"LE#evidence&desc&image.png#%",
		"TI#0#1#0#%",
		"CT#server#hello there#%",
		"MM#1#%",
		"CU#0#%",
		"JD#3#%",
		"PV#0#pos#wit#%",
		"SP#pos#%",
		"TT#timer text#%",
	}
	lines := append([]string{}, nonScene...)
	lines = append(lines, demoMS("Phoenix", "one", 0), demoMS("Phoenix", "two", 0))
	demo := strings.Join(lines, "\n")

	rec, skipped, truncated, err := demoToRecording([]byte(demo), "o")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != len(nonScene) {
		t.Errorf("skipped = %d, want %d (each non-scene header once)", skipped, len(nonScene))
	}
	if truncated != 0 {
		t.Errorf("truncated = %d, want 0", truncated)
	}
	if len(rec.Events) != 2 {
		t.Fatalf("events = %d, want 2 (both MS)", len(rec.Events))
	}
}

// TestDemoImportMultilinePacket pins parseDemoRecords' join: a CT MOTD with raw
// embedded newlines (File B's shape) counts as ONE skipped record, and the MS
// lines after it all import — the '%'-terminated join must not shatter the block.
func TestDemoImportMultilinePacket(t *testing.T) {
	// A CT packet whose message field carries literal newlines: only the final
	// physical line ends with '%', so the loader joins the whole block into one.
	motd := "CT#server#Welcome to the server.\nRules:\n1. Be nice.\n2. Have fun.#0#%"
	demo := strings.Join([]string{
		"SC#Phoenix#%",
		motd,
		demoMS("Phoenix", "one", 0),
		demoMS("Phoenix", "two", 0),
	}, "\n")

	rec, skipped, truncated, err := demoToRecording([]byte(demo), "o")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 2 { // the SC and the one joined CT block
		t.Errorf("skipped = %d, want 2 (SC + the single joined CT block)", skipped)
	}
	if truncated != 0 {
		t.Errorf("truncated = %d, want 0", truncated)
	}
	if len(rec.Events) != 2 {
		t.Fatalf("events = %d, want 2 (both MS after the multiline CT)", len(rec.Events))
	}
}

// TestNextRecordingDest pins the collision-safe destination walk both Studio
// entry points share (the drop route and the native picker): the free name is
// taken verbatim, then "-2", "-3", … as siblings already exist.
func TestNextRecordingDest(t *testing.T) {
	dir := t.TempDir()
	const base, ext = "scene", demoExt

	// Free: the plain base+ext.
	if got, want := nextRecordingDest(dir, base, ext), filepath.Join(dir, base+ext); got != want {
		t.Fatalf("free dest = %q, want %q", got, want)
	}

	// Occupy base+ext → next is base-2+ext.
	if err := os.WriteFile(filepath.Join(dir, base+ext), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := nextRecordingDest(dir, base, ext), filepath.Join(dir, base+"-2"+ext); got != want {
		t.Fatalf("first collision = %q, want %q", got, want)
	}

	// Occupy base-2+ext too → next is base-3+ext.
	if err := os.WriteFile(filepath.Join(dir, base+"-2"+ext), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := nextRecordingDest(dir, base, ext), filepath.Join(dir, base+"-3"+ext); got != want {
		t.Fatalf("second collision = %q, want %q", got, want)
	}
}

// TestDemoDefaultOriginPolicy pins the source-selection default matrix: a .demo
// import resolves against the LOCAL BASE when local-asset mounts are configured
// (AO2 parity), and otherwise against the current URL builder's origin (the live
// session when connected, "" offline). The full 2×2 (mounts present/absent ×
// connected/not) is walked.
func TestDemoDefaultOriginPolicy(t *testing.T) {
	const sessionOrigin = "https://cdn.example/base/"
	mount := t.TempDir()
	// The mount origin is deterministic from the mount set — compute the expected
	// value the SAME way demoDefaultOrigin does.
	wantMount := assets.NewLocalFetcher([]string{mount}).BaseURL()

	cases := []struct {
		name      string
		mounts    bool
		connected bool
		want      string
	}{
		{"mounts + connected → local base wins", true, true, wantMount},
		{"mounts + offline → local base", true, false, wantMount},
		{"no mounts + connected → session origin", false, true, sessionOrigin},
		{"no mounts + offline → empty (warn path)", false, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testTabApp(t)
			if tc.mounts {
				a.d.Prefs.SetLocalAssets(true, []string{mount})
			}
			if tc.connected {
				a.urls = courtroom.NewURLBuilder(sessionOrigin)
			}
			got := a.demoDefaultOrigin()
			// The connected non-local case normalizes the trailing slash via NewURLBuilder;
			// compare on the host-bearing prefix so the trailing-slash rule doesn't flake.
			if tc.want == "" {
				if got != "" {
					t.Errorf("demoDefaultOrigin = %q, want empty (offline, no mounts → warn path)", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("demoDefaultOrigin = %q, want %q", got, tc.want)
			}
			if tc.mounts && !strings.HasPrefix(got, assets.LocalScheme) {
				t.Errorf("with mounts configured the origin must be a local:// origin, got %q", got)
			}
		})
	}
}

// TestDemoImportUnderMountsWarnsPositively pins that importing a .demo when mounts
// are configured (a) resolves against the local base (a local:// origin) and (b)
// does NOT fire the empty-origin "won't stream" warning — instead it gets the
// positive "resolving from your local assets" note. This is the end-to-end proof
// that the empty-origin warning is suppressed when mounts cover the demo.
func TestDemoImportUnderMountsWarnsPositively(t *testing.T) {
	a := testTabApp(t)
	mount := t.TempDir()
	a.d.Prefs.SetLocalAssets(true, []string{mount})

	demo := strings.Join([]string{
		"SC#Phoenix#%",
		demoMS("Phoenix", "hold it", 0),
	}, "\n")
	path := filepath.Join(t.TempDir(), "scene.demo")
	if err := os.WriteFile(path, []byte(demo), 0o644); err != nil {
		t.Fatal(err)
	}

	rec, err := a.loadRecordingAny(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.Origin, assets.LocalScheme) {
		t.Errorf("a .demo under mounts must resolve against the local base, got Origin=%q", rec.Origin)
	}
	// The positive note, not the empty-origin warning.
	if strings.Contains(a.warnLine, "won't stream") {
		t.Errorf("the empty-origin warning must be SUPPRESSED when mounts cover the demo; got %q", a.warnLine)
	}
	if !strings.Contains(a.warnLine, "local assets") {
		t.Errorf("expected the positive local-assets note, got %q", a.warnLine)
	}
}

// TestDemoImportNoMountsNoSessionWarns pins the unchanged offline path: a .demo
// imported with no mounts AND no session gets an empty origin and the honest
// empty-origin warning (the v1.72.0 behavior — not regressed by the policy).
func TestDemoImportNoMountsNoSessionWarns(t *testing.T) {
	a := testTabApp(t)
	demo := strings.Join([]string{
		"SC#Phoenix#%",
		demoMS("Phoenix", "hold it", 0),
	}, "\n")
	path := filepath.Join(t.TempDir(), "scene.demo")
	if err := os.WriteFile(path, []byte(demo), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := a.loadRecordingAny(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Origin != "" {
		t.Errorf("offline import with no mounts must have empty Origin, got %q", rec.Origin)
	}
	if !strings.Contains(a.warnLine, "won't stream") {
		t.Errorf("offline no-mount import must fire the empty-origin warning; got %q", a.warnLine)
	}
}

// TestDemoImportRealFixtures is an env-gated acceptance run against real AO2
// .demo files (set ASYNCAO_REAL_DEMO_DIR to a folder of them). No fixture content
// is embedded — this stays committable. Each file must import without error, stay
// within the cap, and its counts are logged for the record.
func TestDemoImportRealFixtures(t *testing.T) {
	dir := os.Getenv("ASYNCAO_REAL_DEMO_DIR")
	if dir == "" {
		t.Skip("set ASYNCAO_REAL_DEMO_DIR to a folder of .demo files to run this acceptance test")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*"+demoExt))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Skipf("no %s files in %s", demoExt, dir)
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rec, skipped, truncated, err := demoToRecording(data, "o")
		if err != nil {
			t.Fatalf("%s: import errored: %v", filepath.Base(path), err)
		}
		if len(rec.Events) > maxRecordedEvents {
			t.Errorf("%s: events = %d, exceeds the cap %d", filepath.Base(path), len(rec.Events), maxRecordedEvents)
		}
		t.Logf("%s: events=%d skipped=%d truncated=%d", filepath.Base(path), len(rec.Events), skipped, truncated)
	}
}
