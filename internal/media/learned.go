package media

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

const (
	// LearnedVersion changes whenever the saved layout or Key, Class or Place change meaning; another version is
	// ignored.
	LearnedVersion = 2
	// learnedOriginsCap bounds the hosts whose formats are kept between sessions; the ones with the fewest finds go
	// first.
	learnedOriginsCap = 256
	// learnedMaxBytes bounds a decompressed file, so a damaged or hostile one can't fill memory (hard rule 4).
	learnedMaxBytes = 64 << 20
)

// A winner line is its key's numbers, how its path is stored, the path and the file, separated by tabs.
const (
	fieldPlace = iota
	fieldKind
	fieldClass
	fieldSize
	fieldTag
	fieldHow
	fieldPath
	fieldFile
	winnerFields
)

const (
	winnerSep = "\t"
	// decimal and byteBits parse a line's byte-sized numbers.
	decimal  = 10
	byteBits = 8
	// pathUnder: the path is stored after its host's prefix; pathOfOrigin: the key's Origin is the host, path as is;
	// pathWhole: stored whole.
	pathUnder    = "0"
	pathOfOrigin = "1"
	pathWhole    = "2"
	// fileAfterPath marks a file that is the key's own name plus a suffix (its extension); fileUnderHost one stored
	// after the host's prefix. Anything else is a whole URL.
	fileAfterPath = "+"
	fileUnderHost = "~"
)

// Learned is what searches taught a Manager and its CandidateBuilder, as kept between sessions: per host, the
// formats found there and the files keys were last found as.
type Learned struct {
	Version int                     `json:"v"`
	Hosts   map[string]*LearnedHost `json:"h"`
}

// LearnedHost is one host's part of Learned.
type LearnedHost struct {
	// Formats counts how often each format was found, per class.
	Formats map[Class]map[string]int `json:"f,omitempty"`
	// Winners are one line per key; see winnerLine.
	Winners []string `json:"w,omitempty"`
}

var errLearnedTooLarge = errors.New("media: learned file too large")

// Memory is a copy of what a Manager and its CandidateBuilder learned; Learned builds the saved form from it on any
// goroutine, so the Manager's is held only for the copy.
type Memory struct {
	origins []string
	formats map[string]map[Class]map[string]int
	winners map[Key]File
}

// Remember copies what m and b have learned; call it on the Manager's goroutine (Do or Call).
func Remember(m *Manager, b *CandidateBuilder) Memory {
	mem := Memory{formats: map[string]map[Class]map[string]int{}, winners: maps.Clone(m.winners)}
	for origin, h := range b.origins {
		mem.origins = append(mem.origins, origin)
		if len(h.learned) == 0 {
			continue
		}
		classes := make(map[Class]map[string]int, len(h.learned))
		for class, counts := range h.learned {
			classes[class] = maps.Clone(counts)
		}
		mem.formats[origin] = classes
	}
	return mem
}

// Learned is the saved form: hosts with the most finds first, up to learnedOriginsCap, and every winner under the
// host it belongs to.
func (mem Memory) Learned() Learned {
	l := Learned{Version: LearnedVersion, Hosts: map[string]*LearnedHost{}}
	host := func(origin string) *LearnedHost {
		h := l.Hosts[origin]
		if h == nil {
			h = &LearnedHost{}
			l.Hosts[origin] = h
		}
		return h
	}
	origins := slices.SortedFunc(maps.Keys(mem.formats), func(x, y string) int {
		return finds(mem.formats[y]) - finds(mem.formats[x])
	})
	for _, origin := range origins[:min(len(origins), learnedOriginsCap)] {
		host(origin).Formats = mem.formats[origin]
	}
	for k, f := range mem.winners {
		group := originAmong(k, slices.Values(mem.origins))
		if line, ok := winnerLine(group, k, f); ok {
			h := host(group)
			h.Winners = append(h.Winners, line)
		}
	}
	return l
}

// finds totals one host's counts.
func finds(classes map[Class]map[string]int) int {
	n := 0
	for _, counts := range classes {
		for _, c := range counts {
			n += c
		}
	}
	return n
}

// winnerLine writes one winner relative to its host; false for a path or file a line can't hold.
func winnerLine(group string, k Key, f File) (string, bool) {
	how, path := pathWhole, k.Path
	switch {
	case k.Origin != "":
		how = pathOfOrigin // originOf made the key's own origin its group
	case strings.HasPrefix(k.Path, group):
		how, path = pathUnder, strings.TrimPrefix(k.Path, group)
	}
	file := string(f)
	if rest, ok := strings.CutPrefix(file, k.Origin+k.Path); ok {
		file = fileAfterPath + rest
	} else if rest, ok := strings.CutPrefix(file, group); ok {
		file = fileUnderHost + rest
	}
	if strings.ContainsAny(path, winnerSep+"\n") || strings.ContainsAny(file, winnerSep+"\n") {
		return "", false
	}
	return strings.Join([]string{
		strconv.Itoa(int(k.Place)), strconv.Itoa(int(k.Kind)), strconv.Itoa(int(k.Class)), strconv.Itoa(k.Size),
		strconv.Itoa(int(k.Tag)), how, path, file,
	}, winnerSep), true
}

