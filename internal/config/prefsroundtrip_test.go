package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// tempPrefsPath / loadPrefs are the two-line preamble every round-trip test in
// this package writes out by hand; named here so the ones below read as the
// assertion they are.
func tempPrefsPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "prefs.json")
}

func loadPrefs(t *testing.T, path string) *AssetPreferences {
	t.Helper()
	p, err := New(path)
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// Preferences are SAVED by marshalling AssetPreferences (SaveNow) and LOADED by
// unmarshalling into prefsJSON and then copying field by field in a hand-written
// overlay. Two structs, two lists, no compiler tying them together — so a new
// preference that joins only the first one saves perfectly and silently reverts
// to its default on every launch.
//
// That is not hypothetical: assetCharCase shipped that way and stayed broken
// until this gate was written. The trap is called out in CLAUDE.md and was still
// unenforced by anything.
//
// This closes the class. Every json tag on AssetPreferences must have a twin on
// prefsJSON, unless it is on the deliberate list below with a reason.

// saveOnlyPrefTags are the AssetPreferences json tags that intentionally have no
// prefsJSON twin. Each needs a reason, and the gate below fails if an entry here
// stops being save-only — so an accidental fix cannot leave a stale excuse behind.
var saveOnlyPrefTags = map[string]string{}

// prefJSONTags collects the json tag name of every exported field of t,
// following embedded structs the way encoding/json does.
func prefJSONTags(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" { // unexported: encoding/json ignores it entirely
				continue
			}
			tag := f.Tag.Get("json")
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
				walk(f.Type)
				continue
			}
			if name == "" {
				name = f.Name
			}
			out[name] = true
		}
	}
	walk(t)
	return out
}

func TestEverySavedPreferenceIsAlsoLoaded(t *testing.T) {
	saved := prefJSONTags(reflect.TypeOf(AssetPreferences{}))
	loaded := prefJSONTags(reflect.TypeOf(prefsJSON{}))

	for tag := range saved {
		if loaded[tag] {
			continue
		}
		if why, ok := saveOnlyPrefTags[tag]; ok {
			t.Logf("save-only by design: %q — %s", tag, why)
			continue
		}
		t.Errorf("preference %q is written by SaveNow but has no prefsJSON field, so it saves and never loads back. "+
			"Add the field to prefsJSON with the SAME json tag AND a line to the load overlay, "+
			"or list it in saveOnlyPrefTags with a reason.", tag)
	}

	// The allow-list must not rot: an entry that has since gained a DTO field is a
	// stale excuse, and one that is no longer saved at all is dead weight.
	for tag := range saveOnlyPrefTags {
		if !saved[tag] {
			t.Errorf("saveOnlyPrefTags[%q] names a tag AssetPreferences no longer has", tag)
		}
		if loaded[tag] {
			t.Errorf("saveOnlyPrefTags[%q] is loaded now — drop the exemption", tag)
		}
	}
}

// TestAssetCharCasingRoundTrips is the instance the gate above was written for:
// the power-user character-folder casing saved but reverted to lowercase on every
// launch, which silently 404s every character on a capitalised-folder server for
// anyone who set it and then restarted.
func TestAssetCharCasingRoundTrips(t *testing.T) {
	path := tempPrefsPath(t)
	p := loadPrefs(t, path)
	if got := p.AssetCharCasing(); got != 0 {
		t.Fatalf("default casing = %d, want 0 (lowercase — the safe default)", got)
	}
	p.SetAssetCharCasing(2) // title case
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}
	if got := loadPrefs(t, path).AssetCharCasing(); got != 2 {
		t.Errorf("casing after reload = %d, want 2 — it saved but did not load back", got)
	}
}

// TestProxyPrefsRoundTrip: the whole point of the proxy setting is that it
// survives a restart. A user who switches to "never use a proxy" because the
// detected one cannot carry AO traffic must not find themselves back on it the
// next time they launch.
func TestProxyPrefsRoundTrip(t *testing.T) {
	path := tempPrefsPath(t)
	p := loadPrefs(t, path)
	if p.ProxyMode() != ProxyModeSystem {
		t.Fatalf("default proxy mode = %d, want ProxyModeSystem — ignoring the machine's setting is the bug", p.ProxyMode())
	}
	if p.ProxyURLValue() != "" {
		t.Fatalf("default proxy URL = %q, want empty", p.ProxyURLValue())
	}
	p.SetProxyMode(ProxyModeManual)
	p.SetProxyURL("  socks5://box.example:1080  ") // trimmed on the way in
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}
	back := loadPrefs(t, path)
	if back.ProxyMode() != ProxyModeManual {
		t.Errorf("mode after reload = %d, want ProxyModeManual", back.ProxyMode())
	}
	if got := back.ProxyURLValue(); got != "socks5://box.example:1080" {
		t.Errorf("url after reload = %q, want it trimmed and preserved", got)
	}

	// The escape hatch specifically: mode 1 is not the zero value, but mode 0 is,
	// and a user switching BACK to 0 must persist that rather than be
	// indistinguishable from a file written before the setting existed.
	back.SetProxyMode(ProxyModeDirect)
	if err := back.SaveNow(); err != nil {
		t.Fatal(err)
	}
	if got := loadPrefs(t, path).ProxyMode(); got != ProxyModeDirect {
		t.Errorf("mode after reload = %d, want ProxyModeDirect — the escape hatch must survive a restart", got)
	}
}

// TestProxyModeClampsGarbage: an out-of-range mode indexes past the label table,
// and the safe landing is System — the shipped default — rather than Direct,
// which would silently turn the feature off for someone whose file got mangled.
func TestProxyModeClampsGarbage(t *testing.T) {
	path := tempPrefsPath(t)
	p := loadPrefs(t, path)
	for _, bad := range []int{-1, ProxyModeCount, ProxyModeCount + 50} {
		p.SetProxyMode(bad)
		if got := p.ProxyMode(); got != ProxyModeSystem {
			t.Errorf("SetProxyMode(%d) landed on %d, want ProxyModeSystem", bad, got)
		}
	}
	// ...and on the way in too, because a hand-edited file is the other door.
	p.SetProxyMode(ProxyModeManual)
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mangled := strings.Replace(string(raw), `"proxyMode": 2`, `"proxyMode": 99`, 1)
	if mangled == string(raw) {
		t.Fatalf("could not find the proxyMode value to mangle in:\n%s", raw)
	}
	if err := os.WriteFile(path, []byte(mangled), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadPrefs(t, path).ProxyMode(); got != ProxyModeSystem {
		t.Errorf("a hand-edited mode of 99 loaded as %d, want the System fallback", got)
	}
}

// TestAssetCharCasingClampsGarbage: the value indexes a mode table, and the one
// way an out-of-range number can enter is a hand-edited preferences file. Out of
// range falls back to lowercase, matching URLBuilder.WithCharCase's own answer to
// a mode it does not recognise.
func TestAssetCharCasingClampsGarbage(t *testing.T) {
	path := tempPrefsPath(t)
	p := loadPrefs(t, path)
	p.SetAssetCharCasing(AssetCharCaseMax + 7)
	if got := p.AssetCharCasing(); got != 0 {
		t.Errorf("setter accepted an out-of-range mode: got %d, want the lowercase fallback", got)
	}
	// ...and the same on the way in, because the setter is not the only door.
	p.SetAssetCharCasing(AssetCharCaseMax)
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}
	if got := loadPrefs(t, path).AssetCharCasing(); got != AssetCharCaseMax {
		t.Errorf("the highest VALID mode must survive a reload: got %d, want %d", got, AssetCharCaseMax)
	}
}
