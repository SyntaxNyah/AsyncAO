package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestICEntryUsesColor drives the pure colour-filter matcher (#49): the mapping
// from a filter selection to "does this entry render any text in that colour",
// covering the message-level colour, inline standard spans, extended colours and
// rainbow — plus the "Any color" no-filter pass-through.
func TestICEntryUsesColor(t *testing.T) {
	red := logColorFilterStdFirst + 2   // standard palette index 2 (Red)
	green := logColorFilterStdFirst + 1 // standard palette index 1 (Green)
	ext := logColorFilterExtFirst       // first extended colour (Purple)
	rainbow := logColorFilterRainbow

	if !icEntryUsesColor(&icEntry{color: 0}, logColorFilterAny) {
		t.Error("Any color must match a plain entry")
	}
	if !icEntryUsesColor(&icEntry{color: 2}, red) {
		t.Error("message colour 2 must match the red filter")
	}
	if icEntryUsesColor(&icEntry{color: 2}, green) {
		t.Error("message colour 2 must not match the green filter")
	}

	if !icEntryUsesColor(&icEntry{color: 0, styles: []courtroom.StyleRun{{Len: 5, Color: 2}}}, red) {
		t.Error("an inline red span must match the red filter")
	}
	if icEntryUsesColor(&icEntry{color: 0, styles: []courtroom.StyleRun{{Len: 5, Color: 2}}}, green) {
		t.Error("an inline red span must not match the green filter")
	}

	code := int(render.ExtColorAt(0).Code)
	if !icEntryUsesColor(&icEntry{color: 0, styles: []courtroom.StyleRun{{Len: 3, Color: courtroom.ColorExtBase + code}}}, ext) {
		t.Error("an inline extended-colour span must match its filter")
	}

	if !icEntryUsesColor(&icEntry{color: 0, styles: []courtroom.StyleRun{{Len: 4, Color: courtroom.ColorRainbow}}}, rainbow) {
		t.Error("a rainbow run must match the rainbow filter")
	}
}

// TestICLogFilterColor pins the colour filter end-to-end through icLogFiltered:
// it narrows to the entries that use the selected colour and invalidates the
// cache on every colour change (back to "Any color" restores the full set).
func TestICLogFilterColor(t *testing.T) {
	a := &App{}
	a.pushIC("Phoenix: hello court", 0, false, -1, "Phoenix")   // white, no inline
	a.pushIC("Edgeworth: OBJECTION", 2, false, -1, "Edgeworth") // red message colour
	a.pushIC("Maya: green fact", 0, false, -1, "Maya")
	a.icLog[2].styles = []courtroom.StyleRun{{Len: 11, Color: 1}} // inline green span
	a.pushIC("Gumshoe: rainbow", 0, false, -1, "Gumshoe")
	a.icLog[3].styles = []courtroom.StyleRun{{Len: 7, Color: courtroom.ColorRainbow}}

	if got := a.icLogFiltered(); len(got) != 4 {
		t.Fatalf("unfiltered = %d, want 4", len(got))
	}

	a.logFilterColor = logColorFilterStdFirst + 2 // Red
	if got := a.icLogFiltered(); len(got) != 1 || got[0] != 1 {
		t.Errorf("red filter = %v, want [1]", got)
	}

	a.logFilterColor = logColorFilterStdFirst + 1 // Green
	if got := a.icLogFiltered(); len(got) != 1 || got[0] != 2 {
		t.Errorf("green filter = %v, want [2]", got)
	}

	a.logFilterColor = logColorFilterRainbow
	if got := a.icLogFiltered(); len(got) != 1 || got[0] != 3 {
		t.Errorf("rainbow filter = %v, want [3]", got)
	}

	a.logFilterColor = logColorFilterAny
	if got := a.icLogFiltered(); len(got) != 4 {
		t.Errorf("any-color reset = %d, want 4 (cache must invalidate on colour change)", len(got))
	}
}
