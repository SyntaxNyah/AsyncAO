package ui

// SHARING A THEME (v1.90.0 W8) — docs/wip/THEME-EDITOR-DESIGN.md §2.6.
// OFF THE RENDER THREAD (hard rule 2) and single-flight: the job pointer is the
// flag, the channel is buffered, and the shape is editSaveJob's exactly.

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// editExportSubdir is the folder exports land in, beside the themes themselves.
const editExportSubdir = "shared"

// editExportJob is one in-flight bundle write.
type editExportJob struct {
	// ao2 records which mode produced it, so the receipt can say so without a
	// second flag that could disagree with the goroutine that ran.
	ao2  bool
	path string
	done chan editExportResult
}

type editExportResult struct {
	manifest *themepack.Manifest
	err      error
}

// editorExport starts a Share. The MODE is chosen by the modifier the user is
// holding, which is how a one-button header row carries two actions without a
// menu the design has no room for: plain Share is native, Shift+Share is
// "also work in AO2-Client".
func (a *App) editorExport() {
	if a.te == nil || a.te.doc == nil {
		return
	}
	if a.te.export != nil {
		a.te.note("a bundle is already being written")
		return
	}
	if a.te.doc.dirty {
		// REFUSE, AND OFFER THE FIX. Bundling the folder while the document holds
		// unsaved edits would ship the theme as it was BEFORE this session — the
		// zip reads the disk, not the model — and the author would find out from
		// whoever they sent it to.
		a.te.note("save first — Share bundles the files on disk, and this theme has unsaved edits")
		return
	}
	// A THEME THAT CANNOT ROUND-TRIP CANNOT BE SHARED. Validate is the writer's own
	// precondition — the very check Sidecar.Bytes runs before it edits a byte — so
	// asking it here means Share refuses exactly what Save would refuse, in the same
	// words, at the moment the author can still fix it. Without this the sheet would
	// happily bundle a theme whose model has since drifted past a cap, and the
	// recipient would be the one who found out.
	if err := a.te.doc.side.Validate(); err != nil {
		a.te.note("this theme would not save, so it cannot be shared: " + err.Error())
		return
	}
	dir, err := a.editorThemeDir()
	if err != nil {
		a.te.note(err.Error())
		return
	}
	// NO PRE-FLIGHT WALK HERE, and that is hard rule 2 rather than an omission. A
	// "what would this bundle contain?" census reads the whole theme folder, and
	// this function runs on the RENDER THREAD, from a button. themepack.Write does
	// the same census itself, before it creates anything — an over-cap theme is
	// refused with nothing written either way — and the Manifest it returns carries
	// the font-licence warnings into the receipt below.
	ao2 := a.ctx != nil && a.ctx.shiftHeld
	extras, err := a.editorExportExtras(dir, ao2)
	if err != nil {
		a.te.note("could not prepare the AO2 copy: " + err.Error())
		return
	}
	dest := filepath.Join(filepath.Dir(dir), editExportSubdir, a.te.doc.name+themepack.PackExt)
	job := &editExportJob{ao2: ao2, path: dest, done: make(chan editExportResult, 1)}
	a.te.export = job
	go func() {
		m, err := themepack.Write(dir, dest, extras)
		job.done <- editExportResult{manifest: m, err: err}
	}()
	a.te.note("writing " + filepath.Base(dest) + "…")
}

// editorExportExtras builds the AO2-compat mode's three replacement files, or
// nothing at all for a native export.
func (a *App) editorExportExtras(dir string, ao2 bool) ([]themepack.Extra, error) {
	if !ao2 {
		return nil, nil
	}
	src := readFileBounded(filepath.Join(dir, theme.DesignFileName), theme.IniDocByteCap)
	baked, err := theme.BakeAO2Design(src, a.te.doc.side)
	if err != nil {
		return nil, err
	}
	stripped, err := theme.StripForAO2(a.te.doc.side).Bytes()
	if err != nil {
		return nil, err
	}
	return []themepack.Extra{
		{Rel: theme.DesignFileName, Data: baked},
		{Rel: theme.SidecarFileName, Data: stripped},
		{Rel: theme.LostFileName, Data: theme.LostReport(a.te.doc.name, a.te.doc.side)},
	}, nil
}

// pollEditorExport lands a finished bundle, beside pollEditorSave and
// pollEditorFont and for the same reason.
func (a *App) pollEditorExport() {
	if a.te == nil || a.te.export == nil {
		return
	}
	select {
	case res := <-a.te.export.done:
		job := a.te.export
		a.te.export = nil
		if res.err != nil {
			a.te.note("could not write " + filepath.Base(job.path) + ": " + res.err.Error())
			return
		}
		a.te.note(editorExportReceipt(job, res.manifest))
	default:
	}
}

// editorExportReceipt is what the author is told. It names the SIZE first,
// because "will this fit on a message?" is the only question they have, and the
// font-licence warning second, because it is the only one that can get them into
// trouble.
func editorExportReceipt(job *editExportJob, m *themepack.Manifest) string {
	if m == nil {
		return "wrote " + filepath.Base(job.path)
	}
	line := "Shared " + filepath.Base(job.path) + " — " + humanBytes(m.ZipBytes) + " packed, " +
		strconv.Itoa(m.Files) + " files"
	if job.ao2 {
		line += " · AO2-compat: geometry baked in, " + theme.LostFileName + " lists what AO2 drops"
	}
	// THE FONT LICENCE COMES BEFORE THE OTHER NOTES, because it is the only clause
	// here about somebody ELSE's rights and the only one that has to be read before
	// the file is attached to anything. OFL requires the licence to travel with the
	// face, so the author about to share this is the one who would be breaking it.
	if named := fontsMissingLicense(m); named != "" {
		line += " · " + named + " — check what you may pass on"
	} else if len(m.Warnings) > 0 {
		line += " · " + m.Warnings[0]
	}
	return line
}

// readFileBounded reads at most max bytes of a file, or nothing at all.
func readFileBounded(path string, max int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	// One byte past the cap, so the PARSER refuses rather than silently accepting
	// a truncated INI as the author's whole file.
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil
	}
	return b
}

// editorAwaitExport drains the current bundle write, with a bound, THROUGH the
// same landing — the twin of editorAwaitSave, and it exists for the same reason:
// a gate asserts on the FILE rather than on a goroutine having been started.
func (a *App) editorAwaitExport(within time.Duration) bool {
	if a.te == nil || a.te.export == nil {
		return true
	}
	deadline := time.After(within)
	for {
		select {
		case res := <-a.te.export.done:
			job := a.te.export
			a.te.export = nil
			if res.err != nil {
				a.te.note("could not write " + filepath.Base(job.path) + ": " + res.err.Error())
				return false
			}
			a.te.note(editorExportReceipt(job, res.manifest))
			return true
		case <-deadline:
			return false
		}
	}
}
