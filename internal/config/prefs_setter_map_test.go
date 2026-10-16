package config

import (
	"math"
	"reflect"
	"strings"
)

// setterField maps a setter to the Settings field it writes, plus an invert flag for
// the inverse setters (field = !arg). This is the B2-migration oracle: every migrated
// setter (deleted from AssetPreferences) is reproduced through Update + this map, so
// the FROZEN setter golden still proves behaviour-identity after the setter's method
// is gone. The invert entries are deliberate, reviewed facts — a naive setter->field
// mapping without them silently flips SetBoldNames / SetBreathBob / SetBreathScale.
type setterField struct {
	field  string
	invert bool
}

var setterFieldMap = map[string]setterField{
	"SetAdaptiveLatMultiple":   {field: "AdaptiveLatMultipleVal"},
	"SetAdditiveText":          {field: "AdditiveText"},
	"SetAlertVolume":           {field: "AlertVol"},
	"SetAnimatedBudgetMiB":     {field: "AnimatedBudgetMiBVal"},
	"SetAnimateEntrances":      {field: "AnimateEntrances"},
	"SetAnimationsEnabled":     {field: "PreferAnimated"},
	"SetAnimDecodeConcurrency": {field: "AnimDecodeConcurrencyVal"},
	"SetAssetWarnings":         {field: "ShowAssetWarnings"},
	"SetAutoClipModcall":       {field: "AutoClipModcall"},
	"SetAutoConnectOnLaunch":   {field: "AutoConnectOnLaunch"},
	"SetAutoLoginToast":        {field: "AutoLoginToast"},
	"SetAutoReconnect":         {field: "AutoReconnect"},
	"SetBgSlideshow":           {field: "BgSlideshow"},
	"SetBgSlideshowSeconds":    {field: "BgSlideshowSecs"},
	"SetBlipVolMuted":          {field: "BlipVolMuted"},
	"SetBreathBob":             {field: "BreathNoBob", invert: true},
	"SetBreathScale":           {field: "BreathNoScale", invert: true},
	"SetCallwordOSToast":       {field: "CallwordOSToast"},
	"SetCallwordsOOC":          {field: "CallwordsOOC"},
	"SetCallwordToast":         {field: "CallwordToast"},
	"SetCatchUpLingerMs":       {field: "CatchUpLingerMsVal"},
	"SetCharChatbox":           {field: "CharChatbox"},
	"SetCharDownloader":        {field: "CharDownloaderOn"},
	"SetChatboxOpacity":        {field: "ChatboxOpacity"},
	"SetChatboxTint":           {field: "ChatboxTint"},
	"SetClipSpritesToStage":    {field: "ClipSpritesToStage"},
	"SetCrossfadeMs":           {field: "CrossfadeMsVal"},
	"SetDebugOverlay":          {field: "DebugOverlay"},
	"SetDepthOfField":          {field: "DepthOfField"},
	"SetDeskFollowManifest":    {field: "DeskFollowManifest"},
	"SetDetailedLog":           {field: "DetailedLog"},
	"SetDisableAltEmoteRow":    {field: "DisableAltEmoteRow"},
	"SetDisableEffects":        {field: "DisableEffects"},
	"SetDiskCacheBudgetMiB":    {field: "DiskCacheBudgetMiBVal"},
	"SetDiskZstd":              {field: "DiskZstd"},
	"SetDNDPersist":            {field: "DNDPersist"},
	"SetDNDSaved":              {field: "DNDSaved"},
	"SetDownloadCapKBps":       {field: "DownloadKBps"},
	"SetDragLayout":            {field: "DragLayout"},
	"SetDyslexiaFont":          {field: "DyslexiaFont"},
	"SetEmoteButtonImages":     {field: "EmoteButtonImages"},
	"SetEmoteCaptions":         {field: "EmoteCaptions"},
	"SetEmoteFavOnly":          {field: "EmoteFavOnly"},
	"SetEmoteFavStars":         {field: "EmoteFavStars"},
	"SetEmoteGridGap":          {field: "EmoteGridGapPx"},
	"SetEmoteHoverNames":       {field: "EmoteHoverNamesOn"},
	"SetEmoteHoverNamesMs":     {field: "EmoteHoverNamesMs"},
	"SetEventDrivenLoop":       {field: "EventDrivenLoop"},
	"SetFavEmoteBox":           {field: "FavEmoteBox"},
	"SetFollowEnabled":         {field: "FollowEnabled"},
	"SetFontCensus":            {field: "FontCensus"},
	"SetFontEverywhere":        {field: "FontEverywhere"},
	"SetForceCharNames":        {field: "ForceCharNames"},
	"SetFormatAutoDetect":      {field: "AutoDetectFormats"},
	"SetFrameLimiterDisabled":  {field: "DisableFrameLimiter"},
	"SetFriendGlowPulse":       {field: "FriendGlowPulse"},
	"SetFriendHighlight":       {field: "FriendHighlight"},
	"SetFriendNotify":          {field: "FriendNotify"},
	"SetFriendOSToast":         {field: "FriendOSToast"},
	"SetFriendSound":           {field: "FriendSound"},
	"SetGhostCrawlText":        {field: "GhostCrawlText"},
	"SetGroupChatButton":       {field: "GroupChatButton"},
	"SetHideDesk":              {field: "HideDesk"},
	"SetHideReactions":         {field: "HideReactions"},
	"SetHideSpriteStyles":      {field: "HideSpriteStyles"},
	"SetHighlightColor":        {field: "HighlightColor"},
	"SetHoldClearOn":           {field: "HoldClearOn"},
	"SetHoldDebugTint":         {field: "HoldDebugTint"},
	"SetHoldPrevMaxAgeMs":      {field: "HoldPrevMaxAgeMsVal"},
	"SetHotkeysEnabled":        {field: "HotkeysEnabled"},
	"SetICCustomColor":         {field: "ICCustomColor"},
	"SetICLogDepth":            {field: "ICLogDepthVal"},
	"SetICQueueCap":            {field: "ICQueueCapVal"},
	"SetICTimestamps":          {field: "ICTimestamps"},
	"SetInstantDisconnect":     {field: "InstantDisconnect"},
	"SetInstantReplay":         {field: "InstantReplay"},
	"SetLayoutGridSize":        {field: "LayoutGridPx"},
	"SetLegacyDevTheme":        {field: "LegacyDevTheme"},
	"SetLoopPreanim":           {field: "LoopPreanim"},
	"SetMasterVolMuted":        {field: "MasterVolMuted"},
	"SetMasterVolume":          {field: "MasterVol"},
	"SetMentionSelf":           {field: "MentionSelf"},
	"SetMessageCounter":        {field: "MessageCounter"},
	"SetModcallToast":          {field: "ModcallToast"},
	"SetMotionRedrawPerEvent":  {field: "MotionRedrawPerEvent"},
	"SetMusicAcrossTabs":       {field: "MusicAcrossTabs"},
	"SetMusicDucking":          {field: "MusicDuckingOn"},
	"SetMusicFlags":            {field: "MusicFlagsVal"},
	"SetMusicHistory":          {field: "MusicHistory"},
	"SetMusicStreaming":        {field: "MusicStreaming"},
	"SetMusicVolMode":          {field: "MusicVolMode"},
	"SetMusicVolMuted":         {field: "MusicVolMuted"},
	"SetNameColors":            {field: "NameColors"},
	"SetNotFoundTTLSec":        {field: "NotFoundTTLSecVal"},
	"SetNotifyOnOOC":           {field: "NotifyOnOOC"},
	"SetOOCInLogTab":           {field: "OOCInLogTab"},
	"SetOOCScale":              {field: "OOCScalePct"},
	"SetPerAreaScrollback":     {field: "PerAreaScroll"},
	"SetBoldNames":             {field: "BoldNamesOff", invert: true},
}

// scenarioArg reconstructs the edge-input for a golden scenario from the FIELD type.
// For the migrated single-field setters the field type equals the setter's arg type,
// and the scenario labels mirror setterScenarios (zero/valid/neg/huge/empty/nil).
func scenarioArg(t reflect.Type, label string) reflect.Value {
	switch label {
	case "valid":
		return validOf(t)
	case "neg":
		if t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64 {
			return reflect.ValueOf(float64(-1)).Convert(t)
		}
		return reflect.ValueOf(int64(-1)).Convert(t)
	case "huge":
		switch t.Kind() {
		case reflect.Float32, reflect.Float64:
			return reflect.ValueOf(float64(math.MaxFloat32)).Convert(t)
		case reflect.String:
			return reflect.ValueOf(strings.Repeat("x", 4096))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return reflect.ValueOf(uint64(math.MaxInt32)).Convert(t)
		default:
			return reflect.ValueOf(int64(math.MaxInt32)).Convert(t)
		}
	case "empty":
		return reflect.ValueOf("")
	default: // "zero", "nil"
		return reflect.Zero(t)
	}
}
