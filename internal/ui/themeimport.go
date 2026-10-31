package ui

// A dropped .aotheme is inspected first (central directory only, nothing written); a second drop of the same bundle
// inside themeImportConsentWindow extracts and applies it. The two-drop confirmation avoids a consent panel, which
// would need a draw site on every screen a drop can land on.
// Both stages are disk work on a goroutine (hard rules 2 and 4); the job pointer is the single-flight flag.

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

const (
	// themeImportConsentWindow is how long an inspected bundle stays armed for its confirming second drop; short enough
	// that an unrelated drop later cannot import a forgotten bundle.
	themeImportConsentWindow = 30 * time.Second
	// themeImportFontsNamed bounds how many fonts the summary names before it switches to a count.
	themeImportFontsNamed = 2
	// bytesPerMiB / bytesPerKiB are the divisors behind every size in this file.
	bytesPerMiB = 1 << 20
	bytesPerKiB = 1 << 10
)

// themeImportStage names which half of the transport a job is doing.
type themeImportStage uint8

const (
	// themeImportInspect: read the central directory. WRITES NOTHING.
	themeImportInspect themeImportStage = iota
	// themeImportExtract: the user confirmed. This one writes.
	themeImportExtract
)

// themeBundleJob is one in-flight transport operation: buffered channel, pointer as flag.
type themeBundleJob struct {
	stage themeImportStage
	path  string
	done  chan themeBundleResult
}

// themeBundleResult is what the goroutine hands back; the archive is closed before it is sent (hard rule 2).
type themeBundleResult struct {
	manifest *themepack.Manifest
	err      error
}

// themeImport is the whole import state machine.
type themeImport struct {
	// job is the one operation in flight; nil when idle. The pointer is the flag.
	job *themeBundleJob
	// armedPath / armedAt: the bundle an Inspect already described and when; a second drop of it inside
	// themeImportConsentWindow is the confirmation.
	armedPath string
	armedAt   time.Time
	// row memoizes the Settings row's strings so the frame does not allocate.
	row themeImportRowText
}

// armed reports whether path is the bundle already shown and still inside its consent window.
// The path is part of the test so dropping bundle B after A describes B instead of importing A.
func (ti *themeImport) armedFor(path string) bool {
	return ti.armedPath != "" && samePath(ti.armedPath, path) &&
		time.Since(ti.armedAt) < themeImportConsentWindow
}

// themeImportNoteIdle is the row's resting sentence.
const themeImportNoteIdle = "drag a theme bundle onto the window, or browse for one you have already downloaded"

// themeImportNoteBusy is shown while a stage is in flight.
const themeImportNoteBusy = "reading the bundle… the answer appears in the message line at the top"

// themeImportRowText is the row's two strings plus the state they were built for.
type themeImportRowText struct {
	valid  bool
	armed  string // the armed bundle the strings describe, "" when none
	busy   bool
	note   string
	button string
}

// themeImportRow returns the row's strings, rebuilding them only when the state they describe has moved.
// It keys on the effective armed path: the window expires on a clock, so the stored path would offer a lapsed import.
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

// buildThemeImportRow builds the strings; pure over its inputs.
func buildThemeImportRow(armed string, busy bool) themeImportRowText {
	out := themeImportRowText{valid: true, armed: armed, busy: busy}
	switch {
	case armed != "":
		// The confirm button; the sentence says nothing has been written yet.
		out.note = filepath.Base(armed) + " is ready — nothing has been written yet"
		out.button = "Import"
	case busy:
		out.note, out.button = themeImportNoteBusy, "Browse…"
	default:
		out.note, out.button = themeImportNoteIdle, "Browse…"
	}
	return out
}

// themeImportRowAction is the button: browse and describe, then confirm.
// The confirm goes through handleThemeBundleDrop so browse and drag share one consent decision.
func (a *App) themeImportRowAction() {
	if p := a.themeImp.armedPath; p != "" && a.themeImp.armedFor(p) {
		a.handleThemeBundleDrop(p)
		return
	}
	a.openDemoBrowserFor(purposeThemeBundle)
}

// handleThemeBundleDrop is HandleFileDrop's .aotheme arm and the only way a bundle enters the client.
// The write root is resolved here because only internal/config knows it; bundleDrop takes it as an argument.
func (a *App) handleThemeBundleDrop(path string) {
	a.bundleDrop(path, config.UserThemesDir())
}

// bundleDrop is what a dropped bundle does, against a write root the caller resolved.
func (a *App) bundleDrop(path, root string) {
	if a.themeImp.job != nil {
		a.warnBundle("A theme bundle is already being " + a.themeImp.job.stageWord() + " — let it finish first.")
		return
	}
	if root == "" {
		// Name the offender, the limit and the fix.
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
// The write root is resolved on the render thread: config reads preferences, which the goroutine must not lock.
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

// pollThemeBundle lands a finished stage; called once per frame from App.Frame so UI state stays on the render thread.
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

// landThemeBundle is the one place a finished stage changes app state.
func (a *App) landThemeBundle(res themeBundleResult) {
	job := a.themeImp.job
	a.themeImp.job = nil
	if res.err != nil {
		// The error names the cap and entry; the file name is prefixed to tell bundles apart.
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

// applyImportedTheme selects and applies the extracted theme live, and rescans the catalog.
func (a *App) applyImportedTheme(m *themepack.Manifest) {
	if m == nil || m.Root == "" {
		a.warnBundle("The bundle imported but named no theme — open Settings → Theme and pick it.")
		return
	}
	// The custom root is left alone: imports land under config.UserThemesDir(), which themeLoadRoots already walks.
	_, customRoot := a.d.Prefs.Load().Theme()
	a.d.Prefs.SetTheme(m.Root, customRoot)
	settings.themeName = m.Root
	// The gen is armed for the window snap, as a dropdown pick is.
	a.themeResizeArmGen = a.applyThemeAsync()
	// The catalog refreshes unconditionally; the dropdown list does not, because scanThemes' result is only drained on
	// the settings screen and a goroutine started from the courtroom would park on an unread channel.
	a.requestThemeCatalog(themeScanAfterDrop)
	a.warnBundle(themeBundleImportedLine(m))
}

// warnBundle posts one line on the shared warn channel and stamps it; a warnLine without its warnAt never disappears.
func (a *App) warnBundle(line string) {
	a.warnLine = clampLine(line)
	a.warnAt = time.Now()
}

// themeBundleConsentLine is the one-line consent summary: size both ways and the font licence question.
func themeBundleConsentLine(m *themepack.Manifest, file string) string {
	if m == nil {
		return file + ": nothing readable in it."
	}
	var b strings.Builder
	b.WriteString("Import ")
	b.WriteString(themeBundleTitle(m))
	// The call to action comes second because clampLine cuts the end of the line, and the instruction must survive.
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

// themeBundleTitle is the display name when it has one, else the folder, which is the identity.
func themeBundleTitle(m *themepack.Manifest) string {
	if m.Name != "" {
		return m.Name
	}
	return m.Root
}

// fontsMissingLicense names the bundled faces with no licence file beside them, bounded by themeImportFontsNamed.
// OFL requires the licence to travel with the font.
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

// humanBytes formats a byte count: MB with one decimal, KB below a megabyte so small themes do not read 0.0 MB.
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
