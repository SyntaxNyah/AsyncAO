package ui

// The theme creator: mints a new theme folder from a picked starting point. startThemeCreate is the one place
// internal/ui calls themepack.CreateFromSidecar, so the catalog seed, collision suffix and write-root refusal can't be
// forgotten by a second path.
// "Copy of the applied theme" does not come through here: a copy has a source folder, so its files, layout overrides
// and provenance would be dropped by CreateFromSidecar; themecopy.go performs it.
// Off the render thread (hard rule 2) and single-flight (hard rule 4): the job pointer is the flag, and the channel is
// buffered so the goroutine never blocks.

import (
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

const (
	// themeCreateFallbackName names an unnamed theme; themepack collision-suffixes, so Create twice gives "My theme
	// (2)".
	themeCreateFallbackName = "My theme"

	// themeCreateNameRuneCap aliases the sanitizer's bound: a field accepting more would show a name the folder never
	// gets.
	themeCreateNameRuneCap = themepack.NameRuneCap
)

// themeCreateTemplate is which starting point a new theme is cut from. themeCreateTemplateCount sizes the name tables.
type themeCreateTemplate uint8

const (
	// templateBlank is the format's empty document; everything comes from the AO2 base.
	templateBlank themeCreateTemplate = iota
	// templatePreset is the layout × style compose, written to a folder.
	templatePreset
	// templateCopyCurrent copies the applied theme; it hands off to themecopy.go.
	templateCopyCurrent
	// themeCreateTemplateCount sizes the tables; never a real template.
	themeCreateTemplateCount
)

// themeCreateTemplateNames is the cycler's labels.
var themeCreateTemplateNames = [themeCreateTemplateCount]string{
	"Blank", "From a preset", "Copy of the applied theme",
}

// themeCreateTemplateNotes is the one line under the picker. Package constants, so the row allocates nothing.
var themeCreateTemplateNotes = [themeCreateTemplateCount]string{
	"an empty theme — everything still comes from the AO2 base, and you add to it",
	"a full theme built from one of the shipped layout × style pairs, ready to edit",
	"a private copy of the theme on screen, with its files and your layout tweaks",
}

func (t themeCreateTemplate) String() string {
	if int(t) >= len(themeCreateTemplateNames) {
		return "?"
	}
	return themeCreateTemplateNames[t]
}

// themeCreateIntent is what the landing does once the folder exists, set by the caller that started the job.
type themeCreateIntent uint8

const (
	// themeCreateThenNote is the preset gallery: name it in a chip and stay, since opening it would discard the current
	// editor's unsaved work.
	themeCreateThenNote themeCreateIntent = iota
	// themeCreateThenOpen is Settings' "Create": apply the theme and open the editor over it.
	themeCreateThenOpen
)

type themeCreateJob struct {
	// name is the display name asked for; themepack sanitizes and suffixes it, so the manifest is the truth.
	name   string
	intent themeCreateIntent
	done   chan themeCreateResult
}

// themeCreateResult is what the goroutine hands back: a value with no live handles.
type themeCreateResult struct {
	manifest *themepack.Manifest
	err      error
}

// newThemeSidecarFrom builds the model a template starts from. templateCopyCurrent has no model, so asking for one is a
// caller bug and returns an error rather than a blank theme.
func newThemeSidecarFrom(t themeCreateTemplate, display string, pair theme.PresetPair) (*theme.Sidecar, error) {
	switch t {
	case templatePreset:
		if pair.Empty() {
			return nil, errThemeCreateNoPair
		}
		return theme.NewThemeFromPresets(display, pair)
	case templateBlank:
		sc := theme.NewSidecar()
		// Identity is the folder name (docs/THEME-FORMAT.md); this is the display string the picker and editor header
		// show.
		sc.Meta.Name = display
		return sc, nil
	}
	return nil, errThemeCreateNoModel
}

// The refusals newThemeSidecarFrom can produce.
var (
	errThemeCreateNoPair  = errThemeCreate("pick a layout and a style first")
	errThemeCreateNoModel = errThemeCreate("that starting point does not build a theme on its own")
)

// errThemeCreate is a one-line refusal, a named type so it can be compared by value.
type errThemeCreate string

func (e errThemeCreate) Error() string { return string(e) }

// startThemeCreate writes sc into a new folder under the catalog's write root and reports whether a job started. Every
// refusal is a visible line naming the offender and the fix.
func (a *App) startThemeCreate(name string, sc *theme.Sidecar, intent themeCreateIntent) bool {
	if a.themeCreate != nil {
		a.noteThemeCopy("a theme is already being created — let it finish first")
		return false
	}
	if sc == nil {
		a.noteThemeCopy("there is nothing to build a theme from")
		return false
	}
	cat := a.themeCatalogSnapshot()
	if cat == nil || cat.WriteRoot == "" {
		// The catalog alone knows where this client may write, so kick the scan rather than refuse twice.
		a.requestThemeCatalog(themeScanExplicit)
		a.noteThemeCopy("this install has nowhere to write themes yet. Set a theme folder in Settings ▸ Theme.")
		return false
	}
	want := strings.TrimSpace(name)
	if want == "" {
		want = themeCreateFallbackName
	}
	// SanitizeName is the one spelling of folder-name legality. Called here as well to refuse before a goroutine
	// exists, so an unusable name gets a sentence, not a job that fails later.
	if themepack.SanitizeName(want) == "" {
		a.noteThemeCopy("\"" + want + "\" has no characters a folder name can use — try letters and numbers")
		return false
	}
	root := cat.WriteRoot
	job := &themeCreateJob{name: want, intent: intent, done: make(chan themeCreateResult, 1)}
	a.themeCreate = job
	go func() {
		m, err := themepack.CreateFromSidecar(root, job.name, sc)
		job.done <- themeCreateResult{manifest: m, err: err}
	}()
	a.noteThemeCopy("creating " + want + "…")
	return true
}

// startThemeCreateFromTemplate is the Settings entry point: build the template's model, then mint it.
// templateCopyCurrent routes to themecopy.go because it is a different act.
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

// pollThemeCreate lands a finished create, once per frame: a result between frames must not touch UI state from another
// goroutine.
func (a *App) pollThemeCreate() {
	if a.themeCreate == nil {
		return
	}
	select {
	case res := <-a.themeCreate.done:
		a.landThemeCreate(res)
	default:
	}
}

// landThemeCreate is the one place a finished create changes app state. The rescan is deferred because a scan publishes
// a snapshot built before this folder existed, and the seed below lets the next Save resolve the theme.
func (a *App) landThemeCreate(res themeCreateResult) {
	job := a.themeCreate
	a.themeCreate = nil
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
	// Seed the catalog first. The empty source name is deliberate: the badge derives from the zero entry, and
	// themeKindAfterCopy(themeKindBroken) is themeKindNative, which fits a folder holding one asyncao_theme.ini.
	a.noteCopiedTheme("", m)
	if job.intent == themeCreateThenNote {
		a.noteThemeCopy("created " + m.Root + " — it is in Settings ▸ Theme")
		return
	}
	// Apply it, then open the editor through the arming maybeOpenEditorAfterCopy owns (themecopy.go). The custom root
	// stays as it was: the folder landed under the write root themeLoadRoots already walks.
	if a.d.Prefs != nil {
		_, customRoot := a.d.Prefs.Load().Theme()
		a.d.Prefs.SetTheme(m.Root, customRoot)
	}
	settings.themeName = m.Root
	gen := a.applyThemeAsync()
	a.themeEditArmGen, a.themeEditArmFrom = gen, a.screen
	a.noteThemeCopy("Created " + m.Root + " — opening the editor over it")
}
