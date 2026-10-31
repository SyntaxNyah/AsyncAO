package ui

// System font scan: for runes no curated tier (fontfallback.go) covers, it asks which installed font has them.
// All of it runs off the render thread: each GlyphIndex call is a disk read (hard rule 2).

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Named caps (hard rule 4).
const (
	// fontCensusMaxDepth and fontCensusMaxFiles bound the walk; Linux font trees nest by foundry and family.
	fontCensusMaxDepth = 3
	fontCensusMaxFiles = 512

	// censusFaceCap bounds faces added to the live chain per session; buildSet opens each in every per-size font set.
	censusFaceCap = 6

	// censusQueueCap bounds runes awaiting an answer in a fixed array; past it recording stops until the queue drains.
	censusQueueCap = 32

	// censusMaxAnswered bounds the memo of runes already resolved or proven unresolvable.
	censusMaxAnswered = 4096
)

// fontCensusFile is one indexed font file; its face is ReaderAt-backed, holding a file handle rather than the bytes.
type fontCensusFile struct {
	path string
	face *sfnt.Font
	file *os.File
}

// fontCensus answers which installed font file has a rune; owned by its goroutine, so it needs no mutex.
type fontCensus struct {
	files    []fontCensusFile
	answered map[rune]string // rune → path ("" = nothing on this machine has it)
	indexed  bool
}

// censusFontDirs is where each OS keeps fonts; the user's AsyncAO fonts folder comes first so it outranks system ones.
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
		// Per-user installs never appear in %WINDIR%\Fonts.
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

// index walks the font directories once, with fontCensusMaxFiles shared across all of them.
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
	// Regular faces first, then smallest: a small font is usually the one designed for that script.
	// Bold and italic cuts are usually smaller than their regular sibling, so weight outranks size.
	sort.SliceStable(fc.files, func(i, j int) bool {
		bi, bj := isStyledCut(fc.files[i].path), isStyledCut(fc.files[j].path)
		if bi != bj {
			return !bi
		}
		return fileSize(fc.files[i].path) < fileSize(fc.files[j].path)
	})
}

// isStyledCut guesses from the file name stem whether a face is a bold, italic or light cut.
// Being wrong costs only a weight, never a glyph, since it only orders fonts that all cover the rune.
func isStyledCut(path string) bool {
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	for _, s := range []string{"bold", "italic", "oblique", "light", "thin", "black", "semib", "medium", "condensed"} {
		if strings.Contains(stem, s) {
			return true
		}
	}
	// Windows appends a style letter to the stem: segoeuib (bold), segoeuii (italic), segoeuiz (bold italic), LeelUIsl.
	// A bare trailing i is not a style (segoeui, LeelaUI and gadugi are regular); Windows italic doubles it, hence ii.
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

// add parses one font file's cmap, keeping the file open so coverage is answered lazily.
// Collections contribute their first face only: ttf.OpenFontRW hardwires face index 0, so others would draw tofu.
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

// find returns the path of an installed font covering r, or "" if none; negative answers are memoized too.
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

// The render thread sends runes and receives font bytes over buffered channels with non-blocking sends.
// A dropped request comes back next frame, because the rune is still uncovered.

// startCensus launches the scan goroutine on first need; it answers each uncovered rune with a font's bytes.
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
				PushWake() // pollCensusFont installs it on the next frame, even on an idle screen
			default:
				continue // the render thread hasn't drained yet; it will re-ask
			}
			if delivered >= censusFaceCap {
				// The chain is full: release the file handles, since on Windows an open handle blocks replacing a font.
				// The render side stops asking at the same cap, so no request is stranded.
				fc.close()
				return
			}
		}
	}()
}

// drainMissingRunes hands the frame's uncovered runes to the scan, whose goroutine starts on the first miss.
func (a *App) drainMissingRunes() {
	a.missingBuf = a.ctx.TakeMissingRunes(a.missingBuf)
	if len(a.missingBuf) == 0 {
		return
	}
	// The scan reads whatever fonts the machine has and changes the live chain,
	// so it is the first thing to rule out when text misbehaves on one platform; hence this off switch.
	if !a.d.Prefs.Load().FontCensus {
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

// pollCensusFont installs a found face on the render thread and forces a re-raster so tofu repaints.
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
