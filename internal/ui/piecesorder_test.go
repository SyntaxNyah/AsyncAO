package ui

import (
	"os"
	"strings"
	"testing"
)

// TestPiecesPanelDrawsAboveTornTabs pins the draw ORDER, which is where the
// reported fault lived rather than in any one panel's code.
//
// The Players tab can be torn off into a floating panel, and it used to be drawn
// AFTER the Hide-UI-pieces panel — so a torn roster covered the panel completely.
// That is the worst possible ordering for this particular pair: the pieces panel
// is what you open when the layout has gone wrong, and it was being hidden by
// exactly the sort of thing you would be trying to fix.
//
// A source test because the order is a sequence of statements in one function.
// Nothing at runtime can observe "which of these ran first" without a real
// renderer and a screenshot, and a unit test that stubbed the draws would be
// asserting against its own stubs rather than the frame.
func TestPiecesPanelDrawsAboveTornTabs(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	body := funcBodyOf(string(src), "func (a *App) drawScreen(")
	if body == "" {
		// The frame dispatch is not in its own function on every revision; fall back
		// to the whole file, which is still ordered.
		body = string(src)
	}
	torn := strings.Index(body, "a.drawTornTabs(winW, winH)")
	pieces := strings.LastIndex(body, "a.drawToolboxPieces(winW, winH)")
	if torn < 0 || pieces < 0 {
		t.Fatalf("could not find both draw calls (torn=%d pieces=%d) — the gate went blind", torn, pieces)
	}
	if pieces < torn {
		t.Error("drawToolboxPieces runs BEFORE drawTornTabs — a torn-off Players tab would cover the " +
			"one panel a user opens to fix their layout")
	}
}

// TestTornPlayersTabExists guards the premise of the test above: if the Players
// tab ever stops being tearable, the ordering rationale changes and this pair
// should be revisited rather than silently kept.
func TestTornPlayersTabExists(t *testing.T) {
	src, err := os.ReadFile("torntabs.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "logTabPlayers") {
		t.Skip("the Players tab is no longer tearable; revisit why the pieces panel draws last")
	}
}
