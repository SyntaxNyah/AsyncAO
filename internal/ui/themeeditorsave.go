package ui

// Saving the document cannot break the theme it came from: Sidecar.Bytes validates before writing, only
// themeOriginYours is written (others offer a copy), and the file is replaced atomically by rename.
// Serialising runs on the render thread so a refusal shows at once; the write is one goroutine (hard rule 2),
// with a buffered channel so it never blocks on an editor that closed.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// editSaveTempSuffix sits in the theme's own folder because os.Rename is only atomic within a filesystem.
const editSaveTempSuffix = ".asyncao-tmp"

// errThemeNotYours is a sentinel because only this refusal has a one-press fix, the copy for editing.
var errThemeNotYours = errors.New("this theme is not yours to write")

// editSaveJob is one in-flight write; the channel is buffered so the goroutine exits whether or not anyone listens.
type editSaveJob struct {
	path string
	done chan error
}

// editorSave serialises the document and starts the write; every refusal is a chip, never a silent no-op.
func (a *App) editorSave() {
	if a.te == nil || a.te.doc == nil {
		return
	}
	if a.te.save != nil {
		a.te.note("a save is already running")
		return
	}
	path, err := a.editorSidecarPath()
	if err != nil {
		a.offerCopyOnSaveRefusal(err)
		return
	}
	b, err := a.te.doc.bytes()
	if err != nil {
		// The same refusal the reader would make, so name the theme and the limit.
		a.te.note(themeSidecarRefusalTip(a.te.doc.name, err))
		return
	}
	job := &editSaveJob{path: path, done: make(chan error, 1)}
	a.te.save = job
	go func() { job.done <- writeFileAtomically(job.path, b) }()
}

// offerCopyOnSaveRefusal makes the refusal pressable: inside themeCopyConfirmWindow, a second Save copies and writes.
// The offer lasts as long as the chip that makes it.
func (a *App) offerCopyOnSaveRefusal(err error) {
	if !errors.Is(err, errThemeNotYours) {
		a.te.note(err.Error())
		return
	}
	if a.te.copyAt.IsZero() || time.Since(a.te.copyAt) >= themeCopyConfirmWindow {
		a.te.copyAt = time.Now()
		a.te.note(err.Error() + ". Press Save again to copy it into your themes folder and save there")
		return
	}
	// The offer is spent by the act it authorises.
	a.te.copyAt = time.Time{}
	a.copyThemeForEditing(a.te.doc.name, themeCopyThenSave)
}

// pollEditorSave lands a finished write from the draw, so a result never touches UI state from another goroutine.
func (a *App) pollEditorSave() {
	if a.te == nil || a.te.save == nil {
		return
	}
	select {
	case err := <-a.te.save.done:
		a.landEditorSave(err)
	default:
	}
}

// landEditorSave is the one place a finished write changes editor state.
func (a *App) landEditorSave(err error) {
	path := a.te.save.path
	a.te.save = nil
	if err != nil {
		a.te.note("could not write " + filepath.Base(path) + ": " + err.Error())
		return
	}
	// Cleared by the write landing, never its start: an exit racing a failed save would discard edits thought saved.
	a.te.doc.dirty = false
	// Discard means back to the last save, or memory and disk would hold two different themes.
	a.te.doc.rebaseline()
	a.te.note("saved to " + path)
	a.applyThemeAfterSaveIfTypeChanged()
}

// applyThemeAfterSaveIfTypeChanged applies the theme only when the saved type tables differ from the on-screen faces.
// applySidecarFonts reads the sidecar on disk, so bindings need an apply. Only when changed: an apply restarts every
// theme clock and purges the text cache. After the write: an apply re-reads the file.
func (a *App) applyThemeAfterSaveIfTypeChanged() {
	if a.te == nil || a.te.doc == nil {
		return
	}
	sig := a.te.doc.typeSig()
	if sig == a.te.doc.typeSigApplied {
		return
	}
	a.te.doc.typeSigApplied = sig
	a.applyThemeAsync()
}

// editorSidecarPath resolves where this theme's asyncao_theme.ini belongs, or why it cannot be written.
func (a *App) editorSidecarPath() (string, error) {
	name := a.te.doc.name
	if name == "" {
		return "", errors.New("this theme has no folder name — nothing to save into")
	}
	cat := a.themeCatalogSnapshot()
	if cat == nil {
		return "", errors.New("the theme folder has not been scanned yet — try again in a moment")
	}
	entry, ok := cat.Entry(name)
	if !ok {
		return "", errors.New(name + " is not in the theme list — reopen the editor after a refresh")
	}
	if entry.Origin != themeOriginYours {
		// Name the offender and the limit; the sentinel lets editorSave offer the copy that fixes it.
		return "", fmt.Errorf("%s is a %s theme — %w", name, themeOriginLabel(entry.Origin), errThemeNotYours)
	}
	if entry.Dir == "" {
		return "", errors.New("could not work out where " + name + " lives on this machine")
	}
	return filepath.Join(entry.Dir, theme.SidecarFileName), nil
}

// writeFileAtomically replaces path with b by temp file and rename; goroutine only, touching just its arguments.
func writeFileAtomically(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + editSaveTempSuffix
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // a failed rename must not leave litter beside the theme
		return err
	}
	return nil
}

// editorAwaitSave drains the current write, with a bound, through the same landing.
func (a *App) editorAwaitSave(within time.Duration) bool {
	if a.te == nil || a.te.save == nil {
		return true
	}
	select {
	case err := <-a.te.save.done:
		a.landEditorSave(err)
		return err == nil
	case <-time.After(within):
		return false
	}
}
