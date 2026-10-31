package ui

import (
	"context"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Remote-speaker char.ini metadata. The wire carries a blip name only from 2.10.2+ senders and never the chat skin,
// so both come from the speaker's char.ini (as webAO does): a per-URL cache filled by one async fetch per character,
// answered lock-free from the courtroom's begin() via BlipNameFor / ChatSkinFor. Keys are full char.ini URLs.

const (
	// charMetaCap bounds the cache; past it the map resets and refetching heals.
	charMetaCap = 256
	// charMetaResCap sizes the async result channel; senders never block, so a burst beyond it waits.
	charMetaResCap = 64
	// charMetaRefetchAfter is how old an in-flight (done=false) marker must be before charMetaFor re-arms its fetch;
	// a result dropped on channel overflow would otherwise leave done=false forever.
	charMetaRefetchAfter = 5 * time.Second
)

// charMeta is the slice of a remote char.ini the live courtroom needs.
type charMeta struct {
	blips string // [Options] blips / legacy gender ("" = none declared)
	chat  string // [Options] chat — the misc chatbox-skin folder ("" = none)
	// chatMsgRGB / chatNameRGB are the speaker's own default text colour (chat_config.ini c0) and showname colour
	// (courtroom_fonts.ini showname_color), fetched after the char.ini when chat != "". Has flags false means absent
	// or not yet fetched, and the theme's colours win.
	chatMsgRGB  theme.RGB
	chatMsgHas  bool
	chatNameRGB theme.RGB
	chatNameHas bool
	// effects is [Options] effects, the misc screen-effect folder (misc/<effects>/effects.ini). It is also the
	// fallback folder for the 1- and 2-part MS effect fields, which omit it (AO2 text_file_functions.cpp:836-839).
	effects string
	// realization is [Options] realization, the character's own realization sound name. It overrides the effects.ini
	// sound of the realization effect (text_file_functions.cpp:889-892) and plays for legacy REALIZATION=1
	// (courtroom.cpp:4175).
	realization string
	// scaling is [Options] scaling, the texture filter this character asks for (get_scaling); ScalingAuto until set.
	scaling courtroom.ScalingMode
	// showname is get_showname's answer: [Options] showname, the folder name, or "" for needs_showname=false
	// (courtroom.ShownameOrFolder). Empty is a legitimate answer, so check done, not showname != "".
	showname string
	// idle is courtroom.CharINI.IdleAnim: the first [Emotions] anim, the pose AO2 selects when the character is picked.
	// A preview of another player mints its sprite from it, since "normal" is a convention many packs do not keep.
	// Empty until the fetch lands or when the ini has no emotes.
	idle string
	done bool // fetch settled (hit or miss) — misses cache too (no refetch loop)
	// stamp is when an in-flight marker (done=false) was armed; charMetaFor re-arms a fetch once it goes stale.
	// Zero for settled entries.
	stamp time.Time
}

type charMetaFetch struct {
	url         string
	blips       string
	chat        string
	effects     string
	realization string
	showname    string
	idle        string
	scaling     courtroom.ScalingMode
	chatMsgRGB  theme.RGB
	chatMsgHas  bool
	chatNameRGB theme.RGB
	chatNameHas bool
}

// charMetaFor answers from the cache and fires one async fetch on a miss; the zero meta until it lands.
func (a *App) charMetaFor(char string) charMeta {
	char = strings.TrimSpace(char)
	if char == "" || a.urls.Origin() == "" {
		return charMeta{}
	}
	url := a.charINIURL(char)
	if m, ok := a.charMetaCache[url]; ok {
		// A settled entry is final. A stale in-flight marker lost its result on overflow: re-arm the fetch.
		if m.done || time.Since(m.stamp) < charMetaRefetchAfter {
			return m
		}
	}
	if a.charMetaCache == nil {
		a.charMetaCache = make(map[string]charMeta, charMetaCap)
	}
	if len(a.charMetaCache) >= charMetaCap {
		a.charMetaCache = make(map[string]charMeta, charMetaCap) // reset: it's a cache, refetch heals
	}
	a.charMetaCache[url] = charMeta{stamp: time.Now()} // in-flight marker: one fetch per URL
	a.charMetaFetchOne(url, char)
	return charMeta{}
}

// charMetaFetchOne downloads and parses one char.ini off-thread and posts the result without blocking.
//
// char is the folder the url was minted from, get_showname's fallback, resolved here so the cache holds the
// finished answer.
func (a *App) charMetaFetchOne(url, char string) {
	if a.charMetaRes == nil {
		a.charMetaRes = make(chan charMetaFetch, charMetaResCap)
	}
	// Capture the Manager and result channel before the goroutine: a test or torn-down App has no Manager, and the
	// goroutine must not read a.d fields that can be nil or reassigned after it starts.
	mgr := a.d.Manager
	if mgr == nil {
		return
	}
	ub := a.urls // captured too: the goroutine must not read a.urls after spawn
	resCh := a.charMetaRes
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		res := charMetaFetch{url: url, showname: char} // no ini ⇒ get_showname's unreadable-ini answer
		// Layered: a user testing their own character from a mounted folder must see the ini they are editing.
		if data, err := mgr.FetchRawLayered(ctx, url); err == nil {
			if ini, err := courtroom.ParseCharINI(data); err == nil && ini != nil {
				res.blips = strings.TrimSpace(ini.Blips)
				res.chat = strings.TrimSpace(ini.Chat)
				res.effects = strings.TrimSpace(ini.Effects)
				res.realization = strings.TrimSpace(ini.Realization)
				res.scaling = ini.ScalingMode()
				res.showname = ini.ShownameOrFolder(char) // AO2 get_showname, the ONE copy
				res.idle = ini.IdleAnim()                 // emote 1's anim — the ONE copy of "their resting pose"
				// The speaker's own chatbox ink: default text colour (chat_config.ini c0) and showname colour
				// (courtroom_fonts.ini showname_color), each tried down its casing chain.
				if res.chat != "" {
					for _, u := range ub.ChatboxChatConfigIni(res.chat) {
						if data, err := mgr.FetchRawLayered(ctx, u); err == nil {
							if rgb, ok := courtroom.ParseChatboxDefaultColor(data); ok {
								res.chatMsgRGB, res.chatMsgHas = rgb, true
							}
							break
						}
					}
					for _, u := range ub.ChatboxFontsIni(res.chat) {
						if data, err := mgr.FetchRawLayered(ctx, u); err == nil {
							showname, nameOK, message, msgOK := courtroom.ParseChatboxFontsIni(data)
							if nameOK {
								res.chatNameRGB, res.chatNameHas = showname, true
							}
							if msgOK && !res.chatMsgHas {
								res.chatMsgRGB, res.chatMsgHas = message, true
							}
							break
						}
					}
				}
			}
		}
		select {
		case resCh <- res:
		default: // burst overflow: drop — the miss marker stays, a later session refetches
		}
	}()
}

