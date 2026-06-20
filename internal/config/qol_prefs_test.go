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
	if !p.ICTimestampsOn() {
		t.Error("ICTimestampsOn default must be true")
	}
	if !p.AutoReconnectOn() {
		t.Error("AutoReconnectOn default must be true")
	}
	if !p.MusicHistoryOn() {
		t.Error("MusicHistoryOn default must be true")
	}
	if p.RainbowSpeed() != defaultRainbowSpeed || p.RainbowVividness() != defaultRainbowVivid {
		t.Errorf("rainbow speed/vividness defaults = %d/%d, want %d/%d",
			p.RainbowSpeed(), p.RainbowVividness(), defaultRainbowSpeed, defaultRainbowVivid)
	}
	if p.SpriteTintColorRGB() != defaultSpriteTintColor {
		t.Errorf("SpriteTintColor default = %06x, want %06x", p.SpriteTintColorRGB(), defaultSpriteTintColor)
	}
	if p.RainbowSpriteGlowOn() || p.RainbowPairDesyncOn() || p.SpriteSolidTintOn() {
		t.Error("sprite-FX glow/desync/solid must default OFF")
	}
}

// TestExportOptionsDefaultsAndPersist pins the scene-export (GIF/WebP) settings:
// sensible defaults, clamping on Set, and a save→reload round-trip — the merge
// clause that would silently reset every export to defaults if it were dropped.
func TestExportOptionsDefaultsAndPersist(t *testing.T) {
	p, _ := newTestPrefs(t)
	if d := p.ExportOpts(); d.HeightPx != defaultExportHeight || d.FPS != defaultExportFPS || d.Quality != defaultExportQuality || !d.Loop || d.TextScale != defaultExportText {
		t.Fatalf("default export opts = %+v, want %d/%d/%d/%d + loop", d, defaultExportHeight, defaultExportFPS, defaultExportQuality, defaultExportText)
	}
	// Out-of-range values clamp to the configured bounds.
	p.SetExportOpts(ExportOptions{HeightPx: 99999, FPS: 0, Quality: 999, Loop: false, TextScale: 9999})
	if g := p.ExportOpts(); g.HeightPx != maxExportHeight || g.FPS != minExportFPS || g.Quality != maxExportQuality || g.Loop || g.TextScale != maxExportText {
		t.Fatalf("clamped export opts = %+v, want max/min/max/maxText + loop off", g)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetExportOpts(ExportOptions{HeightPx: 480, FPS: 24, Quality: 60, Loop: true, TextScale: 70})
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if o := r.ExportOpts(); o.HeightPx != 480 || o.FPS != 24 || o.Quality != 60 || !o.Loop || o.TextScale != 70 {
		t.Fatalf("reloaded export opts = %+v, want 480/24/60/70 + loop (merge clause dropped a field?)", o)
	}
}

// TestInstantDisconnectPref pins the Disconnect-confirm toggle: OFF by default
// (confirm first), and the on state survives save→reload.
func TestInstantDisconnectPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.InstantDisconnectOn() {
		t.Error("InstantDisconnect must default OFF (Disconnect confirms first)")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetInstantDisconnect(true)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.InstantDisconnectOn() {
		t.Error("InstantDisconnect=true lost on reload")
	}
}

// TestRightClickHideSpritePref pins the hide-sprite toggle: ON by default
// (right-click offers to hide), survives a save→reload OFF (the *bool DTO so an
// absent field keeps the default-ON, not a silent off).
func TestRightClickHideSpritePref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if !p.RightClickHideSpriteOn() {
		t.Error("RightClickHideSprite must default ON")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetRightClickHideSprite(false)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if r.RightClickHideSpriteOn() {
		t.Error("RightClickHideSprite=false lost on reload (absent != default-on)")
	}
}

// TestAutoConnectOnLaunchPref pins the auto-connect-on-launch opt-in: OFF by
// default, and the toggle plus the saved last server (name + URL) survive a
// save→reload so the next launch knows where to dial.
func TestAutoConnectOnLaunchPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.AutoConnectOnLaunchOn() {
		t.Error("AutoConnectOnLaunch must default OFF (auto-connect is opt-in)")
	}
	if name, url := p.LastServer(); name != "" || url != "" {
		t.Errorf("LastServer default = %q/%q, want empty", name, url)
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetAutoConnectOnLaunch(true)
	q.SetLastServer("Skrapegropen", "wss://example.test/ws")
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.AutoConnectOnLaunchOn() {
		t.Error("AutoConnectOnLaunch=true lost on reload")
	}
	if name, url := r.LastServer(); name != "Skrapegropen" || url != "wss://example.test/ws" {
		t.Errorf("reloaded LastServer = %q/%q, want Skrapegropen/wss://example.test/ws", name, url)
	}
}

// TestEmoteFavsPref pins per-character emote favourites: index-keyed (not
// name-keyed, since emote labels/anims duplicate within a character), toggling
// is its own inverse, removing the last one drops the entry, and both the
// favourites and the favs-only display toggle survive a save→reload.
func TestEmoteFavsPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.EmoteFavOnlyOn() {
		t.Error("EmoteFavOnly must default OFF")
	}
	if got := p.EmoteFavsFor("Apollo"); got != nil {
		t.Errorf("fresh EmoteFavsFor = %v, want nil", got)
	}
	// Toggle two emotes on; toggling one again removes it (its own inverse).
	if now := p.ToggleEmoteFav("Apollo", 0); !now {
		t.Error("ToggleEmoteFav(0) should report now-favourited")
	}
	p.ToggleEmoteFav("Apollo", 10)
	if !p.IsEmoteFav("Apollo", 0) || !p.IsEmoteFav("Apollo", 10) {
		t.Fatal("both 0 and 10 should be favourited")
	}
	if now := p.ToggleEmoteFav("apollo", 0); now { // case-insensitive char key
		t.Error("re-toggling 0 should report now-unfavourited")
	}
	if p.IsEmoteFav("Apollo", 0) {
		t.Error("0 should be removed after the second toggle")
	}
	// Removing the last favourite drops the map entry entirely.
	p.ToggleEmoteFav("Apollo", 10)
	if got := p.EmoteFavsFor("Apollo"); got != nil {
		t.Errorf("after clearing, EmoteFavsFor = %v, want nil", got)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.ToggleEmoteFav("Phoenix", 3)
	q.ToggleEmoteFav("Phoenix", 7)
	q.SetEmoteFavOnly(true)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.EmoteFavOnlyOn() {
		t.Error("EmoteFavOnly=true lost on reload")
	}
	if !r.IsEmoteFav("Phoenix", 3) || !r.IsEmoteFav("Phoenix", 7) {
		t.Errorf("reloaded favourites = %v, want [3 7]", r.EmoteFavsFor("Phoenix"))
	}
}

// TestChatboxOpacityPref pins the see-through chatbox setting: default 84,
// clamps, and survives save→reload (the *int DTO so a fresh config isn't 0%).
func TestChatboxOpacityPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.ChatboxOpacityPct() != DefaultChatboxOpacity {
		t.Fatalf("ChatboxOpacity default = %d, want %d", p.ChatboxOpacityPct(), DefaultChatboxOpacity)
	}
	p.SetChatboxOpacity(999)
	if p.ChatboxOpacityPct() != MaxChatboxOpacity {
		t.Errorf("over-max = %d, want %d", p.ChatboxOpacityPct(), MaxChatboxOpacity)
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetChatboxOpacity(30)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if r.ChatboxOpacityPct() != 30 {
		t.Errorf("reloaded opacity = %d, want 30", r.ChatboxOpacityPct())
	}
}

// TestModSFXPrefs pins the #60 mod-command sounds: every action defaults OFF
// with no custom file, and toggles + paths survive save→load.
func TestModSFXPrefs(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.ModBanSFXOn() || p.ModKickSFXOn() || p.ModMuteSFXOn() {
		t.Error("mod-SFX toggles must default OFF")
	}
	if p.ModBanSoundPath() != "" || p.ModKickSoundPath() != "" || p.ModMuteSoundPath() != "" {
		t.Error("mod-SFX sound paths must default empty")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetModBanSFX(true)
	q.SetModKickSFX(true)
	q.SetModMuteSFX(true)
	q.SetModBanSoundPath("ban.opus")
	q.SetModKickSoundPath("kick.wav")
	q.SetModMuteSoundPath("mute.ogg")
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.ModBanSFXOn() || !r.ModKickSFXOn() || !r.ModMuteSFXOn() {
		t.Error("mod-SFX toggles lost across reload")
	}
	if r.ModBanSoundPath() != "ban.opus" || r.ModKickSoundPath() != "kick.wav" || r.ModMuteSoundPath() != "mute.ogg" {
		t.Errorf("mod-SFX paths lost: %q %q %q", r.ModBanSoundPath(), r.ModKickSoundPath(), r.ModMuteSoundPath())
	}
}

// TestFollowEnabledPref pins the opt-in player-follow toggle: OFF by default,
// and an explicit ON survives save→load.
func TestFollowEnabledPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.FollowEnabledOn() {
		t.Error("FollowEnabled must default OFF (opt-in)")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetFollowEnabled(true)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.FollowEnabledOn() {
		t.Error("FollowEnabled=true lost across save/load")
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
	p.SetAutoLoginToast(false)   // explicit false must survive the absent-default-ON pointer
	p.SetCallwordToast(false)    // same absent-default-ON pointer
	p.SetMessageCounter(false)   // same absent-default-ON pointer
	p.SetICTimestamps(false)     // same absent-default-ON pointer
	p.SetAutoReconnect(false)    // same absent-default-ON pointer
	p.SetMusicHistory(false)     // same absent-default-ON pointer
	p.SetRainbowSprites(true)    // default-OFF plain bool — must survive as true
	p.SetShowRecordButton(true)  // default-OFF plain bool
	p.SetShowFriendButton(false) // default-ON *bool — explicit false must survive
	p.SetDragLayout(false)       // default-ON *bool — explicit false must survive
	p.SetRainbowSpriteSpeed(30)
	p.SetRainbowSpriteVividness(95)
	p.SetRainbowSpriteGlow(true)
	p.SetRainbowPairDesync(true)
	p.SetRainbowPerChar(true)
	p.SetSpriteWobble(true)
	p.SetSpriteSpin(true)
	p.SetSpriteSolidTint(true)
	p.SetSpriteTintColor(0x112233)
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
	if q.AutoReconnectOn() {
		t.Error("AutoReconnect=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.MusicHistoryOn() {
		t.Error("MusicHistory=false lost (absent-default ON must not clobber explicit false)")
	}
	if !q.RainbowSpritesOn() {
		t.Error("RainbowSprites=true lost across reload")
	}
	if !q.ShowRecordButtonOn() {
		t.Error("ShowRecordButton=true lost across reload")
	}
	if q.DragLayoutOn() {
		t.Error("DragLayout=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.FriendButtonShown() {
		t.Error("ShowFriendButton=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.RainbowSpeed() != 30 || q.RainbowVividness() != 95 {
		t.Errorf("rainbow speed/vividness lost: %d/%d, want 30/95", q.RainbowSpeed(), q.RainbowVividness())
	}
	if !q.RainbowSpriteGlowOn() || !q.RainbowPairDesyncOn() || !q.SpriteSolidTintOn() {
		t.Error("a sprite-FX toggle (glow/desync/solid) lost across reload")
	}
	if !q.RainbowPerCharOn() || !q.SpriteWobbleOn() || !q.SpriteSpinOn() {
		t.Error("a wacky-FX toggle (per-char/wobble/spin) lost across reload")
	}
	if q.SpriteTintColorRGB() != 0x112233 {
		t.Errorf("SpriteTintColor lost: %06x, want 112233", q.SpriteTintColorRGB())
	}
}

// TestShownameKeyBinds pins the M6 per-preset showname keybinds: keys store
// case-insensitively, an empty showname clears, and removing a preset drops its
// bind (no orphans).
func TestShownameKeyBinds(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.AddShownamePreset("Nick")
	p.AddShownamePreset("Edgey")
	p.SetShownameKeyBind("F1", "Nick") // key lowercased on store
	p.SetShownameKeyBind("f2", "Edgey")

	if b := p.ShownameKeyBinds(); b["f1"] != "Nick" || b["f2"] != "Edgey" {
		t.Fatalf("binds = %v, want f1->Nick f2->Edgey", b)
	}
	p.SetShownameKeyBind("f1", "") // empty showname clears the bind
	if p.ShownameKeyBinds()["f1"] != "" {
		t.Error("f1 bind should be cleared")
	}
	p.RemoveShownamePreset("Edgey") // removing a preset drops its bind
	if p.ShownameKeyBinds()["f2"] != "" {
		t.Error("removing Edgey must drop its f2 keybind")
	}
}

// TestShownameKeyBindRoundTrip pins that a keybind survives save → load.
func TestShownameKeyBindRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	p.AddShownamePreset("Nick")
	p.SetShownameKeyBind("F1", "Nick")
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := q.ShownameKeyBinds()["f1"]; got != "Nick" {
		t.Errorf("keybind lost across save/load: f1 = %q, want Nick", got)
	}
}

// TestMutedSFX pins the M11 per-SFX mute list: case-insensitive store/match,
// dedup, unmute, and survival across save → load.
func TestMutedSFX(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.IsSFXMuted("sfx-stab") {
		t.Error("nothing should be muted initially")
	}
	if !p.MuteSFX("SFX-Stab") { // stored lowercase
		t.Error("MuteSFX should report a change")
	}
	if !p.IsSFXMuted("sfx-stab") || !p.IsSFXMuted("SFX-STAB") {
		t.Error("a muted SFX should match case-insensitively")
	}
	if p.MuteSFX("sfx-stab") {
		t.Error("re-muting the same SFX must be a no-op")
	}
	if l := p.MutedSFXList(); len(l) != 1 || l[0] != "sfx-stab" {
		t.Fatalf("list = %v, want [sfx-stab]", l)
	}
	if !p.UnmuteSFX("sfx-stab") || p.IsSFXMuted("sfx-stab") {
		t.Error("UnmuteSFX should clear it")
	}
}

// TestMutedSFXRoundTrip pins that the mute list survives save → load.
func TestMutedSFXRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	p.MuteSFX("sfx-stab")
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.IsSFXMuted("sfx-stab") {
		t.Error("muted SFX lost across save/load")
	}
}

// TestBlipVolume pins the M11 per-character blip scale: default 100, clamp,
// case-insensitive key, no-op detection, and that resetting to 100 clears the
// entry (so the map only holds real adjustments).
func TestBlipVolume(t *testing.T) {
	p, _ := newTestPrefs(t)
	if v := p.BlipVolumeFor("phoenix"); v != 100 {
		t.Errorf("unset char = %d, want default 100", v)
	}
	if !p.SetBlipVolume("Phoenix", 40) { // stored lowercase
		t.Error("SetBlipVolume should report a change")
	}
	if v := p.BlipVolumeFor("phoenix"); v != 40 {
		t.Errorf("after set = %d, want 40", v)
	}
	if p.SetBlipVolume("phoenix", 40) {
		t.Error("re-setting the same value must be a no-op")
	}
	if !p.SetBlipVolume("phoenix", 999) { // clamps to 100 → also resets the entry
		t.Error("clamped change should report a change")
	}
	if v := p.BlipVolumeFor("phoenix"); v != 100 {
		t.Errorf("clamped-high = %d, want 100", v)
	}
	if vols := p.BlipVolumes(); len(vols) != 0 {
		t.Errorf("resetting to 100 should clear the entry, got %v", vols)
	}
	if !p.SetBlipVolume("edgeworth", -5) { // clamps to 0
		t.Error("clamp-low should report a change")
	}
	if v := p.BlipVolumeFor("edgeworth"); v != 0 {
		t.Errorf("clamped-low = %d, want 0", v)
	}
	if p.SetBlipVolume("", 50) {
		t.Error("empty char name must be rejected")
	}
}

// TestBlipVolumeRoundTrip pins that per-character blip scales survive save→load.
func TestBlipVolumeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	p.SetBlipVolume("phoenix", 25)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if v := q.BlipVolumeFor("phoenix"); v != 25 {
		t.Errorf("per-char blip scale lost across save/load: %d", v)
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
