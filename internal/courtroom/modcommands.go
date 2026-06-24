package courtroom

import (
	"fmt"
	"strings"
)

// Server-software-aware moderation commands (#130). AO moderation is done by sending OOC slash
// commands, and the syntax DIFFERS per server software (read from the server sources — see the
// server-mod-commands research). This file builds the right command string for the detected
// software, so the dashboard's Ban / Kick buttons can't send wrong-syntax-that-silently-fails.
// Pure data + string building, SDL-free, unit-tested against every family's exact format.

// ServerSoftware identifies the AO2 server-software family, which determines the command syntax.
type ServerSoftware uint8

const (
	SoftwareUnknown   ServerSoftware = iota
	SoftwareTsuserver                // KFO / tsuserverCC / tsuserver3: IPID, positional, reason-then-duration (quoted)
	SoftwareAthena                   // Athena (Go): flags -u/-i, -d duration, reason at end (UID or IPID)
	SoftwareAkashi                   // Akashi (C++) + witches/wizards forks: IPID, positional, duration-then-reason
	SoftwareWhisker                  // Whisker (C3): UID, positional, reason-then-duration (quoted)
	// SoftwareNyathena forks Athena: the ban/kick/CM *syntax* is byte-identical (the command
	// builders share Athena's path), but it ships a richer area/CM toolkit, so it gets its own
	// reference. Stock Nyathena announces "Athena" on the wire (it inherited Athena's hard-coded
	// ID string), so it's indistinguishable from Athena UNLESS the operator sets the ID packet's
	// software field to "Nyathena" — then DetectSoftware picks it up (and the user can also select
	// it by hand). Kept last so the existing enum values stay stable.
	SoftwareNyathena
	ServerSoftwareCount
)

// String is the human label (for the dashboard's software selector).
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
	default:
		return "Unknown"
	}
}

// DetectSoftware maps the server's announced software string (Session.Software — the ID
// packet's field 1, e.g. "Akashi 1.8", "KFO-Server", "Nyathena", "Athena", "Whisker") to its
// family, so the dashboard auto-selects the right command syntax on join. Case-insensitive
// substring match (the strings carry a version too); an unrecognised one is Unknown, and the
// dashboard then asks the user to pick. Mirrors the heuristic the built-in login already uses.
func DetectSoftware(software string) ServerSoftware {
	s := strings.ToLower(software)
	switch {
	case strings.Contains(s, "akashi"): // incl. witches/wizards forks
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

// BanDuration is a friendly preset the UI offers; each maps to the software's own token format.
type BanDuration uint8

const (
	BanPerma BanDuration = iota
	Ban1Hour
	Ban6Hours
	Ban1Day
	Ban3Days
	Ban1Week
	BanDurationCount
)

// durationToken renders a preset in the software's own duration format: short tokens (6h/1d/1w)
// for the Go/C++ servers (str2duration / Akashi's parseTime both accept y/w/d/h/m/s), human
// tokens (6 hours / 1 day) for KFO (pytimeparse) and Whisker (its "3 days" example).
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
	}
	return ""
}

// BanDurationLabel is the friendly UI label for a preset (software-independent).
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
	}
	return ""
}

