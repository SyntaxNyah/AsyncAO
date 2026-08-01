package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// TestPanelFontsRoundTrip: these are eight separate settings a user tunes once
// and expects to keep. Losing them on restart would be worse than not having the
// feature — they would have to be re-typed every launch.
func TestPanelFontsRoundTrip(t *testing.T) {
	path := tempPrefsPath(t)
	p := loadPrefs(t, path)
	if len(p.PanelFonts()) != 0 {
		t.Fatalf("a fresh install has overrides: %v", p.PanelFonts())
	}
	p.SetPanelFont("ic_chatlog", PanelFont{Family: "  Comic Sans MS  ", SizePx: 18})
	p.SetPanelFont("music_list", PanelFont{SizePx: 22}) // size only
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}
	back := loadPrefs(t, path)
	if got := back.PanelFontFor("ic_chatlog"); got.Family != "Comic Sans MS" || got.SizePx != 18 {
		t.Errorf("ic_chatlog = %+v, want the trimmed family and 18 px", got)
	}
	if got := back.PanelFontFor("music_list"); got.Family != "" || got.SizePx != 22 {
		t.Errorf("music_list = %+v, want a size-only override", got)
	}
	// Keyed case-insensitively, since the identifier is also a theme INI key.
	if got := back.PanelFontFor("IC_CHATLOG"); got.SizePx != 18 {
		t.Errorf("lookup is case-sensitive: %+v", got)
	}
}

// TestPanelFontsClearRemovesTheKey: an element the user RESET must look
// identical on disk to one they never touched, or a later change to the default
// would never reach them.
func TestPanelFontsClearRemovesTheKey(t *testing.T) {
	path := tempPrefsPath(t)
	p := loadPrefs(t, path)
	p.SetPanelFont("ic_chatlog", PanelFont{Family: "Arial", SizePx: 14})
	p.SetPanelFont("ic_chatlog", PanelFont{}) // the user emptied both boxes
	if got := p.PanelFonts(); len(got) != 0 {
		t.Errorf("an emptied override was stored as a zero value: %v", got)
	}
	p.SetPanelFont("music_list", PanelFont{SizePx: 20})
	p.SetPanelFont("area_list", PanelFont{SizePx: 20})
	p.ClearPanelFonts()
	if got := p.PanelFonts(); len(got) != 0 {
		t.Errorf("ClearPanelFonts left %v — it is the way out of a font you cannot read", got)
	}
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}
	if got := loadPrefs(t, path).PanelFonts(); len(got) != 0 {
		t.Errorf("cleared overrides came back after a reload: %v", got)
	}
}

// TestPanelFontsAreBounded: the map comes off disk, so a hand-edited or corrupt
// file must not produce an unbounded map or an unusable size (hard rule 4).
func TestPanelFontsAreBounded(t *testing.T) {
	huge := map[string]PanelFont{}
	for i := 0; i < PanelFontMaxEntries*3; i++ {
		huge["elem"+strconv.Itoa(i)] = PanelFont{SizePx: 14}
	}
	if got := len(sanitizePanelFonts(huge)); got > PanelFontMaxEntries {
		t.Errorf("sanitised to %d entries, cap is %d", got, PanelFontMaxEntries)
	}
	out := sanitizePanelFonts(map[string]PanelFont{
		"tiny":    {SizePx: 1},                                     // below the floor -> dropped
		"huge":    {SizePx: 100000},                                // above the ceiling -> dropped
		"longfam": {Family: strings.Repeat("x", 5000), SizePx: 14}, // absurd family -> family dropped, size kept
		"blank":   {},                                              // nothing at all -> no entry
		"good":    {Family: "Arial", SizePx: 14},
	})
	if _, ok := out["tiny"]; ok {
		t.Error("a 1 px size survived — it is not legible at any DPI")
	}
	if _, ok := out["huge"]; ok {
		t.Error("a 100000 px size survived")
	}
	if _, ok := out["blank"]; ok {
		t.Error("an entirely empty override was kept")
	}
	if got := out["longfam"]; got.Family != "" || got.SizePx != 14 {
		t.Errorf("longfam = %+v, want the family dropped and the size kept", got)
	}
	if got := out["good"]; got.Family != "Arial" || got.SizePx != 14 {
		t.Errorf("a valid entry was mangled: %+v", got)
	}
	if sanitizePanelFonts(nil) != nil {
		t.Error("an empty map must sanitise to nil, not to an allocated empty one")
	}
}

// TestPanelFontsHandsOutACopy: the caller reads this off the render thread while
// the debounced saver marshals the original under its own lock. Handing out the
// live map is a data race that only shows up on an unlucky interleaving.
func TestPanelFontsHandsOutACopy(t *testing.T) {
	p := loadPrefs(t, tempPrefsPath(t))
	p.SetPanelFont("ic_chatlog", PanelFont{SizePx: 14})
	got := p.PanelFonts()
	got["ic_chatlog"] = PanelFont{SizePx: 99}
	got["injected"] = PanelFont{SizePx: 99}
	if p.PanelFontFor("ic_chatlog").SizePx != 14 {
		t.Error("mutating the returned map reached the live preferences")
	}
	if _, ok := p.PanelFonts()["injected"]; ok {
		t.Error("a key added to the returned map reached the live preferences")
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
