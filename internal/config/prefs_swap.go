package config

import (
	"encoding/json"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// presencePrepass returns the present JSON key paths; a JSON null counts as absent.
func presencePrepass(data []byte) map[string]bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	out := map[string]bool{}
	collectPresence(raw, "", out)
	return out
}

func collectPresence(raw map[string]json.RawMessage, prefix string, out map[string]bool) {
	for k, v := range raw {
		if strings.TrimSpace(string(v)) == "null" {
			continue // null == absent
		}
		key := prefix + k
		out[key] = true
		var nested map[string]json.RawMessage
		if json.Unmarshal(v, &nested) == nil {
			collectPresence(nested, key+".", out)
		}
	}
}

// snapshotLoad overlays the file on defaults, migrates and normalizes; a decode error returns untouched defaults.
func snapshotLoad(data []byte) (*AssetPreferences, error) {
	p := defaultPrefs("")
	s := p.settings.Load()
	if err := json.Unmarshal(data, s); err != nil {
		return p, err
	}
	presence := presencePrepass(data)
	migrateLegacyKeys(s, data, presence)
	if p.normalizeSnapshot(s, presence) {
		p.pending.Store(true) // the one-shot stamps changed state; persist it
	}
	return p, nil
}

// legacyKey is one old-to-current key migration; migrate gets raw bytes as the struct lacks the old keys.
type legacyKey struct {
	migrate func(s *Settings, data []byte, presence map[string]bool)
}

// legacyKeys apply in order; a migration writes the new field only when the new key is absent, so it never overwrites.
var legacyKeys = []legacyKey{
	{
		// layoutPresets to layoutProfiles, one way: presets wrap into Classic-only profiles unless a profile exists.
		migrate: func(s *Settings, data []byte, presence map[string]bool) {
			if presence["layoutProfiles"] || !presence["layoutPresets"] {
				return
			}
			var raw struct {
				LayoutPresets map[string]map[string][4]float64 `json:"layoutPresets"`
			}
			if json.Unmarshal(data, &raw) != nil {
				return
			}
			legacy := sanitizeLayoutPresets(raw.LayoutPresets)
			if len(legacy) == 0 {
				return
			}
			s.LayoutProfiles = make(map[string]LayoutProfile, len(legacy))
			for name, m := range legacy {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				s.LayoutProfiles[name] = LayoutProfile{Classic: m}
			}
		},
	},
}

func migrateLegacyKeys(s *Settings, data []byte, presence map[string]bool) {
	for _, lk := range legacyKeys {
		lk.migrate(s, data, presence)
	}
}

