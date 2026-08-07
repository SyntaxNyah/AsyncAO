package ui

// F4, the LIFETIME half — see translogsplit_test.go for the identity half.
//
// Splitting the transcript per tab session gave every reconnect a brand-new writer
// key. Nothing retired the old one: the map was drained only by CloseTranscript, at
// process exit. So each park / Disconnect / reconnect left an open file and a live
// goroutine behind, and the 64th cycle hit transcriptWriterCap — after which
// transcriptFor returned nil and the opt-in detailed log silently stopped for the
// rest of the run, with nothing on screen to say so.
//
// The rule now is ownership: a writer lives exactly as long as some session — the
// live one or a parked tab's — can still write to it. reapTranscripts asks that
// question rather than trusting a list of teardown sites to stay complete.

import (
	"testing"
)

// transcriptTestApp is a headless App with detailed logging on. It skips the whole
// test where the writers cannot be opened at all (no writable directory beside the
// test binary), exactly as the split test does.
func transcriptTestApp(t *testing.T, server string) *App {
	t.Helper()
	a := testTabApp(t)
	a.d.Prefs.SetDetailedLog(true)
	a.serverName = server
	t.Cleanup(a.CloseTranscript)
	if a.transcriptFor(server, a.logSession) == nil {
		t.Skip("transcript writers unavailable in this environment (no writable exe dir)")
	}
	return a
}

// TestReconnectCyclesDoNotStrandTranscriptWriters is the regression itself. Far more
// reconnects than the cap, and the log must still be being written at the end.
//
// The two assertions are deliberately different in kind. The map size proves the
// files and goroutines are actually released; the final non-nil writer proves the
// USER-VISIBLE symptom is gone — before the reap, cycle 65 and every one after it
// got nil back and simply stopped transcribing.
func TestReconnectCyclesDoNotStrandTranscriptWriters(t *testing.T) {
	const server = "Skrapegropen"
	a := transcriptTestApp(t, server)

	for i := 0; i < transcriptWriterCap*2; i++ {
		w := a.transcriptFor(server, a.logSession)
		if w == nil {
			t.Fatalf("reconnect %d got no transcript writer — detailed logging has silently stopped for the rest of the run (F4)", i)
		}
		w.write("cycle")
		// A disconnect/reconnect: the live session ends and a fresh one is minted.
		a.resetSessionState()
		a.serverName = server
	}
	if n := len(a.translogs); n > 1 {
		t.Errorf("%d transcript writers still open after %d reconnects, want at most the live one — "+
			"each stranded entry is an open file AND a goroutine (F4, hard rule §4)", n, transcriptWriterCap*2)
	}
}

// TestAParkedTabKeepsItsTranscriptAndAClosedTabLosesIt is the ownership rule from
// both sides. Over-reaping is as bad as never reaping: a backgrounded tab is still
// being written to by the parked-tab message seam, so closing its file would drop
// every line it received while it was in the background.
func TestAParkedTabKeepsItsTranscriptAndAClosedTabLosesIt(t *testing.T) {
	const server = "Skrapegropen"
	a := transcriptTestApp(t, server)

	// Tab one logs, then parks; tab two becomes live and logs too.
	parked := a.logSession
	a.tabs = append(a.tabs, &courtTab{})
	a.activeTab = 0
	a.parkActive() // copies the session into the slot, then resets — and reaps
	a.serverName = server
	live := a.logSession
	if a.transcriptFor(server, live) == nil {
		t.Fatal("the new live session got no writer")
	}

	if _, ok := a.translogs[translogKey{server: server, session: parked}]; !ok {
		t.Fatal("parking a tab retired its transcript — the background message seam still files that tab's lines, " +
			"so they would all be dropped (F4)")
	}
	if _, ok := a.translogs[translogKey{server: server, session: live}]; !ok {
		t.Fatal("the LIVE session's own writer was reaped")
	}

	// Now close the parked tab. Its session is gone, so its writer must go too —
	// and the live one must survive.
	a.closeParkedTab(0)
	if _, ok := a.translogs[translogKey{server: server, session: parked}]; ok {
		t.Error("closing a tab left its transcript writer open — one leaked file + goroutine per closed tab (F4)")
	}
	if _, ok := a.translogs[translogKey{server: server, session: live}]; !ok {
		t.Error("closing a background tab reaped the LIVE session's writer — the visible tab would stop transcribing")
	}
}

// TestRetiredWritersCannotBePanickedIntoAShutdown is the seam's robustness contract.
// retire closes the writer's channel, so a second retire, a shutdown drain over the
// same writer, or a line that raced the teardown would each panic on a naive
// implementation — and all three are reachable (a reap and CloseTranscript can see
// the same writer; the message seam holds a pointer it just fetched).
func TestRetiredWritersCannotBePanickedIntoAShutdown(t *testing.T) {
	const server = "Skrapegropen"
	a := transcriptTestApp(t, server)
	w := a.transcriptFor(server, a.logSession)
	if w == nil {
		t.Fatal("no writer to retire")
	}
	w.write("before")
	w.retire()
	// Idempotent retire; an inert write rather than a send on a closed channel; and
	// the shutdown drain arriving after the reap already retired the same writer.
	w.retire()
	w.write("after")
	w.close()
	w.close()

	// And nil-safety, which the shutdown path relies on.
	var none *transcriptWriter
	none.write("x")
	none.retire()
	none.close()
}

// TestEveryTranscriptTeardownEdgeReaps is the deletion-catcher. The reap is one call
// in each of the four places a session can end, and every one of them is silent when
// it goes missing: the log just stops, dozens of reconnects later, on a build nobody
// is watching a file handle count on. A source gate is the only thing that fails
// loudly the moment one is deleted.
func TestEveryTranscriptTeardownEdgeReaps(t *testing.T) {
	for _, site := range []struct{ file, fn, why string }{
		{"tabs.go", "resetSessionState", "a park / Disconnect / reconnect ends the live session"},
		{"tabs.go", "closeParkedTab", "a background tab's ✕ ends that tab's session"},
		{"tabs.go", "closeActiveTab", "the live slot is dropped (Disconnect, failed dial)"},
		{"tabs.go", "allocateTab", "a dead tab is reaped to make room for a new one"},
	} {
		if !containsCall(funcBodySource(t, site.file, site.fn), "reapTranscripts") {
			t.Errorf("%s no longer reaps transcripts — %s, and its file + goroutine would be stranded until the "+
				"cap silently ended detailed logging for the run (F4)", site.fn, site.why)
		}
	}
	// CloseTranscript is the shutdown drain and the ONLY place allowed to wait on a
	// disk flush; the mid-run path must not, or a tab close blocks the render thread
	// (hard rule §2).
	if containsCall(funcBodySource(t, "app.go", "reapTranscripts"), "close") {
		t.Error("reapTranscripts calls close() — that WAITS for the file flush on the render thread; it must retire() instead")
	}
	if !containsCall(funcBodySource(t, "app.go", "CloseTranscript"), "close") {
		t.Error("CloseTranscript no longer waits for the flush — the last lines of every log would be lost at exit")
	}
}
