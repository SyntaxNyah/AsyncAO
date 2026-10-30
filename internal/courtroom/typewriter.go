package courtroom

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
)

const (
	// DefaultCharInterval is the base typewriter cadence.
	DefaultCharInterval = 18 * time.Millisecond
	// DefaultBlipRate fires one blip per N visible characters (AO2 blip_rate default).
	DefaultBlipRate = 2

	// speedStepCount is the AO speed steps; '{' slows and '}' speeds one from speedStepDefault (message_display_speed).
	speedStepDefault = 3

	// \p parity with parse_pause_duration (courtroom.cpp:3515): bare is one second, \p<n> n ms, capped.
	pauseDefaultMs = 1000
	pauseMaxMs     = 10000

	// AO2's blip-spam limit, msg_delay <= 25 ms (courtroom.cpp:4529-4537), as a fraction of its 40 ms base crawl,
	// since our base is 18 ms and an absolute copy would gate at another speed step.
	blipSpamCrawlNum = 25
	blipSpamCrawlDen = 40

	// blipsPerUpdate caps only sound: capping the reveal would couple the crawl to the caller's step rate.
	blipsPerUpdate = 1
)

// speedMultipliers scales the base interval per speed step; index 3 is 1.0 (AO2's 40 ms maps to our 18 ms).
var speedMultipliers = [...]float64{0, 0.25, 0.625, 1.0, 1.25, 1.75, 2.25}

// Inline-color sentinels for StyleRun.Color (real palette colors are 0..N-1).
const (
	// ColorDefault means use the message's own TextColor.
	ColorDefault = -1
	// ColorRainbow cycles the palette per rune, on the render side.
	ColorRainbow = -2
	// ColorExtBase + the inline letter tags an extended color, resolved by letter (render.ExtColorByCode).
	ColorExtBase = 0x1000
	// ColorHexBase + 0xRRGGBB tags an exact `\c#RRGGBB`; it sits above ColorExtBase + any letter, so no collision.
	ColorHexBase = 0x2000
)

// ExtColorCodes lists consumed `\c<letter>` codes; 'r' is rainbow, and 'b', 'i', 'c' stay clear of other markup.
const ExtColorCodes = "pmtlgokv"

// isExtColorCode reports a consumed letter; the ASCII guard stops a non-ASCII rune's low byte matching.
func isExtColorCode(r rune) bool {
	return r >= 'a' && r <= 'z' && strings.IndexByte(ExtColorCodes, byte(r)) >= 0
}

// ao2Markup is one AO2 colour delimiter; these stay in the sent text, so others render the colour. From AO2-Client:
type ao2Markup struct {
	Start   rune // opening delimiter (chat_config.ini cN_start)
	End     rune // closing delimiter (cN_end; == Start for the toggle colours)
	Palette int  // AO colour index 0..8 (== render.TextColor index)
	Remove  bool // cN_remove: true → the delimiter is consumed, false → kept visible
}

// AO2's palette in render.TextColor order; ao2Markups hard-codes the stock chat_config.ini table peers send.
const (
	paletteDefault = iota
	paletteGreen
	paletteRed
	paletteOrange
	paletteBlue
	paletteYellow
	paletteMagenta
	paletteCyan
	paletteGray
)

var ao2Markups = [...]ao2Markup{
	{Start: '`', End: '`', Palette: paletteGreen, Remove: true},
	{Start: '~', End: '~', Palette: paletteRed, Remove: true},
	{Start: '|', End: '|', Palette: paletteOrange, Remove: true},
	{Start: '(', End: ')', Palette: paletteBlue, Remove: false}, // brackets kept
	{Start: 'º', End: 'º', Palette: paletteYellow, Remove: true},
	{Start: '№', End: '№', Palette: paletteMagenta, Remove: true},
	{Start: '√', End: '√', Palette: paletteCyan, Remove: true},
	{Start: '[', End: ']', Palette: paletteGray, Remove: false}, // brackets kept
}

// AO2MarkupFor returns the stock delimiters for p, as on_text_color_changed wraps (courtroom.cpp:6381-6388).
func AO2MarkupFor(p int) (start, end rune, ok bool) {
	for i := range ao2Markups {
		if ao2Markups[i].Palette == p {
			return ao2Markups[i].Start, ao2Markups[i].End, true
		}
	}
	return 0, 0, false
}

