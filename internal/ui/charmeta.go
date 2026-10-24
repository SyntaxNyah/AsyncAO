package ui

import (
	"context"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Remote-speaker char.ini metadata (playtest: characters' own blip sets and
// chatbox skins were ignored for other players). The wire only carries a blip
// name from 2.10.2+ senders, and the chat skin never rides the wire at all —
// webAO resolves both from the SPEAKER's char.ini, and so do we now: a small
// per-URL cache filled by one async fetch per character, answered lock-free
// from the courtroom's begin() via the BlipNameFor / ChatSkinFor callbacks.

const (
	// charMetaCap bounds the cache (rule §17.4). A busy server list tops out
	// far below this; past it the map resets (it's a cache — refetch heals).
	charMetaCap = 256
	// charMetaResCap sizes the async result channel; a burst beyond it just
	// leaves later results to the next drain (senders never block the fetch
	// goroutine — see charMetaFetchOne's non-blocking send). Sized large enough
	// that a busy room's first-message burst rarely overflows (#69).
	charMetaResCap = 64
	// charMetaRefetchAfter is how old an in-flight (done=false) marker must be
	// before charMetaFor re-arms a fetch for it. A result dropped on channel
	// overflow leaves done=false forever otherwise; this heals that drop on the
	// character's next message without ever spawning an unbounded fetch loop.
	charMetaRefetchAfter = 5 * time.Second
)

// charMeta is the slice of a remote char.ini the live courtroom needs.
type charMeta struct {
	blips string // [Options] blips / legacy gender ("" = none declared)
	chat  string // [Options] chat — the misc chatbox-skin folder ("" = none)
	// chatMsgRGB / chatNameRGB are the speaker's own chatbox ink: the default text
	// colour (chat_config.ini c0) and the showname colour (courtroom_fonts.ini
	// showname_color), fetched as a follow-up to the char.ini when chat != "". Has
	// flags false = absent or not yet fetched → the theme's colours win.
	chatMsgRGB  theme.RGB
	chatMsgHas  bool
	chatNameRGB theme.RGB
	chatNameHas bool
	// effects is [Options] effects — the misc SCREEN-EFFECT folder
	// (misc/<effects>/effects.ini + art). It rides this same single fetch, so a
	// character's own effect pack costs no extra network work; it is also the
	// fallback folder for the 1- and 2-part MS effect fields, which omit it
	// (AO2 text_file_functions.cpp:836-839).
	effects string
	// realization is [Options] realization — the character's OWN realization sound
	// NAME. Canon overrides the effects.ini `sound` of the "realization" effect
	// with it outright (text_file_functions.cpp:889-892) and plays it for the
	// legacy REALIZATION=1 path (courtroom.cpp:4175). Rides this same single fetch.
	realization string
	// scaling is [Options] scaling — the texture filter this character asks for
	// (get_scaling). ScalingAuto until the fetch lands and when none is declared.
	scaling courtroom.ScalingMode
	// showname is get_showname's answer for this character — the char.ini
	// [Options] showname, or the folder name, or "" for needs_showname=false
	// (courtroom.ShownameOrFolder). Only meaningful once done: an EMPTY string
	// is a legitimate answer, so `done` and not `showname != ""` is what tells
	// the name chain whether rung 2 has one.
	showname string
	// idle is courtroom.CharINI.IdleAnim: the FIRST [Emotions] entry's anim, i.e.
	// the pose AO2 has selected the instant this character is picked. It is what a
	// PREVIEW of somebody else must mint their sprite from, because "normal" is a
	// convention plenty of packs do not keep. Empty until the fetch lands and for
	// an ini with no emotes — an absent answer, so the reader keeps its optimistic
	// convention for that window rather than drawing nothing.
	idle string
	done bool // fetch settled (hit or miss) — misses cache too (no refetch loop)
	// stamp is when an IN-FLIGHT marker (done=false) was armed. A dropped async
	// result leaves done=false forever; charMetaFor re-arms a fetch once the
	// marker goes stale so a dropped char.ini heals on the next message (#69).
	// Zero for settled entries, where `done` already decides.
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

// charMetaFor answers from the cache and fires ONE async fetch on a miss.
// Returns the zero meta until the fetch lands — the speaker's NEXT message
// picks it up (the same streaming shape as sprites and profiles).
func (a *App) charMetaFor(char string) charMeta {
	char = strings.TrimSpace(char)
	if char == "" || a.link.urls.Origin() == "" {
		return charMeta{}
	}
	url := a.charINIURL(char)
	if m, ok := a.overlays.charMetaCache[url]; ok {
		// A settled entry (hit or miss) is final. An in-flight marker that has
		// gone stale means its result was dropped on channel overflow (#69):
		// fall through and re-arm the fetch instead of returning a marker that
		// would hold the character's blips/skin/effects forever.
		if m.done || time.Since(m.stamp) < charMetaRefetchAfter {
			return m
		}
	}
	if a.overlays.charMetaCache == nil {
		a.overlays.charMetaCache = make(map[string]charMeta, charMetaCap)
	}
	if len(a.overlays.charMetaCache) >= charMetaCap {
		a.overlays.charMetaCache = make(map[string]charMeta, charMetaCap) // reset: it's a cache, refetch heals
	}
	a.overlays.charMetaCache[url] = charMeta{stamp: time.Now()} // in-flight marker: one fetch per URL
	a.charMetaFetchOne(url, char)
	return charMeta{}
}

// charMetaFetchOne downloads + parses one char.ini off-thread and posts the
// result (never blocking; a dropped result refetches next session).
func (a *App) charMetaFetchOne(url, char string) {
	if a.overlays.charMetaRes == nil {
		a.overlays.charMetaRes = make(chan charMetaFetch, charMetaResCap)
	}
	// Capture the Manager and the result channel BEFORE the goroutine. A test App
	// (or a torn-down one) has no Manager, and the goroutine must not read a.d
	// fields that can be nil or reassigned after it is spawned — reading a nil
	// *Manager on the goroutine is the #69 CI panic (nil deref in FetchRawLayered).
	mgr := a.d.Manager
	if mgr == nil {
		return
	}
	ub := a.link.urls // captured too: the goroutine must not read a.urls after spawn
	resCh := a.overlays.charMetaRes
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		res := charMetaFetch{url: url, showname: char} // no ini ⇒ get_showname's unreadable-ini answer
		// LAYERED: this is the whole of a speaker's declared identity — showname,
		// blips, chatbox skin, effects, scaling, idle pose. A user testing their own
		// character out of a mounted folder must see the ini they are editing (#72).
		if data, err := mgr.FetchRawLayered(ctx, url); err == nil {
			if ini, err := courtroom.ParseCharINI(data); err == nil && ini != nil {
				res.blips = strings.TrimSpace(ini.Blips)
				res.chat = strings.TrimSpace(ini.Chat)
				res.effects = strings.TrimSpace(ini.Effects)
				res.realization = strings.TrimSpace(ini.Realization)
				res.scaling = ini.ScalingMode()
				res.showname = ini.ShownameOrFolder(char) // AO2 get_showname, the ONE copy
				res.idle = ini.IdleAnim()                 // emote 1's anim — the ONE copy of "their resting pose"
				// The speaker's own chatbox ink: the default text colour
				// (chat_config.ini c0) and the showname colour (courtroom_fonts.ini
				// showname_color), each tried down its casing chain until one resolves.
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

// pollCharMeta drains landed char.ini fetches into the cache (called once per
// frame from the poll cluster; no-op with nothing pending).
func (overlay *overlayState) pollCharMeta() {
	if overlay.charMetaRes == nil {
		return
	}
	for {
		select {
		case res := <-overlay.charMetaRes:
			if overlay.charMetaCache == nil {
				overlay.charMetaCache = make(map[string]charMeta, charMetaCap)
			}
			overlay.charMetaCache[res.url] = charMeta{
				blips: res.blips, chat: res.chat, effects: res.effects,
				realization: res.realization, scaling: res.scaling,
				showname: res.showname, idle: res.idle, done: true,
				chatMsgRGB: res.chatMsgRGB, chatMsgHas: res.chatMsgHas,
				chatNameRGB: res.chatNameRGB, chatNameHas: res.chatNameHas,
			}
			// Our own character's effects folder may have just arrived: drop the
			// picker's per-frame memos so the roster is re-resolved for it.
			overlay.overlayInvalidateMemos()
		default:
			return
		}
	}
}

// remoteBlipFor is the courtroom's BlipNameFor callback: the speaker's char.ini
// blip set, plus whether the fetch has settled. known=false (still in flight)
// makes the courtroom hold the blip rather than default to "male", so the
// first message a character sends already blips with their own set.
func (a *App) remoteBlipFor(char string) (string, bool) {
	m := a.charMetaFor(char)
	return m.blips, m.done
}

// remoteChatSkinFor is the courtroom's ChatSkinFor callback — gated on the
// pref so turning skins off also stops the misc art fetches.
func (a *App) remoteChatSkinFor(char string) string {
	if !a.d.Prefs.Load().CharChatbox {
		return ""
	}
	return a.charMetaFor(char).chat
}

// remoteChatInkFor is the courtroom's ChatInkFor callback: the speaker's own
// chatbox message/showname colours, off the SAME single char.ini fetch as the
// skin (plus one follow-up fetch of the chatbox's courtroom_fonts.ini).
func (a *App) remoteChatInkFor(char string) (theme.RGB, bool, theme.RGB, bool) {
	m := a.charMetaFor(char)
	return m.chatMsgRGB, m.chatMsgHas, m.chatNameRGB, m.chatNameHas
}

// remoteIniShownameFor is the courtroom's IniShownameFor callback — AO2's
// get_showname for a speaker, off the SAME single char.ini fetch as blips,
// skins, effects and scaling.
func (a *App) remoteIniShownameFor(char string) (string, bool) {
	m := a.charMetaFor(char)
	return m.showname, m.done
}

// roomBlipResolver implements courtroom.BlipResolver from the App's prefs (per-SFX
// mute, per-character blip volume) and the per-URL char.ini cache (blip set), so a
// speaker blips identically in every mode.
type roomBlipResolver struct{ a *App }

func (r roomBlipResolver) SFXMuted(name string) bool { return r.a.d.Prefs.Load().IsSFXMuted(name) }
func (r roomBlipResolver) BlipVolumeFor(char string) int {
	return r.a.d.Prefs.Load().BlipVolumeFor(char)
}
func (r roomBlipResolver) BlipNameFor(char string) (string, bool) {
	return r.a.remoteBlipFor(char)
}

// roomCharResolver implements courtroom.CharResolver from the App's per-URL
// char.ini cache (chatbox skin/ink, custom showname, custom realization), so a
// speaker skins and names identically in every mode.
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

// roomAssetResolver implements courtroom.AssetResolver from the render/audio stores
// and the per-URL char.ini cache: a speaker's effects folder, the overlay engine's
// resolve, texture/audio residency probes and the per-character scaling filter.
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

// envResolver implements courtroom.EnvResolver from the App: our side (the pos
// dropdown, or a per-tab override for the split pane), the :shortcode: registry, and
// a no-op preanim-end stream (the App reads LastPreanimEnd instead of streaming).
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

// wireRoomCharMeta attaches every consumer-defined surface to a room — the
// char.ini lookups (blips, chatbox skin/ink/showname/realization, scaling), the
// asset/overlay engine, and the local env (side, inline emotes, preanim-end) — so
// a room behaves identically in every mode.
// ONE production caller: App.newRoom (newroom.go). Construction and wiring are
// the same step on purpose — this used to be several call sites, and two of the
// five forgot it.
func (a *App) wireRoomCharMeta(room *courtroom.Courtroom) {
	if room == nil {
		return
	}
	room.Blips = roomBlipResolver{a}
	room.Chars = roomCharResolver{a}
	room.Assets = roomAssetResolver{a}
	room.Env = envResolver{a: a}
}

// iniIdleAnimFor is THE seam for "what pose does this character stand in".
// The answer is a NAME, not a URL, deliberately: the caller mints it through
// urls.Emote + urls.EmoteAlts and PrefetchChain, i.e. the real sprite chain with
// AO2-Client's two spellings ((a)X and bare X, CharLayer::load_image) and the
func (a *App) iniIdleAnimFor(char, fallback string) string {
	if idle := a.charMetaFor(char).idle; idle != "" {
		return idle
	}
	return fallback
}

// remoteScalingFor is the courtroom's SpriteScaling callback: the speaker's own
// char.ini [Options] scaling= request (issue #21 label 15). It rides the SAME
// cache and the SAME single fetch as blips and chat skins, so honouring every
// character's declared filter costs no extra network probe.
// Auto until the fetch lands — the speaker's NEXT message picks it up, and the
// renderer's geometry rule covers the gap meanwhile, so a first message is never
// wrong, only potentially less specific than the author asked for.
func (a *App) remoteScalingFor(char string) courtroom.ScalingMode {
	return a.charMetaFor(char).scaling
}
