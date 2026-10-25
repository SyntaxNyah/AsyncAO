package config

import (
	"encoding/json"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// presencePrepass decodes data into the set of PRESENT (non-null) JSON key paths,
// recursing into nested objectp. A JSON null counts as absent, matching the old
// pointer-field load(): a nil *T meant "key not written", so explicit null and an
// absent key were the same thing.
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

// snapshotLoad is the B0 candidate load: start from the defaults, unmarshal the file
// OVER them, run the ordered legacy-key migrations, then normalize (clamps + stamps)
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

// legacyKey is one legacy → current JSON key migration. The migrate func receives the
// raw file bytes (to decode legacy keys AssetPreferences no longer carries), plus the
// presence set so it can honour "only when the new key is absent".
type legacyKey struct {
	migrate func(s *Settings, data []byte, presence map[string]bool)
}

// legacyKeys lists the on-disk key migrations in the order they must apply. Order
// matters: a migration may only write the new field when the new key is ABSENT, so a
// file that already carries the new key is never clobbered. Each migration commits its
// own entry (Order 1A-b) and removes the matching allowlist entry.
var legacyKeys = []legacyKey{
	{
		// Mutation (layoutPresets → layoutProfiles): one-way, no clobber. A legacy
		// layoutPresets file (classic slots only) wraps each sanitized preset into a
		// Classic-only profile; skipped once any profile existp.
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

// migrateLegacyKeys applies every legacyKey in order.
func migrateLegacyKeys(s *Settings, data []byte, presence map[string]bool) {
	for _, lk := range legacyKeys {
		lk.migrate(s, data, presence)
	}
}

// normalize reproduces load()'s range clamps and one-shot stamps over a
// snapshot-loaded value, so defaultPrefs + Unmarshal-over-defaults + normalize ==
// load() for every field and case. The clamps are the same functions/constants load()
func (p *AssetPreferences) normalizeSnapshot(s *Settings, presence map[string]bool) (changed bool) {
	def := defaultSettings()

	// (i) clamp/normalise — unconditional (pointer fields load() clamped regardless).
	s.NameVal = clampPercent(s.NameVal, minNameColorVal, 100)
	s.RainbowSpriteSpeed = clampPercent(s.RainbowSpriteSpeed, minRainbowSpeed, maxRainbowSpeed)
	s.ReplayPlaybackSpeed = clampPercent(s.ReplayPlaybackSpeed, minReplaySpeed, maxReplaySpeed)
	s.CatchUpThreshold = clampPercent(s.CatchUpThreshold, catchUpThresholdMin, catchUpThresholdMax)
	s.MultiTabCap = clampPercent(s.MultiTabCap, minMultiTabCap, maxMultiTabCap)
	s.PreviewHoverMs = clampPercent(s.PreviewHoverMs, minPreviewHoverMs, maxPreviewHoverMs)
	s.EmoteHoverNamesMs = clampPercent(s.EmoteHoverNamesMs, minEmoteHoverNamesMs, maxEmoteHoverNamesMs)
	s.MusicVol = clampPercent(s.MusicVol, 0, defaultAudioVolume)
	s.SFXVol = clampPercent(s.SFXVol, 0, defaultAudioVolume)
	s.BlipVol = clampPercent(s.BlipVol, 0, defaultAudioVolume)
	s.AlertVol = clampPercent(s.AlertVol, 0, defaultAudioVolume)
	s.MasterVol = clampPercent(s.MasterVol, 0, defaultAudioVolume)
	s.HighlightColor &= 0xFFFFFF
	s.ICCustomColor &= 0xFFFFFF
	s.SpriteTintColor &= 0xFFFFFF
	s.ChatboxOpacity = clampPercent(s.ChatboxOpacity, MinChatboxOpacity, MaxChatboxOpacity)
	s.RainbowSpriteVividness = clampPercent(s.RainbowSpriteVividness, minRainbowVivid, maxRainbowVivid)
	s.NameSat = clampPercent(s.NameSat, 0, 100)
	s.MusicFlagsVal &= MusicFlagsMask
	s.TextStayMs = clampPercent(s.TextStayMs, 0, MaxTextStayMs)
	s.EmoteGridGapPx = clampPercent(s.EmoteGridGapPx, MinEmoteGridGapPx, MaxEmoteGridGapPx)

	// (i) clamp/normalise — explicit zero keeps the default (load() used `!= 0`).
	if s.ViewportPct == 0 {
		s.ViewportPct = def.ViewportPct
	} else {
		s.ViewportPct = clampPercent(s.ViewportPct, MinViewportPercent, MaxViewportPercent)
	}
	if s.ChatScalePct == 0 {
		s.ChatScalePct = def.ChatScalePct
	} else {
		s.ChatScalePct = clampPercent(s.ChatScalePct, MinChatScalePercent, MaxChatScalePercent)
	}
	if s.ChatBoxPct == 0 {
		s.ChatBoxPct = def.ChatBoxPct
	} else {
		s.ChatBoxPct = clampPercent(s.ChatBoxPct, MinChatBoxPercent, MaxChatBoxPercent)
	}
	if s.LogScalePct == 0 {
		s.LogScalePct = def.LogScalePct
	} else {
		s.LogScalePct = clampPercent(s.LogScalePct, MinLogScalePercent, MaxLogScalePercent)
	}
	if presence["oocScalePercent"] && s.OOCScalePct != 0 {
		s.OOCScalePct = clampPercent(s.OOCScalePct, MinLogScalePercent, MaxLogScalePercent)
	} else {
		// inherit the IC log size once so the OOC box doesn't visibly jump on upgrade
		// (absent or explicit zero, matching load()'s `onDisk.OOCScalePct != 0`).
		s.OOCScalePct = s.LogScalePct
	}
	if s.InputHeightPct == 0 {
		s.InputHeightPct = def.InputHeightPct
	} else {
		s.InputHeightPct = clampPercent(s.InputHeightPct, MinInputPercent, MaxInputPercent)
	}
	if s.UIScalePct == 0 {
		s.UIScalePct = def.UIScalePct
	} else {
		s.UIScalePct = clampPercent(s.UIScalePct, MinUIScalePercent, MaxUIScalePercent)
	}
	if s.HoldClearMs == 0 {
		s.HoldClearMs = def.HoldClearMs
	} else {
		s.HoldClearMs = clampPercent(s.HoldClearMs, MinHoldClearMs, MaxHoldClearMs)
	}
	if s.TextCrawlMs == 0 {
		s.TextCrawlMs = def.TextCrawlMs
	} else {
		s.TextCrawlMs = clampPercent(s.TextCrawlMs, MinTextCrawlMs, MaxTextCrawlMs)
	}
	if s.BlipRate == 0 {
		s.BlipRate = def.BlipRate
	} else {
		s.BlipRate = clampPercent(s.BlipRate, MinBlipRate, MaxBlipRate)
	}
	if s.ThemeFitZoom == 0 {
		s.ThemeFitZoom = def.ThemeFitZoom
	} else {
		s.ThemeFitZoom = clampPercent(s.ThemeFitZoom, MinThemeZoom, MaxThemeZoom)
	}

	// (i)/(iii) enums and string defaults: invalid or empty keeps the default.
	if s.ChromeShapeKey != chromeShapeRounded && s.ChromeShapeKey != chromeShapePill {
		s.ChromeShapeKey = def.ChromeShapeKey
	}
	if s.HoldClearKey == "" {
		s.HoldClearKey = def.HoldClearKey
	}
	// Mutation (subtheme): sanitize the subtheme folder on the way in (traversal is
	// stripped), matching load()'s theme.SanitizeSubtheme call.
	s.SubthemeName = theme.SanitizeSubtheme(s.SubthemeName)
	// Mutation (proxyMode): clamp on the way in exactly as SetProxyMode clamps on the way
	// out; an out-of-range value keeps the shipped default (ProxyModeSystem).
	if s.ProxyModeVal < 0 || s.ProxyModeVal >= ProxyModeCount {
		s.ProxyModeVal = def.ProxyModeVal
	}

	// (iv) explicit-zero keeps the default (non-pointer `> 0` checks).
	if s.BgSlideshowSecs <= 0 {
		s.BgSlideshowSecs = def.BgSlideshowSecs
	}

	// (ii) one-shot migration stamps — re-apply load()'s stamp decision.
	if presence["spriteScalingMigrated"] && s.SpriteScalingMigrated {
		s.SpriteScaling = ClampSpriteScaling(s.SpriteScaling)
	} else {
		s.SpriteScaling = defaultSpriteScaling
		s.SpriteScalingMigrated = true
		changed = true // persist the stamp promptly so the move never re-fires
	}
	if presence["autoReconnectDefaultMigrated"] && s.AutoReconnectDefaultMigrated {
		// already migrated: the on-disk value is honoured as-is
	} else {
		s.AutoReconnect = false
		s.AutoReconnectDefaultMigrated = true
		changed = true
	}
	// Mutation (themeFit flags): the migration reads the ON-DISK themeFit (zero when the
	// key is absent), not the default-prefs value the unmarshal-over-defaults left there.
	effectiveThemeFit := s.ThemeFit
	if !presence["themeFit"] {
		effectiveThemeFit = 0
	}
	if presence["themeFitDefaultMigrated"] && s.ThemeFitDefaultMigrated {
		s.ThemeFit = clampPercent(effectiveThemeFit, ThemeFitStretch, themeFitModeMax)
	} else {
		saved := clampPercent(effectiveThemeFit, ThemeFitStretch, themeFitModeMax)
		if saved == ThemeFitStretch {
			// Mutation (themeFit flags): only a file still on the legacy Stretch default
			// arms the one-shot move-to-native; every un-migrated file stamps.
			s.ThemeFit = defaultThemeFit
			p.themeFitMovedToNative = true
		} else {
			s.ThemeFit = saved
		}
		s.ThemeFitDefaultMigrated = true
		p.themeFitStampPending = true
		changed = true
	}
	if presence["blipSpeedDefaultMigrated"] && s.BlipSpeedDefaultMigrated {
		if s.TextCrawlMs != 0 {
			s.TextCrawlMs = clampPercent(s.TextCrawlMs, MinTextCrawlMs, MaxTextCrawlMs)
		}
	} else {
		saved := clampPercent(s.TextCrawlMs, MinTextCrawlMs, MaxTextCrawlMs)
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

	// (i) enum clamp: a garbage / newer sprite-load mode falls back to the default.
	if s.SpriteLoadModeVal < SpriteLoadBlank || s.SpriteLoadModeVal >= SpriteLoadModeCount {
		s.SpriteLoadModeVal = defaultSpriteLoadMode
	}

	// (iii) non-zero nested defaults the unmarshal-over-defaults zeroed.
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
	// sanitize tail (Mutations): reproduce load()'s per-helper clean-ups — bounds, blank
	// name/id drops, and dedupe — so a hand-edited file cannot grow or corrupt the maps.
	s.SavedStyles = sanitizeStylePresets(s.SavedStyles)
	s.PanelFontsVal = sanitizePanelFonts(s.PanelFontsVal)
	s.UserMacros = sanitizeMacros(s.UserMacros)
	s.HiddenPanelIDs = sanitizeHiddenPanels(s.HiddenPanelIDs)
	s.ClassicLayout = sanitizeClassicLayout(s.ClassicLayout)
	s.ClassicAnchors = sanitizeClassicAnchors(s.ClassicAnchors)
	s.ClassicRotations = sanitizeClassicRotations(s.ClassicRotations)
	s.ThemeRectRotations = sanitizeThemeRectRotations(s.ThemeRectRotations)
	s.LayoutProfiles = sanitizeLayoutProfiles(s.LayoutProfiles)
	s.LayoutGridPx = clampLayoutGridPx(s.LayoutGridPx)
	s.FontWarnThemes = clampStrings(s.FontWarnThemes, FontWarnThemesCap)
	{
		e := defaultExportOptions()
		if s.Export.HeightPx > 0 {
			e.HeightPx = clampPercent(s.Export.HeightPx, minExportHeight, maxExportHeight)
		}
		if s.Export.FPS > 0 {
			e.FPS = clampPercent(s.Export.FPS, minExportFPS, maxExportFPS)
		}
		if s.Export.Quality > 0 {
			e.Quality = clampPercent(s.Export.Quality, minExportQuality, maxExportQuality)
		}
		if s.Export.TextScale > 0 {
			e.TextScale = clampPercent(s.Export.TextScale, minExportText, maxExportText)
		}
		if s.Export.VideoFormat != "" {
			e.VideoFormat = normalizeVideoFormat(s.Export.VideoFormat)
		}
		e.Loop = s.Export.Loop
		if presence["export"] && !presence["export.loop"] {
			// Mutation (export): the old load read onDisk.Export.Loop — a present export
			// object without a loop key reads false, not the default.
			e.Loop = false
		}
		s.Export = e
	}
	// discord + autoStatus: load() copied the whole on-disk struct when the key was
	// PRESENT, so a present-but-omitempty field reads as ZERO, not the defaultPrefs
	// value (which the unmarshal-over-defaults keeps). An ABSENT key keeps the default.
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
	// Strict load: clamp out-of-range file values exactly as the setters do, so the
	// clamp-on-read getters can collapse to plain field reads. A clamp never sets
	// `changed` (no dirty, no rewrite until the next real Save).
	clampSettingsStrict(s)
	return changed
}
