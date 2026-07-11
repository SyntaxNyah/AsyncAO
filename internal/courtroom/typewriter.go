package courtroom

import (
	"strings"
	"time"
)

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

	// \p pause parity with AO2-Client parse_pause_duration (courtroom.cpp:3515):
	// a bare \p pauses one second; \p<n> pauses n milliseconds, capped so a
	// hostile message can't freeze the crawl.
	pauseDefaultMs = 1000
	pauseMaxMs     = 10000
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
	// ColorExtBase tags an extended AsyncAO color (#98): StyleRun.Color is
	// ColorExtBase + the inline letter code, so the render side resolves it by
	// letter (render.ExtColorByCode) with no positional coupling. Sits well
	// above the palette/sentinels so the buildColorSpans switch can branch on
	// `Color >= ColorExtBase`.
	ColorExtBase = 0x1000
	// ColorHexBase tags an EXACT transmitted colour (v1.52.0, Tifera's "any
	// hex while chatting"): StyleRun.Color is ColorHexBase + 0xRRGGBB, parsed
	// from the inline `\c#RRGGBB` code. Sits above ColorExtBase + any letter
	// (≈0x107A) so the render switch can branch `>= ColorHexBase` first; the
	// packed range spans exactly 24 bits, so the two can never collide.
	ColorHexBase = 0x2000
)

// ExtColorCodes gates which `\c<letter>` codes the parser consumes as extended
// colors (render owns the actual RGB by the same letter; a ui test pins the two
// sets equal). Reserved letters are excluded: 'r' is rainbow, and we keep 'b'/
// 'i'/'c' clear of the bold/italic/lead-in markup to avoid confusion.
const ExtColorCodes = "pmtlgokv"

// isExtColorCode reports whether r is a consumed extended-color letter. The
// ASCII guard keeps a non-ASCII rune whose low byte aliases a code letter from
// matching. Shared by Start and StripChatMarkup so they can't drift.
func isExtColorCode(r rune) bool {
	return r >= 'a' && r <= 'z' && strings.IndexByte(ExtColorCodes, byte(r)) >= 0
}

// parseInlineHex reads the six hex digits of a `\c#RRGGBB` code; at indexes
// the '#'. A short or malformed code returns ok=false and the caller keeps
// the text literal (the standing rule for any unrecognised `\X`). Shared by
// Start and StripChatMarkup so the two can't drift.
func parseInlineHex(rs []rune, at int) (rgb int, ok bool) {
	if at+6 >= len(rs) {
		return 0, false
	}
	for k := at + 1; k <= at+6; k++ {
		var d int
		switch r := rs[k]; {
		case r >= '0' && r <= '9':
			d = int(r - '0')
		case r >= 'a' && r <= 'f':
			d = int(r-'a') + 10
		case r >= 'A' && r <= 'F':
			d = int(r-'A') + 10
		default:
			return 0, false
		}
		rgb = rgb<<4 | d
	}
	return rgb, true
}

// EffectKind identifies an inline screen-effect code the parser records at its
// reveal position. Only \s and \f are effects; \p is a timing modifier (folded
// into the rune intervals) and \n is a real newline rune — neither is a mark.
type EffectKind uint8

const (
	EffectShake EffectKind = iota // \s — screenshake (AO2-Client do_screenshake parity)
	EffectFlash                   // \f — realization flash (AO2-Client do_flash parity)
)

// EffectMark is one inline \s/\f effect to fire when the reveal reaches it. At is
// the VISIBLE-rune index (the codes aren't emitted, so it lines up with
// Typewriter.Visible()); the courtroom drains marks in order via NextEffect and
// mutates the Scene — the typewriter itself never touches SDL.
type EffectMark struct {
	At   int
	Kind EffectKind
}

// parsePauseDuration reads the optional digit run of a \p code beginning at index
// `at` (the char after 'p'). Bare \p is a 1000 ms pause; \p<n> is n ms clamped to
// pauseMaxMs (AO2-Client parse_pause_duration parity). Returns the pause in
// milliseconds and how many digit runes it consumed. Shared by Start and
// StripChatMarkup so the two can't drift.
func parsePauseDuration(rs []rune, at int) (ms, digits int) {
	pos := at
	for pos < len(rs) && rs[pos] >= '0' && rs[pos] <= '9' {
		pos++
	}
	if pos == at {
		return pauseDefaultMs, 0
	}
	n := 0
	for k := at; k < pos; k++ {
		if n < pauseMaxMs { // clamp early so a long digit run can't overflow
			n = n*10 + int(rs[k]-'0')
		}
	}
	if n > pauseMaxMs {
		n = pauseMaxMs
	}
	return n, pos - at
}