// BanCommand builds the OOC /ban for sw. ipid / uid are the target's identifiers (one may be
// ""); the function uses whichever the software bans by — IPID for KFO/Akashi, UID for Whisker,
// and Athena prefers IPID (offline-capable) but falls back to UID. Returns "" when the required
// identifier is missing (the caller then can't ban that target on that software). reason is
// sanitized so it can't break the quoting / command.
func BanCommand(sw ServerSoftware, ipid, uid string, dur BanDuration, reason string) string {
	d := durationToken(sw, dur)
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
	case SoftwareAkashi: // /ban <ipid> <duration> <reason>   (duration BEFORE reason)
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

// KickCommand builds the OOC /kick for sw (same identifier rules as BanCommand). A blank reason
// is allowed (the servers default it).
func KickCommand(sw ServerSoftware, ipid, uid, reason string) string {
	reason = sanitizeReason(reason)
	switch sw {
	case SoftwareTsuserver, SoftwareAkashi: // /kick <ipid> [reason]
		if ipid == "" {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("/kick %s %s", ipid, reason))
	case SoftwareAthena, SoftwareNyathena: // /kick -i <ipid> | -u <uid> <reason>
		target := banTarget(ipid, uid)
		if target == "" {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("/kick %s %s", target, reason))
	case SoftwareWhisker: // /kick <uid> [reason]
		if uid == "" {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("/kick %s %s", uid, reason))
	}
	return ""
}

// --- CM / area room controls (#130) -----------------------------------------------------------
// AO area moderation ("CM" = case maker) is OOC slash commands too, and which exist + how they
// spell the area-kick varies by software (read from the server sources). These build the exact
// command (or "" when the software doesn't have it / the user hasn't picked a software yet), so
// the dashboard's room controls match the server. Unit-tested alongside the ban/kick builders.

// CMClaim / CMRelease claim or hand back area CM. tsuserver/Athena/Akashi share /cm + /uncm;
// Whisker uses a different area model (no /cm), and Unknown stays blank until the user picks a
// software via the dashboard's Change button. "" means the dashboard hides the control.
func CMClaim(sw ServerSoftware) string   { return cmCommand(sw, "/cm", true) }
func CMRelease(sw ServerSoftware) string { return cmCommand(sw, "/uncm", true) }

// LockArea / UnlockArea seal the area to newcomers — universal across the four known families.
func LockArea(sw ServerSoftware) string   { return cmCommand(sw, "/lock", false) }
func UnlockArea(sw ServerSoftware) string { return cmCommand(sw, "/unlock", false) }

// cmCommand returns cmd for a known software, "" for Unknown; cmModel commands (/cm, /uncm) are
// additionally blank on Whisker (no CM model there).
func cmCommand(sw ServerSoftware, cmd string, cmModel bool) string {
	if sw == SoftwareUnknown {
		return ""
	}
	if cmModel && sw == SoftwareWhisker {
		return ""
	}
	return cmd
}

// AreaKick removes uid from the current area (a CM room control). Athena/Nyathena spell it
// /kickarea; KFO/Akashi use /area_kick; Whisker has no area-kick command. Returns "" when the
// software lacks it or uid is blank (the dashboard then disables the button).
func AreaKick(sw ServerSoftware, uid string) string {
	if uid == "" {
		return ""
	}
	switch sw {
	case SoftwareAthena, SoftwareNyathena:
		return "/kickarea " + uid
	case SoftwareTsuserver, SoftwareAkashi:
		return "/area_kick " + uid
	default: // Whisker / unknown
		return ""
	}
}

// CommandReference is the human "look at this server's commands" panel for the dashboard: a short
// list of sw's moderation + CM syntax, "<label> — <command form>". Mirrors what the builders emit.
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
			`Kick — /kick -i <ipid> | -u <uid>  reason`,
			`Area kick — /kickarea <uid>`,
			`CM — /cm · /uncm · /lock [-s] · /unlock`,
		}
	case SoftwareNyathena: // forks Athena: same syntax, richer area/CM toolkit
		return []string{
			`Ban — /ban -i <ipid> | -u <uid>  -d <dur>  reason`,
			`Kick — /kick -i <ipid> | -u <uid>  reason`,
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

// banTarget picks the Athena/Nyathena flag form: prefer IPID (offline-capable), else UID.
func banTarget(ipid, uid string) string {
	if ipid != "" {
		return "-i " + ipid
	}
	if uid != "" {
		return "-u " + uid
	}
	return ""
}

// sanitizeReason keeps a reason from breaking the command: collapse newlines + replace the
// double-quotes that would break the quoted-reason servers' shell-style parse, then trim. May
// return "" (kick allows a blank reason; ban defaults it).
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
