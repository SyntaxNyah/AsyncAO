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

// ao2Markup is one AO2 inline chat-colour delimiter (§3.8 interop). AO2 keeps
// these characters IN the transmitted text — unlike AsyncAO's render-only `\c`
// codes — which is what lets a real AO2/webAO client render the same colour, so
// we interpret them at render time on both incoming and outgoing text and let
// them survive to the wire untouched (packet.go escapes only `#%$&`, never
// these). Semantics + the stock character table are transcribed from AO2-Client:
// the parse loop is Courtroom::filter_ic_text (src/courtroom.cpp:3532-3817) and
// the default characters are its stock theme's chat_config.ini
// (bin/base/themes/default/chat_config.ini). The mapping to AsyncAO is exact:
// AO2 colour index c0..c8 == render.TextColor index 0..8 == StyleRun.Color 0..8.
//
//	Palette: `Start` opens colour `Palette`; `End` closes it. When Start==End the
//	delimiter TOGGLES (a second one closes) — chat_config.ini c1/c2/c3/c5/c6/c7,
//	all `_remove = 1` (the delimiter is consumed, not shown). When Start != End
//	the pair BRACKETS a span — c4 `(`…`)` and c8 `[`…`]`, both `_remove = 0`
//	(the brackets stay visible). `Remove` mirrors that `_remove` flag.
type ao2Markup struct {
	Start   rune // opening delimiter (chat_config.ini cN_start)
	End     rune // closing delimiter (cN_end; == Start for the toggle colours)
	Palette int  // AO colour index 0..8 (== render.TextColor index)
	Remove  bool // cN_remove: true → the delimiter is consumed, false → kept visible
}

// ao2Markups is the STOCK default-theme table, in AO2 colour order c1..c8
// (c0 is the uncoloured default — no delimiter). Values verbatim from
// AO2-Client/bin/base/themes/default/chat_config.ini:
//
//	c1 Green   ` ` `  toggle remove=1   c5 Yellow  º º    toggle remove=1
//	c2 Red     ~ ~    toggle remove=1   c6 Magenta № №    toggle remove=1
//	c3 Orange  | |    toggle remove=1   c7 Cyan    √ √    toggle remove=1
//	c4 Blue    ( )    pair   remove=0   c8 Gray    [ ]    pair   remove=0
//
// AsyncAO's theme system is asset-driven and does not (yet) expose per-theme
// remappable chat markup, so we hard-code the stock table: it is what every
// AO2/webAO peer sends by default, which is exactly the interop target. Every
// character is a named constant here (no magic literals) — hard rule §9.
var ao2Markups = [...]ao2Markup{
	{Start: '`', End: '`', Palette: 1, Remove: true},  // c1 green
	{Start: '~', End: '~', Palette: 2, Remove: true},  // c2 red
	{Start: '|', End: '|', Palette: 3, Remove: true},  // c3 orange
	{Start: '(', End: ')', Palette: 4, Remove: false}, // c4 blue (brackets kept)
	{Start: 'º', End: 'º', Palette: 5, Remove: true},  // c5 yellow
	{Start: '№', End: '№', Palette: 6, Remove: true},  // c6 magenta
	{Start: '√', End: '√', Palette: 7, Remove: true},  // c7 cyan
	{Start: '[', End: ']', Palette: 8, Remove: false}, // c8 gray (brackets kept)
}

// AO2MarkupFor returns the AO2 start/end delimiters for palette colour p (0..8),
// so the IC "select-and-colour" UI can wrap a selection exactly as AO2 does
// (courtroom.cpp:6381-6388 on_text_color_changed brackets the selection with
// cN_start … cN_end). ok is false for palette 0 (the default colour has no
// delimiter) or an out-of-range index. Both returned runes are the STOCK
// default-theme characters, so the wrapped span renders on any AO2/webAO peer.
func AO2MarkupFor(p int) (start, end rune, ok bool) {
	for i := range ao2Markups {
		if ao2Markups[i].Palette == p {
			return ao2Markups[i].Start, ao2Markups[i].End, true
		}
	}
	return 0, 0, false
}

