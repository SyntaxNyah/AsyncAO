package config

import (
	"os"
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
	if p.Load().FontEverywhere {
		t.Fatal("FontEverywhere must default OFF")
	}
	p.Update(func(
		sn *Settings) {

		sn.
			FontEverywhere =

			true

	})
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.Load().FontEverywhere {
		t.Error("FontEverywhere=true lost across save→load")
	}
}

// TestRecordingsKeepAssetsRoundTrip pins the H2 "recordings keep their assets"
// pref: ON by default (an in-app recording auto-packages its warm assets on stop)
func TestRecordingsKeepAssetsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if !p.Load().RecordingsKeepAssets {
		t.Fatal("RecordingsKeepAssetsOn must default true (in-app recordings keep their assets)")
	}
	p.Update(func(sn *Settings) { sn.RecordingsKeepAssets = false }) // default-ON *bool — explicit false must survive
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if q.Load().RecordingsKeepAssets {
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
	if p2.Load().RecordingsKeepAssets {
		t.Fatal("reopened file must still carry the explicit OFF")
	}
	p2.Update(func(sn *Settings) { sn.RecordingsKeepAssets = true })
	if err := p2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.Load().RecordingsKeepAssets {
		t.Error("RecordingsKeepAssets=true lost across save→load")
	}
}

// TestChangelogSeenRoundTrip pins the What's New unread-dot pref (#23): empty by
// default, then it persists the version the user last opened.
func TestChangelogSeenRoundTrip(t *testing.T) {
	p := &AssetPreferences{}
	if p.Load().ChangelogSeen != "" {
		t.Fatalf("ChangelogSeenVersion default = %q, want empty", p.Load().ChangelogSeen)
	}
	p.Update(func(sn *Settings) { sn.ChangelogSeen = "v1.30.0" })
	if p.Load().ChangelogSeen != "v1.30.0" {
		t.Fatalf("ChangelogSeenVersion = %q, want v1.30.0", p.Load().ChangelogSeen)
	}
}

// TestQoLPrefDefaults pins the out-of-the-box values for the QoL-roadmap prefs:
// previews ON at a 5 s dwell, the two opt-ins (banner, sprite-move) OFF, and
// desk-follows-manifest ON (auto-detect; the WebP pin is the opt-out).
func TestQoLPrefDefaults(t *testing.T) {
	p, _ := newTestPrefs(t)
	if !p.Load().SpritePreviewOn {
		t.Error("SpritePreviewsOn default must be true")
	}
	if got := p.Load().PreviewHoverMillis(); got != DefaultPreviewHoverMs {
		t.Errorf("PreviewHoverMillis default = %d, want %d", got, DefaultPreviewHoverMs)
	}
	if p.Load().ShowAssetWarnings {
		t.Error("AssetWarningsOn default must be false (banner is opt-in)")
	}
	if p.Load().SpriteMoveOn {
		t.Error("SpriteMoveEnabled default must be false")
	}
	// Deliberate default flip (user order, 2026-08-08): desks auto-detect from
	// the server manifest like every other class; pinning WebP is the opt-out.
	if !p.Load().DeskFollowManifest {
		t.Error("DeskFollowsManifest default must be true (desks auto-detect like every other class)")
	}
	if !p.Load().AutoLoginToast {
		t.Error("AutoLoginToastOn default must be true")
	}
	if !p.Load().CallwordToast {
		t.Error("CallwordToastOn default must be true")
	}
	if !p.Load().MessageCounter {
		t.Error("MessageCounterOn default must be true")
	}
	if p.Load().ICTimestamps {
		t.Error("ICTimestampsOn default must be false (playtest: timestamps off by default)")
	}
	if p.Load().AutoReconnect {
		t.Error("AutoReconnectOn default must be false (drops stay at the lobby; auto-retry is opt-in)")
	}
	if !p.Load().ScreenEffects {
		t.Error("ScreenEffectsOn default must be true (AO2 screen effects ship ON)")
	}
	if !p.Load().WordDelete {
		t.Error("WordDeleteOn default must be true (Ctrl+Backspace word delete ships ON)")
	}
	if !p.Load().MusicHistory {
		t.Error("MusicHistoryOn default must be true")
	}
	if !p.Load().MusicStreaming {
		t.Error("MusicStreamingOn default must be true (custom /play streaming ships ON)")
	}
	if p.Load().RainbowSpriteSpeed != defaultRainbowSpeed || p.Load().RainbowSpriteVividness != defaultRainbowVivid {
		t.Errorf("rainbow speed/vividness defaults = %d/%d, want %d/%d",
			p.Load().RainbowSpriteSpeed, p.Load().RainbowSpriteVividness, defaultRainbowSpeed, defaultRainbowVivid)
	}
	if p.Load().SpriteTintColorRGB() != defaultSpriteTintColor {
		t.Errorf("SpriteTintColor default = %06x, want %06x", p.Load().SpriteTintColorRGB(), defaultSpriteTintColor)
	}
	if p.Load().RainbowSpriteGlow || p.Load().RainbowPairDesync || p.Load().SpriteSolidTint {
		t.Error("sprite-FX glow/desync/solid must default OFF")
	}
	if p.Load().LoopPreanim {
		t.Error("LoopPreanim must default OFF (canonical AO2 plays preanims once)")
	}
}

