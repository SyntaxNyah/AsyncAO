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

// THE AUTOMATIC ROSTER POLL IS GONE (user order, 2026-08-08, after a live session).
//
// There used to be a debounced re-pull (rosterRefetchDebounce, 3 s) driven off the
// server's own CharsCheck / ARUP / PU packets, so a busy hub re-sent rosterCmd
// indefinitely. On an old KFO hub that answers it with "You cannot see players in all
// areas in this hub!" — which is neither an area list nor a command error, so neither
// the echo suppression nor the unsupported-latch caught it — the client pasted that
// line into OOC every three seconds for the whole session. The instruction was to
// delete the legacy poll outright rather than teach it another special case: even a
// server that will never answer must at least not be spammed.
//
// This also CLOSES the v1.82 rate-limit class for good. A timed OOC command queued by
// a producer that runs while the drain does not is the exact shape that got clients
// flood-kicked ([[minimized-disconnect-root-cause]]: 68 kicks, /gas bursts of five);
// the drain-side fix bounded the burst, and with no timed producer left there is
// nothing to burst.
//
// WHAT SURVIVES: the server-pushed PR/PU roster (rebuildLiveRoster — unchanged, and it
// is the roster wherever the server speaks it), plus the four fetches a PERSON asks
// for — the Players panel's on-open / area-change pull (playerlist.go), the "Refresh
// roster details" menu row (musicheader.go), the mod ban box's /getareas
// (moddashpanel.go), and the one-shot after a successful /login (app.go, EventAuth).
//
// THE COST, stated plainly: a server with no PR/PU (old KFO, the Athena/tsuserver
// family) now has NO self-updating player list beyond the pushed CharsCheck/ARUP rows.
// Its UIDs/IPIDs refresh only when the user asks. That trade is the user's explicit
// decision from the field, not an oversight.

// rosterCmd is the all-areas roster command. Named so a fetch can check whether one is
// already queued before adding another (see fetchRoster).
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
// only the CURRENT area — a silently wrong roster instead of an error. This pull
// therefore stays one compile-time constant, and the one piece of machinery still
// keyed to that is oocQueuePending(rosterCmd), which matches an already-queued copy
// by exact string equality (macros.go:137). Making the pull family-aware means
// revisiting that match, not just swapping this const.
//
// (There used to be a second: a spelling-blind refusal latch that disabled the pull
// for the session on any command error. It went with the automatic poll — see
// maybeRefetchRoster's obituary below — because with every fetch now coming from a
// button, silently disabling one is worse than letting the server's refusal print.)
//
// Being wrong here used to be a repeating cost, because the poll re-sent it forever;
// now every caller is a person, so a server that does not register /gas answers once,
// to a press, and the user sees the answer. Akashi/WAP, tsuserver3/KFO and Athena all
// reply with a command error ("Invalid command." —
// akashi/src/commands/command_helper.cpp:33, Athena/internal/athena/commands.go:392),
// and an old KFO hub may instead reply "You cannot see players in all areas in this
// hub!". Either way the fallback for those families is the current-area
// "Refresh roster details" row, which DOES ask in that server's own spelling
// (rosterDetailCmd).
//
// Shared by the Players-panel pull, the mod IPID refresh, and the on-auth pull.
func (a *App) fetchRoster() {
	if a.oocQueuePending(rosterCmd) {
		// One /gas is already waiting to go out. Stacking another buys nothing (they
		// would return the same snapshot) and, if the drain is running behind, several
		// identical commands leaving close together look like OOC flooding to the
		// server — so a double-press collapses to one command.
		return
	}
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
//     answers "Invalid command" — which now simply prints in the OOC log next to the
//     press that caused it. (It used to latch a session-long refusal flag instead;
//     docs/KNOWN-ISSUES.md, "Why some servers never showed it", is the incident
//     write-up for the automatic poll that needed one.)
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

// maybeRefetchRoster is DELETED, and so are looksLikeCommandError and
// liveRosterMissingIPID, which existed only to serve it. It re-pulled the all-areas
// snapshot off EventCharsUpdated / EventAreasUpdated / EventPlayersUpdated on a 3 s
// debounce; the header comment above records why the whole automatic tier is gone and
// what replaced it. Its mod-IPID self-gate is the part worth naming, because it looked
// self-limiting and was not: `ModGranted && liveRosterMissingIPID()` never settles on a
// server whose roster reply carries no IPIDs at all, so a logged-in mod there polled
// every 3 s for the length of the session. TestOnlyUserActionsFetchTheRoster
// (rosterpoll_test.go) is the deletion catcher — a new automatic caller of fetchRoster
// fails it, and TestNoSessionEverFiresATimedRosterPoll in the same file floods the
// client with the packets that used to drive the poll and asserts nothing goes out.

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
