package courtroom

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestOnlyEndPreanimReleasesTheOneShot is the encapsulation gate for the
// preanim-end seam: the PlayOnce latch may be CLEARED in exactly one place, the
// endPreanim funnel. Bypassing it — a fifth `PlayOnce = false` written straight
// into a phase handler — compiles fine and looks harmless, and that is precisely
// how the release drifted into four unreported sites in the first place. This
// makes the bypass fail loudly instead.
//
// It parses the package's own source rather than mirroring the rule in Go: a
// mirror would re-implement the funnel and pass no matter what the production
// code does (CLAUDE.md §11 on mirror-tests). Test files are excluded — a test
// may stage any SpriteLayer it likes.
func TestOnlyEndPreanimReleasesTheOneShot(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	type site struct {
		file string
		fn   string
	}
	var sites []site
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			fn := "<file scope>"
			ast.Inspect(file, func(n ast.Node) bool {
				if d, ok := n.(*ast.FuncDecl); ok {
					fn = d.Name.Name
					return true
				}
				assign, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, lhs := range assign.Lhs {
					if !isPlayOnceSelector(lhs) || i >= len(assign.Rhs) {
						continue
					}
					if id, ok := assign.Rhs[i].(*ast.Ident); ok && id.Name == "false" {
						sites = append(sites, site{file: shortName(name), fn: fn})
					}
				}
				return true
			})
		}
	}
	if len(sites) != 1 || sites[0].fn != "endPreanim" {
		t.Errorf("`Speaker.PlayOnce = false` sites = %+v; want exactly one, in endPreanim.\n"+
			"Release the one-shot through c.endPreanim(reason) so every interrupt is named.", sites)
	}
}

// isPlayOnceSelector matches an lvalue whose final field is PlayOnce, however
// deep the receiver chain (c.Scene.Speaker.PlayOnce, sp.PlayOnce, ...).
func isPlayOnceSelector(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "PlayOnce"
}

func shortName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// TestPreanimEndReasonsAreAllNamed pins the reason table against the constant
// block: a reason added without a name would String() to "" and make the
// instrumentation silently useless for exactly the case someone added it for.
func TestPreanimEndReasonsAreAllNamed(t *testing.T) {
	for r := PreanimEndNone; r < preanimEndReasonCount; r++ {
		if r.String() == "" {
			t.Errorf("PreanimEndReason(%d) has no name in preanimEndNames", int(r))
		}
	}
	if got := PreanimEndReason(preanimEndReasonCount).String(); got != "unknown" {
		t.Errorf("out-of-range reason String() = %q, want %q", got, "unknown")
	}
	if got := PreanimEndReason(-1).String(); got != "unknown" {
		t.Errorf("negative reason String() = %q, want %q", got, "unknown")
	}
}

