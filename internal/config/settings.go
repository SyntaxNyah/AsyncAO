package config

import (
	"reflect"
	"sync/atomic"
)

// Settings is the plain data snapshot of AssetPreferences.
type Settings struct {
	GlobalFallbacksEnabled       bool                         `json:"globalFallbacksEnabled"`
	PreferAnimated               bool                         `json:"preferAnimated"`
	EmoteButtonImages            bool                         `json:"emoteButtonImages"`
	SmoothScaling                bool                         `json:"smoothScaling"`
	SpriteScaling                int                          `json:"spriteScaling"`
	SpriteScalingMigrated        bool                         `json:"spriteScalingMigrated"`
	UpdateCheck                  bool                         `json:"updateCheck"`
	UpdateExperimental           bool                         `json:"updateExperimental"`
	HighlightColor               int                          `json:"highlightColor"`
	ICCustomColor                int                          `json:"icCustomColor"`
	NameColors                   bool                         `json:"nameColors"`
	NameSat                      int                          `json:"nameColorSat"`
	NameVal                      int                          `json:"nameColorVal"`
	BgSlideshow                  bool                         `json:"bgSlideshow"`
	BgSlideshowSecs              int                          `json:"bgSlideshowSecs"`
	DownloadKBps                 int                          `json:"downloadKBps"`
	ForceCharNames               bool                         `json:"forceCharNames"`
	RandomEmote                  bool                         `json:"randomEmote"`
	FriendHighlight              bool                         `json:"friendHighlight"`
	ShowFriendButton             bool                         `json:"showFriendButton"`
	ClipSpritesToStage           bool                         `json:"clipSpritesToStage"`
	RightClickHideSprite         bool                         `json:"rightClickHideSprite"`
	DragLayout                   bool                         `json:"dragLayout"`
	FollowEnabled                bool                         `json:"followEnabled"`
	ShowPairStatus               bool                         `json:"showPairStatus"`
	PlayerListSort               int                          `json:"playerListSort"`
	PlayerListAreaSort           int                          `json:"playerListAreaSort"`
	DyslexiaFont                 bool                         `json:"dyslexiaFont"`
	FontEverywhere               bool                         `json:"fontEverywhere"`
	DNDPersist                   bool                         `json:"dndPersist"`
	DNDSaved                     bool                         `json:"dndSaved"`
	RainbowMessages              bool                         `json:"rainbowMessages"`
	RandomMessageColor           bool                         `json:"randomMessageColor"`
	RainbowSprites               bool                         `json:"rainbowSprites"`
	ShowRecordButton             bool                         `json:"showRecordButton"`
	InstantDisconnect            bool                         `json:"instantDisconnect"`
	HideDesk                     bool                         `json:"hideDesk"`
	FavEmoteBox                  bool                         `json:"favEmoteBox"`
	InstantReplay                bool                         `json:"instantReplay"`
	InstantReplaySeconds         int                          `json:"instantReplaySeconds"`
	TimerSeconds                 int                          `json:"timerSeconds"`
	TimerRepeat                  bool                         `json:"timerRepeat"`
	NotifyOnOOC                  bool                         `json:"notifyOnOOC"`
	ShowSongURL                  bool                         `json:"showSongURL"`
	AutoConnectOnLaunch          bool                         `json:"autoConnectOnLaunch"`
	LastServerName               string                       `json:"lastServerName"`
	LastServerURL                string                       `json:"lastServerURL"`
	RainbowSpriteSpeed           int                          `json:"rainbowSpriteSpeed"`
	ReplayPlaybackSpeed          int                          `json:"replaySpeed"`
	Export                       ExportOptions                `json:"export"`
	MySpriteStyle                SpriteStylePref              `json:"mySpriteStyle"`
	SavedStyles                  []StylePreset                `json:"stylePresets,omitempty"`
	HideSpriteStyles             bool                         `json:"hideSpriteStyles"`
	HideReactions                bool                         `json:"hideReactions"`
	CharBundlePrefetch           bool                         `json:"charBundlePrefetch"`
	PingChip                     bool                         `json:"pingChip"`
	ValidateTLSCerts             bool                         `json:"validateTLSCerts"`
	AssetOrigin                  string                       `json:"assetOrigin,omitempty"`
	WSOrigin                     string                       `json:"wsOrigin,omitempty"`
	AssetCharCase                uint8                        `json:"assetCharCase,omitempty"`
	ProxyModeVal                 int                          `json:"proxyMode"`
	ProxyURL                     string                       `json:"proxyUrl,omitempty"`
	PanelFontsVal                map[string]PanelFont         `json:"panelFonts,omitempty"`
	FontWarnThemes               []string                     `json:"fontWarnThemes,omitempty"`
	VoiceInputDevice             string                       `json:"voiceInputDevice,omitempty"`
	VoiceOutVolume               int                          `json:"voiceOutVolume,omitempty"`
	PrefetchAggro                int                          `json:"prefetchAggro,omitempty"`
	VoicePTTKey                  string                       `json:"voicePttKey,omitempty"`
	QuitConfirmSkip              bool                         `json:"quitConfirmSkip,omitempty"`
	LegacyDevTheme               bool                         `json:"legacyDevTheme"`
	OOCInLogTab                  bool                         `json:"oocInLogTab"`
	MyProfile                    ProfilePref                  `json:"profile"`
	ChatboxOpacity               int                          `json:"chatboxOpacity"`
	RainbowSpriteVividness       int                          `json:"rainbowSpriteVividness"`
	RainbowSpriteGlow            bool                         `json:"rainbowSpriteGlow"`
	RainbowPairDesync            bool                         `json:"rainbowPairDesync"`
	RainbowPerChar               bool                         `json:"rainbowPerChar"`
	SpriteWobble                 bool                         `json:"spriteWobble"`
	SpriteSpin                   bool                         `json:"spriteSpin"`
	SpriteSolidTint              bool                         `json:"spriteSolidTint"`
	SpriteTintColor              int                          `json:"spriteTintColor"`
	ShoutPunch                   bool                         `json:"shoutPunch"`
	ChatboxTint                  bool                         `json:"chatboxTint"`
	PostVignette                 bool                         `json:"postVignette"`
	PostScanlines                bool                         `json:"postScanlines"`
	PostGrain                    bool                         `json:"postGrain"`
	PostCRT                      bool                         `json:"postCRT"`
	AnimateEntrances             bool                         `json:"animateEntrances"`
	DepthOfField                 bool                         `json:"depthOfField"`
	Spotlight                    bool                         `json:"spotlight"`
	SpotlightStrength            int                          `json:"spotlightStrength"`
	IdleBreath                   bool                         `json:"idleBreath"`
	BreathNoBob                  bool                         `json:"breathNoBob"`
	BreathNoScale                bool                         `json:"breathNoScale"`
	BreathAmount                 int                          `json:"breathAmp"`
	BreathRate                   int                          `json:"breathSpeed"`
	Reflection                   bool                         `json:"reflection"`
	ReflectOpacity               int                          `json:"reflectStrength"`
	WeatherKind                  int                          `json:"weatherType"`
	WeatherDensity               int                          `json:"weatherIntensity"`
	StageFrameKind               int                          `json:"stageFrame"`
	FriendNotify                 bool                         `json:"friendNotify"`
	FriendOSToast                bool                         `json:"friendOSToast"`
	CallwordOSToast              bool                         `json:"callwordOSToast"`
	FriendGlowPulse              bool                         `json:"friendGlowPulse"`
	FriendSound                  bool                         `json:"friendSound"`
	FriendSoundFile              string                       `json:"friendSoundFile"`
	ModBanSFX                    bool                         `json:"modBanSFX"`
	ModKickSFX                   bool                         `json:"modKickSFX"`
	ModMuteSFX                   bool                         `json:"modMuteSFX"`
	ModBanSoundFile              string                       `json:"modBanSoundFile"`
	ModKickSoundFile             string                       `json:"modKickSoundFile"`
	ModMuteSoundFile             string                       `json:"modMuteSoundFile"`
	ModcallToast                 bool                         `json:"modcallToast"`
	CallwordSoundFile            string                       `json:"callwordSoundFile"`
	DebugOverlay                 bool                         `json:"debugOverlay"`
	AutoDetectFormats            bool                         `json:"formatAutoDetect"`
	ThemeLayoutOn                bool                         `json:"themeLayout"`
	ThemeFit                     int                          `json:"themeFit"`
	ThemeFitZoom                 int                          `json:"themeFitZoom"`
	ThemeFitPanX                 int                          `json:"themeFitPanX"`
	ThemeFitPanY                 int                          `json:"themeFitPanY"`
	PlainLobby                   bool                         `json:"plainLobby"`
	ThemeFonts                   bool                         `json:"themeFonts"`
	UIScaleAutoOn                bool                         `json:"uiScaleAuto"`
	CatchUpOn                    bool                         `json:"catchUpWhenBehind"`
	CatchUpThreshold             int                          `json:"catchUpThreshold"`
	MultiTabCap                  int                          `json:"multiTabCap"`
	RestoreTabs                  bool                         `json:"restoreTabs"`
	VolStripOn                   bool                         `json:"volStripOn"`
	ChangelogSeen                string                       `json:"changelogSeenVersion,omitempty"`
	SpriteLoadModeVal            int                          `json:"spriteLoadMode"`
	SpriteWaitMsVal              int                          `json:"spriteWaitMs,omitempty"`
	SpriteWaitPair               bool                         `json:"spriteWaitPair,omitempty"`
	SpriteWaitPreanim            bool                         `json:"spriteWaitPreanim,omitempty"`
	ShoutBypassQueue             bool                         `json:"shoutBypassQueue"`
	HoldPrevMaxAgeMsVal          int                          `json:"holdPrevMaxAgeMs,omitempty"`
	HoldDebugTint                bool                         `json:"holdDebugTint,omitempty"`
	HotkeysEnabled               bool                         `json:"hotkeysEnabled,omitempty"`
	ShoutDurationMsVal           int                          `json:"shoutDurationMs,omitempty"`
	PreanimTimeoutMsVal          int                          `json:"preanimTimeoutMs,omitempty"`
	ICQueueCapVal                int                          `json:"icQueueCap,omitempty"`
	ICLogDepthVal                int                          `json:"icLogDepth,omitempty"`
	CatchUpLingerMsVal           int                          `json:"catchUpLingerMs,omitempty"`
	ThumbCache                   bool                         `json:"thumbCache,omitempty"`
	ThumbHeightPxVal             int                          `json:"thumbHeightPx,omitempty"`
	ThumbQualityVal              int                          `json:"thumbQuality,omitempty"`
	ThumbBudgetMiBVal            int                          `json:"thumbBudgetMiB,omitempty"`
	DiskCacheBudgetMiBVal        int                          `json:"diskCacheBudgetMiB,omitempty"`
	NotFoundTTLSecVal            int                          `json:"notFoundTTLSec,omitempty"`
	AdaptiveLatMultipleVal       int                          `json:"adaptiveLatMultiple,omitempty"`
	SpriteDownscaleOff           bool                         `json:"spriteDownscaleOff,omitempty"`
	FPSCapVal                    int                          `json:"fpsCap,omitempty"`
	IdleFPSVal                   int                          `json:"idleFps,omitempty"`
	UnfocusedFPSVal              int                          `json:"unfocusedFps,omitempty"`
	InputGraceFramesVal          int                          `json:"inputGraceFrames,omitempty"`
	EventDrivenLoop              bool                         `json:"eventDrivenLoop"`
	DisableFrameLimiter          bool                         `json:"disableFrameLimiter,omitempty"`
	MotionRedrawPerEvent         bool                         `json:"motionRedrawPerEvent"`
	SpriteDownscalePctVal        int                          `json:"spriteDownscalePct,omitempty"`
	AnimatedSpriteCapPxVal       int                          `json:"animatedSpriteCapPx,omitempty"`
	TexBudgetMiBVal              int                          `json:"texBudgetMiB,omitempty"`
	AnimatedBudgetMiBVal         int                          `json:"animatedBudgetMiB,omitempty"`
	AnimDecodeConcurrencyVal     int                          `json:"animDecodeConcurrency,omitempty"`
	TexCompressionVal            int                          `json:"texCompression,omitempty"`
	CrossfadeMsVal               int                          `json:"crossfadeMs,omitempty"`
	MusicVolMode                 bool                         `json:"musicVolMode,omitempty"`
	MusicFlagsVal                int                          `json:"musicFlags"`
	OpenTabs                     []OpenTab                    `json:"openTabs"`
	ReduceMotionOn               bool                         `json:"reduceMotion"`
	DisableEffects               bool                         `json:"disableEffects"`
	FontCensus                   bool                         `json:"fontCensus"`
	ScreenEffects                bool                         `json:"screenEffects"`
	WordDelete                   bool                         `json:"wordDelete"`
	RecordingsKeepAssets         bool                         `json:"recordingsKeepAssets"`
	AdditiveText                 bool                         `json:"additiveText"`
	MusicDuckingOn               bool                         `json:"musicDucking"`
	PerAreaScroll                bool                         `json:"perAreaScrollback"`
	DetailedLog                  bool                         `json:"detailedLog"`
	AutoClipModcall              bool                         `json:"autoClipModcall"`
	GroupChatButton              bool                         `json:"groupChatButton"`
	CharChatbox                  bool                         `json:"charChatbox"`
	FontOverridePaths            string                       `json:"fontPaths"`
	UserMacros                   []MacroSpec                  `json:"macros,omitempty"`
	ThemeRectOv                  map[string]map[string][4]int `json:"themeRectOverrides,omitempty"`
	ThemeRectRotations           map[string]map[string]uint8  `json:"themeRectRotations,omitempty"`
	ClassicLayout                map[string][4]float64        `json:"classicLayout,omitempty"`
	LayoutProfiles               map[string]LayoutProfile     `json:"layoutProfiles,omitempty"`
	LayoutGridPx                 int                          `json:"layoutGridPx,omitempty"`
	ClassicAnchors               map[string]ClassicAnchor     `json:"classicAnchors,omitempty"`
	ClassicRotations             map[string]uint8             `json:"classicRotations,omitempty"`
	DiskZstd                     bool                         `json:"diskZstd"`
	StreamerModeOn               bool                         `json:"streamerMode"`
	ThemeName                    string                       `json:"themeName"`
	ThemeDir                     string                       `json:"themeDir"`
	SubthemeName                 string                       `json:"subtheme"`
	OOCName                      string                       `json:"oocName"`
	ViewportPct                  int                          `json:"viewportPercent"`
	ChatScalePct                 int                          `json:"chatScalePercent"`
	ChatBoxPct                   int                          `json:"chatBoxPercent"`
	LogScalePct                  int                          `json:"logScalePercent"`
	InputHeightPct               int                          `json:"inputHeightPercent"`
	UIScalePct                   int                          `json:"uiScalePercent"`
	WindowW                      int                          `json:"windowWidth"`
	WindowH                      int                          `json:"windowHeight"`
	WindowFull                   bool                         `json:"windowFullscreen"`
	MusicVol                     int                          `json:"musicVolume"`
	SFXVol                       int                          `json:"sfxVolume"`
	BlipVol                      int                          `json:"blipVolume"`
	AlertVol                     int                          `json:"alertVolume"`
	MasterVol                    int                          `json:"masterVolume"`
	MasterVolMuted               bool                         `json:"masterVolMuted"`
	MusicVolMuted                bool                         `json:"musicVolMuted"`
	SFXVolMuted                  bool                         `json:"sfxVolMuted"`
	BlipVolMuted                 bool                         `json:"blipVolMuted"`
	HoldClearOn                  bool                         `json:"holdClearOn"`
	HoldClearKey                 string                       `json:"holdClearKey"`
	HoldClearMs                  int                          `json:"holdClearMs"`
	ExtrasBg                     string                       `json:"extrasBg"`
	ExtrasBg2                    string                       `json:"extrasBg2"`
	ExtrasBorder                 string                       `json:"extrasBorder"`
	ExtrasTitle                  string                       `json:"extrasTitle"`
	ExtrasText                   string                       `json:"extrasText"`
	ExtrasGradient               bool                         `json:"extrasGradient"`
	AreaHighlightHex             string                       `json:"areaHighlightHex,omitempty"`
	TextCrawlMs                  int                          `json:"textCrawlMs"`
	TextStayMs                   int                          `json:"textStayMs"`
	ChatRateLimitMs              int                          `json:"chatRateLimitMs"`
	MasterListURL                string                       `json:"masterListUrl"`
	AssetTypes                   map[string]AssetTypePrefs    `json:"assetTypes"`
	LearnedFormats               map[string][]string          `json:"learnedFormats"`
	Showname                     string                       `json:"showname"`
	ShownamePresets              []string                     `json:"shownamePresets,omitempty"`
	ShownameKeys                 map[string]string            `json:"shownameKeys,omitempty"`
	ICPhraseKeys                 map[string]string            `json:"icPhraseKeys,omitempty"`
	MutedSFX                     []string                     `json:"mutedSFX,omitempty"`
	SfxFavorites                 []string                     `json:"sfxFavorites,omitempty"`
	ModReasonTemplates           []string                     `json:"modReasonTemplates,omitempty"`
	ModDurations                 []string                     `json:"modDurations,omitempty"`
	BlipVols                     map[string]int               `json:"blipVolumes,omitempty"`
	EmoteFavs                    map[string][]int             `json:"emoteFavorites,omitempty"`
	EmoteFavOnly                 bool                         `json:"emoteFavOnly"`
	EmoteFavStars                bool                         `json:"emoteFavStars"`
	LocalAssetsEnabled           bool                         `json:"localAssetsEnabled"`
	LocalAssetsLayered           bool                         `json:"localAssetsLayered"`
	EmoteCaptions                bool                         `json:"emoteCaptions"`
	ViewportExactW               int                          `json:"viewportExactW"`
	OOCScalePct                  int                          `json:"oocScalePercent"`
	CustomChromeHex              [7]string                    `json:"customChrome"`
	LayoutPartHex                [4]string                    `json:"layoutPartColors"`
	BoldNamesOff                 bool                         `json:"boldNamesOff"`
	BlipRate                     int                          `json:"blipRate"`
	BlipOnSpaces                 bool                         `json:"blipOnSpaces"`
	CallwordsOOC                 bool                         `json:"callwordsOOC"`
	ExtProfiles                  map[string]string            `json:"extProfiles,omitempty"`
	LocalAssetsPaths             []string                     `json:"localAssetsPaths"`
	Favorites                    []FavoriteServer             `json:"favorites"`
	Wardrobe                     []string                     `json:"wardrobe"`
	CasingEnabled                bool                         `json:"casingEnabled"`
	CasingRoles                  int                          `json:"casingRoles"`
	HiddenPanelIDs               []string                     `json:"hiddenPanels"`
	ServerWarm                   map[string]ServerWarmInfo    `json:"serverWarm"`
	CallWordList                 []string                     `json:"callWords"`
	HotkeyMap                    map[string]string            `json:"hotkeys"`
	DiscordRPC                   DiscordPrefs                 `json:"discord"`
	MyAutoStatus                 AutoStatusPref               `json:"autoStatus"`
	ChromeThemeKey               string                       `json:"chromeTheme,omitempty"`
	ChromeShapeKey               string                       `json:"chromeShape,omitempty"`
	ChromeShapeTierIdx           int                          `json:"chromeShapeTier,omitempty"`
	CharDownloaderOn             bool                         `json:"charDownloader"`
	ToolboxSeen                  bool                         `json:"toolboxSeen"`
	ShowAssetWarnings            bool                         `json:"showAssetWarnings"`
	SpriteMoveOn                 bool                         `json:"spriteMove"`
	DeskFollowManifest           bool                         `json:"deskFollowManifest"`
	SpritePreviewOn              bool                         `json:"spritePreview"`
	PreviewHoverMs               int                          `json:"previewHoverMs"`
	PreviewHeightPxVal           int                          `json:"previewHeightPx"`
	PreviewPinned                bool                         `json:"previewPinned"`
	PreviewWinRectValid          bool                         `json:"previewWinRectValid"`
	PreviewWinRectX              int                          `json:"previewWinRectX"`
	PreviewWinRectY              int                          `json:"previewWinRectY"`
	PreviewWinRectW              int                          `json:"previewWinRectW"`
	PreviewWinRectH              int                          `json:"previewWinRectH"`
	EmoteHoverNamesOn            bool                         `json:"emoteHoverNames"`
	EmoteHoverNamesMs            int                          `json:"emoteHoverNamesMs"`
	EmoteGridGapPx               int                          `json:"emoteGridGapPx"`
	AutoLoginToast               bool                         `json:"autoLoginToast"`
	CallwordToast                bool                         `json:"callwordToast"`
	MessageCounter               bool                         `json:"messageCounter"`
	StickySounds                 bool                         `json:"stickySounds"`
	StickyEffects                bool                         `json:"stickyEffects"`
	StickyPreanims               bool                         `json:"stickyPreanims"`
	SFXOnIdle                    bool                         `json:"sfxOnIdle"`
	UnfocusedFullRate            bool                         `json:"unfocusedFullRate"`
	GhostCrawlText               bool                         `json:"ghostCrawlText"`
	MentionSelf                  bool                         `json:"mentionSelf"`
	LoopPreanim                  bool                         `json:"loopPreanim"`
	DisableAltEmoteRow           bool                         `json:"disableAltEmoteRow"`
	ICTimestamps                 bool                         `json:"icTimestamps"`
	AutoReconnect                bool                         `json:"autoReconnect"`
	AutoReconnectDefaultMigrated bool                         `json:"autoReconnectDefaultMigrated"`
	ThemeFitDefaultMigrated      bool                         `json:"themeFitDefaultMigrated"`
	BlipSpeedDefaultMigrated     bool                         `json:"blipSpeedDefaultMigrated"`
	DeskManifestDefaultMigrated  bool                         `json:"deskManifestDefaultMigrated"`
	DetailedLogDefaultMigrated   bool                         `json:"detailedLogDefaultMigrated"`
	SmoothLogScroll              bool                         `json:"smoothLogScroll"`
	MusicHistory                 bool                         `json:"musicHistory"`
	MusicStreaming               bool                         `json:"musicStreaming"`
	MusicAcrossTabs              bool                         `json:"musicAcrossTabs"`
	ShowMissingPlaceholder       bool                         `json:"showMissingPlaceholder"`
	ErrorSpriteFile              string                       `json:"errorSpritePath,omitempty"`
	MusicHosts                   []string                     `json:"musicHosts,omitempty"`
}

