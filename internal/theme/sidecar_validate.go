package theme

// The sidecar WRITE GUARD — the one thing standing between a live editor model
// and a theme that cannot be loaded again.

import (
	"errors"
	"fmt"
)

// ErrSidecarInvalid reports a MODEL the writer refuses because the reader would
// not read it back as itself: a bad id, or two entries claiming one section.
// It is distinct from ErrSidecarCap because the fix is different and the blame
// is different: a cap refusal says "this theme is too big", this one says "the
// thing that built this model produced something the format cannot express".
var ErrSidecarInvalid = errors.New("theme: sidecar model would not read back")

// Validate reports the FIRST reason ParseSidecar would refuse — or silently
// drop part of — the file Bytes() is about to write.
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

// validateCaps asks the reader's cap question of the MODEL's collection lengths.
// The names and the message come from capErr, so a refusal reads identically
// whichever direction produced it.
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
	// One pane's host list. NHosts is a plain int beside a fixed array, so a
	// caller can set it past the array and walk off the end of the format (the
	// writer's own HostKeys would panic first, which is not a diagnosis).
	for i := range s.Panes {
		if n := s.Panes[i].NHosts; n > PaneHostCap {
			return capErr("["+secPanePrefix+s.Panes[i].ID+"] hosts", n, PaneHostCap)
		}
	}
	return nil
}

// validateIDs enforces the id grammar and, on top of it, UNIQUENESS — which is
// a writer-only rule and the one invariant here the reader cannot state.
func (s *Sidecar) validateIDs() error {
	// One namespace per section prefix — an element and a pane may share an id,
	// because [element.x] and [pane.x] are different sections.
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
		// [media] ids are KEYS, not sections; the reader skips an invalid one
		// with a note and keeps the row in the file, so writing one loses the
		// entry from the model exactly as an invalid section name does.
		if err := checkSectionID(seen, secMedia+iniSectionSep, s.Media[i].ID); err != nil {
			return err
		}
	}
	// [effect.<target>] is the one section family whose suffix is NOT an id: it
	// names an AO2 widget, so the reader's rule is validEffectTarget, not validID.
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
	// A clock's id is the section suffix AND its index: the reader resolves
	// [clock.7] to slot 7, so a spelling that does not read back as the slot it
	// sits in silently moves the whole group. "" is the editor-declared case and
	// is written in the canonical spelling (writeClocks), so it is exempt.
	for i := 1; i < ClockCap; i++ {
		id := s.Clocks[i].ID
		if id == "" {
			continue
		}
		if !validID(id) {
			return fmt.Errorf("%w: [%s%s] is not a valid id ([a-z0-9_-], %d max) — the reader would preserve the section and ignore the group",
				ErrSidecarInvalid, secClockPrefix, id, IDRuneCap)
		}
		if n := atoiTrim(id); n != i {
			return fmt.Errorf("%w: clock %d is spelled %q, which reads back as clock %d — the group would move on save",
				ErrSidecarInvalid, i, id, n)
		}
	}
	return nil
}

// checkSectionID is the id grammar plus uniqueness for one namespace.
func checkSectionID(seen map[string]struct{}, prefix, id string) error {
	if !validID(id) {
		return fmt.Errorf("%w: [%s%s] is not a valid id ([a-z0-9_-], %d max) — the reader would skip it and the entry would be lost from the model while staying in the file",
			ErrSidecarInvalid, prefix, id, IDRuneCap)
	}
	return checkSectionUnique(seen, prefix, id)
}

// checkSectionUnique is the uniqueness half on its own, for the one section
// family whose suffix is not an id ([effect.<widget key>]): the grammar differs,
// the collision does not — one section still cannot carry two entries.
func checkSectionUnique(seen map[string]struct{}, prefix, id string) error {
	key := prefix + id
	if _, dup := seen[key]; dup {
		return fmt.Errorf("%w: [%s] is declared twice — one section cannot carry two entries, so the last would silently overwrite the first",
			ErrSidecarInvalid, key)
	}
	seen[key] = struct{}{}
	return nil
}

// validateText asks checkRuneCap — the reader's own length rule — of every
// bounded string in the model, in the reader's own (section, key) terms so the
// message names the line the author has to fix.
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
		// THE METADATA CLASS DEGRADES, IT DOES NOT REFUSE (metatext.go). Bytes runs
		// degradeMeta immediately before this, so these values are already inside the
		// cap; skipping them here is what makes that the ONE rule rather than two
		// disagreeing ones — a direct Validate() call on a hand-built model must give
		// the same verdict the save gives, and the save's verdict is "shortened, noted,
		// written". The rows stay listed so the census can see the whole surface and so
		// the skip is visible beside what it skips.
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

// validateElementText is one element's six bounded strings, each against the cap
// readElement checks it with.
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
	// gen_params: the pair is written as one "k=v, k=v" value, and genParamsOf
	// bounds each half independently.
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
