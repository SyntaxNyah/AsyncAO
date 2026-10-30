package theme

// The sidecar write check: nothing the reader could not load again reaches a file; cold path only (hard rule 2).

import (
	"errors"
	"fmt"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
)

// ErrSidecarInvalid reports a model the reader would not read back: a builder bug, not a size problem.
var ErrSidecarInvalid = errors.New("theme: sidecar model would not read back")

// Validate reports the first reason ParseSidecar would refuse or drop part of what Bytes will write.
func (s *Sidecar) Validate() error {
	if s == nil {
		return nil
	}
	if err := s.validateCaps(); err != nil {
		return err
	}
	if err := s.validateIDs(); err != nil {
		return err
	}
	return s.validateText()
}

// validateCaps checks the reader's caps against the model's lengths, with capErr's message either way.
func (s *Sidecar) validateCaps() error {
	for _, c := range []struct {
		what       string
		got, limit int
	}{
		{secOverrides, len(s.Overrides), OverrideCap},
		{secRotations, len(s.Rotations), OverrideCap},
		{secFonts, len(s.Fonts), FontCap},
		{secFontBind, len(s.FontBind), FontBindCap},
		{secMedia, len(s.Media), MediaCap},
		{secSounds, len(s.Sounds), SoundCap},
		{secPanePrefix + "*", len(s.Panes), PaneCap},
		{secElementPrefix + "*", len(s.Elements), ElementCap},
		{secEffectPrefix + "*", len(s.Effects), EffectBindCap},
		{"[" + secImport + "] subthemes", len(s.Import.Subthemes), SubthemeCap},
	} {
		if c.got > c.limit {
			return capErr(c.what, c.got, c.limit)
		}
	}
	// NHosts is a plain int beside a fixed array, so a caller can set it past the end, and HostKeys would panic.
	for i := range s.Panes {
		if n := s.Panes[i].NHosts; n > PaneHostCap {
			return capErr("["+secPanePrefix+s.Panes[i].ID+"] hosts", n, PaneHostCap)
		}
	}
	return nil
}

// validateIDs enforces the id grammar and uniqueness; with two [element.x], last writes, first reads.
func (s *Sidecar) validateIDs() error {
	// One namespace per section prefix; an element and a pane may share an id.
	seen := make(map[string]struct{}, len(s.Elements)+len(s.Panes)+len(s.Media)+len(s.Effects))
	for i := range s.Elements {
		if err := checkSectionID(seen, secElementPrefix, s.Elements[i].ID); err != nil {
			return err
		}
	}
	for i := range s.Panes {
		if err := checkSectionID(seen, secPanePrefix, s.Panes[i].ID); err != nil {
			return err
		}
	}
	for i := range s.Media {
		// [media] ids are keys; the reader skips an invalid one, so writing it loses the entry from the model.
		if err := checkSectionID(seen, secMedia+iniSectionSep, s.Media[i].ID); err != nil {
			return err
		}
	}
	// [effect.<target>] names an AO2 widget, so validEffectTarget applies.
	// Duplicates matter because EffectFor returns the first while writeEffects lets the last win.
	for i := range s.Effects {
		target := s.Effects[i].Target
		if !validEffectTarget(target) {
			return fmt.Errorf("%w: [%s%s] names no AO2 widget (1 to %d runes) — the reader would preserve the section and drop the binding from the model",
				ErrSidecarInvalid, secEffectPrefix, target, AnchorRuneCap)
		}
		if err := checkSectionUnique(seen, secEffectPrefix, target); err != nil {
			return err
		}
	}
	// A clock's id is its index, so a spelling reading back elsewhere moves the group.
	// "" is editor-declared and exempt; from 1, as clock 0 is never written.
	for i := 1; i < ClockCap; i++ {
		id := s.Clocks[i].ID
		if id == "" {
			continue
		}
		if !validID(id) {
			return fmt.Errorf("%w: [%s%s] is not a valid id ([a-z0-9_-], %d max) — the reader would preserve the section and ignore the group",
				ErrSidecarInvalid, secClockPrefix, id, IDRuneCap)
		}
		if n := parse.AtoiOr(id, 0); n != i {
			return fmt.Errorf("%w: clock %d is spelled %q, which reads back as clock %d — the group would move on save",
				ErrSidecarInvalid, i, id, n)
		}
	}
	return nil
}