// AO2ColorCount is how many AO palette colours (1..8) carry an inline delimiter;
// the swatch row draws one cube per colour. Colour 0 (default) has no markup.
const AO2ColorCount = len(ao2Markups)

// AO2ColorPalette returns the palette index (1..8) of the i-th swatch (0-based),
// so the UI can draw the cubes in AO2 colour order and resolve each to its
// render colour via render.TextColor without hard-coding the mapping.
func AO2ColorPalette(i int) int { return ao2Markups[i].Palette }

// ao2ColorStack is the parse-time nesting stack for AO2 markup, mirroring
// std::stack<int> ic_color_stack in filter_ic_text (courtroom.cpp:3539). It
// holds AO palette indices of the OPEN colours; the innermost (top) is the
// colour that renders. It is a fixed-size value type so the parser stays
// allocation-free on the message-raster hot path (a message can't nest deeper
// than it has characters, but ao2StackMax bounds a hostile input cheaply).
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
	// Clamp the read index: push lets n advance PAST ao2StackMax (so a later close
	// still balances a dropped open), but buf only has ao2StackMax slots — an
	// unguarded buf[n-1] would panic on a hostile deeply-nested incoming message
	// (e.g. `~`~`~… drives n past the cap). Reading the last real slot instead is
	// harmless: over-deep colours simply share the deepest stored colour.
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

// ao2Match interprets rune r as an AO2 markup delimiter against the current
// colour stack, mirroring the delimiter branch of filter_ic_text exactly
// (courtroom.cpp:3646-3703). It reports whether r was a delimiter (handled),
// whether the delimiter character is consumed (skip = its Remove flag), and the
// new innermost colour (stack top). It MUST be called identically from Start and
// StripChatMarkup — the pair is kept equal by TestStripMatchesTypewriter, and a
// delimiter recognised in one but not the other silently desyncs the chatbox
// from the IC log. Toggle colours (Start==End) close when they are the innermost
// open colour, else open (nest); paired colours open on Start and close on End
// only when they are innermost, else the stray delimiter is an ordinary glyph.
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