// TestExportOptionsDefaultsAndPersist pins the scene-export (GIF/WebP) settings:
// sensible defaults, clamping on Set, and a save→reload round-trip — the merge
// clause that would silently reset every export to defaults if it were dropped.
func TestExportOptionsDefaultsAndPersist(t *testing.T) {
	p, _ := newTestPrefs(t)
	if d := p.Load().Export; d.HeightPx != defaultExportHeight || d.FPS != defaultExportFPS || d.Quality != defaultExportQuality || !d.Loop || d.TextScale != defaultExportText || d.VideoFormat != defaultVideoFormat {
		t.Fatalf("default export opts = %+v, want %d/%d/%d/%d/%s + loop", d, defaultExportHeight, defaultExportFPS, defaultExportQuality, defaultExportText, defaultVideoFormat)
	}
	// An unknown video format normalizes to the MP4 default (never crashes ffmpeg).
	p.SetExportOpts(ExportOptions{HeightPx: 360, FPS: 12, Quality: 80, Loop: true, TextScale: 100, VideoFormat: "avi"})
	if g := p.Load().Export; g.VideoFormat != defaultVideoFormat {
		t.Fatalf("bogus video format = %q, want normalized to %q", g.VideoFormat, defaultVideoFormat)
	}
	// Out-of-range values clamp to the configured bounds.
	p.SetExportOpts(ExportOptions{HeightPx: 99999, FPS: 0, Quality: 999, Loop: false, TextScale: 9999})
	if g := p.Load().Export; g.HeightPx != maxExportHeight || g.FPS != minExportFPS || g.Quality != maxExportQuality || g.Loop || g.TextScale != maxExportText {
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
	if o := r.Load().Export; o.HeightPx != 480 || o.FPS != 24 || o.Quality != 60 || !o.Loop || o.TextScale != 70 || o.VideoFormat != "webm" {
		t.Fatalf("reloaded export opts = %+v, want 480/24/60/70/webm + loop (merge clause dropped a field?)", o)
	}
}

// TestSpriteStylePref pins the transmitted sprite-style settings (#103): empty by
// default, opacity clamped, and a save→reload round-trip (including the hide
// toggle) — the merge clause that would silently drop the style otherwise.
func TestSpriteStylePref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if s := p.Load().MySpriteStyle; s.Tint || s.Glow || s.Opacity != 0 || s.Wobble || s.Spin {
		t.Fatalf("default sprite style not empty: %+v", s)
	}
	if p.Load().HideSpriteStyles {
		t.Fatal("HideSpriteStyles should default OFF (show others' styles)")
	}
	// Opacity clamps to [0,100].
	p.SetSpriteStyle(SpriteStylePref{Tint: true, R: 255, G: 60, B: 60, Opacity: 200})
	if g := p.Load().MySpriteStyle; g.Opacity != 100 {
		t.Errorf("opacity 200 clamped to %d, want 100", g.Opacity)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	want := SpriteStylePref{Tint: true, R: 10, G: 20, B: 30, Opacity: 50, Glow: true, Wobble: true, Spin: true, HueCycle: true, FlipH: true, Brightness: 70, Scale: 130, Rotation: 200}
	q.SetSpriteStyle(want)
	q.Update(func(
		sn *Settings) {

		sn.
			HideSpriteStyles =

			true

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if g := r.Load().MySpriteStyle; g != want {
		t.Errorf("reloaded sprite style = %+v, want %+v (merge clause dropped a field?)", g, want)
	}
	if !r.Load().HideSpriteStyles {
		t.Error("HideSpriteStyles didn't persist")
	}
}

// TestProfilePref pins the #101 character profile: empty by default, every field
// length-clamped, and a save→reload round-trip (the merge clause that would
// silently drop the profile otherwise).
func TestProfilePref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if pr := p.Load().MyProfile; pr.Enabled || pr.ShowOnList || pr.Name != "" || pr.Bio != "" {
		t.Fatalf("default profile not empty: %+v", pr)
	}
	// Oversized fields clamp to their caps.
	long := strings.Repeat("x", 1000)
	p.SetProfile(ProfilePref{Enabled: true, Name: long, Bio: long, ArtURL: long})
	g := p.Load().MyProfile
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
	if g := r.Load().MyProfile; g != want {
		t.Errorf("reloaded profile = %+v, want %+v (merge clause dropped a field?)", g, want)
	}
}

// TestInstantDisconnectPref pins the Disconnect-confirm toggle: OFF by default
// (confirm first), and the on state survives save→reload.
func TestInstantDisconnectPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().InstantDisconnect {
		t.Error("InstantDisconnect must default OFF (Disconnect confirms first)")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			InstantDisconnect =

			true

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.Load().InstantDisconnect {
		t.Error("InstantDisconnect=true lost on reload")
	}
}

// TestRightClickHideSpritePref pins the hide-sprite toggle: ON by default
// (right-click offers to hide), survives a save→reload OFF (the *bool DTO so an
// absent field keeps the default-ON, not a silent off).
func TestRightClickHideSpritePref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if !p.Load().RightClickHideSprite {
		t.Error("RightClickHideSprite must default ON")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(sn *Settings) { sn.RightClickHideSprite = false })
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if r.Load().RightClickHideSprite {
		t.Error("RightClickHideSprite=false lost on reload (absent != default-on)")
	}
}

// TestAutoConnectOnLaunchPref pins the auto-connect-on-launch opt-in: OFF by
// default, and the toggle plus the saved last server (name + URL) survive a
// save→reload so the next launch knows where to dial.
func TestAutoConnectOnLaunchPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().AutoConnectOnLaunch {
		t.Error("AutoConnectOnLaunch must default OFF (auto-connect is opt-in)")
	}
	if name, url := p.Load().LastServer(); name != "" || url != "" {
		t.Errorf("LastServer default = %q/%q, want empty", name, url)
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			AutoConnectOnLaunch =

			true

	})

	q.SetLastServer("Skrapegropen", "wss://example.test/ws")
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.Load().AutoConnectOnLaunch {
		t.Error("AutoConnectOnLaunch=true lost on reload")
	}
	if name, url := r.Load().LastServer(); name != "Skrapegropen" || url != "wss://example.test/ws" {
		t.Errorf("reloaded LastServer = %q/%q, want Skrapegropen/wss://example.test/ws", name, url)
	}
}