// AO2ColorCount is how many colours carry a delimiter; colour 0 has none.
const AO2ColorCount = len(ao2Markups)

// ao2StackMax sizes the stack mirroring ic_color_stack (courtroom.cpp:3539), a value type so parsing doesn't allocate.
const ao2StackMax = 64

type ao2ColorStack struct {
	buf [ao2StackMax]int
	n   int
}

func (s *ao2ColorStack) empty() bool { return s.n == 0 }
func (s *ao2ColorStack) top() int {
	if s.n == 0 {
		return ColorDefault
	}
	// Clamp the read: push lets n pass ao2StackMax so a close balances, and buf[n-1] would panic on hostile nesting.
	idx := s.n - 1
	if idx >= ao2StackMax {
		idx = ao2StackMax - 1
	}
	return s.buf[idx]
}
func (s *ao2ColorStack) push(c int) {
	if s.n < ao2StackMax { // silently cap the STORE: over-deep nesting stops storing new colours
		s.buf[s.n] = c
	}
	s.n++ // count still advances so a matching close still balances the (dropped) open
}
func (s *ao2ColorStack) pop() {
	if s.n > 0 {
		s.n--
	}
}

// ao2Match mirrors filter_ic_text's delimiter branch (courtroom.cpp:3646-3703). Start and StripChatMarkup must call it
// identically, or the chatbox desyncs from the IC log. Toggles nest unless innermost; pairs close only when innermost.
func ao2Match(r rune, stack *ao2ColorStack) (handled, skip bool) {
	for i := range ao2Markups {
		m := &ao2Markups[i]
		if m.Start == m.End { // toggle colour (courtroom.cpp:3664-3683)
			if r == m.Start {
				if stack.top() == m.Palette { // innermost is us → close (courtroom.cpp:3670-3674)
					stack.pop()
				} else { // else open/nest (courtroom.cpp:3676)
					stack.push(m.Palette)
				}
				return true, m.Remove
			}
			continue
		}
		// paired colour (courtroom.cpp:3685-3702)
		if r == m.Start {
			stack.push(m.Palette)
			return true, m.Remove
		}
		if r == m.End && stack.top() == m.Palette { // close only when innermost, else literal
			stack.pop()
			return true, m.Remove
		}
	}
	return false, false
}

// isAO2Delim is for the `\` escape, which emits a delimiter literally (courtroom.cpp:3633-3639, 3729-3759).
func isAO2Delim(r rune) bool {
	for i := range ao2Markups {
		if r == ao2Markups[i].Start || r == ao2Markups[i].End {
			return true
		}
	}
	return false
}

// Bit positions of a packed 0xRRGGBB colour.
const (
	redShift   = 16
	greenShift = 8
)

// parseInlineHex reads `\c#RRGGBB`, at indexes the '#'; ok=false keeps the text literal.
func parseInlineHex(rs []rune, at int) (rgb int, ok bool) {
	const hexDigits = 6
	if at+hexDigits >= len(rs) {
		return 0, false
	}
	c, ok := parse.HexColor(string(rs[at+1 : at+1+hexDigits]))
	if !ok {
		return 0, false
	}
	return int(c.R)<<redShift | int(c.G)<<greenShift | int(c.B), true
}

// EffectKind is an inline \s or \f; \p folds into intervals and \n is a real rune.
type EffectKind uint8

const (
	EffectShake EffectKind = iota // \s — screenshake (AO2-Client do_screenshake parity)
	EffectFlash                   // \f — realization flash (AO2-Client do_flash parity)
)

// EffectMark is one \s or \f to fire when the reveal reaches At, a visible-rune index.
type EffectMark struct {
	At   int
	Kind EffectKind
}

// MidEmoteMark is one mid-text marker to fire when the reveal reaches At, a visible-rune index.
type MidEmoteMark struct {
	At   int
	Mark MidMark
}

// effectMarksMax bounds a message's marks (hard rule 4); each fire restamps one countdown, so overflow costs nothing.
const effectMarksMax = 32

// midMarksMax bounds a message's mid-text markers (hard rule 4).
const midMarksMax = 16

