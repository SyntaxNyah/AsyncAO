package config

import (
	"reflect"
	"testing"
	"unsafe"
)

// oracleClone is the reflection deep clone that used to live in settings.go
// (cloneSettings + cloneValue + cloneStructField). It stays here ONLY as the
// oracle the generated Clone (settings_clone.go) is pinned against: the generated
// clone must DeepEqual this on a fully-filled struct, and the mutate-every-collection
// test below must pass (no aliasing).
// Mutation line: drop one collection's deep copy in settings_clone.go (e.g. remove the
// `c.MusicHosts = append(...)` line) and TestSettingsCloneNeverAliases fails — the clone
// shares that collection with the original.
func oracleClone(s *Settings) *Settings {
	if s == nil {
		return nil
	}
	out := &Settings{}
	reflectCloneValue(reflect.ValueOf(s).Elem(), reflect.ValueOf(out).Elem())
	return out
}

func reflectCloneValue(src, dst reflect.Value) {
	switch src.Kind() {
	case reflect.Map:
		if src.IsNil() {
			return
		}
		dst.Set(reflect.MakeMapWithSize(src.Type(), src.Len()))
		for _, k := range src.MapKeys() {
			dk := reflect.New(src.Type().Key()).Elem()
			reflectCloneValue(k, dk)
			dv := reflect.New(src.Type().Elem()).Elem()
			reflectCloneValue(src.MapIndex(k), dv)
			dst.SetMapIndex(dk, dv)
		}
	case reflect.Slice:
		if src.IsNil() {
			return
		}
		dst.Set(reflect.MakeSlice(src.Type(), src.Len(), src.Len()))
		for i := 0; i < src.Len(); i++ {
			reflectCloneValue(src.Index(i), dst.Index(i))
		}
	case reflect.Ptr:
		if src.IsNil() {
			return
		}
		dst.Set(reflect.New(src.Type().Elem()))
		reflectCloneValue(src.Elem(), dst.Elem())
	case reflect.Struct:
		for i := 0; i < src.NumField(); i++ {
			reflectCloneStructField(src, dst, i)
		}
	default:
		dst.Set(src)
	}
}

func reflectCloneStructField(src, dst reflect.Value, i int) {
	sf, df := src.Field(i), dst.Field(i)
	if df.CanSet() {
		reflectCloneValue(sf, df)
		return
	}
	sbase := unsafe.Add(unsafe.Pointer(src.UnsafeAddr()), src.Type().Field(i).Offset)
	sf = reflect.NewAt(sf.Type(), sbase).Elem()
	dbase := unsafe.Add(unsafe.Pointer(dst.UnsafeAddr()), dst.Type().Field(i).Offset)
	df = reflect.NewAt(df.Type(), dbase).Elem()
	reflectCloneValue(sf, df)
}

