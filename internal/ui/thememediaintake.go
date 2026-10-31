package ui

// Image intake: turns a picture the user points at into a [media] row and an ElemImage element in the theme being
// edited. A drop and the in-app browser's purposeThemeImage pick both land in editorTakeImage, so the caps,
// destination and budget verdict exist once.
//
// Animated WebP, APNG and GIF need no special case: animation is the VP8X ANIM flag, sniffed by the decoder, and
// they arrive as an assets.Decoded with more than one frame, handled by the same budget arithmetic as loadThemeMedia.
//
// The [media] row (opMediaRow) and its element (opElemAdd) are one op group, undone together; an element-only undo
// would leave a declared image that still costs the art budget. The file copy is not an op: undo restores the
// document and must not delete a file the user dropped. The copy is inert once its row is gone.
//
// The apply plan already refuses an over-cap image; refusing at intake too lets the user pick a smaller file, and
// the refusal is a chip naming the offender and the limit.

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

const (
	// editImageDirName is the subfolder of a theme that carries its pictures, keeping the AO2 files at the top visible.
	editImageDirName = "images"

	// editImageFileMaxBytes bounds the source file copied; the planner's own limit, so intake refuses what apply would.
	editImageFileMaxBytes = themeMediaFileByteCap

	// editImageIDTryCap bounds the _2, _3 walk that makes a colliding id unique.
	editImageIDTryCap = 64

	// editImagePlaceX / editImagePlaceY are where a fresh element lands in design space, inset so its drag handles
	// are reachable.
	editImagePlaceX = 40
	editImagePlaceY = 40
	// editImagePlaceStep offsets each successive intake so images dropped in a row are all grabbable.
	editImagePlaceStep = 24
	// editImagePlaceCascade bounds that cascade before it wraps back to the origin.
	editImagePlaceCascade = 8

	// editImageMaxPlaceEdge is the longest edge a fresh element is given in design space, so it stays selectable.
	editImageMaxPlaceEdge = 480
	// editImageMinPlaceEdge keeps a tiny source from landing as an unclickable speck.
	editImageMinPlaceEdge = 16
)

// themeImageExts are the picture files the editor will take in: ElemImage's own list (theme.ElementKind) and no
// more, since claiming a format the decoder cannot sniff would be a false promise.
//
// Named editIntakeImageExts, not themeImageExts: that name is app.go's per-stem probe order (webp, apng, gif, png;
// AO2-Client get_image_suffix), a different question.
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

// isThemeImageName is the browser's filter: the same question by file name.
func isThemeImageName(name string) bool {
	return isThemeImageExt(strings.ToLower(filepath.Ext(name)))
}

// editImageJob is one in-flight intake, shaped like editFontJob: the channel is buffered so the goroutine never
// blocks on a closed editor, and one runs at a time because the pointer is the flag.
type editImageJob struct {
	// id is the [media] id this will declare, already uniqued against the table.
	id string
	// file is theme-relative with forward slashes, as the [media] row will say it.
	file string
	// src / dest are the only two paths the goroutine touches.
	src, dest string
	done      chan editImageResult
}

// editImageResult is what the goroutine hands back.
//
// The decoded page rides home so the picture appears without a whole theme apply (landThemeMediaPage). The landing
// either uploads or releases it; a Decoded that reaches neither is a pooled buffer that never goes back.
// If the editor closes mid-flight, pollEditorImage dies with a.te and the result is ordinary garbage; draining it
// would block the render thread on a goroutine.
type editImageResult struct {
	dec   *assets.Decoded
	bytes int64  // the DECODED payload, which is what the budget is denominated in
	hash  string // xxhash64 of the source file, lowercase hex — the [media] row's own
	err   error
}

// editorTakeImage is the one entry point for a drop and a browse.
//
// It returns whether the editor claimed the path; false means no editor is open over a writable theme, which the
// global drop handler needs (same contract as editorTakeFontDrop). Every refusal is a chip and returns true.
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
		// editorSidecarPath decides where the theme lives and whether it is writable, and its refusals name the fix.
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
	// The destination goes through safepath, the one copy of the traversal guards, not string joining.
	rel := editImageDirName + "/" + base
	dest, err := safepath.Join(dir, filepath.FromSlash(rel))
	if err != nil {
		a.te.note(base + " cannot be written into the theme folder: " + err.Error())
		return true
	}
	job := &editImageJob{id: id, file: rel, src: path, dest: dest, done: make(chan editImageResult, 1)}
	a.te.image = job
	// Off the render thread (hard rule 2): stat, read, hash, copy and decode happen there.
	anims := a.d.Prefs.Load().PreferAnimated
	go func() { job.done <- runEditImageJob(job, anims) }()
	a.te.note("adding " + base + "…")
	return true
}

