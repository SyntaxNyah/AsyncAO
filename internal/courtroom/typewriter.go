package courtroom

import "time"

const (
	// DefaultCharInterval is the base typewriter cadence (spec §1).
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

// Inline-color sentinels for StyleRun.Color (real palette colors are 0..N-1,
// matching render.textColors / the AO TEXT_COLOR field).
const (
	// ColorDefault means "use the message's own TextColor" — the state before
	// any inline code and the initial run color.
	ColorDefault = -1
	// ColorRainbow cycles the palette per rune (rendered render-side; the
	// parser just tags the run).
	ColorRainbow = -2
)

// StyleRun colors a contiguous span of the CLEAN (markup-stripped) rune
// sequence. Runs are produced in order and partition the whole message, so
// they index into the typewriter's runes by simple accumulation — the reveal
// and the colors can never drift (one parse, one rune sequence). Inline markup
// is AsyncAO-native and render-only: `\cN` (N = 0..8) starts a palette color,
// `\cr` rainbow, `\\` is a literal backslash. Incoming AO messages (which don't
// use these) are unaffected; markup you send won't render on stock AO clients.
type StyleRun struct {
	Len   int // clean runes covered
	Color int // palette index, or ColorDefault / ColorRainbow
}

// Typewriter reveals message text at AO cadence and schedules blips. Pure
// logic: rasterization and the audible blip live render/audio-side.
type Typewriter struct {
	runes     []rune
	intervals []time.Duration // per-rune delay (speed codes pre-applied)
	styles    []StyleRun      // inline-color runs over runes (partition, in order)

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

// Start loads a message in a SINGLE pass that produces the clean rune
// sequence, the per-rune delays (AO speed codes '{' slower / '}' faster
// pre-applied), and the inline-color style runs — all indexed off the same
// runes, so the reveal and the colors stay in lockstep. Inline markup: `\cN`
// (palette color 0..8), `\cr` (rainbow), `\\` (literal backslash); a lone `\`
// (or any other `\X`) is kept literally so ordinary text isn't eaten.
func (t *Typewriter) Start(message string) {
	t.runes = t.runes[:0]
	t.intervals = t.intervals[:0]
	t.styles = t.styles[:0]
	t.visible = 0
	t.accumulator = 0
	t.blipCounter = 0

	speed := speedStepDefault
	color := ColorDefault
	runLen := 0
	emit := func(r rune) {
		t.runes = append(t.runes, r)
		t.intervals = append(t.intervals, time.Duration(float64(t.Interval)*speedMultipliers[speed]))
		runLen++
	}
	setColor := func(cn int) {
		if cn == color {
			return
		}
		if runLen > 0 {
			t.styles = append(t.styles, StyleRun{Len: runLen, Color: color})
			runLen = 0
		}
		color = cn
	}

	rs := []rune(message)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '{' {
			if speed < len(speedMultipliers)-1 {
				speed++
			}
			continue
		}
		if r == '}' {
			if speed > 0 {
				speed--
			}
			continue
		}
		if r == '\\' && i+1 < len(rs) {
			switch n := rs[i+1]; {
			case n == '\\': // escaped backslash → one literal '\'
				emit('\\')
				i++
				continue
			case n == 'c' && i+2 < len(rs) && rs[i+2] >= '0' && rs[i+2] <= '8':
				setColor(int(rs[i+2] - '0'))
				i += 2
				continue
			case n == 'c' && i+2 < len(rs) && rs[i+2] == 'r':
				setColor(ColorRainbow)
				i += 2
				continue
			}
			// any other `\X`: fall through and emit the backslash literally
		}
		emit(r)
	}
	if runLen > 0 {
		t.styles = append(t.styles, StyleRun{Len: runLen, Color: color})
	}
}

// StripChatMarkup returns the plain display text for a message — the same
// markup the typewriter removes (speed `{ }`, color `\cN`/`\cr`, and the `\\`
// escape), so the IC log shows exactly what the chatbox renders. Kept in lock-
// step with Start by TestStripMatchesTypewriter.
func StripChatMarkup(message string) string {
	rs := []rune(message)
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '{' || r == '}' {
			continue
		}
		if r == '\\' && i+1 < len(rs) {
			switch n := rs[i+1]; {
			case n == '\\':
				out = append(out, '\\')
				i++
				continue
			case n == 'c' && i+2 < len(rs) && ((rs[i+2] >= '0' && rs[i+2] <= '8') || rs[i+2] == 'r'):
				i += 2
				continue
			}
		}
		out = append(out, r)
	}
	return string(out)
}

// Text returns the full display text (markup stripped).
func (t *Typewriter) Text() string { return string(t.runes) }

// Styles returns the inline-color runs over the clean text (read-only; a
// single run of ColorDefault when the message has no inline color markup).
func (t *Typewriter) Styles() []StyleRun { return t.styles }

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
// cost stays O(revealed) — no per-character layout here (spec §12).
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