// TestEveryPreanimEndPathNamesItself drives the seam through each of the ways a
// one-shot can end and asserts the recorded reason. This is the instrumentation
// the audit asked for: the answer to "who ended the preanimation" is now a value
// the debug surface can read instead of a guess.
func TestEveryPreanimEndPathNamesItself(t *testing.T) {
	t.Run("finished", func(t *testing.T) {
		room, sess, _, _ := newCourtroomRig(t)
		setupReadySession(t, sess)
		room.HandleEvent(Event{Kind: EventMessage, Message: preanimMsg(t, sess, "hi")})
		mustPhase(t, room, PhasePreanim)
		room.NotifyPreanimDone()
		room.Update(time.Millisecond)
		wantReason(t, room, PreanimEndFinished)
	})

	t.Run("timeout", func(t *testing.T) {
		room, sess, _, _ := newCourtroomRig(t)
		setupReadySession(t, sess)
		room.HandleEvent(Event{Kind: EventMessage, Message: preanimMsg(t, sess, "hi")})
		mustPhase(t, room, PhasePreanim)
		room.Update(DefaultPreanimTimeout + time.Millisecond)
		wantReason(t, room, PreanimEndTimeout)
	})

	t.Run("missing", func(t *testing.T) {
		room, sess, _, _ := newCourtroomRig(t)
		setupReadySession(t, sess)
		room.HandleEvent(Event{Kind: EventMessage, Message: preanimMsg(t, sess, "hi")})
		mustPhase(t, room, PhasePreanim)
		room.NotifyAssetMissing(room.Scene.Speaker.PreanimBase)
		wantReason(t, room, PreanimEndMissing)
	})

	t.Run("text settled", func(t *testing.T) {
		// An IMMEDIATE preanim whose bound expires while the text is still
		// crawling lands in the talk loop; then the text finishes and enterLinger
		// parks idle. Drive the plain settle instead: a blocking preanim that
		// finished, text skipped to the end.
		room, _, _, _ := newCourtroomRig(t)
		room.HandleEvent(Event{Kind: EventMessage, Message: immediatePreanimMsg("hi")})
		if !room.Scene.Speaker.PlayOnce {
			t.Fatal("immediate message did not park a one-shot on stage")
		}
		// Kill the decoration's own countdown so enterLinger owns the release —
		// that is the "text settled with nothing still animating" path.
		room.imPreLeft = 0
		room.Typewriter.SkipToEnd()
		room.Update(time.Millisecond)
		wantReason(t, room, PreanimEndTextSettled)
	})

	t.Run("new message", func(t *testing.T) {
		room, sess, _, _ := newCourtroomRig(t)
		setupReadySession(t, sess)
		room.HandleEvent(Event{Kind: EventMessage, Message: preanimMsg(t, sess, "first")})
		mustPhase(t, room, PhasePreanim)
		// A plain IDLE follow-up: it parks no one-shot of its own, so the latch
		// state after begin() is unambiguously the FIRST message's release.
		room.begin(&protocol.ChatMessage{
			CharName: "Erika", Emote: "normal", Side: "jud",
			Message: "second", EmoteMod: protocol.EmoteModIdle,
		})
		wantReason(t, room, PreanimEndNewMessage)
	})

	t.Run("stage wiped", func(t *testing.T) {
		room, sess, _, _ := newCourtroomRig(t)
		setupReadySession(t, sess)
		room.HandleEvent(Event{Kind: EventMessage, Message: preanimMsg(t, sess, "hi")})
		mustPhase(t, room, PhasePreanim)
		room.wipeStage()
		wantReason(t, room, PreanimEndStageWiped)
	})
}

// TestPreanimEndHookFiresOncePerRelease pins the optional stream: one call per
// release, never a repeat from the idempotent second visit (a wipe right after a
// new message, a 404 landing after the natural finish). A hook that double-fired
// would make the diagnostic lie about which cause won.
func TestPreanimEndHookFiresOncePerRelease(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)
	var got []PreanimEndReason
	room.OnPreanimEnd = func(r PreanimEndReason) { got = append(got, r) }

	room.HandleEvent(Event{Kind: EventMessage, Message: preanimMsg(t, sess, "hi")})
	mustPhase(t, room, PhasePreanim)
	room.NotifyPreanimDone()
	room.Update(time.Millisecond)
	// Everything below finds no one-shot on stage and must stay silent.
	room.NotifyAssetMissing(room.Scene.Speaker.PreanimBase)
	room.wipeStage()

	if len(got) != 1 || got[0] != PreanimEndFinished {
		t.Errorf("hook calls = %v, want exactly [%v]", got, PreanimEndFinished)
	}
}

