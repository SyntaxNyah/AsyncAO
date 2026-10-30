package courtroom

import (
	"fmt"
	"strconv"
	"strings"
)

// Moderation is OOC slash commands with per-server syntax, and wrong syntax fails silently; this builds the right one.

type ServerSoftware uint8

const (
	SoftwareUnknown   ServerSoftware = iota
	SoftwareTsuserver                // KFO / tsuserverCC / tsuserver3: IPID, positional, reason-then-duration (quoted)
	SoftwareAthena                   // Athena (Go): flags -u/-i, -d duration, reason at end (UID or IPID)
	SoftwareAkashi                   // Akashi (C++) + witches/wizards forks: IPID, positional, duration-then-reason
	SoftwareWhisker                  // Whisker (C3): UID, positional, reason-then-duration (quoted)
	// SoftwareNyathena has Athena's syntax; stock Nyathena announces "Athena", so detection needs the ID field set.
	// Kept last so earlier enum values stay stable.
	SoftwareNyathena
	// SoftwareWitches has Akashi's syntax, but WAP streams the mod-only IPID in the PU name (playerstateobserver.cpp).
	// Kept last so earlier values stay stable.
	SoftwareWitches
	ServerSoftwareCount
)

func (s ServerSoftware) String() string {
	switch s {
	case SoftwareTsuserver:
		return "KFO / tsuserver"
	case SoftwareAthena:
		return "Athena"
	case SoftwareAkashi:
		return "Akashi"
	case SoftwareWhisker:
		return "Whisker"
	case SoftwareNyathena:
		return "Nyathena"
	case SoftwareWitches:
		return "WAP-Akashi"
	default:
		return "Unknown"
	}
}

// DetectSoftware maps the ID packet's software string by case-insensitive substring, as strings carry versions.
func DetectSoftware(software string) ServerSoftware {
	s := strings.ToLower(software)
	switch {
	case strings.Contains(s, "wap"), strings.Contains(s, "witches"): // witches-akashi-party — MUST precede "akashi" ("WAP-Akashi" contains both)
		return SoftwareWitches
	case strings.Contains(s, "akashi"): // stock Akashi + the wizards-akashi-rave fork
		return SoftwareAkashi
	case strings.Contains(s, "kfo"), strings.Contains(s, "tsuserver"):
		return SoftwareTsuserver
	case strings.Contains(s, "nyathena"): // MUST precede "athena" (it's a substring of "nyathena")
		return SoftwareNyathena
	case strings.Contains(s, "athena"):
		return SoftwareAthena
	case strings.Contains(s, "whisker"):
		return SoftwareWhisker
	default:
		return SoftwareUnknown
	}
}

type BanDuration uint8

const (
	BanPerma BanDuration = iota
	Ban1Hour
	Ban6Hours
	Ban1Day
	Ban3Days
	Ban1Week
	Ban2Weeks
	Ban3Weeks
	Ban1Month
	BanDurationCount
)

// durationToken renders short tokens for str2duration and Akashi parseTime, human ones for KFO and Whisker.
func durationToken(sw ServerSoftware, d BanDuration) string {
	if d == BanPerma {
		return "perma"
	}
	human := sw == SoftwareTsuserver || sw == SoftwareWhisker
	switch d {
	case Ban1Hour:
		return pick(human, "1 hour", "1h")
	case Ban6Hours:
		return pick(human, "6 hours", "6h")
	case Ban1Day:
		return pick(human, "1 day", "1d")
	case Ban3Days:
		return pick(human, "3 days", "3d")
	case Ban1Week:
		return pick(human, "1 week", "1w")
	case Ban2Weeks:
		return pick(human, "2 weeks", "2w")
	case Ban3Weeks:
		return pick(human, "3 weeks", "3w")
	case Ban1Month:
		// Months parse inconsistently (Akashi's "m" is minutes), so emit a day count every server accepts.
		return pick(human, "30 days", "30d")
	}
	return ""
}

func BanDurationLabel(d BanDuration) string {
	switch d {
	case BanPerma:
		return "Permanent"
	case Ban1Hour:
		return "1 hour"
	case Ban6Hours:
		return "6 hours"
	case Ban1Day:
		return "1 day"
	case Ban3Days:
		return "3 days"
	case Ban1Week:
		return "1 week"
	case Ban2Weeks:
		return "2 weeks"
	case Ban3Weeks:
		return "3 weeks"
	case Ban1Month:
		return "1 month"
	}
	return ""
}

