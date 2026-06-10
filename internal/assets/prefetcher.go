package assets

import (
	"sync"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// speakerWindowSize bounds the Markov history (PROMPT.md §10: sliding
	// window of the last 32 messages).
	speakerWindowSize = 32
	// pairPartnerPriorWeight doubles the current pair partner's predicted
	// likelihood — paired characters talk back-to-back.
	pairPartnerPriorWeight = 2
)

// SpriteBaseFunc builds the idle-sprite URL base for a character (injected
// by the courtroom layer, which owns URL conventions).
type SpriteBaseFunc func(character string) string

// Prefetcher predicts the next speaker with a first-order Markov chain over
// recent speakers and warms their idle sprite at LOW priority (PROMPT.md
// §10). All methods are called from the game thread; the lock exists only
// for the metrics snapshot.
type Prefetcher struct {
	mgr        *Manager
	spriteBase SpriteBaseFunc

	mu          sync.Mutex
	window      []string                  // recent speakers, oldest first
	transitions map[string]map[string]int // from → to → count
	predictions int64
}

// NewPrefetcher wires the predictor to the manager.
func NewPrefetcher(mgr *Manager, spriteBase SpriteBaseFunc) *Prefetcher {
	return &Prefetcher{
		mgr:         mgr,
		spriteBase:  spriteBase,
		transitions: map[string]map[string]int{},
	}
}

// OnMessage records a message by speaker (with optional active pair partner)
// and prefetches the predicted next speaker's idle sprite.
func (p *Prefetcher) OnMessage(speaker, pairPartner string) {
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

	predicted := p.predictLocked(speaker, pairPartner)
	p.mu.Unlock()

	if predicted == "" || predicted == speaker {
		return
	}
	p.predictions++
	p.mgr.Prefetch(p.spriteBase(predicted), AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (predicted next speaker)
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
