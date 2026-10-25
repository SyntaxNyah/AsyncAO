package ui

// THE IMPORT REPORT (v1.90.0 W8) — docs/wip/THEME-EDITOR-DESIGN.md §Q5.
// BOUNDED (hard rule 4) and built on the theme-apply goroutine, never on a draw.

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// themeReportCap bounds one theme's report. It is theme.NoteCap plus room for the
// client-side inputs (the missing fonts, the unbound keys, the two inherited-gap
// lines and the preserved-entry line), because the reader's own bound is what caps
// the largest contributor and a second, smaller number here would silently drop
// notes the format promised to report.
const themeReportCap = theme.NoteCap + themeReportExtras

// themeReportExtras is how many lines this file can add on top of the reader's
// own notes: fonts, unbound, per_misc, lobby_design, preserved. Spelled out so a
// sixth one cannot be added without the cap moving with it.
const themeReportExtras = 5

// themeReportGaps are the AO2 features §Q5 declares AsyncAO INHERITS rather than
// imports: recorded by the reader, applied by nothing, and — until the report
// existed — invisible to the author whose theme was quietly missing half of what
// they shipped.
var themeReportGaps = [...]struct {
	// on reads the flag off the parsed sidecar.
	on func(*theme.Sidecar) bool
	// line says what was inherited AND why, because "AsyncAO ignores this" without
	// a reason reads as a bug rather than as a boundary.
	line string
}{
	{
		on: func(s *theme.Sidecar) bool { return s.Import.LobbyDesign },
		line: "this theme ships a lobby_design.ini — AsyncAO's lobby is a phone book, not AO2's, " +
			"so its rects are preserved but not applied",
	},
	{
		on: func(s *theme.Sidecar) bool { return s.Import.PerMisc },
		line: "this theme ships per-misc scaling — the folders are preserved untouched, " +
			"but AsyncAO does not scale per misc yet",
	},
}

// buildThemeReport collects everything noticed about one applied theme.
// The ORDER is by who can act on it: the reader's own degrades first (they name a
// line in the file), then the fonts (the user can install one), then the unbound
// keys (a theme-author diagnostic). A report is read from the top.
func buildThemeReport(sc *theme.Sidecar, fontsMissing []string, unbound string) []string {
	if sc == nil && len(fontsMissing) == 0 && unbound == "" {
		return nil
	}
	out := make([]string, 0, themeReportCap)
	add := func(s string) {
		if len(out) >= themeReportCap || s == "" {
			return
		}
		out = append(out, s)
	}
	for _, n := range sc.Notes() {
		add(n)
	}
	if len(fontsMissing) > 0 {
		add("fonts this theme names but could not find: " + strings.Join(fontsMissing, ", "))
	}
	if unbound != "" {
		add(unbound)
	}
	// THE DECLARED GAPS, by name and with the reason (§Q5). Last of the actionable
	// entries because the author cannot fix these — they are ours — but named all
	// the same, because a client that quietly drops half a theme teaches its authors
	// nothing and the ecosystem never converges.
	for _, g := range themeReportGaps {
		if sc != nil && g.on(sc) {
			add(g.line)
		}
	}
	if n := len(sc.Unknown()); n > 0 {
		// A COUNT, not the list: these are `[x.*]` vendor sections and keys from a
		// newer build, they are carried through byte-for-byte, and nothing is wrong.
		what, verb := "entries", "were"
		if n == 1 {
			what, verb = "entry", "was"
		}
		add(strconv.Itoa(n) + " " + what + " this build does not read " + verb +
			" preserved unchanged (a vendor [x.*] section, or a key from a newer client)")
	}
	return out
}

// themeReportLine is the one sentence the client says about a report, or "" when
// there is nothing to say.
func themeReportLine(name string, report []string) string {
	if len(report) == 0 {
		return ""
	}
	who := name
	if who == "" {
		who = "this theme"
	}
	line := who + ": " + strconv.Itoa(len(report)) + " note"
	if len(report) != 1 {
		line += "s"
	}
	return line + " — " + report[0]
}

// noteThemeReport lands a report on the render thread: kept for the editor, and
// said out loud once per DISTINCT report.
func (a *App) noteThemeReport(name string, report []string) {
	same := sameReport(a.skin.themeReport, report)
	a.skin.themeReport = report
	if len(report) == 0 || same {
		return
	}
	// The DEBUG log gets all of them; the warn line gets the headline. Both,
	// rather than either: the log is where an author reads the whole list, the
	// line is what a player who just imported a stranger's theme actually sees.
	for _, n := range report {
		a.debug.push(n)
	}
	a.warnBundle(themeReportLine(name, report))
}

// sameReport compares two reports entry by entry. A plain loop over two bounded
// slices, not a hash: the bound is themeReportCap and this runs once per theme
// apply, so a signature would be state to keep in step for no gain.
func sameReport(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
