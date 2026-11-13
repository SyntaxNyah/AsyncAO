package theme

// Procedural generator specs (docs/THEME-FORMAT.md section 5).
// Generators must be deterministic, or the cache serves whichever raster came first.

import (
	"strings"

	"github.com/cespare/xxhash/v2"
)

const (
	// GenTileMaxPx bounds a tile's long edge: a tile survives every resize, a full-screen field re-rasters.
	GenTileMaxPx = 256
	// GenNameRuneCap aliases IDRuneCap, as a generator name is matched against a fixed registry like an id.
	GenNameRuneCap = IDRuneCap
)

// GeneratorSpec is one normalised `generator =` + `gen_params =` pair.
// Order-sensitive, since sorting would allocate on a no-allocation path.
type GeneratorSpec struct {
	// Name is the lowercased, trimmed wire name, append-only; an unknown name degrades the element to a flat fill.
	Name string
	// Params are the ordered key/value pairs, packed from index 0.
	Params [GenParamCap]KV
	// NP is how many of Params are populated.
	NP int
}

// GeneratorSpecOf is the one place an Element becomes a spec; cold path, as it allocates.
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

// ParseGenParams publishes the `gen_params` grammar for the editor's parameter row; cold path.
func ParseGenParams(raw string) ([GenParamCap]KV, error) { return genParamsOf(raw) }

// WriteGenParams renders an element's params in the file's spelling; a nil element gives "".
func WriteGenParams(e *Element) string {
	if e == nil {
		return ""
	}
	return formatGenParams(e)
}

// NormalizeGenName folds a name to the registry's spelling, so "Scanlines" keeps its generator.
func NormalizeGenName(s string) string {
	n := strings.ToLower(strings.TrimSpace(s))
	if len([]rune(n)) > GenNameRuneCap {
		return "" // not a name this build could ever hold; degrades to a flat fill
	}
	return n
}

func (g GeneratorSpec) Empty() bool { return g.Name == "" }

// genHashSep is a byte no legal name, key or value contains, so "ab"+"c" and "a"+"bc" differ.
const genHashSep = "\x00"

// Hash is xxhash64 over the name and ordered params; cold path, computed once per element per theme apply.
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

// ParseRGBA is the format's colour grammar, so internal/ui's generator params and an element's `fill` share one parser.
func ParseRGBA(v string) (RGBA, bool) { return parseRGBAValue(v) }

// Param returns the value written for key and whether it was written; linear, cold path only.
func (g GeneratorSpec) Param(key string) (string, bool) {
	for i := 0; i < g.NP && i < len(g.Params); i++ {
		if g.Params[i].Key == key {
			return g.Params[i].Value, true
		}
	}
	return "", false
}
