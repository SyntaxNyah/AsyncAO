package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// snapshotMatchesInline reports whether every Settings field equals the corresponding
// AssetPreferences data field (reflection, field-by-field, unexported included).
func snapshotMatchesInline(p *AssetPreferences) bool {
	s := p.Load()
	if s == nil {
		return false
	}
	sv := reflect.ValueOf(s).Elem()
	pv := reflect.ValueOf(p).Elem()
	for i := 0; i < sv.NumField(); i++ {
		sf := sv.Field(i)
		pf := pv.FieldByName(sv.Type().Field(i).Name)
		if !pf.IsValid() || !reflect.DeepEqual(sf.Interface(), pf.Interface()) {
			return false
		}
	}
	return true
}

// TestSnapshotMatchesInlineAfterSetters runs every edge case of every Set* method and
// asserts the republished snapshot equals the inline state after each.
func TestSnapshotMatchesInlineAfterSetters(t *testing.T) {
	pt := reflect.TypeOf(&AssetPreferences{})
	for i := 0; i < pt.NumMethod(); i++ {
		name := pt.Method(i).Name
		if !strings.HasPrefix(name, "Set") {
			continue
		}
		m, _ := pt.MethodByName(name)
		for _, sc := range setterScenarios(m) {
			base := defaultPrefs("")
			callArgs := append([]reflect.Value{reflect.ValueOf(base)}, sc.args...)
			func() {
				defer func() { recover() }()
				m.Func.Call(callArgs)
			}()
			if !snapshotMatchesInline(base) {
				t.Errorf("%s|%s: snapshot != inline after setter", name, sc.label)
			}
		}
	}
}

// TestSnapshotMatchesInlineAfterBulk verifies the republish on the non-setter paths:
// load, ImportSettings, ResetSettings and ResetAll.
func TestSnapshotMatchesInlineAfterBulk(t *testing.T) {
	// load: a default file round-trips through snapshotLoad.
	p, err := load(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !snapshotMatchesInline(p) {
		t.Error("snapshot != inline after load (first run)")
	}

	// load over a written file.
	dir := t.TempDir()
	path := filepath.Join(dir, "prefs.json")
	if err := os.WriteFile(path, []byte(`{"smoothScaling":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err = load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !snapshotMatchesInline(p) {
		t.Error("snapshot != inline after load (file)")
	}

	// ImportSettings writes the file and freezes; the in-memory state is unchanged.
	importSrc := filepath.Join(dir, "import.json")
	if err := os.WriteFile(importSrc, []byte(`{"smoothScaling":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p = defaultPrefs(filepath.Join(dir, "target.json"))
	if err := p.ImportSettings(importSrc); err != nil {
		t.Fatalf("ImportSettings: %v", err)
	}
	if !snapshotMatchesInline(p) {
		t.Error("snapshot != inline after ImportSettings")
	}

	// ResetSettings and ResetAll.
	p = defaultPrefs("")
	p.ResetSettings()
	if !snapshotMatchesInline(p) {
		t.Error("snapshot != inline after ResetSettings")
	}
	p.ResetAll()
	if !snapshotMatchesInline(p) {
		t.Error("snapshot != inline after ResetAll")
	}
}

// TestSettingsCoversDataFields asserts Settings mirrors every AssetPreferences data
// field (exported and unexported) except the runtime machinery, listed as exclusions.
func TestSettingsCoversDataFields(t *testing.T) {
	settingsType := reflect.TypeOf(Settings{})
	apType := reflect.TypeOf(AssetPreferences{})
	excluded := map[string]bool{
		"mu": true, "path": true, "dirty": true, "stop": true, "done": true,
		"pending": true, "saveDelay": true, "closeOnce": true, "onSaveErr": true,
		"frozen": true, "quarantine": true, "themeFitMovedToNative": true,
		"themeFitStampPending": true, "formatGen": true, "wardrobeGen": true,
		"settings": true, "stale": true,
	}
	for i := 0; i < apType.NumField(); i++ {
		name := apType.Field(i).Name
		if excluded[name] {
			continue
		}
		if _, ok := settingsType.FieldByName(name); !ok {
			t.Errorf("Settings is missing AssetPreferences data field %s", name)
		}
	}
	for i := 0; i < settingsType.NumField(); i++ {
		name := settingsType.Field(i).Name
		if _, ok := apType.FieldByName(name); !ok {
			t.Errorf("Settings field %s is not an AssetPreferences field", name)
		}
	}
}
