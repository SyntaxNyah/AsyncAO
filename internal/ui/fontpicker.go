package ui

// A font-family text field that suggests the fonts a family name can resolve to. The names are the file names in
// the folders a family is looked up in (systemFontDirs: AsyncAO's fonts folder, then the OS font folders), since the
// file name is what a family matches. They are listed off-thread once, on first use.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// fontPickRows is how many suggestions show under the field.
	fontPickRows = 8
	// fontPickCap bounds the listed fonts (hard rule 4).
	fontPickCap = 4096
	// fontPickRowH is one suggestion's height.
	fontPickRowH int32 = 24
	// fontPickTextInset indents a suggestion's text.
	fontPickTextInset int32 = 8
)

// fontPicker is the suggestion state shared by every font field; one list is open at a time.
type fontPicker struct {
	names   []string
	lower   []string
	loading bool
	res     chan []string
	// open is the field whose list was shown last frame, and listR where; a click on the list lands after the field
	// already dropped focus, so the hit test uses last frame's rect.
	open  string
	listR sdl.Rect
	match [fontPickRows]int
}

// listFontNames returns the font file names in dirs, without extensions, once each (ignoring case), sorted. It walks
// the disk: goroutine only.
func listFontNames(dirs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !isFontFile(e.Name()) {
				continue
			}
			name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if key := strings.ToLower(name); !seen[key] && len(out) < fontPickCap {
				seen[key] = true
				out = append(out, name)
			}
		}
	}
	slices.SortFunc(out, func(x, y string) int { return strings.Compare(strings.ToLower(x), strings.ToLower(y)) })
	return out
}

// ensureFontNames starts the listing once and lands it when done.
func (a *App) ensureFontNames() {
	fp := &a.fontPick
	if fp.names == nil && !fp.loading {
		fp.loading = true
		fp.res = make(chan []string, 1)
		ch := fp.res
		go func() {
			ch <- listFontNames(systemFontDirs())
			PushWake()
		}()
	}
	if fp.loading {
		select {
		case names := <-fp.res:
			fp.loading = false
			fp.names = names
			fp.lower = make([]string, len(names))
			for i, n := range names {
				fp.lower[i] = strings.ToLower(n)
			}
		default:
		}
	}
}

// fontSuggestions fills fp.match with up to fontPickRows names containing q, those starting with it first, and returns
// how many.
func (fp *fontPicker) fontSuggestions(q string) int {
	q = strings.ToLower(strings.TrimSpace(q))
	n := 0
	for pass := 0; pass < 2 && n < fontPickRows; pass++ {
		for i, l := range fp.lower {
			prefix := strings.HasPrefix(l, q)
			if (pass == 0) != prefix || !strings.Contains(l, q) {
				continue
			}
			fp.match[n] = i
			n++
			if n == fontPickRows {
				break
			}
		}
	}
	return n
}

// fontFamilyField is a text field for a font family or file path with suggestions under it while it is focused. It
// returns the value and how far the suggestions pushed the content below it down.
func (a *App) fontFamilyField(id string, r sdl.Rect, value, placeholder string) (string, int32) {
	c := a.ctx
	fp := &a.fontPick
	a.ensureFontNames()
	wasOpen := fp.open == id
	pickedLast := wasOpen && c.ClickedIn(fp.listR)
	next, _ := c.TextField(id, r, value, placeholder)
	if pickedLast {
		c.focusID = id // the click on the list unfocused the field; keep the list for this frame's hit test
	}
	if c.focusID != id {
		if wasOpen {
			fp.open = ""
		}
		return next, 0
	}
	n := 0
	if !fp.loading {
		n = fp.fontSuggestions(next)
	}
	fp.open = id
	if n == 0 {
		fp.listR = sdl.Rect{}
		if fp.loading {
			c.Label(r.X, r.Y+r.H+fontPickTextInset/2, "Listing fonts…", ColTextDim)
			return next, fontPickRowH
		}
		return next, 0
	}
	list := sdl.Rect{X: r.X, Y: r.Y + r.H + 2, W: r.W, H: int32(n) * fontPickRowH}
	c.Fill(list, ColPanel)
	c.Border(list, ColAccent)
	for i := range n {
		row := sdl.Rect{X: list.X + 1, Y: list.Y + int32(i)*fontPickRowH, W: list.W - 2, H: fontPickRowH}
		name := fp.names[fp.match[i]]
		if c.hovering(row) {
			c.Fill(row, ColPanelHi)
		}
		c.LabelClipped(row.X+fontPickTextInset, row.Y+(row.H-a.fontLineH())/2, row.W-2*fontPickTextInset, name, ColText)
		if pickedLast && c.ClickedIn(row) {
			next = name
			c.focusID = ""
			fp.open = ""
		}
	}
	fp.listR = list
	return next, list.H + 2
}