// TestStalePreanimDoneFromASharedViewportIsIgnored is the regression pin for the
// "the preanimation is randomly interrupted" report.
//
// internal/ui keeps ONE viewport with ONE OnPreanimDone slot and re-points it at
// whichever room drives it (ui/replay.go, ui/scenemaker.go, ui/gifexport.go each
// assign it and assign it back to a.room). A completion that fires across such a
// swap used to land on a room with nothing playing and set the sticky
// preanimDone flag anyway; the NEXT message then inherited it and lost its
// preanim three different ways at once — enterAfterShout's `!c.preanimDone` term
// skips the selection, PhasePreanim exits on its first Update, and
// tickImmediatePreanim ends the decoration on tick one. No local cause, no
// repro. NotifyPreanimStarted already carried this guard; its sibling did not.
func TestStalePreanimDoneFromASharedViewportIsIgnored(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)

	// The other room's animation finishes while this one has nothing on stage.
	room.NotifyPreanimDone()
	if room.preanimDone {
		t.Fatal("a done-report with no one-shot on stage was accepted")
	}

	// The next message must still play its preanimation in full.
	room.HandleEvent(Event{Kind: EventMessage, Message: preanimMsg(t, sess, "hi")})
	if room.Phase() != PhasePreanim || !room.Scene.Speaker.PlayOnce {
		t.Fatalf("stale report skipped the preanim outright: phase=%v playOnce=%v", room.Phase(), room.Scene.Speaker.PlayOnce)
	}
	room.Update(DefaultPreanimTimeout / 2)
	if room.Phase() != PhasePreanim {
		t.Fatalf("stale report cut the preanim short: phase=%v", room.Phase())
	}
	if got := room.LastPreanimEnd(); got != PreanimEndNone {
		t.Errorf("LastPreanimEnd = %v while the preanim is still playing, want %v", got, PreanimEndNone)
	}
}

// TestStalePreanimDoneCannotEndAnImmediateDecoration is the same guard on the
// other arm: an immediate-mode preanim is decoration that outlives its phase, so
// a stale completion arriving during PhaseLinger would end it a frame later
// through tickImmediatePreanim.
func TestStalePreanimDoneCannotEndAnImmediateDecoration(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.HandleEvent(Event{Kind: EventMessage, Message: immediatePreanimMsg("hi")})
	room.Typewriter.SkipToEnd()
	room.Update(time.Millisecond)
	if room.Phase() != PhaseLinger || !room.Scene.Speaker.PlayOnce {
		t.Fatalf("setup: phase=%v playOnce=%v, want a settled message with the decoration still up", room.Phase(), room.Scene.Speaker.PlayOnce)
	}
	// The decoration IS on stage, so its own room's report is legitimate and must
	// still land — the guard rejects "no one-shot here", not "not in PhasePreanim".
	room.NotifyPreanimDone()
	room.Update(time.Millisecond)
	if room.Scene.Speaker.PlayOnce {
		t.Error("the decoration's own completion must still end it")
	}
	if got := room.LastPreanimEnd(); got != PreanimEndFinished {
		t.Errorf("LastPreanimEnd = %v, want %v", got, PreanimEndFinished)
	}

	// Now that nothing is playing, a further report must not SET the flag again.
	// (begin() clears it per message; this stands in for the report landing in
	// the window before the next message arrives.)
	room.preanimDone = false
	room.NotifyPreanimDone()
	if room.preanimDone {
		t.Error("a report after the release was accepted and will poison the next message")
	}
}

func mustPhase(t *testing.T, room *Courtroom, want MessagePhase) {
	t.Helper()
	if room.Phase() != want {
		t.Fatalf("phase = %v, want %v", room.Phase(), want)
	}
}

func wantReason(t *testing.T, room *Courtroom, want PreanimEndReason) {
	t.Helper()
	if got := room.LastPreanimEnd(); got != want {
		t.Errorf("LastPreanimEnd = %v, want %v", got, want)
	}
	if room.Scene.Speaker.PlayOnce {
		t.Error("the one-shot latch survived the release")
	}
}

// compile-time proof the hook keeps the shape internal/ui would wire it with.
var _ = func(c *Courtroom) { c.OnPreanimEnd = func(PreanimEndReason) {} }
var _ = protocol.ChatMessage{}
