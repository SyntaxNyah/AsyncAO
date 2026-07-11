package assets

import (
	"sort"
	"sync"
)

const (
	// speakerWindowSize bounds the Markov history (spec §10: sliding
	// window of the last 32 messages).
	speakerWindowSize = 32
	// pairPartnerPriorWeight doubles the current pair partner's predicted
	// likelihood — paired characters talk back-to-back.
	pairPartnerPriorWeight = 2
	// emoteChainCap bounds the per-character emote-transition table
	// (rule §17.4): past the cap the chain stops learning new edges but
	// keeps reinforcing known ones.
	emoteChainCap = 2048
	// lastEmoteCap bounds the per-character last-emote map.
	lastEmoteCap = 256
	// prefetchMaxPredict caps the predictive-prefetch aggressiveness slider (#100):
	// how many of the top predicted next sprites to warm per message.
	prefetchMaxPredict = 4
)

// WarmFunc speculatively warms a predicted (character, emote) sprite. The UI
// layer supplies it, because it alone owns the URL conventions (the (a)/(b)/
// bare spelling chain) AND the manager: the implementation must warm BOTH the
// idle and the (b) talk sprite through the full spelling chain at PriorityLow
// with the missing-asset warning SUPPRESSED (this is pure speculation — a bare-
// named pack 404s the prefixed spelling in every format, and warning about a
// sprite no one demanded is a false alarm). emote "" means the character's
// default loop.
type WarmFunc func(character, emote string)

// Prefetcher predicts the next speaker with a first-order Markov chain over
// recent speakers — and, per character, the next EMOTE with a second chain —
// then warms the predicted sprite at LOW priority (spec §10). All methods
// are called from the game thread; the lock exists only for the metrics
// snapshot.
type Prefetcher struct {
	warm WarmFunc

	mu          sync.Mutex
	window      []string                  // recent speakers, oldest first
	transitions map[string]map[string]int // speaker → next speaker → count

	// Per-character emote chains: "<char>|<from>" → to → count. Characters
	// habitually cycle the same few emotes (think → slam → point), so the
	// predicted next SPEAKER gets their predicted next EMOTE warmed, not
	// just their default sprite.
	emoteChain map[string]map[string]int
	emoteEdges int               // total edges across emoteChain (cap guard)
	lastEmote  map[string]string // char → last seen emote

	predictions int64
	maxPredict  int // top-N predicted sprites to warm per message (1 = conservative; #100 slider)
}

// NewPrefetcher wires the predictor to a warm callback (the UI supplies it —
// it owns the URL spelling chain and the manager). warm may be nil (tests that
// exercise prediction only).
func NewPrefetcher(warm WarmFunc) *Prefetcher {
	return &Prefetcher{
		warm:        warm,
		transitions: map[string]map[string]int{},
		emoteChain:  map[string]map[string]int{},
		lastEmote:   map[string]string{},
		maxPredict:  1,
	}
}

// OnMessage records a message (speaker, optional pair partner, emote) and
// prefetches the predicted next speaker's predicted emote sprite.
func (p *Prefetcher) OnMessage(speaker, pairPartner, emote string) {
	if speaker == "" {
		return
	}
	p.mu.Lock()
	if n := len(p.window); n > 0 {
		prev := p.window[n-1]
		if prev != speaker {
			to := p.transitions[prev]
			if to == nil {
				to = map[string]int{}
				p.transitions[prev] = to
			}
			to[speaker]++
		}
	}
	p.window = append(p.window, speaker)
	if len(p.window) > speakerWindowSize {
		// Drop the oldest transition's weight as it slides out, keeping
		// the chain representative of the recent conversation.
		old := p.window[0]
		if len(p.window) > 1 {
			next := p.window[1]
			if to := p.transitions[old]; to != nil {
				if to[next] > 0 {
					to[next]--
				}
				if to[next] == 0 {
					delete(to, next)
				}
				if len(to) == 0 {
					delete(p.transitions, old)
				}
			}
		}
		copy(p.window, p.window[1:])
		p.window = p.window[:speakerWindowSize]
	}

	p.learnEmoteLocked(speaker, emote)
	n := p.maxPredict
	if n < 1 {
		n = 1
	}
	warm := make([]warmTarget, 0, n)
	for _, pc := range p.predictTopNLocked(speaker, pairPartner, n) {
		if pc == "" || pc == speaker {
			continue
		}
		warm = append(warm, warmTarget{pc, p.predictEmoteLocked(pc)})
		p.predictions++
	}
	p.mu.Unlock()

	// Warm each predicted next sprite — the callback fires the idle AND (b)
	// talk spelling chain at LOW priority (shed-able speculation, §10) with
	// the missing-asset warning suppressed. // AssetType: CharSprite (predicted next speaker+emote)
	if p.warm != nil {
		for _, w := range warm {
			p.warm(w.char, w.emote)
		}
	}
}

