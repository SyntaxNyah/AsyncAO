package ui

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Live player list (M1). The roster used to be a /getarea snapshot stamped "as
// of HH:MM". webAO keeps its list live WITHOUT polling getarea (its only timer
// is the CH keepalive): it reacts to the server-pushed CharsCheck (taken
// characters) and the ARUP head-count. We do the same, so the list updates as
// people join and leave with ZERO extra traffic (no floodguard risk) and zero
// per-frame cost — it rebuilds on the packet, never in the draw loop.
//
// Spectators have no character, so CharsCheck can't see them by name; but
// spectator_count = ARUP head-count − taken characters, which still drops when
// one leaves, so anonymous Spectator rows appear and vanish too (parity with
// webAO, which also can't name live spectators over the AO protocol). The rich
// roster — names, UIDs, IPIDs — is snapshot-only data: flip the Legacy tick box
// for the full /getarea list on demand.

// specName is the wire/display name AO uses for a character-less player; the
// roster row renderer already special-cases it (no icon, a SPEC chip).
const specName = "Spectator"

// buildLiveRoster assembles the in-area roster from the real-time signals: one
// row per taken character, then Spectator rows for the head-count beyond them.
// The `snapshot` (the last /getarea — i.e. areaPlayers) enriches each row with
// the data CharsCheck can't carry — UID, IPID, OOC name (matched by character;
// spectators handed out in snapshot order) — so a live row offers the same
// Pair/Copy actions as the legacy snapshot. Pure + table-tested.
func buildLiveRoster(chars []courtroom.CharacterSlot, headCount int, haveCount bool, area string, shownameFor map[string]string, snapshot []areaPlayer) []areaPlayer {
	// Split the snapshot: characters key by name; spectators are anonymous to
	// CharsCheck, so queue them in order to hand out up to the live head-count.
	var byChar map[string]areaPlayer
	var snapSpecs []areaPlayer
	for i := range snapshot {
		if snapshot[i].name == specName {
			snapSpecs = append(snapSpecs, snapshot[i])
			continue
		}
		if byChar == nil {
			byChar = make(map[string]areaPlayer, len(snapshot)*2)
		}
		// Index by BOTH the name and the showname: servers disagree on which the
		// /getarea row leads with (Akashi "char (showname)", tsuserver/Athena/
		// Nyathena "showname (char)"), so either one lands a match.
		byChar[strings.ToLower(snapshot[i].name)] = snapshot[i]
		if sn := snapshot[i].showname; sn != "" {
			byChar[strings.ToLower(sn)] = snapshot[i]
		}
	}

	out := make([]areaPlayer, 0, len(chars)+4)
	for i := range chars {
		if !chars[i].Taken {
			continue
		}
		row := areaPlayer{
			name:     chars[i].Name,
			showname: shownameFor[strings.ToLower(chars[i].Name)],
			area:     area,
		}
		snap, ok := byChar[strings.ToLower(chars[i].Name)]
		if !ok && row.showname != "" {
			snap, ok = byChar[strings.ToLower(row.showname)] // match by the cached IC name
		}
		if ok {
			row.uid, row.ooc, row.ipid = snap.uid, snap.ooc, snap.ipid
			if row.showname == "" {
				row.showname = snap.showname
			}
		}
		out = append(out, row)
	}
	// Spectators: the ARUP head-count beyond the characters. Prefer the named
	// snapshot rows (UID/OOC) in order; anonymous rows fill any remainder the
	// count knows about. Either way the COUNT moves live, so they come and go.
	if haveCount {
		for s, want := 0, headCount-len(out); s < want; s++ {
			if s < len(snapSpecs) {
				out = append(out, snapSpecs[s])
			} else {
				out = append(out, areaPlayer{name: specName, area: area})
			}
		}
	}
	return out
}

// rosterEqual reports whether two rosters are identical for display purposes —
// same length, same per-row identity (name + showname). Used to skip a rebuild
// (and the icon-cache invalidation it forces) when a CharsCheck/ARUP packet
// didn't actually change the current area's roster.
func rosterEqual(a, b []areaPlayer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].showname != b[i].showname ||
			a[i].uid != b[i].uid || a[i].ooc != b[i].ooc || a[i].ipid != b[i].ipid {
			return false // rich fields included so a /getarea enrich triggers a rebuild
		}
	}
	return true
}