// readWinner rebuilds one winner line; false for a damaged one.
func readWinner(group, line string) (Key, File, bool) {
	parts := strings.Split(line, winnerSep)
	if len(parts) != winnerFields {
		return Key{}, "", false
	}
	// The byte fields parse at 8 bits, so a value past 255 fails here rather than wrapping in a conversion.
	var small [fieldSize]uint8
	for _, i := range []int{fieldPlace, fieldKind, fieldClass} {
		n, err := strconv.ParseUint(parts[i], decimal, byteBits)
		if err != nil {
			return Key{}, "", false
		}
		small[i] = uint8(n)
	}
	tag, err := strconv.ParseUint(parts[fieldTag], decimal, byteBits)
	size, sizeErr := strconv.Atoi(parts[fieldSize])
	if err != nil || sizeErr != nil || size < 0 || Class(small[fieldClass]) > ClassBlips {
		return Key{}, "", false
	}
	k := Key{Place: Place(small[fieldPlace]), Kind: Kind(small[fieldKind]), Class: Class(small[fieldClass]),
		Size: size, Tag: uint8(tag)}
	path, file := parts[fieldPath], parts[fieldFile]
	switch parts[fieldHow] {
	case pathUnder:
		k.Path = group + path
	case pathOfOrigin:
		k.Origin, k.Path = group, path
	case pathWhole:
		k.Path = path
	default:
		return Key{}, "", false
	}
	if rest, ok := strings.CutPrefix(file, fileAfterPath); ok {
		file = k.Origin + k.Path + rest
	} else if rest, ok := strings.CutPrefix(file, fileUnderHost); ok {
		file = group + rest
	}
	if k.Path == "" || file == "" {
		return Key{}, "", false
	}
	return k, File(file), true
}

// Memory reads the saved form back, leaving out what another version, an unknown class or format, or a damaged line
// would get wrong; any goroutine. Saved counts are halved, so a host that changed its formats is relearned in a few
// sessions.
func (l Learned) Memory() Memory {
	mem := Memory{formats: map[string]map[Class]map[string]int{}, winners: map[Key]File{}}
	if l.Version != LearnedVersion {
		return mem
	}
	for origin, h := range l.Hosts {
		if h == nil {
			continue
		}
		for class, counts := range h.Formats {
			if class == ClassExact || class > ClassBlips {
				continue
			}
			for ext, c := range counts {
				if c <= 0 || !knownExt(ext) {
					continue
				}
				if mem.formats[origin] == nil {
					mem.formats[origin] = map[Class]map[string]int{}
				}
				if mem.formats[origin][class] == nil {
					mem.formats[origin][class] = map[string]int{}
				}
				mem.formats[origin][class][ext] = (c + 1) / 2
			}
		}
		for _, line := range h.Winners {
			if len(mem.winners) >= winnersCap {
				break
			}
			if k, f, ok := readWinner(origin, line); ok {
				mem.winners[k] = f
			}
		}
	}
	return mem
}

// Recall adds mem to what m and b know, keeping what this session already learned; call it on the Manager's
// goroutine. m may keep mem's maps, so mem is not used again.
func Recall(m *Manager, b *CandidateBuilder, mem Memory) {
	for origin, classes := range mem.formats {
		hints := b.hints(origin)
		for class, counts := range classes {
			if hints.learned[class] == nil {
				hints.learned[class] = map[string]int{}
			}
			for ext, c := range counts {
				hints.learned[class][ext] += c
			}
		}
	}
	if len(m.winners) == 0 && mem.winners != nil {
		m.winners = mem.winners // the usual case at start; Memory caps it at winnersCap
		return
	}
	for k, f := range mem.winners {
		if len(m.winners) >= winnersCap {
			break
		}
		if _, known := m.winners[k]; !known {
			m.winners[k] = f
		}
	}
}

// EncodeLearned is the saved form of l: JSON, gzip'd.
func EncodeLearned(l Learned) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(zw).Encode(l); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeLearned reads what EncodeLearned wrote.
func DecodeLearned(data []byte) (Learned, error) {
	var l Learned
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return l, fmt.Errorf("media: reading learned file: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(zr, learnedMaxBytes+1))
	if err != nil {
		return l, fmt.Errorf("media: reading learned file: %w", err)
	}
	if len(raw) > learnedMaxBytes {
		return l, errLearnedTooLarge
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return l, fmt.Errorf("media: reading learned file: %w", err)
	}
	return l, nil
}

// knownExt reports whether ext is a format some decoder reads.
func knownExt(ext string) bool {
	return slices.Contains(defaultImageOrder, ext) || slices.Contains(defaultAudioOrder, ext)
}

// Call runs fn on the Manager's goroutine and waits for it; false when the Manager stopped first. Never call it from
// the Manager's goroutine.
func (m *Manager) Call(fn func()) bool {
	done := make(chan struct{})
	m.Do(func() {
		fn()
		close(done)
	})
	select {
	case <-done:
		return true
	case <-m.stopped:
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
}