// Load returns the current settings snapshot. Callers must not mutate the returned
// value (it is shared) and must not retain it across time. It rebuilds from the
// inline fields on the first call after a write, so a reader sees a consistent,
// never-torn snapshot.
func (p *AssetPreferences) Load() *Settings {
	if s := p.settings.Load(); s != nil && !p.stale.Load() {
		return s
	}
	p.mu.Lock()
	if p.settings.Load() == nil || p.stale.Load() {
		p.syncSnapshot()
		p.stale.Store(false)
	}
	s := p.settings.Load()
	p.mu.Unlock()
	return s
}

// syncSnapshot seeds the snapshot from the inline data fields — the B0 bridge: the
// fields and the snapshot coexist until the last method moves off the fields.
// markDirtyCalls and snapshotRebuilds are the race-hammer v3 counters: they count the
// write-path dirty signals and the snapshot rebuilds so a hammer can assert both actually ran
// (not just that the setter calls returned).
var (
	markDirtyCalls   atomic.Int64
	snapshotRebuilds atomic.Int64
)

func (p *AssetPreferences) syncSnapshot() {
	snapshotRebuilds.Add(1)
	p.settings.Store(syncFromInline(p))
}

// Update applies f to a clone of the current snapshot, clamps the result to the same
// ranges the setters enforce, writes the changed scalar fields back into the inline data
// fields, then stores the clone as the fresh snapshot (copy-on-write) so the next Load is
// free. This is the B2 writer: setters migrate to `p.Update(func(s *Settings){ s.Field = v })`
// so the inline fields stay the single source of truth for Save until the final flip. The
// explicit (non-Update) setters still mark the snapshot stale, and this path rebuilds it.
func (p *AssetPreferences) Update(f func(*Settings)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := p.settings.Load()
	if cur == nil || p.stale.Load() {
		// an explicit setter wrote the inline fields directly and marked the snapshot
		// stale; rebuild it under the lock before cloning.
		cur = syncFromInline(p)
		p.stale.Store(false)
		p.settings.Store(cur)
	}
	s := cur.Clone()
	f(s)
	changed := changedFieldNames(cur, s)
	normalizeSettings(s)

	sv, dv := reflect.ValueOf(s).Elem(), reflect.ValueOf(p).Elem()
	for _, name := range changed {
		sf, df := sv.FieldByName(name), dv.FieldByName(name)
		if df.IsValid() && df.CanSet() && sf.IsValid() && sf.Type() == df.Type() {
			df.Set(sf)
		}
	}
	// copy-on-write: the clone becomes the fresh snapshot (no stale rebuild on the hot path).
	p.settings.Store(s)
	p.markDirty()
}

