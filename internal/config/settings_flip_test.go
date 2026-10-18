package config

import (
	"reflect"
	"testing"
)

// TestDataFieldsOnlyOnSettings pins the flip: every exported, json-tagged data field
// lives on Settings, and AssetPreferences keeps only runtime machinery (no data field
// may survive on it).
func TestDataFieldsOnlyOnSettings(t *testing.T) {
	st := reflect.TypeOf(Settings{})
	at := reflect.TypeOf(AssetPreferences{})
	for i := 0; i < st.NumField(); i++ {
		sf := st.Field(i)
		if sf.PkgPath != "" || sf.Tag.Get("json") == "" {
			continue
		}
		if _, ok := at.FieldByName(sf.Name); ok {
			t.Errorf("data field %s still lives on AssetPreferences (flip incomplete)", sf.Name)
		}
	}
}
