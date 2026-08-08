package theme

// AO2-COMPAT EXPORT (v1.90.0 W8) — design §Q4 "Also work in AO2-Client".
//
// ONE concern: producing the TWO files an AO2-Client user needs out of an
// AsyncAO theme — a courtroom_design.ini with our [overrides] baked into AO2's
// own keys, and a plain-text list of everything AO2 will not show.
//
// IT PRODUCES BYTES AND WRITES NOTHING. Every function here takes the source
// bytes and returns the result; no path, no os call, no destination. That is
// what keeps the design's hardest write guard true by construction rather than
// by discipline: the author's own courtroom_design.ini is the ONE file the
// editor must never touch (a re-download would destroy their work and every
// upstream diff would be noise), so the baked version exists only for the
// length of one zip and only inside it.
//
// THE BAKE IS LOSSLESS ON THE WAY IN. It runs through INIDoc, so the author's
// comments, key order, casing, unknown keys and line endings all survive; only
// the keys the sidecar overrides are rewritten, and only to values AO2 already
// reads. Gate: the baked bytes go back through the Qt-parity oracle.
//
// AND ASYNCAO-LOST.txt GOES IN THE BUNDLE. A file in the archive beats a dialog
// in our UI: it survives re-share, renaming and re-zipping, and it reaches the
// AO2 user who will never see our export sheet.

import (
	"fmt"
	"sort"
	"strings"
)

// LostFileName is the plain-text report bundled with an AO2-compat export.
// SHOUTED and hyphenated so it sorts to the top of a folder listing and reads
// as a notice rather than as one of the theme's own files.
const LostFileName = "ASYNCAO-LOST.txt"

// lostReportCap bounds the report's lines (hard rule 4). It is ElementCap plus
// room for the header and the non-element notes, because the report's job is to
// name every element AO2 drops and a theme cannot hold more elements than that.
const lostReportCap = ElementCap + 16

// BakeAO2Design rewrites a courtroom_design.ini so an AO2-Client reading it sees
// the geometry this theme actually draws with.
//
// src is the author's file (nil or empty is fine — a theme may ship none, and
// the result is then a file holding only the baked keys). The returned bytes are
// the bundle's copy; the caller never writes them back over src.
//
// ROTATIONS ARE NOT BAKED, and that is not an omission. AO2 has no rotation key
// to bake them INTO — a rotated widget in AO2 is simply the un-rotated one — so
// each is reported in ASYNCAO-LOST.txt instead. Inventing a key AO2 does not
// read would produce a file its own parser warns about, which is worse than a
// documented absence.
//
// COLD PATH ONLY (hard rule 2).
func BakeAO2Design(src []byte, s *Sidecar) ([]byte, error) {
	d, err := ParseINIDoc(src)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return d.Bytes(), nil
	}
	for _, o := range s.Overrides {
		// AO2's design keys live in the ROOT section: courtroom_design.ini has no
		// [headers] at all, which is why every reader of it uses the "" section.
		if err := d.Set("", o.Key, formatRect(o.Rect)); err != nil {
			return nil, fmt.Errorf("%s: %w", o.Key, err)
		}
	}
	return d.Bytes(), nil
}

// StripForAO2 returns a COPY of the sidecar with the tier that has just been
// baked emptied out, so the exported theme does not apply its overrides twice
// when an AsyncAO client opens it.
//
// A COPY, never the live model: the editor's document and the applied theme are
// the same *Sidecar (themeDoc.side), so mutating it here would silently move
// every widget in the user's own courtroom the moment they pressed Share.
//
// The lossless carrier is deliberately NOT copied — the copy re-emits from the
// model into a fresh document, which is exactly right for a DERIVED file: the
// bundle's sidecar is a new artefact, and carrying the original's untouched
// [overrides] lines into it would put back the rows this function exists to
// remove.
func StripForAO2(s *Sidecar) *Sidecar {
	if s == nil {
		return nil
	}
	out := *s
	out.doc = nil
	out.Overrides = nil
	out.Rotations = nil
	// The slices that remain are shared with the original and are only READ from
	// here on (Bytes walks them). Copying them would be defensive against a
	// mutation nothing performs.
	return &out
}

// LostToAO2 names, by id and kind, everything an AO2-Client will not show. It is
// the body of ASYNCAO-LOST.txt.
//
// EVERY LINE IS ACTIONABLE OR IT IS NOT WRITTEN. "Your theme is different in
// AO2" helps nobody; "[element.butterflies] (image) — AsyncAO free elements have
// no AO2 equivalent" tells the reader exactly what they are missing and lets the
// author decide whether it mattered.
func LostToAO2(s *Sidecar) []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, 16)
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

// LostReport renders LostToAO2 as the file's whole text, header included.
//
// The header explains itself to somebody who has never heard of AsyncAO, because
// that is exactly who finds this file: a person who unzipped a theme a friend
// sent them and is reading the odd shouty text file in it.
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
