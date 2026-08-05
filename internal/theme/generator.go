package theme

// Procedural generators — the SPEC half (v1.90.0 W4).
// docs/wip/THEME-EDITOR-DESIGN.md §6.3, docs/THEME-FORMAT.md §5.
//
// A `kind = gen` element names a generator and hands it an ordered `k = v` list.
// This file is everything about that which is SDL-free, serialisable and hashable:
// the name, the params, and the content address the rasterised tile is cached
// under. The rasteriser itself lives in internal/ui (gentex.go) because it produces
// pixels; hard rule 1 keeps the two apart, and the seam is exactly this struct.
//
// WHY A CONTENT ADDRESS AT ALL. A generator produces a TILE, never a field
// (GenTileMaxPx), and the tile's key carries no width, no height and no theme —
// only the hash of (name, params). Three consequences, and they are the whole
// reason generators are affordable:
//
//   - A window resize never re-rasters and never re-pins anything: the tile is the
//     same tile at every canvas size, and the ELEMENT stretches or repeats it.
//     (TestGeneratorKeyIsResizeInvariant.)
//   - Two elements with identical params share one texture, across elements and
//     across themes. A 14-element scanline set costs one tile.
//   - The cache key cannot collide by construction, so `theme://g/<hash>` needs no
//     theme namespace where `theme://m/<themeid>/<id>` does.
//
// That last point is why the hash has to be a real hash of the real bytes, and why
// the generator contract's first clause is DETERMINISM (gentex.go): a generator
// that returned different pixels for the same params would have the cache serve
// whichever one happened to be rastered first, forever.

import (
	"strings"

	"github.com/cespare/xxhash/v2"
)

const (
	// GenTileMaxPx bounds a generator tile's long edge. THE affordability decision
	// (design §Q8): a full-screen 1512x648 scanline FIELD is 3.7 MiB of ARGB and has
	// to be re-rastered on every resize; the same look as a 256x256 TILE is 256 KiB
	// and survives every resize untouched. 256 is also the point past which a
	// repeating pattern stops reading as a pattern and starts reading as art.
	GenTileMaxPx = 256
	// GenNameRuneCap bounds a generator's wire name. Generator names are ids in the
	// same sense element ids are — they appear in a section value, they are matched
	// against a fixed registry, and an over-long one is not a name this build has
	// ever heard of. Shares IDRuneCap's number for the reason the two share a
	// grammar; written as a const alias, not a second 24.
	GenNameRuneCap = IDRuneCap
)

// GeneratorSpec is one `generator =` + `gen_params =` pair, normalised.
//
// The param list is ORDER-SENSITIVE and the hash is order-sensitive with it. That
// is deliberate and it is the cheap direction of the trade: two elements that write
// the same params in a different order rasterise the same tile twice (one wasted
// entry against GenCap, no visual difference), whereas an order-INsensitive hash
// would need a sort on a path that must not allocate and would make the key depend
// on a canonicalisation this format has never published. The writer emits params in
// the author's own order, so a file that round-trips keeps its own key.
type GeneratorSpec struct {
	// Name is the wire name, lowercased and trimmed. WIRE NAMES ARE THE FORMAT:
	// append-only, never renamed, never repurposed (§7.4). An unknown name is not an
	// error — the element degrades to a flat fill and still draws.
	Name string
	// Params are the ordered key/value pairs, packed from index 0.
	Params [GenParamCap]KV
	// NP is how many of Params are populated.
	NP int
}

// GeneratorSpecOf builds the spec for an element. It is the ONE place an Element's
// generator fields become a spec, so the hash can never be computed from a
// differently-normalised view of the same element.
//
// Cold path (the theme-apply goroutine): it lowercases and trims, which allocates.
func GeneratorSpecOf(e *Element) GeneratorSpec {
	if e == nil {
		return GeneratorSpec{}
	}
	g := GeneratorSpec{Name: NormalizeGenName(e.Gen)}
	for i := 0; i < e.GenParamCount() && i < len(g.Params); i++ {
		kv := e.GenParams[i]
		g.Params[g.NP] = KV{
			Key:   strings.ToLower(strings.TrimSpace(kv.Key)),
			Value: strings.TrimSpace(kv.Value),
		}
		g.NP++
	}
	return g
}

// NormalizeGenName folds a generator name to the spelling the registry is keyed
// by. Case-insensitive because `generator` is read as free text rather than as an
// enum (sidecar_read.go), exactly like `shape`: a theme writing "Scanlines" means
// scanlines, and degrading it to a flat fill would be a silent downgrade of a file
// that says what it means.
func NormalizeGenName(s string) string {
	n := strings.ToLower(strings.TrimSpace(s))
	if len([]rune(n)) > GenNameRuneCap {
		return "" // not a name this build could ever hold; degrades to a flat fill
	}
	return n
}

// Empty reports a spec that names no generator.
func (g GeneratorSpec) Empty() bool { return g.Name == "" }

// genHashSep separates the hashed fields. A byte no legal name, key or value can
// contain (the id grammar is [a-z0-9_-], and INIDoc refuses control characters), so
// "ab"+"c" and "a"+"bc" cannot hash alike.
const genHashSep = "\x00"

// Hash is the content address: xxhash64 over the name and the ordered params.
//
// xxhash64 is what the disk cache already keys full URLs by (cache/disk.go), so
// this introduces no dependency and no second hashing discipline. Collisions are
// the usual 64-bit story and the consequence is bounded: two generators that
// collided would share one tile, which is a wrong-looking element, not a crash or a
// leak — and the alternative (a longer digest in a cache key) costs bytes on every
// key for a risk nobody will meet.
//
// Cold path: it walks strings. The KEY is computed once per element per theme apply
// and then lives in the baked row as an already-built string.
func (g GeneratorSpec) Hash() uint64 {
	var d xxhash.Digest
	d.Reset()
	_, _ = d.WriteString(g.Name)
	for i := 0; i < g.NP && i < len(g.Params); i++ {
		_, _ = d.WriteString(genHashSep)
		_, _ = d.WriteString(g.Params[i].Key)
		_, _ = d.WriteString(genHashSep)
		_, _ = d.WriteString(g.Params[i].Value)
	}
	return d.Sum64()
}

// ParseRGBA is the format's colour grammar, exported for the ONE caller outside
// this package that needs it: internal/ui's generator param parser (gentex.go).
//
// A generator parameter and an element's `fill` must mean the same thing by the
// same code. The alternative — a second parser in internal/ui — is precisely the
// divergence that let upper-case hex silently drop four of themes/thh_trial's five
// [palette] roles while `#101010` beside them worked, and a generator whose `tint`
// disagreed with the element's `fill` about `#1C1C1C` would be the same bug with a
// second place to fix it.
//
// Accepts `#rrggbb`, `#rrggbbaa` (either hex case) and `r, g, b[, a]`.
func ParseRGBA(v string) (RGBA, bool) { return parseRGBAValue(v) }

// Param returns the value written for key, and whether it was written at all.
// Linear over at most GenParamCap entries; cold path only.
func (g GeneratorSpec) Param(key string) (string, bool) {
	for i := 0; i < g.NP && i < len(g.Params); i++ {
		if g.Params[i].Key == key {
			return g.Params[i].Value, true
		}
	}
	return "", false
}