// filledSettings returns a Settings with every map, slice, pointer and nested value
// populated, so the oracle DeepEqual and the mutate-every-collection test exercise the
// whole reference graph. A new reference-holding field must be added here or the
// mutate test stops covering it (and a generated Clone that forgets to deep-copy it fails).
func filledSettings() *Settings {
	s := &Settings{
		SmoothScaling:      true,
		SpriteScaling:      1,
		HighlightColor:     0x123456,
		SavedStyles:        []StylePreset{{Name: "a", Color: 1, Emote: "e"}, {Name: "b"}},
		PanelFontsVal:      map[string]PanelFont{"ic": {Family: "Arial", SizePx: 14}},
		FontWarnThemes:     []string{"w1", "w2"},
		OpenTabs:           []OpenTab{{Name: "t", URL: "ws://x"}},
		UserMacros:         []MacroSpec{{Name: "m", Key: "k", Lines: []string{"l1", "l2"}}},
		ThemeRectOv:        map[string]map[string][4]int{"a": {"b": {1, 2, 3, 4}}},
		ThemeRectRotations: map[string]map[string]uint8{"a": {"b": 7}},
		ClassicLayout:      map[string][4]float64{"a": {1, 2, 3, 4}},
		LayoutProfiles: map[string]LayoutProfile{
			"p": {
				Classic:   map[string][4]float64{"c": {1, 2, 3, 4}},
				Anchors:   map[string]ClassicAnchor{"d": {Mode: "ft", WinW: 1, WinH: 2}},
				Hidden:    []string{"h"},
				GridPx:    8,
				Rotations: map[string]uint8{"r": 1},
			},
		},
		ClassicAnchors:     map[string]ClassicAnchor{"e": {Mode: "cc", WinW: 3, WinH: 4}},
		ClassicRotations:   map[string]uint8{"f": 2},
		AssetTypes:         map[string]AssetTypePrefs{"g": {FormatOrder: []string{"png", "webp"}, FallbacksEnabled: true}},
		LearnedFormats:     map[string][]string{"h": {"png"}},
		ShownamePresets:    []string{"s1"},
		ShownameKeys:       map[string]string{"k1": "v1"},
		ICPhraseKeys:       map[string]string{"k2": "v2"},
		MutedSFX:           []string{"m1"},
		SfxFavorites:       []string{"f1"},
		ModReasonTemplates: []string{"r1"},
		ModDurations:       []string{"d1"},
		BlipVols:           map[string]int{"b": 5},
		EmoteFavs:          map[string][]int{"e": {1, 2}},
		CustomChromeHex:    [7]string{"a", "b", "c", "d", "e", "f", "g"},
		LayoutPartHex:      [4]string{"a", "b", "c", "d"},
		ExtProfiles:        map[string]string{"x": "y"},
		LocalAssetsPaths:   []string{"p1"},
		Favorites:          []FavoriteServer{{Name: "n", URL: "u", Description: "d"}},
		Wardrobe:           []string{"w"},
		HiddenPanelIDs:     []string{"h1"},
		ServerWarm: map[string]ServerWarmInfo{
			"s": {
				Char:                "c",
				Background:          "b",
				Wardrobe:            []string{"w"},
				FavBackgrounds:      []string{"fb"},
				FavBackgroundFolder: map[string]string{"fb": "f"},
				CharKeys:            map[string]string{"k": "c"},
				WardrobeFolder:      map[string]string{"w": "f"},
				Origin:              "o",
				AudioOn:             true,
				AudioMaster:         intPtr(70),
				AudioMusic:          intPtr(71),
				AudioSFX:            intPtr(72),
				AudioBlip:           intPtr(73),
				Chars:               []string{"ch"},
				Backgrounds:         []string{"bg"},
				Friends:             []string{"fr"},
				Ignored:             []string{"ig"},
			},
		},
		CallWordList: []string{"cw"},
		HotkeyMap:    map[string]string{"hk": "hv"},
		MusicHosts:   []string{"mh"},
	}
	return s
}

func intPtr(v int) *int { return &v }

func TestSettingsCloneMatchesOracle(t *testing.T) {
	s := filledSettings()
	if !reflect.DeepEqual(s.Clone(), oracleClone(s)) {
		t.Fatal("generated Clone diverges from the reflection oracle on a fully-filled struct")
	}
	if !reflect.DeepEqual(s.Clone(), s) {
		t.Fatal("generated Clone changed a value on a fully-filled struct")
	}
}

// TestSettingsCloneNeverAliases mutates every collection (and nested value) of the clone
// and asserts the original is untouched. This is the guard that a NEW reference-holding
// Settings field fails until settings_clone.go deep-copies it.
func TestSettingsCloneNeverAliases(t *testing.T) {
	s := filledSettings()
	want := filledSettings() // independent copy of the same shape
	c := s.Clone()
	mutateAllCollections(reflect.ValueOf(c).Elem())
	if !reflect.DeepEqual(s, want) {
		t.Fatal("mutating the clone's collections aliased the original: Clone misses a deep copy")
	}
}

// mutateAllCollections recursively zeroes/mutates every map, slice and pointer in v in place.
func mutateAllCollections(v reflect.Value) {
	switch v.Kind() {
	case reflect.Map:
		if v.Len() > 0 {
			for _, k := range v.MapKeys() {
				v.SetMapIndex(k, reflect.Zero(v.Type().Elem()))
				break
			}
		} else {
			v.SetMapIndex(reflect.Zero(v.Type().Key()), reflect.Zero(v.Type().Elem()))
		}
	case reflect.Slice:
		if v.Len() > 0 {
			v.Index(0).Set(reflect.Zero(v.Type().Elem()))
		}
	case reflect.Ptr:
		if !v.IsNil() {
			if v.Elem().CanSet() && isScalarKind(v.Elem().Kind()) {
				v.Elem().Set(reflect.Zero(v.Elem().Type()))
			} else {
				mutateAllCollections(v.Elem())
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				mutateAllCollections(f)
			}
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			mutateAllCollections(v.Index(i))
		}
	}
}

func isScalarKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return true
	}
	return false
}
