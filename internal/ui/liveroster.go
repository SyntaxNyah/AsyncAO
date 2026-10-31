package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Live player list. Like webAO, it reacts to server-pushed CharsCheck and ARUP instead of polling getarea, so there is
// no extra traffic or floodguard risk, and it rebuilds on the packet, never in the draw loop.
// Spectators have no character: spectator_count = ARUP head-count minus taken characters.

// specName is the name AO uses for a character-less player; the row renderer special-cases it.
const specName = "Spectator"

// buildLiveRoster makes one row per taken character, then Spectator rows for the head-count beyond them.
// snapshot, the last /getarea, adds the UID, IPID and OOC name CharsCheck can't carry.
func buildLiveRoster(chars []courtroom.CharacterSlot, headCount int, haveCount bool, area string, shownameFor map[string]string, snapshot []areaPlayer) []areaPlayer {
	// Spectators are anonymous to CharsCheck, so queue them to hand out up to the live head-count.
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
		// Index by name and showname: servers disagree on which the /getarea row leads with
		// (Akashi "char (showname)", tsuserver/Athena/Nyathena "showname (char)").
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
	// Spectators: the head-count beyond the characters, preferring named snapshot rows, then anonymous ones.
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

// buildLivePlayers converts the server-pushed PR/PU roster into rows keyed by server UID, tagged with area name.
// PU never carries IPID, so ipidByUID merges it from a /getarea snapshot on an exact UID key.
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

// ipidByUID maps UID to IPID from the last /getarea snapshot; nil when no snapshot has IPIDs.
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

// rosterEqual reports display equality, used to skip a rebuild and the icon-cache invalidation it forces.
func rosterEqual(a, b []areaPlayer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].showname != b[i].showname ||
			a[i].uid != b[i].uid || a[i].ooc != b[i].ooc || a[i].ipid != b[i].ipid ||
			// A pure move (PU type 3) changes only the area, and must regroup the Players tab.
			a[i].area != b[i].area {
			return false // rich fields included so a /getarea enrich triggers a rebuild
		}
	}
	return true
}