// parsePauseDuration reads a \p digit run at `at`, clamped to pauseMaxMs, returning ms and digits consumed.
func parsePauseDuration(rs []rune, at int) (ms, digits int) {
	const decimalBase = 10
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
			n = n*decimalBase + int(rs[k]-'0')
		}
	}
	if n > pauseMaxMs {
		n = pauseMaxMs
	}
	return n, pos - at
}

// StyleRun styles a span of the clean runes; runs partition the message. A literal AO2 delimiter needs a leading `\`.
type StyleRun struct {
	Len    int  // clean runes covered
	Color  int  // palette index, ColorDefault / ColorRainbow, or ColorExtBase+code
	Bold   bool // \b
	Italic bool // \i
}

// Typewriter reveals text at AO cadence and schedules blips; rasterizing and audio live elsewhere.
type Typewriter struct {
	runes     []rune
	intervals []time.Duration // per-rune delay (speed codes pre-applied)
	styles    []StyleRun      // inline-color runs over runes (partition, in order)
	effects   []EffectMark    // inline \s/\f screen-effect marks, in reveal order
	midMarks  []MidEmoteMark  // inline <e:...>/<s:...> markers, in reveal order

	visible      int
	accumulator  time.Duration
	blipCounter  int
	effectCursor int // next unfired effect (advanced by NextEffect; dropped by SkipToEnd)
	midCursor    int // next unfired mid-text marker (advanced by NextMidEmote; dropped by SkipToEnd)
	// effectMarkBase is where the mark budget starts: 0 for Start, the join for StartAppend.
	effectMarkBase int

	Interval time.Duration
	// BlipRate fires a blip every N visible non-space characters.
	BlipRate int
	// BlipOnSpaces also counts spaces toward blips.
	BlipOnSpaces bool
}

func NewTypewriter() Typewriter {
	return Typewriter{Interval: DefaultCharInterval, BlipRate: DefaultBlipRate}
}

// Start parses in one pass into runes, delays and style runs; every rule must be mirrored in StripChatMarkup.
func (t *Typewriter) Start(message string) { t.startJoined("", message) }