// StyleRun styles a contiguous span of the CLEAN (markup-stripped) rune
// sequence. Runs are produced in order and partition the whole message, so
// they index into the typewriter's runes by simple accumulation — the reveal
// and the styling can never drift (one parse, one rune sequence). Inline markup
// is AsyncAO-native and render-only: `\cN` (N = 0..8) starts a palette color,
// `\cr` rainbow, `\c<letter>` an extended AsyncAO color (#98), `\c#RRGGBB` an
// EXACT hex color (v1.52.0), `\b` toggles bold, `\i` toggles italic, `\\` is a
// literal backslash. Incoming AO messages (which don't use these) are
// unaffected; markup you send won't render on stock AO clients (they drop the
// escape — extended and hex colors carry a nearest-standard wire fallback so
// those clients still see a sensible colour).
type StyleRun struct {
	Len    int  // clean runes covered
	Color  int  // palette index, ColorDefault / ColorRainbow, or ColorExtBase+code
	Bold   bool // \b
	Italic bool // \i
}

// Typewriter reveals message text at AO cadence and schedules blips. Pure
// logic: rasterization and the audible blip live render/audio-side.
type Typewriter struct {
	runes     []rune
	intervals []time.Duration // per-rune delay (speed codes pre-applied)
	styles    []StyleRun      // inline-color runs over runes (partition, in order)
	effects   []EffectMark    // inline \s/\f screen-effect marks, in reveal order

	visible      int
	accumulator  time.Duration
	blipCounter  int
	effectCursor int // next unfired effect (advanced by NextEffect; dropped by SkipToEnd)

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
	t.effects = t.effects[:0]
	t.visible = 0
	t.accumulator = 0
	t.blipCounter = 0
	t.effectCursor = 0

	speed := speedStepDefault
	color := ColorDefault
	bold, italic := false, false
	runLen := 0
	// pendingPause holds a \p code's delay until the next rune is emitted; it
	// rides that rune's interval (a timing modifier like the '{'/'}' speed steps,
	// NOT a fired effect).
	pendingPause := time.Duration(0)
	emit := func(r rune) {
		t.runes = append(t.runes, r)
		t.intervals = append(t.intervals, time.Duration(float64(t.Interval)*speedMultipliers[speed])+pendingPause)
		pendingPause = 0
		runLen++
	}
	// flush closes the current run; a style change opens a new one, so each run
	// carries one (color, bold, italic) over the runes it covers.
	flush := func() {
		if runLen > 0 {
			t.styles = append(t.styles, StyleRun{Len: runLen, Color: color, Bold: bold, Italic: italic})
			runLen = 0
		}
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
				if c := int(rs[i+2] - '0'); c != color {
					flush()
					color = c
				}
				i += 2
				continue
			case n == 'c' && i+2 < len(rs) && rs[i+2] == 'r':
				if color != ColorRainbow {
					flush()
					color = ColorRainbow
				}
				i += 2
				continue
			case n == 'c' && i+2 < len(rs) && isExtColorCode(rs[i+2]):
				if ec := ColorExtBase + int(rs[i+2]); ec != color {
					flush()
					color = ec
				}
				i += 2
				continue
			case n == 'c' && i+2 < len(rs) && rs[i+2] == '#':
				// Exact hex colour `\c#RRGGBB` (v1.52.0). Malformed hex exits
				// the switch → the backslash (and the rest) stays literal.
				if rgb, hok := parseInlineHex(rs, i+2); hok {
					if hc := ColorHexBase + rgb; hc != color {
						flush()
						color = hc
					}
					i += 8 // past \c#RRGGBB
					continue
				}
			case n == 'b': // toggle bold
				flush()
				bold = !bold
				i++
				continue
			case n == 'i': // toggle italic
				flush()
				italic = !italic
				i++
				continue
			case n == 'n': // \n → a real line break (wrapText splits on '\n')
				emit('\n')
				i++
				continue
			case n == 's': // \s → screenshake when the reveal reaches here (AO2 parity)
				t.effects = append(t.effects, EffectMark{At: len(t.runes), Kind: EffectShake})
				i++
				continue
			case n == 'f': // \f → realization flash when the reveal reaches here
				t.effects = append(t.effects, EffectMark{At: len(t.runes), Kind: EffectFlash})
				i++
				continue
			case n == 'p': // \p / \p<n> → pause before the next rune (timing modifier)
				ms, digits := parsePauseDuration(rs, i+2)
				pendingPause += time.Duration(ms) * time.Millisecond
				i += 1 + digits
				continue
			}
			// any other `\X`: fall through and emit the backslash literally
		}
		emit(r)
	}
	flush()
}

