package ui

// F5, the ROUTING half — see internal/render/pictograph_test.go for the eligibility
// table.
//
// The rule: a BMP pictograph is handed the colour-emoji face ONLY when no face in
// the text chain covers it. That gate is what keeps ★, →, ▪ and ™ drawing in the
// row's own family at the row's own weight on the (many) machines whose text fonts
// have them, while ♥ and ✨ stop being boxes on the machines whose fonts do not.

import (
	"strings"
	"testing"

	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// symbolCorpus is the census corpus, split by the route each rune is DESIGNED to
// take. That split is the whole point: the eligibility table admits whole Unicode
// blocks, and those blocks hold both real emoji (♥ ✨ ⭐ — the colour face is the
// only thing on a stock machine that has them) and ordinary typographic ornaments
// (♡ ★ ● ■ ← — every decent text font has them, and they must keep drawing in the
// row's own family at the row's own weight). A census that treats the two alike
// cannot tell "correctly promoted" from "handed to a face with no glyph for it".
//
// colour=true means: if the text chain lacks it, the bundled colour face MUST have
// it, because that is where the gate will send it.
var symbolCorpus = []struct {
	name   string
	colour bool
	runes  []rune
}{
	// Emoji presentation — the F5 gap itself. Twemoji is the guaranteed serving face
	// (bestEmojiFont prefers the OS emoji font only when SDL_ttf can render it).
	{"hearts", true, []rune{0x2665, 0x2764}},
	{"stars", true, []rune{0x2B50}},
	{"sparkles / dingbats", true, []rune{0x2728, 0x2702, 0x2705, 0x274C}},
	{"misc symbols", true, []rune{0x2600, 0x263A, 0x2622, 0x26A1}},
	{"arrows", true, []rune{0x2194, 0x21A9, 0x27A1, 0x2B05, 0x2934}},
	{"misc technical (clocks)", true, []rune{0x231A, 0x23F3, 0x23E9}},
	{"geometric shapes", true, []rune{0x25FE}},
	{"enclosed / CJK symbol strays", true, []rune{0x24C2, 0x3030, 0x3297, 0x3299}},
	// Ornaments that share the blocks. No emoji presentation, so the colour face is
	// not expected to have them — the text chain, or the v1.87 last-resort census,
	// is their route.
	{"ornaments", false, []rune{0x2661, 0x2605, 0x2606, 0x2190, 0x25A0, 0x25CF}},
}

// symbolCensusUnroutable is a codepoint deep in a supplementary Private Use Area:
// no font ships it, and it is outside every pictograph block, so NO route can claim
// it. It exists to prove the census's own predicate can still say "no" — the defect
// this test was rewritten for was a gate whose answer was structurally always "yes".
const symbolCensusUnroutable = rune(0x0F0000)

// TestUncoveredSymbolsRouteToTheEmojiFace drives the real coverage gate against the
// real embedded chain. The embedded chat face is a Latin face, so a pictograph it
// lacks must be flagged; a Latin letter it has must not be.
func TestUncoveredSymbolsRouteToTheEmojiFace(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	// A scaled set (not the chrome face): the gate resolves the chain through
	// Ctx.setOf, which only knows the scaled sets.
	primary := c.LogFontFor(DefaultScalePct, "probe")
	if primary == nil {
		t.Skip("no log face in this environment")
	}
	if c.setOf(primary) == nil {
		t.Skip("the log face is not in a scaled set here (single-font fast path)")
	}

	// Ordinary Latin must never be promoted, whatever else is on the line.
	if c.uncoveredPictograph(primary, "Objection!") {
		t.Error("plain Latin text was flagged for the colour face — the gate must be coverage-driven")
	}

	// Find a pictograph this machine's chain genuinely cannot draw. On a stock box at
	// least one of these is uncovered; if the chain happens to cover all of them the
	// gap does not exist here and there is nothing to prove.
	probes := []rune{0x2728, 0x2B50, 0x2764, 0x2665, 0x23F3, 0x2705}
	var gap rune
	for _, r := range probes {
		if c.uncoveredPictograph(primary, string(r)) {
			gap = r
			break
		}
	}
	if gap == 0 {
		t.Skip("this machine's text chain covers every probe pictograph — nothing to promote")
	}
	line := "look: " + string(gap) + " !"
	if !c.uncoveredPictograph(primary, line) {
		t.Fatalf("U+%04X is uncovered alone but not inside a sentence — the scan must look at every rune", gap)
	}
	// And render.NeedsEmojiFallback must NOT see it: that is precisely why nothing
	// used to ask for the colour face, and the whole reason this gate exists.
	if render.NeedsEmojiFallback(line) {
		t.Fatalf("U+%04X trips NeedsEmojiFallback after all — then it was never the F5 gap and this gate is testing the wrong thing", gap)
	}

	mask := c.uncoveredPictographMask(primary, []rune(line))
	if mask == nil {
		t.Fatal("uncoveredPictographMask returned nil for a line that uncoveredPictograph flagged — the two must agree")
	}
	promoted := 0
	for i, p := range mask {
		if p {
			promoted++
			if !render.BMPPictograph([]rune(line)[i]) {
				t.Errorf("mask promoted rune %d (U+%04X), which is not a pictograph", i, []rune(line)[i])
			}
		}
	}
	if promoted != 1 {
		t.Errorf("promoted %d runes of %q, want exactly the one uncovered pictograph", promoted, line)
	}
	// Nothing to promote → nil, so an ordinary label allocates nothing extra.
	if c.uncoveredPictographMask(primary, []rune("Objection!")) != nil {
		t.Error("uncoveredPictographMask allocated for a line with nothing to promote")
	}
}

// TestTheColourFaceDrawsEverySymbolWePromoteToIt is the SERVING-SIDE gate, and the
// one the routing half is worthless without: promotion only helps if the face it
// promotes to actually has the glyph. Nothing asserted that. The eligibility table
// admits whole blocks, so a rune could be routed off a text chain that has no glyph
// for it onto a colour face that has no glyph for it either — a box swapped for a
// different box, with the v1.87 last-resort census bypassed on the way.
//
// The bundled Twemoji is the face under test because it is the guaranteed one: the
// OS emoji font is used only where SDL_ttf can render it (bestEmojiFont), so
// Twemoji is what every other machine falls back to. Pure sfnt cmap probe — no SDL,
// so this runs everywhere, including where the render harness skips.
func TestTheColourFaceDrawsEverySymbolWePromoteToIt(t *testing.T) {
	face, err := sfnt.Parse(twemojiTTF)
	if err != nil {
		t.Fatalf("parse the bundled colour face: %v", err)
	}
	probed := 0
	for _, b := range symbolCorpus {
		if !b.colour {
			continue
		}
		for _, r := range b.runes {
			// The gate only ever promotes what the table admits, so a rune the table
			// does NOT admit could never reach this face — and claiming it here would
			// make the census read as coverage it does not have.
			if !render.BMPPictograph(r) {
				t.Errorf("%s: U+%s is in the colour-route corpus but the eligibility table rejects it — "+
					"nothing would ever promote it, so the chain is its only hope", b.name, upperHex(r))
				continue
			}
			probed++
			if !sfntCovers(t, face, r) {
				t.Errorf("%s: the bundled colour face has no glyph for U+%s — promoting it swaps a text-chain box "+
					"for a colour-face box, and bypasses the last-resort font census on the way (F5)", b.name, upperHex(r))
			}
		}
	}
	if probed < 20 {
		t.Fatalf("probed only %d runes — the corpus has collapsed and this gate proves nothing", probed)
	}
	// Anti-vacuity: the same probe must be able to say NO.
	if sfntCovers(t, face, symbolCensusUnroutable) {
		t.Fatalf("the colour face claims U+%s (a private-use codepoint) — the coverage probe answers yes to everything", upperHex(symbolCensusUnroutable))
	}
}

// TestSymbolCensusCoversTheSymbolBlocks extends the v1.87 Unicode census to symbols.
// The v1.87 pass proved the client could draw 27 SCRIPTS; it never asked about
// hearts, stars, arrows or dingbats, which is how the gap survived it.
//
// Every corpus rune must have a route: a face in the TEXT CHAIN draws it, or — for
// the emoji-presentation ones — the eligibility table admits it and the colour face
// it is promoted to has the glyph. "Neither" is a permanent box.
//
// The order matters and is the fix to this test's first version, which asked
// BMPPictograph first and `continue`d on yes. Every rune in the corpus is inside an
// eligible block, so that skipped the whole body: setCovers was never called, the
// stranded list was provably always empty, and the test could not fail. Chain
// first, colour face second, is also the order production resolves in.
func TestSymbolCensusCoversTheSymbolBlocks(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	primary := c.LogFontFor(DefaultScalePct, "probe")
	if primary == nil || c.setOf(primary) == nil {
		t.Skip("no scaled log set in this environment")
	}
	set := c.setOf(primary)
	colour, err := sfnt.Parse(twemojiTTF)
	if err != nil {
		t.Fatalf("parse the bundled colour face: %v", err)
	}

	// routed answers the census question for one rune, and is the ONLY place the
	// two-tier rule is written down: chain, then promotion. The anti-vacuity probe
	// below drives this same function, so a rewrite that made it always say yes
	// fails immediately instead of silently retiring the census.
	routed := func(r rune) bool {
		if c.setCovers(set, r) {
			return true
		}
		return render.BMPPictograph(r) && sfntCovers(t, colour, r)
	}

	chain, promoted := 0, 0
	var stranded, ornaments []string
	for _, b := range symbolCorpus {
		for _, r := range b.runes {
			switch {
			case c.setCovers(set, r):
				chain++
			case routed(r):
				promoted++
			case b.colour:
				stranded = append(stranded, b.name+": U+"+upperHex(r))
			default:
				ornaments = append(ornaments, b.name+": U+"+upperHex(r))
			}
		}
	}
	if len(stranded) > 0 {
		t.Errorf("emoji-presentation symbols with no route to any face — these draw as boxes forever (F5):\n  %s",
			strings.Join(stranded, "\n  "))
	}
	t.Logf("symbol census: %d drawn by the text chain, %d promoted to the colour face", chain, promoted)
	// The ORNAMENTS are reported, not failed, and the distinction is deliberate. On a
	// bare chain (the headless fixture's is the embedded Latin face alone) ♡ ★ ☆ ← ■ ●
	// are covered by neither tier here — but on a real desktop the broad-Unicode
	// fallback tier loads a system face that has them, and this fixture never triggers
	// that load. Failing on it would be asserting a property of the TEST MACHINE.
	//
	// DEFERRED, named so it is not mistaken for coverage: the eligibility table admits
	// these ornaments by block, so on a machine whose chain also lacks them the gate
	// promotes them to the colour face, which has no glyph either — a box for a box,
	// with the last-resort census skipped on the way. Narrowing the table or gating
	// promotion on the colour face's own cmap is the fix; neither is in this change.
	if len(ornaments) > 0 {
		t.Logf("ornaments with no route on THIS machine's chain (deferred class — see the note above):\n  %s",
			strings.Join(ornaments, "\n  "))
	}
	// Anti-vacuity. Neither tier can claim a supplementary private-use codepoint, so
	// a `routed` that says yes here is answering by construction and the census above
	// is worthless whatever it printed.
	if routed(symbolCensusUnroutable) {
		t.Fatalf("the census routed U+%s (private use, no font, no block) — the predicate answers yes to everything, "+
			"which is exactly the failure this test was rewritten for", upperHex(symbolCensusUnroutable))
	}
	// And the promotion tier has to have actually been exercised: if the chain covered
	// every symbol here there would be no F5 gap on this machine and the census would
	// be measuring nothing.
	if promoted == 0 {
		t.Error("not one corpus symbol reached the colour face — the promotion tier was never exercised, so this census proves nothing about F5")
	}
}

// upperHex formats a rune as 4+ uppercase hex digits.
func upperHex(r rune) string {
	const digits = "0123456789ABCDEF"
	var b [8]byte
	n := 0
	for v := uint32(r); ; v >>= 4 {
		b[n] = digits[v&0xF]
		n++
		if v < 16 {
			break
		}
	}
	for n < 4 {
		b[n] = '0'
		n++
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = b[n-1-i]
	}
	return string(out)
}

// TestLabelEmojiAsksForTheColourFaceOnAnUncoveredSymbol is the wiring catcher. The
// callers gate their emoji face on NeedsEmojiFallback, which is blind to a bare BMP
// pictograph — so labelEmoji itself has to notice, load the face and resolve one at
// the row's own scale. Without all three the raster is built with emoji==nil and the
// character stays a box no matter how good the table is.
func TestLabelEmojiAsksForTheColourFaceOnAnUncoveredSymbol(t *testing.T) {
	body := funcBodySource(t, "emojilabel.go", "labelEmojiWeight")
	for _, fn := range []string{"uncoveredPictograph", "ensureEmojiFontLoad", "emojiFaceFor"} {
		if !containsCall(body, fn) {
			t.Errorf("labelEmojiWeight no longer calls %s — a bare ♥/✨ would never reach the colour-emoji face (F5)", fn)
		}
	}
	raster := funcBodySource(t, "emojilabel.go", "emojiRasterWeight")
	if !containsCall(raster, "uncoveredPictographMask") {
		t.Error("emojiRasterWeight no longer promotes uncovered pictographs — the face is loaded but no rune is routed to it")
	}
}
