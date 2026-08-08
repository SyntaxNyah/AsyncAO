package ui

// IMPORTING A THEME BUNDLE (v1.90.0 W8) — docs/wip/THEME-EDITOR-DESIGN.md §Q4/§Q6.
//
// ONE concern: turning a dropped .aotheme into an applied theme, with the user
// told what is inside it BEFORE a byte is written.
//
// WHAT W2 PARKED AND THIS LANDS. W2 fixed the half that was actively wrong — a
// dropped bundle used to fall through the Settings screen's default arm and
// silently repoint the user's THEME ROOT at their Downloads folder — and left the
// extraction for the wave that could do it safely, with a note telling the user to
// unzip by hand. themes/README.md made the same promise ("one-step bundle import
// arrives with the theme editor"). This is that step.
//
// CONSENT WITHOUT A SHEET, AND WHY. The design specifies a consent panel rendered
// before extraction. A drop can land on ANY screen — lobby, courtroom, settings,
// the editor — so a panel would need a draw site on every one of them, and the
// editor's own budget of exactly two modals is already spent (design §3.1). The
// consent is therefore a TWO-DROP confirmation on the client's existing warn
// line, which is the same press-again-to-confirm idiom requestEditorExit already
// uses in this feature:
//
//	drop  → Inspect (central directory ONLY; nothing is written, nothing is
//	        decompressed but the sidecar) → a line naming the theme, the file
//	        count, both sizes, the fonts and every licence warning
//	drop again inside themeImportConsentWindow → Extract → apply
//
// The user learns exactly what the design's sheet would have told them, at the
// moment the sheet would have told them, and the confirming act is deliberate.
// The one thing it gives up is a Cancel BUTTON — cancelling is walking away, and
// the window expires on its own.
//
// SINGLE-FLIGHT AND OFF-THREAD (hard rules 2 and 4). Both stages are disk work on
// a goroutine; the job pointer IS the flag, so exactly one bundle can be in
// flight; the result channel is buffered so the goroutine never blocks on an app
// that moved on.

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// ---------------------------------------------------------------------------
// Named caps (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// themeImportConsentWindow is how long an inspected bundle stays armed for
	// its confirming second drop. Long enough to read a three-clause sentence
	// about fonts and licences, short enough that a bundle inspected and
	// forgotten cannot be imported by an unrelated drop ten minutes later.
	themeImportConsentWindow = 30 * time.Second
	// (There is deliberately NO themeImportWarnLineCap. The summary line's bound is
	// logLineMax, applied by clampLine on the way onto the shared warn line — the
	// same bound every other one-line message in this client is held to. A second
	// constant here would be a number that agreed with nothing: it would not be the
	// length anything is actually cut to, and the ordering fix in
	// themeBundleConsentLine below — call to action second, detail last — is
	// correct precisely because clampLine's bound, not this file's, is the one that
	// decides what falls off the end.)
	//
	// themeImportFontsNamed bounds how many fonts the summary names before it
	// switches to a count. Two is what fits beside everything else the line has
	// to say, and the export sheet names them all.
	themeImportFontsNamed = 2
	// bytesPerMiB / bytesPerKiB are the divisors behind every size in this file.
	bytesPerMiB = 1 << 20
	bytesPerKiB = 1 << 10
)

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

// themeImportStage names which half of the transport a job is doing. It exists
// so the LANDING can tell an inspect from an extract without a second flag that
// could disagree with the goroutine that set it.
type themeImportStage uint8

const (
	// themeImportInspect: read the central directory. WRITES NOTHING.
	themeImportInspect themeImportStage = iota
	// themeImportExtract: the user confirmed. This one writes.
	themeImportExtract
)

// themeBundleJob is one in-flight transport operation. Same shape as editSaveJob
// and editFontJob, and for the same reasons: buffered channel, pointer-as-flag.
type themeBundleJob struct {
	stage themeImportStage
	path  string
	done  chan themeBundleResult
}

// themeBundleResult is what the goroutine hands back. A VALUE with no live
// handles in it — the archive is closed before this is sent, so nothing the
// render thread touches can still be holding a file open (hard rule 2).
type themeBundleResult struct {
	manifest *themepack.Manifest
	err      error
}

// themeImport is the whole import state machine.
type themeImport struct {
	// job is the one operation in flight; nil when idle. The pointer is the flag.
	job *themeBundleJob
	// armedPath / armedAt are the CONSENT: the bundle an Inspect has already
	// described to the user, and when it said so. A second drop of the same path
	// inside themeImportConsentWindow is the confirmation.
	armedPath string
	armedAt   time.Time
	// row memoizes the Settings row's two strings on the state they are a pure
	// function of. The settings-text doctrine (themeRowText): a row that formatted
	// a string every frame would allocate sixty times a second and rasterise a
	// fresh label texture with it.
	row themeImportRowText
}

