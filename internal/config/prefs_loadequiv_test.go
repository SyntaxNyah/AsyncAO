package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// jsonKey returns the json tag key for a struct field ("" for untagged or "-").
func jsonKey(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	return strings.Split(tag, ",")[0]
}

// presencePrepass decodes data into the set of PRESENT (non-null) JSON key paths,
// recursing into nested objects. A JSON null counts as absent, matching the old
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
// OVER them, then normalize (clamps + stamps) using the presence pre-pass.
func snapshotLoad(data []byte) *AssetPreferences {
	p := defaultPrefs("")
	_ = json.Unmarshal(data, p)
	p.normalize(presencePrepass(data))
	return p
}

// nonZeroValue returns a value for t that differs from its zero value, so an
// explicit-non-zero file is observably different from absent/zero.
func nonZeroValue(t reflect.Type) any {
	switch t.Kind() {
	case reflect.Bool:
		return true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int64(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return uint64(7)
	case reflect.Float32, reflect.Float64:
		return float64(7)
	case reflect.String:
		return "nv"
	default:
		return nil
	}
}

// normalize reproduces load()'s range clamps and one-shot stamps over a
// snapshot-loaded value, so defaultPrefs + Unmarshal-over-defaults + normalize ==
// load() for every field and case. The clamps are the same functions/constants load()
// uses; the resets use the defaultPrefs reference so a default change keeps them in step.
func (p *AssetPreferences) normalize(presence map[string]bool) {
	def := defaultPrefs("")

	// (i) clamp/normalise — unconditional (pointer fields load() clamped regardless).
	p.NameVal = clampPercent(p.NameVal, minNameColorVal, 100)
	p.RainbowSpriteSpeed = clampPercent(p.RainbowSpriteSpeed, minRainbowSpeed, maxRainbowSpeed)
	p.ReplayPlaybackSpeed = clampPercent(p.ReplayPlaybackSpeed, minReplaySpeed, maxReplaySpeed)
	p.CatchUpThreshold = clampPercent(p.CatchUpThreshold, catchUpThresholdMin, catchUpThresholdMax)
	p.MultiTabCap = clampPercent(p.MultiTabCap, minMultiTabCap, maxMultiTabCap)
	p.PreviewHoverMs = clampPercent(p.PreviewHoverMs, minPreviewHoverMs, maxPreviewHoverMs)
	p.EmoteHoverNamesMs = clampPercent(p.EmoteHoverNamesMs, minEmoteHoverNamesMs, maxEmoteHoverNamesMs)
	p.MusicVol = clampPercent(p.MusicVol, 0, defaultAudioVolume)
	p.SFXVol = clampPercent(p.SFXVol, 0, defaultAudioVolume)
	p.BlipVol = clampPercent(p.BlipVol, 0, defaultAudioVolume)
	p.AlertVol = clampPercent(p.AlertVol, 0, defaultAudioVolume)
	p.MasterVol = clampPercent(p.MasterVol, 0, defaultAudioVolume)
	p.HighlightColor &= 0xFFFFFF
	p.ICCustomColor &= 0xFFFFFF
	p.SpriteTintColor &= 0xFFFFFF
	p.ChatboxOpacity = clampPercent(p.ChatboxOpacity, MinChatboxOpacity, MaxChatboxOpacity)
	p.RainbowSpriteVividness = clampPercent(p.RainbowSpriteVividness, minRainbowVivid, maxRainbowVivid)
	p.NameSat = clampPercent(p.NameSat, 0, 100)
	p.MusicFlagsVal &= MusicFlagsMask
	p.TextStayMs = clampPercent(p.TextStayMs, 0, MaxTextStayMs)
	p.EmoteGridGapPx = clampPercent(p.EmoteGridGapPx, MinEmoteGridGapPx, MaxEmoteGridGapPx)

	// (i) clamp/normalise — explicit zero keeps the default (load() used `!= 0`).
	if p.ViewportPct == 0 {
		p.ViewportPct = def.ViewportPct
	} else {
		p.ViewportPct = clampPercent(p.ViewportPct, MinViewportPercent, MaxViewportPercent)
	}
	if p.ChatScalePct == 0 {
		p.ChatScalePct = def.ChatScalePct
	} else {
		p.ChatScalePct = clampPercent(p.ChatScalePct, MinChatScalePercent, MaxChatScalePercent)
	}
	if p.ChatBoxPct == 0 {
		p.ChatBoxPct = def.ChatBoxPct
	} else {
		p.ChatBoxPct = clampPercent(p.ChatBoxPct, MinChatBoxPercent, MaxChatBoxPercent)
	}
	if p.LogScalePct == 0 {
		p.LogScalePct = def.LogScalePct
	} else {
		p.LogScalePct = clampPercent(p.LogScalePct, MinLogScalePercent, MaxLogScalePercent)
	}
	if p.OOCScalePct == 0 {
		p.OOCScalePct = def.OOCScalePct
	} else {
		p.OOCScalePct = clampPercent(p.OOCScalePct, MinLogScalePercent, MaxLogScalePercent)
	}
	if p.InputHeightPct == 0 {
		p.InputHeightPct = def.InputHeightPct
	} else {
		p.InputHeightPct = clampPercent(p.InputHeightPct, MinInputPercent, MaxInputPercent)
	}
	if p.UIScalePct == 0 {
		p.UIScalePct = def.UIScalePct
	} else {
		p.UIScalePct = clampPercent(p.UIScalePct, MinUIScalePercent, MaxUIScalePercent)
	}
	if p.HoldClearMs == 0 {
		p.HoldClearMs = def.HoldClearMs
	} else {
		p.HoldClearMs = clampPercent(p.HoldClearMs, MinHoldClearMs, MaxHoldClearMs)
	}
	if p.TextCrawlMs == 0 {
		p.TextCrawlMs = def.TextCrawlMs
	} else {
		p.TextCrawlMs = clampPercent(p.TextCrawlMs, MinTextCrawlMs, MaxTextCrawlMs)
	}
	if p.BlipRate == 0 {
		p.BlipRate = def.BlipRate
	} else {
		p.BlipRate = clampPercent(p.BlipRate, MinBlipRate, MaxBlipRate)
	}
	if p.ThemeFitZoom == 0 {
		p.ThemeFitZoom = def.ThemeFitZoom
	} else {
		p.ThemeFitZoom = clampPercent(p.ThemeFitZoom, MinThemeZoom, MaxThemeZoom)
	}

	// (i)/(iii) enums and string defaults: invalid or empty keeps the default.
	if p.ChromeShapeKey != chromeShapeRounded && p.ChromeShapeKey != chromeShapePill {
		p.ChromeShapeKey = def.ChromeShapeKey
	}
	if p.HoldClearKey == "" {
		p.HoldClearKey = def.HoldClearKey
	}

	// (iv) explicit-zero keeps the default (non-pointer `> 0` checks).
	if p.BgSlideshowSecs <= 0 {
		p.BgSlideshowSecs = def.BgSlideshowSecs
	}

	// (ii) one-shot migration stamps — re-apply load()'s stamp decision.
	if presence["spriteScalingMigrated"] && p.SpriteScalingMigrated {
		p.SpriteScaling = ClampSpriteScaling(p.SpriteScaling)
	} else {
		p.SpriteScaling = defaultSpriteScaling
		p.SpriteScalingMigrated = true
	}
	if presence["autoReconnectDefaultMigrated"] && p.AutoReconnectDefaultMigrated {
		// already migrated: the on-disk value is honoured as-is
	} else {
		p.AutoReconnect = false
		p.AutoReconnectDefaultMigrated = true
	}
	if presence["themeFitDefaultMigrated"] && p.ThemeFitDefaultMigrated {
		p.ThemeFit = clampPercent(p.ThemeFit, ThemeFitStretch, themeFitModeMax)
	} else {
		saved := clampPercent(p.ThemeFit, ThemeFitStretch, themeFitModeMax)
		if saved == ThemeFitStretch {
			p.ThemeFit = defaultThemeFit
		} else {
			p.ThemeFit = saved
		}
		p.ThemeFitDefaultMigrated = true
	}
	if presence["blipSpeedDefaultMigrated"] && p.BlipSpeedDefaultMigrated {
		if p.TextCrawlMs != 0 {
			p.TextCrawlMs = clampPercent(p.TextCrawlMs, MinTextCrawlMs, MaxTextCrawlMs)
		}
	} else {
		saved := clampPercent(p.TextCrawlMs, MinTextCrawlMs, MaxTextCrawlMs)
		if p.TextCrawlMs == 0 || saved == preMoveTextCrawlMs {
			p.TextCrawlMs = DefaultTextCrawlMs
		} else {
			p.TextCrawlMs = saved
		}
		p.BlipSpeedDefaultMigrated = true
	}
	if presence["deskManifestDefaultMigrated"] && p.DeskManifestDefaultMigrated {
		// honoured as-is
	} else {
		p.DeskFollowManifest = true
		p.DeskManifestDefaultMigrated = true
	}
	if presence["detailedLogDefaultMigrated"] && p.DetailedLogDefaultMigrated {
		// honoured as-is
	} else {
		p.DetailedLog = true
		p.DetailedLogDefaultMigrated = true
	}

	// (i) enum clamp: a garbage / newer sprite-load mode falls back to the default.
	if p.SpriteLoadModeVal < SpriteLoadBlank || p.SpriteLoadModeVal >= SpriteLoadModeCount {
		p.SpriteLoadModeVal = defaultSpriteLoadMode
	}

	// (iii) non-zero nested defaults the unmarshal-over-defaults zeroed.
	if len(p.AssetTypes) == 0 {
		p.AssetTypes = def.AssetTypes
	} else {
		for name, tp := range p.AssetTypes {
			if len(tp.FormatOrder) == 0 {
				tp.FormatOrder = DefaultFormatOrder(name)
			}
			if name == TypeMisc && len(tp.FormatOrder) == 1 && tp.FormatOrder[0] == ExtWebP {
				tp.FormatOrder = DefaultFormatOrder(name)
			}
			p.AssetTypes[name] = tp
		}
	}
	if p.MusicHosts == nil {
		p.MusicHosts = def.MusicHosts
	} else {
		p.MusicHosts = sanitizeMusicHosts(p.MusicHosts)
	}
	if p.LearnedFormats == nil {
		p.LearnedFormats = def.LearnedFormats
	}
	{
		e := defaultExportOptions()
		if p.Export.HeightPx > 0 {
			e.HeightPx = clampPercent(p.Export.HeightPx, minExportHeight, maxExportHeight)
		}
		if p.Export.FPS > 0 {
			e.FPS = clampPercent(p.Export.FPS, minExportFPS, maxExportFPS)
		}
		if p.Export.Quality > 0 {
			e.Quality = clampPercent(p.Export.Quality, minExportQuality, maxExportQuality)
		}
		if p.Export.TextScale > 0 {
			e.TextScale = clampPercent(p.Export.TextScale, minExportText, maxExportText)
		}
		if p.Export.VideoFormat != "" {
			e.VideoFormat = normalizeVideoFormat(p.Export.VideoFormat)
		}
		e.Loop = p.Export.Loop
		p.Export = e
	}
	// discord + autoStatus: load() copied the whole on-disk struct when the key was
	// PRESENT, so a present-but-omitempty field reads as ZERO, not the defaultPrefs
	// value (which the unmarshal-over-defaults keeps). An ABSENT key keeps the default.
	if presence["discord"] && !presence["discord.appId"] {
		p.DiscordRPC.AppID = ""
	}
	if presence["autoStatus"] {
		if !presence["autoStatus.clearWords"] {
			p.MyAutoStatus.ClearWords = ""
		}
		if !presence["autoStatus.afkWords"] {
			p.MyAutoStatus.AFKWords = ""
		}
		if !presence["autoStatus.busyWords"] {
			p.MyAutoStatus.BusyWords = ""
		}
		if !presence["autoStatus.writingWords"] {
			p.MyAutoStatus.WritingWords = ""
		}
		if !presence["autoStatus.lfrpWords"] {
			p.MyAutoStatus.LFRPWords = ""
		}
	}
	p.MyAutoStatus = sanitizeAutoStatus(p.MyAutoStatus)
}

// TestLoadEquivalence drives the B0 gap question directly: for every field with a
// non-zero default, does a file with the key ABSENT, explicitly ZERO, and explicitly
// NON-ZERO load identically through the old load() and through a plain
// unmarshal-over-defaults? Every difference is a case the snapshot's normalize() must
// reproduce; the test prints them rather than failing, so the report is a list, not a
// binary gate.
func TestLoadEquivalence(t *testing.T) {
	pt := reflect.TypeOf(AssetPreferences{})
	def := defaultPrefs("")

	var diffs []string
	record := func(key, cs string, oldv, newv any) {
		diffs = append(diffs, key+"|"+cs+"|old="+stableV(reflect.ValueOf(oldv))+"|new="+stableV(reflect.ValueOf(newv)))
	}

	dir := t.TempDir()
	for i := 0; i < pt.NumField(); i++ {
		f := pt.Field(i)
		key := jsonKey(f)
		if key == "" {
			continue
		}
		dv := reflect.ValueOf(def).Elem().Field(i)
		if !dv.CanInterface() || reflect.DeepEqual(dv.Interface(), reflect.Zero(f.Type).Interface()) {
			continue // zero default: absent and explicit-zero already agree
		}
		cases := []struct {
			name  string
			value any
			write bool // false = the key is absent from the file
		}{
			{"absent", nil, false},
			{"null", nil, true}, // JSON null — must read as absent, like the old pointer-field load()
			{"zero", reflect.Zero(f.Type).Interface(), true},
		}
		if nv := nonZeroValue(f.Type); nv != nil {
			cases = append(cases, struct {
				name  string
				value any
				write bool
			}{"nonzero", nv, true})
		}
		// below-min / above-max exercise the clamps, not just absent/zero/non-zero.
		switch f.Type.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			cases = append(cases,
				struct {
					name  string
					value any
					write bool
				}{"below-min", int64(-1000), true},
				struct {
					name  string
					value any
					write bool
				}{"above-max", int64(1000000), true},
			)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			cases = append(cases, struct {
				name  string
				value any
				write bool
			}{"above-max", uint64(1000000), true})
		case reflect.Float32, reflect.Float64:
			cases = append(cases,
				struct {
					name  string
					value any
					write bool
				}{"below-min", float64(-1000), true},
				struct {
					name  string
					value any
					write bool
				}{"above-max", float64(1000000), true},
			)
		}
		for _, cs := range cases {
			obj := map[string]any{}
			if cs.write {
				obj[key] = cs.value
			}
			raw, _ := json.Marshal(obj)

			path := filepath.Join(dir, "p.json")
			_ = os.WriteFile(path, raw, 0o644)
			oldp, err := load(path)
			if err != nil {
				t.Fatalf("load(%s): %v", key, err)
			}
			oldv := reflect.ValueOf(oldp).Elem().Field(i).Interface()

			newv := reflect.ValueOf(snapshotLoad(raw)).Elem().Field(i).Interface()
			if !reflect.DeepEqual(oldv, newv) {
				record(key, cs.name, oldv, newv)
			}
		}
	}

	if len(diffs) > 0 {
		// The allowlist must reach empty: each difference is a case the snapshot's
		// normalize() failed to reproduce from load(). Report every one, then fail.
		for _, d := range diffs {
			t.Errorf("snapshot load differs from load(): %s", d)
		}
		t.Fatalf("%d load-equivalence differences remain — the snapshot's normalize() must close every one (allowlist must be empty)", len(diffs))
	}
}

