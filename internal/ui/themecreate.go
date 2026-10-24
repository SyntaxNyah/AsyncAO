package ui

// THE THEME CREATOR (v1.90.0 W10) — "make a theme from nothing", from Settings.
// OFF THE RENDER THREAD (hard rule 2) and single-flight (hard rule 4): the job
// pointer IS the flag, the channel is buffered so the goroutine never blocks on an
// app that moved on. themeCopyJob's shape exactly.

import (
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// ---------------------------------------------------------------------------
// Named constants (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// themeCreateFallbackName is what an unnamed theme is called. A create with an
	// empty field is a slip, not a refusal — themepack collision-suffixes, so
	// pressing Create twice gives "my theme" and "my theme (2)" rather than an error
	// nobody reads.
	themeCreateFallbackName = "My theme"

	// themeCreateNameRuneCap bounds the name field. An ALIAS of the sanitizer's own
	// bound rather than a second number: themepack.SanitizeName truncates at
	// NameRuneCap, so a field that accepted more would show the user a name the
	// folder is never going to have.
	themeCreateNameRuneCap = themepack.NameRuneCap
)

// ---------------------------------------------------------------------------
// Templates — the starting points
// ---------------------------------------------------------------------------

// themeCreateTemplate is WHICH starting point a new theme is cut from.
//
// A SMALL ORDERED ENUM with a name table beside it, so the picker is a cycler over
// the enum rather than a list of strings some other switch has to stay in step
// with. themeCreateTemplateCount sizes both tables, which is what makes adding a
// fourth template a compile error until its label and its note exist.
type themeCreateTemplate uint8

const (
	// templateBlank: the format's empty document. Everything comes from the AO2
	// base the loader resolves, and the editor writes on top of it.
	templateBlank themeCreateTemplate = iota
	// templatePreset: W9's layout × style compose funnel, materialised into a
	// folder. The pair is picked by the two cyclers beside the row.
	templatePreset
	// templateCopyCurrent: the applied theme, copied into your themes folder. NOT a
	// create (see the file header) — it hands off to themecopy.go.
	templateCopyCurrent
	// themeCreateTemplateCount sizes the tables; never a real template.
	themeCreateTemplateCount
)

// themeCreateTemplateNames is the cycler's labels.
var themeCreateTemplateNames = [themeCreateTemplateCount]string{
	"Blank", "From a preset", "Copy of the applied theme",
}

// themeCreateTemplateNotes is the one line under the picker: what you get. Package
// constants in a fixed array, so the row costs no allocation to draw (the
// settings-text doctrine — see themeRowText).
var themeCreateTemplateNotes = [themeCreateTemplateCount]string{
	"an empty theme — everything still comes from the AO2 base, and you add to it",
	"a full theme built from one of the shipped layout × style pairs, ready to edit",
	"a private copy of the theme on screen, with its files and your layout tweaks",
}

// String makes the enum printable for a refusal line without a second table.
func (t themeCreateTemplate) String() string {
	if int(t) >= len(themeCreateTemplateNames) {
		return "?"
	}
	return themeCreateTemplateNames[t]
}

// ---------------------------------------------------------------------------
// The job
// ---------------------------------------------------------------------------

// themeCreateIntent is what the landing does once the folder exists. It exists for
// the reason themeCopyIntent does: one landing, one flag, set by the caller that
// started the job, so the act cannot disagree with whoever asked for it.
type themeCreateIntent uint8

const (
	// themeCreateThenNote: the preset gallery. The theme is written and named in a
	// chip; the editor stays where it is, over the theme the user is still editing.
	// Opening the new one would throw away that session's unsaved work.
	themeCreateThenNote themeCreateIntent = iota
	// themeCreateThenOpen: Settings pressed "Create". The theme is applied and the
	// editor opens over it — a create that stops at "it is in the list" has done
	// half of what the button offered.
	themeCreateThenOpen
)