// normalizeSnapshot applies stamps, default resets and sanitizers; normalizeSettings clamps ranges last.
func (p *AssetPreferences) normalizeSnapshot(s *Settings, presence map[string]bool) (changed bool) {
	def := defaultSettings()

	// Explicit zero keeps the default.
	if s.ViewportPct == 0 {
		s.ViewportPct = def.ViewportPct
	}
	if s.ChatScalePct == 0 {
		s.ChatScalePct = def.ChatScalePct
	}
	if s.ChatBoxPct == 0 {
		s.ChatBoxPct = def.ChatBoxPct
	}
	if s.LogScalePct == 0 {
		s.LogScalePct = def.LogScalePct
	}
	if !presence["oocScalePercent"] || s.OOCScalePct == 0 {
		// Inherit the IC log size once so the OOC box doesn't jump, whether the key is absent or zero.
		s.OOCScalePct = s.LogScalePct
	}
	if s.InputHeightPct == 0 {
		s.InputHeightPct = def.InputHeightPct
	}
	if s.UIScalePct == 0 {
		s.UIScalePct = def.UIScalePct
	}
	if s.HoldClearMs == 0 {
		s.HoldClearMs = def.HoldClearMs
	}
	if s.TextCrawlMs == 0 {
		s.TextCrawlMs = def.TextCrawlMs
	}
	if s.BlipRate == 0 {
		s.BlipRate = def.BlipRate
	}
	if s.ThemeFitZoom == 0 {
		s.ThemeFitZoom = def.ThemeFitZoom
	}

	// Invalid or empty enums and strings keep the default.
	if s.ChromeShapeKey != chromeShapeRounded && s.ChromeShapeKey != chromeShapePill {
		s.ChromeShapeKey = def.ChromeShapeKey
	}
	if s.HoldClearKey == "" {
		s.HoldClearKey = def.HoldClearKey
	}
	// Strip traversal from the subtheme folder.
	s.SubthemeName = theme.SanitizeSubtheme(s.SubthemeName)

	// Explicit zero keeps the default.
	if s.BgSlideshowSecs <= 0 {
		s.BgSlideshowSecs = def.BgSlideshowSecs
	}

	// One-shot migration stamps.
	if !presence["spriteScalingMigrated"] || !s.SpriteScalingMigrated {
		s.SpriteScaling = defaultSpriteScaling
		s.SpriteScalingMigrated = true
		changed = true // persist the stamp promptly so the move never re-fires
	}
	if presence["autoReconnectDefaultMigrated"] && s.AutoReconnectDefaultMigrated {
		// Already migrated: the on-disk value stands.
	} else {
		s.AutoReconnect = false
		s.AutoReconnectDefaultMigrated = true
		changed = true
	}
	// Older files have catchUpWhenBehind:true baked in, so only the absent stamp tells the old default from an opt-in.
	if presence["catchUpDefaultMigrated"] && s.CatchUpDefaultMigrated {
		// already migrated: the on-disk value is honoured as-is
	} else {
		s.CatchUpOn = defaultCatchUpWhenBehind
		s.CatchUpDefaultMigrated = true
		changed = true
	}
	// The migration reads the on-disk themeFit (zero when absent), not the default the overlay left there.
	effectiveThemeFit := s.ThemeFit
	if !presence["themeFit"] {
		effectiveThemeFit = 0
	}
	if presence["themeFitDefaultMigrated"] && s.ThemeFitDefaultMigrated {
		s.ThemeFit = value.Clamp(effectiveThemeFit, ThemeFitStretch, themeFitModeMax)
	} else {
		saved := value.Clamp(effectiveThemeFit, ThemeFitStretch, themeFitModeMax)
		if saved == ThemeFitStretch {
			// Only a file still on the legacy Stretch default moves to Native; every unmigrated file gets the stamp.
			s.ThemeFit = defaultThemeFit
			p.themeFitMovedToNative = true
		} else {
			s.ThemeFit = saved
		}
		s.ThemeFitDefaultMigrated = true
		p.themeFitStampPending = true
		changed = true
	}
	if !presence["blipSpeedDefaultMigrated"] || !s.BlipSpeedDefaultMigrated {
		saved := value.Clamp(s.TextCrawlMs, MinTextCrawlMs, MaxTextCrawlMs)
		if s.TextCrawlMs == 0 || saved == preMoveTextCrawlMs {
			s.TextCrawlMs = DefaultTextCrawlMs
		} else {
			s.TextCrawlMs = saved
		}
		s.BlipSpeedDefaultMigrated = true
		changed = true
	}
	if presence["deskManifestDefaultMigrated"] && s.DeskManifestDefaultMigrated {
		// honoured as-is
	} else {
		s.DeskFollowManifest = true
		s.DeskManifestDefaultMigrated = true
		changed = true
	}
	if presence["detailedLogDefaultMigrated"] && s.DetailedLogDefaultMigrated {
		// honoured as-is
	} else {
		s.DetailedLog = true
		s.DetailedLogDefaultMigrated = true
		changed = true
	}

	// A garbage or newer sprite-load mode falls back to the default.
	if s.SpriteLoadModeVal < SpriteLoadBlank || s.SpriteLoadModeVal >= SpriteLoadModeCount {
		s.SpriteLoadModeVal = defaultSpriteLoadMode
	}

	// Restore non-zero nested defaults the overlay zeroed.
	if len(s.AssetTypes) == 0 {
		s.AssetTypes = def.AssetTypes
	} else {
		for name, tp := range s.AssetTypes {
			if len(tp.FormatOrder) == 0 {
				tp.FormatOrder = DefaultFormatOrder(name)
			}
			if name == TypeMisc && len(tp.FormatOrder) == 1 && tp.FormatOrder[0] == ExtWebP {
				tp.FormatOrder = DefaultFormatOrder(name)
			}
			s.AssetTypes[name] = tp
		}
	}
	if s.MusicHosts == nil {
		s.MusicHosts = def.MusicHosts
	} else {
		s.MusicHosts = sanitizeMusicHosts(s.MusicHosts)
	}
	if s.LearnedFormats == nil {
		s.LearnedFormats = def.LearnedFormats
	}
	// Bound, drop blank names and ids, and dedupe, so a hand-edited file cannot grow or corrupt the maps.
	s.SavedStyles = sanitizeStylePresets(s.SavedStyles)
	s.PanelFontsVal = sanitizePanelFonts(s.PanelFontsVal)
	s.UserMacros = sanitizeMacros(s.UserMacros)
	s.HiddenPanelIDs = sanitizeHiddenPanels(s.HiddenPanelIDs)
	s.ClassicLayout = sanitizeClassicLayout(s.ClassicLayout)
	s.ClassicAnchors = sanitizeClassicAnchors(s.ClassicAnchors)
	s.ClassicRotations = sanitizeClassicRotations(s.ClassicRotations)
	s.ThemeRectRotations = sanitizeThemeRectRotations(s.ThemeRectRotations)
	s.LayoutProfiles = sanitizeLayoutProfiles(s.LayoutProfiles)
	s.FontWarnThemes = clampStrings(s.FontWarnThemes, FontWarnThemesCap)
	{
		e := defaultExportOptions()
		if s.Export.HeightPx > 0 {
			e.HeightPx = value.Clamp(s.Export.HeightPx, minExportHeight, maxExportHeight)
		}
		if s.Export.FPS > 0 {
			e.FPS = value.Clamp(s.Export.FPS, minExportFPS, maxExportFPS)
		}
		if s.Export.Quality > 0 {
			e.Quality = value.Clamp(s.Export.Quality, minExportQuality, maxExportQuality)
		}
		if s.Export.TextScale > 0 {
			e.TextScale = value.Clamp(s.Export.TextScale, minExportText, maxExportText)
		}
		if s.Export.VideoFormat != "" {
			e.VideoFormat = normalizeVideoFormat(s.Export.VideoFormat)
		}
		e.Loop = s.Export.Loop
		if presence["export"] && !presence["export.loop"] {
			// A present export object without a loop key reads false, not the default.
			e.Loop = false
		}
		s.Export = e
	}
	// A present key's omitted field reads as zero, not the default; an absent key keeps the default.
	if presence["discord"] && !presence["discord.appId"] {
		s.DiscordRPC.AppID = ""
	}
	if presence["autoStatus"] {
		if !presence["autoStatus.clearWords"] {
			s.MyAutoStatus.ClearWords = ""
		}
		if !presence["autoStatus.afkWords"] {
			s.MyAutoStatus.AFKWords = ""
		}
		if !presence["autoStatus.busyWords"] {
			s.MyAutoStatus.BusyWords = ""
		}
		if !presence["autoStatus.writingWords"] {
			s.MyAutoStatus.WritingWords = ""
		}
		if !presence["autoStatus.lfrpWords"] {
			s.MyAutoStatus.LFRPWords = ""
		}
	}
	s.MyAutoStatus = sanitizeAutoStatus(s.MyAutoStatus)
	// Clamp as the setters do so getters can be plain reads; a clamp never sets changed, so nothing rewrites.
	normalizeSettings(s)
	return changed
}
