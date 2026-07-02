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
	done  bool   // fetch settled (hit or miss) — misses cache too (no refetch loop)
}

type charMetaFetch struct {
	url   string
	blips string
	chat  string
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
	a.charMetaFetchOne(url)
	return charMeta{}
}

// charMetaFetchOne downloads + parses one char.ini off-thread and posts the
// result (never blocking; a dropped result refetches next session).
func (a *App) charMetaFetchOne(url string) {
	if a.charMetaRes == nil {
		a.charMetaRes = make(chan charMetaFetch, charMetaResCap)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		res := charMetaFetch{url: url}
		if data, err := a.d.Manager.FetchRaw(ctx, url); err == nil {
			if ini, err := courtroom.ParseCharINI(data); err == nil && ini != nil {
				res.blips = strings.TrimSpace(ini.Blips)
				res.chat = strings.TrimSpace(ini.Chat)
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
			a.charMetaCache[res.url] = charMeta{blips: res.blips, chat: res.chat, done: true}
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

// wireRoomCharMeta attaches the char.ini-driven callbacks to a room — the
// live courtroom, replays, and the maker preview all wire through here so a
// speaker blips and skins identically in every mode.
func (a *App) wireRoomCharMeta(room *courtroom.Courtroom) {
	if room == nil {
		return
	}
	room.BlipNameFor = a.remoteBlipFor
	room.ChatSkinFor = a.remoteChatSkinFor
}
