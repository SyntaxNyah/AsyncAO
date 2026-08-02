package ui

// The system-font CENSUS: the backstop that ends the hardcoded-fallback-list
// treadmill, and the reason a rune the client cannot draw should now be a
// genuinely unassigned codepoint rather than "nobody added that font path yet".
//
// The curated tiers (fontfallback.go) stay: they are ranked, they pick the RIGHT
// face for a script rather than merely a covering one — Japanese before Chinese
// for shared Han, a modern proportional Gothic before a bitmap-era legacy face —
// and for the overwhelming majority of sessions they answer everything with two
// or three faces. The census only runs when something reaches the draw that none
// of them covers. Then it asks a much simpler question: of every font actually
// installed on this machine, which one has this rune? That is self-maintaining
// (a user who installs a Tibetan font gets Tibetan), OS-agnostic (it is the only
// reason macOS and Linux are not permanently stuck on the embedded Latin face),
// and it needs no new font bytes shipped in the binary.
//
// EVERYTHING HERE IS OFF THE RENDER THREAD. Parsing is done through
// sfnt.ParseReaderAt, so a face is never slurped into the heap — but that also
// means every GlyphIndex call is a disk read, which is exactly the synchronous
// I/O the render path forbids (CLAUDE.md rule 2). The render thread only ever
// hands over runes and receives font BYTES; it never touches a census face.

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Bounds. Every one of these is the named cap CLAUDE.md rule 4 asks for.
const (
	// fontCensusMaxDepth / fontCensusMaxFiles bound the directory walk. Linux font
	// trees nest by foundry and family, hence a depth of 3 rather than a flat scan;
	// 512 files is comfortably past a stock Windows 11 install (~150) or a
	// well-stocked Linux desktop, and stops a user who has symlinked a font farm
	// into ~/.fonts from turning the first non-Latin rune into a minutes-long walk.
	fontCensusMaxDepth = 3
	fontCensusMaxFiles = 512

	// censusFaceCap bounds how many faces the census may add to the live chain over
	// a whole session. Each one is opened by buildSet into every per-size font set,
	// so this is really a cap on open FreeType faces and their glyph caches, not
	// just on bytes. Six is more scripts than any real conversation mixes, and the
	// curated tiers have already answered the common ones before we get here.
	censusFaceCap = 6

	// censusQueueCap bounds the runes awaiting an answer. The render thread records
	// misses into a fixed array (no allocation, no growth); past this it simply
	// stops recording until the queue drains, because a message full of unknown
	// runes needs one answer, not four hundred.
	censusQueueCap = 32

	// censusMaxAnswered bounds the memo of runes already resolved (or proven
	// unresolvable), so a session cannot accumulate an unbounded map by scrolling
	// through text nothing can draw.
	censusMaxAnswered = 4096
)

// fontCensusFile is one indexed font file: its path and the parsed cmap face used
// to answer coverage questions. The face is ReaderAt-backed, so this holds a file
// handle rather than the file's bytes.
type fontCensusFile struct {
	path string
	face *sfnt.Font
	file *os.File
}

// fontCensus indexes the machine's installed fonts and answers "which file has
// this rune". Owned entirely by its goroutine — no mutex, because nothing else
// ever touches it.
type fontCensus struct {
	files    []fontCensusFile
	answered map[rune]string // rune → path ("" = nothing on this machine has it)
	indexed  bool
}

// censusFontDirs is where each OS keeps fonts. The user's own AsyncAO fonts
// folder comes first so a font someone dropped in there outranks a system one,
// which is the whole point of that folder.
func censusFontDirs() []string {
	var dirs []string
	if d := config.UserFontsDir(); d != "" {
		dirs = append(dirs, d)
	}
	switch runtime.GOOS {
	case "windows":
		if win := os.Getenv("WINDIR"); win != "" {
			dirs = append(dirs, filepath.Join(win, "Fonts"))
		}
		// Per-user installs (Windows 10+ "Install for me only") never appear in
		// %WINDIR%\Fonts, and are exactly where a user puts a script font.
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			dirs = append(dirs, filepath.Join(local, "Microsoft", "Windows", "Fonts"))
		}
	case "darwin":
		dirs = append(dirs,
			"/System/Library/Fonts",
			"/System/Library/Fonts/Supplemental",
			"/Library/Fonts",
		)
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, "Library", "Fonts"))
		}
	default: // linux and the other unixes
		dirs = append(dirs,
			"/usr/share/fonts",
			"/usr/local/share/fonts",
			"/run/host/fonts", // flatpak's view of the host's fonts
		)
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs,
				filepath.Join(home, ".local", "share", "fonts"),
				filepath.Join(home, ".fonts"),
			)
		}
	}
	return dirs
}

