package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
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
		{"no PR/PU (the old KFO hub), logged-in mod", func(a *App) { a.sess.ModGranted = true }},
		{
			// The nastiest historical case: the mod IPID top-up looked self-limiting
			// (`ModGranted && liveRosterMissingIPID()`) but never settled on a server
			// whose roster reply carries no IPIDs, so it re-sent /gas every 3 s for the
			// whole session.
			"live roster whose rows never carry an IPID, logged-in mod",
			func(a *App) {
				a.sess.ModGranted = true
				a.livePlayersOn = true
				a.liveRoster = []areaPlayer{{uid: "1", name: "Phoenix", area: "Courtroom"}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testTabApp(t)
			a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix", "Edgeworth"})
			a.sess.Areas = []string{"Courtroom"}
			tc.setup(a)
			t0 := time.Now()

			const step = 250 * time.Millisecond
			for elapsed := time.Duration(0); elapsed < rosterPollProbeWindow; elapsed += step {
				a.frameNow = t0.Add(elapsed)
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
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	a.frameNow = time.Now()

	a.fetchRoster()
	if len(a.oocQueue) != 1 || a.oocQueue[0].line != rosterCmd {
		t.Fatalf("a manual fetch must queue %q, got %+v", rosterCmd, a.oocQueue)
	}

	// The reply burst it asked for is parsed but suppressed (areaEchoSuppressWindow).
	before := len(a.oocLog)
	a.pushOOC("Server: [0] Phoenix Wright (1001) [Def]", "Server") // a real roster row (looksLikeAreaList)
	if len(a.oocLog) != before {
		t.Error("the roster body we asked for must stay out of the OOC log")
	}

	// A refusal is NOT suppressed any more: it is the answer to a press, it can only
	// appear once per press, and hiding it makes the control look broken.
	before = len(a.oocLog)
	a.pushOOC("Server: You cannot see players in all areas in this hub!", "Server")
	if len(a.oocLog) == before {
		t.Error("a server's refusal must reach the OOC log now that nothing sends the command on its own")
	}

	// Three presses before the drain runs are still one command on the wire.
	a.fetchRoster()
	a.fetchRoster()
	if len(a.oocQueue) != 1 {
		t.Errorf("three presses queued %d commands, want 1", len(a.oocQueue))
	}
}

// rosterFetchSite is one file allowed to start a roster pull, and why. Each entry IS the
// audit: every survivor is a person acting, never a timer and never a server packet.
type rosterFetchSite struct {
	file string
	why  string
}

var rosterFetchSites = []rosterFetchSite{
	{"liveroster.go", "fetchRoster's own definition"},
	{"playerlist.go", "the Players panel's on-open / area-change pull — the user opened it, or moved"},
	{"app.go", "the one-shot after a successful /login (EventAuth ≥ 1): the user typed the command, and it fires once per auth transition, not on a clock"},
}

// TestOnlyUserActionsFetchTheRoster is the deletion catcher. Growing the poll back means
// adding a fetchRoster call somewhere, and every legal site is named above with the
// reason it is user-driven; an unlisted one fails, and a listed one that vanishes fails
// too — so the census cannot quietly stop covering anything (losing the Players-panel
// pull would leave a PR/PU-less server with no way to refresh its roster at all).
func TestOnlyUserActionsFetchTheRoster(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	seen := make([]bool, len(rosterFetchSites))
	var offenders []string
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			base := path
			if i := strings.LastIndexAny(base, `/\`); i >= 0 {
				base = base[i+1:]
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.FuncDecl:
					if v.Name.Name == "fetchRoster" {
						if i := rosterSiteIndex(base); i >= 0 {
							seen[i] = true
						} else {
							offenders = append(offenders, base+" defines fetchRoster")
						}
					}
				case *ast.CallExpr:
					sel, ok := v.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "fetchRoster" {
						return true
					}
					if i := rosterSiteIndex(base); i >= 0 {
						seen[i] = true
						return true
					}
					offenders = append(offenders, base+" calls fetchRoster")
				}
				return true
			})
		}
	}
	if len(offenders) > 0 {
		t.Errorf("roster pull started from unlisted file(s):\n  %s\n\n"+
			"The automatic roster poll was deleted by user order (liveroster.go): a roster command may only "+
			"leave this client because a PERSON asked for it. If this really is a user action, add the file to "+
			"rosterFetchSites with the reason; if it is a packet handler or a timer, it is the poll growing back "+
			"— and with it the v1.82 OOC rate-limit kick class.", strings.Join(offenders, "\n  "))
	}
	for i := range rosterFetchSites {
		if !seen[i] {
			t.Errorf("rosterFetchSites lists %q (%s) but nothing there fetches any more — either it moved "+
				"(point the census at it) or the control is gone", rosterFetchSites[i].file, rosterFetchSites[i].why)
		}
	}
}

func rosterSiteIndex(base string) int {
	for i := range rosterFetchSites {
		if rosterFetchSites[i].file == base {
			return i
		}
	}
	return -1
}
