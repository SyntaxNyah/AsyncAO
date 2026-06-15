package config

import (
	"path/filepath"
	"testing"
)

// TestQoLPrefDefaults pins the out-of-the-box values for the QoL-roadmap prefs:
// previews ON at a 5 s dwell, and the three opt-ins (banner, sprite-move,
// desk-follows-manifest) OFF.
func TestQoLPrefDefaults(t *testing.T) {
	p, _ := newTestPrefs(t)
	if !p.SpritePreviewsOn() {
		t.Error("SpritePreviewsOn default must be true")
	}
	if got := p.PreviewHoverMillis(); got != DefaultPreviewHoverMs {
		t.Errorf("PreviewHoverMillis default = %d, want %d", got, DefaultPreviewHoverMs)
	}
	if p.AssetWarningsOn() {
		t.Error("AssetWarningsOn default must be false (banner is opt-in)")
	}
	if p.SpriteMoveEnabled() {
		t.Error("SpriteMoveEnabled default must be false")
	}
	if p.DeskFollowsManifest() {
		t.Error("DeskFollowsManifest default must be false (desks stay WebP)")
	}
	if !p.AutoLoginToastOn() {
		t.Error("AutoLoginToastOn default must be true")
	}
}

// TestPreviewHoverClamp pins the dwell bounds (the setter is authoritative).
func TestPreviewHoverClamp(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.SetPreviewHoverMs(1 << 30)
	if got := p.PreviewHoverMillis(); got != maxPreviewHoverMs {
		t.Errorf("clamp hi = %d, want %d", got, maxPreviewHoverMs)
	}
	p.SetPreviewHoverMs(1)
	if got := p.PreviewHoverMillis(); got != minPreviewHoverMs {
		t.Errorf("clamp lo = %d, want %d", got, minPreviewHoverMs)
	}
}

// TestQoLPrefRoundTrip pins that the QoL prefs survive save→load — in
// particular that an explicit SpritePreviews=false isn't clobbered by its
// absent-default-ON pointer field.
func TestQoLPrefRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	p.SetSpritePreviews(false)
	p.SetPreviewHoverMs(8000)
	p.SetAssetWarnings(true)
	p.SetSpriteMove(true)
	p.SetDeskFollowManifest(true)
	p.SetAutoLoginToast(false) // explicit false must survive the absent-default-ON pointer
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if q.SpritePreviewsOn() {
		t.Error("SpritePreview=false lost (absent-default ON must not clobber explicit false)")
	}
	if got := q.PreviewHoverMillis(); got != 8000 {
		t.Errorf("PreviewHoverMillis = %d, want 8000", got)
	}
	if !q.AssetWarningsOn() {
		t.Error("ShowAssetWarnings lost")
	}
	if !q.SpriteMoveEnabled() {
		t.Error("SpriteMove lost")
	}
	if !q.DeskFollowsManifest() {
		t.Error("DeskFollowManifest lost")
	}
	if q.AutoLoginToastOn() {
		t.Error("AutoLoginToast=false lost (absent-default ON must not clobber explicit false)")
	}
}

// TestWardrobeGenerationBumps pins the counter the char-select star grid caches
// against: it moves on a real membership change and stays put on a no-op.
func TestWardrobeGenerationBumps(t *testing.T) {
	p, _ := newTestPrefs(t)
	const key = "wss://srv.example:2096"
	g0 := p.WardrobeGeneration()
	if !p.AddWardrobe(key, "Phoenix") {
		t.Fatal("AddWardrobe should report a change")
	}
	g1 := p.WardrobeGeneration()
	if g1 == g0 {
		t.Error("WardrobeGeneration must bump on AddWardrobe")
	}
	p.AddWardrobe(key, "phoenix") // duplicate (case-insensitive) — no change
	if p.WardrobeGeneration() != g1 {
		t.Error("duplicate AddWardrobe must not bump the generation")
	}
	if !p.RemoveWardrobe(key, "Phoenix") {
		t.Fatal("RemoveWardrobe should report a change")
	}
	if p.WardrobeGeneration() == g1 {
		t.Error("WardrobeGeneration must bump on RemoveWardrobe")
	}
}

// TestClearLearnedType pins that clearing one type's learned formats (the
// desk-policy toggle path) leaves other types alone.
func TestClearLearnedType(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.RecordLearned("h.example", TypeDeskOverlay, ExtPNG)
	p.RecordLearned("h.example", TypeBackground, ExtPNG)
	p.ClearLearnedType(TypeDeskOverlay)
	snap := p.LearnedSnapshot()
	if _, ok := snap[LearnedKey("h.example", TypeDeskOverlay)]; ok {
		t.Error("ClearLearnedType(DeskOverlay) must drop desk learned entries")
	}
	if _, ok := snap[LearnedKey("h.example", TypeBackground)]; !ok {
		t.Error("ClearLearnedType(DeskOverlay) must NOT drop other types")
	}
}

// TestThemeFitDefaultsAndClamp pins the theme-fit prefs: Stretch is the default
// (zero value), and the mode/zoom/pan all clamp to their bounds.
func TestThemeFitDefaultsAndClamp(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.ThemeFitMode() != ThemeFitStretch {
		t.Errorf("ThemeFitMode default = %d, want Stretch(%d)", p.ThemeFitMode(), ThemeFitStretch)
	}
	if p.ThemeZoom() != DefaultThemeZoom {
		t.Errorf("ThemeZoom default = %d, want %d", p.ThemeZoom(), DefaultThemeZoom)
	}
	if !p.PlainLobbyOn() {
		t.Error("PlainLobbyOn default must be true (readable server list)")
	}
	p.SetThemeFit(99) // out of range → clamps to the last mode (Custom)
	if p.ThemeFitMode() != ThemeFitCustom {
		t.Errorf("SetThemeFit clamp = %d, want Custom(%d)", p.ThemeFitMode(), ThemeFitCustom)
	}
	p.SetThemeFitZoom(1 << 20)
	if p.ThemeZoom() != MaxThemeZoom {
		t.Errorf("zoom clamp = %d, want %d", p.ThemeZoom(), MaxThemeZoom)
	}
	p.SetThemeFitPan(999, -999)
	if x, y := p.ThemePan(); x != MaxThemePan || y != -MaxThemePan {
		t.Errorf("pan clamp = (%d,%d), want (%d,%d)", x, y, MaxThemePan, -MaxThemePan)
	}
}
