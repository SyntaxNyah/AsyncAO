package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// logColorFilterAny is the "no colour filter" selection: every entry matches.
const logColorFilterAny = 0

// logColorFilterStdFirst is the first standard-palette selection; 1, not 0, because "Any color" comes first.
const logColorFilterStdFirst = 1

// Block boundaries derive from the palette lengths; extended entries sit between the standard block and Rainbow.
var (
	logColorFilterExtFirst = logColorFilterStdFirst + render.TextColorCount
	logColorFilterRainbow  = logColorFilterExtFirst + render.ExtColorCount()
	logColorFilterChoices  = buildLogColorFilterChoices()
)

// buildLogColorFilterChoices builds the dropdown once so the filter panel never allocates per frame.
func buildLogColorFilterChoices() []string {
	out := []string{"Any color"}
	out = append(out, render.TextColorNames()...)
	for i := 0; i < render.ExtColorCount(); i++ {
		out = append(out, render.ExtColorAt(i).Name)
	}
	return append(out, "Rainbow")
}

// icEntryUsesColor reports whether e renders any text in the colour selection sel names.
// A run with ColorDefault resolves to the message colour, so matching e.color already covers those runs.
// Custom-hex spans can't be enumerated so aren't filterable; such a message still matches its nearest palette colour
// through e.color.
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
