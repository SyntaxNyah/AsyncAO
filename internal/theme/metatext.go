package theme

// Metadata degrade: metaTextFields is the only list, for reader, writer and Validate's skip.

import "unicode/utf8"

// metaTextField locates one metadata value; a pointer accessor because degradeMeta must shorten it.
type metaTextField struct {
	section, key string
	ptr          func(*Sidecar) *string
}

// metaTextFields are strings only a human reads; `base` and `subtheme` name folders.
// Element text is painted, so shortening it would change the theme.
var metaTextFields = [...]metaTextField{
	{secTheme, "name", func(s *Sidecar) *string { return &s.Meta.Name }},
	{secTheme, "author", func(s *Sidecar) *string { return &s.Meta.Author }},
	{secTheme, "license", func(s *Sidecar) *string { return &s.Meta.License }},
	{secTheme, "credit", func(s *Sidecar) *string { return &s.Meta.Credit }},
	{secTheme, "description", func(s *Sidecar) *string { return &s.Meta.Description }},
	// min_client is informational, so an over-long one is a comment.
	{secTheme, "min_client", func(s *Sidecar) *string { return &s.Meta.MinClient }},
	{secTheme, "layout_preset", func(s *Sidecar) *string { return &s.Meta.LayoutPreset }},
	{secTheme, "style_preset", func(s *Sidecar) *string { return &s.Meta.StylePreset }},
	{secTheme, "generated_by", func(s *Sidecar) *string { return &s.Meta.GeneratedBy }},
	// [import] is provenance: a long upstream name must not cost the theme.
	{secImport, "derived_from", func(s *Sidecar) *string { return &s.Import.DerivedFrom }},
	{secImport, "derived_at", func(s *Sidecar) *string { return &s.Import.DerivedAt }},
	{secImport, "derived_hash", func(s *Sidecar) *string { return &s.Import.DerivedHash }},
}

// isMetaTextKey reports whether a pair degrades rather than refuses.
func isMetaTextKey(section, key string) bool {
	for _, f := range metaTextFields {
		if f.section == section && f.key == key {
			return true
		}
	}
	return false
}

// degradeMeta truncates metadata to NameRuneCap with notes; idempotent, so a re-save adds none.
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
		_ = s.note("[%s] %s is %d runes, %d over the %d-rune limit — it is shortened for display and the file is kept as written",
			f.section, f.key, utf8.RuneCountInString(cut)+dropped, dropped, NameRuneCap)
	}
}

// truncMetaRunes cuts v to NameRuneCap runes, as a byte cut could write invalid UTF-8.
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
