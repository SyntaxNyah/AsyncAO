package ui

// The theme media planner decides, before any decode, which declared images and generator tiles fit the allowance.
// render.UploadThemeMedia enforces it again; both call render.ThemeMediaByteCap, so they cannot disagree.
// An over-budget entry is planned with its own reason and drawn as a placeholder, never silently dropped or refused.

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// themeMediaFileByteCap refuses an oversized source before any decode, stopping a decompression bomb before the
	// decoded cap.
	themeMediaFileByteCap = 32 << 20
	// themeMediaMaxEdgePx is the downscale target's long edge; past it a theme pays for pixels the canvas fit throws
	// away.
	themeMediaMaxEdgePx = 1920
	// themeArtWarnPct is where the editor's budget bar turns ColDanger.
	themeArtWarnPct = 85
	// themeMediaIDMaxLen bounds the id used in a cache key, aliasing the format's own bound.
	themeMediaIDMaxLen = theme.IDRuneCap
)

// themeMediaState is what the planner decided about one declared entry.
type themeMediaState uint8

const (
	// mediaPlanned: it fits, and the apply will decode and upload it.
	mediaPlanned themeMediaState = iota
	// mediaOverBudget: over one of the two caps; the element is still created and draws a placeholder with its reason.
	mediaOverBudget
	// mediaMissing: the declared file is absent or outside the theme folder (refused by internal/safepath); also a
	// placeholder.
	mediaMissing
)

// themeMediaPlanEntry is one declared media id, planned.
type themeMediaPlanEntry struct {
	// ID is the [media] id an element's `media =` names.
	ID string
	// Key is this entry's T1 key, theme-namespaced so two themes declaring "bg" never share a page.
	Key string
	// Path is the resolved on-disk file ("" when missing).
	Path string
	// Bytes is the declared decoded size, so the plan can budget before decoding; the store re-measures the real
	// payload.
	Bytes int64
	State themeMediaState
	// Why is the one-line reason for a non-planned state, ready for the inspector.
	Why string
}

// themeMediaPlan is the whole decision for one apply; the reader refuses a sidecar past theme.MediaCap.
type themeMediaPlan struct {
	Entries []themeMediaPlanEntry
	// Gens are the generator tiles needed, deduplicated by content address.
	Gens []themeGenPlanEntry
	// ElemKeys is each element's T1 key by Sidecar.Elements position ("" for no art). The bake indexes it rather than
	// re-deriving keys, whose lowercasing and hashing per element would break its constant allocation ceiling.
	ElemKeys []string
	// Bytes / GenBytes are what the plan commits.
	Bytes, GenBytes int64
	// Budget is the allowance this plan was made against, so the bar and inspector quote the planner's number.
	Budget int64
	// ArtSig signs the elements these keys came from (themeArtSig). planElements stamps both together, so they cannot
	// disagree. The editor asks every frame whether the drawn art still matches its document.
	ArtSig uint64
}

// themeGenPlanEntry is one generator tile: spec, content-addressed key, and raster size.
type themeGenPlanEntry struct {
	Spec theme.GeneratorSpec
	Key  string
	W, H int32
}

// planThemeMedia decides what a theme's art costs before reading any, on the theme-apply goroutine.
// Declaration order, first come first served, refused explicitly: appearance must not depend on who eviction hit.
func planThemeMedia(sc *theme.Sidecar, themeID, dir string, texBudgetMiB int) themeMediaPlan {
	// Both numbers come from internal/render by call: the store re-checks them, and two copies would be two budgets.
	budget := render.ThemeMediaByteCap(texBudgetMiB)
	item := render.ThemeMediaItemByteCap(texBudgetMiB)
	plan := themeMediaPlan{Budget: budget}
	if sc == nil {
		return plan
	}
	for _, m := range sc.Media {
		if len(plan.Entries) >= render.ThemeMediaCap {
			break // the reader's own cap already bounds this; belt.
		}
		e := themeMediaPlanEntry{
			ID:    m.ID,
			Key:   render.ThemeMediaKey(themeID, m.ID),
			Bytes: m.Bytes,
		}
		path, ok := themeMediaPath(dir, m.File)
		switch {
		case !ok:
			e.State, e.Why = mediaMissing, fmt.Sprintf("%s — not found in the theme folder", m.File)
		case m.Bytes > item:
			e.State = mediaOverBudget
			e.Why = fmt.Sprintf("%s — %s, over the %s per-image cap. Downscale to fit, or remove it.",
				m.File, themeMediaMiB(m.Bytes), themeMediaMiB(item))
		case plan.Bytes+m.Bytes > budget:
			e.State = mediaOverBudget
			e.Why = fmt.Sprintf("%s — %s would take this theme past its %s art budget (%s already used). "+
				"Downscale it, or remove another image.",
				m.File, themeMediaMiB(m.Bytes), themeMediaMiB(budget), themeMediaMiB(plan.Bytes))
		default:
			e.Path = path
			plan.Bytes += m.Bytes
		}
		plan.Entries = append(plan.Entries, e)
	}
	plan.planElements(sc, themeID)
	return plan
}