// TestEmoteFavsPref pins per-character emote favourites: index-keyed (not
// name-keyed, since emote labels/anims duplicate within a character), toggling
// is its own inverse, removing the last one drops the entry, and both the
// favourites and the favs-only display toggle survive a save→reload.
func TestEmoteFavsPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().EmoteFavOnly {
		t.Error("EmoteFavOnly must default OFF")
	}
	if got := p.Load().EmoteFavsFor("Apollo"); got != nil {
		t.Errorf("fresh EmoteFavsFor = %v, want nil", got)
	}
	// Toggle two emotes on; toggling one again removes it (its own inverse).
	if now := p.ToggleEmoteFav("Apollo", 0); !now {
		t.Error("ToggleEmoteFav(0) should report now-favourited")
	}
	p.ToggleEmoteFav("Apollo", 10)
	if !p.Load().IsEmoteFav("Apollo", 0) || !p.Load().IsEmoteFav("Apollo", 10) {
		t.Fatal("both 0 and 10 should be favourited")
	}
	if now := p.ToggleEmoteFav("apollo", 0); now { // case-insensitive char key
		t.Error("re-toggling 0 should report now-unfavourited")
	}
	if p.Load().IsEmoteFav("Apollo", 0) {
		t.Error("0 should be removed after the second toggle")
	}
	// Removing the last favourite drops the map entry entirely.
	p.ToggleEmoteFav("Apollo", 10)
	if got := p.Load().EmoteFavsFor("Apollo"); got != nil {
		t.Errorf("after clearing, EmoteFavsFor = %v, want nil", got)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.ToggleEmoteFav("Phoenix", 3)
	q.ToggleEmoteFav("Phoenix", 7)
	q.Update(func(
		sn *Settings) {

		sn.
			EmoteFavOnly =

			true

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.Load().EmoteFavOnly {
		t.Error("EmoteFavOnly=true lost on reload")
	}
	if !r.Load().IsEmoteFav("Phoenix", 3) || !r.Load().IsEmoteFav("Phoenix", 7) {
		t.Errorf("reloaded favourites = %v, want [3 7]", r.Load().EmoteFavsFor("Phoenix"))
	}
}

// TestServerIgnorePref pins the per-server ignore list (#81): empty by default,
// case-insensitive match, trim + dedup on set, per-server isolation, and a
// save→reload round-trip (so you can un-ignore someone who's left).
func TestServerIgnorePref(t *testing.T) {
	p, _ := newTestPrefs(t)
	const key = "wss://example.test/ws"
	if p.Load().ServerIgnoreMatch(key, "Bob") {
		t.Error("nothing should be ignored by default")
	}
	p.SetServerIgnored(key, []string{"Bob", "  Alice  ", "bob", ""}) // dup (ci) + pad + blank
	if got := p.Load().ServerIgnored(key); len(got) != 2 {
		t.Fatalf("ignored = %v, want 2 (Bob, Alice — deduped/trimmed)", got)
	}
	if !p.Load().ServerIgnoreMatch(key, "BOB") || !p.Load().ServerIgnoreMatch(key, "alice") {
		t.Error("match must be case-insensitive")
	}
	if p.Load().ServerIgnoreMatch(key, "Carol") {
		t.Error("Carol isn't ignored")
	}
	if p.Load().ServerIgnoreMatch("wss://other/ws", "Bob") {
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
	if !r.Load().ServerIgnoreMatch(key, "Mallory") {
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
	p.Update(func(sn *Settings) {
		sn.ServerWarm["wss://partial/ws"] = ServerWarmInfo{AudioOn: true, AudioMaster: &mv}
	}) // music/sfx/blip unset
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

	friend, color, nick := p.Load().ServerFriendInfo(key, "phoenix") // case-insensitive
	if !friend || color != 0xff4488 || nick != "Wright buddy" {
		t.Fatalf("Phoenix info = %v/%06x/%q, want true/ff4488/\"Wright buddy\" (comma stripped)", friend, color, nick)
	}
	if _, c, nk := p.Load().ServerFriendInfo(key, "Alice"); c != -1 || nk != "Allie" {
		t.Fatalf("Alice info color/nick = %d/%q, want -1/\"Allie\"", c, nk)
	}
	if _, c, nk := p.Load().ServerFriendInfo(key, "Bob"); c != 0x00ff00 || nk != "" {
		t.Fatalf("Bob info color/nick = %06x/%q, want 00ff00/\"\"", c, nk)
	}
	// ServerFriendMatch (the 2-return wrapper) still works for colour-only callers.
	if f, c := p.Load().ServerFriendMatch(key, "Phoenix"); !f || c != 0xff4488 {
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
	if f, c, nk := r.Load().ServerFriendInfo(key, "Mallory"); !f || c != 0x123456 || nk != "Mal" {
		t.Errorf("reloaded = %v/%06x/%q, want true/123456/\"Mal\"", f, c, nk)
	}
}

// TestInstantReplayPref pins the rolling clip buffer settings: OFF by default,
// the window defaults to 60s and clamps to [10s, 1 hour], and both survive
// save→reload.
func TestInstantReplayPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().InstantReplay {
		t.Error("InstantReplay must default OFF (opt-in)")
	}
	if got := p.Load().InstantReplaySecondsValue(); got != InstantReplayDefaultSeconds {
		t.Errorf("unset window = %d, want default %d", got, InstantReplayDefaultSeconds)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			InstantReplaySeconds =

			2

	})

	// below the floor
	if got := p.Load().InstantReplaySecondsValue(); got != InstantReplayMinSeconds {
		t.Errorf("clamped-low window = %d, want %d", got, InstantReplayMinSeconds)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			InstantReplaySeconds =

			100000

	})

	// past the 1-hour ceiling
	if got := p.Load().InstantReplaySecondsValue(); got != InstantReplayMaxSeconds {
		t.Errorf("clamped-high window = %d, want %d", got, InstantReplayMaxSeconds)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			InstantReplay =

			true

	})

	q.Update(func(
		sn *Settings) {

		sn.
			InstantReplaySeconds =

			120

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.Load().InstantReplay {
		t.Error("InstantReplay=true lost on reload")
	}
	if got := r.Load().InstantReplaySecondsValue(); got != 120 {
		t.Errorf("reloaded window = %d, want 120", got)
	}
}

// TestTimerPref pins the local alarm/timer settings (#97): repeat OFF by default,
// the remembered duration defaults to 5 min and clamps to [1s, 99 min], and both
// survive save→reload.
func TestTimerPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().TimerRepeat {
		t.Error("TimerRepeat must default OFF")
	}
	if got := p.Load().TimerSecondsValue(); got != TimerDefaultSeconds {
		t.Errorf("unset duration = %d, want default %d", got, TimerDefaultSeconds)
	}
	p.SetTimerSeconds(0) // 0/unset → default, not the floor
	if got := p.Load().TimerSecondsValue(); got != TimerDefaultSeconds {
		t.Errorf("zero duration = %d, want default %d", got, TimerDefaultSeconds)
	}
	p.SetTimerSeconds(1000000) // past the 99-minute ceiling
	if got := p.Load().TimerSecondsValue(); got != TimerMaxSeconds {
		t.Errorf("clamped-high duration = %d, want %d", got, TimerMaxSeconds)
	}

	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.SetTimerSeconds(90)
	q.Update(func(sn *Settings) { sn.TimerRepeat = true })
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := r.Load().TimerSecondsValue(); got != 90 {
		t.Errorf("reloaded duration = %d, want 90", got)
	}
	if !r.Load().TimerRepeat {
		t.Error("TimerRepeat=true lost on reload")
	}
}

// TestNotifyOnOOCPref pins the unread-badge OOC toggle: OFF by default (IC only),
// and survives save→reload when enabled.
func TestNotifyOnOOCPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().NotifyOnOOC {
		t.Error("NotifyOnOOC must default OFF (IC-only badge)")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			NotifyOnOOC =

			true

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.Load().NotifyOnOOC {
		t.Error("NotifyOnOOC=true lost on reload")
	}
}

// TestOOCBoxIsTheDefault pins the v1.87.0 default flip AND the reason it needed no
// migration stamp. A fresh install must land on the OOC BOX (OOCInLogTab OFF) — the
// layout every call site already documents as the default — while anyone who had
// opted into the Legacy-style log tab keeps it across the upgrade. That upgrade
// safety rests entirely on prefsJSON.OOCInLogTab having NO omitempty: every file
// ever saved carries an explicit value, so the unconditional load overlay restores
// it instead of falling back to the (now inverted) seed. Add omitempty there and a
// tab user silently becomes a box user on upgrade — this test is the tripwire.
func TestOOCBoxIsTheDefault(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().OOCInLogTab {
		t.Error("a fresh install must default to the OOC BOX (OOCInLogTab OFF)")
	}

	// The opt-in survives a save→reload round trip (the upgrade path).
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			OOCInLogTab =

			true

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.Load().OOCInLogTab {
		t.Error("OOCInLogTab=true lost on reload — the opt-in must survive the default flip")
	}

	// And the serialized form really does carry the key explicitly (what makes the
	// line above work). A marshalled default must ALSO write it, or a fresh config
	// saved today would read back as "absent" tomorrow.
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read prefs: %v", err)
	}
	if !strings.Contains(string(blob), `"oocInLogTab"`) {
		t.Error(`saved prefs must always carry "oocInLogTab" (no omitempty) — that is the upgrade guarantee`)
	}
}

// TestEmoteGridGapPref pins the emote-grid icon spacing end to end. The subtle
// part is ZERO: "no gap at all" is a legal choice, so the on-disk DTO carries the
// value as a *int. If it were ever demoted to a plain int, a config written before
// the setting existed would read back as 0 and every upgrading user's emote grid
// would silently collapse to flush icons — which is exactly what the
// absent-key case below catches.
func TestEmoteGridGapPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if got := p.EmoteGridGap(); got != DefaultEmoteGridGapPx {
		t.Errorf("fresh EmoteGridGap = %d, want the shipped %d (nobody's grid may move on upgrade)", got, DefaultEmoteGridGapPx)
	}

	// A deliberate 0 must survive save→reload as 0, not snap back to the default.
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			EmoteGridGapPx =

			0

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := r.EmoteGridGap(); got != 0 {
		t.Errorf("reloaded EmoteGridGap = %d, want 0 — a chosen zero gap must not be mistaken for 'unset'", got)
	}

	// A config that predates the setting keeps the DEFAULT, not zero.
	old := filepath.Join(t.TempDir(), PrefsFileName)
	if err := os.WriteFile(old, []byte(`{"emoteHoverNames": true}`), 0o600); err != nil {
		t.Fatalf("write legacy prefs: %v", err)
	}
	legacy, err := load(old)
	if err != nil {
		t.Fatalf("load legacy prefs: %v", err)
	}
	if got := legacy.EmoteGridGap(); got != DefaultEmoteGridGapPx {
		t.Errorf("legacy config EmoteGridGap = %d, want the default %d (absent must not read as a chosen 0)", got, DefaultEmoteGridGapPx)
	}

	// Out-of-range values clamp on the way in and on the way out.
	s, _ := newTestPrefs(t)
	s.Update(func(
		sn *Settings) {

		sn.
			EmoteGridGapPx =

			MaxEmoteGridGapPx +

				500

	})

	if got := s.EmoteGridGap(); got != MaxEmoteGridGapPx {
		t.Errorf("over-max EmoteGridGap = %d, want %d", got, MaxEmoteGridGapPx)
	}
	s.Update(func(
		sn *Settings) {

		sn.
			EmoteGridGapPx =

			-42

	})

	if got := s.EmoteGridGap(); got != MinEmoteGridGapPx {
		t.Errorf("under-min EmoteGridGap = %d, want %d", got, MinEmoteGridGapPx)
	}
}