// rebuildLiveRoster refreshes the live roster on CharsCheck and ARUP, never per frame, doing nothing if unchanged. On a
// change it nulls the index-keyed icon cache, since a same-length roster reuses indices, and restamps the memo time.
func (a *App) rebuildLiveRoster() {
	if a.rosterLegacy || a.sess == nil {
		return
	}
	var next []areaPlayer
	if live := a.sess.Players(); len(live) > 0 {
		// The server-pushed PR/PU roster; IPID comes by UID from the last snapshot when present.
		a.livePlayersOn = true
		next = buildLivePlayers(live, a.sess.Areas, a.ipidByUID())
	} else {
		a.livePlayersOn = false
		if len(a.areaPlayers) > 0 {
			return // no PR/PU on this server; the /getarea snapshot is the roster
		}
		// No PR/PU: name-only rows from CharsCheck so the list isn't blank.
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

// updateJoinFlash stamps UIDs new to the roster and drops ones that left, keeping the map bounded and letting a rejoin
// flash. The first population isn't flashed, since joining a busy room shouldn't light every row.
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

// Never poll the roster on a timer. Timed OOC commands get clients flood-kicked, and an old KFO hub answers /gas with a
// line that is neither an area list nor a command error, which would land in OOC on every poll. Fetches come only
// from PR/PU pushes and user actions; a server without PR/PU has UIDs and IPIDs refreshed only when the user asks.

// rosterCmd is the all-areas roster command, named so fetchRoster can check for a queued copy.
const rosterCmd = "/gas"

// areaEchoSuppressWindow keeps area-list messages out of OOC after a /gas: a multi-area reply is several messages.
const areaEchoSuppressWindow = 3 * time.Second

// fetchRoster pulls the all-areas detail the live list merges over PR/PU rows. /gas, not /getareas, since the list
// spans every area. Called only from user actions (Players panel, mod IPID refresh, on-auth pull).
//
// No spelling is universal: Nyathena and Whisker use /gas; Akashi/WAP use /getareas (aoclient.cpp:29, aoclient.cpp:30),
// as does KFO; Athena uses /players -a. "/players -a" is a trap: Whisker ignores arguments and silently returns the
// current area. Family-aware pulls must also revisit oocQueuePending's exact-string match on rosterCmd. Elsewhere a
// refusal ("Invalid command.", command_helper.cpp:33) prints once; rosterDetailCmd is the fallback.
func (a *App) fetchRoster() {
	if a.oocQueuePending(rosterCmd) {
		// A second queued /gas returns the same snapshot, and identical commands close together look like flooding.
		return
	}
	a.suppressAreaEchoUntil = a.now().Add(areaEchoSuppressWindow) // its whole reply burst is parsed but kept out of OOC
	a.pairAreaReset = true
	a.queueOOCLines([]string{rosterCmd})
}

// The two spellings of the current-area detail command; a fixed string would be inert on half the servers:
//   - rosterCmdPlayers is the only one Athena, Nyathena and Whisker all answer; Athena has no "ga", which Nyathena only
//     aliases.
//   - rosterCmdGetarea is AO2-canonical and the only one Akashi (aoclient.cpp:28-29) and tsuserver3/KFO have.
const (
	rosterCmdPlayers = "/players"
	rosterCmdGetarea = "/getarea"
)

// rosterDetailCmd is the current-area detail command this server answers, carrying what CharsCheck and ARUP cannot.
// Family-aware because this is the only roster control left on servers with no live player list, and a control may be
// absent but never inert. Unknown servers get the canonical /getarea.
func (a *App) rosterDetailCmd() string {
	switch a.detectedSoftware() {
	case courtroom.SoftwareAthena, courtroom.SoftwareNyathena, courtroom.SoftwareWhisker:
		// All three answer /players; Athena answers only that.
		return rosterCmdPlayers
	}
	return rosterCmdGetarea
}

// client with the packets that used to drive the poll and asserts nothing goes out.

// rosterView is the player list's active data: the live roster by default, or the /getarea snapshot in legacy mode.
// The pair popup always uses areaPlayers, needing UIDs only /getarea carries.
func (a *App) rosterView() []areaPlayer {
	if a.rosterLegacy {
		return a.areaPlayers // explicit legacy /getarea snapshot
	}
	if a.livePlayersOn {
		return a.liveRoster // server-pushed PR/PU roster (UIDs/shownames live)
	}
	// No PR/PU: the /getarea snapshot once landed, else the CharsCheck name-only roster.
	if len(a.areaPlayers) > 0 {
		return a.areaPlayers
	}
	return a.liveRoster
}

// myAreaName is our current area name from PR/PU; curArea only tracks clicks and is "" on a fresh join.
func (a *App) myAreaName() string {
	if a.sess != nil {
		if id, ok := a.sess.PlayerArea(a.sess.PlayerID); ok && id >= 0 && id < len(a.sess.Areas) {
			return a.sess.Areas[id]
		}
	}
	return a.curArea
}

// rosterStamp is the active roster's last-change time, the memo key for grouped rows and sort order.
func (a *App) rosterStamp() time.Time {
	if !a.rosterLegacy && a.livePlayersOn {
		return a.liveRosterAt
	}
	if a.rosterLegacy || len(a.areaPlayers) > 0 {
		return a.areaListAt
	}
	return a.liveRosterAt
}

// setRosterLegacy switches between the live roster and the /getarea snapshot, dropping the index-keyed icon cache.
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

// noteShowname caches a character's latest showname from IC, the only source outside a /getarea snapshot.
func (a *App) noteShowname(char, showname string) {
	if char == "" || showname == "" {
		return
	}
	if a.shownameFor == nil {
		a.shownameFor = make(map[string]string, 32)
	}
	a.shownameFor[strings.ToLower(char)] = showname
}
