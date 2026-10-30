package assets

import (
	"sort"
	"sync"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

const (
	// speakerWindowSize bounds the Markov history to the last 32 messages.
	speakerWindowSize = 32
	// pairPartnerPriorWeight doubles the pair partner's likelihood: paired characters talk back-to-back.
	pairPartnerPriorWeight = 2
	// emoteChainCap bounds the per-character emote table (hard rule 4); past it, known edges still reinforce.
	emoteChainCap = 2048
	// lastEmoteCap bounds the per-character last-emote map.
	lastEmoteCap = 256
	// prefetchMaxPredict caps how many predicted next sprites to warm per message.
	prefetchMaxPredict = 4
)

// WarmFunc warms idle and (b) talk sprites at PriorityLow with the missing warning suppressed; "" is the default loop.
type WarmFunc func(character, emote string)

// Prefetcher predicts the next speaker and emote with Markov chains and warms the sprite at low priority.
// Methods run on the game thread; the lock is only for the metrics snapshot.
type Prefetcher struct {
	warm WarmFunc

	mu          sync.Mutex
	window      []string                  // recent speakers, oldest first
	transitions map[string]map[string]int // speaker → next speaker → count

	// emoteChain counts "<char>|<from>" to next emote, because characters cycle the same few emotes.
	emoteChain map[string]map[string]int
	emoteEdges int               // total edges across emoteChain (cap guard)
	lastEmote  map[string]string // char → last seen emote

	predictions int64
	maxPredict  int // top-N predicted sprites to warm per message (1 = conservative; #100 slider)
}

// NewPrefetcher wires the predictor to warm, which may be nil.
func NewPrefetcher(warm WarmFunc) *Prefetcher {
	return &Prefetcher{
		warm:        warm,
		transitions: map[string]map[string]int{},
		emoteChain:  map[string]map[string]int{},
		lastEmote:   map[string]string{},
		maxPredict:  1,
	}
}

// OnMessage records a message and prefetches the predicted next speaker's predicted sprite.
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

	// Low priority, so it can be shed. // AssetType: CharSprite (predicted next speaker+emote)
	if p.warm != nil {
		for _, w := range warm {
			p.warm(w.char, w.emote)
		}
	}
}

// warmTarget carries a prediction out of the lock, so the warm call happens unlocked.
type warmTarget struct{ char, emote string }

// SetAggressiveness sets how many top predictions to warm per message, clamped to a sane range.
func (p *Prefetcher) SetAggressiveness(n int) {
	n = value.Clamp(n, 1, prefetchMaxPredict)
	p.mu.Lock()
	p.maxPredict = n
	p.mu.Unlock()
}

// predictTopNLocked returns up to n likely next speakers, best first; the pair partner keeps its prior.
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

func (p *Prefetcher) learnEmoteLocked(speaker, emote string) {
	if emote == "" {
		return
	}
	if prev := p.lastEmote[speaker]; prev != "" && prev != emote {
		key := speaker + "|" + prev
		to := p.emoteChain[key]
		if to == nil {
			if p.emoteEdges >= emoteChainCap {
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

// predictEmoteLocked returns the most likely next emote, "" for no signal.
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

// predictLocked returns the most likely next speaker; the pair partner gets a 2x prior.
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