// themeCreateJob is one in-flight folder write.
type themeCreateJob struct {
	// name is the DISPLAY name asked for. themepack sanitizes and suffixes it, so
	// the folder that lands may be called something else; the manifest is the truth.
	name   string
	intent themeCreateIntent
	done   chan themeCreateResult
}

// themeCreateResult is what the goroutine hands back — a VALUE with no live
// handles in it, exactly as themeCopyResult is.
type themeCreateResult struct {
	manifest *themepack.Manifest
	err      error
}

// ---------------------------------------------------------------------------
// Building the model
// ---------------------------------------------------------------------------

// newThemeSidecarFrom builds the MODEL a template starts from, or the reason it
// cannot. PURE except for the preset library read, and that is behind a sync.Once
// over embedded bytes (theme.ShippedPresets) rather than the disk.
//
// templateCopyCurrent is absent BY CONSTRUCTION rather than by omission: it has no
// model to build (see the file header), so asking for one is a caller bug and says
// so, instead of quietly producing a blank theme with the right name on it.
func newThemeSidecarFrom(t themeCreateTemplate, display string, pair theme.PresetPair) (*theme.Sidecar, error) {
	switch t {
	case templatePreset:
		if pair.Empty() {
			return nil, errThemeCreateNoPair
		}
		return theme.NewThemeFromPresets(display, pair)
	case templateBlank:
		sc := theme.NewSidecar()
		// THE ONE FIELD A BLANK THEME CARRIES. Identity is the FOLDER name
		// (docs/THEME-FORMAT.md §1); this is the display string, and it is what the
		// picker and the editor header show. Everything else stays absent, which is
		// what makes the file that lands the smallest one this format can express and
		// still read back with zero notes.
		sc.Meta.Name = display
		return sc, nil
	}
	return nil, errThemeCreateNoModel
}

// The two refusals newThemeSidecarFrom can produce. Package-level values rather
// than formatted strings: they are compared in a gate and drawn in a chip.
var (
	errThemeCreateNoPair  = errThemeCreate("pick a layout and a style first")
	errThemeCreateNoModel = errThemeCreate("that starting point does not build a theme on its own")
)

// errThemeCreate is a one-line refusal. A named type so a gate can assert on the
// VALUE rather than on the sentence, which is free to be reworded.
type errThemeCreate string

func (e errThemeCreate) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// The funnel — the ONE place internal/ui mints a theme folder
// ---------------------------------------------------------------------------

// startThemeCreate writes sc into a new folder under this install's write root.
// Returns whether a job was started, so a caller that has UI state to clear (the
// Settings name field) can tell a start from a refusal without re-deriving it.
func (a *App) startThemeCreate(name string, sc *theme.Sidecar, intent themeCreateIntent) bool {
	if a.skin.themeCreate != nil {
		a.noteThemeCopy("a theme is already being created — let it finish first")
		return false
	}
	if sc == nil {
		a.noteThemeCopy("there is nothing to build a theme from")
		return false
	}
	cat := a.themeCatalogSnapshot()
	if cat == nil || cat.WriteRoot == "" {
		// The catalog is the only thing that knows where this client may write. Kick
		// the scan the answer needs rather than refusing twice.
		a.requestThemeCatalog(themeScanExplicit)
		a.noteThemeCopy("this install has nowhere to write themes yet. Set a theme folder in Settings ▸ Theme.")
		return false
	}
	want := strings.TrimSpace(name)
	if want == "" {
		want = themeCreateFallbackName
	}
	// SanitizeName is the ONE spelling of folder-name legality (themepack/pack.go's
	// own note: "this is where identity is decided"). It is called here as well as
	// inside CreateFromSidecar — not to duplicate the decision but to REFUSE before
	// a goroutine exists, so a name made entirely of characters a folder may not
	// carry produces a sentence instead of a job that fails four frames later.
	if themepack.SanitizeName(want) == "" {
		a.noteThemeCopy("\"" + want + "\" has no characters a folder name can use — try letters and numbers")
		return false
	}
	root := cat.WriteRoot
	job := &themeCreateJob{name: want, intent: intent, done: make(chan themeCreateResult, 1)}
	a.skin.themeCreate = job
	go func() {
		m, err := themepack.CreateFromSidecar(root, job.name, sc)
		job.done <- themeCreateResult{manifest: m, err: err}
	}()
	a.noteThemeCopy("creating " + want + "…")
	return true
}

