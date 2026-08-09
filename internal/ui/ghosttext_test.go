package ui

// Gates for the ghost-text option (ghosttext.go): the default, the pure boundary
// arithmetic, the pairing between log lines and the play queue, and the
// encapsulation gate that stops the IC log's draw from quietly forgetting to ask.

import (
	"go/ast"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestGhostCrawlTextShipsOn pins the DEFAULT (user-approved ON, no migration
// stamp — a brand-new preference whose absent value has never meant anything
// else).
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

// TestUnspokenCountFollowsThePhase pins which phases still owe the room a spoken
// line. PhaseLinger is the interesting row: the text is fully revealed by then, so
// the message HAS been said and must not stay faint through the linger.
func TestUnspokenCountFollowsThePhase(t *testing.T) {
	for _, tc := range []struct {
		phase  courtroom.MessagePhase
		queued int
		want   int
		why    string
	}{
		{courtroom.PhaseIdle, 0, 0, "nothing playing, nothing queued"},
		{courtroom.PhaseIdle, 3, 3, "a drained stage still owes the queue"},
		{courtroom.PhaseShout, 0, 1, "a shout has revealed no text yet"},
		{courtroom.PhasePreanim, 2, 3, "the preanim plus everything behind it"},
		{courtroom.PhaseTalking, 0, 1, "the line being typed is not said yet"},
		{courtroom.PhaseLinger, 0, 0, "the crawl finished: the line HAS been said"},
		{courtroom.PhaseLinger, 1, 1, "…but the queue behind it has not"},
	} {
		if got := unspokenCount(tc.phase, tc.queued); got != tc.want {
			t.Errorf("unspokenCount(%v, %d) = %d, want %d (%s)", tc.phase, tc.queued, got, tc.want, tc.why)
		}
	}
}

// TestFirstUnspokenEntrySkipsSystemLines pins the pairing rule: only entries
// stamped `speech` have a message in the play queue, so a music or evidence line
// interleaved among them must not be counted as something a character is about to
// say (nor be ghosted itself).
func TestFirstUnspokenEntrySkipsSystemLines(t *testing.T) {
	log := []icEntry{
		{text: "A: said", speech: true},
		{text: "someone played a song"}, // system line — never queued
		{text: "B: saying", speech: true},
		{text: "C: queued", speech: true},
	}
	if got := firstUnspokenEntry(log, 0); got != len(log) {
		t.Errorf("nothing unspoken must ghost nothing, got entry %d", got)
	}
	if got := firstUnspokenEntry(log, 2); got != 2 {
		t.Errorf("two unspoken lines start at the B line (index 2), got %d", got)
	}
	if got := firstUnspokenEntry(log, 1); got != 3 {
		t.Errorf("one unspoken line is the last speech line (index 3), got %d", got)
	}
	// More claimed than the log holds (a shed message, a catch-up skip): ghost
	// nothing rather than hide a line that has already been said.
	if got := firstUnspokenEntry(log, 9); got != len(log) {
		t.Errorf("an over-claim must ghost nothing, got entry %d", got)
	}
}

// TestGhostRowStartWalksTheTail pins the row boundary: a row is faint until its
// LAST rune is revealed, so a one-line message stays faint for the whole crawl and
// a wrapped one goes solid a row at a time from the top.
func TestGhostRowStartWalksTheTail(t *testing.T) {
	// Entry 1 owns rows 1..3 ("aaaa", "bbbb", "cccc" — 4 runes each, 12 total);
	// entries 0 and 2 exist to prove the walk stays inside its own entry.
	rows := []icWrapLine{
		{text: "other", entry: 0},
		{text: "aaaa", entry: 1},
		{text: "bbbb", entry: 1},
		{text: "cccc", entry: 1},
		{text: "later", entry: 2},
	}
	for _, tc := range []struct {
		unrevealed, want int
		why              string
	}{
		{0, 4, "fully revealed: no row is faint (one past the entry's last row)"},
		{1, 3, "one rune left: only the last row is faint"},
		{4, 3, "the whole last row unrevealed: still just that row"},
		{5, 2, "the tail reaches into the middle row"},
		{8, 2, "the last two rows entirely"},
		{9, 1, "the tail reaches the first row"},
		{12, 1, "nothing revealed: every row of the entry is faint"},
		{99, 1, "an over-long tail clamps to the entry's first row"},
	} {
		if got := ghostRowStart(rows, 1, tc.unrevealed); got != tc.want {
			t.Errorf("ghostRowStart(unrevealed=%d) = %d, want %d (%s)", tc.unrevealed, got, tc.want, tc.why)
		}
	}
	// An entry filtered out of the view has no rows at all: answer 0 rather than
	// index off the end of a slice the caller is about to walk.
	if got := ghostRowStart(rows, 7, 5); got != 0 {
		t.Errorf("a filtered-out entry = %d, want 0", got)
	}
}

// TestGhostSpanGhostsTheQueueBehindTheCrawl pins the span's own rule: rows of the
// crawling entry go faint from rowFrom, and EVERY row of a later entry is faint —
// the queue is what runs furthest ahead of the chatbox and spoils the most.
func TestGhostSpanGhostsTheQueueBehindTheCrawl(t *testing.T) {
	g := ghostSpan{entry: 2, rowFrom: 5, ok: true}
	for _, tc := range []struct {
		ri, entry int
		want      bool
		why       string
	}{
		{0, 1, false, "an earlier entry has been said"},
		{4, 2, false, "a revealed row of the crawling entry"},
		{5, 2, true, "the first unrevealed row"},
		{6, 2, true, "and the rows after it"},
		{7, 3, true, "a queued entry is faint from its first row"},
	} {
		if got := g.ghosted(tc.ri, tc.entry); got != tc.want {
			t.Errorf("ghosted(row %d, entry %d) = %v, want %v (%s)", tc.ri, tc.entry, got, tc.want, tc.why)
		}
	}
	// The zero value is the off switch: it must ghost nothing at all.
	var off ghostSpan
	if off.ghosted(0, 0) || off.ghosted(99, 99) {
		t.Error("the zero ghostSpan must ghost nothing — that is the option-off path")
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
	// system line, firstUnspokenEntry can never find the crawling message, and the
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