// changedFieldNames returns the Settings fields f mutated (before normalization), so only
// those are written back. Scalars, arrays and structs compare by value; maps, slices and
// pointers are skipped — the clone deep-copies them, so a plain scalar setter leaves them
// untouched, and collection-mutating closures (item 6) copy-on-write the section they touch.
func changedFieldNames(cur, s *Settings) []string {
	var out []string
	cv, sv := reflect.ValueOf(cur).Elem(), reflect.ValueOf(s).Elem()
	for i := 0; i < sv.NumField(); i++ {
		if valueChanged(sv.Field(i), cv.Field(i)) {
			out = append(out, sv.Type().Field(i).Name)
		}
	}
	return out
}

// valueChanged reports whether two reflect.Values differ, without boxing through
// interface{} (the old reflect.DeepEqual path) and without walking collections.
func valueChanged(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Bool:
		return a.Bool() != b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() != b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return a.Uint() != b.Uint()
	case reflect.Float32, reflect.Float64:
		return a.Float() != b.Float()
	case reflect.Complex64, reflect.Complex128:
		return a.Complex() != b.Complex()
	case reflect.String:
		return a.String() != b.String()
	case reflect.Array:
		for i := 0; i < a.Len(); i++ {
			if valueChanged(a.Index(i), b.Index(i)) {
				return true
			}
		}
		return false
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if valueChanged(a.Field(i), b.Field(i)) {
				return true
			}
		}
		return false
	default:
		// maps, slices, pointers, interfaces: the clone deep-copied them, so a differing
		// identity is not a change signal. Collection writes are copy-on-write in the
		// closure (item 6), not detected here.
		return false
	}
}