// TestMusicAcrossTabsPref pins the cross-tab music-continuity toggle: OFF by
// default (a backgrounded tab's music ducks to silence), and survives save→reload
// when enabled (the marshal + load overlay must BOTH carry it — the saves-but-does-
// not-load trap).
func TestMusicAcrossTabsPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().MusicAcrossTabs {
		t.Error("MusicAcrossTabs must default OFF (backgrounded tab music ducks to 0)")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			MusicAcrossTabs =

			true

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.Load().MusicAcrossTabs {
		t.Error("MusicAcrossTabs=true lost on reload")
	}
}

// TestPlayerListSortPref pins that the Players-tab sort choices (player sort +
// /gas area-group order) default to 0 and survive save→reload.
func TestPlayerListSortPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().PlayerListSort != 0 || p.Load().PlayerListAreaSort != 0 {
		t.Fatalf("sort prefs must default to 0, got %d/%d", p.Load().PlayerListSort, p.Load().PlayerListAreaSort)
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(sn *Settings) { sn.PlayerListSort = 1 })
	q.Update(func(sn *Settings) { sn.PlayerListAreaSort = 2 })
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if r.Load().PlayerListSort != 1 || r.Load().PlayerListAreaSort != 2 {
		t.Errorf("reloaded sorts = %d/%d, want 1/2", r.Load().PlayerListSort, r.Load().PlayerListAreaSort)
	}
}

// TestChatboxOpacityPref pins the see-through chatbox setting: default 84,
// clamps, and survives save→reload (the *int DTO so a fresh config isn't 0%).
func TestChatboxOpacityPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().ChatboxOpacity != DefaultChatboxOpacity {
		t.Fatalf("ChatboxOpacity default = %d, want %d", p.Load().ChatboxOpacity, DefaultChatboxOpacity)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			ChatboxOpacity =

			999

	})

	if p.Load().ChatboxOpacity != MaxChatboxOpacity {
		t.Errorf("over-max = %d, want %d", p.Load().ChatboxOpacity, MaxChatboxOpacity)
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			ChatboxOpacity =

			30

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if r.Load().ChatboxOpacity != 30 {
		t.Errorf("reloaded opacity = %d, want 30", r.Load().ChatboxOpacity)
	}
}

// TestModSFXPrefs pins the #60 mod-command sounds: every action defaults OFF
// with no custom file, and toggles + paths survive save→load.
func TestModSFXPrefs(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().ModBanSFX || p.Load().ModKickSFX || p.Load().ModMuteSFX {
		t.Error("mod-SFX toggles must default OFF")
	}
	if p.Load().ModBanSoundFile != "" || p.Load().ModKickSoundFile != "" || p.Load().ModMuteSoundFile != "" {
		t.Error("mod-SFX sound paths must default empty")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			ModBanSFX =

			true

	})

	q.Update(func(
		sn *Settings) {

		sn.
			ModKickSFX =

			true

	})

	q.Update(func(
		sn *Settings) {

		sn.
			ModMuteSFX =

			true

	})

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
	if !r.Load().ModBanSFX || !r.Load().ModKickSFX || !r.Load().ModMuteSFX {
		t.Error("mod-SFX toggles lost across reload")
	}
	if r.Load().ModBanSoundFile != "ban.opus" || r.Load().ModKickSoundFile != "kick.wav" || r.Load().ModMuteSoundFile != "mute.ogg" {
		t.Errorf("mod-SFX paths lost: %q %q %q", r.Load().ModBanSoundFile, r.Load().ModKickSoundFile, r.Load().ModMuteSoundFile)
	}
}

// TestFollowEnabledPref pins the opt-in player-follow toggle: OFF by default,
// and an explicit ON survives save→load.
func TestFollowEnabledPref(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().FollowEnabled {
		t.Error("FollowEnabled must default OFF (opt-in)")
	}
	path := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	q.Update(func(
		sn *Settings) {

		sn.
			FollowEnabled =

			true

	})

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !r.Load().FollowEnabled {
		t.Error("FollowEnabled=true lost across save/load")
	}
}

// TestPreviewHoverClamp pins the dwell bounds (the setter is authoritative).
func TestPreviewHoverClamp(t *testing.T) {
	p, _ := newTestPrefs(t)
	{
		pv1 := 1 << 30
		p.Update(func(sn *Settings) {
			sn.PreviewHoverMs = pv1
		})
	}
	if got := p.Load().PreviewHoverMillis(); got != maxPreviewHoverMs {
		t.Errorf("clamp hi = %d, want %d", got, maxPreviewHoverMs)
	}
	p.Update(func(sn *Settings) { sn.PreviewHoverMs = 1 })
	if got := p.Load().PreviewHoverMillis(); got != minPreviewHoverMs {
		t.Errorf("clamp lo = %d, want %d", got, minPreviewHoverMs)
	}
}

