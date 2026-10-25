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

// AddFavorite stars a server (upsert by URL): an existing entry is renamed/re-described,
// otherwise a new entry is appended. The caller pre-checks url != "".
func (s *Settings) AddFavorite(name, url, description string) {
	for i, f := range s.Favorites {
		if f.URL == url {
			if f.Name == name && f.Description == description {
				return
			}
			s.Favorites[i].Name = name
			s.Favorites[i].Description = description
			return
		}
	}
	s.Favorites = append(s.Favorites, FavoriteServer{Name: name, URL: url, Description: description})
}

// RemoveFavorite unstars the server with the given URL.
func (s *Settings) RemoveFavorite(url string) {
	kept := s.Favorites[:0]
	for _, f := range s.Favorites {
		if f.URL == url {
			continue
		}
		kept = append(kept, f)
	}
	s.Favorites = kept
}

// UpdateFavorite renames/re-addresses an existing phone-book entry, preserving its
// position. Reports false when oldURL is absent, or when newURL already belongs to a
// DIFFERENT entry (a silent merge would surprise). A no-op edit reports true.
func (s *Settings) UpdateFavorite(oldURL, newName, newURL, description string) bool {
	idx := -1
	for i, f := range s.Favorites {
		if f.URL == oldURL {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	if newURL != oldURL {
		for i, f := range s.Favorites {
			if i != idx && f.URL == newURL {
				return false
			}
		}
	}
	cur := s.Favorites[idx]
	if cur.Name == newName && cur.URL == newURL && cur.Description == description {
		return true
	}
	s.Favorites[idx].Name = newName
	s.Favorites[idx].URL = newURL
	s.Favorites[idx].Description = description
	return true
}

// AddStylePreset stores a preset by name (overwrite in place, else append bounded by
// stylePresetCap). The caller pre-normalizes pr (trim, clamp opacity, lower key).
func (s *Settings) AddStylePreset(pr StylePreset) {
	for i := range s.SavedStyles {
		if s.SavedStyles[i].Name == pr.Name {
			pr.Key = s.SavedStyles[i].Key // keep an existing key binding across an overwrite
			s.SavedStyles[i] = pr
			return
		}
	}
	if len(s.SavedStyles) >= stylePresetCap {
		return
	}
	s.SavedStyles = append(s.SavedStyles, pr)
}

// DeleteStylePreset removes the preset at i (bounds-checked).
func (s *Settings) DeleteStylePreset(i int) {
	if i >= 0 && i < len(s.SavedStyles) {
		s.SavedStyles = append(s.SavedStyles[:i], s.SavedStyles[i+1:]...)
	}
}

// SetStylePresetKey binds (or clears, key == "") a key to preset i, unbinding it from any
// other preset first so a key maps to exactly one mood. The caller lowercases/trims key.
func (s *Settings) SetStylePresetKey(i int, key string) {
	if i < 0 || i >= len(s.SavedStyles) {
		return
	}
	if key != "" {
		for j := range s.SavedStyles {
			if j != i && s.SavedStyles[j].Key == key {
				s.SavedStyles[j].Key = ""
			}
		}
	}
	s.SavedStyles[i].Key = key
}

// MarkFontWarnShown records a theme as warned (fold-deduped, keeps the most recent
// FontWarnThemesCap). The caller trims theme.
// Mutation: change the keep-tail slice to keep-head and TestFontWarnThemesCap fails.
func (s *Settings) MarkFontWarnShown(theme string) {
	for _, t := range s.FontWarnThemes {
		if strings.EqualFold(t, theme) {
			return
		}
	}
	s.FontWarnThemes = append(s.FontWarnThemes, theme)
	if len(s.FontWarnThemes) > FontWarnThemesCap {
		s.FontWarnThemes = s.FontWarnThemes[len(s.FontWarnThemes)-FontWarnThemesCap:]
	}
}

// ClearFontWarnThemes forgets every warned theme, so the warnings come back.
func (s *Settings) ClearFontWarnThemes() {
	s.FontWarnThemes = nil
}

// SetPanelFont stores one element's override (an empty override removes the entry rather
// than storing a zero value, so a later default change still reaches the user). The
// caller normalizes id and clamps pf.
func (s *Settings) SetPanelFont(id string, pf PanelFont) {
	if pf.Family == "" && pf.SizePx == 0 {
		delete(s.PanelFontsVal, id)
		if len(s.PanelFontsVal) == 0 {
			s.PanelFontsVal = nil
		}
		return
	}
	if s.PanelFontsVal == nil {
		s.PanelFontsVal = make(map[string]PanelFont, 4)
	}
	if len(s.PanelFontsVal) >= PanelFontMaxEntries {
		if _, replacing := s.PanelFontsVal[id]; !replacing {
			return // full, and this would be a new key
		}
	}
	s.PanelFontsVal[id] = pf
}

// ClearPanelFonts drops every per-panel override at once.
func (s *Settings) ClearPanelFonts() {
	s.PanelFontsVal = nil
}

// SetShownameKeyBind binds a bare key to a showname preset (an empty showname clears it),
// bounded by shownameKeyCap. The caller lowercases/trims key.
// Mutation: drop the len >= shownameKeyCap guard and TestShownameKeyCap fails.
func (s *Settings) SetShownameKeyBind(key, showname string) {
	if showname == "" {
		delete(s.ShownameKeys, key)
		return
	}
	if s.ShownameKeys == nil {
		s.ShownameKeys = map[string]string{}
	}
	if _, exists := s.ShownameKeys[key]; !exists && len(s.ShownameKeys) >= shownameKeyCap {
		return
	}
	s.ShownameKeys[key] = showname
}

// SetICPhraseKey binds key to a canned IC line (an empty phrase clears it), bounded by
// icPhraseKeyCap. The caller lowercases/trims key and trims phrase.
func (s *Settings) SetICPhraseKey(key, phrase string) {
	if phrase == "" {
		delete(s.ICPhraseKeys, key)
		return
	}
	if s.ICPhraseKeys == nil {
		s.ICPhraseKeys = map[string]string{}
	}
	if _, exists := s.ICPhraseKeys[key]; !exists && len(s.ICPhraseKeys) >= icPhraseKeyCap {
		return
	}
	s.ICPhraseKeys[key] = phrase
}

// SetHotkey records the key bound to an action.
func (s *Settings) SetHotkey(action, key string) {
	if s.HotkeyMap == nil {
		s.HotkeyMap = map[string]string{}
	}
	if s.HotkeyMap[action] == key {
		return
	}
	s.HotkeyMap[action] = key
}

// SetBlipVolume stores one character's typing-blip volume (the default value removes the
// entry). Reports a change. The caller lowercases/trims char and clamps pct.
// Mutation: change the "default removes" branch to store and TestBlipVolumeDefaultRemoves fails.
func (s *Settings) SetBlipVolume(char string, pct int) bool {
	cur, had := s.BlipVols[char]
	if pct == blipVolumeDefault {
		if !had {
			return false // already default
		}
		delete(s.BlipVols, char)
		return true
	}
	if had && cur == pct {
		return false // unchanged
	}
	if !had && len(s.BlipVols) >= blipVolsCap {
		return false // bounded
	}
	if s.BlipVols == nil {
		s.BlipVols = make(map[string]int)
	}
	s.BlipVols[char] = pct
	return true
}

// ToggleEmoteFav flips an emote index's favourite state for a character and returns the
// NEW state (true = now favourited). Bounded by the per-char and char-count caps; removing
// the last favourite drops the character's entry. The caller lowercases/trims char and
// pre-checks idx >= 0.
func (s *Settings) ToggleEmoteFav(char string, idx int) bool {
	list := s.EmoteFavs[char]
	if !containsInt(list, idx) {
		if len(list) >= emoteFavPerCharCap {
			return false
		}
		if _, had := s.EmoteFavs[char]; !had && len(s.EmoteFavs) >= emoteFavCharsCap {
			return false
		}
	}
	list, nowIn := toggleInt(list, idx)
	if !nowIn { // removed
		if len(list) == 0 {
			delete(s.EmoteFavs, char)
		} else {
			s.EmoteFavs[char] = list
		}
		return false
	}
	if s.EmoteFavs == nil {
		s.EmoteFavs = make(map[string][]int)
	}
	s.EmoteFavs[char] = list
	return true
}

// SetClassicSlot stores one default-courtroom slot's override (bounded by classicSlotCap).
func (s *Settings) SetClassicSlot(name string, frac [4]float64) {
	if s.ClassicLayout == nil {
		s.ClassicLayout = map[string][4]float64{}
	}
	if _, exists := s.ClassicLayout[name]; !exists && len(s.ClassicLayout) >= classicSlotCap {
		return
	}
	s.ClassicLayout[name] = frac
}

// ClearClassicSlot drops one slot's override (name "" clears every slot plus its anchors
// and rotations, the editor's "Reset all").
func (s *Settings) ClearClassicSlot(name string) {
	if name == "" {
		s.ClassicLayout = nil
		s.ClassicAnchors = nil
		s.ClassicRotations = nil
		return
	}
	delete(s.ClassicLayout, name)
	delete(s.ClassicAnchors, name)
	delete(s.ClassicRotations, name)
}

// SetClassicAnchor pins one slot to a window reference (bounded by classicSlotCap).
func (s *Settings) SetClassicAnchor(name string, a ClassicAnchor) {
	if s.ClassicAnchors == nil {
		s.ClassicAnchors = map[string]ClassicAnchor{}
	}
	if _, exists := s.ClassicAnchors[name]; !exists && len(s.ClassicAnchors) >= classicSlotCap {
		return
	}
	s.ClassicAnchors[name] = a
}

// ClearClassicAnchor drops one slot's window pin.
func (s *Settings) ClearClassicAnchor(name string) {
	delete(s.ClassicAnchors, name)
}

// SetClassicRotation stores one slot's rotation angle byte (bounded by classicSlotCap).
func (s *Settings) SetClassicRotation(name string, deg uint8) {
	if s.ClassicRotations == nil {
		s.ClassicRotations = map[string]uint8{}
	}
	if _, exists := s.ClassicRotations[name]; !exists && len(s.ClassicRotations) >= classicSlotCap {
		return
	}
	s.ClassicRotations[name] = deg
}

// ClearClassicRotation drops one slot's rotation.
func (s *Settings) ClearClassicRotation(name string) {
	delete(s.ClassicRotations, name)
}

// SaveLayoutProfile stores a sanitized profile (bounded by layoutProfileCap). The caller
// sanitizes prof and rejects empty profiles.
func (s *Settings) SaveLayoutProfile(name string, prof LayoutProfile) {
	if s.LayoutProfiles == nil {
		s.LayoutProfiles = map[string]LayoutProfile{}
	}
	if _, exists := s.LayoutProfiles[name]; !exists && len(s.LayoutProfiles) >= layoutProfileCap {
		return
	}
	s.LayoutProfiles[name] = prof
}

// DeleteLayoutProfile removes a saved profile by name.
func (s *Settings) DeleteLayoutProfile(name string) {
	if s.LayoutProfiles != nil {
		delete(s.LayoutProfiles, name)
		if len(s.LayoutProfiles) == 0 {
			s.LayoutProfiles = nil
		}
	}
}

// SetThemeRectOverride stores one widget's edit (bounded by themeOvThemesCap/themeOvRectsCap).
func (s *Settings) SetThemeRectOverride(theme, key string, r [4]int) {
	if s.ThemeRectOv == nil {
		s.ThemeRectOv = map[string]map[string][4]int{}
	}
	m, ok := s.ThemeRectOv[theme]
	if !ok {
		if len(s.ThemeRectOv) >= themeOvThemesCap {
			return
		}
		m = map[string][4]int{}
		s.ThemeRectOv[theme] = m
	}
	if _, exists := m[key]; !exists && len(m) >= themeOvRectsCap {
		return
	}
	m[key] = r
}

// ClearThemeRectOverride drops one widget's edit (key "" = the whole theme's edits); the
// matching rotation dies with its override (A4), mirroring the classic anchor coupling.
func (s *Settings) ClearThemeRectOverride(theme, key string) {
	if m, ok := s.ThemeRectOv[theme]; ok {
		if key == "" {
			delete(s.ThemeRectOv, theme)
		} else {
			delete(m, key)
			if len(m) == 0 {
				delete(s.ThemeRectOv, theme)
			}
		}
	}
	if rm, ok := s.ThemeRectRotations[theme]; ok {
		if key == "" {
			delete(s.ThemeRectRotations, theme)
		} else {
			delete(rm, key)
			if len(rm) == 0 {
				delete(s.ThemeRectRotations, theme)
			}
		}
	}
}

// SetThemeRectRotation stores one widget's rotation angle byte (bounded by the same
// themeOvThemesCap/themeOvRectsCap as the overrides).
func (s *Settings) SetThemeRectRotation(theme, key string, deg uint8) {
	if s.ThemeRectRotations == nil {
		s.ThemeRectRotations = map[string]map[string]uint8{}
	}
	m, ok := s.ThemeRectRotations[theme]
	if !ok {
		if len(s.ThemeRectRotations) >= themeOvThemesCap {
			return
		}
		m = map[string]uint8{}
		s.ThemeRectRotations[theme] = m
	}
	if _, exists := m[key]; !exists && len(m) >= themeOvRectsCap {
		return
	}
	m[key] = deg
}

// ClearThemeRectRotation drops one widget's rotation (key "" = the whole theme's).
func (s *Settings) ClearThemeRectRotation(theme, key string) {
	if m, ok := s.ThemeRectRotations[theme]; ok {
		if key == "" {
			delete(s.ThemeRectRotations, theme)
		} else {
			delete(m, key)
			if len(m) == 0 {
				delete(s.ThemeRectRotations, theme)
			}
		}
	}
}
