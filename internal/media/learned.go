package media

import (
	"maps"
	"slices"
)

const (
	// LearnedVersion changes whenever Key, Class or Place change meaning; a saved file of another version is ignored.
	LearnedVersion = 1
	// learnedOriginsCap bounds the origins kept between sessions; the ones with the fewest finds go first.
	learnedOriginsCap = 256
)

// Learned is what searches taught a Manager and its CandidateBuilder, kept between sessions.
type Learned struct {
	Version int `json:"version"`
	// Formats counts, per origin and class, how often each format was found.
	Formats map[string]map[Class]map[string]int `json:"formats"`
	Winners []Winner                            `json:"winners"`
}

// Winner is the file a key was last found as.
type Winner struct {
	Key  Key  `json:"key"`
	File File `json:"file"`
}

// Remember copies what m and b have learned; call it on the Manager's goroutine (Do or Call).
func Remember(m *Manager, b *CandidateBuilder) Learned {
	l := Learned{Version: LearnedVersion, Formats: map[string]map[Class]map[string]int{}}
	for origin, h := range b.origins {
		if len(h.learned) == 0 {
			continue
		}
		classes := make(map[Class]map[string]int, len(h.learned))
		for class, counts := range h.learned {
			classes[class] = maps.Clone(counts)
		}
		l.Formats[origin] = classes
	}
	if len(l.Formats) > learnedOriginsCap {
		origins := slices.SortedFunc(maps.Keys(l.Formats), func(x, y string) int {
			return finds(l.Formats[y]) - finds(l.Formats[x])
		})
		for _, origin := range origins[learnedOriginsCap:] {
			delete(l.Formats, origin)
		}
	}
	l.Winners = make([]Winner, 0, len(m.winners))
	for k, f := range m.winners {
		l.Winners = append(l.Winners, Winner{Key: k, File: f})
	}
	return l
}

// finds totals one origin's counts.
func finds(classes map[Class]map[string]int) int {
	n := 0
	for _, counts := range classes {
		for _, c := range counts {
			n += c
		}
	}
	return n
}

// Recall adds a saved Learned to what m and b know; call it on the Manager's goroutine. Saved counts are halved, so a
// host that changed its formats is relearned in a few sessions, and what this session already learned stays.
func Recall(m *Manager, b *CandidateBuilder, l Learned) {
	if l.Version != LearnedVersion {
		return
	}
	for origin, classes := range l.Formats {
		for class, counts := range classes {
			if class == ClassExact || class > ClassBlips {
				continue
			}
			for ext, c := range counts {
				if c <= 0 || !knownExt(ext) {
					continue
				}
				h := b.hints(origin)
				if h.learned[class] == nil {
					h.learned[class] = map[string]int{}
				}
				h.learned[class][ext] += (c + 1) / 2
			}
		}
	}
	for _, w := range l.Winners {
		if len(m.winners) >= winnersCap {
			break
		}
		if _, ok := m.winners[w.Key]; !ok && w.Key.Path != "" && w.File != "" {
			m.winners[w.Key] = w.File
		}
	}
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
