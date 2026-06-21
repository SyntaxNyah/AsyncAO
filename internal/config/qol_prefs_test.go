package config

import (
	"path/filepath"
	"strings"
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
	want := SpriteStylePref{Tint: true, R: 10, G: 20, B: 30, Opacity: 50, Glow: true, Wobble: true, Spin: true}
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
