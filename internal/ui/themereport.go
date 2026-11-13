package ui

// The import report: everything the client noticed about a theme and would otherwise not say.
// Inputs are the sidecar's Notes() and Unknown(), res.themeFontsMissing, res.unbound and the sidecar [import] flags
// for features AsyncAO inherits rather than imports (`per_misc`, `lobby_design`), each named with its reason.
// The `_sharp` font keys are absent on purpose: they live in the courtroom_design.ini font tier, per element id.
// The report lands first on the warn line (count, then first note); the editor's panel reads App.themeReport.
// Bounded (hard rule 4) and built on the theme-apply goroutine, never on a draw.

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// themeReportCap bounds one theme's report: theme.NoteCap plus the lines this file adds, so notes the format
// promised to report are not dropped.
const themeReportCap = theme.NoteCap + themeReportExtras

// themeReportExtras is how many lines this file adds to the reader's notes: fonts, unbound, per_misc, lobby_design,
// preserved. A sixth must move the cap.
const themeReportExtras = 5

// themeReportGaps are AO2 features AsyncAO inherits rather than imports: recorded by the reader, applied by nothing.
// It is a table over the sidecar's flags so a new gap is a row.
var themeReportGaps = [...]struct {
	// on reads the flag off the parsed sidecar.
	on func(*theme.Sidecar) bool
	// line says what was inherited and why, so it reads as a boundary rather than a bug.
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
// It is goroutine-only: it runs inside applyThemeAsync's worker over the sidecar that worker parsed.
// Order is by who can act: the reader's degrades, then the missing fonts, then the unbound keys.
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
	// The declared gaps come last: the author cannot fix them, but they are named all the same.
	for _, g := range themeReportGaps {
		if sc != nil && g.on(sc) {
			add(g.line)
		}
	}
	if n := len(sc.Unknown()); n > 0 {
		// A count, not the list: `[x.*]` vendor sections and newer-build keys are carried through byte-for-byte.
		what, verb := "entries", "were"
		if n == 1 {
			what, verb = "entry", "was"
		}
		add(strconv.Itoa(n) + " " + what + " this build does not read " + verb +
			" preserved unchanged (a vendor [x.*] section, or a key from a newer client)")
	}
	return out
}

// themeReportLine is the one sentence the client says about a report, or "" when there is nothing to say.
// It names the first note as well as the count, since the first note is the likeliest reason the theme looks wrong.
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

// noteThemeReport lands a report on the render thread: kept for the editor, and said out loud once per distinct report.
// Not once per apply: healTheme, the texture-filter row and boot all re-kick applies, which would nag.
func (a *App) noteThemeReport(name string, report []string) {
	same := sameReport(a.themeReport, report)
	a.themeReport = report
	if len(report) == 0 || same {
		return
	}
	// The debug log gets every note; the warn line gets the headline.
	for _, n := range report {
		a.pushDebug(n)
	}
	a.warnBundle(themeReportLine(name, report))
}

// sameReport compares two reports entry by entry; both are bounded by themeReportCap, so a signature is not worth it.
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
