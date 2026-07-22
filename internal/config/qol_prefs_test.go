package config

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestFontEverywhereRoundTrip pins the whole-UI font toggle: OFF by default
// (the chrome's fixed metrics are tuned for the embedded face, so extending an
// override to every menu/button is an explicit opt-in), and an explicit ON
// survives save→load.
func TestFontEverywhereRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if p.FontEverywhereOn() {
		t.Fatal("FontEverywhere must default OFF")
	}
	p.SetFontEverywhere(true)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.FontEverywhereOn() {
		t.Error("FontEverywhere=true lost across save→load")
	}
}

// TestRecordingsKeepAssetsRoundTrip pins the H2 "recordings keep their assets"
// pref: ON by default (an in-app recording auto-packages its warm assets on stop)
// and — because it's a default-ON *bool — an explicit OFF must survive save→load
// (the WordDelete/ScreenEffects absent-vs-explicit-false shape). Both directions.
func TestRecordingsKeepAssetsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if !p.RecordingsKeepAssetsOn() {
		t.Fatal("RecordingsKeepAssetsOn must default true (in-app recordings keep their assets)")
	}
	p.SetRecordingsKeepAssets(false) // default-ON *bool — explicit false must survive
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if q.RecordingsKeepAssetsOn() {
		t.Error("RecordingsKeepAssets=false lost (absent-default ON must not clobber explicit false)")
	}
	// And the other direction: an explicit ON on a file that had it OFF survives
	// too. NOTE: load() builds a bare reader with NO saver goroutine — Close on
	// it panics on the nil stop channel (the sibling round-trips never Close a
	// load() result) — so the write leg reopens a REAL saver-backed instance.
	p2, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if p2.RecordingsKeepAssetsOn() {
		t.Fatal("reopened file must still carry the explicit OFF")
	}
	p2.SetRecordingsKeepAssets(true)
	if err := p2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.RecordingsKeepAssetsOn() {
		t.Error("RecordingsKeepAssets=true lost across save→load")
	}
}

// TestChangelogSeenRoundTrip pins the What's New unread-dot pref (#23): empty by
// default, then it persists the version the user last opened.
func TestChangelogSeenRoundTrip(t *testing.T) {
	p := &AssetPreferences{}
	if p.ChangelogSeenVersion() != "" {
		t.Fatalf("ChangelogSeenVersion default = %q, want empty", p.ChangelogSeenVersion())
	}
	p.SetChangelogSeen("v1.30.0")
	if p.ChangelogSeenVersion() != "v1.30.0" {
		t.Fatalf("ChangelogSeenVersion = %q, want v1.30.0", p.ChangelogSeenVersion())
	}
}

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
	if p.ICTimestampsOn() {
		t.Error("ICTimestampsOn default must be false (playtest: timestamps off by default)")
	}
	if p.AutoReconnectOn() {
		t.Error("AutoReconnectOn default must be false (drops stay at the lobby; auto-retry is opt-in)")
	}
	if !p.ScreenEffectsOn() {
		t.Error("ScreenEffectsOn default must be true (AO2 screen effects ship ON)")
	}
	if !p.WordDeleteOn() {
		t.Error("WordDeleteOn default must be true (Ctrl+Backspace word delete ships ON)")
	}
	if !p.MusicHistoryOn() {
		t.Error("MusicHistoryOn default must be true")
	}
	if !p.MusicStreamingOn() {
		t.Error("MusicStreamingOn default must be true (custom /play streaming ships ON)")
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
	if p.LoopPreanimOn() {
		t.Error("LoopPreanim must default OFF (canonical AO2 plays preanims once)")
	}
}