// pollCharMeta drains landed char.ini fetches into the cache once per frame.
func (a *App) pollCharMeta() {
	if a.charMetaRes == nil {
		return
	}
	for {
		select {
		case res := <-a.charMetaRes:
			if a.charMetaCache == nil {
				a.charMetaCache = make(map[string]charMeta, charMetaCap)
			}
			a.charMetaCache[res.url] = charMeta{
				blips: res.blips, chat: res.chat, effects: res.effects,
				realization: res.realization, scaling: res.scaling,
				showname: res.showname, idle: res.idle, done: true,
				chatMsgRGB: res.chatMsgRGB, chatMsgHas: res.chatMsgHas,
				chatNameRGB: res.chatNameRGB, chatNameHas: res.chatNameHas,
			}
			// Our own character's effects folder may have just arrived: drop the picker's per-frame memos.
			a.overlayInvalidateMemos()
		default:
			return
		}
	}
}

// remoteBlipFor is the courtroom's BlipNameFor callback: the speaker's blip set, and whether the fetch has settled.
// known=false makes the courtroom hold the blip rather than default to "male".
func (a *App) remoteBlipFor(char string) (string, bool) {
	m := a.charMetaFor(char)
	return m.blips, m.done
}

// remoteChatSkinFor is the courtroom's ChatSkinFor callback, gated on the pref so skins off stops misc art fetches.
func (a *App) remoteChatSkinFor(char string) string {
	if !a.d.Prefs.Load().CharChatbox {
		return ""
	}
	return a.charMetaFor(char).chat
}

// remoteChatInkFor is the courtroom's ChatInkFor callback: the speaker's chatbox message and showname colours, from
// the same char.ini fetch plus one follow-up of the chatbox's courtroom_fonts.ini.
func (a *App) remoteChatInkFor(char string) (theme.RGB, bool, theme.RGB, bool) {
	m := a.charMetaFor(char)
	return m.chatMsgRGB, m.chatMsgHas, m.chatNameRGB, m.chatNameHas
}

// remoteIniShownameFor is the courtroom's IniShownameFor callback: AO2's get_showname for a speaker.
//
// known is done, never showname != "": an empty answer is a character with needs_showname=false that wants a
// blank plate, and reading it as not fetched would put the folder name back.
func (a *App) remoteIniShownameFor(char string) (string, bool) {
	m := a.charMetaFor(char)
	return m.showname, m.done
}

