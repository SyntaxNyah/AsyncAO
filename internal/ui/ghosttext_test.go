package ui

// Gates for the ghost-text option (ghosttext.go): the default, the pairing
// between log lines and the play QUEUE, the corrected rule (only queued
// successors are ghosted — never the message being spoken), and the
// encapsulation gate that stops the IC log's draw from quietly forgetting to ask.

import (
	"go/ast"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestGhostCrawlTextShipsOn pins the DEFAULT (user-approved ON, no migration
// stamp — a brand-new preference whose absent value has never meant anything
// else). The spec correction narrowed WHAT is ghosted; it did not turn the
// option off or change its default.
func TestGhostCrawlTextShipsOn(t *testing.T) {
	p, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if !p.GhostCrawlTextOn() {
		t.Fatal("GhostCrawlText must ship ON")
	}
	p.SetGhostCrawlText(false)
	if p.GhostCrawlTextOn() {
		t.Fatal("turning ghost text off must stick")
	}
}

// TestQueuedCountIgnoresTheMessageOnStage is the corrected spec in one
// assertion. The first cut added 1 for PhaseShout / PhasePreanim / PhaseTalking,
// which is exactly how the message being spoken ended up ghosted — the defect
// the field report names ("only supposed to ghost the incoming next message,
// not the lines of the current message"). The phase must not reach this
// arithmetic at all.
func TestQueuedCountIgnoresTheMessageOnStage(t *testing.T) {
	for _, tc := range []struct {
		queued, want int
		why          string
	}{
		{0, 0, "nothing queued: nothing is ghosted, whatever the stage is doing"},
		{1, 1, "one message waiting behind the current line"},
		{3, 3, "three waiting"},
		{-1, 0, "a nonsense queue length can never ghost a line that HAS been said"},
	} {
		if got := queuedCount(tc.queued); got != tc.want {
			t.Errorf("queuedCount(%d) = %d, want %d (%s)", tc.queued, got, tc.want, tc.why)
		}
	}
}

// TestFirstQueuedEntrySkipsSystemLines pins the pairing rule: only entries
// stamped `speech` have a message in the play queue, so a music or evidence line
// interleaved among them must not be counted as something a character is about to
// say (nor be ghosted itself).
func TestFirstQueuedEntrySkipsSystemLines(t *testing.T) {
	log := []icEntry{
		{text: "A: said", speech: true},
		{text: "someone played a song"}, // system line — never queued
		{text: "B: saying", speech: true},
		{text: "C: queued", speech: true},
	}
	if got := firstQueuedEntry(log, 0); got != len(log) {
		t.Errorf("an empty queue must ghost nothing, got entry %d", got)
	}
	if got := firstQueuedEntry(log, 2); got != 2 {
		t.Errorf("two queued lines start at the B line (index 2), got %d", got)
	}
	if got := firstQueuedEntry(log, 1); got != 3 {
		t.Errorf("one queued line is the last speech line (index 3), got %d", got)
	}
	// More claimed than the log holds (a shed message, a catch-up skip): ghost
	// nothing rather than hide a line that has already been said.
	if got := firstQueuedEntry(log, 9); got != len(log) {
		t.Errorf("an over-claim must ghost nothing, got entry %d", got)
	}
}

// TestGhostSpanGhostsWholeQueuedEntriesOnly is the corrected rule at the span
// level: EVERY row of a queued entry is faint and NO row of any earlier entry
// is — including the one crawling right now, whose rows used to flip solid one
// at a time as the crawl passed them.
func TestGhostSpanGhostsWholeQueuedEntriesOnly(t *testing.T) {
	g := ghostSpan{entry: 2, ok: true}
	for _, tc := range []struct {
		ri, entry int
		want      bool
		why       string
	}{
		{0, 0, false, "an entry already said"},
		{4, 1, false, "the message being spoken RIGHT NOW — never ghosted"},
		{9, 1, false, "…including its last row, however far ahead of the crawl it is"},
		{0, 2, true, "the first queued entry, from its first row"},
		{3, 2, true, "…and every other row of it"},
		{7, 3, true, "and everything queued behind that"},
	} {
		if got := g.ghosted(tc.ri, tc.entry); got != tc.want {
			t.Errorf("ghosted(row %d, entry %d) = %v, want %v (%s)", tc.ri, tc.entry, got, tc.want, tc.why)
		}
	}
	// The row index must not change the answer for a given entry. This is the
	// property that makes "ghost the current message's own lines" unexpressible:
	// reintroduce a row boundary and this fails.
	for ri := 0; ri < 64; ri++ {
		if g.ghosted(ri, 1) {
			t.Fatalf("row %d of the crawling entry is ghosted — the span grew a row boundary again", ri)
		}
		if !g.ghosted(ri, 2) {
			t.Fatalf("row %d of a queued entry is NOT ghosted — the preview is partial", ri)
		}
	}
	// The zero value is the off switch: it must ghost nothing at all.
	var off ghostSpan
	if off.ghosted(0, 0) || off.ghosted(99, 99) {
		t.Error("the zero ghostSpan must ghost nothing — that is the option-off path")
	}
}

// TestGhostSpanPromotesWhenTheQueueDrains walks the live sequence the report
// describes, driving the REAL boundary resolution (ghostSpan + firstQueuedEntry,
// the two pieces ghostLogSpan composes) rather than restating it:
//
//	3 messages logged, 1 crawling, 2 queued → the two successors are ghosted
//	the crawl finishes, one dequeues        → the promoted one draws normal
//	the queue empties                       → nothing is ghosted anywhere
func TestGhostSpanPromotesWhenTheQueueDrains(t *testing.T) {
	log := []icEntry{
		{text: "A: crawling", speech: true},
		{text: "B: queued", speech: true},
		{text: "C: queued", speech: true},
	}
	span := func(queued int) ghostSpan {
		n := queuedCount(queued)
		if n == 0 {
			return ghostSpan{}
		}
		e := firstQueuedEntry(log, n)
		if e >= len(log) {
			return ghostSpan{}
		}
		return ghostSpan{entry: e, ok: true}
	}

	g := span(2)
	if g.ghosted(0, 0) {
		t.Error("the crawling message must draw NORMAL while two are queued behind it")
	}
	if !g.ghosted(0, 1) || !g.ghosted(0, 2) {
		t.Error("both queued successors must draw ghosted")
	}

	g = span(1) // B started crawling; only C is still waiting
	if g.ghosted(0, 1) {
		t.Error("a message must promote to normal the moment it starts crawling")
	}
	if !g.ghosted(0, 2) {
		t.Error("the message still queued behind it stays ghosted")
	}

	g = span(0) // nothing left in the queue
	if g.ok || g.ghosted(0, 0) || g.ghosted(0, 2) {
		t.Error("with no queue, nothing anywhere is ghosted")
	}
}

// TestGhostInkPullsTowardTheSurface pins the blend: a ghosted colour moves toward
// the background (so it reads as faint on the panel) without becoming it, and the
// alpha channel is left alone — the log's text path has no per-draw alpha, which
// is exactly why this is a blend.
func TestGhostInkPullsTowardTheSurface(t *testing.T) {
	white := ColText
	got := ghostInk(white, ColPanel)
	if got == white {
		t.Fatal("a ghosted colour must differ from the solid one")
	}
	if got == ColPanel {
		t.Fatal("a ghosted colour must stay legible — it must not become the background")
	}
	if got.A != white.A {
		t.Errorf("ghostInk must not touch alpha: got %d, want %d", got.A, white.A)
	}
	// Monotone: pulling a bright ink toward a dark panel can only darken it.
	if got.R > white.R || got.G > white.G || got.B > white.B {
		t.Errorf("ghostInk brightened a colour it should have faded: %+v -> %+v", white, got)
	}
}

// TestGhostSpanNeverReadsTheCrawl is the deletion catcher for the CORRECTION
// itself. The defect was ghostLogSpan measuring the typewriter — Scene.VisibleRunes
// against the message length — to find a row boundary inside the current message.
// Reading either of those here again is that defect returning, and every unit gate
// above would stay green while it did.
func TestGhostSpanNeverReadsTheCrawl(t *testing.T) {
	body := funcBodySource(t, "ghosttext.go", "ghostLogSpan")
	for _, banned := range []string{"VisibleRunes", "MessageText", "Phase"} {
		if readsIdent(body, banned) {
			t.Errorf("ghostLogSpan reads %s again — the ghost boundary is back inside the "+
				"message being spoken, which is the reported defect (only QUEUED messages "+
				"may be ghosted)", banned)
		}
	}
	if !containsCall(body, "QueueLen") {
		t.Fatal("ghostLogSpan no longer asks the room for its QUEUE length — the preview " +
			"has nothing to pair log lines with")
	}
}

// readsIdent reports whether n mentions the identifier name anywhere (as a bare
// ident or as a selector's field/method), which is what a source census needs:
// `sc.VisibleRunes`, `a.room.Scene.VisibleRunes` and a bare `VisibleRunes` all count.
func readsIdent(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		switch v := node.(type) {
		case *ast.SelectorExpr:
			if v.Sel != nil && v.Sel.Name == name {
				found = true
			}
		case *ast.Ident:
			if v.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

// TestICLogAsksForTheGhostSpan is the encapsulation gate. The whole feature is one
// call in the IC log's draw; delete it and every gate above still passes while the
// option silently does nothing — the parsed-but-never-applied failure CLAUDE.md
// names. Source, because a missing call has no observable return value.
func TestICLogAsksForTheGhostSpan(t *testing.T) {
	body := funcBodySource(t, "screens.go", "drawICLogList")
	if !containsCall(body, "ghostLogSpan") {
		t.Fatal("drawICLogList no longer resolves a ghostSpan — the ghost-text option is dead")
	}
	if !containsCall(body, "ghosted") {
		t.Fatal("drawICLogList resolves a ghostSpan but never asks it about a row")
	}
	if !containsCall(body, "ghostInk") {
		t.Fatal("drawICLogList decides a row is ghosted but never fades its ink")
	}
	// The stamp the pairing depends on: without it every log line looks like a
	// system line, firstQueuedEntry can never find the queued message, and the
	// whole option goes quiet with every unit gate above still green.
	if !assignsField(funcBodySource(t, "app.go", "handleSessionEvents"), "speech") {
		t.Fatal("handleSessionEvents no longer stamps icEntry.speech — the ghost pairing cannot tell a spoken line from a queued one")
	}
}

// assignsField reports whether n contains an assignment whose left-hand side is a
// selector ending in .field (e.g. `a.icLog[i].speech = true`).
func assignsField(n ast.Node, field string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
				found = true
			}
		}
		return !found
	})
	return found
}
