package ui

// F4 — multiple tabs writing ONE transcript.
//
// The writers were keyed by server NAME. Two tabs on the same server therefore
// resolved to the same writer and interleaved into a single file, with nothing in
// the file to tell the two sessions apart afterwards. The key is now (server, tab
// session), the session id lives on sessionState so it follows its tab through
// park/activate, and the file name carries a per-server ordinal so two tabs joining
// inside the same second cannot be handed the same path.

import (
	"go/ast"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestTwoTabsOnOneServerGetTwoLogSessions pins the identity half: a fresh live slot
// always gets an id no other tab holds, and park/activate MOVE that id with the tab
// rather than re-minting it (a backgrounded tab that came back must keep appending
// to its own file, not start a third).
func TestTwoTabsOnOneServerGetTwoLogSessions(t *testing.T) {
	a := testTabApp(t)
	const server = "Skrapegropen"

	first := a.logSession
	if first == 0 {
		t.Fatal("resetSessionState left logSession at the zero value — every tab would share writer key 0")
	}
	a.serverName = server
	// A live session on the slot, the headless pattern the other tab-lifecycle tests
	// use. Without it activateTab takes its "died while backgrounded" arm (a.sess ==
	// nil) and Disconnects instead of restoring — which would make the id assertion
	// below measure the teardown path rather than the park/activate hand-off.
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})

	// Park tab one and open tab two on the SAME server.
	a.tabs = append(a.tabs, &courtTab{})
	a.activeTab = 0
	a.parkActive()
	second := a.logSession
	if second == first {
		t.Fatalf("a fresh live slot reused log session %d — two tabs on one server would share a transcript file (F4)", first)
	}
	if got := a.tabs[0].state.logSession; got != first {
		t.Errorf("the parked tab's log session is %d, want %d — the id must ride the session state into the tab", got, first)
	}
	a.serverName = server

	// Both sessions log; each must get its OWN writer.
	a.d.Prefs.SetDetailedLog(true)
	if w1, w2 := a.transcriptFor(server, first), a.transcriptFor(server, second); w1 == nil || w2 == nil {
		t.Skip("transcript writers unavailable in this environment (no writable exe dir)")
	} else if w1 == w2 {
		t.Error("two tab sessions on one server resolved to the SAME writer — this is the reported defect (F4)")
	}
	defer a.CloseTranscript()

	// And the same session asks twice and gets the same writer back (one file per
	// tab, not one per message).
	if a.transcriptFor(server, first) != a.transcriptFor(server, first) {
		t.Error("the same tab session opened two writers — it would fragment its own log")
	}

	// Bringing tab one back must NOT mint a third id.
	a.activateTab(0)
	if a.logSession != first {
		t.Errorf("activating a parked tab changed its log session to %d, want %d — it would abandon its own file mid-session", a.logSession, first)
	}
}

// TestTranscriptFilenameDisambiguatesWithinASecond pins the name rule. The stamp
// resolves to the second, so two tabs joining one server at the same moment would
// otherwise be handed an identical path and append into each other — defeating the
// per-tab key entirely. The FIRST file keeps the historical bare-timestamp name, so
// every log already on disk still parses.
func TestTranscriptFilenameDisambiguatesWithinASecond(t *testing.T) {
	now := time.Date(2026, 8, 7, 15, 4, 5, 0, time.UTC)
	first := transcriptFilename(now, 1)
	if first != "2026-08-07_15-04-05.log" {
		t.Errorf("the first file of a run must keep the historical name, got %q", first)
	}
	if got := transcriptFilename(now, 0); got != first {
		t.Errorf("a zero/absent ordinal must behave like the first file, got %q", got)
	}
	seen := map[string]bool{first: true}
	for seq := 2; seq <= 5; seq++ {
		name := transcriptFilename(now, seq)
		if seen[name] {
			t.Fatalf("ordinal %d produced a duplicate name %q — two tabs would share a file", seq, name)
		}
		seen[name] = true
		// The browser has to be able to read every one of them back.
		if label := sessionLabel(name); label == name {
			t.Errorf("sessionLabel(%q) fell through to the raw file name — the log browser would show a per-tab log as a corrupt entry", name)
		}
	}
}

// TestSessionLabelReadsBothNameShapes is the round-trip between the writer and the
// browser. They are the only two places that know the file-name format, and they
// share transcriptStampLayout precisely so they cannot drift.
func TestSessionLabelReadsBothNameShapes(t *testing.T) {
	now := time.Date(2026, 8, 7, 15, 4, 5, 0, time.UTC)
	if got, want := sessionLabel(transcriptFilename(now, 1)), "2026-08-07 15:04"; got != want {
		t.Errorf("sessionLabel(first) = %q, want %q", got, want)
	}
	if got, want := sessionLabel(transcriptFilename(now, 3)), "2026-08-07 15:04 (tab 3)"; got != want {
		t.Errorf("sessionLabel(third) = %q, want %q", got, want)
	}
	// A name that is neither shape still degrades to the stem, as it always did.
	if got, want := sessionLabel("notes.log"), "notes"; got != want {
		t.Errorf("sessionLabel(unrecognised) = %q, want %q", got, want)
	}
	// A server folder full of hyphens must not be mistaken for an ordinal.
	if got := sessionLabel("my-session.log"); got != "my-session" {
		t.Errorf("sessionLabel(%q) = %q — a non-numeric tail must not parse as a tab ordinal", "my-session.log", got)
	}
}

// TestBothMessageSeamsFileUnderTheirOwnTab is the wiring catcher. There are two
// call sites — the live tab's and the parked tab's — and the parked one runs while a
// DIFFERENT session is live, so a site that read the live session instead of its own
// would file every backgrounded tab's lines under the foreground tab's log. That is
// invisible to a unit test of logDetailed, and it is exactly the mistake the old
// single-argument signature invited.
func TestBothMessageSeamsFileUnderTheirOwnTab(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob the package: %v", err)
	}
	seams := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset, f := parsedFile(t, name)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || callName(call) != "logDetailed" {
				return true
			}
			seams++
			if len(call.Args) < 3 {
				t.Errorf("%s:%d — logDetailed must be handed the CALLING tab's session id", name, fset.Position(call.Pos()).Line)
				return true
			}
			sel, ok := call.Args[1].(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "logSession" {
				t.Errorf("%s:%d — logDetailed's session argument is not a .logSession: the background seam runs while a DIFFERENT tab is live, "+
					"so anything but the calling session's own id files a parked tab's lines under the foreground tab's log (F4)",
					name, fset.Position(call.Pos()).Line)
			}
			return true
		})
	}
	if seams < 2 {
		t.Fatalf("found %d logDetailed call sites, want both (the live tab's and the parked tab's) — one of the message seams stopped transcribing", seams)
	}
}