// rebuildLiveRoster refreshes the live roster in place (live mode only). Called
// on CharsCheck (EventCharsUpdated) and ARUP (EventAreasUpdated) — never per
// frame. It no-ops when the roster is unchanged, so spurious packets cost
// nothing; on a real change it nulls the index-keyed icon cache (the cachedPage
// reorder invariant — a same-length new roster reuses indices) and restamps the
// memo time so the grouped-rows cache rebuilds once.
func (a *App) rebuildLiveRoster() {
	if a.rosterLegacy || a.sess == nil || len(a.areaPlayers) > 0 {
		return // snapshot is showing; the CharsCheck-names fallback isn't displayed
	}
	n, ok := a.curAreaPlayers()
	next := buildLiveRoster(a.sess.Chars, n, ok, a.curArea, a.shownameFor, a.areaPlayers)
	if rosterEqual(a.liveRoster, next) {
		return
	}
	a.liveRoster = next
	a.playerIconPages = nil // re-resolve icons (same-length new roster reuses indices)
	a.liveRosterAt = a.now()
}

// rosterRefetchDebounce bounds how often a join/leave re-pulls the rich /getarea
// snapshot in live mode — fresh enough, but never a command per packet.
const rosterRefetchDebounce = 3 * time.Second

// fetchRoster pulls the /getarea snapshot (the UID/IPID/Pair data the live list
// shows) and stamps the debounce. Shared by the on-open/area-change fetch and the
// join/leave refresh.
func (a *App) fetchRoster() {
	a.lastRosterFetch = a.now()
	a.suppressAreaEcho = true // its reply is parsed but kept out of the OOC log
	a.pairAreaReset = true
	a.queueOOCLines([]string{"/getarea"})
}

// maybeRefetchRoster re-pulls the snapshot after a join/leave (CharsCheck/ARUP),
// debounced, so the live list's rich data stays current without a command per
// packet. Live mode only.
func (a *App) maybeRefetchRoster() {
	if a.rosterLegacy || a.sess == nil {
		return
	}
	if a.now().Sub(a.lastRosterFetch) < rosterRefetchDebounce {
		return
	}
	a.fetchRoster()
}

// rosterView is the player list's active data: the live (CharsCheck/ARUP) roster
// by default, or the /getarea snapshot in legacy mode. The pair popup always uses
// the snapshot (areaPlayers) directly — it needs the UIDs only /getarea carries,
// so the live roster lives in its own slice rather than swapping areaPlayers out.
func (a *App) rosterView() []areaPlayer {
	// Both modes show the /getarea snapshot once it has landed — it is the only
	// source of UIDs/IPIDs/OOC and the Pair/Copy buttons, and matching it back onto
	// CharsCheck rows by name was too fragile. Live mode auto-fetches and refreshes
	// it; before the first reply we fall back to the CharsCheck names so the list
	// isn't blank. (The pair popup uses areaPlayers directly too.)
	if a.rosterLegacy || len(a.areaPlayers) > 0 {
		return a.areaPlayers
	}
	return a.liveRoster
}

// rosterStamp is the active roster's last-change time — the memo-invalidation
// key for the grouped rows and sort order.
func (a *App) rosterStamp() time.Time {
	if a.rosterLegacy || len(a.areaPlayers) > 0 {
		return a.areaListAt
	}
	return a.liveRosterAt
}

// setRosterLegacy switches the player list between the live roster and the
// /getarea snapshot. The active roster (its length and index→player mapping)
// changes, so the index-keyed icon cache is dropped; switching back to live
// rebuilds at once.
func (a *App) setRosterLegacy(legacy bool) {
	if a.rosterLegacy == legacy {
		return
	}
	a.rosterLegacy = legacy
	a.playerIconPages = nil
	if !legacy {
		a.rebuildLiveRoster()
	}
}

// noteShowname caches a character's latest showname from incoming IC — the only
// place a showname arrives outside a /getarea snapshot — so a live row can show
// it instead of the bare character folder.
func (a *App) noteShowname(char, showname string) {
	if char == "" || showname == "" {
		return
	}
	if a.shownameFor == nil {
		a.shownameFor = make(map[string]string, 32)
	}
	a.shownameFor[strings.ToLower(char)] = showname
}