// normalizeSettings is the SINGLE write-path clamp implementation: it reproduces every
// range clamp the setters enforce, using the same package clamp helpers, so an assignment
// through Update normalizes exactly like the setter it will replace. normalize (load) and
// Update both reach these clamps; the one-shot migration stamps live in normalize only.
func normalizeSettings(s *Settings) {
	// masks
	s.HighlightColor &= 0xFFFFFF
	s.ICCustomColor &= 0xFFFFFF
	s.SpriteTintColor &= 0xFFFFFF
	s.MusicFlagsVal &= MusicFlagsMask

	// unconditional range clamps
	s.NameVal = clampPercent(s.NameVal, minNameColorVal, 100)
	s.RainbowSpriteSpeed = clampPercent(s.RainbowSpriteSpeed, minRainbowSpeed, maxRainbowSpeed)
	s.ReplayPlaybackSpeed = clampPercent(s.ReplayPlaybackSpeed, minReplaySpeed, maxReplaySpeed)
	s.CatchUpThreshold = clampPercent(s.CatchUpThreshold, catchUpThresholdMin, catchUpThresholdMax)
	s.MultiTabCap = clampPercent(s.MultiTabCap, minMultiTabCap, maxMultiTabCap)
	s.PreviewHoverMs = clampPercent(s.PreviewHoverMs, minPreviewHoverMs, maxPreviewHoverMs)
	s.EmoteHoverNamesMs = clampPercent(s.EmoteHoverNamesMs, minEmoteHoverNamesMs, maxEmoteHoverNamesMs)
	s.MusicVol = clampPercent(s.MusicVol, 0, defaultAudioVolume)
	s.SFXVol = clampPercent(s.SFXVol, 0, defaultAudioVolume)
	s.BlipVol = clampPercent(s.BlipVol, 0, defaultAudioVolume)
	s.AlertVol = clampPercent(s.AlertVol, 0, defaultAudioVolume)
	s.MasterVol = clampPercent(s.MasterVol, 0, defaultAudioVolume)
	s.ChatboxOpacity = clampPercent(s.ChatboxOpacity, MinChatboxOpacity, MaxChatboxOpacity)
	s.RainbowSpriteVividness = clampPercent(s.RainbowSpriteVividness, minRainbowVivid, maxRainbowVivid)
	s.NameSat = clampPercent(s.NameSat, 0, 100)
	s.TextStayMs = clampPercent(s.TextStayMs, 0, MaxTextStayMs)
	s.EmoteGridGapPx = clampPercent(s.EmoteGridGapPx, MinEmoteGridGapPx, MaxEmoteGridGapPx)
	s.BreathAmount = clampPercent(s.BreathAmount, 1, 100)
	s.BreathRate = clampPercent(s.BreathRate, 1, 100)
	s.CatchUpLingerMsVal = clampPercent(s.CatchUpLingerMsVal, 0, CatchUpLingerMaxMs)
	s.SpotlightStrength = clampPercent(s.SpotlightStrength, minSpotlightStrength, maxSpotlightStrength)
	s.SpriteWaitMsVal = clampPercent(s.SpriteWaitMsVal, SpriteWaitMinMs, SpriteWaitMaxMs)
	s.ThemeFitZoom = clampPercent(s.ThemeFitZoom, MinThemeZoom, MaxThemeZoom)
	s.OOCScalePct = clampPercent(s.OOCScalePct, MinLogScalePercent, MaxLogScalePercent)
	s.PreviewHeightPxVal = clampPercent(s.PreviewHeightPxVal, MinPreviewHeightPx, MaxPreviewHeightPx)
	s.VoiceOutVolume = clampPercent(s.VoiceOutVolume, 0, 100)
	s.WeatherDensity = clampPercent(s.WeatherDensity, 1, 100)
	s.BgSlideshowSecs = clampPercent(s.BgSlideshowSecs, minBgSlideshowSecs, maxBgSlideshowSecs)
	s.PrefetchAggro = clampPercent(s.PrefetchAggro, 1, 4)
	s.StageFrameKind = clampPercent(s.StageFrameKind, 0, stageFrameKindMax)
	if s.DownloadKBps < 0 {
		s.DownloadKBps = 0
	} else if s.DownloadKBps > maxDownloadKBps {
		s.DownloadKBps = maxDownloadKBps
	}

	// sentinel clamps (0 = unset, kept as-is)
	if s.TexBudgetMiBVal != 0 {
		s.TexBudgetMiBVal = clampPercent(s.TexBudgetMiBVal, TexBudgetMinMiB, TexBudgetMaxMiB)
	}
	if s.AnimatedBudgetMiBVal != 0 {
		s.AnimatedBudgetMiBVal = clampPercent(s.AnimatedBudgetMiBVal, AnimatedBudgetMinMiB, AnimatedBudgetMaxMiB)
	}
	if s.AdaptiveLatMultipleVal != 0 {
		s.AdaptiveLatMultipleVal = clampPercent(s.AdaptiveLatMultipleVal, AdaptiveLatMultipleMin, AdaptiveLatMultipleMax)
	}
	if s.AnimDecodeConcurrencyVal != 0 {
		s.AnimDecodeConcurrencyVal = clampPercent(s.AnimDecodeConcurrencyVal, AnimDecodeConcurrencyMin, AnimDecodeConcurrencyMax)
	}
	if s.CrossfadeMsVal != 0 {
		s.CrossfadeMsVal = clampPercent(s.CrossfadeMsVal, CrossfadeMinMs, CrossfadeMaxMs)
	}
	if s.DiskCacheBudgetMiBVal != 0 {
		s.DiskCacheBudgetMiBVal = clampPercent(s.DiskCacheBudgetMiBVal, DiskCacheBudgetMinMiB, DiskCacheBudgetMaxMiB)
	}
	if s.HoldPrevMaxAgeMsVal != 0 {
		s.HoldPrevMaxAgeMsVal = clampPercent(s.HoldPrevMaxAgeMsVal, HoldPrevMaxAgeMinMs, HoldPrevMaxAgeMaxMs)
	}
	if s.ICQueueCapVal != 0 {
		s.ICQueueCapVal = clampPercent(s.ICQueueCapVal, ICQueueCapMin, ICQueueCapMax)
	}
	if s.NotFoundTTLSecVal != 0 {
		s.NotFoundTTLSecVal = clampPercent(s.NotFoundTTLSecVal, NotFoundTTLMinSec, NotFoundTTLMaxSec)
	}
	if s.PreanimTimeoutMsVal != 0 {
		s.PreanimTimeoutMsVal = clampPercent(s.PreanimTimeoutMsVal, PreanimTimeoutMinMs, PreanimTimeoutMaxMs)
	}
	if s.ShoutDurationMsVal != 0 {
		s.ShoutDurationMsVal = clampPercent(s.ShoutDurationMsVal, ShoutDurationMinMs, ShoutDurationMaxMs)
	}
	if s.SpriteDownscalePctVal != 0 {
		s.SpriteDownscalePctVal = clampPercent(s.SpriteDownscalePctVal, SpriteDownscaleMinPct, SpriteDownscaleMaxPct)
	}
	if s.ThumbBudgetMiBVal != 0 {
		s.ThumbBudgetMiBVal = clampPercent(s.ThumbBudgetMiBVal, ThumbBudgetMinMiB, ThumbBudgetMaxMiB)
	}
	if s.ThumbHeightPxVal != 0 {
		s.ThumbHeightPxVal = clampPercent(s.ThumbHeightPxVal, ThumbHeightMinPx, ThumbHeightMaxPx)
	}
	if s.ThumbQualityVal != 0 {
		s.ThumbQualityVal = clampPercent(s.ThumbQualityVal, ThumbQualityMin, ThumbQualityMax)
	}

	// explicit-zero keeps the default (the getter resolves 0)
	if s.ViewportPct != 0 {
		s.ViewportPct = clampPercent(s.ViewportPct, MinViewportPercent, MaxViewportPercent)
	}
	if s.ChatScalePct != 0 {
		s.ChatScalePct = clampPercent(s.ChatScalePct, MinChatScalePercent, MaxChatScalePercent)
	}
	if s.ChatBoxPct != 0 {
		s.ChatBoxPct = clampPercent(s.ChatBoxPct, MinChatBoxPercent, MaxChatBoxPercent)
	}
	if s.LogScalePct != 0 {
		s.LogScalePct = clampPercent(s.LogScalePct, MinLogScalePercent, MaxLogScalePercent)
	}
	if s.InputHeightPct != 0 {
		s.InputHeightPct = clampPercent(s.InputHeightPct, MinInputPercent, MaxInputPercent)
	}
	if s.UIScalePct != 0 {
		s.UIScalePct = clampPercent(s.UIScalePct, MinUIScalePercent, MaxUIScalePercent)
	}
	if s.HoldClearMs != 0 {
		s.HoldClearMs = clampPercent(s.HoldClearMs, MinHoldClearMs, MaxHoldClearMs)
	}
	if s.TextCrawlMs != 0 {
		s.TextCrawlMs = clampPercent(s.TextCrawlMs, MinTextCrawlMs, MaxTextCrawlMs)
	}
	if s.BlipRate != 0 {
		s.BlipRate = clampPercent(s.BlipRate, MinBlipRate, MaxBlipRate)
	}

	// custom clamp helpers (the setters call these same functions)
	s.FPSCapVal = normalizeFPSPref(s.FPSCapVal, FPSCapMin, FPSCapMax)
	s.IdleFPSVal = normalizeFPSPref(s.IdleFPSVal, IdleFPSMin, IdleFPSMax)
	s.UnfocusedFPSVal = normalizeFPSPref(s.UnfocusedFPSVal, UnfocusedFPSMin, UnfocusedFPSMax)
	s.TimerSeconds = clampTimerSeconds(s.TimerSeconds)
	s.InstantReplaySeconds = clampInstantReplaySeconds(s.InstantReplaySeconds)
	s.LayoutGridPx = clampLayoutGridPx(s.LayoutGridPx)
	s.ViewportExactW = clampViewportExactPx(s.ViewportExactW)
	s.SpriteScaling = ClampSpriteScaling(s.SpriteScaling)

	// enums with a valid range (invalid keeps the shipped default)
	if s.ProxyModeVal < 0 || s.ProxyModeVal >= ProxyModeCount {
		s.ProxyModeVal = defaultPrefs("").ProxyModeVal
	}
	if s.ICLogDepthVal < 0 {
		s.ICLogDepthVal = ICLogDepthUnlimited
	} else if s.ICLogDepthVal != 0 {
		s.ICLogDepthVal = clampPercent(s.ICLogDepthVal, ICLogDepthMin, ICLogDepthMax)
	}
	if s.InputGraceFramesVal <= 0 {
		s.InputGraceFramesVal = InputGraceOff
	} else {
		s.InputGraceFramesVal = clampPercent(s.InputGraceFramesVal, InputGraceFramesMin, InputGraceFramesMax)
	}
}
