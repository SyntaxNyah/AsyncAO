package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// The automatic roster poll is DELETED (user order, 2026-08-08, after a live session on
// an old KFO hub answered every tick with "You cannot see players in all areas in this
// hub!"). These tests are the fence around that decision: no server push may send a
// roster command, the pulls a PERSON asks for still work and still show their reply, and
// the deletion cannot rot back into a timer without something turning red.

// rosterPollProbeWindow is comfortably longer than the deleted poll's 3 s debounce, so a
// reintroduced timer of anything like the old cadence would fire inside it many times.
const rosterPollProbeWindow = 30 * time.Second

// TestNoSessionEverFiresATimedRosterPoll floods the client with exactly the pushed
// packets that used to drive the poll — CharsCheck, ARUP and PU, i.e. what a busy hub
// emits constantly — right across the old poll window, and asserts nothing is sent back.
func TestNoSessionEverFiresATimedRosterPoll(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(a *App)
	}{
		{"no PR/PU (the old KFO hub), plain user", func(*App) {}},
		{"no PR/PU (the old KFO hub), logged-in mod", func(a *App) { a.link.sess.ModGranted = true }},
		{
			// The nastiest historical case: the mod IPID top-up looked self-limiting
			// (`ModGranted && liveRosterMissingIPID()`) but never settled on a server
			// whose roster reply carries no IPIDs, so it re-sent /gas every 3 s for the
			// whole session.
			"live roster whose rows never carry an IPID, logged-in mod",
			func(a *App) {
				a.link.sess.ModGranted = true
				a.roster.livePlayersOn = true
				a.roster.liveRoster = []areaPlayer{{uid: "1", name: "Phoenix", area: "Courtroom"}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testTabApp(t)
			a.link.sess = courtroom.NewRehearsalSession("", []string{"Phoenix", "Edgeworth"})
			a.link.sess.Areas = []string{"Courtroom"}
			tc.setup(a)
			t0 := time.Now()

			const step = 250 * time.Millisecond
			for elapsed := time.Duration(0); elapsed < rosterPollProbeWindow; elapsed += step {
				a.budget.frameNow = t0.Add(elapsed)
				a.handleSessionEvents([]courtroom.Event{
					{Kind: courtroom.EventCharsUpdated},
					{Kind: courtroom.EventAreasUpdated},
					{Kind: courtroom.EventPlayersUpdated},
				})
				a.processOOCQueue() // drain, so a queued command cannot hide behind the pacer
			}
			for _, q := range a.oocQueue {
				t.Fatalf("a server push queued the OOC line %q with no user action — the automatic roster "+
					"poll is deleted (liveroster.go); nothing may send one but a button", q.line)
			}
		})
	}
}

// TestAManualRosterFetchStillWorksAndShowsItsReply pins what SURVIVED: a person asking
// still sends the command, the roster body it asked for is still kept out of chat, a
// server that refuses now SAYS SO instead of being silently latched off, and repeated
// presses still collapse to one command.
func TestAManualRosterFetchStillWorksAndShowsItsReply(t *testing.T) {
	a := testTabApp(t)
	a.link.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	a.budget.frameNow = time.Now()

	a.fetchRoster()
	if len(a.oocQueue) != 1 || a.oocQueue[0].line != rosterCmd {
		t.Fatalf("a manual fetch must queue %q, got %+v", rosterCmd, a.oocQueue)
	}

	// The reply burst it asked for is parsed but suppressed (areaEchoSuppressWindow).
	before := len(a.chatlog.oocLog)
	a.pushOOC("Server: [0] Phoenix Wright (1001) [Def]", "Server") // a real roster row (looksLikeAreaList)
	if len(a.chatlog.oocLog) != before {
		t.Error("the roster body we asked for must stay out of the OOC log")
	}

	// A refusal is NOT suppressed any more: it is the answer to a press, it can only
	// appear once per press, and hiding it makes the control look broken.
	before = len(a.chatlog.oocLog)
	a.pushOOC("Server: You cannot see players in all areas in this hub!", "Server")
	if len(a.chatlog.oocLog) == before {
		t.Error("a server's refusal must reach the OOC log now that nothing sends the command on its own")
	}

	// Three presses before the drain runs are still one command on the wire.
	a.fetchRoster()
	a.fetchRoster()
	if len(a.oocQueue) != 1 {
		t.Errorf("three presses queued %d commands, want 1", len(a.oocQueue))
	}
}