// startThemeCreateFromTemplate is the SETTINGS entry point: build the model the
// template describes, then mint it.
//
// templateCopyCurrent is routed to themecopy.go here rather than inside the funnel,
// because it is a different act with a different landing (a copy carries files and
// layout overrides) and the funnel's job is to be the one MINT, not the one button.
func (a *App) startThemeCreateFromTemplate(t themeCreateTemplate, name string, pair theme.PresetPair) bool {
	if t == templateCopyCurrent {
		a.copyAppliedThemeForEditing(themeCopyThenOpen)
		return true
	}
	display := strings.TrimSpace(name)
	if display == "" {
		display = themeCreateFallbackName
	}
	sc, err := newThemeSidecarFrom(t, display, pair)
	if err != nil {
		a.noteThemeCopy(err.Error())
		return false
	}
	return a.startThemeCreate(display, sc, themeCreateThenOpen)
}

// ---------------------------------------------------------------------------
// Landing
// ---------------------------------------------------------------------------

// pollThemeCreate lands a finished create. Called once per frame from App.Frame,
// beside pollThemeCopy and for the same reason: a result that arrives between
// frames must not touch UI state from another goroutine.
func (a *App) pollThemeCreate() {
	if a.skin.themeCreate == nil {
		return
	}
	select {
	case res := <-a.skin.themeCreate.done:
		a.landThemeCreate(res)
	default:
	}
}

// landThemeCreate is the ONE place a finished create changes app state.
//
// THE RESCAN IS REQUESTED LAST (deferred), for the reason landThemeCopy spells
// out: a scan publishes a snapshot built before this folder existed, and the seed
// below is what lets the very next Save resolve a theme that is sitting on disk.
func (a *App) landThemeCreate(res themeCreateResult) {
	job := a.skin.themeCreate
	a.skin.themeCreate = nil
	if res.err != nil {
		a.noteThemeCopy("could not create " + job.name + ": " + res.err.Error())
		return
	}
	m := res.manifest
	if m == nil || m.Root == "" || m.Dir == "" {
		a.noteThemeCopy("the new theme named no folder — open Settings ▸ Theme and pick it")
		return
	}
	defer a.requestThemeCatalog(themeScanAfterDrop)
	// THE CATALOG LEARNS ABOUT IT before anything asks. noteCopiedTheme is the same
	// copy-on-write + CAS seed a copy makes, and it is called with an EMPTY source
	// name deliberately: there is no source theme, so the badge derives from the
	// zero entry — themeKindAfterCopy(themeKindBroken) is themeKindNative, which is
	// exactly what a folder holding one asyncao_theme.ini is.
	a.noteCopiedTheme("", m)
	if job.intent == themeCreateThenNote {
		a.noteThemeCopy("created " + m.Root + " — it is in Settings ▸ Theme")
		return
	}
	// IT BECOMES THE APPLIED THEME, and then the editor opens over it through the
	// SAME arming maybeOpenEditorAfterCopy already owns (themecopy.go): one
	// generation, consumed when its own apply lands, disarmed when a later apply
	// outraces it. A second open path here would be a second set of those rails.
	//
	// The CUSTOM root is left exactly as it was — the folder landed under the write
	// root themeLoadRoots already walks, so repointing anything would be the W2 bug
	// arriving by the front door.
	if a.d.Prefs != nil {
		_, customRoot := a.d.Prefs.Load().Theme()
		a.d.Prefs.SetTheme(m.Root, customRoot)
	}
	settings.themeName = m.Root
	gen := a.applyThemeAsync()
	a.skin.themeEditArmGen, a.skin.themeEditArmFrom = gen, a.screen
	a.noteThemeCopy("Created " + m.Root + " — opening the editor over it")
}
