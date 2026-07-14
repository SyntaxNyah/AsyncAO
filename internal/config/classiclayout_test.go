package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeClassicLayout(t *testing.T) {
	in := map[string][4]float64{
		"ok":     {0.5, 0.5, 0.3, 0.3},
		"offneg": {-0.5, 0.1, 0.2, 0.2}, // slightly off-stage X is allowed by design
		"nan":    {math.NaN(), 0.1, 0.2, 0.2},
		"inf":    {0.1, math.Inf(1), 0.2, 0.2},
		"zeroW":  {0.1, 0.1, 0, 0.2},
		"negW":   {0.1, 0.1, -0.2, 0.2},
		"huge":   {0.1, 0.1, 9, 0.2},
		"":       {0.1, 0.1, 0.2, 0.2}, // empty name dropped
	}
	out := sanitizeClassicLayout(in)
	for _, good := range []string{"ok", "offneg"} {
		if _, ok := out[good]; !ok {
			t.Errorf("sanitize dropped valid slot %q", good)
		}
	}
	for _, bad := range []string{"nan", "inf", "zeroW", "negW", "huge", ""} {
		if _, ok := out[bad]; ok {
			t.Errorf("sanitize kept invalid slot %q", bad)
		}
	}
}

func TestSanitizeClassicLayoutEmpty(t *testing.T) {
	if got := sanitizeClassicLayout(nil); got != nil {
		t.Errorf("sanitize(nil) = %v, want nil", got)
	}
	if got := sanitizeClassicLayout(map[string][4]float64{}); got != nil {
		t.Errorf("sanitize(empty) = %v, want nil", got)
	}
}

func TestClassicSlotRoundTrip(t *testing.T) {
	p := &AssetPreferences{}
	frac := [4]float64{0.25, 0.1, 0.5, 0.4}
	p.SetClassicSlot("viewport", frac)
	got := p.ClassicLayoutOverrides()
	if got["viewport"] != frac {
		t.Fatalf("round-trip = %+v, want %+v", got["viewport"], frac)
	}
	// Returned map is a copy — mutating it must not touch the stored prefs.
	got["viewport"] = [4]float64{}
	if p.ClassicLayoutOverrides()["viewport"] != frac {
		t.Fatalf("ClassicLayoutOverrides leaked the internal map")
	}
	p.ClearClassicSlot("viewport")
	if len(p.ClassicLayoutOverrides()) != 0 {
		t.Fatalf("ClearClassicSlot left entries")
	}
}

func TestClassicSlotCap(t *testing.T) {
	p := &AssetPreferences{}
	for i := 0; i < classicSlotCap+10; i++ {
		p.SetClassicSlot(string(rune('a'+i%26))+string(rune('0'+i/26)), [4]float64{0.1, 0.1, 0.2, 0.2})
	}
	if n := len(p.ClassicLayoutOverrides()); n > classicSlotCap {
		t.Fatalf("classic slots = %d, exceeds cap %d", n, classicSlotCap)
	}
}

// TestLayoutProfileRoundTrip pins A6: save a full-state profile (all four axes),
// reload it through the disk DTO + overlay, and assert every axis survives. This
// exercises the save side (AssetPreferences json tags) AND the load side
// (prefsJSON DTO + sanitizeLayoutProfiles overlay).
func TestLayoutProfileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	prof := LayoutProfile{
		Classic: map[string][4]float64{"viewport": {0.0, 0.0, 0.74, 0.66}, "ooc": {0.1, 0.7, 0.4, 0.2}},
		Anchors: map[string]ClassicAnchor{"viewport": {Mode: "lt", WinW: 1280, WinH: 720}},
		Hidden:  []string{"emotes", "ooc"},
		GridPx:  16,
	}
	p.SaveLayoutProfile("  streamer ", prof) // name is trimmed
	if names := p.LayoutProfileNames(); len(names) != 1 || names[0] != "streamer" {
		t.Fatalf("profile names = %v, want [streamer]", names)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := q.LayoutProfile("streamer")
	if got.Classic["viewport"] != prof.Classic["viewport"] || got.Classic["ooc"] != prof.Classic["ooc"] {
		t.Fatalf("Classic axis lost: %+v", got.Classic)
	}
	if got.Anchors["viewport"] != prof.Anchors["viewport"] {
		t.Fatalf("Anchors axis lost: %+v", got.Anchors)
	}
	if len(got.Hidden) != 2 {
		t.Fatalf("Hidden axis lost: %+v", got.Hidden)
	}
	if got.GridPx != 16 {
		t.Fatalf("GridPx axis lost: %d", got.GridPx)
	}
	// The returned profile must be a deep copy — mutating it can't touch storage.
	got.Classic["viewport"] = [4]float64{}
	got.Hidden[0] = "junk"
	if q.LayoutProfile("streamer").Classic["viewport"] != prof.Classic["viewport"] {
		t.Fatalf("LayoutProfile leaked the Classic map")
	}
	if q.LayoutProfile("streamer").Hidden[0] != "emotes" {
		t.Fatalf("LayoutProfile leaked the Hidden slice")
	}
}