// isFontFile keeps the walk to things sfnt can actually parse.
func isFontFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf", ".otf", ".ttc", ".otc":
		return true
	}
	return false
}

// index walks the font directories once and parses each file's cmap. Bounded by
// fontCensusMaxFiles across ALL directories, so the budget cannot be spent twice.
func (fc *fontCensus) index() {
	if fc.indexed {
		return
	}
	fc.indexed = true
	fc.answered = make(map[rune]string, 64)
	budget := fontCensusMaxFiles
	for _, dir := range censusFontDirs() {
		if budget <= 0 {
			break
		}
		fc.walk(dir, fontCensusMaxDepth, &budget)
	}
	// Regular faces first, then smallest first.
	//
	// Size is the primary signal because a rune covered by both a 0.4 MB Leelawadee
	// and a 35 MB PMingLiU should resolve to Leelawadee: it opens faster, costs less
	// in every per-size set, and a font that small is almost always the one actually
	// designed for that script rather than a pan-Unicode catch-all that happens to
	// include it. But bold and italic cuts are usually SMALLER than their regular
	// sibling, so size alone picked "Leelawadee UI Bold" for Thai — correct glyphs,
	// wrong weight, next to text drawn in a regular face. Weight outranks size.
	sort.SliceStable(fc.files, func(i, j int) bool {
		bi, bj := isStyledCut(fc.files[i].path), isStyledCut(fc.files[j].path)
		if bi != bj {
			return !bi
		}
		return fileSize(fc.files[i].path) < fileSize(fc.files[j].path)
	})
}

// isStyledCut guesses from the file name whether a face is a bold/italic/light cut
// rather than the family's regular one. A guess is the right tool here: it only
// orders candidates that all cover the rune, so being wrong costs a weight, never
// a glyph. Matching is on the stem so a family whose NAME contains one of these
// words is not demoted for it.
func isStyledCut(path string) bool {
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	for _, s := range []string{"bold", "italic", "oblique", "light", "thin", "black", "semib", "medium", "condensed"} {
		if strings.Contains(stem, s) {
			return true
		}
	}
	// Windows' terse convention: a style letter tacked onto the family stem —
	// segoeuib (bold), segoeuii (italic), segoeuiz (bold italic), LeelaUIb,
	// LeelUIsl (semilight), mmrtextb.
	//
	// A BARE trailing "i" is deliberately not in this list: plenty of regular faces
	// legitimately end in one (segoeui, LeelaUI, gadugi), and demoting those ranked
	// every family's bold cut above its regular. Windows spells italic as a DOUBLED
	// i on a stem that already ends in one, which is what "ii" catches.
	for _, suf := range []string{"bi", "bd", "sl", "z", "ii"} {
		if strings.HasSuffix(stem, suf) {
			return true
		}
	}
	return len(stem) > 2 && strings.HasSuffix(stem, "b")
}

func fileSize(p string) int64 {
	if st, err := os.Stat(p); err == nil {
		return st.Size()
	}
	return 1 << 62 // unreadable sorts last
}

// walk indexes one directory tree, decrementing the shared file budget.
func (fc *fontCensus) walk(dir string, depth int, budget *int) {
	if depth < 0 || *budget <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if *budget <= 0 {
			return
		}
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			fc.walk(p, depth-1, budget)
			continue
		}
		if !isFontFile(e.Name()) {
			continue
		}
		*budget--
		fc.add(p)
	}
}

// add parses one font file's cmap, keeping the file open so the face can answer
// coverage lazily instead of holding the whole font in memory. Collections
// contribute their first face, matching what memFont opens at draw time
// (ttf.OpenFontRW hardwires face index 0) — indexing a face we could not then
// render would hand back a path that still draws tofu.
func (fc *fontCensus) add(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	if face, err := sfnt.ParseReaderAt(f); err == nil {
		fc.files = append(fc.files, fontCensusFile{path: path, face: face, file: f})
		return
	}
	if col, err := sfnt.ParseCollectionReaderAt(f); err == nil && col.NumFonts() > 0 {
		if face, err := col.Font(0); err == nil {
			fc.files = append(fc.files, fontCensusFile{path: path, face: face, file: f})
			return
		}
	}
	_ = f.Close()
}

