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
	SoftwareAthena                   // Athena / Nyathena (Go): flags -u/-i, -d duration, reason at end (UID or IPID)
	SoftwareAkashi                   // Akashi (C++) + witches/wizards forks: IPID, positional, duration-then-reason
	SoftwareWhisker                  // Whisker (C3): UID, positional, reason-then-duration (quoted)
	ServerSoftwareCount
)

// String is the human label (for the dashboard's software selector).
func (s ServerSoftware) String() string {
	switch s {
	case SoftwareTsuserver:
		return "KFO / tsuserver"
	case SoftwareAthena:
		return "Athena / Nyathena"
	case SoftwareAkashi:
		return "Akashi"
	case SoftwareWhisker:
		return "Whisker"
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
	case strings.Contains(s, "athena"): // covers Nyathena
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
	case SoftwareAthena: // /ban -i <ipid> | -u <uid>  -d <duration> <reason>
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
	case SoftwareAthena: // /kick -i <ipid> | -u <uid> <reason>
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