// TestLayoutProfileSanitizeAndCap pins the blank-name / empty-profile / junk-axis
// rejections, the deduped+capped Hidden set, and the profile-count bound.
func TestLayoutProfileSanitizeAndCap(t *testing.T) {
	p := &AssetPreferences{}
	p.SaveLayoutProfile("", LayoutProfile{Classic: map[string][4]float64{"viewport": {0, 0, 0.5, 0.5}}}) // blank name
	p.SaveLayoutProfile("empty", LayoutProfile{})                                                        // nothing in any axis
	p.SaveLayoutProfile("garbage", LayoutProfile{Classic: map[string][4]float64{"v": {0, 0, -1, -1}}})   // only invalid slots → empty
	if n := len(p.LayoutProfileNames()); n != 0 {
		t.Fatalf("guards let through %d profile(s), want 0", n)
	}
	// A profile with a good slot but junk anchors + over-cap dup Hidden keeps only
	// the valid axes: good slot stays, junk anchor dropped, Hidden deduped+capped.
	dupHidden := make([]string, 0, maxHiddenPanels+20)
	for i := 0; i < maxHiddenPanels+20; i++ {
		dupHidden = append(dupHidden, "id"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	dupHidden = append(dupHidden, dupHidden[0]) // a duplicate must be dropped
	p.SaveLayoutProfile("mixed", LayoutProfile{
		Classic: map[string][4]float64{"good": {0.1, 0.1, 0.3, 0.3}, "bad": {0, 0, 0, 0}},
		Anchors: map[string]ClassicAnchor{"good": {Mode: "zz", WinW: -1, WinH: -1}}, // invalid mode + window
		Hidden:  dupHidden,
	})
	mixed := p.LayoutProfile("mixed")
	if len(mixed.Classic) != 1 || mixed.Classic["good"] == ([4]float64{}) {
		t.Fatalf("mixed.Classic = %+v, want only the good slot", mixed.Classic)
	}
	if len(mixed.Anchors) != 0 {
		t.Fatalf("mixed.Anchors kept a junk anchor: %+v", mixed.Anchors)
	}
	if len(mixed.Hidden) > maxHiddenPanels {
		t.Fatalf("mixed.Hidden = %d, exceeds cap %d", len(mixed.Hidden), maxHiddenPanels)
	}
	// Profile-count cap.
	for i := 0; i < layoutProfileCap+5; i++ {
		p.SaveLayoutProfile(string(rune('a'+i%26))+string(rune('0'+i/26)), LayoutProfile{GridPx: 8})
	}
	if n := len(p.LayoutProfileNames()); n > layoutProfileCap {
		t.Fatalf("profiles = %d, exceeds cap %d", n, layoutProfileCap)
	}
}

// TestLayoutProfileMigrationFromPresets pins the one-way load migration: a file
// written by an older build carries the legacy layoutPresets key and no
// layoutProfiles; each preset must become a Classic-only profile on load.
func TestLayoutProfileMigrationFromPresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	// Hand-write a legacy prefs file (the save-side preset API is retired, so this
	// is the only way to synthesize one).
	legacy := `{"layoutPresets":{"streamer":{"viewport":[0,0,0.74,0.66]},"wide":{"ooc":[0.1,0.7,0.4,0.2]}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy prefs: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	names := q.LayoutProfileNames()
	if len(names) != 2 {
		t.Fatalf("migrated profile count = %d (%v), want 2", len(names), names)
	}
	streamer := q.LayoutProfile("streamer")
	if streamer.Classic["viewport"] != ([4]float64{0, 0, 0.74, 0.66}) {
		t.Fatalf("streamer.Classic not migrated: %+v", streamer.Classic)
	}
	// Migration is Classic-ONLY: the other axes stay empty.
	if len(streamer.Anchors) != 0 || len(streamer.Hidden) != 0 || streamer.GridPx != 0 {
		t.Fatalf("migration populated a non-Classic axis: %+v", streamer)
	}
}

// TestLayoutProfileMigrationNoClobber pins the migration's no-clobber guard: when a
// file carries BOTH the legacy layoutPresets key AND a non-empty layoutProfiles,
// the presets are IGNORED (a newer file already owns the profiles key) — only the
// pre-existing profiles survive, and neither preset name leaks in as a profile.
func TestLayoutProfileMigrationNoClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	// A file with a real full-state profile ("kept") AND a stale legacy preset
	// ("streamer"). The guard (len(LayoutProfiles)==0) is false, so migration must
	// not run — the preset is dropped, not wrapped into a profile.
	both := `{"layoutProfiles":{"kept":{"classic":{"ooc":[0.1,0.7,0.4,0.2]},"gridPx":8}},` +
		`"layoutPresets":{"streamer":{"viewport":[0,0,0.74,0.66]}}}`
	if err := os.WriteFile(path, []byte(both), 0o644); err != nil {
		t.Fatalf("write prefs: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	names := q.LayoutProfileNames()
	if len(names) != 1 || names[0] != "kept" {
		t.Fatalf("no-clobber failed: profiles = %v, want [kept] only (preset ignored)", names)
	}
	// The existing profile is intact (its own axes survive)…
	kept := q.LayoutProfile("kept")
	if kept.Classic["ooc"] != ([4]float64{0.1, 0.7, 0.4, 0.2}) || kept.GridPx != 8 {
		t.Fatalf("existing profile clobbered: %+v", kept)
	}
	// …and the legacy preset did NOT sneak in as a profile.
	if streamer := q.LayoutProfile("streamer"); len(streamer.Classic) != 0 {
		t.Fatalf("legacy preset was migrated despite existing profiles: %+v", streamer)
	}
}

// TestClassicAnchorsRoundTrip pins the ClassicAnchors axis (previously untested):
// a single-slot anchor survives save→reload.
func TestClassicAnchorsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	want := ClassicAnchor{Mode: "rb", WinW: 1600, WinH: 900}
	p.SetClassicAnchor("viewport", want)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := q.ClassicAnchorSnapshot()["viewport"]; got != want {
		t.Fatalf("anchor round-trip = %+v, want %+v", got, want)
	}
}

// TestSetClassicAnchorsWholesale pins the new wholesale setter: it copies (never
// aliases), sanitizes junk, and respects classicSlotCap.
func TestSetClassicAnchorsWholesale(t *testing.T) {
	p := &AssetPreferences{}
	in := map[string]ClassicAnchor{
		"viewport": {Mode: "lt", WinW: 1280, WinH: 720},
		"junk":     {Mode: "zz", WinW: 0, WinH: 0}, // invalid mode + window → dropped
	}
	p.SetClassicAnchors(in)
	got := p.ClassicAnchorSnapshot()
	if len(got) != 1 || got["viewport"] != in["viewport"] {
		t.Fatalf("wholesale set = %+v, want only viewport", got)
	}
	// Mutating the caller's input must not touch storage (copy-never-alias).
	in["viewport"] = ClassicAnchor{Mode: "cc", WinW: 1, WinH: 1}
	if p.ClassicAnchorSnapshot()["viewport"] != (ClassicAnchor{Mode: "lt", WinW: 1280, WinH: 720}) {
		t.Fatalf("SetClassicAnchors aliased the caller's map")
	}
	// Cap.
	big := make(map[string]ClassicAnchor, classicSlotCap+10)
	for i := 0; i < classicSlotCap+10; i++ {
		big[string(rune('a'+i%26))+string(rune('0'+i/26))] = ClassicAnchor{Mode: "lt", WinW: 800, WinH: 600}
	}
	p.SetClassicAnchors(big)
	if n := len(p.ClassicAnchorSnapshot()); n > classicSlotCap {
		t.Fatalf("anchors = %d, exceeds cap %d", n, classicSlotCap)
	}
	// A nil/empty map clears all pins.
	p.SetClassicAnchors(nil)
	if n := len(p.ClassicAnchorSnapshot()); n != 0 {
		t.Fatalf("SetClassicAnchors(nil) left %d entries", n)
	}
}

// TestHiddenPanelsRoundTripDedupCap pins the hidden-panel axis (previously
// untested AND uncapped): SetHiddenPanels dedups blanks/duplicates, bounds at
// maxHiddenPanels, and round-trips.
func TestHiddenPanelsRoundTripDedupCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	p.SetHiddenPanels([]string{"emotes", "", "ooc", "emotes"}) // blank + dup dropped
	got := p.HiddenPanels()
	if len(got) != 2 || got[0] != "emotes" || got[1] != "ooc" {
		t.Fatalf("dedup = %v, want [emotes ooc]", got)
	}
	// Cap.
	over := make([]string, 0, maxHiddenPanels+20)
	for i := 0; i < maxHiddenPanels+20; i++ {
		over = append(over, "p"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	p.SetHiddenPanels(over)
	if n := len(p.HiddenPanels()); n > maxHiddenPanels {
		t.Fatalf("hidden = %d, exceeds cap %d", n, maxHiddenPanels)
	}
	// Round-trip a modest set through disk.
	p.SetHiddenPanels([]string{"emotes", "log"})
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := q.HiddenPanels(); len(got) != 2 {
		t.Fatalf("hidden round-trip = %v, want 2 entries", got)
	}
}