// startJoined parses prefix+message in one pass, carrying colour and speed across the join as AO2's HTML does.
func (t *Typewriter) startJoined(prefix, message string) {
	t.runes = t.runes[:0]
	t.intervals = t.intervals[:0]
	t.styles = t.styles[:0]
	t.effects = t.effects[:0]
	t.midMarks = t.midMarks[:0]
	t.visible = 0
	t.accumulator = 0
	t.blipCounter = 0
	t.effectCursor = 0
	t.effectMarkBase = 0
	t.midCursor = 0

	speed := speedStepDefault
	// base is the `\c` colour; ao2 is the nesting stack, whose innermost colour wins, so a span returns to base.
	base := ColorDefault
	var ao2 ao2ColorStack
	curColor := func() int {
		if !ao2.empty() {
			return ao2.top()
		}
		return base
	}
	color := curColor() // the colour of the run currently being built
	bold, italic := false, false
	runLen := 0
	// pendingPause holds a \p delay until the next rune, riding its interval.
	pendingPause := time.Duration(0)
	emit := func(r rune) {
		t.runes = append(t.runes, r)
		t.intervals = append(t.intervals, time.Duration(float64(t.Interval)*speedMultipliers[speed])+pendingPause)
		pendingPause = 0
		runLen++
	}
	// flush closes the current run; each run has one (color, bold, italic).
	flush := func() {
		if runLen > 0 {
			t.styles = append(t.styles, StyleRun{Len: runLen, Color: color, Bold: bold, Italic: italic})
			runLen = 0
		}
	}
	// setColor recomputes the run colour after a base or stack change.
	setColor := func() {
		if c := curColor(); c != color {
			flush()
			color = c
		}
	}

	rs := []rune(prefix + message)
	// join is where the tail starts; `>=` because i skips whole escapes and can step over it.
	join := len([]rune(prefix))
	tailOpen := false
	for i := 0; i < len(rs); i++ {
		if !tailOpen && i >= join {
			tailOpen = true
			t.effectMarkBase = len(t.effects)
		}
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
				base = int(rs[i+2] - '0')
				setColor() // only takes effect when no AO2 span is nested on top
				i += 2
				continue
			case n == 'c' && i+2 < len(rs) && rs[i+2] == 'r':
				base = ColorRainbow
				setColor()
				i += 2
				continue
			case n == 'c' && i+2 < len(rs) && isExtColorCode(rs[i+2]):
				base = ColorExtBase + int(rs[i+2])
				setColor()
				i += 2
				continue
			case n == 'c' && i+2 < len(rs) && rs[i+2] == '#':
				// Malformed hex exits the switch, so the backslash stays literal.
				if rgb, hok := parseInlineHex(rs, i+2); hok {
					base = ColorHexBase + rgb
					setColor()
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
				t.addEffect(EffectShake)
				i++
				continue
			case n == 'f': // \f → realization flash when the reveal reaches here
				t.addEffect(EffectFlash)
				i++
				continue
			case n == 'p': // \p / \p<n> → pause before the next rune (timing modifier)
				ms, digits := parsePauseDuration(rs, i+2)
				pendingPause += time.Duration(ms) * time.Millisecond
				i += 1 + digits
				continue
			case isAO2Delim(n):
				// A backslash before a delimiter makes it literal (parse_escape_seq, courtroom.cpp:3633-3639).
				emit(n)
				i++
				continue
			}
			// any other `\X`: fall through and emit the backslash literally
		}
		// Delimiters run before the plain emit, so an unmatched one still emits as a normal character.
		depthBefore := ao2.n
		if handled, skip := ao2Match(r, &ao2); handled {
			if ao2.n > depthBefore {
				// Open: AO2 colours an opening bracket with the new colour (courtroom.cpp:3723), so recompute first.
				setColor()
				if !skip {
					emit(r)
				}
			} else {
				// Close: AO2 keeps a closing bracket in the closed colour (courtroom.cpp:3716-3722), so emit first.
				if !skip {
					emit(r)
				}
				setColor()
			}
			continue
		}
		if r == '<' {
			if m, n, ok := parseMidEmoteRunes(rs, i); ok {
				t.addMidMark(m)
				i += n - 1
				continue
			}
			if m, n, ok := parseMidSFXRunes(rs, i); ok {
				t.addMidMark(m)
				i += n - 1
				continue
			}
		}
		emit(r)
	}
	flush()
}

// addEffect records a mark at the reveal position, capped at effectMarksMax since effectMarkBase.
func (t *Typewriter) addEffect(k EffectKind) {
	if len(t.effects)-t.effectMarkBase >= effectMarksMax {
		return
	}
	t.effects = append(t.effects, EffectMark{At: len(t.runes), Kind: k})
}

// addMidMark records a marker at the reveal position, capped at midMarksMax per message.
func (t *Typewriter) addMidMark(m MidMark) {
	if len(t.midMarks) >= midMarksMax {
		return
	}
	t.midMarks = append(t.midMarks, MidEmoteMark{At: len(t.runes), Mark: m})
}

// StartAppend loads an ADDITIVE=1 line with prefix revealed. Prefix marks already fired, so they drop.
func (t *Typewriter) StartAppend(prefix, message string) {
	t.startJoined(prefix, message)
	// StripChatMarkup's rune output equals Start's, so this lands exactly at the join.
	pre := len([]rune(StripChatMarkup(prefix)))
	if pre > len(t.runes) {
		pre = len(t.runes)
	}
	t.visible = pre
	// Skip marks strictly inside the prefix; a mark at the boundary (At == pre) belongs to the tail.
	for t.effectCursor < len(t.effects) && t.effects[t.effectCursor].At < pre {
		t.effectCursor++
	}
	// Same for mid-text markers.
	for t.midCursor < len(t.midMarks) && t.midMarks[t.midCursor].At < pre {
		t.midCursor++
	}
}

