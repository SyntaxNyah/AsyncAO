package config

import "strings"

// Collection mutators on *Settings. Each owns ONE rule (via the shared collection
// helpers in collection_helpers.go) so the *AssetPreferences orchestration — normalize
// the input, run Update, bump any generation counter — never re-derives the
// dedupe/cap/toggle logic at a call site.

// AddCallWord appends one normalized highlight word (the caller lowercases + trims);
// exact-deduped and capped at callWordCap. Reports whether the list grew.
func (s *Settings) AddCallWord(w string) bool {
	var ok bool
	s.CallWordList, ok = addExactUnique(s.CallWordList, w, callWordCap)
	return ok
}

// RemoveCallWord drops a highlight word (exact; the caller lowercases). Reports a change.
func (s *Settings) RemoveCallWord(word string) bool {
	var changed bool
	s.CallWordList, changed = removeExact(s.CallWordList, word)
	return changed
}

// AddShownamePreset appends a showname (case-insensitive dedupe, capped). Reports change.
func (s *Settings) AddShownamePreset(name string) bool {
	var changed bool
	s.ShownamePresets, changed = addFoldUnique(s.ShownamePresets, name, ShownameCap)
	return changed
}

// RemoveShownamePreset drops a showname and any key bound to it. Reports change.
func (s *Settings) RemoveShownamePreset(name string) bool {
	var changed bool
	s.ShownamePresets, changed = removeFold(s.ShownamePresets, name)
	if changed {
		for k, v := range s.ShownameKeys {
			if strings.EqualFold(v, name) {
				delete(s.ShownameKeys, k)
			}
		}
	}
	return changed
}

// MuteSFX appends an SFX name (exact dedupe, capped). Reports change.
func (s *Settings) MuteSFX(name string) bool {
	var changed bool
	s.MutedSFX, changed = addExactUnique(s.MutedSFX, name, mutedSFXCap)
	return changed
}

// UnmuteSFX removes an SFX name (exact). Reports change.
func (s *Settings) UnmuteSFX(name string) bool {
	var changed bool
	s.MutedSFX, changed = removeExact(s.MutedSFX, name)
	return changed
}

// ToggleSfxFavorite stars an SFX name if absent (deduped, bounded) or unstars it if
// present. Reports the new state (true = now starred). A no-op (false) when at the cap.
func (s *Settings) ToggleSfxFavorite(name string) bool {
	var removed, nowFav bool
	s.SfxFavorites, removed = removeExact(s.SfxFavorites, name)
	if removed {
		return false
	}
	s.SfxFavorites, nowFav = addExactUnique(s.SfxFavorites, name, sfxFavoritesCap)
	return nowFav
}

// AddModDuration appends a custom duration token (deduped, bounded). Reports change.
func (s *Settings) AddModDuration(token string) bool {
	var changed bool
	s.ModDurations, changed = addFoldUnique(s.ModDurations, token, modDurationsCap)
	return changed
}

// RemoveModDuration drops a custom duration token. Reports change.
func (s *Settings) RemoveModDuration(token string) bool {
	var changed bool
	s.ModDurations, changed = removeFold(s.ModDurations, token)
	return changed
}

// AddModReasonTemplate appends a reason template (deduped, bounded), seeding the
// built-in list first. Reports change.
func (s *Settings) AddModReasonTemplate(v string) bool {
	seedModTemplates(s)
	var changed bool
	s.ModReasonTemplates, changed = addFoldUnique(s.ModReasonTemplates, v, modReasonTemplatesCap)
	return changed
}

// RemoveModReasonTemplate drops a reason template, seeding the built-ins first. Reports change.
func (s *Settings) RemoveModReasonTemplate(v string) bool {
	seedModTemplates(s)
	var changed bool
	s.ModReasonTemplates, changed = removeFold(s.ModReasonTemplates, v)
	return changed
}

// AddMusicHost appends a music-host entry (exact dedupe, capped). Reports change.
func (s *Settings) AddMusicHost(lbl string) bool {
	var changed bool
	s.MusicHosts, changed = addExactUnique(s.MusicHosts, lbl, musicHostsCap)
	return changed
}

// RemoveMusicHost removes a music-host entry (exact). Reports change.
func (s *Settings) RemoveMusicHost(label string) bool {
	var changed bool
	s.MusicHosts, changed = removeExact(s.MusicHosts, label)
	return changed
}
