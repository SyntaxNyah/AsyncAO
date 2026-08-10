package ui

// IMAGE INTAKE (v1.90.0 W10) — "an easy way to upload images and animated webps".
//
// ONE concern: turning a picture the user points at into a [media] row and an
// ElemImage element inside the theme being edited.
//
// ONE FUNNEL, TWO DOORS. A drop lands in editorTakeImage; the in-app browser's
// purposeThemeImage pick lands in editorTakeImage. There is no second copy of the
// caps, the destination or the budget verdict, so browsing and dropping cannot
// diverge — the same property W8 built the bundle import on, and
// TestOneThemeImageIntakeFunnel is the census that keeps a third door from
// appearing.
//
// ANIMATED WEBP NEEDS NO SPECIAL CASE, and that is not an oversight to be fixed
// later: `.webp.animated` does not exist (CLAUDE.md), animation is the VP8X ANIM
// flag and the decode pipeline sniffs it. An animated WebP, an APNG and a GIF all
// arrive here as an assets.Decoded with more than one frame, and every one of them
// is already handled by the same downscale, decimation and budget arithmetic the
// theme apply runs (loadThemeMedia).
//
// EVERY EDIT IS AN OP, and both of them are ONE op group: the [media] row
// (opMediaRow) and the element that draws it (opElemAdd) go in together and come
// out together on one Ctrl+Z. A declared image nothing points at still costs the
// theme's art budget, so an undo that took back only the element would leave the
// budget bar counting a picture nobody can see.
//
// THE FILE COPY IS NOT AN OP, exactly as the font intake's is not, and for the
// stated reason: undo restores the DOCUMENT, and an undo that deleted a file the
// user dropped would be an undo that destroys data outside the document it owns.
// The copy is inert once its row is gone — nothing reads a file the [media] table
// does not name.
//
// BUDGET-AWARE AT INTAKE. The W4 plan already refuses an over-cap image at APPLY
// time with a self-describing placeholder; refusing it HERE as well means the user
// finds out while they can still pick a smaller file, and the refusal is the chip
// (design §3.3: name the offender, name the limit, offer the fix) rather than an
// element that silently draws a grey box.

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cespare/xxhash/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Named caps and metrics (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// editImageDirName is the folder inside a theme that carries its pictures. A
	// SUBFOLDER rather than the theme root, so a theme with twenty images still has
	// a readable folder and the AO2 files at the top of it stay visible.
	editImageDirName = "images"

	// editImageFileMaxBytes bounds the SOURCE file this will copy. An alias of the
	// planner's own door (themeMediaPath refuses anything larger before a decode),
	// so a file that would be refused at apply time is refused at intake instead of
	// copied and then ignored.
	editImageFileMaxBytes = themeMediaFileByteCap

	// editImageIDTryCap bounds the "_2", "_3" walk that makes a colliding id unique.
	// Hard rule 4: a bounded search with a refusal, never a loop that trusts the
	// input to terminate it.
	editImageIDTryCap = 64

	// editImagePlaceX / editImagePlaceY are where a fresh element lands in design
	// space. Inset from the corner so its drag handles are all reachable, and the
	// same for every intake so a second image lands somewhere predictable rather
	// than exactly on top of the first.
	editImagePlaceX = 40
	editImagePlaceY = 40
	// editImagePlaceStep offsets each successive intake, cascading them like a
	// window manager so two images dropped in a row are both grabbable.
	editImagePlaceStep = 24
	// editImagePlaceCascade bounds that cascade before it wraps back to the origin,
	// so the tenth image is still on the canvas.
	editImagePlaceCascade = 8

	// editImageMaxPlaceEdge is the longest edge a fresh element is given in design
	// space. A 1920-px wallpaper placed at its own pixel size would be four times
	// the design canvas and unselectable; scaled to this, it is a thing you can see
	// and then resize deliberately.
	editImageMaxPlaceEdge = 480
	// editImageMinPlaceEdge keeps a tiny source from landing as an unclickable
	// speck.
	editImageMinPlaceEdge = 16
)

