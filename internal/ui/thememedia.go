package ui

// Theme media — the PRODUCER side (v1.90.0 W4). design §Q2.
//
// internal/render owns the store's ceiling; this file owns the PLAN. The apply
// goroutine works out, before it decodes a single byte, which of a theme's declared
// images and generator tiles fit the allowance — and what to do with the ones that
// do not.
//
// TWO ENFORCEMENT POINTS, because a bug in one is a budget breach and the budget is
// the product. The plan here is the first; render.UploadThemeMedia is the second.
// They share ONE number: render.ThemeMediaByteCap, called (never copied) from both
// sides, so the planner and the store cannot disagree about what the allowance is.
//
// AT THE CAP THE USER NEVER DEAD-ENDS. An over-budget entry is not dropped and the
// theme is not refused: the element is planned in state elemOverBudget, carrying its
// own reason ("anime-girl.webp — 18.2 MiB, over the 8.0 MiB per-image cap"), and the
// editor draws it as a hatch placeholder with inline fixes. The theme still saves.
// A silent drop would make a theme render differently depending on the order its
// author added elements, which is a property nobody can reason about — and refusing
// the import dead-ends somebody at the exact moment they are being creative.

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
	// themeMediaFileByteCap refuses an oversized SOURCE before any decode runs, via
	// an io.LimitedReader-shaped read, so a pathological file never reaches the
	// decoder at all. 32 MiB is four times the per-item DECODED cap at the default
	// budget, which comfortably covers any legitimately-compressed source and stops
	// a decompression bomb one layer earlier than the decode cap would.
	themeMediaFileByteCap = 32 << 20
	// themeMediaMaxEdgePx is the import-time downscale offer's target long edge.
	// 1920 is the largest edge that is still meaningfully "a screen" — past it a
	// theme is paying for pixels the canvas fit is about to throw away.
	themeMediaMaxEdgePx = 1920
	// themeArtWarnPct is where the editor's budget bar turns ColDanger.
	themeArtWarnPct = 85
	// themeMediaIDMaxLen bounds the id used in a cache key. It is the format's own
	// id grammar bound, aliased rather than restated.
	themeMediaIDMaxLen = theme.IDRuneCap
)

// themeMediaState is what the planner decided about one declared entry.
type themeMediaState uint8

const (
	// mediaPlanned: it fits, and the apply will decode and upload it.
	mediaPlanned themeMediaState = iota
	// mediaOverBudget: legal, declared, and over one of the two caps. The element is
	// still CREATED — it draws as a placeholder carrying its own reason.
	mediaOverBudget
	// mediaMissing: the file the theme declares is not in the theme folder (or is
	// outside it, which internal/safepath refuses). Also a placeholder, different
	// reason.
	mediaMissing
)

// themeMediaPlanEntry is one declared media id, planned.
type themeMediaPlanEntry struct {
	// ID is the [media] id, i.e. what an element's `media =` names.
	ID string
	// Key is the T1 key this entry will occupy — theme-namespaced, so two themes
	// that both declare "bg" can never share a page.
	Key string
	// Path is the resolved on-disk file ("" when missing).
	Path string
	// Bytes is the DECLARED decoded size from the [media] row. The author declares
	// it so the plan can budget before it decodes anything a stranger sent; the
	// store re-measures the real payload at the door, which is why a lying manifest
	// costs the liar an element rather than costing everyone the budget.
	Bytes int64
	State themeMediaState
	// Why is the one-line reason for a non-planned state, ready for the inspector.
	Why string
}

// themeMediaPlan is the whole decision for one apply. Fixed-size by construction —
// the reader already refuses a sidecar past theme.MediaCap.
type themeMediaPlan struct {
	Entries []themeMediaPlanEntry
	// Gens are the generator tiles this theme needs, deduplicated by content
	// address: two elements with identical params are ONE tile.
	Gens []themeGenPlanEntry
	// ElemKeys is the T1 key each ELEMENT resolves to, indexed by the element's own
	// position in Sidecar.Elements ("" for an element that has no art).
	//
	// It exists so the BAKE can resolve a page with an indexed load instead of
	// re-deriving a key. Re-deriving would mean lowercasing a name and hashing a
	// param list once per element per rebuild, and the bake's allocation ceiling
	// (elemBakeAllocCeil) is a CONSTANT rather than a per-element budget precisely so
	// that a preset swap cannot hitch the frame it lands on.
	ElemKeys []string
	// Bytes / GenBytes are what the plan commits.
	Bytes, GenBytes int64
	// Budget is the allowance this plan was made against, carried so the budget bar
	// and the inspector quote the same number the planner used.
	Budget int64
	// ArtSig signs the ELEMENTS this plan's keys were derived from (themeArtSig).
	//
	// It rides on the plan rather than beside it because a signature that can be set
	// without deriving the keys is a signature that will one day say the plan is
	// current when it is not: planElements — the ONE derivation — stamps it in the
	// same breath as it fills ElemKeys, so the two cannot disagree, and a plan
	// carried between goroutines carries its own provenance with it.
	//
	// Its consumer is the editor (themeeditorart.go), which asks every frame whether
	// the drawn art still matches the document being edited.
	ArtSig uint64
}

