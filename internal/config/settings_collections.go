package config

import "strings"

// Collection mutators on *Settings; the *AssetPreferences callers normalize input, run Update and bump generations.

// AddCallWord appends a normalized highlight word, deduped and capped; reports whether it grew.
func (s *Settings) AddCallWord(w string) bool {
	var ok bool
	s.CallWordList, ok = addExactUnique(s.CallWordList, w, callWordCap)
	return ok
}

// RemoveCallWord drops a highlight word; the caller lowercases. Reports a change.
func (s *Settings) RemoveCallWord(word string) bool {
	var changed bool
	s.CallWordList, changed = removeExact(s.CallWordList, word)
	return changed
}

// AddShownamePreset appends a showname, fold-deduped and capped; reports a change.
func (s *Settings) AddShownamePreset(name string) bool {
	var changed bool
	s.ShownamePresets, changed = addFoldUnique(s.ShownamePresets, name, ShownameCap)
	return changed
}

// RemoveShownamePreset drops a showname and any key bound to it; reports a change.
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

// MuteSFX appends an SFX name, deduped and capped; reports a change.
func (s *Settings) MuteSFX(name string) bool {
	var changed bool
	s.MutedSFX, changed = addExactUnique(s.MutedSFX, name, mutedSFXCap)
	return changed
}

// UnmuteSFX removes an SFX name; reports a change.
func (s *Settings) UnmuteSFX(name string) bool {
	var changed bool
	s.MutedSFX, changed = removeExact(s.MutedSFX, name)
	return changed
}

// ToggleSfxFavorite flips a star and reports the new state; false at the cap.
func (s *Settings) ToggleSfxFavorite(name string) bool {
	var removed, nowFav bool
	s.SfxFavorites, removed = removeExact(s.SfxFavorites, name)
	if removed {
		return false
	}
	s.SfxFavorites, nowFav = addExactUnique(s.SfxFavorites, name, sfxFavoritesCap)
	return nowFav
}

// AddModDuration appends a duration token, deduped and capped; reports a change.
func (s *Settings) AddModDuration(token string) bool {
	var changed bool
	s.ModDurations, changed = addFoldUnique(s.ModDurations, token, modDurationsCap)
	return changed
}

// RemoveModDuration drops a duration token; reports a change.
func (s *Settings) RemoveModDuration(token string) bool {
	var changed bool
	s.ModDurations, changed = removeFold(s.ModDurations, token)
	return changed
}

// AddModReasonTemplate appends a template after seeding the built-ins; reports a change.
func (s *Settings) AddModReasonTemplate(v string) bool {
	seedModTemplates(s)
	var changed bool
	s.ModReasonTemplates, changed = addFoldUnique(s.ModReasonTemplates, v, modReasonTemplatesCap)
	return changed
}

// RemoveModReasonTemplate drops a template after seeding the built-ins; reports a change.
func (s *Settings) RemoveModReasonTemplate(v string) bool {
	seedModTemplates(s)
	var changed bool
	s.ModReasonTemplates, changed = removeFold(s.ModReasonTemplates, v)
	return changed
}

// AddMusicHost appends an entry, deduped and capped; reports a change.
func (s *Settings) AddMusicHost(lbl string) bool {
	var changed bool
	s.MusicHosts, changed = addExactUnique(s.MusicHosts, lbl, musicHostsCap)
	return changed
}

// RemoveMusicHost removes an entry; reports a change.
func (s *Settings) RemoveMusicHost(label string) bool {
	var changed bool
	s.MusicHosts, changed = removeExact(s.MusicHosts, label)
	return changed
}

// AddFavorite upserts a server by URL; the caller checks url != "".
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

// UpdateFavorite edits in place; false when oldURL is absent or newURL is another entry's (no silent merge).
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

// AddStylePreset overwrites by name or appends up to the cap; the caller normalizes pr.
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

// DeleteStylePreset removes the preset at i, bounds-checked.
func (s *Settings) DeleteStylePreset(i int) {
	if i >= 0 && i < len(s.SavedStyles) {
		s.SavedStyles = append(s.SavedStyles[:i], s.SavedStyles[i+1:]...)
	}
}

// SetStylePresetKey binds or, with "", clears a key, unbinding it elsewhere so it maps to one preset.
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

// MarkFontWarnShown records a warned theme, fold-deduped, keeping the most recent; the caller trims theme.
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

// SetPanelFont stores an override; an empty one removes the entry so a later default change still reaches the user.
func (s *Settings) SetPanelFont(id string, pf PanelFont) {
	if pf.Family == "" && pf.SizePx == 0 {
		delete(s.PanelFontsVal, id)
		if len(s.PanelFontsVal) == 0 {
			s.PanelFontsVal = nil
		}
		return
	}
	const panelFontsInitialCap = 4 // a typical user overrides a few panels
	if s.PanelFontsVal == nil {
		s.PanelFontsVal = make(map[string]PanelFont, panelFontsInitialCap)
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

// SetShownameKeyBind binds a bare key to a showname, "" clears, capped; the caller normalizes key.
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

// SetICPhraseKey binds a key to a canned IC line, "" clears, capped; the caller normalizes both.
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

// SetBlipVolume stores a character's volume, the default removes the entry; reports a change.
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

// ToggleEmoteFav flips a favourite, capped, and returns the new state; removing the last drops the character.
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

// SetClassicSlot stores one default-courtroom slot's override, capped.
func (s *Settings) SetClassicSlot(name string, frac [4]float64) {
	if s.ClassicLayout == nil {
		s.ClassicLayout = map[string][4]float64{}
	}
	if _, exists := s.ClassicLayout[name]; !exists && len(s.ClassicLayout) >= classicSlotCap {
		return
	}
	s.ClassicLayout[name] = frac
}

// ClearClassicSlot drops a slot's override; "" clears every slot with its anchors and rotations.
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

// SetClassicAnchor pins a slot to a window reference, capped.
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

// SetClassicRotation stores a slot's rotation angle byte, capped.
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

// SaveLayoutProfile stores a profile, capped; the caller sanitizes and rejects empty ones.
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

// SetThemeRectOverride stores a widget's edit, capped.
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

// ClearThemeRectOverride drops a widget's edit ("" = the whole theme's) and its rotation with it.
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

// SetThemeRectRotation stores a widget's rotation angle byte, capped.
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

// ClearThemeRectRotation drops a widget's rotation ("" = the whole theme's).
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