// themeImageExts are the picture files the editor will take in.
//
// EXACTLY ElemImage's own list ("imported PNG/WebP/GIF/APNG/AVIF, static or
// animated" — theme.ElementKind), and nothing else: claiming a format the decode
// pipeline cannot sniff would trade a wrong action for a wrong promise, which is
// the same ruling themeFontExts made about .ttc and .woff.
//
// NAMED editIntakeImageExts, not themeImageExts: that name is already taken by
// app.go's per-stem PROBE ORDER (webp → apng → gif → png, AO2-Client's
// get_image_suffix). Two lists, two questions — "which suffix do we try for a
// theme's own chrome stem" and "which files will the editor take in" — and giving
// them one name would be the drift the distinction exists to prevent.
var editIntakeImageExts = [...]string{".png", ".webp", ".gif", ".apng", ".avif"}

// isThemeImageExt reports whether an already-lowered extension is theme art.
func isThemeImageExt(ext string) bool {
	for _, e := range editIntakeImageExts {
		if ext == e {
			return true
		}
	}
	return false
}

// isThemeImageName is the BROWSER's filter — the same question by file name.
func isThemeImageName(name string) bool {
	return isThemeImageExt(strings.ToLower(filepath.Ext(name)))
}

// ---------------------------------------------------------------------------
// The job
// ---------------------------------------------------------------------------

// editImageJob is one in-flight intake. Same shape as editFontJob and for the same
// reasons: the channel is buffered so the goroutine never blocks on an editor that
// closed, and exactly one runs at a time because the pointer IS the flag.
type editImageJob struct {
	// id is the [media] id this will declare, already uniqued against the table.
	id string
	// file is theme-relative with forward slashes — what the [media] row will say.
	file string
	// src / dest are the two paths the goroutine touches and the only two.
	src, dest string
	done      chan editImageResult
}

// editImageResult is what the goroutine hands back.
//
// THE DECODED PAGE RIDES HOME. It was decoded off the render thread to measure it,
// and throwing it away would mean the picture does not appear until the next whole
// theme apply — which is the hitch the incremental land path (landThemeMediaPage)
// exists to avoid. The landing either uploads it or releases it; both arms are
// covered, because a Decoded that reaches neither is a pooled buffer that never
// goes back.
//
// THE ONE ARM THAT IS NOT COVERED IS AN EDITOR CLOSED MID-FLIGHT, and it is a
// deliberate non-fix rather than an oversight: pollEditorImage is the only reader
// and it dies with a.te, so a result that lands after the editor closed reaches the
// GC as ordinary garbage instead of the decoder pool. That is exactly what
// releaseDecoded describes as the acceptable outcome for a dropped apply — one
// frame set, once, on a path a user reaches by closing the editor inside the second
// it takes to copy a file. Draining it properly means blocking the render thread on
// a goroutine, which is the trade this codebase refuses everywhere else.
type editImageResult struct {
	dec   *assets.Decoded
	bytes int64  // the DECODED payload, which is what the budget is denominated in
	hash  string // xxhash64 of the source file, lowercase hex — the [media] row's own
	err   error
}

// ---------------------------------------------------------------------------
// The funnel
// ---------------------------------------------------------------------------