// stableV is a tiny stable renderer for the diff report (maps already handled by
// stableString in the setter golden; keep this for the scalar summary).
func stableV(v reflect.Value) string {
	if v.IsValid() && v.Kind() == reflect.Map {
		return "map(len=" + itoa(v.Len()) + ")"
	}
	return stableString(v)
}

// dumpPrefs renders an AssetPreferences as a comparable map: the exported fields via
// JSON, plus the unexported DATA fields (themeFitMovedToNative / themeFitStampPending)
// that json.Marshal skips. The mutex, the dirty/stop/done channels and any timer are
// runtime state, not persisted data, so they are deliberately excluded. This is the
// comparator the swap-equivalence test uses to prove new == old INCLUDING the one-shot
// stamp side-effects.
func dumpPrefs(p *AssetPreferences) map[string]any {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["__themeFitMovedToNative"] = p.themeFitMovedToNative
	m["__themeFitStampPending"] = p.themeFitStampPending
	return m
}

// themeFitFlagCases are the unexported one-shot side-effect cases: a file still on the
// legacy Stretch default arms the move-to-native (movedToNative) and always stamps
// (stampPending); a deliberate non-Stretch pick keeps the value but still stamps; an
// already-migrated file does neither. The JSON golden cannot see these, so the swap
// equivalence test reads them via dumpPrefs.
func themeFitFlagCases() []struct {
	name  string
	input map[string]any
} {
	return []struct {
		name  string
		input map[string]any
	}{
		{"themeFit_stretch_moves", map[string]any{"themeFit": 0}},
		{"themeFit_letterbox_keeps", map[string]any{"themeFit": 1}},
		{"themeFit_migrated_honours", map[string]any{"themeFit": 2, "themeFitDefaultMigrated": true}},
	}
}
