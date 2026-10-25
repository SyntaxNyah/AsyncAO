package theme

// THE METADATA DEGRADE (v1.90.0 W8) — docs/THEME-FORMAT.md §7 "Caps".
// ONE SPELLING, BOTH DIRECTIONS. metaTextFields is the list, and it is the only
// list: the reader degrades through it after the parse, the writer degrades
// through it before Validate, and Validate skips exactly the (section, key)

import "unicode/utf8"

// metaTextField is one metadata-class value: where it lives in the file, and the
// model field it lands in.
//
// A POINTER ACCESSOR rather than a value, because this table is the WRITE path
// too — degradeMeta has to shorten the field, not a copy of it.
type metaTextField struct {
	section, key string
	ptr          func(*Sidecar) *string
}

// metaTextFields is THE metadata class, complete.
// Membership is decided by one question: does anything but a human eye ever read
// this string? `base` and `subtheme` are absent because both name a FOLDER the
// loader resolves; `[element.*] text` is absent because it is painted into the
// courtroom, so a shortened one is a visibly different theme rather than a
// shortened caption about one.
var metaTextFields = [...]metaTextField{
	{secTheme, "name", func(s *Sidecar) *string { return &s.Meta.Name }},
	{secTheme, "author", func(s *Sidecar) *string { return &s.Meta.Author }},
	{secTheme, "license", func(s *Sidecar) *string { return &s.Meta.License }},
	{secTheme, "credit", func(s *Sidecar) *string { return &s.Meta.Credit }},
	{secTheme, "description", func(s *Sidecar) *string { return &s.Meta.Description }},
	// min_client is informational and never enforced (§3 [theme]), so an over-long
	// one is a comment, not a constraint.
	{secTheme, "min_client", func(s *Sidecar) *string { return &s.Meta.MinClient }},
	{secTheme, "layout_preset", func(s *Sidecar) *string { return &s.Meta.LayoutPreset }},
	{secTheme, "style_preset", func(s *Sidecar) *string { return &s.Meta.StylePreset }},
	{secTheme, "generated_by", func(s *Sidecar) *string { return &s.Meta.GeneratedBy }},
	// [import] is provenance in its entirety: who this was derived from, when, and
	// the hash that proves it. Losing a whole theme over a long upstream name would
	// be the ruling's own headline case wearing a different section header.
	{secImport, "derived_from", func(s *Sidecar) *string { return &s.Import.DerivedFrom }},
	{secImport, "derived_at", func(s *Sidecar) *string { return &s.Import.DerivedAt }},
	{secImport, "derived_hash", func(s *Sidecar) *string { return &s.Import.DerivedHash }},
}

// isMetaTextKey reports whether a (section, key) pair degrades rather than
// refuses. Validate's length walk asks it, so the two directions cannot drift.
func isMetaTextKey(section, key string) bool {
	for _, f := range metaTextFields {
		if f.section == section && f.key == key {
			return true
		}
	}
	return false
}

// degradeMeta truncates every metadata-class value to NameRuneCap, recording one
// note per value it shortened.
// IDEMPOTENT — a value already inside the cap is untouched and notes nothing, so
// a document saved twice does not grow a second copy of the same note.
func (s *Sidecar) degradeMeta() {
	if s == nil {
		return
	}
	for _, f := range metaTextFields {
		p := f.ptr(s)
		cut, dropped := truncMetaRunes(*p)
		if dropped == 0 {
			continue
		}
		*p = cut
		// DEGRADE path: NoteCap bounds the report, not the load (see note).
		_ = s.note("[%s] %s is %d runes, %d over the %d-rune limit — it is shortened for display and the file is kept as written",
			f.section, f.key, utf8.RuneCountInString(cut)+dropped, dropped, NameRuneCap)
	}
}

// truncMetaRunes cuts v to NameRuneCap RUNES, returning the cut string and how
// many runes were dropped (0 when it fitted).
// BY RUNES, which is the format's own unit: a byte truncation would cut a
// multi-byte rune in half and write invalid UTF-8 into a value the writer then
// has to re-encode.
func truncMetaRunes(v string) (string, int) {
	n := utf8.RuneCountInString(v)
	if n <= NameRuneCap {
		return v, 0
	}
	seen := 0
	for idx := range v {
		if seen == NameRuneCap {
			return v[:idx], n - NameRuneCap
		}
		seen++
	}
	// Unreachable: n > NameRuneCap guarantees a rune boundary at index NameRuneCap.
	return v, 0
}