// editorTakeImage is the ONE entry point: a drop and a browse both land here.
//
// Returns whether the EDITOR claimed the path. false means "no editor is open over
// a writable theme", which is the answer the global drop handler needs so it can
// say what to do instead — the same contract editorTakeFontDrop has.
//
// EVERY REFUSAL IS A CHIP AND RETURNS TRUE: once the editor is open, the file is
// ours, and a silent no-op would be indistinguishable from a lost drop.
func (a *App) editorTakeImage(path string) bool {
	if a.te == nil || a.te.doc == nil || a.te.doc.side == nil {
		return false
	}
	if a.te.image != nil {
		a.te.note("one image at a time — the last one is still being copied")
		return true
	}
	base := filepath.Base(strings.TrimSpace(path))
	if !isThemeImageName(base) {
		a.te.note(base + " is not a picture this theme can carry — PNG, WebP, GIF, APNG or AVIF")
		return true
	}
	dir, err := a.editorThemeDir()
	if err != nil {
		// editorSidecarPath is the ONE authority on "where this theme lives and is it
		// ours to write" and its refusals already name the fix, so this is its sentence
		// rather than a second opinion.
		a.te.note(err.Error())
		return true
	}
	if len(a.te.doc.side.Media) >= theme.MediaCap {
		a.te.note("this theme already declares " + strconv.Itoa(theme.MediaCap) +
			" images, which is the limit — remove one before adding another")
		return true
	}
	if len(a.te.doc.side.Elements) >= theme.ElementCap {
		a.te.note("this theme already has " + strconv.Itoa(theme.ElementCap) +
			" elements, which is the limit — delete one before adding another")
		return true
	}
	id, ok := uniqueMediaID(a.te.doc.side, base)
	if !ok {
		a.te.note(base + " has no usable id — rename it to something with letters or numbers in it")
		return true
	}
	// THE DESTINATION IS RESOLVED THROUGH safepath, not by joining strings: the
	// name comes from a file the user pointed at, and internal/safepath is THE copy
	// of the traversal guards (CLAUDE.md's package map). A base name cannot escape a
	// folder, but the guard is what makes that a property rather than a belief.
	rel := editImageDirName + "/" + base
	dest, err := safepath.Join(dir, filepath.FromSlash(rel))
	if err != nil {
		a.te.note(base + " cannot be written into the theme folder: " + err.Error())
		return true
	}
	job := &editImageJob{id: id, file: rel, src: path, dest: dest, done: make(chan editImageResult, 1)}
	a.te.image = job
	// OFF THE RENDER THREAD (hard rule 2): the stat, the read, the hash, the copy
	// and the decode all happen there, and the goroutine touches nothing but its own
	// two paths and the flag it was handed.
	anims := a.d.Prefs.AnimationsEnabled()
	go func() { job.done <- runEditImageJob(job, anims) }()
	a.te.note("adding " + base + "…")
	return true
}

// runEditImageJob is the whole off-thread half. GOROUTINE ONLY.
//
// THE ORDER IS THE POINT: refuse by SIZE before anything is read, hash the bytes we
// actually copied (so the [media] row's verification is of the file in the theme,
// not of whatever the source has become since), copy atomically, then decode the
// COPY — because the copy is the file the theme will load, and decoding the source
// instead would let a truncated write ship a row whose hash and size describe a
// picture that is not there.
func runEditImageJob(job *editImageJob, anims bool) editImageResult {
	info, err := os.Stat(job.src)
	if err != nil {
		return editImageResult{err: err}
	}
	if info.IsDir() {
		return editImageResult{err: errors.New("that is a folder, not a picture")}
	}
	if info.Size() > editImageFileMaxBytes {
		return editImageResult{err: errors.New(strconv.FormatInt(info.Size()>>20, 10) +
			" MB is past the " + strconv.Itoa(editImageFileMaxBytes>>20) + " MB per-file limit")}
	}
	src, err := os.ReadFile(job.src)
	if err != nil {
		return editImageResult{err: err}
	}
	if int64(len(src)) > editImageFileMaxBytes {
		return editImageResult{err: errors.New("the file grew while it was being read")}
	}
	if err := writeFileAtomically(job.dest, src); err != nil {
		return editImageResult{err: err}
	}
	dec, err := loadThemeMedia(job.dest, anims)
	if err != nil {
		return editImageResult{err: err}
	}
	if dec == nil || len(dec.Frames) == 0 {
		return editImageResult{err: errors.New("nothing in it decoded to a picture")}
	}
	return editImageResult{
		dec:   dec,
		bytes: dec.PixelBytes(),
		// xxhash64, lowercase hex — MediaEntry.Hash's own spelling, and the same
		// hasher themepack's provenance stamp and the disk cache already use.
		hash: strconv.FormatUint(xxhash.Sum64(src), 16),
	}
}

// ---------------------------------------------------------------------------
// Landing
// ---------------------------------------------------------------------------