// armed reports whether path is the bundle the user has already been shown, and
// still inside its consent window.
//
// THE PATH IS PART OF THE TEST, not just the timer: dropping bundle A and then
// bundle B must describe B rather than importing A, or the confirmation would
// mean "the last thing you dropped" instead of "this thing".
func (ti *themeImport) armedFor(path string) bool {
	return ti.armedPath != "" && samePath(ti.armedPath, path) &&
		time.Since(ti.armedAt) < themeImportConsentWindow
}

// ---------------------------------------------------------------------------
// Entry point — the Settings row (the BROWSE half)
// ---------------------------------------------------------------------------

// themeImportNoteIdle is the row's resting sentence. A const, so the ordinary
// frame costs no allocation at all.
const themeImportNoteIdle = "drag a theme bundle onto the window, or browse for one you have already downloaded"

// themeImportNoteBusy is shown while a stage is in flight. Also a const: the
// result lands on the client's shared message line, which is where every other
// long operation in this client reports.
const themeImportNoteBusy = "reading the bundle… the answer appears in the message line at the top"

// themeImportRowText is the row's two strings plus the state they were built for.
type themeImportRowText struct {
	valid  bool
	armed  string // the armed bundle the strings describe, "" when none
	busy   bool
	note   string
	button string
}

// themeImportRow returns the row's current strings, rebuilding them only when the
// state they describe has moved.
//
// The EFFECTIVE armed path is used as the key, not the stored one: the consent
// window expires on a clock, so keying on themeImport.armedPath alone would leave
// the row offering to import a bundle whose consent had lapsed — a button that
// promises something the funnel would refuse.
func (a *App) themeImportRow() themeImportRowText {
	ti := &a.themeImp
	armed := ""
	if ti.armedPath != "" && ti.armedFor(ti.armedPath) {
		armed = ti.armedPath
	}
	busy := ti.job != nil
	if ti.row.valid && ti.row.armed == armed && ti.row.busy == busy {
		return ti.row
	}
	ti.row = buildThemeImportRow(armed, busy)
	return ti.row
}

// buildThemeImportRow is the strings themselves. PURE over its two inputs, so the
// row's wording is testable without a window, a renderer or a theme.
func buildThemeImportRow(armed string, busy bool) themeImportRowText {
	out := themeImportRowText{valid: true, armed: armed, busy: busy}
	switch {
	case armed != "":
		// THE CONFIRM. The bundle has been described; this button is the second act
		// that authorises the write, and the sentence says plainly that nothing has
		// been written yet — which is the one fact a user needs before pressing it.
		out.note = filepath.Base(armed) + " is ready — nothing has been written yet"
		out.button = "Import"
	case busy:
		out.note, out.button = themeImportNoteBusy, "Browse…"
	default:
		out.note, out.button = themeImportNoteIdle, "Browse…"
	}
	return out
}

// themeImportRowAction is the button. It is deliberately the SAME two acts the
// drop path has: browse-and-describe, then confirm.
//
// The confirm goes through handleThemeBundleDrop — the drop's own entry point —
// rather than starting an extract itself, so there is exactly one place that
// decides what a bundle does, and the browse and drag paths cannot drift apart
// about consent, caps or where the theme lands.
func (a *App) themeImportRowAction() {
	if p := a.themeImp.armedPath; p != "" && a.themeImp.armedFor(p) {
		a.handleThemeBundleDrop(p)
		return
	}
	a.openDemoBrowserFor(purposeThemeBundle)
}

// ---------------------------------------------------------------------------
// Entry point — the drop
// ---------------------------------------------------------------------------

// handleThemeBundleDrop is HandleFileDrop's .aotheme arm (dropclaim.go names it
// the single owner). It is the only way a bundle enters the client.
//
// It is TWO functions, and the split is a seam rather than a style choice: "where
// may I write themes?" is a question about portability and preferences that only
// internal/config can answer, and a gate that had to answer it would either write
// into the developer's real AppData or re-implement the policy. So the resolution
// lives here, in one line, and every decision the import makes lives in
// bundleDrop, which takes the root as an argument.
func (a *App) handleThemeBundleDrop(path string) {
	a.bundleDrop(path, config.UserThemesDir())
}

