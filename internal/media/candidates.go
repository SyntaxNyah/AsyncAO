package media

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// ManifestFile is the format list a host publishes at its asset origin.
const ManifestFile = "extensions.json"

// manifestExtCap bounds each list, so a hostile manifest can't grow the search.
const manifestExtCap = 16

// defaultImageOrder is the order for a host with no list of its own; formats that have worked there go ahead of it.
var defaultImageOrder = []string{config.ExtWebP, config.ExtPNG, config.ExtAPNG, config.ExtGIF, config.ExtAVIF, config.ExtJPG}

// defaultAudioOrder is the built-in order for sounds.
var defaultAudioOrder = []string{config.ExtOpus, config.ExtOgg, config.ExtMP3, config.ExtWAV}

// originHints is what is known about one origin's formats.
type originHints struct {
	allowed map[Class][]string
	learned map[Class]map[string]int
}

// CandidateBuilder turns a key into exact files; it is used only on the Manager's goroutine.
type CandidateBuilder struct {
	origins map[string]*originHints
}

// NewCandidateBuilder knows nothing about any origin yet.
func NewCandidateBuilder() *CandidateBuilder {
	return &CandidateBuilder{origins: map[string]*originHints{}}
}

func (b *CandidateBuilder) hints(origin string) *originHints {
	h := b.origins[origin]
	if h == nil {
		h = &originHints{allowed: map[Class][]string{}, learned: map[Class]map[string]int{}}
		b.origins[origin] = h
	}
	return h
}

// Build lists the files a key may be: each spelling in turn, each with the formats in best-first order.
func (b *CandidateBuilder) Build(key Key) []File {
	if key.Class == ClassExact {
		return []File{File(key.Origin + key.Path)}
	}
	order := b.order(key.Origin, key.Class)
	files := make([]File, 0, len(order)*(maxAlts+1))
	add := func(path string) {
		for _, ext := range order {
			files = append(files, File(key.Origin+path+ext))
		}
	}
	add(key.Path)
	for _, alt := range key.Alts {
		if alt != "" {
			add(alt)
		}
	}
	return files
}

// order is the host's own list when it published one, which it expects to be used as written.
// Without one, what has worked goes first, then the built-in order.
func (b *CandidateBuilder) order(origin string, class Class) []string {
	defaults := defaultImageOrder
	if class == ClassSFX || class == ClassBlips {
		defaults = defaultAudioOrder
	}
	h := b.origins[origin]
	if h == nil {
		return defaults
	}
	if allowed := h.allowed[class]; len(allowed) > 0 {
		return allowed
	}
	order := slices.Clone(defaults)
	counts := h.learned[class]
	slices.SortStableFunc(order, func(x, y string) int { return counts[y] - counts[x] })
	return order
}

// Found learns a key's format from the file it turned out to be; it matches Config.Found.
func (b *CandidateBuilder) Found(key Key, f File) {
	if key.Class == ClassExact {
		return
	}
	if dot := strings.LastIndexByte(string(f), '.'); dot >= 0 {
		b.Learn(key.Origin, key.Class, string(f[dot:]))
	}
}

// Learn records that a format worked for a class on an origin.
func (b *CandidateBuilder) Learn(origin string, class Class, ext string) {
	h := b.hints(origin)
	if h.learned[class] == nil {
		h.learned[class] = map[string]int{}
	}
	h.learned[class][ext]++
}

// Allow sets the formats and their order from a host's extensions.json.
func (b *CandidateBuilder) Allow(origin string, allowed map[Class][]string) {
	h := b.hints(origin)
	for class, exts := range allowed {
		h.allowed[class] = exts
	}
}

// ParseManifest reads an extensions.json into ordered format lists per class, ignoring anything unknown.
func ParseManifest(data []byte) (map[Class][]string, error) {
	var raw struct {
		CharIcon   []string `json:"charicon_extensions"`
		Emote      []string `json:"emote_extensions"`
		Emotions   []string `json:"emotions_extensions"`
		Background []string `json:"background_extensions"`
		SFX        []string `json:"sfx_extensions"`
		Blips      []string `json:"blips_extensions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("media: parsing %s: %w", ManifestFile, err)
	}
	out := map[Class][]string{}
	for class, list := range map[Class][]string{ClassCharIcon: raw.CharIcon, ClassEmote: raw.Emote, ClassEmotions: raw.Emotions,
		ClassBackground: raw.Background, ClassSFX: raw.SFX, ClassBlips: raw.Blips} {
		if clean := cleanExts(list); len(clean) > 0 {
			out[class] = clean
		}
	}
	return out, nil
}

func cleanExts(in []string) []string {
	var out []string
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		// .webp.static and .webp.animated are webAO's pseudo-suffixes for the one .webp file.
		if strings.HasPrefix(e, config.ExtWebP+".") {
			e = config.ExtWebP
		}
		known := slices.Contains(defaultImageOrder, e) || slices.Contains(defaultAudioOrder, e)
		if known && !slices.Contains(out, e) && len(out) < manifestExtCap {
			out = append(out, e)
		}
	}
	return out
}

// Join fetches a server's extensions.json on the Network pool. Any answer, even a 404, proves the host is alive.
func Join(m *Manager, pool *Pool, remote *RemoteSource, fetch Fetcher, builder *CandidateBuilder, origin string, done func(found bool)) {
	type answer struct {
		data []byte
		err  error
	}
	file := File(origin + ManifestFile)
	m.Run(pool, Now, func() any {
		data, err := fetch.Fetch(remote.ctx, string(file))
		return answer{data, err}
	}, func(v any) {
		a := v.(answer)
		if a.err == nil || isNotFound(a.err) || isStatus(a.err) {
			remote.Confirm(hostOf(file))
		}
		found := false
		if a.err == nil {
			if allowed, err := ParseManifest(a.data); err == nil {
				builder.Allow(origin, allowed)
				found = true
			}
		}
		if done != nil {
			done(found)
		}
	})
}

func isNotFound(err error) bool { return errors.Is(err, network.ErrAssetNotFound) }

func isStatus(err error) bool { return errors.Is(err, network.ErrUnexpectedStatus) }