// StartAppend loads message with prefix ALREADY revealed — the 2.8 additive case
// (#14): an ADDITIVE=1 line continues the previous one, so the prior text shows
// instantly and only the appended tail crawls (pacing + blips run on the tail
// alone). It runs the ordinary single pass over prefix+message — so inline
// colour/speed state carries across the join exactly like AO2 concatenating the
// HTML — then reveals up to the prefix's rune count. Interior \s/\f marks inside the
// prefix are DROPPED (they fired on the original line; a re-reveal must not re-fire
// them), matching SkipToEnd's rule for skipped marks. prefix "" is identical to
// Start(message).
func (t *Typewriter) StartAppend(prefix, message string) {
	t.Start(prefix + message)
	// The processed-prefix rune count is what StripChatMarkup yields for the prefix:
	// TestStripMatchesTypewriter pins StripChatMarkup's rune output equal to Start's
	// emitted runes, so this lands the boundary exactly at the join.
	pre := len([]rune(StripChatMarkup(prefix)))
	if pre > len(t.runes) {
		pre = len(t.runes)
	}
	t.visible = pre
	// Skip effect marks strictly INSIDE the prefix (they fired on the original
	// line). A mark AT the boundary (At == pre) belongs to the appended tail — the
	// reveal reaches it as the tail starts — so it must NOT be skipped.
	for t.effectCursor < len(t.effects) && t.effects[t.effectCursor].At < pre {
		t.effectCursor++
	}
}

// StripChatMarkup returns the plain display text for a message — the same
// markup the typewriter removes (speed `{ }`, color `\cN`/`\cr`, bold/italic
// `\b`/`\i`, and the `\\` escape), so the IC log shows exactly what the chatbox
// renders. Kept in lock-step with Start by TestStripMatchesTypewriter.
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
			case n == 'c' && i+2 < len(rs) && ((rs[i+2] >= '0' && rs[i+2] <= '8') || rs[i+2] == 'r' || isExtColorCode(rs[i+2])):
				i += 2
				continue
			case n == 'c' && i+2 < len(rs) && rs[i+2] == '#':
				if _, hok := parseInlineHex(rs, i+2); hok { // exact hex (v1.52.0); malformed stays literal
					i += 8
					continue
				}
			case n == 'b' || n == 'i': // bold / italic toggles
				i++
				continue
			case n == 'n': // \n → newline (chatbox breaks on it; the IC log flattens it)
				out = append(out, '\n')
				i++
				continue
			case n == 's' || n == 'f': // screen-effect codes leave no glyph
				i++
				continue
			case n == 'p': // pause code: drop 'p' and any digits
				_, digits := parsePauseDuration(rs, i+2)
				i += 1 + digits
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

// SkipToEnd reveals everything (queue skip / user interrupt). Interior \s/\f
// marks are DROPPED, not fired — a skip/recall must not burst every screenshake
// the message would have crawled through.
func (t *Typewriter) SkipToEnd() {
	t.visible = len(t.runes)
	t.effectCursor = len(t.effects)
}

// NextEffect returns the next inline \s/\f mark whose position has been revealed
// since the last call, advancing an internal cursor; ok=false when none are
// pending. The courtroom drains these each tick and mutates the Scene, so the
// pure typewriter never touches SDL. 0-alloc (value return, no slice).
func (t *Typewriter) NextEffect() (EffectMark, bool) {
	if t.effectCursor < len(t.effects) && t.effects[t.effectCursor].At <= t.visible {
		m := t.effects[t.effectCursor]
		t.effectCursor++
		return m, true
	}
	return EffectMark{}, false
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

		if (r != ' ' && r != '\n') || t.BlipOnSpaces {
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
