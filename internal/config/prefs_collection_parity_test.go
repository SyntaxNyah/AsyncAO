package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// collectionParityCase is one collection field with its JSON key and the three
// representative values (nil, empty, non-empty) for the nil/empty/non-empty parity.
type collectionParityCase struct {
	name    string
	jsonKey string
	typ     reflect.Type
}

var collectionParityCases = []collectionParityCase{
	{name: "CallWordList", jsonKey: "callWords", typ: reflect.TypeOf([]string(nil))},
	{name: "OpenTabs", jsonKey: "openTabs", typ: reflect.TypeOf([]OpenTab(nil))},
	{name: "UserMacros", jsonKey: "macros", typ: reflect.TypeOf([]MacroSpec(nil))},
	{name: "HiddenPanelIDs", jsonKey: "hiddenPanels", typ: reflect.TypeOf([]string(nil))},
	{name: "ClassicLayout", jsonKey: "classicLayout", typ: reflect.TypeOf(map[string][4]float64(nil))},
	{name: "ClassicAnchors", jsonKey: "classicAnchors", typ: reflect.TypeOf(map[string]ClassicAnchor(nil))},
	{name: "ClassicRotations", jsonKey: "classicRotations", typ: reflect.TypeOf(map[string]uint8(nil))},
	{name: "HotkeyMap", jsonKey: "hotkeys", typ: reflect.TypeOf(map[string]string(nil))},
	{name: "BlipVols", jsonKey: "blipVolumes", typ: reflect.TypeOf(map[string]int(nil))},
	{name: "SavedStyles", jsonKey: "stylePresets", typ: reflect.TypeOf([]StylePreset(nil))},
}

// colValues returns nil, empty and non-empty values for a collection type.
func colValues(t reflect.Type) (nilV, emptyV, nonEmptyV reflect.Value) {
	nilV = reflect.Zero(t)
	if t.Kind() == reflect.Map {
		emptyV = reflect.MakeMap(t)
	} else {
		emptyV = reflect.MakeSlice(t, 0, 0)
	}
	nonEmptyV = validOf(t)
	return nilV, emptyV, nonEmptyV
}

// saveResult captures what Save writes plus the dirty flag after a mutation.
type saveResult struct {
	bytes []byte
	dirty bool
}

func saveAfterUpdate(t *testing.T, field string, v reflect.Value) saveResult {
	t.Helper()
	p := defaultPrefs("")
	p.Update(func(s *Settings) { setSettingsField(s, field, v) })
	dirty := p.pending.Load()
	sp := filepath.Join(t.TempDir(), "s.json")
	p.path = sp
	if err := p.SaveNow(); err != nil {
		t.Fatalf("%s: SaveNow: %v", field, err)
	}
	b, _ := os.ReadFile(sp)
	return saveResult{bytes: b, dirty: dirty}
}

// TestCollectionNilEmptyParity is the mutation line: assigning an EMPTY collection
// over a nil one through Update must mark dirty (the write actually happens). If equal
// ever treats nil and empty as equal, the empty assignment is not written back, the
// dirty flag stays false, and this test fails.
func TestCollectionNilEmptyParity(t *testing.T) {
	for _, c := range collectionParityCases {
		nilV, emptyV, _ := colValues(c.typ)
		nilRes := saveAfterUpdate(t, c.name, nilV)
		emptyRes := saveAfterUpdate(t, c.name, emptyV)
		if nilRes.dirty {
			t.Errorf("%s: nil assignment marked dirty (it should be a no-op)", c.name)
		}
		if !emptyRes.dirty {
			t.Errorf("%s: empty-over-nil assignment did not mark dirty — equal is treating nil==empty", c.name)
		}
	}
}
