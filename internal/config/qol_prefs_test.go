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
	if !p.CallwordToastOn() {
		t.Error("CallwordToastOn default must be true")
	}
	if !p.MessageCounterOn() {
		t.Error("MessageCounterOn default must be true")
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
	p.SetCallwordToast(false)  // same absent-default-ON pointer
	p.SetMessageCounter(false) // same absent-default-ON pointer
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
	if q.CallwordToastOn() {
		t.Error("CallwordToast=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.MessageCounterOn() {
		t.Error("MessageCounter=false lost (absent-default ON must not clobber explicit false)")
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

// TestClampWindowSize pins the shared window-size clamp (the stuck-too-big fix):
// an oversize request on a smaller display fits to the usable bounds, a tiny
// request floors at the minimum, and an unknown display (0) skips the ceiling.
func TestClampWindowSize(t *testing.T) {
	cases := []struct {
		reqW, reqH, uW, uH, wantW, wantH int
	}{
		{4000, 3000, 1920, 1080, 1920, 1080},           // oversize → fit the screen
		{1280, 720, 1920, 1080, 1280, 720},             // fits → unchanged
		{200, 100, 1920, 1080, MinWindowW, MinWindowH}, // tiny → floor
		{4000, 3000, 0, 0, 4000, 3000},                 // unknown display → no ceiling
		{1280, 720, 1024, 768, 1024, 720},              // width over, height fits
	}
	for _, c := range cases {
		if w, h := ClampWindowSize(c.reqW, c.reqH, c.uW, c.uH); w != c.wantW || h != c.wantH {
			t.Errorf("ClampWindowSize(%d,%d,%d,%d) = %d,%d, want %d,%d",
				c.reqW, c.reqH, c.uW, c.uH, w, h, c.wantW, c.wantH)
		}
	}
}

// TestHoldClearDefaults pins the hold-to-clear prefs: ON by default, the
// Backspace key, a 1.5s threshold, and that all three round-trip (the threshold
// clamping included).
func TestHoldClearDefaults(t *testing.T) {
	p, _ := newTestPrefs(t)
	on, key, ms := p.HoldClear()
	if !on {
		t.Error("hold-to-clear must default ON")
	}
	if key != "Backspace" {
		t.Errorf("default key = %q, want Backspace", key)
	}
	if ms != DefaultHoldClearMs {
		t.Errorf("default ms = %d, want %d", ms, DefaultHoldClearMs)
	}
	p.SetHoldClearMs(1 << 20) // over-large clamps to max
	if _, _, got := p.HoldClear(); got != MaxHoldClearMs {
		t.Errorf("ms clamp = %d, want %d", got, MaxHoldClearMs)
	}
	p.SetHoldClearOn(false)
	p.SetHoldClearKey("Delete")
	if gotOn, gotKey, _ := p.HoldClear(); gotOn || gotKey != "Delete" {
		t.Errorf("after edits: on=%v key=%q, want false, Delete", gotOn, gotKey)
	}
}

// TestExtrasBoxStyle pins the Extras-box theme prefs: blank/false by default
// (stock look), and a full round-trip of the five colours + gradient flag.
func TestExtrasBoxStyle(t *testing.T) {
	p, _ := newTestPrefs(t)
	if bg, _, _, _, _, grad := p.ExtrasBoxStyle(); bg != "" || grad {
		t.Errorf("default ExtrasBoxStyle = bg %q grad %v, want blank/false", bg, grad)
	}
	p.SetExtrasBoxStyle("101018", "000000", "78aaff", "202030", "ffffff", true)
	bg, bg2, border, title, text, grad := p.ExtrasBoxStyle()
	if bg != "101018" || bg2 != "000000" || border != "78aaff" || title != "202030" || text != "ffffff" || !grad {
		t.Errorf("round-trip = %q %q %q %q %q %v", bg, bg2, border, title, text, grad)
	}
}

// TestShownamePresets pins the global showname-preset list (M6): add (deduped
// case-insensitively), remove, and that it round-trips through save/load.
func TestShownamePresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if !p.AddShownamePreset("Phoenix") || !p.AddShownamePreset("Edgeworth") {
		t.Fatal("AddShownamePreset should report a change")
	}
	if p.AddShownamePreset("phoenix") { // case-insensitive duplicate
		t.Error("duplicate AddShownamePreset must not change")
	}
	if got := p.ShownameList(); len(got) != 2 {
		t.Fatalf("ShownameList = %v, want 2", got)
	}
	if !p.RemoveShownamePreset("Phoenix") {
		t.Error("RemoveShownamePreset should report a change")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := q.ShownameList(); len(got) != 1 || got[0] != "Edgeworth" {
		t.Errorf("after reload: ShownameList = %v, want [Edgeworth]", got)
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
