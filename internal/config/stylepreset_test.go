package config

import "testing"

// TestStylePresetCRUD pins #126's saved-mood store: add (returning a copy), overwrite by name,
// the cap, delete, and the opacity clamp.
func TestStylePresetCRUD(t *testing.T) {
	p, _ := newTestPrefs(t)
	if got := p.StylePresets(); got != nil {
		t.Fatalf("fresh prefs have presets: %v", got)
	}
	p.AddStylePreset(StylePreset{Name: "Angry", Style: SpriteStylePref{Tint: true, R: 255}, Color: 2})
	p.AddStylePreset(StylePreset{Name: "Calm", Color: 0})
	if got := p.StylePresets(); len(got) != 2 || got[0].Name != "Angry" || got[1].Name != "Calm" {
		t.Fatalf("presets = %v, want Angry+Calm", got)
	}
	// A blank name is ignored.
	p.AddStylePreset(StylePreset{Name: "   "})
	if len(p.StylePresets()) != 2 {
		t.Error("a blank-named preset was stored")
	}
	// Same name overwrites in place (a "save" updates the mood).
	p.AddStylePreset(StylePreset{Name: "Angry", Color: 5, Style: SpriteStylePref{Opacity: 250}})
	got := p.StylePresets()
	if len(got) != 2 || got[0].Color != 5 {
		t.Fatalf("overwrite didn't update Angry in place: %v", got)
	}
	if got[0].Style.Opacity != 100 {
		t.Errorf("opacity not clamped on add: %d", got[0].Style.Opacity)
	}
	// The returned slice is a copy — mutating it can't corrupt the store.
	got[0].Name = "Hacked"
	if p.StylePresets()[0].Name != "Angry" {
		t.Error("StylePresets() returned the live slice, not a copy")
	}
	p.DeleteStylePreset(0)
	if g := p.StylePresets(); len(g) != 1 || g[0].Name != "Calm" {
		t.Fatalf("delete left %v, want just Calm", g)
	}
}

// TestStylePresetKeys pins the bind machinery: a key maps to exactly one preset (re-binding it
// elsewhere clears it from the first), and StylePresetForKey resolves it.
func TestStylePresetKeys(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.AddStylePreset(StylePreset{Name: "A"})
	p.AddStylePreset(StylePreset{Name: "B"})
	p.SetStylePresetKey(0, "F1")
	if pr, ok := p.StylePresetForKey("f1"); !ok || pr.Name != "A" { // case-insensitive
		t.Fatalf("key f1 → %+v ok=%v, want A", pr, ok)
	}
	// Binding the same key to B steals it from A.
	p.SetStylePresetKey(1, "f1")
	if pr, ok := p.StylePresetForKey("f1"); !ok || pr.Name != "B" {
		t.Fatalf("after rebind, f1 → %+v, want B", pr)
	}
	if p.StylePresets()[0].Key != "" {
		t.Error("rebinding f1 to B didn't clear it from A")
	}
	// Clearing a key.
	p.SetStylePresetKey(1, "")
	if _, ok := p.StylePresetForKey("f1"); ok {
		t.Error("cleared key still resolves")
	}
}

// TestStylePresetCap pins the §17.4 bound.
func TestStylePresetCap(t *testing.T) {
	p, _ := newTestPrefs(t)
	for i := 0; i < stylePresetCap+10; i++ {
		p.AddStylePreset(StylePreset{Name: string(rune('a'+i%26)) + string(rune('0'+i/26))})
	}
	if n := len(p.StylePresets()); n > stylePresetCap {
		t.Errorf("stored %d presets, want capped at %d", n, stylePresetCap)
	}
}
