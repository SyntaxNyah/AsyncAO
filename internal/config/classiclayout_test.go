package config

import (
	"math"
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

// TestLayoutPresetRoundTrip pins #34: save a named layout snapshot, list it, load a
// COPY (never an alias), and delete it.
func TestLayoutPresetRoundTrip(t *testing.T) {
	p := &AssetPreferences{}
	layout := map[string][4]float64{
		"viewport": {0.0, 0.0, 0.74, 0.66},
		"ooc":      {0.1, 0.7, 0.4, 0.2},
	}
	p.SaveLayoutPreset("  streamer ", layout) // name is trimmed
	names := p.LayoutPresetNames()
	if len(names) != 1 || names[0] != "streamer" {
		t.Fatalf("preset names = %v, want [streamer]", names)
	}
	got := p.LayoutPreset("streamer")
	if got["viewport"] != layout["viewport"] || got["ooc"] != layout["ooc"] {
		t.Fatalf("loaded preset = %+v, want %+v", got, layout)
	}
	// Returned map is a copy — mutating it must not touch the stored preset.
	got["viewport"] = [4]float64{}
	if p.LayoutPreset("streamer")["viewport"] != layout["viewport"] {
		t.Fatalf("LayoutPreset leaked the internal map")
	}
	p.DeleteLayoutPreset("streamer")
	if len(p.LayoutPresetNames()) != 0 {
		t.Fatalf("DeleteLayoutPreset left entries")
	}
}

// TestSaveLayoutPresetGuards pins the blank-name / empty-map / invalid-slot rejections.
func TestSaveLayoutPresetGuards(t *testing.T) {
	p := &AssetPreferences{}
	p.SaveLayoutPreset("", map[string][4]float64{"viewport": {0, 0, 0.5, 0.5}}) // blank name
	p.SaveLayoutPreset("empty", map[string][4]float64{})                        // empty map
	p.SaveLayoutPreset("garbage", map[string][4]float64{"v": {0, 0, -1, -1}})   // all slots invalid → empty
	if n := len(p.LayoutPresetNames()); n != 0 {
		t.Fatalf("guards let through %d preset(s), want 0", n)
	}
	// A preset with one good + one bad slot keeps only the good one (sanitize inherited).
	p.SaveLayoutPreset("mixed", map[string][4]float64{"good": {0.1, 0.1, 0.3, 0.3}, "bad": {0, 0, 0, 0}})
	if got := p.LayoutPreset("mixed"); len(got) != 1 || got["good"] == ([4]float64{}) {
		t.Fatalf("mixed preset = %+v, want only the good slot", got)
	}
}

// TestLayoutPresetCap bounds how many presets persist (#34).
func TestLayoutPresetCap(t *testing.T) {
	p := &AssetPreferences{}
	for i := 0; i < layoutPresetCap+5; i++ {
		p.SaveLayoutPreset(string(rune('a'+i%26))+string(rune('0'+i/26)), map[string][4]float64{"viewport": {0.1, 0.1, 0.2, 0.2}})
	}
	if n := len(p.LayoutPresetNames()); n > layoutPresetCap {
		t.Fatalf("presets = %d, exceeds cap %d", n, layoutPresetCap)
	}
}