// bundleDrop is the whole of what a dropped bundle does, against a write root the
// caller resolved.
func (a *App) bundleDrop(path, root string) {
	if a.themeImp.job != nil {
		a.warnBundle("A theme bundle is already being " + a.themeImp.job.stageWord() + " — let it finish first.")
		return
	}
	if root == "" {
		// NAME THE OFFENDER, NAME THE LIMIT, OFFER THE FIX (design §3.3).
		a.warnBundle("Cannot import " + filepath.Base(path) +
			": this install has nowhere to write themes. Set a theme folder in Settings → Theme.")
		return
	}
	stage := themeImportInspect
	if a.themeImp.armedFor(path) {
		stage = themeImportExtract
		a.themeImp.armedPath = "" // consent is spent by the act it authorises
	}
	a.startThemeBundleJob(stage, path, root)
}

// startThemeBundleJob runs one stage off the render thread.
//
// THE WRITE ROOT IS RESOLVED HERE, on the render thread, and handed in: config
// reads preferences, and the goroutine must not take the preferences lock. It is
// the same discipline requestThemeCatalog uses for the custom root.
func (a *App) startThemeBundleJob(stage themeImportStage, path, root string) {
	job := &themeBundleJob{stage: stage, path: path, done: make(chan themeBundleResult, 1)}
	a.themeImp.job = job
	go func() {
		var m *themepack.Manifest
		var err error
		if stage == themeImportExtract {
			m, err = themepack.Extract(path, root)
		} else {
			m, err = themepack.Inspect(path)
		}
		job.done <- themeBundleResult{manifest: m, err: err}
	}()
	if stage == themeImportInspect {
		a.warnBundle("Reading " + filepath.Base(path) + "…")
	} else {
		a.warnBundle("Importing " + filepath.Base(path) + "…")
	}
}

// stageWord is the in-flight verb, for the "already running" refusal.
func (j *themeBundleJob) stageWord() string {
	if j.stage == themeImportExtract {
		return "imported"
	}
	return "read"
}

// ---------------------------------------------------------------------------
// Landing
// ---------------------------------------------------------------------------

// pollThemeBundle lands a finished stage. Called once per frame from App.Frame,
// beside pollThemeApply and for the same reason: a result that arrives between
// frames must not touch UI state from another goroutine.
func (a *App) pollThemeBundle() {
	if a.themeImp.job == nil {
		return
	}
	select {
	case res := <-a.themeImp.job.done:
		a.landThemeBundle(res)
	default:
	}
}

// landThemeBundle is the ONE place a finished stage changes app state, so the
// polled path and any awaited one cannot drift.
func (a *App) landThemeBundle(res themeBundleResult) {
	job := a.themeImp.job
	a.themeImp.job = nil
	if res.err != nil {
		// EVERY REFUSAL SAYS WHY. themepack's errors already name the cap, the
		// number and the offending entry; the file name is prefixed so a user with
		// two bundles knows which one this is about.
		a.warnBundle(filepath.Base(job.path) + ": " + res.err.Error())
		return
	}
	if job.stage == themeImportInspect {
		a.themeImp.armedPath = job.path
		a.themeImp.armedAt = time.Now()
		a.warnBundle(themeBundleConsentLine(res.manifest, filepath.Base(job.path)))
		return
	}
	a.applyImportedTheme(res.manifest)
}

// applyImportedTheme is the landing of a successful extraction: the theme is on
// disk, so select it, apply it live, and rescan the catalog.
//
// LIVE, NOT NEXT LAUNCH. This is deliberately NOT ImportSettings' freeze-and-
// apply-next-launch semantics (design §Q4): the whole experience is "your friend
// sends you a theme and two drops later you are looking at it".
func (a *App) applyImportedTheme(m *themepack.Manifest) {
	if m == nil || m.Root == "" {
		a.warnBundle("The bundle imported but named no theme — open Settings → Theme and pick it.")
		return
	}
	// The CUSTOM root is left exactly as it was. An import lands under
	// config.UserThemesDir(), which themeLoadRoots already walks, so repointing
	// anything here would be the W2 bug arriving by the front door.
	_, customRoot := a.d.Prefs.Theme()
	a.d.Prefs.SetTheme(m.Root, customRoot)
	settings.themeName = m.Root
	// The gen is armed for the #35 window snap, exactly as a deliberate pick from
	// the dropdown is: an imported theme with a different canvas is the same event.
	a.themeResizeArmGen = a.applyThemeAsync()
	// The CATALOG is refreshed (unconditional — the drop knows what it just wrote,
	// which is the whole reason themeScanAfterDrop exists). The settings screen's
	// dropdown LIST is deliberately not: scanThemes' result is drained only by
	// pollThemeScan, which runs on the settings screen, so kicking one from a drop
	// on the courtroom would park a goroutine on a channel nobody is reading. The
	// picker rescans on open anyway (themeScanOnOpen), which is the first moment
	// anybody can look at it.
	a.requestThemeCatalog(themeScanAfterDrop)
	a.warnBundle(themeBundleImportedLine(m))
}

