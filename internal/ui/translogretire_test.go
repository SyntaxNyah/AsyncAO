package ui

// F4, the LIFETIME half — see translogsplit_test.go for the identity half.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"testing"
)

// transcriptTestApp is a headless App with detailed logging on. It skips the whole
// test where the writers cannot be opened at all (no writable directory beside the
// test binary), exactly as the split test does.
func transcriptTestApp(t *testing.T, server string) *App {
	t.Helper()
	withExeDataRoot(t)
	a := testTabApp(t)
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			DetailedLog =

			true

	})
	a.srvMeta.serverName = server
	t.Cleanup(a.CloseTranscript)
	if a.transcriptFor(server, a.srvMeta.logSession) == nil {
		t.Skip("transcript writers unavailable in this environment (no writable exe dir)")
	}
	return a
}

// TestReconnectCyclesDoNotStrandTranscriptWriters is the regression itself. Far more
// reconnects than the cap, and the log must still be being written at the end.
func TestReconnectCyclesDoNotStrandTranscriptWriters(t *testing.T) {
	const server = "Skrapegropen"
	a := transcriptTestApp(t, server)

	for i := 0; i < transcriptWriterCap*2; i++ {
		w := a.transcriptFor(server, a.srvMeta.logSession)
		if w == nil {
			t.Fatalf("reconnect %d got no transcript writer — detailed logging has silently stopped for the rest of the run (F4)", i)
		}
		w.write("cycle")
		// A disconnect/reconnect: the live session ends and a fresh one is minted.
		a.resetSessionState()
		a.srvMeta.serverName = server
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
	parked := a.srvMeta.logSession
	a.tabs = append(a.tabs, &courtTab{})
	a.activeTab = 0
	a.parkActive() // copies the session into the slot, then resets — and reaps
	a.srvMeta.serverName = server
	live := a.srvMeta.logSession
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
func TestRetiredWritersCannotBePanickedIntoAShutdown(t *testing.T) {
	const server = "Skrapegropen"
	a := transcriptTestApp(t, server)
	w := a.transcriptFor(server, a.srvMeta.logSession)
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
