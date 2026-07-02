package ui

import (
	"strings"
	"testing"

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
	rec, skipped, err := demoToRecording([]byte(demo), "https://cdn.example/base/")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 2 { // SC + CT
		t.Errorf("skipped = %d, want 2 (SC + CT)", skipped)
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

	back, skipped, err := demoToRecording(data, "o")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 { // just the SC
		t.Errorf("skipped = %d, want 1", skipped)
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