// banDurationUnits maps a unit to its letter; months and years are out, as "1mo" vs "1m" is ambiguous.
var banDurationUnits = map[string]byte{
	"m": 'm', "min": 'm', "mins": 'm', "minute": 'm', "minutes": 'm',
	"h": 'h', "hr": 'h', "hrs": 'h', "hour": 'h', "hours": 'h',
	"d": 'd', "day": 'd', "days": 'd',
	"w": 'w', "wk": 'w', "wks": 'w', "week": 'w', "weeks": 'w',
}

// maxCustomBanNumber stops a typo like "100000d" from saving a nonsense chip.
const maxCustomBanNumber = 9999

// CanonicalBanDuration parses a typed duration into its short token; ok=false for anything unparseable.
func CanonicalBanDuration(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "perma" || s == "permanent" || s == "forever" {
		return "perma", true
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i > 4 { // no number, or absurdly long (> maxCustomBanNumber digits)
		return "", false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil || n < 1 || n > maxCustomBanNumber {
		return "", false
	}
	unit, ok := banDurationUnits[strings.TrimSpace(s[i:])]
	if !ok {
		return "", false
	}
	return strconv.Itoa(n) + string(unit), true
}

// BanDurationTokenLabel is the UI label for a canonical custom token.
func BanDurationTokenLabel(token string) string {
	if token == "perma" {
		return "Permanent"
	}
	return banDurationHuman(token)
}

// banDurationHuman renders a short token in words for servers whose parsers take human phrasing.
func banDurationHuman(token string) string {
	if len(token) < 2 {
		return token
	}
	num, unit := token[:len(token)-1], token[len(token)-1]
	var word string
	switch unit {
	case 'm':
		word = "minute"
	case 'h':
		word = "hour"
	case 'd':
		word = "day"
	case 'w':
		word = "week"
	default:
		return token
	}
	if num != "1" {
		word += "s"
	}
	return num + " " + word
}

// BanCommand builds the OOC /ban: IPID for KFO/Akashi, UID for Whisker, Athena prefers IPID; "" if the id is missing.
func BanCommand(sw ServerSoftware, ipid, uid string, dur BanDuration, reason string) string {
	return banCommandWith(sw, ipid, uid, durationToken(sw, dur), reason)
}

// BanCommandToken is BanCommand with a canonical custom duration token, rendered per durationToken's split.
func BanCommandToken(sw ServerSoftware, ipid, uid, token, reason string) string {
	if token == "" {
		return ""
	}
	d := token
	if sw == SoftwareTsuserver || sw == SoftwareWhisker {
		d = banDurationHuman(token)
	}
	if token == "perma" {
		d = "perma"
	}
	return banCommandWith(sw, ipid, uid, d, reason)
}

// banCommandWith builds the per-software /ban with an already-rendered duration.
func banCommandWith(sw ServerSoftware, ipid, uid, d, reason string) string {
	reason = sanitizeReason(reason)
	if reason == "" {
		reason = "No reason given" // a ban always carries a reason (the quoted-reason servers need a non-empty arg)
	}
	switch sw {
	case SoftwareTsuserver: // /ban <ipid> "<reason>" "<duration>"
		if ipid == "" {
			return ""
		}
		return fmt.Sprintf("/ban %s %s %s", ipid, quote(reason), quote(d))
	case SoftwareAkashi, SoftwareWitches: // /ban <ipid> <duration> <reason>   (duration BEFORE reason; WAP forks Akashi's cmdBan)
		if ipid == "" {
			return ""
		}
		return fmt.Sprintf("/ban %s %s %s", ipid, d, reason)
	case SoftwareAthena, SoftwareNyathena: // /ban -i <ipid> | -u <uid>  -d <duration> <reason>  (Nyathena forks Athena — same flags)
		target := banTarget(ipid, uid)
		if target == "" {
			return ""
		}
		return fmt.Sprintf("/ban %s -d %s %s", target, d, reason)
	case SoftwareWhisker: // /ban <uid> "<reason>" "<duration>"
		if uid == "" {
			return ""
		}
		return fmt.Sprintf("/ban %s %s %s", uid, quote(reason), quote(d))
	}
	return ""
}

// KickCommand builds the OOC /kick. Athena/Nyathena need a reason and the client UID: "/kick -i" is a silent no-op.
func KickCommand(sw ServerSoftware, ipid, uid, reason string) string {
	reason = sanitizeReason(reason)
	switch sw {
	case SoftwareTsuserver, SoftwareAkashi, SoftwareWitches: // /kick <ipid> [reason]  (WAP kicks by IPID unless prefixed "*<id>")
		if ipid == "" {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("/kick %s %s", ipid, reason))
	case SoftwareAthena, SoftwareNyathena: // /kick -u <uid> <reason>  (connected client → UID, never IPID; reason REQUIRED)
		if uid == "" || reason == "" {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("/kick -u %s %s", uid, reason))
	case SoftwareWhisker: // /kick <uid> [reason]
		if uid == "" {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("/kick %s %s", uid, reason))
	}
	return ""
}

// KickRequiresReason reports whether sw's /kick rejects a blank reason, which only Athena/Nyathena do.
func KickRequiresReason(sw ServerSoftware) bool {
	return sw == SoftwareAthena || sw == SoftwareNyathena
}

// CM and area controls: each builder returns the exact command, or "" when the software lacks it or none is picked yet.

// CMClaim and CMRelease claim or hand back area CM; Whisker has no /cm, and "" means the dashboard hides the control.
func CMClaim(sw ServerSoftware) string   { return cmCommand(sw, "/cm", true) }
func CMRelease(sw ServerSoftware) string { return cmCommand(sw, "/uncm", true) }

// LockArea and UnlockArea seal the area to newcomers on all known families.
func LockArea(sw ServerSoftware) string   { return cmCommand(sw, "/lock", false) }
func UnlockArea(sw ServerSoftware) string { return cmCommand(sw, "/unlock", false) }

// cmCommand returns cmd for a known software, ""  for Unknown; cmModel commands are also blank on Whisker.
func cmCommand(sw ServerSoftware, cmd string, cmModel bool) string {
	if sw == SoftwareUnknown {
		return ""
	}
	if cmModel && sw == SoftwareWhisker {
		return ""
	}
	return cmd
}

// AreaKick is /kickarea on Athena/Nyathena and /area_kick on KFO/Akashi; "" on Whisker or a blank uid.
func AreaKick(sw ServerSoftware, uid string) string {
	if uid == "" {
		return ""
	}
	switch sw {
	case SoftwareAthena, SoftwareNyathena:
		return "/kickarea " + uid
	case SoftwareTsuserver, SoftwareAkashi, SoftwareWitches:
		return "/area_kick " + uid
	default: // Whisker / unknown
		return ""
	}
}

// CommandReference lists sw's moderation and CM syntax for the dashboard panel; mirrors what the builders emit.
func CommandReference(sw ServerSoftware) []string {
	switch sw {
	case SoftwareTsuserver:
		return []string{
			`Ban — /ban <ipid> "reason" "duration"`,
			`Kick — /kick <ipid> [reason]`,
			`Area kick — /area_kick <id>`,
			`CM — /cm · /uncm · /lock · /unlock`,
		}
	case SoftwareAthena:
		return []string{
			`Ban — /ban -i <ipid> | -u <uid>  -d <dur>  reason`,
			`Kick — /kick -u <uid>  reason (required)`,
			`Area kick — /kickarea <uid>`,
			`CM — /cm · /uncm · /lock [-s] · /unlock`,
		}
	case SoftwareNyathena: // forks Athena: same syntax, richer area/CM toolkit
		return []string{
			`Ban — /ban -i <ipid> | -u <uid>  -d <dur>  reason`,
			`Kick — /kick -u <uid>  reason (required)`,
			`Area kick — /kickarea <uid>`,
			`CM — /cm · /uncm · /lock [-s] · /unlock`,
			`+ /invite · /uninvite · /lockbg · /lockmusic · /spectate · /status`,
		}
	case SoftwareAkashi:
		return []string{
			`Ban — /ban <ipid> <duration> reason`,
			`Kick — /kick <ipid> reason`,
			`Area kick — /area_kick <uid>`,
			`CM — /cm · /uncm · /lock · /unlock`,
		}
	case SoftwareWitches: // WAP forks Akashi's syntax; the difference is the IPID source
		return []string{
			`Ban — /ban <ipid> <duration> reason   (IPID auto-fills from the player list)`,
			`Kick — /kick <ipid> reason`,
			`Area kick — /area_kick <uid>`,
			`CM — /cm · /uncm · /lock · /unlock`,
		}
	case SoftwareWhisker:
		return []string{
			`Ban — /ban <uid> "reason" "duration"`,
			`Kick — /kick <uid> [reason]`,
			`Lock — /lock · /unlock`,
			`(no /cm model on Whisker)`,
		}
	default:
		return []string{`Unknown server software — use "Change" to pick one and enable commands.`}
	}
}

// banTarget picks the Athena/Nyathena flag form: IPID (offline-capable), else UID.
func banTarget(ipid, uid string) string {
	if ipid != "" {
		return "-i " + ipid
	}
	if uid != "" {
		return "-u " + uid
	}
	return ""
}

// sanitizeReason collapses newlines and replaces the double quotes that break quoted-reason parsing; may return "".
func sanitizeReason(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ", "\"", "'").Replace(s))
}

func quote(s string) string { return "\"" + s + "\"" }

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
