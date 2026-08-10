package ui

import (
	"context"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Remote-speaker char.ini metadata (playtest: characters' own blip sets and
// chatbox skins were ignored for other players). The wire only carries a blip
// name from 2.10.2+ senders, and the chat skin never rides the wire at all —
// webAO resolves both from the SPEAKER's char.ini, and so do we now: a small
// per-URL cache filled by one async fetch per character, answered lock-free
// from the courtroom's begin() via the BlipNameFor / ChatSkinFor callbacks.
// Keys are full char.ini URLs (origin included), so per-server separation is
// structural — the repo's cache-key convention.

const (
	// charMetaCap bounds the cache (rule §17.4). A busy server list tops out
	// far below this; past it the map resets (it's a cache — refetch heals).
	charMetaCap = 256
	// charMetaResCap sizes the async result channel; a burst beyond it just
	// leaves later results to the next drain (senders never block the fetch
	// goroutine — see charMetaFetchOne's non-blocking send).
	charMetaResCap = 8
)

// charMeta is the slice of a remote char.ini the live courtroom needs.
type charMeta struct {
	blips string // [Options] blips / legacy gender ("" = none declared)
	chat  string // [Options] chat — the misc chatbox-skin folder ("" = none)
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
	//
	// It rides this SAME single fetch, so previewing a partner honestly costs no
	// extra network probe: the char.ini was already being downloaded for their
	// blips, skin, effects, scaling and showname.
	idle string
	done bool // fetch settled (hit or miss) — misses cache too (no refetch loop)
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
}

// charMetaFor answers from the cache and fires ONE async fetch on a miss.
// Returns the zero meta until the fetch lands — the speaker's NEXT message
// picks it up (the same streaming shape as sprites and profiles).
func (a *App) charMetaFor(char string) charMeta {
	char = strings.TrimSpace(char)
	if char == "" || a.urls.Origin() == "" {
		return charMeta{}
	}
	url := a.charINIURL(char)
	if m, ok := a.charMetaCache[url]; ok {
		return m
	}
	if a.charMetaCache == nil {
		a.charMetaCache = make(map[string]charMeta, charMetaCap)
	}
	if len(a.charMetaCache) >= charMetaCap {
		a.charMetaCache = make(map[string]charMeta, charMetaCap) // reset: it's a cache, refetch heals
	}
	a.charMetaCache[url] = charMeta{} // in-flight marker: one fetch per URL
	a.charMetaFetchOne(url, char)
	return charMeta{}
}

// charMetaFetchOne downloads + parses one char.ini off-thread and posts the
// result (never blocking; a dropped result refetches next session).
//
// char is the FOLDER the url was minted from — get_showname's fallback value,
// resolved here so the cache holds canon's finished answer and no second copy
// of the rule exists at the read side.
func (a *App) charMetaFetchOne(url, char string) {
	if a.charMetaRes == nil {
		a.charMetaRes = make(chan charMetaFetch, charMetaResCap)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		res := charMetaFetch{url: url, showname: char} // no ini ⇒ get_showname's unreadable-ini answer
		if data, err := a.d.Manager.FetchRaw(ctx, url); err == nil {
			if ini, err := courtroom.ParseCharINI(data); err == nil && ini != nil {
				res.blips = strings.TrimSpace(ini.Blips)
				res.chat = strings.TrimSpace(ini.Chat)
				res.effects = strings.TrimSpace(ini.Effects)
				res.realization = strings.TrimSpace(ini.Realization)
				res.scaling = ini.ScalingMode()
				res.showname = ini.ShownameOrFolder(char) // AO2 get_showname, the ONE copy
				res.idle = ini.IdleAnim()                 // emote 1's anim — the ONE copy of "their resting pose"
			}
		}
		select {
		case a.charMetaRes <- res:
		default: // burst overflow: drop — the miss marker stays, a later session refetches
		}
	}()
}

// pollCharMeta drains landed char.ini fetches into the cache (called once per
// frame from the poll cluster; no-op with nothing pending).
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
			}
			// Our own character's effects folder may have just arrived: drop the
			// picker's per-frame memos so the roster is re-resolved for it.
			a.overlayInvalidateMemos()
		default:
			return
		}
	}
}

// remoteBlipFor is the courtroom's BlipNameFor callback: the speaker's
// char.ini blip set ("" until fetched / when none is declared).
func (a *App) remoteBlipFor(char string) string { return a.charMetaFor(char).blips }

// remoteChatSkinFor is the courtroom's ChatSkinFor callback — gated on the
// pref so turning skins off also stops the misc art fetches.
func (a *App) remoteChatSkinFor(char string) string {
	if !a.d.Prefs.CharChatboxOn() {
		return ""
	}
	return a.charMetaFor(char).chat
}

// remoteIniShownameFor is the courtroom's IniShownameFor callback — AO2's
// get_showname for a speaker, off the SAME single char.ini fetch as blips,
// skins, effects and scaling.
//
// known is `done`, never `showname != ""`: an empty answer is a character that
// declared needs_showname=false and wants a blank plate, and reading emptiness
// as "not fetched" would silently put the folder name back on exactly the
// characters that asked for no name at all.
func (a *App) remoteIniShownameFor(char string) (string, bool) {
	m := a.charMetaFor(char)
	return m.showname, m.done
}

// wireRoomCharMeta attaches the char.ini-driven callbacks to a room, so a
// speaker blips and skins identically in every mode.
//
// ONE production caller: App.newRoom (newroom.go). Construction and wiring are
// the same step on purpose — this used to be a second line every call site had
// to remember, and two of the five forgot it.
func (a *App) wireRoomCharMeta(room *courtroom.Courtroom) {
	if room == nil {
		return
	}
	room.BlipNameFor = a.remoteBlipFor
	room.ChatSkinFor = a.remoteChatSkinFor
	room.SpriteScaling = a.remoteScalingFor
	room.IniShownameFor = a.remoteIniShownameFor
	// Screen-effect overlays ride the same cache and the same single fetch
	// (effectspicker.go).
	a.wireRoomOverlay(room)
}

// iniIdleAnimFor is THE seam for "what pose does this character stand in".
//
// It answers the character's own first [Emotions] anim (courtroom.CharINI.IdleAnim
// — emote 1, the pose AO2 has selected the moment they are picked) and falls back
// to `fallback` only while the char.ini has not landed or names no emotes at all.
//
// WHY IT IS NOT JUST charMetaFor(...).idle AT EACH CALL SITE. The fallback is the
// interesting half. A preview must draw SOMETHING on the frame the cursor arrives,
// and the optimistic "normal" probe is what fills the common pack's box in a
// single request — so the rule is "convention until the ini disagrees", and that
// rule is the thing that must exist once. Spelled at each site it decays into a
// bare literal again, which is exactly how the pair ghost ended up minting
// (a)normal for characters that have no such sprite: their partner previewed as an
// empty box with a name label in it, on every server, forever.
//
// The answer is a NAME, not a URL, deliberately: the caller mints it through
// urls.Emote + urls.EmoteAlts and PrefetchChain, i.e. the real sprite chain with
// AO2-Client's two spellings ((a)X and bare X, CharLayer::load_image) and the
// authored case the URL builder already lower-cases for the path. Returning a URL
// here would put a second minting site next to the one that knows about alts.
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
//
// Auto until the fetch lands — the speaker's NEXT message picks it up, and the
// renderer's geometry rule covers the gap meanwhile, so a first message is never
// wrong, only potentially less specific than the author asked for.
func (a *App) remoteScalingFor(char string) courtroom.ScalingMode {
	return a.charMetaFor(char).scaling
}