// warnBundle posts one line on the client's shared warn channel and stamps it.
// Named rather than inlined because five call sites set the same pair, and a
// warnLine written without its warnAt is a line that never disappears.
func (a *App) warnBundle(line string) {
	a.warnLine = clampLine(line)
	a.warnAt = time.Now()
}

// ---------------------------------------------------------------------------
// The two sentences
// ---------------------------------------------------------------------------

// themeBundleConsentLine is what the design's consent sheet would have said,
// on one line: what it is, how big it is both ways, and the FONT LICENCE
// question — which is the real legal hazard in sharing a theme and gets named
// every time rather than being a footnote in a panel nobody scrolls.
func themeBundleConsentLine(m *themepack.Manifest, file string) string {
	if m == nil {
		return file + ": nothing readable in it."
	}
	var b strings.Builder
	b.WriteString("Import ")
	b.WriteString(themeBundleTitle(m))
	// THE CALL TO ACTION COMES SECOND, not last, and that ordering is a bug fix
	// rather than a preference: the warn line is CLAMPED to one line's worth of
	// text (clampLine), and with the instruction at the end a bundle carrying two
	// fonts pushed "Drop it again to import" off the edge — a consent prompt that
	// never says how to consent. Everything after this point is detail that can be
	// truncated without costing the user the ability to act.
	b.WriteString("? Drop it again to import. ")
	b.WriteString(strconv.Itoa(m.Files))
	b.WriteString(" files · ")
	b.WriteString(humanBytes(m.ZipBytes))
	b.WriteString(" packed / ")
	b.WriteString(humanBytes(m.Bytes))
	b.WriteString(" on disk")
	if n := len(m.Fonts); n > 0 {
		b.WriteString(" · ")
		b.WriteString(strconv.Itoa(n))
		b.WriteString(" font")
		if n != 1 {
			b.WriteString("s")
		}
		if named := fontsMissingLicense(m); named != "" {
			b.WriteString(" (")
			b.WriteString(named)
			b.WriteString(")")
		}
	}
	return b.String()
}

// themeBundleImportedLine is the receipt: where it went and what to do next.
func themeBundleImportedLine(m *themepack.Manifest) string {
	line := "Imported " + themeBundleTitle(m) + " as \"" + m.Root + "\" and applied it"
	if len(m.Warnings) > 0 {
		line += " · " + strconv.Itoa(len(m.Warnings)) + " note"
		if len(m.Warnings) != 1 {
			line += "s"
		}
		line += ": " + m.Warnings[0]
	}
	return line
}

// themeBundleTitle is the theme's display name when it has one, else its folder.
// The folder is the IDENTITY (docs/THEME-FORMAT.md §1); the name is the label,
// and a bundle with no sidecar has only the former.
func themeBundleTitle(m *themepack.Manifest) string {
	if m.Name != "" {
		return m.Name
	}
	return m.Root
}

// fontsMissingLicense names the bundled faces with no licence file beside them,
// bounded by themeImportFontsNamed.
//
// OFL specifically requires the licence to travel with the font, so the person
// re-sharing this bundle is the one who would be breaking the terms. Naming the
// files is what turns that from a legal abstraction into something fixable.
func fontsMissingLicense(m *themepack.Manifest) string {
	var named []string
	extra := 0
	for _, f := range m.Fonts {
		if f.LicenseFile != "" {
			continue
		}
		if len(named) >= themeImportFontsNamed {
			extra++
			continue
		}
		named = append(named, filepath.Base(f.File))
	}
	if len(named) == 0 {
		return ""
	}
	out := strings.Join(named, ", ")
	if extra > 0 {
		out += " and " + strconv.Itoa(extra) + " more"
	}
	return out + " ship without a licence file"
}

// humanBytes formats a byte count for a human.
//
// MB with one decimal place, because "6.4 MB" is the number that decides whether a
// theme is shareable and "6 MB" is not — but KB BELOW A MEGABYTE, because a
// two-file theme rendered as "0.0 MB packed / 0.0 MB on disk", which reads as a
// broken import rather than as a small one.
func humanBytes(n int64) string {
	switch {
	case n <= 0:
		return "0 KB"
	case n < bytesPerMiB:
		kb := (n + bytesPerKiB - 1) / bytesPerKiB // round UP: nothing real is "0 KB"
		return strconv.FormatInt(kb, 10) + " KB"
	default:
		whole := n / bytesPerMiB
		tenths := (n % bytesPerMiB) * 10 / bytesPerMiB
		return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(tenths, 10) + " MB"
	}
}