// planElements derives each element's T1 key, the generator tiles needed, and their signature.
// Split out for the editor: inspector edits change these, and this half can run on a frame, unlike [media] entries,
// which stat files (hard rule 2). Allocates fresh slices, because the editor derives into a copy of the plan
// while the current frame reads the old arrays.
func (plan *themeMediaPlan) planElements(sc *theme.Sidecar, themeID string) {
	if sc == nil {
		plan.ElemKeys, plan.Gens, plan.GenBytes, plan.ArtSig = nil, nil, 0, themeArtSig(nil)
		return
	}
	plan.ElemKeys = make([]string, len(sc.Elements))
	plan.Gens = nil
	plan.GenBytes = 0
	for i := range sc.Elements {
		el := &sc.Elements[i]
		switch {
		case el.Kind == theme.ElemImage && el.Media != "":
			// Gets the key even when not admitted: an over-budget element still exists, and the inspector names it by
			// this key.
			if _, found := sc.FindMedia(el.Media); found {
				plan.ElemKeys[i] = render.ThemeMediaKey(themeID, el.Media)
			}
			continue
		case el.Kind != theme.ElemGen || el.Gen == "" || el.Inert():
			continue
		}
		spec := theme.GeneratorSpecOf(el)
		if !GeneratorKnown(spec.Name) {
			continue // degrades to a flat fill; nothing to rasterise, nothing to budget
		}
		key := render.ThemeGenKey(spec.Hash())
		plan.ElemKeys[i] = key
		if themeGenPlanned(plan.Gens, key) {
			continue // the same params ARE the same tile
		}
		if len(plan.Gens) >= render.ThemeGenCap {
			plan.ElemKeys[i] = "" // past the cap: no tile, so no key to resolve to
			continue
		}
		w, h := genTileSizeOf(spec) // ONE spec: the budget and the raster cannot disagree
		plan.Gens = append(plan.Gens, themeGenPlanEntry{Spec: spec, Key: key, W: w, H: h})
		plan.GenBytes += int64(w) * int64(h) * 4
	}
	plan.ArtSig = themeArtSig(sc)
}

// themeArtSig signs what planElements reads from the elements, including count and order, since keys are indexed by
// position. Same FNV-1a fold as themeDoc.typeSig, with a terminator per field so "ab"+"c" and "a"+"bc" differ. Read
// every editor frame, so it folds raw fields: GeneratorSpecOf allocates, and this only must change when the spec would.
func themeArtSig(sc *theme.Sidecar) uint64 {
	h := typeSigOffset64
	if sc == nil {
		return h
	}
	for i := range sc.Elements {
		el := &sc.Elements[i]
		h = typeSigFoldByte(h, uint8(el.Kind))
		h = typeSigFoldByte(h, boolByte(el.Hidden))
		h = typeSigFold(h, el.Media)
		h = typeSigFold(h, el.Gen)
		for j := 0; j < el.GenParamCount(); j++ {
			h = typeSigFold(h, el.GenParams[j].Key)
			h = typeSigFold(h, el.GenParams[j].Value)
		}
	}
	return h
}

// ElemKey is element i's T1 key or "", bounds-checked because a stale plan beside a newer sidecar must not panic on the
// render thread.
func (p *themeMediaPlan) ElemKey(i int) string {
	if p == nil || i < 0 || i >= len(p.ElemKeys) {
		return ""
	}
	return p.ElemKeys[i]
}

// MediaState reports the planner's decision for an element's entry; mediaPlanned when it has no [media] row.
func (p *themeMediaPlan) MediaState(mediaID string) (themeMediaState, string) {
	if p == nil || mediaID == "" {
		return mediaPlanned, ""
	}
	for i := range p.Entries {
		if p.Entries[i].ID == mediaID {
			return p.Entries[i].State, p.Entries[i].Why
		}
	}
	return mediaPlanned, ""
}

// themeGenPlanned reports whether a content address is already in the plan.
func themeGenPlanned(gens []themeGenPlanEntry, key string) bool {
	for i := range gens {
		if gens[i].Key == key {
			return true
		}
	}
	return false
}