// pollEditorImage lands a finished intake. Called once per editor frame beside
// pollEditorFont, and for the same reason: a result that arrives between frames
// must not touch UI state from another goroutine.
func (a *App) pollEditorImage() {
	if a.te == nil || a.te.image == nil {
		return
	}
	// PREVIEW DEFERS THE LANDING (field batch 9), for pollEditorFont's reason and one
	// of its own: landEditorImage mutates the document through editorApply, which
	// refuses while the demo is up, and its refusal arm Releases the decoded page — so
	// draining here would cost the user the picture AND its decode, silently and under
	// the wrong sentence ("the [media] table is full"). The job waits instead; the
	// first editing frame after the demo lands it, decode included.
	if a.editorPreviewOn() {
		return
	}
	select {
	case res := <-a.te.image.done:
		job := a.te.image
		a.te.image = nil
		a.landEditorImage(job, res)
	default:
	}
}

// landEditorImage is the ONE place a finished intake changes the document.
//
// IT ALWAYS DISPOSES OF THE DECODED PAGE — uploaded on the happy path, released on
// every refusal. A Decoded that reaches neither is a pooled buffer that never goes
// back (releaseDecoded's own note).
func (a *App) landEditorImage(job *editImageJob, res editImageResult) {
	base := filepath.Base(job.file)
	if res.err != nil {
		a.te.note("could not add " + base + ": " + res.err.Error())
		return
	}
	if refusal := a.editImageBudgetRefusal(res.bytes); refusal != "" {
		// THE CAP IS SURFACED WITH ITS REASON (design §3.3). The file stays in the
		// theme folder — it is inert until something names it — and the user can
		// downscale it and drop it again without hunting for it.
		res.dec.Release()
		a.te.note(base + " " + refusal)
		return
	}
	row := theme.MediaEntry{ID: job.id, File: job.file, Bytes: res.bytes, Hash: res.hash}
	if why := a.applyImageIntakeOps(row, newImageElement(job.id, res.dec, len(a.te.doc.side.Elements))); why != "" {
		res.dec.Release()
		a.te.note(base + " " + why)
		return
	}
	// THE PLAN LEARNS ABOUT IT WITHOUT A DISK WALK. planThemeMedia resolves every
	// [media] row with an os.Stat, which may not happen on a frame (hard rule 2) —
	// which is exactly why editorSyncArt re-derives only the ELEMENT half and leaves
	// the entries alone. The goroutine above already did that stat, that read and
	// that decode, so the entry it produced is appended from facts rather than
	// re-derived from the disk.
	a.noteIntakenMedia(row, job.dest)
	// THE PICTURE APPEARS NOW, through the incremental land path, which re-anchors
	// nothing and restarts no clock (landThemeMediaPage's own note). Without it the
	// element would draw a placeholder until the next whole theme apply.
	if err := a.landThemeMediaPage(a.themeMediaKeyFor(job.id), res.dec); err != nil {
		a.te.note(base + " was added but could not be shown — " + err.Error())
		return
	}
	a.te.selectOnly(elementTarget(len(a.te.doc.side.Elements) - 1))
	a.te.note(base + " added — drag it, or use the inspector to place it")
}