// themeGenPlanEntry is one generator tile: its spec, its content-addressed key and
// the tile size the raster will produce.
type themeGenPlanEntry struct {
	Spec theme.GeneratorSpec
	Key  string
	W, H int32
}

// planThemeMedia decides what a theme's declared art costs before anything is read
// from disk. Runs on the theme-apply goroutine.
//
// DECLARATION ORDER, first-come-first-served, with an explicit refusal — never an
// eviction to make room for a latecomer. That is design §Q2's "silent eviction is
// forbidden", and the reason is that a theme whose appearance depended on the order
// its author added elements is a theme nobody can debug.
func planThemeMedia(sc *theme.Sidecar, themeID, dir string, texBudgetMiB int) themeMediaPlan {
	// BOTH numbers come from internal/render, by CALL rather than by copy. The store
	// re-checks each one at the door, and two copies of a budget are two budgets.
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

// planElements derives the ELEMENT half of a plan: the T1 key every element resolves
// to, the generator tiles those keys need, and the signature of the model they came
// from.
//
// SPLIT OUT OF planThemeMedia — AND CALLED BY IT, so there is exactly one copy — for
// the editor (v1.90.0 W7b). The inspector edits an element's `kind`, `media`,
// `generator` and `gen_params`, and adding or deleting an element shifts every later
// index; all four therefore change which page an element draws, and the bake resolves
// that page by INDEX out of this array (themeelementbake.go). Re-deriving this half in
// place is what makes those rows change the picture instead of the file, and it is the
// half that CAN be re-derived on a frame: the [media] entries above stat the theme
// folder, which is disk I/O the render thread may not do (hard rule 2), and they
// cannot move under the editor anyway because no op writes the [media] table.
//
// IDEMPOTENT, and it ALLOCATES its two slices fresh rather than reslicing what it was
// handed. The editor derives into a COPY of the live plan, so reusing the backing
// arrays would write through them while the frame that is still drawing reads the old
// lengths.
//
// Generator tiles are deduplicated by content address, and they are planned against
// their own count cap rather than competing with images for one, because a tile is
// 256 KiB at worst and refusing one to keep a wallpaper would be the wrong trade in
// every case.
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
			// An image element points at a [media] row. It gets the key whether or not
			// the entry was admitted: an over-budget element still EXISTS, and the key
			// is what the inspector uses to name what it is waiting for.
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

// themeArtSig signs everything planElements READS out of the elements, and nothing
// else: the kind, the media id, the generator name and its params, the `hidden` flag
// that Inert() turns on, and the element COUNT and ORDER (the keys are indexed by
// position, so a delete that shifts the tail changes the answer).
//
// IT IS themeDoc.typeSig's TWIN, and it exists for the same reason: a save has to be
// able to ask "did the TYPE change" without asking "did anything change", and a frame
// has to be able to ask "did the ART change" without re-planning for a rect nudge.
// Same FNV-1a parameters, same terminator-per-field fold, so "ab"+"c" and "a"+"bc"
// cannot sign alike.
//
// ALLOCATION-FREE, which typeSig does not have to be and this does: it is read on
// EVERY editor frame. That is why it folds the RAW fields instead of building the
// normalised theme.GeneratorSpec the key is hashed from — GeneratorSpecOf lower-cases
// and trims, i.e. allocates, and this only has to CHANGE when the spec would, never to
// agree with it.
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

// ElemKey is the T1 key element i resolves to, or "" — bounds-checked, because the
// plan and the sidecar are landed by two different paths and a stale plan beside a
// newer sidecar must degrade to "no art" rather than panic on the render thread.
func (p *themeMediaPlan) ElemKey(i int) string {
	if p == nil || i < 0 || i >= len(p.ElemKeys) {
		return ""
	}
	return p.ElemKeys[i]
}

// MediaState reports what the planner decided about the entry an element points at.
// mediaPlanned for anything with no [media] row of its own, so a generator or a
// shape is never mistaken for a placeholder.
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

// themeMediaMiB renders a byte count the way the inspector and the budget bar
// quote it. One decimal, because "18.2 MiB" is actionable and "19088743 bytes" is
// not.
func themeMediaMiB(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// themeMediaPath resolves a [media] file inside the theme folder, refusing anything
// that escapes it.
//
// internal/safepath is THE copy of the zip-slip / symlink / `..` / depth guards
// (CLAUDE.md's package map says so in as many words), and a theme is exactly the
// untrusted-archive shape those guards exist for: the file name comes out of a file
// a stranger wrote, and the format's own §6 says "absolute file paths, or any path
// outside the theme folder" are deliberately not in it.
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

// themeSanitizeID folds a theme folder name into the id used in a cache key.
//
// IDENTITY IS THE FOLDER NAME (docs/THEME-FORMAT.md §1), lowercased and stripped of
// anything that is not the id grammar — never the sidecar's display name, because
// re-shared zips get renamed constantly and a manifest-name identity would make two
// different themes the same theme, which is precisely the collision the namespace
// exists to prevent.
//
// THE FOLD IS LOSSY AND THAT WAS CONSIDERED. Every rune outside the grammar becomes
// '_', so "My Theme" and "My_Theme" — two folders that can coexist on disk — share
// one namespace, and the id is truncated at themeMediaIDMaxLen besides. Benign,
// because a key is only ever a CACHE key here and only one theme is applied at a
// time: landThemeMedia prunes every key the incoming theme did not declare and then
// uploads its own, so a shared key is REPLACED by the theme that is actually on
// screen rather than read from the other one, and a key whose upload was refused or
// whose decode failed is pruned rather than left standing. The property the
// namespace has to carry is "theme A's art cannot be served to theme B", and
// replace-on-apply gives that even when the two ids fold together.
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

// ---------------------------------------------------------------------------
// Decode + downscale
// ---------------------------------------------------------------------------

// loadThemeMedia reads and decodes one planned entry, downscaling it when its long
// edge is past themeMediaMaxEdgePx. Theme-apply goroutine only (hard rule 2).
//
// The downscale is x/image/draw's CatmullRom, which is already a DIRECT dependency
// doing exactly this in assets/thumbcache.go — no new dependency, no
// docs/ARCHITECTURE.md justification, and emphatically not a hand-rolled box filter.
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

// downscaleThemeMedia resamples every frame when the long edge is over the cap.
//
// It returns the SAME Decoded when nothing needs resizing, which is the ordinary
// case and costs one comparison — the v1.89.0 "0 base = 0 cost" habit applied one
// tier down.
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
	// The source's pooled buffers go back now: the resampled frames are ours and the
	// originals are unreachable from here on.
	d.Release()
	return out
}

// themeDecimationNote is the inspector's honest concession about a long animation,
// or "" when nothing was dropped.
//
// design §Q2 states the concession in the product rather than hiding it: a 60 fps
// full-screen animated background is not deliverable inside a 256 MiB process. What
// IS deliverable is the correct DURATION, an exact count of the frames kept, and the
// name of the one knob that changes it — which is what this sentence carries.
// assets.Decoded.SourceFrames already reports the pre-decimation count, so nothing
// new has to be measured.
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

// themeArtBarLabel is the editor's permanent budget line: what is spent, out of
// what, and how many of each cap is in use.
//
// PERMANENT rather than shown-at-the-cap, because the whole point is that the user
// can always see who to blame before they hit it.
func themeArtBarLabel(bytes int64, images, gens int, budget int64) string {
	return fmt.Sprintf("Theme art %s / %s · %d / %d images · %d / %d tiles",
		themeMediaMiB(bytes), themeMediaMiB(budget), images, render.ThemeMediaCap, gens, render.ThemeGenCap)
}

// themeArtOverWarnPct reports whether the budget bar should turn ColDanger.
func themeArtOverWarnPct(bytes, budget int64) bool {
	return budget > 0 && bytes*100 >= budget*themeArtWarnPct
}

// ---------------------------------------------------------------------------
// The apply: load off-thread, land on the render thread
// ---------------------------------------------------------------------------

// loadThemeMediaSet decodes every PLANNED entry and rasterises every planned
// generator tile. Theme-apply goroutine only: it reads files and fills 256² pixel
// buffers, neither of which may happen on a frame (hard rule 2).
//
// An entry that fails to decode is downgraded to mediaMissing in place, so the
// element still exists and still says why — the same "never a silent drop" rule the
// over-budget path follows.
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

// releaseDecoded hands every pixel buffer this apply is still holding back to the
// decoder's pool, for an apply that will never land.
//
// The DROP PATH needs it and the land path does not: an upload copies the pixels
// into a texture and the store's own accounting takes over from there, but a result
// that loses the consume-side newest-wins race (pollThemeApply) is dropped whole,
// and without this its buffers reach the GC as ordinary garbage instead of the pool
// they came from. W4 is what made that worth a line of code: a stale drop used to
// strand at most the chrome stems, and it can now strand those PLUS up to
// render.ThemeMediaCap media pages and render.ThemeGenCap generator tiles, each one
// of them a full-size RGBA frame set.
//
// Release is idempotent (it nils both Frames and the pooled list), so this is safe
// against a future caller that releases twice, and it is pure memory bookkeeping —
// no I/O, nothing that could not run on either side of the hand-off.
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

// landThemeMedia lands one apply's art on the render thread.
//
// PRUNE FIRST, THEN ADMIT — the opposite order to the chrome loop's, and the order
// is load-bearing. Chrome uploads then prunes because it has no budget and replacing
// a skin in place avoids a one-frame gap. Theme media HAS a budget, and the outgoing
// theme's art is not part of the incoming theme's allowance: admit-then-prune
// measures the new pages against an allowance the old theme is still holding, so a
// swap between two heavy themes admits nothing and then leaves the user with a blank
// one. Both halves happen inside one pollThemeApply, before the frame paints, so
// pruning first costs nothing visible.
func (a *App) landThemeMedia(res *themeApply) {
	budgetMiB := a.d.Prefs.TexBudgetMiB()
	if a.themeMediaKeys == nil {
		// NewApp seeds it; a hand-built App (a test fixture, or a future caller) does
		// not, and a nil map is a panic on the WRITE below rather than on the reads
		// above — which would land on the render thread the first time somebody
		// applied a theme with art.
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
			// The store refused what the planner admitted. That is the two enforcement
			// points disagreeing, which is worth a log line rather than a shrug — but
			// the element still exists and still draws its placeholder, so nothing is
			// lost and nothing is silent.
			a.pushDebug("theme media " + key + ": " + err.Error())
			continue
		}
		a.themeMediaKeys[key] = true
	}
	a.themeMediaPlan = res.mediaPlan
}