// themeMediaMiB renders a byte count for the inspector and budget bar.
func themeMediaMiB(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// themeMediaPath resolves a [media] file inside the theme folder via internal/safepath: the name comes from an
// untrusted file.
func themeMediaPath(dir, file string) (string, bool) {
	if dir == "" || file == "" {
		return "", false
	}
	rel := filepath.FromSlash(strings.TrimSpace(file))
	full, err := safepath.Join(dir, rel)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return "", false
	}
	if info.Size() > themeMediaFileByteCap {
		return "", false // refused BEFORE any decode: a bomb never reaches the decoder
	}
	return full, true
}

// themeSanitizeID folds a theme folder name into a cache key id. Identity is the folder name, not the sidecar's display
// name, because re-shared zips get renamed and two themes would collide. The fold is lossy ("My Theme" and "My_Theme"
// share an id), which is safe: landThemeMedia prunes and replaces on apply, so one theme's art cannot be served to
// another.
func themeSanitizeID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= themeMediaIDMaxLen {
			break
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// loadThemeMedia reads and decodes one planned entry, downscaling a long edge past themeMediaMaxEdgePx.
// Theme-apply goroutine only (hard rule 2).
func loadThemeMedia(path string, anims bool) (*assets.Decoded, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > themeMediaFileByteCap {
		return nil, fmt.Errorf("theme media %s is %d bytes, over the %d source cap", filepath.Base(path), len(data), themeMediaFileByteCap)
	}
	d, err := assets.DecodeImage(data, anims)
	if err != nil {
		return nil, err
	}
	return downscaleThemeMedia(d), nil
}

// downscaleThemeMedia resamples every frame when the long edge is over the cap, returning the same Decoded otherwise.
func downscaleThemeMedia(d *assets.Decoded) *assets.Decoded {
	if d == nil || len(d.Frames) == 0 {
		return d
	}
	long := d.Width
	if d.Height > long {
		long = d.Height
	}
	if long <= themeMediaMaxEdgePx {
		return d
	}
	w := d.Width * themeMediaMaxEdgePx / long
	h := d.Height * themeMediaMaxEdgePx / long
	if w < 1 || h < 1 {
		return d
	}
	out := &assets.Decoded{
		Animated:     d.Animated,
		SourceFrames: d.SourceFrames,
		Delays:       d.Delays,
		Width:        w,
		Height:       h,
	}
	for _, src := range d.Frames {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
		out.Frames = append(out.Frames, dst)
	}
	// Return the source's pooled buffers: the originals are unreachable from here on.
	d.Release()
	return out
}

// themeDecimationNote is the inspector's note about a decimated long animation, or "" when nothing was dropped.
func themeDecimationNote(d *assets.Decoded) string {
	if d == nil || d.SourceFrames <= len(d.Frames) {
		return ""
	}
	var total int64
	for _, delay := range d.Delays {
		total += delay.Milliseconds()
	}
	kept := len(d.Frames)
	fps := 0.0
	if total > 0 {
		fps = float64(kept) * 1000 / float64(total)
	}
	return fmt.Sprintf("%d frames → %d kept · plays over the full %.1f s at ~%.0f fps. "+
		"Raise Settings ▸ General ▸ Texture budget, or downscale, for more frames.",
		d.SourceFrames, kept, float64(total)/1000, fps)
}

// themeArtBarLabel is the editor's permanent budget line, so the user can see the usage before hitting a cap.
func themeArtBarLabel(bytes int64, images, gens int, budget int64) string {
	return fmt.Sprintf("Theme art %s / %s · %d / %d images · %d / %d tiles",
		themeMediaMiB(bytes), themeMediaMiB(budget), images, render.ThemeMediaCap, gens, render.ThemeGenCap)
}

// themeArtOverWarnPct reports whether the budget bar should turn ColDanger.
func themeArtOverWarnPct(bytes, budget int64) bool {
	return budget > 0 && bytes*100 >= budget*themeArtWarnPct
}

// loadThemeMediaSet decodes planned entries and rasterises generator tiles; theme-apply goroutine only (hard rule 2).
// A decode failure downgrades to mediaMissing in place, so the element still exists and says why.
func (res *themeApply) loadThemeMediaSet(anims bool) {
	res.media = map[string]*assets.Decoded{}
	for i := range res.mediaPlan.Entries {
		e := &res.mediaPlan.Entries[i]
		if e.State != mediaPlanned || e.Path == "" {
			continue
		}
		d, err := loadThemeMedia(e.Path, anims)
		if err != nil || d == nil || len(d.Frames) == 0 {
			e.State = mediaMissing
			e.Why = fmt.Sprintf("%s — could not be decoded", filepath.Base(e.Path))
			continue
		}
		if note := themeDecimationNote(d); note != "" {
			e.Why = note // not a failure: the honest frame-count concession (§Q2)
		}
		res.media[e.Key] = d
	}
	for _, g := range res.mediaPlan.Gens {
		img := RasterGenerator(g.Spec)
		if img == nil {
			continue // an unknown name; the element degrades to its own flat fill
		}
		b := img.Bounds()
		res.media[g.Key] = &assets.Decoded{
			Frames: []*image.RGBA{img},
			Width:  b.Dx(),
			Height: b.Dy(),
		}
	}
}

// releaseDecoded returns this apply's pixel buffers to the decoder's pool, for an apply that will never land.
// Needed when a result loses pollThemeApply's newest-wins race. Release is idempotent.
func (res *themeApply) releaseDecoded() {
	for _, d := range res.images {
		if d != nil {
			d.Release()
		}
	}
	for _, d := range res.media {
		if d != nil {
			d.Release()
		}
	}
}

// landThemeMedia lands one apply's art on the render thread. Prune first, then admit: the outgoing theme's art is not
// in the incoming allowance, so the reverse order would admit nothing on a swap between two heavy themes.
func (a *App) landThemeMedia(res *themeApply) {
	budgetMiB := a.d.Prefs.Load().TexBudgetMiB()
	if a.themeMediaKeys == nil {
		// A hand-built App may have a nil map, and the write below would panic on the render thread.
		a.themeMediaKeys = map[string]bool{}
	}
	declared := make(map[string]bool, len(res.media))
	for key := range res.media {
		declared[key] = true
	}
	for key := range a.themeMediaKeys {
		if declared[key] {
			continue
		}
		a.d.Store.Remove(key)
		delete(a.themeMediaKeys, key)
	}
	for key, d := range res.media {
		if err := a.d.Store.UploadThemeMedia(key, d, budgetMiB); err != nil {
			// The two enforcement points disagree, worth a log line; the element still draws its placeholder.
			a.pushDebug("theme media " + key + ": " + err.Error())
			continue
		}
		a.themeMediaKeys[key] = true
	}
	a.themeMediaPlan = res.mediaPlan
}

// landThemeMediaPage lands one page in place for the editor; render thread only.
// It does not re-anchor a.themeAt, which would jump every animation to frame 0, or purge the text cache,
// which exists for palette changes and would re-raster every string.
func (a *App) landThemeMediaPage(key string, d *assets.Decoded) error {
	if d == nil {
		return fmt.Errorf("theme media %s: nothing to land", key)
	}
	if err := a.d.Store.UploadThemeMedia(key, d, a.d.Prefs.Load().TexBudgetMiB()); err != nil {
		return err
	}
	if a.themeMediaKeys == nil {
		a.themeMediaKeys = map[string]bool{}
	}
	a.themeMediaKeys[key] = true
	// UploadThemeMedia already bumped the store generation, so this clear is a cheap safety measure.
	clear(a.themePages)
	return nil
}

// themeMediaPage resolves a theme-media key through themePage's generation-keyed cache: a settled frame is one map
// probe.
func (a *App) themeMediaPage(key string) (*render.TexturePage, bool) {
	if key == "" || !a.themeMediaKeys[key] {
		return nil, false
	}
	gen := a.d.Store.Generation()
	if a.themePages == nil {
		// Lazily created because the bake reaches this too; themePage only runs after an apply has populated themeTex.
		a.themePages = map[string]*render.TexturePage{}
	}
	if gen != a.themePagesGen {
		clear(a.themePages)
		a.themePagesGen = gen
	}
	if page, cached := a.themePages[key]; cached {
		return page, page != nil
	}
	page, ok := a.d.Store.Get(key)
	if !ok || len(page.Frames) == 0 {
		a.themePages[key] = nil
		a.healTheme()
		return nil, false
	}
	a.themePages[key] = page
	return page, true
}

// themeMediaDebugLine is the F8 panel's `theme media` row, built only while the panel is open.
func (a *App) themeMediaDebugLine() string {
	budget := render.ThemeMediaByteCap(a.d.Prefs.Load().TexBudgetMiB())
	return fmt.Sprintf("theme media %s / %s · %d images · %d tiles · %d over budget",
		themeMediaMiB(a.d.Store.ThemeMediaBytes()), themeMediaMiB(budget),
		a.d.Store.ThemeMediaCount(), a.d.Store.ThemeGenCount(), a.themeMediaOverBudget())
}

// themeMediaOverBudget counts the elements the plan created as placeholders.
func (a *App) themeMediaOverBudget() int {
	n := 0
	for i := range a.themeMediaPlan.Entries {
		if a.themeMediaPlan.Entries[i].State != mediaPlanned {
			n++
		}
	}
	return n
}