// warmTarget is one predicted (character, emote) sprite to warm, carried out of the
// lock so the Prefetch call happens unlocked.
type warmTarget struct{ char, emote string }

// SetAggressiveness sets how many of the top predicted next sprites to warm per
// message (#100): 1 = conservative (just the single best guess), higher warms more
// guesses at the cost of more speculative bandwidth. Clamped to a sane range.
func (p *Prefetcher) SetAggressiveness(n int) {
	if n < 1 {
		n = 1
	}
	if n > prefetchMaxPredict {
		n = prefetchMaxPredict
	}
	p.mu.Lock()
	p.maxPredict = n
	p.mu.Unlock()
}

// predictTopNLocked returns up to n most likely next speakers after current, best
// first (the active pair partner keeps the 2× prior). The per-speaker transition
// fan-out is small, so the partial sort is cheap; n<=1 fast-paths to predictLocked.
func (p *Prefetcher) predictTopNLocked(current, pairPartner string, n int) []string {
	if n <= 1 {
		if s := p.predictLocked(current, pairPartner); s != "" {
			return []string{s}
		}
		return nil
	}
	type cand struct {
		name  string
		score int
	}
	cands := make([]cand, 0, len(p.transitions[current])+1)
	seenPartner := false
	for to, count := range p.transitions[current] {
		score := count
		if to == pairPartner && pairPartner != "" {
			score *= pairPartnerPriorWeight
			seenPartner = true
		}
		cands = append(cands, cand{to, score})
	}
	if pairPartner != "" && !seenPartner {
		cands = append(cands, cand{pairPartner, 1}) // no history yet — still a good guess
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	out := make([]string, 0, n)
	for i := 0; i < len(cands) && len(out) < n; i++ {
		out = append(out, cands[i].name)
	}
	return out
}

// learnEmoteLocked records the speaker's emote transition.
func (p *Prefetcher) learnEmoteLocked(speaker, emote string) {
	if emote == "" {
		return
	}
	if prev := p.lastEmote[speaker]; prev != "" && prev != emote {
		key := speaker + "|" + prev
		to := p.emoteChain[key]
		if to == nil {
			if p.emoteEdges >= emoteChainCap {
				// Table full: keep reinforcing known edges only.
				to = nil
			} else {
				to = map[string]int{}
				p.emoteChain[key] = to
			}
		}
		if to != nil {
			if _, known := to[emote]; !known {
				if p.emoteEdges >= emoteChainCap {
					to = nil
				} else {
					p.emoteEdges++
				}
			}
			if to != nil {
				to[emote]++
			}
		}
	}
	if len(p.lastEmote) < lastEmoteCap || p.lastEmote[speaker] != "" {
		p.lastEmote[speaker] = emote
	}
}

// predictEmoteLocked returns the character's most likely next emote ("" =
// no signal; the caller's emoteBase falls back to the default sprite).
func (p *Prefetcher) predictEmoteLocked(character string) string {
	if character == "" {
		return ""
	}
	last := p.lastEmote[character]
	if last == "" {
		return ""
	}
	best, bestScore := "", 0
	for to, count := range p.emoteChain[character+"|"+last] {
		if count > bestScore {
			best, bestScore = to, count
		}
	}
	return best
}

// predictLocked returns the most likely next speaker after current. The
// active pair partner gets a 2× prior.
func (p *Prefetcher) predictLocked(current, pairPartner string) string {
	best, bestScore := "", 0
	for to, count := range p.transitions[current] {
		score := count
		if to == pairPartner && pairPartner != "" {
			score *= pairPartnerPriorWeight
		}
		if score > bestScore {
			best, bestScore = to, score
		}
	}
	if best == "" && pairPartner != "" {
		// No history yet: the pair partner is still the best guess.
		return pairPartner
	}
	return best
}

// Predicted returns how many speculative prefetches were issued.
func (p *Prefetcher) Predicted() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.predictions
}
