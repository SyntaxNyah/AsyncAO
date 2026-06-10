package courtroom

import "time"

const (
	// DefaultCharInterval is the base typewriter cadence (PROMPT.md §1).
	DefaultCharInterval = 18 * time.Millisecond
	// DefaultBlipRate fires one blip per N visible characters (AO2-Client
	// blip_rate default).
	DefaultBlipRate = 2

	// speedStepCount is the number of AO text-speed steps; '{' slows one
	// step, '}' speeds one step, starting at speedStepDefault — mirrors
	// AO2-Client's message_display_speed table, expressed as multipliers of
	// the base interval.
	speedStepDefault = 3
)

// speedMultipliers scales the base interval per AO speed step. Index 3 is
// 1.0× (AO2-Client's 40 ms baseline maps to our 18 ms budget cadence).
var speedMultipliers = [...]float64{0, 0.25, 0.625, 1.0, 1.25, 1.75, 2.25}

// Typewriter reveals message text at AO cadence and schedules blips. Pure
// logic: rasterization and the audible blip live render/audio-side.
type Typewriter struct {
	runes     []rune
	intervals []time.Duration // per-rune delay (speed codes pre-applied)

	visible     int
	accumulator time.Duration
	blipCounter int

	// Interval is the base per-character delay.
	Interval time.Duration
	// BlipRate fires a blip every N visible non-space characters.
	BlipRate int
	// BlipOnSpaces also counts spaces toward blips (AO "blank blips").
	BlipOnSpaces bool
}

// NewTypewriter returns a typewriter with AO defaults.
func NewTypewriter() Typewriter {
	return Typewriter{Interval: DefaultCharInterval, BlipRate: DefaultBlipRate}
}

// Start loads a message, stripping AO inline speed codes ('{' slower,
// '}' faster) into per-rune delays.
func (t *Typewriter) Start(message string) {
	t.runes = t.runes[:0]
	t.intervals = t.intervals[:0]
	t.visible = 0
	t.accumulator = 0
	t.blipCounter = 0

	speed := speedStepDefault
	for _, r := range message {
		switch r {
		case '{':
			if speed < len(speedMultipliers)-1 {
				speed++
			}
			continue
		case '}':
			if speed > 0 {
				speed--
			}
			continue
		}
		t.runes = append(t.runes, r)
		scaled := time.Duration(float64(t.Interval) * speedMultipliers[speed])
		t.intervals = append(t.intervals, scaled)
	}
}

// Text returns the full display text (speed codes stripped).
func (t *Typewriter) Text() string { return string(t.runes) }

// Visible returns how many runes are revealed.
func (t *Typewriter) Visible() int { return t.visible }

// Done reports whether the full message is revealed.
func (t *Typewriter) Done() bool { return t.visible >= len(t.runes) }

// SkipToEnd reveals everything (queue skip / user interrupt).
func (t *Typewriter) SkipToEnd() {
	t.visible = len(t.runes)
}

// Update advances the reveal by dt. It returns how many new runes became
// visible and how many blips to fire this tick. The render thread's frame
// cost stays O(revealed) — no per-character layout here (PROMPT.md §12).
func (t *Typewriter) Update(dt time.Duration) (revealed, blips int) {
	if t.Done() {
		return 0, 0
	}
	t.accumulator += dt
	for !t.Done() {
		need := t.intervals[t.visible]
		if need > 0 && t.accumulator < need {
			break
		}
		t.accumulator -= need
		r := t.runes[t.visible]
		t.visible++
		revealed++

		if r != ' ' || t.BlipOnSpaces {
			t.blipCounter++
			rate := t.BlipRate
			if rate < 1 {
				rate = 1
			}
			if t.blipCounter%rate == 0 {
				blips++
			}
		}
	}
	return revealed, blips
}
