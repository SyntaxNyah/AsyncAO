package ui

import (
	"strconv"
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

// buildLivePlayers converts the server-pushed live roster (PR/PU, the Akashi/
// Nyathena PlayerStateObserver) into display rows. Every player is a row keyed by
// its server UID — the live source of UID/showname/OOC/area the /getarea snapshot
// used to stand in for — tagged with its area name (areas[AreaID]) so the
// existing per-area grouping works across the whole server. A player with no
// character is a Spectator, so spectators appear and vanish live. IPID is the one
// field PU never carries; ipidByUID merges it from a /getarea snapshot (when a mod
// pulled one) on an exact UID key. Pure + table-tested.
func buildLivePlayers(players []courtroom.LivePlayer, areas []string, ipidByUID map[string]string) []areaPlayer {
	out := make([]areaPlayer, 0, len(players))
	for i := range players {
		p := players[i]
		name := p.Char
		if name == "" {
			name = specName // no character = spectator / still at char select
		}
		area := ""
		if p.AreaID >= 0 && p.AreaID < len(areas) {
			area = areas[p.AreaID]
		}
		uid := strconv.Itoa(p.ID)
		ipid := p.IPID // WAP streams the mod-only IPID live in the PU name — prefer it
		if ipid == "" {
			ipid = ipidByUID[uid] // else a /getarea snapshot (other IPID-only servers); "" is safe
		}
		out = append(out, areaPlayer{
			uid:      uid,
			name:     name,
			showname: p.Showname,
			ooc:      p.OOCName,
			ipid:     ipid,
			area:     area,
		})
	}
	return out
}

// ipidByUID maps UID→IPID from the last /getarea snapshot (areaPlayers) so the
// PR/PU live roster can show IPIDs, which PU never carries. Keyed by UID, so the
// merge is exact (unlike the old by-name match). Nil when no snapshot has IPIDs.
func (a *App) ipidByUID() map[string]string {
	var m map[string]string
	for i := range a.areaPlayers {
		if p := &a.areaPlayers[i]; p.uid != "" && p.ipid != "" {
			if m == nil {
				m = make(map[string]string, len(a.areaPlayers))
			}
			m[p.uid] = p.ipid
		}
	}
	return m
}

// rosterEqual reports whether two rosters are identical for display purposes —
// same length, same per-row identity and placement. Used to skip a rebuild
// (and the icon-cache invalidation it forces) when a CharsCheck/ARUP packet
// didn't actually change the current area's roster.
func rosterEqual(a, b []areaPlayer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].showname != b[i].showname ||
			a[i].uid != b[i].uid || a[i].ooc != b[i].ooc || a[i].ipid != b[i].ipid ||
			// area included: a pure move (PU type 3) changes NOTHING else, and
			// treating it as "unchanged" froze the Players tab's room grouping
			// until someone joined or left (playtest report).
			a[i].area != b[i].area {
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
	if a.rosterLegacy || a.sess == nil {
		return
	}
	var next []areaPlayer
	if live := a.sess.Players(); len(live) > 0 {
		// Primary: the server-pushed PR/PU roster — live UIDs, shownames, OOC
		// names and areas, plus spectators, with no /getarea polling. IPID is
		// enriched by UID from the last snapshot (mod-only) when present.
		a.livePlayersOn = true
		next = buildLivePlayers(live, a.sess.Areas, a.ipidByUID())
	} else {
		a.livePlayersOn = false
		if len(a.areaPlayers) > 0 {
			return // no PR/PU on this server; the /getarea snapshot is the roster
		}
		// Pre-PR/PU fallback: name-only rows from CharsCheck so the list isn't blank.
		n, ok := a.curAreaPlayers()
		next = buildLiveRoster(a.sess.Chars, n, ok, a.curArea, a.shownameFor, a.areaPlayers)
	}
	if rosterEqual(a.liveRoster, next) {
		return
	}
	a.liveRoster = next
	a.playerIconPages = nil // re-resolve icons (same-length new roster reuses indices)
	a.liveRosterAt = a.now()
	a.updateJoinFlash(a.liveRosterAt) // new-joiner highlight (#107)
}

// joinFlashWindow is how long a newly-joined player's row stays highlighted.
const joinFlashWindow = 4 * time.Second

// updateJoinFlash reconciles the new-joiner highlight timestamps against the live
// roster: a UID newly present is stamped (its row flashes briefly), one that left is
// dropped (so a rejoin flashes again, and the map stays bounded). The very first
// population isn't flashed — joining a busy room shouldn't light up every row. Called
// only from rebuildLiveRoster on a real roster change, so the per-frame draw just does
// a 0-alloc map lookup.
func (a *App) updateJoinFlash(now time.Time) {
	if a.joinFlash == nil {
		a.joinFlash = make(map[string]time.Time, len(a.liveRoster)+4)
	}
	stampNew := a.joinFlashInit // skip flashing the initial population
	for i := range a.liveRoster {
		uid := a.liveRoster[i].uid
		if uid == "" {
			continue
		}
		if _, ok := a.joinFlash[uid]; !ok {
			if stampNew {
				a.joinFlash[uid] = now
			} else {
				a.joinFlash[uid] = time.Time{} // present at first sight — never flashed
			}
		}
	}
	a.joinFlashInit = true
	for uid := range a.joinFlash {
		if _, ok := a.rosterByUID(uid); !ok {
			delete(a.joinFlash, uid)
		}
	}
}

// rosterRefetchDebounce bounds how often a join/leave re-pulls the rich all-areas
// snapshot (rosterCmd, below) in live mode — fresh enough, but never a command
// per packet.
const rosterRefetchDebounce = 3 * time.Second

// rosterCmd is the all-areas roster command. Named so the poll can check
// whether one is already queued before adding another (see fetchRoster).
const rosterCmd = "/gas"

// areaEchoSuppressWindow is how long after a /gas we keep incoming area-list
// messages out of OOC — a multi-area /gas (Athena/Nyathena) replies as SEVERAL
// messages, and the old single-shot suppression let every line after the first
// leak into the chat log.
const areaEchoSuppressWindow = 3 * time.Second

// fetchRoster pulls the all-areas UID/showname/IPID detail the live list merges
// over the PR/PU rows, and stamps the debounce. It sends rosterCmd ("/gas"), not
// /getareas, because the live list spans every area, so its IPID source must too.
//
// Every family DOES register some all-areas spelling; they just disagree on which.
// /gas is Nyathena's (Nyathena/internal/athena/commands_registry.go:318) and
// Whisker's (Whisker/src/commands.c3:79); Akashi and its WAP fork spell it
// /getareas (akashi/src/aoclient.cpp:29, witches-akashi-party/src/aoclient.cpp:30),
// as do tsuserver3/KFO (KFO-Server/server/commands/areas.py:288); and on Athena it
// is the "-a" flag of its one roster command, /players
// (Athena/internal/athena/commands.go:282, `Usage: /players [-a]`). So a
// family-aware poll IS constructible — rosterDetailCmd below is that exact
// pattern — and it would even survive the Athena/stock-Nyathena detection
// ambiguity, because "/players -a" means all areas on BOTH (Nyathena's own gas
// handler is cmdPlayers(client, []string{"-a"}, ""),
// commands_registry.go:319).
//
// What does not exist is a single UNIVERSAL string, and "/players -a" is the
// dangerous near-miss: Whisker maps players onto its own cmd_ga and passes no
// arguments at all (Whisker/src/commands.c3:78), so there it would quietly return
// only the CURRENT area — a silently wrong roster instead of an error. This poll
// therefore stays one compile-time constant, and the machinery around it is keyed
// to that: oocQueuePending(rosterCmd) matches an already-queued copy by exact
// string equality (macros.go:137), and the refusal latch is spelling-blind — any
// command error inside the suppression window sets the single rosterCmdUnsupported
// flag (app.go:9949), which records that THE poll was refused, never which
// spelling was. Making the poll family-aware is a change to both of those, not a
// swap of this const.
//
// Being WRONG here is not merely inert: it repeats for the life of the session,
// and an unanswerable command every rosterRefetchDebounce is the flood shape that
// got clients kicked (docs/KNOWN-ISSUES.md, "Why some servers never showed it").
// So the fixed spelling ACCEPTS being switched off wherever /gas is unregistered:
// Akashi/WAP, tsuserver3/KFO and Athena all answer a command error ("Invalid
// command." — akashi/src/commands/command_helper.cpp:33,
// Athena/internal/athena/commands.go:392), looksLikeCommandError latches
// rosterCmdUnsupported, and the poll stops for that session. The cost, stated
// plainly: on those servers NO all-areas snapshot is ever pulled again.
//   - Akashi/WAP and KFO push the 2.11 live list (serverhelp.go plist), so the
//     roster itself is complete anyway; what is lost is the mod-only IPID for
//     players in OTHER areas.
//   - Athena and tsuserver3 have no live list at all (serverhelp.go:79, :121), so
//     there the whole all-areas snapshot is lost. What is left is the pushed
//     CharsCheck/ARUP rows plus "Refresh roster details" (rosterDetailCmd), which
//     does ask in that server's own spelling (on Athena the /players this poll
//     cannot use) — but that is CURRENT-area detail only.
//
// Shared by the on-open fetch, the mod IPID refresh, and the on-auth pull.
func (a *App) fetchRoster() {
	if a.rosterCmdUnsupported {
		return // this server rejected /gas earlier — don't re-spam it
	}
	if a.oocQueuePending(rosterCmd) {
		// One /gas is already waiting to go out. Stacking another buys nothing
		// (they'd return the same snapshot) and, if the drain is running behind,
		// several identical commands leaving close together look like OOC
		// flooding to the server. Re-stamp the debounce so the poll backs off
		// until this one has actually been sent.
		a.lastRosterFetch = a.now()
		return
	}
	a.lastRosterFetch = a.now()
	a.suppressAreaEchoUntil = a.now().Add(areaEchoSuppressWindow) // its whole reply burst is parsed but kept out of OOC
	a.pairAreaReset = true
	a.queueOOCLines([]string{rosterCmd})
}

// The two spellings of the CURRENT-area detail command. There is no universal
// one, so a control that sends a fixed string is inert on half the fleet. The
// split is by lineage, and each side is named after the spelling it registers —
// read out of the server sources, not assumed:
//
//   - rosterCmdPlayers ("/players") is the one current-area spelling the whole
//     Go/C3 lineage answers, which is why it is NOT the shorter "/ga": Athena
//     registers only "players" (Athena/internal/athena/commands.go:282,
//     `Usage: /players [-a]`) — no ga, no gas, no getarea, and its Command struct
//     has no alias field to hide one (commands.go:39-45). Nyathena registers
//     "players" too (Nyathena/internal/athena/commands_registry.go:779) and hangs
//     "ga" off the same cmdPlayers handler as a documented shortcut (:310), and
//     Whisker's dispatcher maps players onto its own cmd_ga
//     (Whisker/src/commands.c3:78) and has no getarea case at all (:77-79). So
//     "/players" is the only string all three answer; "/ga" would be an unknown
//     command on Athena.
//   - rosterCmdGetarea ("/getarea") is the AO2-canonical spelling and the only one
//     the Python/C++ lineage has: Akashi registers getarea/getareas and NOT
//     players or the short aliases (akashi/src/aoclient.cpp:28-29), and
//     tsuserver3/KFO define ooc_cmd_getarea/getareas only
//     (KFO-Server/server/commands/areas.py:271,288). That is why a /gas on those
//     answers "Invalid command" and latches rosterCmdUnsupported
//     (docs/KNOWN-ISSUES.md, "Why some servers never showed it").
const (
	rosterCmdPlayers = "/players"
	rosterCmdGetarea = "/getarea"
)

// rosterDetailCmd is the current-area detail command THIS server answers: the
// UIDs, IPIDs, OOC names and pair state the pushed CharsCheck/ARUP packets cannot
// carry. It is family-aware because the Players panel's "Refresh roster details"
// row is the only roster control the debloated toolbar kept (playerlisttoolbar.go),
// and a menu row that reliably replies "unknown command" is worse than no row —
// the degradation contract there is that a control may be absent, never inert.
//
// The families with no 2.11 live player list (serverhelp.go) are exactly the ones
// that depend on this, and Whisker is one of them (plistPlugin) — so each family
// MUST get a spelling it actually registers, or the one roster control that family
// has left is the inert row this function exists to prevent. An unrecognised
// server still gets the canonical /getarea rather than a guess.
func (a *App) rosterDetailCmd() string {
	switch a.detectedSoftware() {
	case courtroom.SoftwareAthena, courtroom.SoftwareNyathena, courtroom.SoftwareWhisker:
		// All three answer /players; Athena answers ONLY that (see the const block).
		return rosterCmdPlayers
	}
	return rosterCmdGetarea
}

// looksLikeCommandError reports whether an OOC line is a server "command not
// recognised" reply (the response to a /gas the server doesn't support). Only
// consulted inside the post-fetch window, so a real chat line can't trip it.
func looksLikeCommandError(text string) bool {
	t := strings.ToLower(text)
	return strings.Contains(t, "unknown command") ||
		strings.Contains(t, "invalid command") ||
		strings.Contains(t, "not a command") ||
		strings.Contains(t, "command not found")
}

// maybeRefetchRoster re-pulls the all-areas snapshot (fetchRoster, i.e. rosterCmd
// — NOT /getareas; the two spellings are not interchangeable, see the const block
// above), debounced. On the PR/PU path the ONLY thing that fetch adds over the
// live roster is mod-only IPID, so it polls only while a mod still has rows
// without one — once they land (or for a non-mod, who can't see IPIDs at all) the
// list stays event-driven and quiet. The pre-PR/PU fallback keeps the old
// always-refresh behaviour (it needs the fetch for UIDs).
func (a *App) maybeRefetchRoster() {
	if a.rosterLegacy || a.sess == nil {
		return
	}
	if a.livePlayersOn && (!a.sess.ModGranted || !a.liveRosterMissingIPID()) {
		return
	}
	if a.now().Sub(a.lastRosterFetch) < rosterRefetchDebounce {
		return
	}
	a.fetchRoster()
}

// liveRosterMissingIPID reports whether any live row with a CHARACTER still has a
// UID but no IPID — the signal a mod needs a /getareas pull to fill them in.
// Spectators are excluded: a server may omit them from /getareas, and counting
// them would poll forever.
func (a *App) liveRosterMissingIPID() bool {
	for i := range a.liveRoster {
		r := &a.liveRoster[i]
		if r.uid != "" && r.ipid == "" && r.name != specName {
			return true
		}
	}
	return false
}

// rosterView is the player list's active data: the live (CharsCheck/ARUP) roster
// by default, or the /getarea snapshot in legacy mode. The pair popup always uses
// the snapshot (areaPlayers) directly — it needs the UIDs only /getarea carries,
// so the live roster lives in its own slice rather than swapping areaPlayers out.
func (a *App) rosterView() []areaPlayer {
	if a.rosterLegacy {
		return a.areaPlayers // explicit legacy /getarea snapshot
	}
	if a.livePlayersOn {
		return a.liveRoster // server-pushed PR/PU roster (UIDs/shownames live)
	}
	// No PR/PU on this server: show the /getarea snapshot once it has landed,
	// else the CharsCheck name-only roster so the list isn't blank. (The pair
	// popup uses areaPlayers directly too.)
	if len(a.areaPlayers) > 0 {
		return a.areaPlayers
	}
	return a.liveRoster
}

// myAreaName is our current area NAME from the PR/PU roster — reliable on spawn
// AND on every move (unlike curArea, which only tracks areas we CLICK to and is ""
// on a fresh join). Falls back to curArea for servers that don't report our own
// area. Used to float our area to the top of the grouped player list.
func (a *App) myAreaName() string {
	if a.sess != nil {
		if id, ok := a.sess.PlayerArea(a.sess.PlayerID); ok && id >= 0 && id < len(a.sess.Areas) {
			return a.sess.Areas[id]
		}
	}
	return a.curArea
}

// rosterStamp is the active roster's last-change time — the memo-invalidation
// key for the grouped rows and sort order.
func (a *App) rosterStamp() time.Time {
	if !a.rosterLegacy && a.livePlayersOn {
		return a.liveRosterAt
	}
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