// TestICLogDepthRoundTrip pins the IC scrollback depth pref (Settings → Chat):
// 0 = the shipped default, a typed number clamps into range, and the
// ICLogDepthUnlimited "don't cap at all" sentinel survives save→load un-clamped.
func TestICLogDepthRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if p.Load().ICLogDepthVal != 0 {
		t.Fatalf("ICLogDepth must default 0 (the shipped 1024), got %d", p.Load().ICLogDepthVal)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			ICLogDepthVal =

			5000

	})

	if got := p.Load().ICLogDepthVal; got != 5000 {
		t.Fatalf("ICLogDepth = %d, want 5000", got)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			ICLogDepthVal =

			1 <<

				30

	})

	if got := p.Load().ICLogDepthVal; got != ICLogDepthMax {
		t.Fatalf("ICLogDepth clamp hi = %d, want %d", got, ICLogDepthMax)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			ICLogDepthVal =

			0

	})

	if got := p.Load().ICLogDepthVal; got != 0 {
		t.Fatalf("ICLogDepth back to default = %d, want 0", got)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			ICLogDepthVal =

			ICLogDepthUnlimited

	})

	if got := p.Load().ICLogDepthVal; got != ICLogDepthUnlimited {
		t.Fatalf("ICLogDepth unlimited = %d, want %d", got, ICLogDepthUnlimited)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if q.Load().ICLogDepthVal != ICLogDepthUnlimited {
		t.Errorf("unlimited sentinel lost across save→load: %d, want %d", q.Load().ICLogDepthVal, ICLogDepthUnlimited)
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
	p.Update(func(sn *Settings) { sn.SpritePreviewOn = false })
	p.Update(func(sn *Settings) { sn.PreviewHoverMs = 8000 })
	p.SetPreviewHeightPx(512)                                     // default-when-0 int — must survive
	p.Update(func(sn *Settings) { sn.UpdateExperimental = true }) // default-OFF plain bool — must survive (the test-branch channel)
	p.Update(func(
		sn *Settings) {

		sn.
			ShowAssetWarnings =

			true

	})

	p.Update(func(sn *Settings) { sn.SpriteMoveOn = true })
	p.Update(func(
		sn *Settings) {

		sn.
			DeskFollowManifest =

			false

	})

	// explicit non-default false must survive the absent-default-ON pointer (default flipped OFF→ON 2026-08-08 — true would now be a no-op set)
	p.Update(func(
		sn *Settings) {

		sn.
			AutoLoginToast =

			false

	})

	// explicit false must survive the absent-default-ON pointer
	p.Update(func(
		sn *Settings) {

		sn.
			CallwordToast =

			false

	})

	// same absent-default-ON pointer
	p.Update(func(
		sn *Settings) {

		sn.
			MessageCounter =

			false

	})

	// same absent-default-ON pointer
	p.Update(func(
		sn *Settings) {

		sn.
			ICTimestamps =

			true

	})

	// explicit non-default true must survive the absent-default-OFF pointer
	p.Update(func(
		sn *Settings) {

		sn.
			AutoReconnect =

			true

	})

	// explicit non-default true must survive the absent-default-OFF pointer
	p.Update(func(sn *Settings) { sn.ScreenEffects = false }) // default-ON *bool — explicit false must survive
	p.Update(func(sn *Settings) { sn.WordDelete = false })    // default-ON *bool — explicit false must survive
	p.Update(func(
		sn *Settings) {

		sn.
			MusicHistory =

			false

	})

	// same absent-default-ON pointer
	p.Update(func(
		sn *Settings) {

		sn.
			MusicStreaming =

			false

	})

	// same absent-default-ON pointer — explicit OFF must survive
	p.Update(func(sn *Settings) { sn.RainbowSprites = true }) // default-OFF plain bool — must survive as true
	p.Update(func(
		sn *Settings) {

		sn.
			LoopPreanim =

			true

	})

	// default-OFF plain bool — must survive as true
	p.Update(func(sn *Settings) { sn.ShowRecordButton = true })  // default-OFF plain bool
	p.Update(func(sn *Settings) { sn.ShowFriendButton = false }) // default-ON *bool — explicit false must survive
	p.Update(func(
		sn *Settings) {

		sn.
			DragLayout =

			false

	})

	// default-ON *bool — explicit false must survive
	p.Update(func(
		sn *Settings) {

		sn.
			EventDrivenLoop =

			false

	})

	// default-ON *bool — the experimental-loop kill switch must stick
	p.Update(func(
		sn *Settings) {

		sn.
			DisableFrameLimiter =

			true

	})

	// #5 bypass — default-OFF plain bool must survive as true
	p.Update(func(
		sn *Settings) {

		sn.
			MotionRedrawPerEvent =

			false

	})

	// per-event motion redraw — default-ON *bool, so an explicit OFF must survive
	p.Update(func(
		sn *Settings) {

		sn.
			IdleFPSVal =

			FPSUnlimited

	})

	// the ∞ sentinel must survive save→load un-clamped
	p.Update(func(sn *Settings) { sn.RainbowSpriteSpeed = 30 })
	p.Update(func(sn *Settings) { sn.RainbowSpriteVividness = 95 })
	p.Update(func(sn *Settings) { sn.RainbowSpriteGlow = true })
	p.Update(func(sn *Settings) { sn.RainbowPairDesync = true })
	p.Update(func(sn *Settings) { sn.RainbowPerChar = true })
	p.Update(func(sn *Settings) { sn.SpriteWobble = true })
	p.Update(func(sn *Settings) { sn.SpriteSpin = true })
	p.Update(func(sn *Settings) { sn.SpriteSolidTint = true })
	p.Update(func(sn *Settings) { sn.SpriteTintColor = 0x112233 })
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if q.Load().SpritePreviewOn {
		t.Error("SpritePreview=false lost (absent-default ON must not clobber explicit false)")
	}
	if got := q.Load().PreviewHoverMillis(); got != 8000 {
		t.Errorf("PreviewHoverMillis = %d, want 8000", got)
	}
	if !q.Load().ShowAssetWarnings {
		t.Error("ShowAssetWarnings lost")
	}
	if !q.Load().SpriteMoveOn {
		t.Error("SpriteMove lost")
	}
	if q.Load().DeskFollowManifest {
		t.Error("DeskFollowManifest=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.Load().AutoLoginToast {
		t.Error("AutoLoginToast=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.Load().ScreenEffects {
		t.Error("ScreenEffects=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.Load().WordDelete {
		t.Error("WordDelete=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.Load().CallwordToast {
		t.Error("CallwordToast=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.Load().MessageCounter {
		t.Error("MessageCounter=false lost (absent-default ON must not clobber explicit false)")
	}
	if !q.Load().AutoReconnect {
		t.Error("AutoReconnect=true lost (absent-default OFF must not clobber explicit true)")
	}
	if q.Load().MusicHistory {
		t.Error("MusicHistory=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.Load().MusicStreaming {
		t.Error("MusicStreaming=false lost (absent-default ON must not clobber explicit false)")
	}
	if !q.Load().RainbowSprites {
		t.Error("RainbowSprites=true lost across reload")
	}
	if !q.Load().LoopPreanim {
		t.Error("LoopPreanim=true lost across reload (default-OFF plain bool must persist an explicit ON)")
	}
	if !q.Load().ShowRecordButton {
		t.Error("ShowRecordButton=true lost across reload")
	}
	if q.Load().DragLayout {
		t.Error("DragLayout=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.Load().ShowFriendButton {
		t.Error("ShowFriendButton=false lost (absent-default ON must not clobber explicit false)")
	}
	if q.Load().EventDrivenLoop {
		t.Error("EventDrivenLoop=false lost (absent-default ON must not clobber the explicit kill switch)")
	}
	if !q.Load().DisableFrameLimiter {
		t.Error("DisableFrameLimiter=true lost across reload")
	}
	if q.Load().MotionRedrawPerEvent {
		t.Error("MotionRedrawPerEvent=false lost across reload (default-ON *bool must persist an explicit OFF)")
	}
	if got := q.Load().IdleFPS(); got != FPSUnlimited {
		t.Errorf("IdleFPS unlimited sentinel lost across reload: %d, want %d", got, FPSUnlimited)
	}
	if got := q.Load().PreviewHeightPx(); got != 512 {
		t.Errorf("PreviewHeightPx lost: %d, want 512", got)
	}
	if !q.Load().UpdateExperimental {
		t.Error("UpdateExperimental=true lost across reload (the channel swap must stick)")
	}
	if q.Load().RainbowSpriteSpeed != 30 || q.Load().RainbowSpriteVividness != 95 {
		t.Errorf("rainbow speed/vividness lost: %d/%d, want 30/95", q.Load().RainbowSpriteSpeed, q.Load().RainbowSpriteVividness)
	}
	if !q.Load().RainbowSpriteGlow || !q.Load().RainbowPairDesync || !q.Load().SpriteSolidTint {
		t.Error("a sprite-FX toggle (glow/desync/solid) lost across reload")
	}
	if !q.Load().RainbowPerChar || !q.Load().SpriteWobble || !q.Load().SpriteSpin {
		t.Error("a wacky-FX toggle (per-char/wobble/spin) lost across reload")
	}
	if q.Load().SpriteTintColorRGB() != 0x112233 {
		t.Errorf("SpriteTintColor lost: %06x, want 112233", q.Load().SpriteTintColorRGB())
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

	if b := p.Load().ShownameKeyBinds(); b["f1"] != "Nick" || b["f2"] != "Edgey" {
		t.Fatalf("binds = %v, want f1->Nick f2->Edgey", b)
	}
	p.SetShownameKeyBind("f1", "") // empty showname clears the bind
	if p.Load().ShownameKeyBinds()["f1"] != "" {
		t.Error("f1 bind should be cleared")
	}
	p.RemoveShownamePreset("Edgey") // removing a preset drops its bind
	if p.Load().ShownameKeyBinds()["f2"] != "" {
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
	if got := q.Load().ShownameKeyBinds()["f1"]; got != "Nick" {
		t.Errorf("keybind lost across save/load: f1 = %q, want Nick", got)
	}
}

// TestICPhraseKeyBinds pins the hotkeyed IC phrases: keys store lowercased, an
// empty phrase clears, and a bind survives save→load.
func TestICPhraseKeyBinds(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().ICPhraseBinds() != nil {
		t.Error("no IC-phrase binds by default")
	}
	p.SetICPhraseKey("E", "Happy Pride Month") // key lowercased on store
	p.SetICPhraseKey("q", "Objection!")
	if b := p.Load().ICPhraseBinds(); b["e"] != "Happy Pride Month" || b["q"] != "Objection!" {
		t.Fatalf("binds = %v, want e->phrase q->Objection!", b)
	}
	p.SetICPhraseKey("e", "") // an empty phrase clears the bind
	if p.Load().ICPhraseBinds()["e"] != "" {
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
	if got := r.Load().ICPhraseBinds()["f1"]; got != "Take that!" {
		t.Errorf("IC-phrase bind lost across save/load: f1 = %q, want Take that!", got)
	}
}

// TestMutedSFX pins the M11 per-SFX mute list: case-insensitive store/match,
// dedup, unmute, and survival across save → load.
func TestMutedSFX(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().IsSFXMuted("sfx-stab") {
		t.Error("nothing should be muted initially")
	}
	if !p.MuteSFX("SFX-Stab") { // stored lowercase
		t.Error("MuteSFX should report a change")
	}
	if !p.Load().IsSFXMuted("sfx-stab") || !p.Load().IsSFXMuted("SFX-STAB") {
		t.Error("a muted SFX should match case-insensitively")
	}
	if p.MuteSFX("sfx-stab") {
		t.Error("re-muting the same SFX must be a no-op")
	}
	if l := p.Load().MutedSFXList(); len(l) != 1 || l[0] != "sfx-stab" {
		t.Fatalf("list = %v, want [sfx-stab]", l)
	}
	if !p.UnmuteSFX("sfx-stab") || p.Load().IsSFXMuted("sfx-stab") {
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
	if !q.Load().IsSFXMuted("sfx-stab") {
		t.Error("muted SFX lost across save/load")
	}
}

// TestSfxFavorites pins the #12 starred-SFX list: case-insensitive store/match, toggle on/off,
// dedup, and survival across save → load.
func TestSfxFavorites(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().IsSfxFavorite("sfx-stab") {
		t.Error("nothing should be starred initially")
	}
	if !p.ToggleSfxFavorite("SFX-Stab") { // stored lowercase; reports now-starred
		t.Error("first toggle should report starred (true)")
	}
	if !p.Load().IsSfxFavorite("sfx-stab") || !p.Load().IsSfxFavorite("SFX-STAB") {
		t.Error("a starred SFX should match case-insensitively")
	}
	if l := p.Load().SfxFavoritesList(); len(l) != 1 || l[0] != "sfx-stab" {
		t.Fatalf("list = %v, want [sfx-stab]", l)
	}
	if p.ToggleSfxFavorite("sfx-stab") { // toggling again unstars; reports false
		t.Error("second toggle should report unstarred (false)")
	}
	if p.Load().IsSfxFavorite("sfx-stab") {
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
	if !q.Load().IsSfxFavorite("sfx-stab") {
		t.Error("starred SFX lost across save/load")
	}
}

// TestModReasonTemplates pins the editable reason chips: defaults when empty, add (case-preserved,
// deduped case-insensitively), remove a seeded default, and the cap.
func TestModReasonTemplates(t *testing.T) {
	p, _ := newTestPrefs(t)
	def := p.Load().ModReasonTemplatesList()
	if len(def) == 0 {
		t.Fatal("an unset list must return the built-in defaults")
	}
	if !p.AddModReasonTemplate("Metagaming") {
		t.Error("AddModReasonTemplate should report a change")
	}
	if p.AddModReasonTemplate("metagaming") { // case-insensitive duplicate
		t.Error("dup add must be a no-op")
	}
	list := p.Load().ModReasonTemplatesList()
	if len(list) != len(def)+1 || list[len(list)-1] != "Metagaming" {
		t.Errorf("after add = %v, want the defaults + Metagaming", list)
	}
	if !p.RemoveModReasonTemplate("spam") { // case-insensitive remove of the "Spam" default
		t.Error("removing a seeded default should work")
	}
	for _, s := range p.Load().ModReasonTemplatesList() {
		if s == "Spam" {
			t.Error("Spam should be gone after removal")
		}
	}
	// Cap: piling on can't grow past modReasonTemplatesCap.
	for i := 0; i < modReasonTemplatesCap+10; i++ {
		p.AddModReasonTemplate("r" + strconv.Itoa(i))
	}
	if len(p.Load().ModReasonTemplatesList()) > modReasonTemplatesCap {
		t.Errorf("list grew to %d, want capped at %d", len(p.Load().ModReasonTemplatesList()), modReasonTemplatesCap)
	}
}

// TestResetPowerUser pins the nuke button's scope: every power-user knob reverts to
// its shipped default, and the deliberately-excluded state (custom mod durations —
// user data, not a knob) survives. If a new power-user pref is added, it belongs in
// ResetPowerUser AND in this list.
func TestResetPowerUser(t *testing.T) {
	p, _ := newTestPrefs(t)
	// Scramble every knob off its default.
	p.Update(func(sn *Settings) { sn.ValidateTLSCerts = true })
	p.SetAssetOriginHeader("https://a.example")
	p.SetWSOriginHeader("https://b.example")
	p.SetSpriteLoadMode(SpriteLoadWait)
	p.SetSpriteWaitMs(4000)
	p.Update(func(sn *Settings) { sn.SpriteWaitPair = true })
	p.Update(func(sn *Settings) { sn.SpriteWaitPreanim = true })
	p.Update(func(sn *Settings) { sn.ShoutBypassQueue = false }) // off the default (ON) — the nuke must restore it
	p.Update(func(
		sn *Settings) {

		sn.
			HoldPrevMaxAgeMsVal =

			9000

	})

	p.Update(func(
		sn *Settings) {

		sn.
			HoldDebugTint =

			true

	})

	p.Update(func(
		sn *Settings) {

		sn.
			HotkeysEnabled =

			true

	})

	p.Update(func(sn *Settings) { sn.ShoutDurationMsVal = 1000 })
	p.Update(func(sn *Settings) { sn.PreanimTimeoutMsVal = 5000 })
	p.Update(func(
		sn *Settings) {

		sn.
			ICQueueCapVal =

			128

	})

	p.Update(func(
		sn *Settings) {

		sn.
			CatchUpLingerMsVal =

			250

	})

	p.Update(func(sn *Settings) { sn.ThumbCache = true })
	p.Update(func(sn *Settings) { sn.ThumbHeightPxVal = 120 })
	p.Update(func(sn *Settings) { sn.ThumbQualityVal = 40 })
	p.Update(func(sn *Settings) { sn.ThumbBudgetMiBVal = 256 })
	p.Update(func(
		sn *Settings) {

		sn.
			NotFoundTTLSecVal =

			60

	})

	p.Update(func(
		sn *Settings) {

		sn.
			AdaptiveLatMultipleVal =

			4

	})

	p.Update(func(sn *Settings) { sn.SpriteDownscaleOff = true })
	p.Update(func(sn *Settings) { sn.SpriteDownscalePctVal = 150 })
	p.Update(func(
		sn *Settings) {

		sn.
			AnimatedSpriteCapPxVal =

			480

	})

	p.Update(func(sn *Settings) { sn.TexBudgetMiBVal = 96 })
	p.Update(func(
		sn *Settings) {

		sn.
			CrossfadeMsVal =

			300

	})

	p.Update(func(
		sn *Settings) {

		sn.
			FPSCapVal =

			144

	})

	p.Update(func(
		sn *Settings) {

		sn.
			IdleFPSVal =

			60

	})

	p.Update(func(sn *Settings) { sn.UnfocusedFPSVal = 30 })
	p.Update(func(
		sn *Settings) {

		sn.
			EventDrivenLoop =

			false

	})

	p.Update(func(
		sn *Settings) {

		sn.
			DisableFrameLimiter =

			true

	})

	// #5 bypass — must revert to OFF on nuke
	p.Update(func(
		sn *Settings) {

		sn.
			MotionRedrawPerEvent =

			false

	})

	// per-event motion redraw — must revert to its default (ON) on nuke
	p.Update(func(
		sn *Settings) {

		sn.
			ClipSpritesToStage =

			false

	})

	p.Update(func(sn *Settings) { sn.ProxyModeVal = ProxyModeManual })
	p.SetProxyURL("http://proxy.example:8080")
	p.SetAssetCharCasing(2)
	p.AddModDuration("45m") // user data — must SURVIVE the nuke

	p.ResetPowerUser()

	if p.Load().ValidateTLSCerts || p.Load().AssetOrigin != "" || p.Load().WSOrigin != "" {
		t.Error("nuke must clear TLS + both Origin overrides")
	}
	// Back to "use the machine's setting", NOT to "never proxy": the nuke is a
	// return to shipped defaults, and shipping with the proxy ignored is the
	// defect the setting exists to fix.
	if p.Load().ProxyModeVal != ProxyModeSystem || p.Load().ProxyURL != "" {
		t.Errorf("nuke left proxy mode=%d url=%q, want the system default and no manual URL", p.Load().ProxyModeVal, p.Load().ProxyURL)
	}
	if p.Load().AssetCharCase != 0 {
		t.Errorf("nuke left character-folder casing at %d, want lowercase", p.Load().AssetCharCase)
	}
	if p.Load().SpriteLoadModeVal != SpriteLoadDefault || p.Load().SpriteWaitMs() != SpriteWaitDefaultMs ||
		p.Load().SpriteWaitPair || p.Load().SpriteWaitPreanim {
		t.Error("nuke must reset the loading mode to its default (Default) + the wait knobs")
	}
	if !p.Load().ShoutBypassQueue {
		t.Error("nuke must restore the shout-bypass toggle to its default ON (AO2 parity)")
	}
	if p.Load().HoldPrevMaxAgeMsVal != 0 || p.Load().HoldDebugTint {
		t.Error("nuke must reset the hold-previous knobs")
	}
	if p.Load().HotkeysEnabled {
		t.Error("nuke must reset the hotkeys opt-in to its default OFF")
	}
	if p.Load().ShoutDurationMsVal != 0 || p.Load().PreanimTimeoutMsVal != 0 || p.Load().ICQueueCapVal != 0 || p.Load().CatchUpLingerMsVal != 0 {
		t.Error("nuke must reset the core timings + queue knobs to their defaults")
	}
	if !p.Load().ClipSpritesToStage {
		t.Error("nuke must restore the sprite mask to its default ON")
	}
	if p.Load().ThumbCache || p.Load().ThumbHeightPx() != ThumbHeightDefaultPx || p.Load().ThumbQuality() != ThumbQualityDefault || p.Load().ThumbBudgetMiB() != ThumbBudgetDefaultMiB {
		t.Error("nuke must reset the thumbnail-cache knobs (stored thumbs on disk are untouched — Clear is separate)")
	}
	if p.Load().NotFoundTTLSecVal != 0 || p.Load().AdaptiveLatMultipleVal != 0 {
		t.Error("nuke must reset the network knobs to their 0 = default sentinels")
	}
	if p.Load().SpriteDownscaleOff || p.Load().SpriteDownscalePctVal != 0 || p.Load().AnimatedSpriteCapPxVal != 0 || p.Load().TexBudgetMiB() != TexBudgetDefaultMiB {
		t.Error("nuke must reset the downscale + texture-budget knobs")
	}
	if p.Load().CrossfadeMsVal != 0 {
		t.Error("nuke must reset the crossfade to off")
	}
	if p.Load().FPSCap() != FPSCapDefault || p.Load().IdleFPS() != IdleFPSDefault || p.Load().UnfocusedFPS() != UnfocusedFPSDefault {
		t.Error("nuke must reset the frame-pacing rates to their defaults")
	}
	if !p.Load().EventDrivenLoop {
		t.Error("nuke must restore the experimental event-driven loop to its default ON")
	}
	if p.Load().DisableFrameLimiter {
		t.Error("nuke must restore the frame-limiter bypass to its default OFF")
	}
	if !p.Load().MotionRedrawPerEvent {
		t.Error("nuke must restore per-event motion redraw to its default ON")
	}
	if got := p.Load().ModDurationsList(); len(got) != 1 || got[0] != "45m" {
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
	if got := p.Load().ModDurationsList(); len(got) != 0 {
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
	if got := p.Load().ModDurationsList(); len(got) != 2 || got[0] != "45m" || got[1] != "2d" {
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
	if n := len(p.Load().ModDurationsList()); n > modDurationsCap {
		t.Errorf("list grew to %d, want capped at %d", n, modDurationsCap)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := q.Load().ModDurationsList(); len(got) == 0 || got[0] != "2d" {
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
	for _, s := range q.Load().ModReasonTemplatesList() {
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
	if p.Load().ShowPairStatus {
		t.Error("ShowPairStatus must default OFF")
	}
	p.Update(func(sn *Settings) { sn.ShowPairStatus = true })
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.Load().ShowPairStatus {
		t.Error("ShowPairStatus lost across save/load (missing copy-on-load line?)")
	}
}

// TestBlipVolume pins the M11 per-character blip scale: default 100, clamp,
// case-insensitive key, no-op detection, and that resetting to 100 clears the
// entry (so the map only holds real adjustments).
func TestBlipVolume(t *testing.T) {
	p, _ := newTestPrefs(t)
	if v := p.Load().BlipVolumeFor("phoenix"); v != 100 {
		t.Errorf("unset char = %d, want default 100", v)
	}
	if !p.SetBlipVolume("Phoenix", 40) { // stored lowercase
		t.Error("SetBlipVolume should report a change")
	}
	if v := p.Load().BlipVolumeFor("phoenix"); v != 40 {
		t.Errorf("after set = %d, want 40", v)
	}
	if p.SetBlipVolume("phoenix", 40) {
		t.Error("re-setting the same value must be a no-op")
	}
	if !p.SetBlipVolume("phoenix", 999) { // clamps to 100 → also resets the entry
		t.Error("clamped change should report a change")
	}
	if v := p.Load().BlipVolumeFor("phoenix"); v != 100 {
		t.Errorf("clamped-high = %d, want 100", v)
	}
	if vols := p.Load().BlipVolumes(); len(vols) != 0 {
		t.Errorf("resetting to 100 should clear the entry, got %v", vols)
	}
	if !p.SetBlipVolume("edgeworth", -5) { // clamps to 0
		t.Error("clamp-low should report a change")
	}
	if v := p.Load().BlipVolumeFor("edgeworth"); v != 0 {
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
	if v := q.Load().BlipVolumeFor("phoenix"); v != 25 {
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
	snap := p.Load().LearnedSnapshot()
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
	on, key, ms := p.Load().HoldClear()
	if !on {
		t.Error("hold-to-clear must default ON")
	}
	if key != "Backspace" {
		t.Errorf("default key = %q, want Backspace", key)
	}
	if ms != DefaultHoldClearMs {
		t.Errorf("default ms = %d, want %d", ms, DefaultHoldClearMs)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			HoldClearMs =

			1 <<

				20

	})

	// over-large clamps to max
	if _, _, got := p.Load().HoldClear(); got != MaxHoldClearMs {
		t.Errorf("ms clamp = %d, want %d", got, MaxHoldClearMs)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			HoldClearOn =

			false

	})

	p.SetHoldClearKey("Delete")
	if gotOn, gotKey, _ := p.Load().HoldClear(); gotOn || gotKey != "Delete" {
		t.Errorf("after edits: on=%v key=%q, want false, Delete", gotOn, gotKey)
	}
}

// TestExtrasBoxStyle pins the Extras-box theme prefs: blank/false by default
// (stock look), and a full round-trip of the five colours + gradient flag.
func TestExtrasBoxStyle(t *testing.T) {
	p, _ := newTestPrefs(t)
	if bg, _, _, _, _, grad := p.Load().ExtrasBoxStyle(); bg != "" || grad {
		t.Errorf("default ExtrasBoxStyle = bg %q grad %v, want blank/false", bg, grad)
	}
	p.SetExtrasBoxStyle("101018", "000000", "78aaff", "202030", "ffffff", true)
	bg, bg2, border, title, text, grad := p.Load().ExtrasBoxStyle()
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
	if got := p.Load().ShownameList(); len(got) != 2 {
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
	if got := q.Load().ShownameList(); len(got) != 1 || got[0] != "Edgeworth" {
		t.Errorf("after reload: ShownameList = %v, want [Edgeworth]", got)
	}
}

// TestThemeFitDefaultsAndClamp pins the theme-fit prefs: Native (1:1) is the
// default, and the mode/zoom/pan all clamp to their bounds.
// The default expectation CHANGED here (it was Stretch, the zero value) because
// the shipped default deliberately moved: stock AO2 never scales a theme, so 1:1
// is what an imported theme must look like with no settings tweaking. The clamp
// expectation moved with it — the upper bound is the LAST mode, which is now
// Native, not Custom.
func TestThemeFitDefaultsAndClamp(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.Load().ThemeFit != ThemeFitNative {
		t.Errorf("ThemeFitMode default = %d, want Native(%d)", p.Load().ThemeFit, ThemeFitNative)
	}
	if p.Load().ThemeZoom() != DefaultThemeZoom {
		t.Errorf("ThemeZoom default = %d, want %d", p.Load().ThemeZoom(), DefaultThemeZoom)
	}
	if !p.Load().PlainLobby {
		t.Error("PlainLobbyOn default must be true (readable server list)")
	}
	p.SetThemeFit(99) // out of range → clamps to the last mode (Native)
	if p.Load().ThemeFit != ThemeFitNative {
		t.Errorf("SetThemeFit clamp = %d, want Native(%d)", p.Load().ThemeFit, ThemeFitNative)
	}
	p.SetThemeFit(-1) // below the first mode → clamps to Stretch(0)
	if p.Load().ThemeFit != ThemeFitStretch {
		t.Errorf("SetThemeFit low clamp = %d, want Stretch(%d)", p.Load().ThemeFit, ThemeFitStretch)
	}
	p.SetThemeFit(ThemeFitCustom)
	{
		pv1 := // restore a mid mode so the zoom/pan checks below are meaningful
			1 << 20
		p.Update(func(sn *Settings) {
			sn.ThemeFitZoom = pv1
		})
	}
	if p.Load().ThemeZoom() != MaxThemeZoom {
		t.Errorf("zoom clamp = %d, want %d", p.Load().ThemeZoom(), MaxThemeZoom)
	}
	p.SetThemeFitPan(999, -999)
	if x, y := p.Load().ThemePan(); x != MaxThemePan || y != -MaxThemePan {
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
	if p.Load().FPSCap() != FPSCapDefault || p.Load().IdleFPS() != IdleFPSDefault || p.Load().UnfocusedFPS() != UnfocusedFPSDefault {
		t.Fatalf("fresh defaults = %d/%d/%d, want %d/%d/%d",
			p.Load().FPSCap(), p.Load().IdleFPS(), p.Load().UnfocusedFPS(), FPSCapDefault, IdleFPSDefault, UnfocusedFPSDefault)
	}
	// Below the old floors is now allowed, not clamped up.
	p.Update(func(
		sn *Settings) {

		sn.
			FPSCapVal =

			5

	})

	p.Update(func(
		sn *Settings) {

		sn.
			IdleFPSVal =

			2

	})

	p.Update(func(sn *Settings) { sn.UnfocusedFPSVal = 1 })
	if p.Load().FPSCap() != 5 || p.Load().IdleFPS() != 2 || p.Load().UnfocusedFPS() != 1 {
		t.Errorf("low rates clamped: cap=%d idle=%d unfocused=%d, want 5/2/1",
			p.Load().FPSCap(), p.Load().IdleFPS(), p.Load().UnfocusedFPS())
	}
	// Out-of-range positives still clamp to the max.
	p.Update(func(
		sn *Settings) {

		sn.
			IdleFPSVal =

			99999

	})

	if got := p.Load().IdleFPS(); got != IdleFPSMax {
		t.Errorf("idle over-max = %d, want clamp to %d", got, IdleFPSMax)
	}
	// The Settings "0"/off maps to FPSOff; ∞ to FPSUnlimited. Both are held live
	// and must survive the disk round-trip un-normalized.
	p.Update(func(
		sn *Settings) {

		sn.
			IdleFPSVal =

			FPSOff

	})

	p.Update(func(sn *Settings) { sn.UnfocusedFPSVal = FPSOff })
	p.Update(func(
		sn *Settings) {

		sn.
			FPSCapVal =

			FPSUnlimited

	})

	if p.Load().IdleFPS() != FPSOff || p.Load().UnfocusedFPS() != FPSOff || p.Load().FPSCap() != FPSUnlimited {
		t.Fatalf("sentinels not held live: idle=%d unfocused=%d cap=%d", p.Load().IdleFPS(), p.Load().UnfocusedFPS(), p.Load().FPSCap())
	}
	// Input-grace frames: default 1, clamps into range, survives the round-trip.
	if got := p.Load().InputGraceFrames(); got != InputGraceFramesDefault {
		t.Errorf("input-grace default = %d, want %d", got, InputGraceFramesDefault)
	}
	p.Update(func(
		sn *Settings) {

		sn.
			InputGraceFramesVal =

			99999

	})

	if got := p.Load().InputGraceFrames(); got != InputGraceFramesMax {
		t.Errorf("input-grace over-max = %d, want clamp to %d", got, InputGraceFramesMax)
	}
	p.SetInputGraceFrames(0)

	// 0 = OFF — must read back as 0 and survive as 0 (NOT snap to the default)
	if got := p.Load().InputGraceFrames(); got != 0 {
		t.Errorf("input-grace off = %d, want 0", got)
	}
	if err := p.Close(); err != nil { // flush + release before reload
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path) // load() alone starts no saver goroutine, so there's nothing to Close
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if q.Load().IdleFPS() != FPSOff {
		t.Errorf("idle off (FPSOff) lost across reload: %d, want %d (0 = off must persist)", q.Load().IdleFPS(), FPSOff)
	}
	if q.Load().UnfocusedFPS() != FPSOff {
		t.Errorf("unfocused off (FPSOff) lost across reload: %d, want %d", q.Load().UnfocusedFPS(), FPSOff)
	}
	if q.Load().FPSCap() != FPSUnlimited {
		t.Errorf("active ∞ lost across reload: %d, want %d", q.Load().FPSCap(), FPSUnlimited)
	}
	if q.Load().InputGraceFrames() != 0 {
		t.Errorf("input-grace OFF lost across reload: %d, want 0 (off must persist, not snap to the default)", q.Load().InputGraceFrames())
	}
}