// isAO2Delim reports whether r is any AO2 markup delimiter (start OR end). Used
// only by the `\`-escape path so `\`+delimiter emits the delimiter LITERALLY —
// AO2's parse_escape_seq suppresses markup on the next character
// (courtroom.cpp:3633-3639, 3729-3759: an escaped non-code char just falls
// through to the literal insert). Shared by Start and StripChatMarkup.
func isAO2Delim(r rune) bool {
	for i := range ao2Markups {
		if r == ao2Markups[i].Start || r == ao2Markups[i].End {
			return true
		}
	}
	return false
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
// and the styling can never drift (one parse, one rune sequence). Two inline
// colour schemes are honoured:
//
//   - AsyncAO-native, render-ONLY `\c` codes: `\cN` (N = 0..8) starts a palette
//     colour, `\cr` rainbow, `\c<letter>` an extended AsyncAO colour (#98),
//     `\c#RRGGBB` an EXACT hex colour (v1.52.0), `\b`/`\i` toggle bold/italic,
//     `\\` a literal backslash. These are stripped from the wire text, so they
//     colour only on another AsyncAO client (extended/hex carry a nearest-
//     standard wire text_color fallback so stock clients still see a sensible
//     colour).
//   - AO2 inline markup (§3.8, ao2Markups): the bare delimiters “ ` ~ | º № √ “
//     (toggles) and `( )` / `[ ]` (pairs) that AO2/webAO keep IN the transmitted
//     text. They map to palette indices 0..8 and are interpreted at render time
//     on BOTH incoming and outgoing messages, and they SURVIVE to the wire — so a
//     real AO2/webAO peer renders the same colour. `\` before a delimiter escapes
//     it to a literal (AO2's own convention). The two schemes compose in one
//     message: an AO2 span nests OVER the current `\c` colour and returns to it
//     when it closes.
//
// A literal backtick, tilde, pipe, º, №, √, or a matched bracket pair therefore
// now colours chat instead of showing verbatim — escape with a leading `\` to
// type one literally.
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
// runes, so the reveal and the colors stay in lockstep. Inline markup: the
// AsyncAO-native `\c` codes (`\cN` palette, `\cr` rainbow, `\c<letter>`/`\c#hex`,
// `\b`/`\i`) plus AO2's bare colour delimiters (§3.8, ao2Markups) which nest OVER
// the current `\c` colour; `\\` is a literal backslash and `\`+delimiter escapes
// an AO2 delimiter. A lone `\` (or any other `\X`) is kept literally so ordinary
// text isn't eaten. Every consumption rule here is mirrored in StripChatMarkup
// (pinned equal by TestStripMatchesTypewriter).
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
	// base is the AsyncAO-native `\c` colour (ColorDefault until a `\cN` code).
	// ao2 is the AO2 inline-markup nesting stack (§3.8): while it is non-empty its
	// innermost colour WINS over base, so an AO2 span colours text even mid-message
	// and returns to base when it closes — the "return to default" that the render-
	// only `\c` scheme lacks. curColor folds the two into the run colour, so the
	// two schemes compose in one message without confusing each other.
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
	// setColor re-derives the effective run colour after a base or AO2-stack change
	// and starts a fresh run if it moved. Called after every colour-affecting code.
	setColor := func() {
		if c := curColor(); c != color {
			flush()
			color = c
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
				// Exact hex colour `\c#RRGGBB` (v1.52.0). Malformed hex exits
				// the switch → the backslash (and the rest) stays literal.
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
			case isAO2Delim(n):
				// AO2 escape (§3.8): a backslash before an AO2 markup delimiter
				// makes it a LITERAL character, not a colour toggle — AO2's
				// parse_escape_seq (courtroom.cpp:3633-3639). The backslash is
				// dropped, the delimiter emitted as an ordinary glyph.
				emit(n)
				i++
				continue
			}
			// any other `\X`: fall through and emit the backslash literally
		}
		// AO2 inline colour markup (§3.8): interpret bare delimiters against the
		// nesting stack. A handled delimiter updates the effective colour; its
		// glyph is dropped for toggle colours (Remove) and kept for the bracket
		// pairs. Runs BEFORE the plain emit so a stray/unmatched delimiter still
		// emits as a normal character (ao2Match returned handled=false).
		depthBefore := ao2.n
		if handled, skip := ao2Match(r, &ao2); handled {
			if ao2.n > depthBefore {
				// An OPEN delimiter: AO2 colours an opening bracket with the NEW
				// span colour (courtroom.cpp:3723 emits the font switch before the
				// char), so recompute the colour, then emit any kept glyph.
				setColor()
				if !skip {
					emit(r)
				}
			} else {
				// A CLOSE delimiter: AO2 keeps the closing bracket in the span
				// colour it closes (courtroom.cpp:3716-3722 inserts the char, then
				// closes the font), so emit any kept glyph before recomputing.
				if !skip {
					emit(r)
				}
				setColor()
			}
			continue
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
// `\b`/`\i`, the `\\` escape, and AO2 inline colour delimiters §3.8), so the IC
// log shows exactly what the chatbox renders. Kept in lock-step with Start by
// TestStripMatchesTypewriter — every consumption rule here MUST mirror Start.
func StripChatMarkup(message string) string {
	rs := []rune(message)
	out := make([]rune, 0, len(rs))
	// AO2 markup stack, run IDENTICALLY to Start so the two can't drift: toggle
	// delimiters (Remove) are dropped, bracket pairs are kept — the stack drives
	// exactly the same skip decision ao2Match makes in Start.
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