// runEditImageJob is the whole off-thread half. GOROUTINE ONLY.
//
// Order matters: refuse by size before reading, hash the bytes actually copied, copy atomically, then decode the
// copy, since the copy is the file the theme will load.
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
		// xxhash64, lowercase hex: MediaEntry.Hash's spelling, as themepack and the disk cache use.
		hash: strconv.FormatUint(xxhash.Sum64(src), 16),
	}
}

// pollEditorImage lands a finished intake once per editor frame, beside pollEditorFont.
func (a *App) pollEditorImage() {
	if a.te == nil || a.te.image == nil {
		return
	}
	// Preview defers the landing: landEditorImage goes through editorApply, which refuses while the demo is up and
	// would release the decoded page. The job waits and lands on the first editing frame after the demo.
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

// landEditorImage is the one place a finished intake changes the document. It always disposes of the decoded
// page: uploaded on success, released on every refusal.
func (a *App) landEditorImage(job *editImageJob, res editImageResult) {
	base := filepath.Base(job.file)
	if res.err != nil {
		a.te.note("could not add " + base + ": " + res.err.Error())
		return
	}
	if refusal := a.editImageBudgetRefusal(res.bytes); refusal != "" {
		// The cap is surfaced with its reason; the file stays in the theme folder, inert until something names it.
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
	// The plan learns about it without a disk walk: planThemeMedia stats every [media] row, which may not happen on
	// a frame (hard rule 2), so the entry is appended from the goroutine's facts.
	a.noteIntakenMedia(row, job.dest)
	// The picture appears now through the incremental land path, not as a placeholder until the next apply.
	if err := a.landThemeMediaPage(a.themeMediaKeyFor(job.id), res.dec); err != nil {
		a.te.note(base + " was added but could not be shown — " + err.Error())
		return
	}
	a.te.selectOnly(elementTarget(len(a.te.doc.side.Elements) - 1))
	a.te.note(base + " added — drag it, or use the inspector to place it")
}

// applyImageIntakeOps is the undoable half of an intake: the [media] declaration and its element as one op group.
// It returns "" on success, or the tail of a refusal sentence naming the cap.
//
// The ring stamps the group onto every op it pushes (themeEditRing.push), so the ops carry no group field.
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

// editImageBudgetRefusal is the intake's budget verdict, or "" for one that fits. It calls the planner's two
// numbers and arithmetic (per-item, then running total), so an accepted image is never refused on apply.
func (a *App) editImageBudgetRefusal(want int64) string {
	mib := a.d.Prefs.Load().TexBudgetMiB()
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

// noteIntakenMedia appends the plan entry for a row just written, as noteCopiedTheme does: planThemeMedia cannot
// run here, and until then the new element would resolve to no key. Bounded by render.ThemeMediaCap.
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

// themeMediaKeyFor is the T1 key a media id occupies under the applied theme's namespace.
func (a *App) themeMediaKeyFor(id string) string {
	name := ""
	if a.d.Prefs != nil {
		name, _ = a.d.Prefs.Load().Theme()
	}
	return render.ThemeMediaKey(themeSanitizeID(name), id)
}

// uniqueMediaID derives a [media] id from a file name and makes it unique inside the table, using
// themeSanitizeID's fold since a media id and a theme id share the id grammar ([a-z0-9_-]{1,24}).
// It is derived rather than typed to avoid a modal at drop time.
func uniqueMediaID(sc *theme.Sidecar, base string) (string, bool) {
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	id := themeSanitizeID(stem)
	// An id of only separators is refused: themeSanitizeID folds every out-of-grammar rune to _, so a name in another
	// script comes back as ___, a legal id every such file would collide on. Asking for a rename is better here.
	if !hasIDSubstance(id) {
		return "", false
	}
	if !mediaIDTaken(sc, id) {
		return id, true
	}
	for n := 2; n < editImageIDTryCap; n++ {
		// The suffix goes on a truncated stem so the result still fits the id grammar's rune cap.
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
// It is placed, not zeroed: a zero Element cannot paint (theme.Element's note on Opacity), so the rail would gain
// a row and the screen nothing. It lands visible at a cascaded offset, sized to the picture's aspect, in
// BandOverlay so the user sees it immediately.
func newImageElement(id string, dec *assets.Decoded, ordinal int) theme.Element {
	w, h := placedImageSize(dec.Width, dec.Height)
	step := (ordinal % editImagePlaceCascade) * editImagePlaceStep
	return theme.Element{
		ID:    id,
		Kind:  theme.ElemImage,
		Band:  theme.BandOverlay,
		Space: theme.SpaceCourtroom,
		Media: id,
		// FitContain, not FitStretch: dragging a corner letterboxes instead of distorting the import.
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

// placedImageSize scales a decoded size down to editImageMaxPlaceEdge, keeping aspect, floored at the min edge.
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
		// Multiply first so a 2000 px long edge does not scale the short edge to zero.
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