// StripChatMarkup returns the display text; every consumption rule must mirror Start.
func StripChatMarkup(message string) string {
	rs := []rune(message)
	out := make([]rune, 0, len(rs))
	// Run the AO2 stack as Start does, so the skip decision is the same.
	var ao2 ao2ColorStack
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
			case isAO2Delim(n): // \+delimiter → literal delimiter (AO2 escape), backslash dropped
				out = append(out, n)
				i++
				continue
			}
		}
		if handled, skip := ao2Match(r, &ao2); handled {
			if !skip { // paired brackets stay visible; toggle delimiters are consumed
				out = append(out, r)
			}
			continue
		}
		if r == '<' {
			if _, n, ok := parseMidEmoteRunes(rs, i); ok {
				i += n - 1
				continue
			}
			if _, n, ok := parseMidSFXRunes(rs, i); ok {
				i += n - 1
				continue
			}
		}
		out = append(out, r)
	}
	return string(out)
}

func (t *Typewriter) Text() string { return string(t.runes) }

// Styles returns the inline-color runs (read-only).
func (t *Typewriter) Styles() []StyleRun { return t.styles }

func (t *Typewriter) Visible() int { return t.visible }

func (t *Typewriter) Done() bool { return t.visible >= len(t.runes) }

// SkipToEnd reveals everything; marks are dropped, as a skip must not burst every screenshake.
func (t *Typewriter) SkipToEnd() {
	t.visible = len(t.runes)
	t.effectCursor = len(t.effects)
	t.midCursor = len(t.midMarks)
}

// EffectsPending reports unfired marks: an effect-only message is Done at once, yet AO2 tests the raw text and ticks it
// (courtroom.cpp:4183).
func (t *Typewriter) EffectsPending() bool { return t.effectCursor < len(t.effects) }

// NextEffect returns the next revealed mark, advancing a cursor; ok=false when none. 0-alloc.
func (t *Typewriter) NextEffect() (EffectMark, bool) {
	if t.effectCursor < len(t.effects) && t.effects[t.effectCursor].At <= t.visible {
		m := t.effects[t.effectCursor]
		t.effectCursor++
		return m, true
	}
	return EffectMark{}, false
}

// MidEmotesPending is EffectsPending for mid-text markers.
func (t *Typewriter) MidEmotesPending() bool { return t.midCursor < len(t.midMarks) }

// NextMidEmote returns the next revealed marker, advancing a cursor; ok=false when none. 0-alloc.
func (t *Typewriter) NextMidEmote() (MidEmoteMark, bool) {
	if t.midCursor < len(t.midMarks) && t.midMarks[t.midCursor].At <= t.visible {
		m := t.midMarks[t.midCursor]
		t.midCursor++
		return m, true
	}
	return MidEmoteMark{}, false
}

// Update advances the reveal; only blips have a per-call budget, one per chat_tick (courtroom.cpp:4545, :4586).
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
		if need <= 0 && !t.Done() {
			continue // AO2's zero-delay fast path: no sound, no ticker (see above)
		}
		if t.tickBlip(r, need) && blips < blipsPerUpdate {
			blips++ // due AND within this delivery's audible budget
		}
	}
	return revealed, blips
}

// tickBlip advances blip_ticker and reports a due blip (chat_tick, courtroom.cpp:4540-4555), keeping phase if dropped.
func (t *Typewriter) tickBlip(r rune, need time.Duration) bool {
	if r == '\n' {
		// AO2: `\n` is a formatting_char (:4436-4439) and never reaches the blip block (:4479).
		return false
	}
	if t.blipCounter%t.effectiveBlipRate(need) != 0 {
		t.blipCounter++ // AO2's else branch: keep ticking, whitespace included
		return false
	}
	if r == ' ' && !t.BlipOnSpaces {
		return false // due, but there is no letter to sound it on yet
	}
	t.blipCounter++
	return true
}

// effectiveBlipRate is AO2's blip period for delay need: BlipRate, raised on a fast crawl (courtroom.cpp:4528-4537).
func (t *Typewriter) effectiveBlipRate(need time.Duration) int {
	rate := t.BlipRate
	if rate < 1 {
		rate = 1
	}
	if need <= 0 || t.Interval <= 0 {
		return rate
	}
	if need*blipSpamCrawlDen > t.Interval*blipSpamCrawlNum {
		return rate // slower than the spam threshold: the user's rate stands
	}
	if r := int((t.Interval + need/2) / need); r > rate { // qRound(text_crawl / msg_delay)
		rate = r
	}
	return rate
}
