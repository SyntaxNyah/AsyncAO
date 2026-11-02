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
	// extras maps a server's origin to the user's extra hosts, asked in order after it.
	extras map[string][]string
}

// NewCandidateBuilder knows nothing about any origin yet.
func NewCandidateBuilder() *CandidateBuilder {
	return &CandidateBuilder{origins: map[string]*originHints{}, extras: map[string][]string{}}
}

// Uses reports whether any origin asks host as an extra host.
func (b *CandidateBuilder) Uses(host string) bool {
	for _, hosts := range b.extras {
		if slices.Contains(hosts, host) {
			return true
		}
	}
	return false
}

// SetHosts sets the hosts asked, in this order, after origin, and reports whether that changed anything;
// each must be an origin ending in a slash.
func (b *CandidateBuilder) SetHosts(origin string, hosts []string) bool {
	if slices.Equal(b.extras[origin], hosts) {
		return false
	}
	if len(hosts) == 0 {
		delete(b.extras, origin)
		return true
	}
	b.hints(origin)
	b.extras[origin] = slices.Clone(hosts)
	return true
}

func (b *CandidateBuilder) hints(origin string) *originHints {
	h := b.origins[origin]
	if h == nil {
		h = &originHints{allowed: map[Class][]string{}, learned: map[Class]map[string]int{}}
		b.origins[origin] = h
	}
	return h
}

// Build lists the files a key may be: each spelling in turn, each with the formats in best-first order,
// then the same on each extra host in its order.
func (b *CandidateBuilder) Build(key Key) []File {
	origin := b.originOf(key)
	paths := []string{key.Path}
	if key.Class != ClassExact {
		for _, alt := range key.Alts {
			if alt != "" {
				paths = append(paths, alt)
			}
		}
	}
	files := b.at(key.Class, origin, key.Origin, paths)
	extras := b.extras[origin]
	if len(extras) == 0 {
		return files
	}
	if key.Origin == "" {
		// A key holding whole URLs moves to another host by its path under origin.
		rel := paths[:0:0]
		for _, p := range paths {
			if r, ok := strings.CutPrefix(p, origin); ok {
				rel = append(rel, r)
			}
		}
		paths = rel
	}
	for _, host := range extras {
		files = append(files, b.at(key.Class, host, host, paths)...)
	}
	return files
}

// at lists each path under prefix with host's formats; an exact path is already one file.
func (b *CandidateBuilder) at(class Class, host, prefix string, paths []string) []File {
	if class == ClassExact {
		files := make([]File, 0, len(paths))
		for _, p := range paths {
			files = append(files, File(prefix+p))
		}
		return files
	}
	order := b.order(host, class)
	files := make([]File, 0, len(order)*len(paths))
	for _, p := range paths {
		for _, ext := range order {
			files = append(files, File(prefix+p+ext))
		}
	}
	return files
}

// originOf is the key's own origin, or for a key holding a whole URL, the longest origin known to contain it, then its host.
func (b *CandidateBuilder) originOf(key Key) string {
	if key.Origin != "" {
		return key.Origin
	}
	best := ""
	for origin := range b.origins {
		if len(origin) > len(best) && strings.HasPrefix(key.Path, origin) {
			best = origin
		}
	}
	if best != "" {
		return best
	}
	return hostOf(File(key.Path))
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

// Found learns a format for the host the file turned up on; it matches Config.Found.
func (b *CandidateBuilder) Found(key Key, f File) {
	if key.Class == ClassExact {
		return
	}
	origin := b.originOf(key)
	for _, host := range b.extras[origin] {
		if strings.HasPrefix(string(f), host) {
			origin = host
			break
		}
	}
	if dot := strings.LastIndexByte(string(f), '.'); dot >= 0 {
		b.Learn(origin, key.Class, string(f[dot:]))
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

// ManifestState is what reading a host's extensions.json found.
type ManifestState uint8

// The states a host's format list can be in.
const (
	ManifestUnchecked ManifestState = iota
	ManifestRead
	ManifestAbsent
	ManifestFailed
)

// Join fetches a host's extensions.json on the Network pool. Any answer, even a 404, proves the host is alive.
func Join(m *Manager, pool *Pool, remote *RemoteSource, fetch Fetcher, builder *CandidateBuilder, origin string, done func(ManifestState)) {
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
		state := ManifestFailed
		switch {
		case isNotFound(a.err):
			state = ManifestAbsent
		case a.err == nil:
			if allowed, err := ParseManifest(a.data); err == nil {
				builder.Allow(origin, allowed)
				state = ManifestRead
			}
		}
		if done != nil {
			done(state)
		}
	})
}

func isNotFound(err error) bool { return errors.Is(err, network.ErrAssetNotFound) }

func isStatus(err error) bool { return errors.Is(err, network.ErrUnexpectedStatus) }