// applyImageIntakeOps is the UNDOABLE half of an intake: the [media] declaration
// and the element that draws it, as ONE op group. Returns "" on success, or the
// tail of a refusal sentence naming the cap that stopped it.
//
// SPLIT OUT OF THE LANDING so a gate can drive the real ops without a decoder, a
// texture store or a file on disk (TestAnImageIntakeIsOneUndoStep). It is the
// production path, not a copy of it — landEditorImage calls exactly this — which is
// what rule 11 means by driving the seam rather than mirroring it.
//
// ONE GROUP, so Ctrl+Z takes back the declaration and the element together. A
// declared image nothing points at still costs the theme's art budget, so an undo
// that took back only the element would leave the budget bar counting a picture
// nobody can see. The ring stamps the group onto every op it pushes
// (themeEditRing.push), so the ops below carry no group field of their own — one
// writer, no second copy to disagree with it.
func (a *App) applyImageIntakeOps(row theme.MediaEntry, el theme.Element) string {
	if a.te == nil || a.te.doc == nil || a.te.doc.side == nil {
		return "could not be added — the editor closed while it was being read"
	}
	a.te.ring.begin()
	defer a.te.ring.end()
	rowOp := themeEditOp{
		kind:   opMediaRow,
		target: mediaTarget(len(a.te.doc.side.Media)),
		after:  editValueStr(a.te.doc.intern(mediaRowValue(row))),
	}
	if !a.editorApply(rowOp) {
		return "could not be declared — the [media] table is full"
	}
	slot, ok := a.te.doc.graveSlot()
	if !ok {
		return "could not be added — too many undoable changes this session; save, then add it again"
	}
	addOp := themeEditOp{
		kind:   opElemAdd,
		target: elementTarget(len(a.te.doc.side.Elements)),
		after:  a.te.doc.park(slot, el),
	}
	if !a.editorApply(addOp) {
		return "was declared but no element could be added — the element list is full"
	}
	return ""
}

// editImageBudgetRefusal is the intake's budget verdict, or "" for one that fits.
//
// IT ASKS THE SAME TWO NUMBERS THE PLANNER ASKS, by CALL rather than by copy
// (planThemeMedia's own note: two copies of a budget are two budgets). The
// arithmetic is the planner's too — per-item first, then the running total — so an
// image accepted here cannot be refused by the plan on the next apply.
func (a *App) editImageBudgetRefusal(want int64) string {
	mib := a.d.Prefs.TexBudgetMiB()
	item := render.ThemeMediaItemByteCap(mib)
	if want > item {
		return "decodes to " + themeMediaMiB(want) + ", over the " + themeMediaMiB(item) +
			" per-image cap. Downscale it and add it again."
	}
	total := render.ThemeMediaByteCap(mib)
	if used := a.themeMediaPlan.Bytes; used+want > total {
		return "decodes to " + themeMediaMiB(want) + ", which would take this theme past its " +
			themeMediaMiB(total) + " art budget (" + themeMediaMiB(used) +
			" already used). Downscale it, or remove another image."
	}
	return ""
}

// noteIntakenMedia appends the plan entry for a row this client has just written.
//
// The twin of noteCopiedTheme, and it exists for the same reason: the authoritative
// producer (planThemeMedia / the theme scan) cannot run here, and waiting for it
// would leave a window in which the element the user just added resolves to no key
// at all. Bounded by render.ThemeMediaCap, which is the plan's own bound.
func (a *App) noteIntakenMedia(row theme.MediaEntry, path string) {
	if len(a.themeMediaPlan.Entries) >= render.ThemeMediaCap {
		return // hard rule 4: the plan's own bound wins over convenience
	}
	a.themeMediaPlan.Entries = append(a.themeMediaPlan.Entries, themeMediaPlanEntry{
		ID:    row.ID,
		Key:   a.themeMediaKeyFor(row.ID),
		Path:  path,
		Bytes: row.Bytes,
		State: mediaPlanned,
	})
	a.themeMediaPlan.Bytes += row.Bytes
}

// themeMediaKeyFor is the T1 key one media id occupies under the APPLIED theme's
// namespace. One spelling, so the upload and the plan entry cannot key differently.
func (a *App) themeMediaKeyFor(id string) string {
	name := ""
	if a.d.Prefs != nil {
		name, _ = a.d.Prefs.Theme()
	}
	return render.ThemeMediaKey(themeSanitizeID(name), id)
}

// ---------------------------------------------------------------------------
// Naming and placement
// ---------------------------------------------------------------------------