// find returns the path of an installed font covering r, or "" when nothing on
// this machine does. Memoized both ways: a negative answer is as worth keeping as
// a positive one, since it is what stops a page of unknown runes from re-walking
// every font on every frame.
func (fc *fontCensus) find(r rune) string {
	fc.index()
	if p, ok := fc.answered[r]; ok {
		return p
	}
	var buf sfnt.Buffer
	hit := ""
	for i := range fc.files {
		idx, err := fc.files[i].face.GlyphIndex(&buf, r)
		if err == nil && idx != 0 {
			hit = fc.files[i].path
			break
		}
	}
	if len(fc.answered) >= censusMaxAnswered {
		fc.answered = make(map[rune]string, 64) // wholesale reset, same rule as the width memos
	}
	fc.answered[r] = hit
	return hit
}

// close releases the indexed file handles.
func (fc *fontCensus) close() {
	for i := range fc.files {
		if fc.files[i].file != nil {
			_ = fc.files[i].file.Close()
		}
	}
	fc.files = nil
}

// --- App wiring: render thread ⇄ census goroutine ----------------------------
//
// The render thread only ever sends RUNES and receives font BYTES. Both channels
// are buffered and every send is non-blocking, so a busy or dead census can never
// stall a frame — a dropped request simply comes back next frame, because the
// rune is still uncovered and coverRunes will queue it again.

// startCensus launches the census goroutine on first need. It serves requests
// forever (one per uncovered rune) and answers with the font's bytes. Idempotent.
func (a *App) startCensus() {
	if a.censusStarted {
		return
	}
	a.censusStarted = true
	a.censusReq = make(chan rune, censusQueueCap)
	a.censusRes = make(chan []byte, 2)
	req, res := a.censusReq, a.censusRes
	go func() {
		fc := &fontCensus{}
		defer fc.close()
		sent := make(map[string]bool, censusFaceCap) // never hand back the same file twice
		delivered := 0
		for r := range req {
			path := fc.find(r)
			if path == "" || sent[path] {
				continue // nothing installed has it, or the chain already has this face
			}
			data, err := os.ReadFile(path)
			if err != nil || len(data) == 0 || len(data) > fontFileMaxBytes {
				continue
			}
			select {
			case res <- data:
				sent[path] = true
				delivered++
			default:
				continue // the render thread hasn't drained yet; it will re-ask
			}
			if delivered >= censusFaceCap {
				// The chain is full, so no further answer could be installed. Release the
				// indexed file handles now rather than at process exit: holding a hundred
				// or so system font files open for a whole session serves nothing, and on
				// Windows an open handle is enough to stop the user replacing a font.
				// The render side stops asking at the same cap, so this cannot strand a
				// pending request.
				fc.close()
				return
			}
		}
	}()
}

// drainMissingRunes hands the frame's uncovered runes to the census. Starting the
// goroutine is deferred to the first miss, so a session that never draws an
// uncoverable rune — nearly all of them — never scans the font directories at all.
func (a *App) drainMissingRunes() {
	a.missingBuf = a.ctx.TakeMissingRunes(a.missingBuf)
	if len(a.missingBuf) == 0 {
		return
	}
	if len(a.ctx.censusData) >= censusFaceCap {
		return // the chain is already at its cap; more faces would be evicted anyway
	}
	a.startCensus()
	for _, r := range a.missingBuf {
		select {
		case a.censusReq <- r:
		default: // queue full — the rune is still uncovered next frame
		}
	}
}

// pollCensusFont installs a face the census found, on the render thread, and
// forces a re-raster so the text that was drawing tofu repaints with real glyphs.
func (a *App) pollCensusFont() {
	if !a.censusStarted {
		return
	}
	select {
	case data := <-a.censusRes:
		a.ctx.AddCensusFace(data)
		a.rasterText = "" // re-raster the visible message with the wider coverage
	default:
	}
}
