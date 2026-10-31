package ui

// The inspector's live lists: what an fkPick row may offer for this theme and build.
//
// They are not the row's enum (the format's own append-only name table): they are facts about the current document
// (placed widgets, declared families) and build (generators, silhouettes), and a value outside them is legal, so
// they are offered, never enforced.
//
// The rail draws every frame, so each dynamic list is rebuilt only when its cheap validity key moves; the two
// static lists are built once at init. All lists are sorted so the control does not reshuffle between runs.

import (
	"sort"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// pickAnchorNone is the first anchor entry: no anchor, so the rect is absolute in its Space. "(none)" is a choice.
const pickAnchorNone = "(none)"

// pickListCap bounds one picker's offered list (hard rule 4). The anchor list is widest and themeSlots is well under
// a hundred rows, so this is a ceiling; a name not offered is still typeable.
const pickListCap = 128

// pickStatic holds the two lists that are properties of the build, built once.
var pickStatic = [pickSourceCount][]string{
	pickGenerator: sortedStrings(GeneratorNames()),
	pickShape:     sortedStrings(elemShapeNames[:]),
}

// sortedStrings copies and sorts; elemShapeNames is indexed by id elsewhere, so an in-place sort would re-map shapes.
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// pickCache is the editor's cache of the two dynamic lists.
//
// It uses a validity key rather than an invalidate call, which someone must remember at every site that changes the
// document. The key (two len() calls) is re-read every frame; it is wrong only if a change keeps the counts equal,
// which is recoverable (the value is still typeable) and heals on the next add or remove.
type pickCache struct {
	names [pickSourceCount][]string
	key   [pickSourceCount]int
	built [pickSourceCount]bool
}

// editorPickNames returns the live list for one source, or nil.
//
// pickNone, the zero value every non-picker row carries, is answered here rather than at the draw site so it does
// not take a cache slot to be told there is no list.
func (te *themeEditor) editorPickNames(src pickSource) []string {
	if src == pickNone || int(src) >= int(pickSourceCount) {
		return nil
	}
	if s := pickStatic[src]; s != nil {
		return s
	}
	if te == nil || te.doc == nil {
		return nil
	}
	key := te.editorPickKey(src)
	c := &te.picks
	if c.built[src] && c.key[src] == key {
		return c.names[src]
	}
	c.names[src] = te.buildPickNames(src, c.names[src][:0])
	c.key[src], c.built[src] = key, true
	return c.names[src]
}

// editorPickKey is the cheap validity key for a dynamic list. See pickCache.
func (te *themeEditor) editorPickKey(src pickSource) int {
	d := te.doc
	switch src {
	case pickAnchor:
		return len(d.live)*2 + len(d.side.Panes)
	case pickFace:
		return len(d.side.Fonts)
	}
	return 0
}

// buildPickNames fills out one dynamic list, reusing the cached backing array so a rebuild costs at most one grow.
func (te *themeEditor) buildPickNames(src pickSource, out []string) []string {
	d := te.doc
	switch src {
	case pickAnchor:
		// Every key this build placed, not every key the registry knows: an unplaced anchor parks at the origin.
		out = append(out, pickAnchorNone)
		for key := range d.live {
			if len(out) >= pickListCap {
				break
			}
			if editorSlotKeyEditable(key) {
				out = append(out, key)
			}
		}
		// The theme's own panes, which theme.Element.Anchor accepts alongside a slot key when Space == SpacePane.
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
		// The AO2 class ids this client honours first (a font naming one means inherit that element's face), then the
		// families this theme declares in [fonts].
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
		// Not sorted: theme.FontElements' order is append-only and meaningful, and families keep the author's order.
	}
	return out
}

// sortTailOfPickList sorts everything after the leading "(none)" entry, which stays first.
func sortTailOfPickList(out []string) {
	if len(out) > 1 {
		sort.Strings(out[1:])
	}
}

// pickIndexOf is where cur sits in the offered list, or -1; "" matches "(none)" so the cycler steps off an empty value.
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

// pickValueAt is the value a list entry writes: "" for the "(none)" row, else the name; the inverse of pickIndexOf.
func pickValueAt(names []string, i int) string {
	if i < 0 || i >= len(names) || names[i] == pickAnchorNone {
		return ""
	}
	return names[i]
}