// roomBlipResolver implements courtroom.BlipResolver from the prefs (SFX mute, blip volume) and the char.ini cache.
type roomBlipResolver struct{ a *App }

func (r roomBlipResolver) SFXMuted(name string) bool { return r.a.d.Prefs.Load().IsSFXMuted(name) }
func (r roomBlipResolver) BlipVolumeFor(char string) int {
	return r.a.d.Prefs.Load().BlipVolumeFor(char)
}
func (r roomBlipResolver) BlipNameFor(char string) (string, bool) {
	return r.a.remoteBlipFor(char)
}

// roomCharResolver implements courtroom.CharResolver from the char.ini cache (skin, ink, showname, realization).
type roomCharResolver struct{ a *App }

func (r roomCharResolver) ChatSkinFor(char string) string { return r.a.remoteChatSkinFor(char) }
func (r roomCharResolver) ChatInkFor(char string) (theme.RGB, bool, theme.RGB, bool) {
	return r.a.remoteChatInkFor(char)
}
func (r roomCharResolver) IniShownameFor(char string) (string, bool) {
	return r.a.remoteIniShownameFor(char)
}
func (r roomCharResolver) CustomRealizationFor(char string) string {
	return r.a.overlayCustomRealizationFor(char)
}

// roomAssetResolver implements courtroom.AssetResolver from the render/audio stores and the char.ini cache: effects
// folder, overlay resolve, residency probes and the per-character scaling filter.
type roomAssetResolver struct{ a *App }

func (r roomAssetResolver) EffectsFolderFor(char string) string {
	return r.a.overlayEffectsFolderFor(char)
}
func (r roomAssetResolver) OverlayFor(name, folder string) (courtroom.OverlayResolved, bool) {
	return r.a.overlayResolve(name, folder)
}
func (r roomAssetResolver) SpriteReady(base string) bool { return r.a.d.Store.Contains(base) }
func (r roomAssetResolver) AudioReady(base string) bool  { return r.a.d.Audio.HasChunk(base) }
func (r roomAssetResolver) SpriteScaling(charName string) courtroom.ScalingMode {
	return r.a.remoteScalingFor(charName)
}

// envResolver implements courtroom.EnvResolver from the App: our side (pos dropdown or per-tab override), the
// :shortcode: registry, and a no-op preanim-end stream (the App reads LastPreanimEnd instead).
type envResolver struct {
	a    *App
	side func() string // nil = a.mySide
}

func (r envResolver) LocalSide() string {
	if r.side != nil {
		return r.side()
	}
	return r.a.mySide()
}
func (r envResolver) InlineEmote(stem string) (string, bool)  { return inlineEmoteFor(stem) }
func (r envResolver) OnPreanimEnd(courtroom.PreanimEndReason) {}

// wireRoomCharMeta attaches the char.ini lookups, asset/overlay engine and local env to a room, so a room behaves
// identically in every mode. Its one caller is App.newRoom (newroom.go); construction and wiring are one step.
func (a *App) wireRoomCharMeta(room *courtroom.Courtroom) {
	if room == nil {
		return
	}
	room.Blips = roomBlipResolver{a}
	room.Chars = roomCharResolver{a}
	room.Assets = roomAssetResolver{a}
	room.Env = envResolver{a: a}
}

// iniIdleAnimFor answers what pose a character stands in: its first [Emotions] anim (courtroom.CharINI.IdleAnim),
// or fallback only while the char.ini has not landed or names no emotes.
//
// The rule is "convention until the ini disagrees": a preview must draw something on the frame the cursor arrives,
// and the optimistic "normal" probe fills most packs in one request. It lives here so call sites do not spell a
// bare literal, which minted (a)normal for characters with no such sprite.
//
// It returns a name, not a URL: the caller mints it through urls.Emote + urls.EmoteAlts and PrefetchChain, which
// know AO2-Client's two spellings ((a)X and bare X, CharLayer::load_image).
func (a *App) iniIdleAnimFor(char, fallback string) string {
	if idle := a.charMetaFor(char).idle; idle != "" {
		return idle
	}
	return fallback
}

// remoteScalingFor is the courtroom's SpriteScaling callback: the speaker's char.ini [Options] scaling= request,
// from the same cache and fetch as blips and skins.
//
// Auto until the fetch lands; the renderer's geometry rule covers the gap.
func (a *App) remoteScalingFor(char string) courtroom.ScalingMode {
	return a.charMetaFor(char).scaling
}