func checkSectionID(seen map[string]struct{}, prefix, id string) error {
	if !validID(id) {
		return fmt.Errorf("%w: [%s%s] is not a valid id ([a-z0-9_-], %d max) — the reader would skip it and the entry would be lost from the model while staying in the file",
			ErrSidecarInvalid, prefix, id, IDRuneCap)
	}
	return checkSectionUnique(seen, prefix, id)
}

// checkSectionUnique is the uniqueness half alone, for [effect.<widget key>], whose suffix is not an id.
func checkSectionUnique(seen map[string]struct{}, prefix, id string) error {
	key := prefix + id
	if _, dup := seen[key]; dup {
		return fmt.Errorf("%w: [%s] is declared twice — one section cannot carry two entries, so the last would silently overwrite the first",
			ErrSidecarInvalid, key)
	}
	seen[key] = struct{}{}
	return nil
}

// validateText applies checkRuneCap to every bounded string, so the message names the line to fix.
func (s *Sidecar) validateText() error {
	for _, r := range []struct {
		section, key, value string
		limit               int
	}{
		{secTheme, "name", s.Meta.Name, NameRuneCap},
		{secTheme, "author", s.Meta.Author, NameRuneCap},
		{secTheme, "license", s.Meta.License, NameRuneCap},
		{secTheme, "credit", s.Meta.Credit, NameRuneCap},
		{secTheme, "description", s.Meta.Description, NameRuneCap},
		{secTheme, "min_client", s.Meta.MinClient, NameRuneCap},
		{secTheme, "base", s.Meta.Base, NameRuneCap},
		{secTheme, "subtheme", s.Meta.Subtheme, NameRuneCap},
		{secTheme, "layout_preset", s.Meta.LayoutPreset, NameRuneCap},
		{secTheme, "style_preset", s.Meta.StylePreset, NameRuneCap},
		{secTheme, "generated_by", s.Meta.GeneratedBy, NameRuneCap},
		{secChrome, "shape", s.Chrome.Shape, NameRuneCap},
		{secImport, "derived_from", s.Import.DerivedFrom, NameRuneCap},
		{secImport, "derived_at", s.Import.DerivedAt, NameRuneCap},
		{secImport, "derived_hash", s.Import.DerivedHash, NameRuneCap},
	} {
		// Metadata degrades: Bytes runs degradeMeta first, and a direct Validate gives the same verdict.
		if isMetaTextKey(r.section, r.key) {
			continue
		}
		if err := checkRuneCap(r.section, r.key, r.value, r.limit); err != nil {
			return err
		}
	}
	for _, f := range s.Fonts {
		if err := checkRuneCap(secFonts, f.Family, f.File, NameRuneCap); err != nil {
			return err
		}
	}
	for _, b := range s.FontBind {
		if err := checkRuneCap(secFontBind, b.Key, b.Value, NameRuneCap); err != nil {
			return err
		}
	}
	for _, m := range s.Media {
		if err := checkRuneCap(secMedia, m.ID, m.File, NameRuneCap); err != nil {
			return err
		}
	}
	for _, kv := range s.Sounds {
		if err := checkRuneCap(secSounds, kv.Key, kv.Value, NameRuneCap); err != nil {
			return err
		}
	}
	for i := range s.Elements {
		if err := validateElementText(&s.Elements[i]); err != nil {
			return err
		}
	}
	return nil
}

// validateElementText checks one element's six bounded strings against readElement's caps.
func validateElementText(e *Element) error {
	sec := secElementPrefix + e.ID
	for _, r := range []struct {
		key, value string
		limit      int
	}{
		{"anchor", e.Anchor, AnchorRuneCap},
		{"media", e.Media, IDRuneCap},
		{"shape", e.Shape, IDRuneCap},
		{"generator", e.Gen, IDRuneCap},
		{elementTextKey, e.Text, TextRuneCap},
		{"font", e.Font, NameRuneCap},
	} {
		if err := checkRuneCap(sec, r.key, r.value, r.limit); err != nil {
			return err
		}
	}
	// genParamsOf bounds each half of the pair independently.
	for i := 0; i < e.GenParamCount(); i++ {
		p := e.GenParams[i]
		if err := checkRuneCap(sec, "gen_params", p.Key, GenParamRuneCap); err != nil {
			return err
		}
		if err := checkRuneCap(sec, "gen_params", p.Value, GenParamRuneCap); err != nil {
			return err
		}
	}
	return checkRuneCap(sec, "visible_when", e.VisibleWhen.Value, ConditionValueRuneCap)
}
