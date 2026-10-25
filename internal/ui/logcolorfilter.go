package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// Log colour filter (#49): the IC log's find/filter can now narrow to entries
// that use a particular text colour — the message-level AO colour OR any inline
// colour span — so a coloured callout (a red objection, a green fact) can be
// looked up at a glance. AO2's own log colouring existed for exactly that
// "find it by colour" use, and the filter restores the lookup half of it.

// logColorFilterAny is the "no colour filter" selection: every entry matches.
const logColorFilterAny = 0

// logColorFilterStdFirst is the FIRST standard-palette entry's selection index;
// a selection in [logColorFilterStdFirst, logColorFilterStdFirst+TextColorCount)
const logColorFilterStdFirst = 1

// logColorFilterExtFirst / logColorFilterRainbow are the block boundaries, all
// len-derived so they shift correctly if the palettes change. Extended entries
// sit between the standard block and the trailing Rainbow entry.
var (
	logColorFilterExtFirst = logColorFilterStdFirst + render.TextColorCount
	logColorFilterRainbow  = logColorFilterExtFirst + render.ExtColorCount()
	logColorFilterChoices  = buildLogColorFilterChoices()
)

// buildLogColorFilterChoices assembles the filter dropdown once at init: a
// leading "Any color", the standard AO palette, the extended AsyncAO colours,
// then Rainbow. Built once so the filter panel never allocates per frame.
func buildLogColorFilterChoices() []string {
	out := []string{"Any color"}
	out = append(out, render.TextColorNames()...)
	for i := 0; i < render.ExtColorCount(); i++ {
		out = append(out, render.ExtColorAt(i).Name)
	}
	return append(out, "Rainbow")
}

// icEntryUsesColor reports whether entry e renders any text in the colour the
// filter selection sel names. Pure — no SDL — so the mapping is drivable in a
// test. A rendered message's colours are its message-level text_color plus any
// inline colour runs the typewriter parsed (courtroom.StyleRun); a run with
// ColorDefault (-1) resolves to the message colour, so a standard-palette target
// matching on e.color already covers those runs.
func icEntryUsesColor(e *icEntry, sel int) bool {
	if sel == logColorFilterAny {
		return true
	}
	if sel < logColorFilterExtFirst {
		idx := sel - logColorFilterStdFirst
		if e.color == idx {
			return true
		}
		for _, s := range e.styles {
			if s.Color == idx {
				return true
			}
		}
		return false
	}
	if sel < logColorFilterRainbow {
		code := int(render.ExtColorAt(sel - logColorFilterExtFirst).Code)
		for _, s := range e.styles {
			if s.Color == courtroom.ColorExtBase+code {
				return true
			}
		}
		return false
	}
	for _, s := range e.styles {
		if s.Color == courtroom.ColorRainbow {
			return true
		}
	}
	return false
}
