package theme

// AO2-compatible export; ASYNCAO-LOST.txt is in the bundle so it survives re-share and reaches AO2 users.

import (
	"fmt"
	"sort"
	"strings"
)

// LostFileName is the report in an AO2-compat export, uppercase and hyphenated so it sorts first and reads as a notice.
const LostFileName = "ASYNCAO-LOST.txt"

// lostReportCap bounds the report's lines (hard rule 4): ElementCap plus the header and non-element notes.
const (
	lostReportNoteLines = 16 // the header and the non-element notes
	lostReportCap       = ElementCap + lostReportNoteLines
)

// BakeAO2Design rewrites a courtroom_design.ini with the geometry this theme draws; cold path only (hard rule 2).
func BakeAO2Design(src []byte, s *Sidecar) ([]byte, error) {
	d, err := ParseINIDoc(src)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return d.Bytes(), nil
	}
	for _, o := range s.Overrides {
		// AO2's design keys live in the root section: courtroom_design.ini has no [headers].
		if err := d.Set("", o.Key, formatRect(o.Rect)); err != nil {
			return nil, fmt.Errorf("%s: %w", o.Key, err)
		}
	}
	return d.Bytes(), nil
}

// StripForAO2 returns a copy with the baked tier emptied, so overrides do not apply twice.
// The document is not copied, or its [overrides] lines would come back.
func StripForAO2(s *Sidecar) *Sidecar {
	if s == nil {
		return nil
	}
	out := *s
	out.doc = nil
	out.Overrides = nil
	out.Rotations = nil
	// The remaining slices are shared with the original and only read from here on.
	return &out
}

// LostToAO2 names by id and kind everything an AO2-Client will not show; every line must be actionable.
func LostToAO2(s *Sidecar) []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, lostReportNoteLines)
	add := func(format string, args ...any) {
		if len(out) >= lostReportCap {
			return
		}
		out = append(out, fmt.Sprintf(format, args...))
	}
	for i := range s.Elements {
		e := &s.Elements[i]
		add("[%s%s] (%s) — AsyncAO free elements have no AO2 equivalent; nothing is drawn for it",
			secElementPrefix, e.ID, e.Kind)
	}
	for _, r := range s.Rotations {
		add("[%s] %s = %d — AO2 has no rotation key, so the widget is drawn upright",
			secRotations, r.Key, r.Angle)
	}
	for i := range s.Panes {
		add("[%s%s] — split-screen panes are an AsyncAO layout; AO2 draws the single viewport",
			secPanePrefix, s.Panes[i].ID)
	}
	for i := range s.Effects {
		add("[%s%s] (%s) — AO2 has no widget effects; the widget is drawn plain",
			secEffectPrefix, s.Effects[i].Target, s.Effects[i].Effect)
	}
	for i := 1; i < ClockCap; i++ {
		if s.Clocks[i].Declared {
			add("[%s%s] — animation clocks are AsyncAO-only; anything on this clock is static in AO2",
				secClockPrefix, s.Clocks[i].ID)
		}
	}
	if len(s.Media) > 0 {
		names := make([]string, 0, len(s.Media))
		for _, m := range s.Media {
			names = append(names, m.ID)
		}
		sort.Strings(names)
		add("[%s] %s — the art is in the folder and AO2 will simply not reference it",
			secMedia, strings.Join(names, ", "))
	}
	if !s.Palette.Empty() {
		add("[%s] — AsyncAO palette roles; AO2 takes its colours from courtroom_stylesheets.css", secPalette)
	}
	return out
}

// LostReport renders the file's text, with a header for someone who has never heard of AsyncAO.
func LostReport(themeName string, s *Sidecar) []byte {
	var b strings.Builder
	name := themeName
	if name == "" {
		name = "this theme"
	}
	b.WriteString("What AO2-Client will not show from " + name + "\n")
	b.WriteString("====================================" + strings.Repeat("=", len(name)) + "\n\n")
	b.WriteString("This theme was made with AsyncAO and exported so that AO2-Client can use it.\n")
	b.WriteString("Its geometry has been baked into " + DesignFileName + ", so widgets sit where\n")
	b.WriteString("the author put them. The list below is everything AsyncAO draws that AO2 has\n")
	b.WriteString("no way to draw — the files are all still here, AO2 just never asks for them.\n")
	b.WriteString("Open the theme in AsyncAO to see it as it was designed.\n\n")
	lost := LostToAO2(s)
	if len(lost) == 0 {
		b.WriteString("Nothing. This theme uses only what AO2 already understands.\n")
		return []byte(b.String())
	}
	for _, line := range lost {
		b.WriteString("  - " + line + "\n")
	}
	return []byte(b.String())
}