// TestExportOptionsDefaultsAndPersist pins the scene-export (GIF/WebP) settings:
// sensible defaults, clamping on Set, and a save→reload round-trip — the merge
// clause that would silently reset every export to defaults if it were dropped.
func TestExportOptionsDefaultsAndPersist(t *testing.T) {
	p, _ := newTestPrefs(t)
	if d := p.ExportOpts(); d.HeightPx != defaultExportHeight || d.FPS != defaultExportFPS || d.Quality != defaultExportQuality || !d.Loop || d.TextScale != defaultExportText || d.VideoFormat != defaultVideoFormat {
		t.Fatalf("default export opts = %+v, want %d/%d/%d/%d/%s + loop", d, defaultExportHeight, defaultExportFPS, defaultExportQuality, defaultExportText, defaultVideoFormat)
	}
	// An unknown video format normalizes to the MP4 default (never crashes ffmpeg).
	p.SetExportOpts(ExportOptions{HeightPx: 360, FPS: 12, Quality: 80, Loop: true, TextScale: 100, VideoFormat: "avi"})
	if g := p.ExportOpts(); g.VideoFormat != defaultVideoFormat {
		t.Fatalf("bogus video format = %q, want normalized to %q", g.VideoFormat, defaultVideoFormat)
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
	q.SetExportOpts(ExportOptions{HeightPx: 480, FPS: 24, Quality: 60, Loop: true, TextScale: 70, VideoFormat: "webm"})
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if o := r.ExportOpts(); o.HeightPx != 480 || o.FPS != 24 || o.Quality != 60 || !o.Loop || o.TextScale != 70 || o.VideoFormat != "webm" {
		t.Fatalf("reloaded export opts = %+v, want 480/24/60/70/webm + loop (merge clause dropped a field?)", o)
	}
}

// TestSpriteStylePref pins the transmitted sprite-style settings (#103): empty by
// default, opacity clamped, and a save→reload round-trip (including the hide
// toggle) — the merge clause that would silently drop the style otherwise.
func TestSpriteStylePref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if s := p.SpriteStyle(); s.Tint || s.Glow || s.Opacity != 0 || s.Wobble || s.Spin {
		t.Fatalf("default sprite style not empty: %+v", s)
	}
	if p.HideSpriteStylesOn() {
		t.Fatal("HideSpriteStyles should default OFF (show others' styles)")
	}
	// Opacity clamps to [0,100].
	p.SetSpriteStyle(SpriteStylePref{Tint: true, R: 255, G: 60, B: 60, Opacity: 200})
	if g := p.SpriteStyle(); g.Opacity != 100 {
		t.Errorf("opacity 200 clamped to %d, want 100", g.Opacity)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	want := SpriteStylePref{Tint: true, R: 10, G: 20, B: 30, Opacity: 50, Glow: true, Wobble: true, Spin: true, HueCycle: true, FlipH: true, Brightness: 70, Scale: 130, Rotation: 200}
	q.SetSpriteStyle(want)
	q.SetHideSpriteStyles(true)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if g := r.SpriteStyle(); g != want {
		t.Errorf("reloaded sprite style = %+v, want %+v (merge clause dropped a field?)", g, want)
	}
	if !r.HideSpriteStylesOn() {
		t.Error("HideSpriteStyles didn't persist")
	}
}

// TestProfilePref pins the #101 character profile: empty by default, every field
// length-clamped, and a save→reload round-trip (the merge clause that would
// silently drop the profile otherwise).
func TestProfilePref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if pr := p.Profile(); pr.Enabled || pr.ShowOnList || pr.Name != "" || pr.Bio != "" {
		t.Fatalf("default profile not empty: %+v", pr)
	}
	// Oversized fields clamp to their caps.
	long := strings.Repeat("x", 1000)
	p.SetProfile(ProfilePref{Enabled: true, Name: long, Bio: long, ArtURL: long})
	g := p.Profile()
	if len([]rune(g.Name)) != profileNameMax || len([]rune(g.Bio)) != profileBioMax || len([]rune(g.ArtURL)) != profileURLMax {
		t.Errorf("fields not clamped: name=%d bio=%d url=%d", len([]rune(g.Name)), len([]rune(g.Bio)), len([]rune(g.ArtURL)))
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	want := ProfilePref{Enabled: true, ShowOnList: true, Name: "Nick", Pronouns: "they/them", Tag: "ace attorney", Bio: "objection enjoyer", ThemeSong: "https://x/y.opus", ArtURL: "https://x/a.png"}
	q.SetProfile(want)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if g := r.Profile(); g != want {
		t.Errorf("reloaded profile = %+v, want %+v (merge clause dropped a field?)", g, want)
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

// TestServerIgnorePref pins the per-server ignore list (#81): empty by default,
// case-insensitive match, trim + dedup on set, per-server isolation, and a
// save→reload round-trip (so you can un-ignore someone who's left).
func TestServerIgnorePref(t *testing.T) {
	p, _ := newTestPrefs(t)
	const key = "wss://example.test/ws"
	if p.ServerIgnoreMatch(key, "Bob") {
		t.Error("nothing should be ignored by default")
	}
	p.SetServerIgnored(key, []string{"Bob", "  Alice  ", "bob", ""}) // dup (ci) + pad + blank
	if got := p.ServerIgnored(key); len(got) != 2 {
		t.Fatalf("ignored = %v, want 2 (Bob, Alice — deduped/trimmed)", got)
	}
	if !p.ServerIgnoreMatch(key, "BOB") || !p.ServerIgnoreMatch(key, "alice") {
		t.Error("match must be case-insensitive")
	}
	if p.ServerIgnoreMatch(key, "Carol") {
		t.Error("Carol isn't ignored")
	}
	if p.ServerIgnoreMatch("wss://other/ws", "Bob") {
		t.Error("ignore list must be per-server")
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetServerIgnored(key, []string{"Mallory"})
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.ServerIgnoreMatch(key, "Mallory") {
		t.Error("ignore list lost on reload")
	}
}

// TestServerAudioPref pins the per-server audio override (the "sandbox each tab's
// sound" option): off by default, values are per-server, clamp to 0–100, and
// survive a save→reload.
func TestServerAudioPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	const key = "wss://example.test/ws"
	if on, _, _, _, _ := p.ServerAudio(key); on {
		t.Error("per-server audio should be off by default")
	}
	p.SetServerAudioVolumes(key, 80, 70, 0, 50) // e.g. mute SFX on this server only
	p.SetServerAudioOn(key, true)
	if on, m, mu, s, b := p.ServerAudio(key); !on || m != 80 || mu != 70 || s != 0 || b != 50 {
		t.Fatalf("ServerAudio = %v/%d/%d/%d/%d, want true/80/70/0/50", on, m, mu, s, b)
	}
	if on, _, _, _, _ := p.ServerAudio("wss://other/ws"); on {
		t.Error("per-server audio must be per-server (another server stays on global)")
	}
	p.SetServerAudioVolumes(key, 999, -5, 50, 50) // out-of-range clamps to [0,100]
	if _, m, mu, _, _ := p.ServerAudio(key); m != 100 || mu != 0 {
		t.Errorf("clamp failed: master=%d music=%d, want 100/0", m, mu)
	}

	// A profile that's "on" but never set some channels (nil) must fall those back to
	// the GLOBAL default — NOT a silent 0 — so a channel like SFX can't be muted just by
	// the profile existing (the regression that silenced objection / emote sounds). Only
	// an EXPLICIT 0 (set above for sfx) mutes.
	p.SetAudioVolumes(90, 85, 80) // global music=90 sfx=85 blip=80
	mv := 50
	// The setters can't build a PARTIAL ServerWarm entry (only AudioMaster set),
	// so write the map directly — but under p.mu, exactly as SaveNow's marshal
	// takes p.mu.RLock(): the debounced saver goroutine may be flushing the
	// SetAudioVolumes dirty-mark concurrently, and an unlocked map write here
	// races that marshal (a -race flake, pre-existing).
	p.mu.Lock()
	p.ServerWarm["wss://partial/ws"] = ServerWarmInfo{AudioOn: true, AudioMaster: &mv} // music/sfx/blip unset
	p.mu.Unlock()
	if on, m, mu, s, b := p.ServerAudio("wss://partial/ws"); !on || m != 50 || mu != 90 || s != 85 || b != 80 {
		t.Errorf("unset channels must fall back to global: got %v/%d/%d/%d/%d, want true/50/90/85/80", on, m, mu, s, b)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetServerAudioVolumes(key, 60, 40, 20, 10)
	q.SetServerAudioOn(key, true)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if on, m, mu, s, b := r.ServerAudio(key); !on || m != 60 || mu != 40 || s != 20 || b != 10 {
		t.Errorf("after reload ServerAudio = %v/%d/%d/%d/%d, want true/60/40/20/10", on, m, mu, s, b)
	}
}

// TestServerFriendNickColor pins #82: a friend entry parses into name + custom
// glow colour + personal nickname, the colour stays parseable when a nickname is
// present, commas are stripped from nicknames (they'd corrupt the comma-separated
// store), and it all survives a save→reload.
func TestServerFriendNickColor(t *testing.T) {
	p, _ := newTestPrefs(t)
	const key = "wss://example.test/ws"
	// name=RRGGBB=nick (with a comma in the nick, which must be stripped) + a
	// nick-only entry (name==nick, default colour) + a plain colour entry.
	p.SetServerFriends(key, []string{"Phoenix=ff4488=Wright, buddy", "Alice==Allie", "Bob=00ff00"})

	friend, color, nick := p.ServerFriendInfo(key, "phoenix") // case-insensitive
	if !friend || color != 0xff4488 || nick != "Wright buddy" {
		t.Fatalf("Phoenix info = %v/%06x/%q, want true/ff4488/\"Wright buddy\" (comma stripped)", friend, color, nick)
	}
	if _, c, nk := p.ServerFriendInfo(key, "Alice"); c != -1 || nk != "Allie" {
		t.Fatalf("Alice info color/nick = %d/%q, want -1/\"Allie\"", c, nk)
	}
	if _, c, nk := p.ServerFriendInfo(key, "Bob"); c != 0x00ff00 || nk != "" {
		t.Fatalf("Bob info color/nick = %06x/%q, want 00ff00/\"\"", c, nk)
	}
	// ServerFriendMatch (the 2-return wrapper) still works for colour-only callers.
	if f, c := p.ServerFriendMatch(key, "Phoenix"); !f || c != 0xff4488 {
		t.Fatalf("ServerFriendMatch = %v/%06x, want true/ff4488", f, c)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetServerFriends(key, []string{"Mallory=123456=Mal"})
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if f, c, nk := r.ServerFriendInfo(key, "Mallory"); !f || c != 0x123456 || nk != "Mal" {
		t.Errorf("reloaded = %v/%06x/%q, want true/123456/\"Mal\"", f, c, nk)
	}
}

// TestInstantReplayPref pins the rolling clip buffer settings: OFF by default,
// the window defaults to 60s and clamps to [10s, 1 hour], and both survive
// save→reload.
func TestInstantReplayPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.InstantReplayOn() {
		t.Error("InstantReplay must default OFF (opt-in)")
	}
	if got := p.InstantReplaySecondsValue(); got != InstantReplayDefaultSeconds {
		t.Errorf("unset window = %d, want default %d", got, InstantReplayDefaultSeconds)
	}
	p.SetInstantReplaySeconds(2) // below the floor
	if got := p.InstantReplaySecondsValue(); got != InstantReplayMinSeconds {
		t.Errorf("clamped-low window = %d, want %d", got, InstantReplayMinSeconds)
	}
	p.SetInstantReplaySeconds(100000) // past the 1-hour ceiling
	if got := p.InstantReplaySecondsValue(); got != InstantReplayMaxSeconds {
		t.Errorf("clamped-high window = %d, want %d", got, InstantReplayMaxSeconds)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetInstantReplay(true)
	q.SetInstantReplaySeconds(120)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.InstantReplayOn() {
		t.Error("InstantReplay=true lost on reload")
	}
	if got := r.InstantReplaySecondsValue(); got != 120 {
		t.Errorf("reloaded window = %d, want 120", got)
	}
}

// TestTimerPref pins the local alarm/timer settings (#97): repeat OFF by default,
// the remembered duration defaults to 5 min and clamps to [1s, 99 min], and both
// survive save→reload.
func TestTimerPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.TimerRepeatOn() {
		t.Error("TimerRepeat must default OFF")
	}
	if got := p.TimerSecondsValue(); got != TimerDefaultSeconds {
		t.Errorf("unset duration = %d, want default %d", got, TimerDefaultSeconds)
	}
	p.SetTimerSeconds(0) // 0/unset → default, not the floor
	if got := p.TimerSecondsValue(); got != TimerDefaultSeconds {
		t.Errorf("zero duration = %d, want default %d", got, TimerDefaultSeconds)
	}
	p.SetTimerSeconds(1000000) // past the 99-minute ceiling
	if got := p.TimerSecondsValue(); got != TimerMaxSeconds {
		t.Errorf("clamped-high duration = %d, want %d", got, TimerMaxSeconds)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetTimerSeconds(90)
	q.SetTimerRepeat(true)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := r.TimerSecondsValue(); got != 90 {
		t.Errorf("reloaded duration = %d, want 90", got)
	}
	if !r.TimerRepeatOn() {
		t.Error("TimerRepeat=true lost on reload")
	}
}

// TestNotifyOnOOCPref pins the unread-badge OOC toggle: OFF by default (IC only),
// and survives save→reload when enabled.
func TestNotifyOnOOCPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.NotifyOnOOCOn() {
		t.Error("NotifyOnOOC must default OFF (IC-only badge)")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetNotifyOnOOC(true)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.NotifyOnOOCOn() {
		t.Error("NotifyOnOOC=true lost on reload")
	}
}

// TestMusicAcrossTabsPref pins the cross-tab music-continuity toggle: OFF by
// default (a backgrounded tab's music ducks to silence), and survives save→reload
// when enabled (the marshal + load overlay must BOTH carry it — the saves-but-does-
// not-load trap).
func TestMusicAcrossTabsPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.MusicAcrossTabsOn() {
		t.Error("MusicAcrossTabs must default OFF (backgrounded tab music ducks to 0)")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetMusicAcrossTabs(true)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.MusicAcrossTabsOn() {
		t.Error("MusicAcrossTabs=true lost on reload")
	}
}

// TestPlayerListSortPref pins that the Players-tab sort choices (player sort +
// /gas area-group order) default to 0 and survive save→reload.
func TestPlayerListSortPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.PlayerListSortMode() != 0 || p.PlayerListAreaSortMode() != 0 {
		t.Fatalf("sort prefs must default to 0, got %d/%d", p.PlayerListSortMode(), p.PlayerListAreaSortMode())
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetPlayerListSort(1)
	q.SetPlayerListAreaSort(2)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if r.PlayerListSortMode() != 1 || r.PlayerListAreaSortMode() != 2 {
		t.Errorf("reloaded sorts = %d/%d, want 1/2", r.PlayerListSortMode(), r.PlayerListAreaSortMode())
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
	p.SetPreviewHeightPx(512)            // default-when-0 int — must survive
	p.SetUpdateChannelExperimental(true) // default-OFF plain bool — must survive (the test-branch channel)
	p.SetAssetWarnings(true)
	p.SetSpriteMove(true)
	p.SetDeskFollowManifest(true)
	p.SetAutoLoginToast(false)       // explicit false must survive the absent-default-ON pointer
	p.SetCallwordToast(false)        // same absent-default-ON pointer
	p.SetMessageCounter(false)       // same absent-default-ON pointer
	p.SetICTimestamps(true)          // explicit non-default true must survive the absent-default-OFF pointer
	p.SetAutoReconnect(true)         // explicit non-default true must survive the absent-default-OFF pointer
	p.SetScreenEffects(false)        // default-ON *bool — explicit false must survive
	p.SetWordDelete(false)           // default-ON *bool — explicit false must survive
	p.SetMusicHistory(false)         // same absent-default-ON pointer
	p.SetMusicStreaming(false)       // same absent-default-ON pointer — explicit OFF must survive
	p.SetRainbowSprites(true)        // default-OFF plain bool — must survive as true
	p.SetLoopPreanim(true)           // default-OFF plain bool — must survive as true
	p.SetShowRecordButton(true)      // default-OFF plain bool
	p.SetShowFriendButton(false)     // default-ON *bool — explicit false must survive
	p.SetDragLayout(false)           // default-ON *bool — explicit false must survive
	p.SetEventDrivenLoop(false)      // default-ON *bool — the experimental-loop kill switch must stick
	p.SetFrameLimiterDisabled(true)  // #5 bypass — default-OFF plain bool must survive as true
	p.SetMotionRedrawPerEvent(false) // per-event motion redraw — default-ON *bool, so an explicit OFF must survive
	p.SetIdleFPS(FPSUnlimited)       // the ∞ sentinel must survive save→load un-clamped
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
	if q.ScreenEffectsOn() {
		t.Error("ScreenEffects=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.WordDeleteOn() {
		t.Error("WordDelete=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.CallwordToastOn() {
		t.Error("CallwordToast=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.MessageCounterOn() {
		t.Error("MessageCounter=false lost (absent-default ON must not clobber explicit false)")
	}
	if !q.AutoReconnectOn() {
		t.Error("AutoReconnect=true lost (absent-default OFF must not clobber explicit true)")
	}
	if q.MusicHistoryOn() {
		t.Error("MusicHistory=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.MusicStreamingOn() {
		t.Error("MusicStreaming=false lost (absent-default ON must not clobber explicit false)")
	}
	if !q.RainbowSpritesOn() {
		t.Error("RainbowSprites=true lost across reload")
	}
	if !q.LoopPreanimOn() {
		t.Error("LoopPreanim=true lost across reload (default-OFF plain bool must persist an explicit ON)")
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
	if q.EventDrivenLoopOn() {
		t.Error("EventDrivenLoop=false lost (absent-default ON must not clobber the explicit kill switch)")
	}
	if !q.FrameLimiterDisabled() {
		t.Error("DisableFrameLimiter=true lost across reload")
	}
	if q.MotionRedrawPerEventOn() {
		t.Error("MotionRedrawPerEvent=false lost across reload (default-ON *bool must persist an explicit OFF)")
	}
	if got := q.IdleFPS(); got != FPSUnlimited {
		t.Errorf("IdleFPS unlimited sentinel lost across reload: %d, want %d", got, FPSUnlimited)
	}
	if got := q.PreviewHeightPx(); got != 512 {
		t.Errorf("PreviewHeightPx lost: %d, want 512", got)
	}
	if !q.UpdateChannelExperimentalOn() {
		t.Error("UpdateExperimental=true lost across reload (the channel swap must stick)")
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

// TestICPhraseKeyBinds pins the hotkeyed IC phrases: keys store lowercased, an
// empty phrase clears, and a bind survives save→load.
func TestICPhraseKeyBinds(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.ICPhraseBinds() != nil {
		t.Error("no IC-phrase binds by default")
	}
	p.SetICPhraseKey("E", "Happy Pride Month") // key lowercased on store
	p.SetICPhraseKey("q", "Objection!")
	if b := p.ICPhraseBinds(); b["e"] != "Happy Pride Month" || b["q"] != "Objection!" {
		t.Fatalf("binds = %v, want e->phrase q->Objection!", b)
	}
	p.SetICPhraseKey("e", "") // an empty phrase clears the bind
	if p.ICPhraseBinds()["e"] != "" {
		t.Error("empty phrase should clear the e bind")
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetICPhraseKey("F1", "Take that!")
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := r.ICPhraseBinds()["f1"]; got != "Take that!" {
		t.Errorf("IC-phrase bind lost across save/load: f1 = %q, want Take that!", got)
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

// TestSfxFavorites pins the #12 starred-SFX list: case-insensitive store/match, toggle on/off,
// dedup, and survival across save → load.
func TestSfxFavorites(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.IsSfxFavorite("sfx-stab") {
		t.Error("nothing should be starred initially")
	}
	if !p.ToggleSfxFavorite("SFX-Stab") { // stored lowercase; reports now-starred
		t.Error("first toggle should report starred (true)")
	}
	if !p.IsSfxFavorite("sfx-stab") || !p.IsSfxFavorite("SFX-STAB") {
		t.Error("a starred SFX should match case-insensitively")
	}
	if l := p.SfxFavoritesList(); len(l) != 1 || l[0] != "sfx-stab" {
		t.Fatalf("list = %v, want [sfx-stab]", l)
	}
	if p.ToggleSfxFavorite("sfx-stab") { // toggling again unstars; reports false
		t.Error("second toggle should report unstarred (false)")
	}
	if p.IsSfxFavorite("sfx-stab") {
		t.Error("toggle should have unstarred it")
	}
}

// TestSfxFavoritesRoundTrip pins that the starred list survives save → load.
func TestSfxFavoritesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	p.ToggleSfxFavorite("sfx-stab")
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.IsSfxFavorite("sfx-stab") {
		t.Error("starred SFX lost across save/load")
	}
}

// TestModReasonTemplates pins the editable reason chips: defaults when empty, add (case-preserved,
// deduped case-insensitively), remove a seeded default, and the cap.
func TestModReasonTemplates(t *testing.T) {
	p, _ := newTestPrefs(t)
	def := p.ModReasonTemplatesList()
	if len(def) == 0 {
		t.Fatal("an unset list must return the built-in defaults")
	}
	if !p.AddModReasonTemplate("Metagaming") {
		t.Error("AddModReasonTemplate should report a change")
	}
	if p.AddModReasonTemplate("metagaming") { // case-insensitive duplicate
		t.Error("dup add must be a no-op")
	}
	list := p.ModReasonTemplatesList()
	if len(list) != len(def)+1 || list[len(list)-1] != "Metagaming" {
		t.Errorf("after add = %v, want the defaults + Metagaming", list)
	}
	if !p.RemoveModReasonTemplate("spam") { // case-insensitive remove of the "Spam" default
		t.Error("removing a seeded default should work")
	}
	for _, s := range p.ModReasonTemplatesList() {
		if s == "Spam" {
			t.Error("Spam should be gone after removal")
		}
	}
	// Cap: piling on can't grow past modReasonTemplatesCap.
	for i := 0; i < modReasonTemplatesCap+10; i++ {
		p.AddModReasonTemplate("r" + strconv.Itoa(i))
	}
	if len(p.ModReasonTemplatesList()) > modReasonTemplatesCap {
		t.Errorf("list grew to %d, want capped at %d", len(p.ModReasonTemplatesList()), modReasonTemplatesCap)
	}
}

// TestResetPowerUser pins the nuke button's scope: every power-user knob reverts to
// its shipped default, and the deliberately-excluded state (custom mod durations —
// user data, not a knob) survives. If a new power-user pref is added, it belongs in
// ResetPowerUser AND in this list.
func TestResetPowerUser(t *testing.T) {
	p, _ := newTestPrefs(t)
	// Scramble every knob off its default.
	p.SetValidateTLSCerts(true)
	p.SetAssetOriginHeader("https://a.example")
	p.SetWSOriginHeader("https://b.example")
	p.SetSpriteLoadMode(SpriteLoadWait)
	p.SetSpriteWaitMs(4000)
	p.SetSpriteWaitPair(true)
	p.SetSpriteWaitPreanim(true)
	p.SetHoldPrevMaxAgeMs(9000)
	p.SetHoldDebugTint(true)
	p.SetShoutDurationMs(1000)
	p.SetPreanimTimeoutMs(5000)
	p.SetICQueueCap(128)
	p.SetCatchUpLingerMs(250)
	p.SetThumbCache(true)
	p.SetThumbHeightPx(120)
	p.SetThumbQuality(40)
	p.SetThumbBudgetMiB(256)
	p.SetNotFoundTTLSec(60)
	p.SetAdaptiveLatMultiple(4)
	p.SetSpriteDownscaleOff(true)
	p.SetSpriteDownscalePct(150)
	p.SetTexBudgetMiB(96)
	p.SetCrossfadeMs(300)
	p.SetFPSCap(144)
	p.SetIdleFPS(60)
	p.SetUnfocusedFPS(30)
	p.SetEventDrivenLoop(false)
	p.SetFrameLimiterDisabled(true)  // #5 bypass — must revert to OFF on nuke
	p.SetMotionRedrawPerEvent(false) // per-event motion redraw — must revert to its default (ON) on nuke
	p.SetClipSpritesToStage(false)
	p.AddModDuration("45m") // user data — must SURVIVE the nuke

	p.ResetPowerUser()

	if p.ValidateTLSCertsOn() || p.AssetOriginHeader() != "" || p.WSOriginHeader() != "" {
		t.Error("nuke must clear TLS + both Origin overrides")
	}
	if p.SpriteLoadMode() != SpriteLoadHoldPrev || p.SpriteWaitMs() != SpriteWaitDefaultMs ||
		p.SpriteWaitPairOn() || p.SpriteWaitPreanimOn() {
		t.Error("nuke must reset the cold-load mode to its default (hold-previous) + the wait knobs")
	}
	if p.HoldPrevMaxAgeMs() != 0 || p.HoldDebugTintOn() {
		t.Error("nuke must reset the hold-previous knobs")
	}
	if p.ShoutDurationMs() != 0 || p.PreanimTimeoutMs() != 0 || p.ICQueueCap() != 0 || p.CatchUpLingerMs() != 0 {
		t.Error("nuke must reset the core timings + queue knobs to their defaults")
	}
	if !p.ClipSpritesToStageOn() {
		t.Error("nuke must restore the sprite mask to its default ON")
	}
	if p.ThumbCacheOn() || p.ThumbHeightPx() != ThumbHeightDefaultPx || p.ThumbQuality() != ThumbQualityDefault || p.ThumbBudgetMiB() != ThumbBudgetDefaultMiB {
		t.Error("nuke must reset the thumbnail-cache knobs (stored thumbs on disk are untouched — Clear is separate)")
	}
	if p.NotFoundTTLSec() != 0 || p.AdaptiveLatMultiple() != 0 {
		t.Error("nuke must reset the network knobs to their 0 = default sentinels")
	}
	if p.SpriteDownscaleOffOn() || p.SpriteDownscalePct() != 0 || p.TexBudgetMiB() != TexBudgetDefaultMiB {
		t.Error("nuke must reset the downscale + texture-budget knobs")
	}
	if p.CrossfadeMs() != 0 {
		t.Error("nuke must reset the crossfade to off")
	}
	if p.FPSCap() != FPSCapDefault || p.IdleFPS() != IdleFPSDefault || p.UnfocusedFPS() != UnfocusedFPSDefault {
		t.Error("nuke must reset the frame-pacing rates to their defaults")
	}
	if !p.EventDrivenLoopOn() {
		t.Error("nuke must restore the experimental event-driven loop to its default ON")
	}
	if p.FrameLimiterDisabled() {
		t.Error("nuke must restore the frame-limiter bypass to its default OFF")
	}
	if !p.MotionRedrawPerEventOn() {
		t.Error("nuke must restore per-event motion redraw to its default ON")
	}
	if got := p.ModDurationsList(); len(got) != 1 || got[0] != "45m" {
		t.Errorf("custom mod durations are user data and must survive the nuke, got %v", got)
	}
}

// TestModDurations pins the saved custom ban-duration chips: empty by default (the
// enum presets are the built-ins), add (deduped case-insensitively), remove, the cap,
// and persistence across a save → load.
func TestModDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if got := p.ModDurationsList(); len(got) != 0 {
		t.Fatalf("a fresh config must have no custom durations, got %v", got)
	}
	if !p.AddModDuration("45m") {
		t.Error("AddModDuration should report a change")
	}
	if p.AddModDuration("45M") { // case-insensitive duplicate
		t.Error("dup add must be a no-op")
	}
	if p.AddModDuration("  ") {
		t.Error("blank add must be a no-op")
	}
	p.AddModDuration("2d")
	if got := p.ModDurationsList(); len(got) != 2 || got[0] != "45m" || got[1] != "2d" {
		t.Errorf("after adds = %v, want [45m 2d]", got)
	}
	if !p.RemoveModDuration("45m") {
		t.Error("removing a saved duration should work")
	}
	if p.RemoveModDuration("45m") {
		t.Error("removing a missing duration must be a no-op")
	}
	for i := 0; i < modDurationsCap+10; i++ {
		p.AddModDuration(strconv.Itoa(i+1) + "h")
	}
	if n := len(p.ModDurationsList()); n > modDurationsCap {
		t.Errorf("list grew to %d, want capped at %d", n, modDurationsCap)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := q.ModDurationsList(); len(got) == 0 || got[0] != "2d" {
		t.Errorf("custom durations lost across save/load: %v", got)
	}
}

// TestModReasonTemplatesRoundTrip pins that a custom reason survives save → load.
func TestModReasonTemplatesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	p.AddModReasonTemplate("Metagaming")
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	found := false
	for _, s := range q.ModReasonTemplatesList() {
		if s == "Metagaming" {
			found = true
		}
	}
	if !found {
		t.Error("custom reason template lost across save/load")
	}
}

// TestShowPairStatusRoundTrip pins the #20 opt-in pref: default OFF, and it survives save → load
// (guarding the copy-on-load line that bool prefs need).
func TestShowPairStatusRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if p.ShowPairStatusOn() {
		t.Error("ShowPairStatus must default OFF")
	}
	p.SetShowPairStatus(true)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.ShowPairStatusOn() {
		t.Error("ShowPairStatus lost across save/load (missing copy-on-load line?)")
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

// TestFPSKnobDomain pins the widened frame-rate domain: values below the old
// floors (30/10/5) are now honored (the "can't go below 10" report), 0 maps to
// the FPSOff sentinel for Idle/Background and survives save→load un-clamped (0 =
// never redraw must persist, not silently reset to the default), ∞ still round-
// trips, out-of-range positives still clamp, and an ABSENT key still resolves to
// the shipped default rather than off.
func TestFPSKnobDomain(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	// A fresh (absent-key) prefs resolves to the defaults, never 0/off.
	if p.FPSCap() != FPSCapDefault || p.IdleFPS() != IdleFPSDefault || p.UnfocusedFPS() != UnfocusedFPSDefault {
		t.Fatalf("fresh defaults = %d/%d/%d, want %d/%d/%d",
			p.FPSCap(), p.IdleFPS(), p.UnfocusedFPS(), FPSCapDefault, IdleFPSDefault, UnfocusedFPSDefault)
	}
	// Below the old floors is now allowed, not clamped up.
	p.SetFPSCap(5)
	p.SetIdleFPS(2)
	p.SetUnfocusedFPS(1)
	if p.FPSCap() != 5 || p.IdleFPS() != 2 || p.UnfocusedFPS() != 1 {
		t.Errorf("low rates clamped: cap=%d idle=%d unfocused=%d, want 5/2/1",
			p.FPSCap(), p.IdleFPS(), p.UnfocusedFPS())
	}
	// Out-of-range positives still clamp to the max.
	p.SetIdleFPS(99999)
	if got := p.IdleFPS(); got != IdleFPSMax {
		t.Errorf("idle over-max = %d, want clamp to %d", got, IdleFPSMax)
	}
	// The Settings "0"/off maps to FPSOff; ∞ to FPSUnlimited. Both are held live
	// and must survive the disk round-trip un-normalized.
	p.SetIdleFPS(FPSOff)
	p.SetUnfocusedFPS(FPSOff)
	p.SetFPSCap(FPSUnlimited)
	if p.IdleFPS() != FPSOff || p.UnfocusedFPS() != FPSOff || p.FPSCap() != FPSUnlimited {
		t.Fatalf("sentinels not held live: idle=%d unfocused=%d cap=%d", p.IdleFPS(), p.UnfocusedFPS(), p.FPSCap())
	}
	// Input-grace frames: default 1, clamps into range, survives the round-trip.
	if got := p.InputGraceFrames(); got != InputGraceFramesDefault {
		t.Errorf("input-grace default = %d, want %d", got, InputGraceFramesDefault)
	}
	p.SetInputGraceFrames(99999)
	if got := p.InputGraceFrames(); got != InputGraceFramesMax {
		t.Errorf("input-grace over-max = %d, want clamp to %d", got, InputGraceFramesMax)
	}
	p.SetInputGraceFrames(0) // 0 = OFF — must read back as 0 and survive as 0 (NOT snap to the default)
	if got := p.InputGraceFrames(); got != 0 {
		t.Errorf("input-grace off = %d, want 0", got)
	}
	if err := p.Close(); err != nil { // flush + release before reload
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path) // load() alone starts no saver goroutine, so there's nothing to Close
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if q.IdleFPS() != FPSOff {
		t.Errorf("idle off (FPSOff) lost across reload: %d, want %d (0 = off must persist)", q.IdleFPS(), FPSOff)
	}
	if q.UnfocusedFPS() != FPSOff {
		t.Errorf("unfocused off (FPSOff) lost across reload: %d, want %d", q.UnfocusedFPS(), FPSOff)
	}
	if q.FPSCap() != FPSUnlimited {
		t.Errorf("active ∞ lost across reload: %d, want %d", q.FPSCap(), FPSUnlimited)
	}
	if q.InputGraceFrames() != 0 {
		t.Errorf("input-grace OFF lost across reload: %d, want 0 (off must persist, not snap to the default)", q.InputGraceFrames())
	}
}