// uniqueMediaID derives a [media] id from a file name and makes it unique inside
// the table.
//
// THE FOLD IS themeSanitizeID's, not a second one: that function is already the
// one spelling of "this string, reduced to the format's id grammar", and a media
// id and a theme id answer to the same grammar ([a-z0-9_-]{1,24}).
//
// A DERIVED name rather than a typed one, for the reason fontFamilyFromFile gives:
// the alternative at drop time is a modal asking for a string, and the design
// allows two modals in the whole editor and spends both elsewhere.
func uniqueMediaID(sc *theme.Sidecar, base string) (string, bool) {
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	id := themeSanitizeID(stem)
	// AN ID OF NOTHING BUT SEPARATORS IS REFUSED. themeSanitizeID is TOTAL — it folds
	// every rune outside the grammar to '_' — so a name written entirely in a script
	// the grammar has no room for ("。。。.png") comes back as "___": a legal id that
	// carries no information and that every other such file would collide with. The
	// fold is the right answer for a THEME FOLDER name, where the alternative is not
	// caching the theme at all; here there is a better one, which is to ask the user
	// to rename the file.
	if !hasIDSubstance(id) {
		return "", false
	}
	if !mediaIDTaken(sc, id) {
		return id, true
	}
	for n := 2; n < editImageIDTryCap; n++ {
		// The suffix is applied to a TRUNCATED stem so the result still fits the id
		// grammar's rune cap — themeSanitizeID truncates, and appending after it would
		// walk straight past the bound it just enforced.
		suffix := "_" + strconv.Itoa(n)
		trimmed := id
		if len(trimmed)+len(suffix) > themeMediaIDMaxLen {
			trimmed = trimmed[:themeMediaIDMaxLen-len(suffix)]
		}
		if cand := trimmed + suffix; !mediaIDTaken(sc, cand) {
			return cand, true
		}
	}
	return "", false
}

// hasIDSubstance reports an id with at least one letter or digit in it.
func hasIDSubstance(id string) bool {
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// mediaIDTaken reports an id the table already declares.
func mediaIDTaken(sc *theme.Sidecar, id string) bool {
	for i := range sc.Media {
		if sc.Media[i].ID == id {
			return true
		}
	}
	return false
}

// newImageElement is the element a fresh intake creates.
//
// PLACED, NOT ZEROED. A zero Element is deliberately unable to paint
// (theme.Element's own note on Opacity), so an intake that appended one would add a
// row to the rail and nothing to the screen — the "it did not work" shape. It lands
// visible, at a cascaded offset, at a size derived from the picture's own aspect.
//
// BandOverlay, deliberately: it is above everything except the editor's own chrome,
// so the user SEES what they just added instead of hunting for it behind the stage.
// Moving it down a band is one inspector row away, and a picture you cannot find is
// not.
func newImageElement(id string, dec *assets.Decoded, ordinal int) theme.Element {
	w, h := placedImageSize(dec.Width, dec.Height)
	step := (ordinal % editImagePlaceCascade) * editImagePlaceStep
	return theme.Element{
		ID:    id,
		Kind:  theme.ElemImage,
		Band:  theme.BandOverlay,
		Space: theme.SpaceCourtroom,
		Media: id,
		// FitContain, not FitStretch: the rect below already has the source's aspect,
		// so the two agree today — and when the user drags a corner, contain
		// letterboxes instead of silently distorting the picture they imported.
		Fit:     theme.FitContain,
		Opacity: theme.OpacityOpaque,
		Rect: theme.Rect{
			X: editImagePlaceX + step,
			Y: editImagePlaceY + step,
			W: w,
			H: h,
		},
	}
}

// placedImageSize scales a decoded size down to editImageMaxPlaceEdge, preserving
// aspect, and floors both edges at editImageMinPlaceEdge.
func placedImageSize(srcW, srcH int) (int, int) {
	w, h := srcW, srcH
	if w <= 0 || h <= 0 {
		return editImageMaxPlaceEdge, editImageMaxPlaceEdge
	}
	long := w
	if h > long {
		long = h
	}
	if long > editImageMaxPlaceEdge {
		// Integer arithmetic, in that order: multiply first so a picture 2000 px on
		// its long edge does not scale its short edge to zero.
		w = w * editImageMaxPlaceEdge / long
		h = h * editImageMaxPlaceEdge / long
	}
	if w < editImageMinPlaceEdge {
		w = editImageMinPlaceEdge
	}
	if h < editImageMinPlaceEdge {
		h = editImageMinPlaceEdge
	}
	return w, h
}
