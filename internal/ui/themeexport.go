package ui

// Sharing a theme: zip the theme folder into one .aotheme file. An AsyncAO theme is a theme folder (the sidecar beside
// the author's courtroom_design.ini, everything else verbatim), so round-tripping falls out of the format.
//
// Two modes on one button: native, or AO2-compat, which bakes [overrides] into a copy of courtroom_design.ini, empties
// the sidecar's [overrides] in a copy of the sidecar, and adds ASYNCAO-LOST.txt naming every element AO2 won't show.
// Those copies are bytes handed to themepack.Write as Extras; nothing is written into the theme folder, and the
// author's courtroom_design.ini is never touched. Runs off the render thread (hard rule 2), single-flight like
// editSaveJob.

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// editExportSubdir is the folder exports land in, a sibling of the theme folders: a .aotheme inside themes/<name>/
// would be bundled by the next export and grow every time.
const editExportSubdir = "shared"

// editExportJob is one in-flight bundle write.
type editExportJob struct {
	// ao2 records which mode produced the job, for the receipt.
	ao2  bool
	path string
	done chan editExportResult
}

type editExportResult struct {
	manifest *themepack.Manifest
	err      error
}

// editorExport starts a Share. Shift picks the AO2-compat mode; plain Share is native.
func (a *App) editorExport() {
	if a.te == nil || a.te.doc == nil {
		return
	}
	if a.te.export != nil {
		a.te.note("a bundle is already being written")
		return
	}
	if a.te.doc.dirty {
		// Refuse and offer the fix: the zip reads the disk, not the model, so bundling with unsaved edits would ship
		// the theme as it was before this session.
		a.te.note("save first — Share bundles the files on disk, and this theme has unsaved edits")
		return
	}
	// A theme that can't round-trip can't be shared: Validate is Sidecar.Bytes's own precondition, so Share refuses
	// exactly what Save would, in the same words, while the author can still fix it. Validate rather than a throwaway
	// bytes(), which would rewrite the lossless carrier as a side effect.
	if err := a.te.doc.side.Validate(); err != nil {
		a.te.note("this theme would not save, so it cannot be shared: " + err.Error())
		return
	}
	dir, err := a.editorThemeDir()
	if err != nil {
		a.te.note(err.Error())
		return
	}
	// No pre-flight walk here (hard rule 2): a "what would this bundle contain?" census reads the whole theme folder,
	// and this runs on the render thread from a button. themepack.Write does that census itself before creating
	// anything, refusing an over-cap theme with nothing written, and its Manifest carries the font-licence warnings
	// into the receipt. An off-thread pre-flight stays unbuilt until the export sheet that would show it exists; what
	// ships is Share refusing what Save would refuse, and a receipt naming every warning after the write.
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

// editorExportExtras builds the AO2-compat mode's three replacement files, or nothing for a native export. It reads the
// folder for the design INI (it belongs to the upstream author and the model never held it) and the model for the
// overrides (what the editor has changed). Render thread: two small reads and a serialise, and it must be able to
// refuse before the goroutine starts so the refusal can be a chip. Bounded by theme.IniDocByteCap (applied by
// ParseINIDoc).
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

// pollEditorExport lands a finished bundle, as pollEditorSave and pollEditorFont do.
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

// editorExportReceipt is what the author is told: the size first (will it fit on a message?), then the font-licence
// warning, the only one that can get them into trouble.
func editorExportReceipt(job *editExportJob, m *themepack.Manifest) string {
	if m == nil {
		return "wrote " + filepath.Base(job.path)
	}
	line := "Shared " + filepath.Base(job.path) + " — " + humanBytes(m.ZipBytes) + " packed, " +
		strconv.Itoa(m.Files) + " files"
	if job.ao2 {
		line += " · AO2-compat: geometry baked in, " + theme.LostFileName + " lists what AO2 drops"
	}
	// The font licence comes before the other notes: it is the only clause about someone else's rights, and OFL
	// requires the licence to travel with the face.
	if named := fontsMissingLicense(m); named != "" {
		line += " · " + named + " — check what you may pass on"
	} else if len(m.Warnings) > 0 {
		line += " · " + m.Warnings[0]
	}
	return line
}

// readFileBounded reads at most max bytes of a file, or nothing.
//
// A missing file is not an error: a theme may ship no courtroom_design.ini (the sidecar is a standalone source), and
// the AO2-compat bake is then a design INI of just the baked keys.
//
// Render thread, and the one synchronous disk touch in this file: bounded by the format's cap, and synchronous because
// its refusal is a chip the user must see on the click that caused it.
func readFileBounded(path string, max int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	// One byte past the cap, so the parser refuses rather than accepting a truncated INI as the author's whole file.
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil
	}
	return b
}

// editorAwaitExport drains the current bundle write, with a bound, through the same landing; the twin of
// editorAwaitSave.
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