// landThemeMediaPage is the INCREMENTAL land path: one page, in place, with no
// theme apply behind it.
//
// It exists for the editor (W7), where changing one image must not restage the whole
// theme — and the two things it deliberately does NOT do are the whole point:
//
//   - it does not re-anchor a.themeAt, so every animated element keeps its phase.
//     Restarting the theme clock makes every looping generator and every animated
//     skin on screen jump back to frame 0, which reads as the client glitching every
//     time the author nudges a value.
//   - it does not purge the text cache. That purge exists for a PALETTE change (label
//     textures are colour-keyed); a media page has no colour anyone cached, and
//     purging would re-raster every string in the client for one image.
//
// Gated by TestIncrementalMediaLandDoesNotRestartTheThemeClock. Render thread only.
func (a *App) landThemeMediaPage(key string, d *assets.Decoded) error {
	if d == nil {
		return fmt.Errorf("theme media %s: nothing to land", key)
	}
	if err := a.d.Store.UploadThemeMedia(key, d, a.d.Prefs.TexBudgetMiB()); err != nil {
		return err
	}
	if a.themeMediaKeys == nil {
		a.themeMediaKeys = map[string]bool{}
	}
	a.themeMediaKeys[key] = true
	// The generation-keyed page cache is what a draw site reads through, and
	// UploadThemeMedia already bumped the store generation — so clearing this map is
	// belt to that brace and costs one map clear on an editor keystroke.
	clear(a.themePages)
	return nil
}

// themeMediaPage resolves a theme-media key to its page through the SAME
// generation-keyed cache theme chrome uses (themePage), so a settled frame costs one
// map probe and an evicted-by-purge page heals on the same paced re-kick.
func (a *App) themeMediaPage(key string) (*render.TexturePage, bool) {
	if key == "" || !a.themeMediaKeys[key] {
		return nil, false
	}
	gen := a.d.Store.Generation()
	if a.themePages == nil {
		// Lazily created, because this is reached from the BAKE as well as from a
		// draw: themePage (the chrome twin) only ever runs after a theme apply has
		// populated themeTex, so it can assume the map, and this one cannot.
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

// themeMediaDebugLine is the F8 panel's `theme media` row, beside heldSteals /
// heldReleases. One line, built only while the panel is open.
func (a *App) themeMediaDebugLine() string {
	budget := render.ThemeMediaByteCap(a.d.Prefs.TexBudgetMiB())
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
