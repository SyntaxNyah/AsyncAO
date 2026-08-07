package ui

// THE INSPECTOR'S LIVE LISTS (v1.90.0 W7b).
//
// ONE concern: what an fkPick row may offer, for THIS theme, on THIS build.
//
// WHY THEY ARE NOT `enum` ON THE ROW. inspectorField.enum is the FORMAT's own name
// table — appended-only, never renamed, handed over by internal/theme so the editor
// cannot hold a second copy of it. These four lists are a different kind of thing
// entirely: they are facts about the CURRENT document (which widgets this theme
// places, which families it declares) and about the CURRENT build (which generators
// rasterise, which silhouettes draw). They change while the editor is open, they are
// not the format, and a value outside them is legal — so they are offered, never
// enforced.
//
// CACHED, BECAUSE THE RAIL DRAWS EVERY FRAME. Building a sorted key list per frame is
// tens of allocations on a surface gated at editorChromeAllocBudget, so each dynamic
// list is rebuilt only when its own cheap validity key moves (the number of placed
// keys, the number of declared families). The two STATIC lists are built once at
// package init and shared — a build's generator registry and shape vocabulary cannot
// change at runtime.
//
// SORTED, ALWAYS. Both dynamic sources are maps or author-ordered slices; an unsorted
// picker would offer the same theme's anchors in a different order on different runs,
// which reads as the control shuffling itself.

import (
	"sort"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// pickAnchorNone is the first entry of the anchor picker: no anchor at all, i.e. the
// element's rect is absolute in its own Space. Spelled out rather than left as a bare
// empty row, because "(none)" is a choice an author makes and an empty line is not.
const pickAnchorNone = "(none)"

// pickListCap bounds ONE picker's offered list (hard rule 4). The anchor list is the
// widest — every placed design key plus every [pane.*] id — and themeSlots is a fixed
// registry of well under a hundred rows, so this is a ceiling rather than a limit
// anybody meets. Past it the list simply stops growing; a name that is not offered is
// still typeable, which is the whole reason fkPick keeps its text field.
const pickListCap = 128

// pickStatic holds the two lists that are properties of the BUILD. Package-level and
// built once: genRegistry and elemShapeNames are compile-time facts.
var pickStatic = [pickSourceCount][]string{
	pickGenerator: sortedStrings(GeneratorNames()),
	pickShape:     sortedStrings(elemShapeNames[:]),
}

// sortedStrings copies and sorts. A copy, not a sort in place: elemShapeNames is
// indexed BY ID everywhere else in this package (elemShapeSharp is 0), so sorting the
// real array would silently re-map every shape a theme names.
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// pickCache is the editor's cache of the two DYNAMIC lists.
//
// A validity KEY rather than an invalidation call, and that is deliberate: an explicit
// invalidate is a call somebody has to remember at every site that changes the
// document, and W7a already has three of those (an op, a reclaim, a baseline restore).
// A key that is a cheap function of the thing being cached cannot be forgotten — it is
// re-read every frame, costs two len() calls, and is wrong only if a change leaves the
// count identical, which for both of these lists means the picker offers the same
// number of differently-named entries: recoverable (the value is still typeable) and
// self-healing on the next add or remove.
type pickCache struct {
	names [pickSourceCount][]string
	key   [pickSourceCount]int
	built [pickSourceCount]bool
}

// editorPickNames returns the live list for one source, or nil.
func (a *App) editorPickNames(src pickSource) []string {
	if int(src) >= int(pickSourceCount) {
		return nil
	}
	if s := pickStatic[src]; s != nil {
		return s
	}
	if a.te == nil || a.te.doc == nil {
		return nil
	}
	key := a.editorPickKey(src)
	c := &a.te.picks
	if c.built[src] && c.key[src] == key {
		return c.names[src]
	}
	c.names[src] = a.buildPickNames(src, c.names[src][:0])
	c.key[src], c.built[src] = key, true
	return c.names[src]
}

// editorPickKey is the cheap validity key for a dynamic list. See pickCache.
func (a *App) editorPickKey(src pickSource) int {
	d := a.te.doc
	switch src {
	case pickAnchor:
		return len(d.live)*2 + len(d.side.Panes)
	case pickFace:
		return len(d.side.Fonts)
	}
	return 0
}

// buildPickNames fills out one dynamic list, reusing the cached backing array so a
// rebuild costs at most one grow rather than a fresh allocation every time.
func (a *App) buildPickNames(src pickSource, out []string) []string {
	d := a.te.doc
	switch src {
	case pickAnchor:
		// EVERY key this build actually PLACED, not every key the registry knows: an
		// anchor that resolves to nothing is an element parked at the origin with no
		// explanation, and the difference between the two sets is exactly the widgets
		// this theme chose not to declare.
		out = append(out, pickAnchorNone)
		for key := range d.live {
			if len(out) >= pickListCap {
				break
			}
			if editorSlotKeyEditable(key) {
				out = append(out, key)
			}
		}
		// The theme's own panes, which theme.Element.Anchor accepts alongside a slot key
		// when Space == SpacePane.
		for i := range d.side.Panes {
			if len(out) >= pickListCap {
				break
			}
			if id := d.side.Panes[i].ID; id != "" {
				out = append(out, id)
			}
		}
		sortTailOfPickList(out)
	case pickFace:
		// THE LADDER, IN OWNER ORDER: the AO2 class ids this client honours first (a
		// `font` that names one means "inherit that element's face" — theme.Element's
		// own comment), then the families this theme declares in [fonts].
		out = append(out, pickAnchorNone)
		for _, id := range theme.FontElements {
			if len(out) >= pickListCap {
				break
			}
			out = append(out, id)
		}
		for i := range d.side.Fonts {
			if len(out) >= pickListCap {
				break
			}
			if f := d.side.Fonts[i].Family; f != "" {
				out = append(out, f)
			}
		}
		// NOT sorted: the class ladder's order IS theme.FontElements' order, which is
		// append-only and meaningful (TestFontElementsIsAppendOnly), and the families
		// follow in the author's own declaration order — the order they read in the file.
	}
	return out
}

// sortTailOfPickList sorts everything after the leading "(none)" entry, so the escape
// hatch stays first however the keys sort.
func sortTailOfPickList(out []string) {
	if len(out) > 1 {
		sort.Strings(out[1:])
	}
}

// pickIndexOf is where cur sits in the offered list, or -1. "" matches the leading
// "(none)" entry, which is what makes the cycler step OFF an empty value.
func pickIndexOf(names []string, cur string) int {
	if cur == "" {
		cur = pickAnchorNone
	}
	for i := range names {
		if names[i] == cur {
			return i
		}
	}
	return -1
}

// pickValueAt is the value a list entry writes: "" for the "(none)" row, the name
// itself otherwise. The inverse of pickIndexOf's mapping, and beside it for the same
// reason rgbaValue/valueRGBA are beside each other.
func pickValueAt(names []string, i int) string {
	if i < 0 || i >= len(names) || names[i] == pickAnchorNone {
		return ""
	}
	return names[i]
}
