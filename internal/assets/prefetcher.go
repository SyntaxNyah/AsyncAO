package assets

import (
	"sync"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
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
)

// EmoteBaseFunc builds the idle-sprite URL base for a character's emote
// (injected by the courtroom layer, which owns URL conventions); emote ""
// means the character's default.
type EmoteBaseFunc func(character, emote string) string

// Prefetcher predicts the next speaker with a first-order Markov chain over
// recent speakers — and, per character, the next EMOTE with a second chain —
// then warms the predicted sprite at LOW priority (spec §10). All methods
// are called from the game thread; the lock exists only for the metrics
// snapshot.
type Prefetcher struct {
	mgr       *Manager
	emoteBase EmoteBaseFunc

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
}

// NewPrefetcher wires the predictor to the manager.
func NewPrefetcher(mgr *Manager, emoteBase EmoteBaseFunc) *Prefetcher {
	return &Prefetcher{
		mgr:         mgr,
		emoteBase:   emoteBase,
		transitions: map[string]map[string]int{},
		emoteChain:  map[string]map[string]int{},
		lastEmote:   map[string]string{},
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
	predicted := p.predictLocked(speaker, pairPartner)
	predictedEmote := p.predictEmoteLocked(predicted)
	p.mu.Unlock()

	if predicted == "" || predicted == speaker {
		return
	}
	p.predictions++
	p.mgr.Prefetch(p.emoteBase(predicted, predictedEmote), AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (predicted next speaker+emote)
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
