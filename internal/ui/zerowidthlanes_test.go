package ui

// Gates for the zero-width sidechannel's DISPLAY boundary.
//
// AsyncAO transmits sprite styles, profiles, reactions and status as an invisible
// run of zero-width runes appended to an IC/OOC body (courtroom/spritestyle.go).
// The receiver deliberately keeps msg.Message literal so a recording replays the
// same marker, and hands the chatbox a separately-decoded clean lane
// (Courtroom.currentText). Every OTHER surface that turns a wire body into text a
// human reads needed its own copy of that strip and did not have one, so the codec
// runes reached the log — where they counted against the line cap, rode into the
// clipboard and the URL scan, and walked the row's font pick off the primary face
// so an occasional line drew in a stray family.
//
// The rule these gates hold: STRIP wherever a wire body becomes a DISPLAY string,
// KEEP it wherever it stays a wire record (demofile.go, replay.go, makerSave).
//
// The four gates are deliberately different in kind — behavioural for what the
// user sees, source-structural for the seams a unit test cannot reach (App
// methods), and one cross-package harvest that keeps the codec alphabet and the
// font predicate from drifting apart.

import (
	"go/ast"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/veandco/go-sdl2/ttf"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
)

// codecRunes harvests the sidechannel's alphabet FROM ITS OWN SOURCE: the zwStart
// sentinel and every entry of octalSyms in courtroom/spritestyle.go.
//
// Harvested rather than copied on purpose. A literal list here would be a mirror —
// it would keep passing while the codec grew a tenth rune the font predicate has
// never heard of, which is exactly the shape of the bug these gates exist for. It
// fails loudly if either declaration is renamed or restructured, so the harvest can
// never silently return nothing and pass vacuously.
func codecRunes(t *testing.T) []rune {
	t.Helper()
	const src = "../courtroom/spritestyle.go"
	_, f := parsedFile(t, src)

	var out []rune
	lit := func(e ast.Expr) (rune, bool) {
		bl, ok := e.(*ast.BasicLit)
		if !ok {
			return 0, false
		}
		v, err := strconv.ParseInt(bl.Value, 0, 32) // base 0: honours the 0x form the codec is written in
		if err != nil {
			return 0, false
		}
		return rune(v), true
	}
	sentinel := false
	syms := 0
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				switch name.Name {
				case "zwStart":
					r, ok := lit(vs.Values[i])
					if !ok {
						t.Fatalf("%s: zwStart is no longer a plain code-point literal — this harvest reads nothing", src)
					}
					out = append(out, r)
					sentinel = true
				case "octalSyms":
					cl, ok := vs.Values[i].(*ast.CompositeLit)
					if !ok {
						t.Fatalf("%s: octalSyms is no longer a composite literal — this harvest reads nothing", src)
					}
					for _, e := range cl.Elts {
						r, ok := lit(e)
						if !ok {
							t.Fatalf("%s: octalSyms holds a non-literal element — this harvest reads nothing", src)
						}
						out = append(out, r)
						syms++
					}
				}
			}
		}
	}
	if !sentinel {
		t.Fatalf("%s: no zwStart declaration found — it was renamed and this gate now checks nothing", src)
	}
	if syms != 8 {
		t.Fatalf("%s: harvested %d octalSyms entries, want the codec's 8 — the harvest is out of step with the codec", src, syms)
	}
	return out
}

// encoderRunes is codecRunes' BEHAVIOURAL twin: the union of every rune the real
// encoder actually emits, gathered by running courtroom.SpriteStyle.EncodeMarker over a
// spread of field values.
//
// It exists because the AST harvest has a blind spot it cannot see past. codecRunes
// matches the declarations named zwStart and octalSyms; add a SECOND sentinel for a new
// frame type — say zwStartV2 = 0x2065 — and the harvest still finds 1 sentinel and 8
// symbols, every gate stays green, and U+2065 is genuinely NOT in isInvisibleRune
// (ui.go covers 0x2060-0x2064 and 0x2066-0x2069; 0x2065 is the hole between them). The
// original bug would return under a green suite. Only running the encoder sees it.
//
// The payload packs THREE bits per rune, so the spread has to vary the low-order colour
// and percent bytes — flags alone never exercise all eight octal digits.
func encoderRunes(t *testing.T) map[rune]bool {
	t.Helper()
	seen := map[rune]bool{}
	for v := 0; v < 256; v++ {
		for _, s := range []courtroom.SpriteStyle{
			{Tint: true, R: uint8(v), G: uint8(255 - v), B: uint8(v * 3), Opacity: uint8(v), Rotation: uint8(v)},
			{Glow: true, Grayscale: true, Brightness: uint8(v), Scale: uint8(v), Restyle: uint8(v % 8)},
			{Outline: true, OutlineR: uint8(v), OutlineG: uint8(255 - v), Glitch: true, GlitchMode: uint8(v % 4)},
		} {
			for _, r := range s.EncodeMarker() {
				seen[r] = true
			}
		}
	}
	if len(seen) < 9 {
		t.Fatalf("the spread produced only %d distinct codec runes — it does not exercise the whole alphabet", len(seen))
	}
	return seen
}

// containsAnyRune returns the first harvested codec rune present in s, or -1.
func containsAnyRune(s string, runes []rune) rune {
	for _, r := range runes {
		if strings.ContainsRune(s, r) {
			return r
		}
	}
	return -1
}

// TestDisplayLanesStripTheZeroWidthSidechannel drives the REAL encoder and the REAL
// display formatters: a style marker built by courtroom.SpriteStyle.EncodeMarker is
// appended to an ordinary body, and every line a human reads must come back with the
// visible text byte-for-byte and not one codec rune.
//
// This is the behavioural half. It cannot reach the App methods (pushOOC, pmAppend,
// routeBackgroundEvent), which is what TestEveryDisplayLaneStripsTheSidechannel is for.
func TestDisplayLanesStripTheZeroWidthSidechannel(t *testing.T) {
	runes := codecRunes(t)

	// A real, Active style — the send-on-change encoder returns "" for an inactive one,
	// so an inactive style would make this whole test pass vacuously.
	style := courtroom.SpriteStyle{Tint: true, R: 200, G: 40, B: 40, Opacity: 60, Rotation: 12}
	marker := style.EncodeMarker()
	if marker == "" {
		t.Fatal("EncodeMarker returned nothing for an active style — the fixture carries no sidechannel")
	}
	if containsAnyRune(marker, runes) < 0 {
		t.Fatal("the encoded marker holds none of the harvested codec runes — the harvest and the encoder disagree")
	}

	// Deliberately awkward visible text: an ampersand (the wire's escaped character), a
	// URL (the log's link scan reads the stripped line) and non-ASCII, so "survives
	// byte-for-byte" means something.
	const body = "Objection & proof — see https://example.test/x ⵜ"
	m := &protocol.ChatMessage{
		CharName: "Phoenix",
		Showname: "Nick",
		Message:  body + marker,
	}

	// icLogLineDisplay is the formatter the LIVE IC log actually calls (app.go, the
	// EventMessage branch), and its friend-nick arm hand-builds the line instead of
	// delegating to icLogLine — so it is a second, separate place the strip has to
	// happen and the only one the running client uses. Both of its arms are driven.
	nickLine, nickSpeaker := icLogLineDisplay(m, false, "Ace", nil)
	plainLine, plainSpeaker := icLogLineDisplay(m, false, "", nil)
	if nickSpeaker != "Nick" || plainSpeaker != "Nick" {
		t.Errorf("icLogLineDisplay speaker = %q / %q, want %q both — the speaker field is the real identity, not the nickname", nickSpeaker, plainSpeaker, "Nick")
	}

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"icMessageBody", icMessageBody(m), body},
		{"icLogLine", icLogLine(m, false, nil), "Nick: " + body},
		{"icLogLineDisplay (nick arm)", nickLine, "Ace (Nick): " + body},
		{"icLogLineDisplay (plain arm)", plainLine, "Nick: " + body},
		// capLogLine is the chokepoint every STORED icEntry.text passes through
		// (pushIC for the active tab, routeBackgroundEvent for a parked one). Driven
		// with a line that was never formatted, which is what an unremembered future
		// write site looks like.
		{"capLogLine", capLogLine("Nick: "+body+marker, "Nick"), "Nick: " + body},
		{"detailedLogLine", detailedLogLine(time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), m), "[2026-09-08 12:00:00] Nick (Phoenix): " + body},
	}
	for _, tc := range cases {
		if r := containsAnyRune(tc.got, runes); r >= 0 {
			t.Errorf("%s: U+%04X survived into a display string: %q", tc.name, r, tc.got)
		}
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	// A message with no marker must come back untouched — the strip is a no-op on the
	// overwhelmingly common line, not a reformatter.
	plain := &protocol.ChatMessage{CharName: "Phoenix", Showname: "Nick", Message: body}
	if got := icMessageBody(plain); got != body {
		t.Errorf("icMessageBody mangled an unmarked message: %q, want %q", got, body)
	}
}

// TestEveryDisplayLaneStripsTheSidechannel is the encapsulation gate: it names the
// seams that turn a wire body into display text and requires each of them to call
// StripSpriteStyle, then goes LOOKING for a seam nobody remembered.
//
// Source-structural because three of the six are App methods that mutate render-thread
// state (pushOOC appends to the ACTIVE tab's log, routeBackgroundEvent to a parked
// tab's, pmAppend to a DM thread) and cannot be driven from a unit test without
// standing up an App. funcBodySource fails the test when a function is renamed, so a
// rename breaks the gate loudly instead of quietly making it check nothing.
func TestEveryDisplayLaneStripsTheSidechannel(t *testing.T) {
	// callee differs on purpose. A lane that composes "<name>: <body>" must strip only
	// the BODY (stripDisplayTail), because StripSpriteStyle is gated on the U+2060
	// sentinel but then deletes the whole nine-rune alphabet — so on a composed line one
	// speaker's body marker authorises deleting U+200B/200C/200D out of another player's
	// NAME. A lane handed a bare body calls StripSpriteStyle directly.
	lanes := []struct {
		file, fn, callee, why string
	}{
		{"app.go", "icMessageBody", "StripSpriteStyle", "the IC log's body for the active tab"},
		{"app.go", "capLogLine", "stripDisplayTail", "the chokepoint every STORED icEntry.text passes through"},
		{"app.go", "pushOOC", "stripDisplayTail", "the OOC panel, which AsyncAO's own DMs ride on"},
		{"tabs.go", "routeBackgroundEvent", "stripDisplayTail", "a PARKED tab's OOC log, which does not go through pushOOC"},
		{"translog.go", "detailedLogLine", "StripSpriteStyle", "the on-disk transcript, which is read by a human and by the log browser"},
		{"friendstab.go", "pmAppend", "StripSpriteStyle", "a DM thread, the one channel the codec rides by design"},
		{"scenemaker.go", "eventSummary", "StripSpriteStyle", "the scene maker's event list"},
		{"logbrowser.go", "readLogScope", "StripSpriteStyle", "the log browser, whose input is HISTORY and so still holds runes written by older builds"},
		{"subtitles.go", "subtitleLine", "StripSpriteStyle", "the exported .srt/.vtt, which strips the body and keeps Showname — the precedent the rest follow"},
	}
	for _, l := range lanes {
		body := funcBodySource(t, l.file, l.fn)
		if !containsCall(body, l.callee) {
			t.Errorf("%s:%s no longer calls %s — %s would show the invisible codec runes again", l.file, l.fn, l.callee, l.why)
		}
	}

	// The KEEP side of the rule, and the open–closed evidence for it. These are WIRE
	// RECORDS, not display: a recording has to replay the same marker it received, so
	// stripping here would silently delete every transmitted sprite style from every
	// saved scene — a failure nobody would see until they played a recording back.
	// Stated as a gate because "do not add the strip here" is otherwise only a comment,
	// and the strip roster above is a standing invitation to add one more.
	keeps := []struct {
		file, fn, why string
	}{
		{"replay.go", "recEventFrom", "the recorder, which deliberately APPENDS EncodeMarker so a clip is self-contained"},
		{"demofile.go", "recordingToDemo", "the .demo writer — an AO wire record other clients read"},
		{"demofile.go", "demoToRecording", "the .demo reader; a marker in an imported file is wire data, not display text"},
	}
	for _, k := range keeps {
		body := funcBodySource(t, k.file, k.fn)
		for _, banned := range []string{"StripSpriteStyle", "stripDisplayTail"} {
			if containsCall(body, banned) {
				t.Errorf("%s:%s calls %s — it must NOT: %s", k.file, k.fn, banned, k.why)
			}
		}
	}

	// The discovery half. The roster above is what we remembered; a NEW IC log write
	// site is precisely what has not been. Every icEntry composite literal in the
	// package must build its text field THROUGH one of the sanctioned formatters.
	//
	// A POSITIVE requirement, deliberately, after the negative form ("contains no raw
	// .Message read") turned out to be wrong in both directions. It passed VACUOUSLY on
	// app.go's pushIC — `text: capLogLine(line)`, the whole IC log's real write site,
	// whose wire read happens one stack frame up in the EventMessage branch — and it
	// fired FALSELY on any expression reading a non-text field off the event pointer,
	// because courtroom.Event.Message is the *ChatMessage while ChatMessage.Message is
	// the string, and an AST name match cannot tell those two apart. Requiring the value
	// to pass through a strip-bearing formatter cannot be satisfied by moving the read
	// out of sight, and cannot misfire on CharName.
	sanctioned := []string{"capLogLine", "icLogLine", "icLogLineDisplay", "icMessageBody", "StripSpriteStyle", "stripDisplayTail"}
	textIdx := icEntryTextIndex(t)
	sites := 0
	packageFuncs(t, func(file, fn string, fnBody *ast.BlockStmt) {
		for _, cl := range icEntryLits(fnBody, "icEntry") {
			for i, elt := range cl.Elts {
				val := elt
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "text" {
						continue
					}
					val = kv.Value
				} else if i != textIdx {
					continue // positional literal: only the text slot is ours
				}
				sites++
				if !routedThrough(fnBody, val, sanctioned) {
					t.Errorf("%s:%s builds icEntry.text without routing it through one of %v — the zero-width sidechannel would survive into the log", file, fn, sanctioned)
				}
			}
		}
	})
	if sites < 2 {
		t.Fatalf("found %d icEntry text sites, want at least the two known writes (pushIC, routeBackgroundEvent) — the discovery half is passing vacuously", sites)
	}
}

// routedThrough reports that val reaches its icEntry.text slot via one of the named
// formatters, accepting the two idioms this package actually uses:
//
//	INLINE  text: capLogLine(icLogLine(...), speaker)   — tabs.go's parked-tab write
//	REBIND  line = capLogLine(line, speaker) … text: line — app.go's pushIC
//
// The rebind form exists because pushIC has to describe the STORED text afterwards
// (extractURLs reads it), so the call cannot sit inside the literal. Refusing to
// understand that would push the code back into the shape that shipped the bug: a url
// scanned off the unstripped original.
func routedThrough(fnBody *ast.BlockStmt, val ast.Expr, sanctioned []string) bool {
	for _, name := range sanctioned {
		if len(callsNamed(val, name)) > 0 {
			return true
		}
	}
	id, ok := val.(*ast.Ident)
	if !ok {
		return false
	}
	found := false
	ast.Inspect(fnBody, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			lid, ok := lhs.(*ast.Ident)
			if !ok || lid.Name != id.Name || i >= len(as.Rhs) {
				continue
			}
			for _, name := range sanctioned {
				if len(callsNamed(as.Rhs[i], name)) > 0 {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// icEntryLits collects every composite literal of the named struct under n, INCLUDING
// the elided-type forms. Inside []icEntry{...}, [N]icEntry{...} and
// map[K][]icEntry{k: {{...}}} Go lets the inner literal omit its type, so cl.Type is
// nil — and a gate that tests cl.Type.(*ast.Ident) sees such a write as no literal at
// all. A future site that appends a BATCH of entries is exactly that shape, and it is
// exactly what the discovery half exists to catch.
func icEntryLits(n ast.Node, want string) []*ast.CompositeLit {
	var out []*ast.CompositeLit
	var walk func(node ast.Node, typ ast.Expr)
	walk = func(node ast.Node, typ ast.Expr) {
		ast.Inspect(node, func(nd ast.Node) bool {
			cl, ok := nd.(*ast.CompositeLit)
			if !ok {
				return true
			}
			t := cl.Type
			if t == nil {
				t = typ // elided: inherit the enclosing literal's element type
			}
			if id, ok := t.(*ast.Ident); ok && id.Name == want {
				out = append(out, cl)
			}
			elem := elementType(t)
			for _, e := range cl.Elts {
				v := e
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					v = kv.Value
				}
				walk(v, elem)
			}
			return false // children walked above, carrying the elided-type context
		})
	}
	walk(n, nil)
	return out
}

// elementType is the type an UNTYPED element literal inherits from a composite literal
// of type t: the element of an array/slice, the value of a map.
func elementType(t ast.Expr) ast.Expr {
	switch x := t.(type) {
	case *ast.ArrayType:
		return x.Elt
	case *ast.MapType:
		return x.Value
	case *ast.StarExpr:
		return elementType(x.X)
	}
	return nil
}

// icEntryTextIndex is the position of `text` in the icEntry struct, read from app.go's
// own declaration. A positional literal names no field, so the gate has to know the
// slot — and reading it rather than hard-coding 0 means a reordered struct moves the
// gate with it instead of silently checking `color`.
func icEntryTextIndex(t *testing.T) int {
	t.Helper()
	_, f := parsedFile(t, "app.go")
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			ts, ok := s.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "icEntry" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				t.Fatal("app.go: icEntry is no longer a struct — this gate cannot find its text slot")
			}
			i := 0
			for _, fld := range st.Fields.List {
				for _, nm := range fld.Names {
					if nm.Name == "text" {
						return i
					}
					i++
				}
			}
		}
	}
	t.Fatal("app.go: icEntry has no text field — the discovery half now checks nothing")
	return -1
}

// TestTheStripTakesTheBodyAndNeverTheName pins the half of the rule that is easy to
// get backwards, and that the first cut of this fix DID get backwards.
//
// StripSpriteStyle is gated on the U+2060 sentinel but then deletes the whole nine-rune
// alphabet, three of which (U+200B/200C/200D) are ordinary text — a ZWJ is what holds an
// emoji cluster together. So it does not distribute over concatenation: run over a
// composed "<name>: <body>", one speaker's BODY marker supplies the gate and the
// deletion then eats codec-alphabet runes out of the NAME. Two things broke at once:
// the name came apart on screen, and the stored speaker stopped being a substring of
// the line it is stored parallel to, so Ctx.logRowSplit's strings.Index missed and the
// bold + tinted name span silently vanished for every line that player sent.
//
// The substring property is the one to assert, because it is what logRowSplit actually
// requires — of both logs, through both of their seams.
func TestTheStripTakesTheBodyAndNeverTheName(t *testing.T) {
	runes := codecRunes(t)
	// A showname holding a ZWJ emoji cluster. U+200D is in the codec alphabet, so a
	// whole-line strip breaks the flag into two glyphs.
	const name = "Nick \U0001F3F3️‍\U0001F308"
	marker := courtroom.SpriteStyle{Tint: true, R: 200, G: 40, B: 40}.EncodeMarker()
	if marker == "" {
		t.Fatal("EncodeMarker returned nothing for an active style — nothing would be under test")
	}
	if containsAnyRune(name, runes) < 0 {
		t.Fatal("the fixture name holds no codec-alphabet rune — this test would pass for the wrong reason")
	}

	const body = "hello there"
	for _, tc := range []struct {
		lane string
		got  string
	}{
		{"capLogLine", capLogLine(name+": "+body+marker, name)},
		{"stripDisplayTail", stripDisplayTail(name+": "+body+marker, name)},
	} {
		want := name + ": " + body
		if tc.got != want {
			t.Errorf("%s = %q, want %q — the speaker head must survive byte-for-byte", tc.lane, tc.got, want)
		}
		if !strings.Contains(tc.got, name) {
			t.Errorf("%s ate the speaker out of its own line: logRowSplit's strings.Index would miss and the name tint would go dead", tc.lane)
		}
		if r := containsAnyRune(strings.TrimPrefix(tc.got, name), runes); r >= 0 {
			t.Errorf("%s left U+%04X in the BODY — the marker was not stripped", tc.lane, r)
		}
	}

	// A system line has no name to protect, so the whole line is body.
	if got := capLogLine(body+marker, ""); got != body {
		t.Errorf("capLogLine(system line) = %q, want %q", got, body)
	}
}

// TestStoredLinkComesFromTheStoredText pins pushIC's ordering. extractURLs splits on
// strings.Fields and no codec rune is whitespace, so a trailing marker FUSES onto the
// last token: a message ending in a link used to store a url with invisible runes baked
// into it, and that string is what the tooltip shows and what openBrowser is handed.
func TestStoredLinkComesFromTheStoredText(t *testing.T) {
	runes := codecRunes(t)
	marker := courtroom.SpriteStyle{Tint: true, R: 9, G: 9, B: 9}.EncodeMarker()
	const url = "https://example.test/x"

	a := &App{}
	a.pushIC("Nick: see "+url+marker, 0, false, -1, "Nick")
	if len(a.icLog) != 1 {
		t.Fatalf("pushIC stored %d entries, want 1", len(a.icLog))
	}
	e := a.icLog[0]
	if e.url != url {
		t.Errorf("icEntry.url = %q, want %q — it is scanned off the line BEFORE the strip", e.url, url)
	}
	if r := containsAnyRune(e.url, runes); r >= 0 {
		t.Errorf("icEntry.url carries U+%04X — openBrowser would be handed it", r)
	}
	if !strings.Contains(e.text, url) {
		t.Errorf("icEntry.text = %q does not contain the url the entry offers", e.text)
	}
}

// TestIsInvisibleRuneCoversTheWholeZeroWidthCodec is the drift catcher behind the font
// fix, and the one gate that spans the two packages.
//
// pickFont may ignore a rune the face lacks ONLY when isInvisibleRune says it draws
// nothing. The codec picks its runes for a different reason — that they pass an
// arbitrary AO server — so the two sets are maintained independently and nothing but
// this test makes them agree. If the codec grows a ninth symbol outside
// isInvisibleRune's ranges, one style-change message silently walks a whole log row
// onto a stray font again, and no other test in the tree would notice.
func TestIsInvisibleRuneCoversTheWholeZeroWidthCodec(t *testing.T) {
	runes := codecRunes(t)
	if len(runes) != 9 { // the sentinel plus the eight data symbols
		t.Fatalf("harvested %d codec runes, want 9", len(runes))
	}

	// Declared set == emitted set. Each direction catches a different drift, which is why
	// both harvests exist: the AST side fails loudly when a declaration is renamed or the
	// count changes, and the behavioural side is the only one that notices a rune the
	// encoder emits from a declaration this harvest does not read by name.
	emitted := encoderRunes(t)
	declared := map[rune]bool{}
	for _, r := range runes {
		declared[r] = true
	}
	for r := range emitted {
		if !declared[r] {
			t.Errorf("the encoder emits U+%04X but the declared alphabet does not contain it — codecRunes is reading the wrong declarations", r)
		}
	}
	for r := range declared {
		if !emitted[r] {
			t.Errorf("U+%04X is declared but the encoder never emits it — the alphabet and the codec have drifted", r)
		}
	}

	// The predicate, over the union, so a rune found by EITHER harvest is checked.
	for r := range declared {
		emitted[r] = true
	}
	for r := range emitted {
		if !isInvisibleRune(r) {
			t.Errorf("codec rune U+%04X is not in isInvisibleRune — a message carrying it will drag the font pick off the primary face", r)
		}
	}

	// And the end the predicate is a means to: a line carrying a REAL marker still picks
	// the primary face. isInvisibleRune's coverage becomes an observed consequence here
	// rather than a restated one, which is the difference between driving the fix and
	// mirroring it.
	primary, mid, last := &ttf.Font{}, &ttf.Font{}, &ttf.Font{}
	fonts := []*ttf.Font{primary, mid, last}
	emb := parseCover(goregular.TTF)
	if emb == nil {
		t.Fatal("parseCover(goregular) returned nil — sfnt can't read the embedded font")
	}
	cover := []*sfnt.Font{emb, nil, nil}
	var buf sfnt.Buffer
	for _, s := range []courtroom.SpriteStyle{
		{Tint: true, R: 200, G: 40, B: 40, Opacity: 60, Rotation: 12},
		{Glow: true, Grayscale: true, Brightness: 40, Scale: 150},
		{Outline: true, OutlineR: 255, Glitch: true},
	} {
		marker := s.EncodeMarker()
		if marker == "" {
			t.Fatal("EncodeMarker returned nothing for an active style — nothing would be under test")
		}
		if got, _ := pickFont(fonts, cover, &buf, "abc"+marker); got != primary {
			t.Errorf("a marked line walked off the primary face — the funky-fonts bug is back for style %+v", s)
		}
	}
}

// TestPickFontIgnoresInvisibleRunesWhenChoosingAFace pins the render-side half of the
// fix directly on the production picker.
//
// The bug: coverHasAll demanded a real cmap glyph for EVERY rune, and no ordinary text
// face has one for the codec's zero-width symbols, so a single marker rune disqualified
// the primary face and threw the whole row onto whatever came later in the chain. The
// fix has to satisfy two things at once, which is why coverScan returns two values:
// the PICK ignores invisible runes, and the covered flag stays HONEST (false here), so
// the raster gate still takes the per-glyph path rather than blitting .notdef.
//
// Hermetic: pickFont only compares and returns the font pointers, never dereferences
// them, so empty sentinels stand in for real faces and this runs with no SDL at all.
func TestPickFontIgnoresInvisibleRunesWhenChoosingAFace(t *testing.T) {
	marker := courtroom.SpriteStyle{Tint: true, R: 10, G: 20, B: 30}.EncodeMarker()
	if marker == "" {
		t.Fatal("EncodeMarker returned nothing for an active style — nothing would be under test")
	}

	primary, middle, last := &ttf.Font{}, &ttf.Font{}, &ttf.Font{}
	fonts := []*ttf.Font{primary, middle, last}
	emb := parseCover(goregular.TTF)
	if emb == nil {
		t.Fatal("parseCover(goregular) returned nil — sfnt can't read the embedded font")
	}
	// Only the primary has a cover; the middle is a face nothing is known about, which is
	// what a chain looks like before the fallback tiers load.
	cover := []*sfnt.Font{emb, nil, nil}
	var buf sfnt.Buffer

	const tifinaghYath = "ⵜ" // a VISIBLE rune the embedded face genuinely lacks

	// Ground truth this test rests on, in all three parts: the embedded face covers
	// 'a', covers NONE of the codec runes, and does NOT cover the tifinagh. Every case
	// below is an assertion about one of those three, so if any stopped being true the
	// table would keep passing while testing nothing.
	if !coverHasRune(emb, &buf, 'a') {
		t.Fatal("the embedded face does not cover 'a' — the fixture is broken")
	}
	for _, r := range codecRunes(t) {
		if coverHasRune(emb, &buf, r) {
			t.Fatalf("the embedded face covers codec rune U+%04X — this test would pass for the wrong reason", r)
		}
	}
	if coverHasRune(emb, &buf, []rune(tifinaghYath)[0]) {
		t.Fatal("the embedded face covers U+2D5C — the fall-through cases below would never fall through")
	}

	cases := []struct {
		name        string
		text        string
		wantFont    *ttf.Font
		wantCovered bool
	}{
		{"plain latin", "abc", primary, true},
		{"latin plus a style marker", "abc" + marker, primary, false},
		{"a marker on its own", marker, primary, false},
		{"a genuinely uncovered visible rune still falls through", tifinaghYath, last, false},
		{"an uncovered visible rune wins over the invisible ones", "abc" + tifinaghYath + marker, last, false},
	}
	for _, tc := range cases {
		got, covered := pickFont(fonts, cover, &buf, tc.text)
		if got != tc.wantFont {
			t.Errorf("%s: pickFont chose the wrong face", tc.name)
		}
		if covered != tc.wantCovered {
			t.Errorf("%s: covered = %v, want %v — the raster gate reads this flag", tc.name, covered, tc.wantCovered)
		}
	}

	// coverScan's two answers, stated separately: the marker is invisible so the face is
	// still a valid PICK, and it is still not FULL coverage.
	if visible, all := coverScan(emb, &buf, "abc"+marker); !visible || all {
		t.Errorf("coverScan(latin+marker) = (%v,%v), want (true,false)", visible, all)
	}
	if visible, all := coverScan(emb, &buf, "abc"); !visible || !all {
		t.Errorf("coverScan(latin) = (%v,%v), want (true,true)", visible, all)
	}
	if visible, all := coverScan(emb, &buf, tifinaghYath); visible || all {
		t.Errorf("coverScan(tifinagh) = (%v,%v), want (false,false)", visible, all)
	}
	if visible, all := coverScan(nil, &buf, "abc"); visible || all {
		t.Errorf("coverScan(nil face) = (%v,%v), want (false,false)", visible, all)
	}

	// pickFont may not allocate. Production reaches it only on a pickIn memo MISS — a
	// string this client has not drawn before at this (set, scale) — so this is not a
	// per-frame cost; it is the cost of every FIRST sighting of a line, which for a
	// busy log is every line. The reused sfnt buffer is what makes zero possible.
	//
	// BOTH exits are measured. The early return (a face covers the visible runes) and
	// the fall-through (nothing does, so the last entry is handed back) walk different
	// amounts of the chain, and only the fall-through scans every cover in the set.
	marked := "abc" + marker // concatenated OUTSIDE the measured closure
	if n := testing.AllocsPerRun(50, func() { pickFont(fonts, cover, &buf, marked) }); n != 0 {
		t.Errorf("pickFont allocated %v times per call on the early-return path", n)
	}
	if n := testing.AllocsPerRun(50, func() { pickFont(fonts, cover, &buf, tifinaghYath) }); n != 0 {
		t.Errorf("pickFont allocated %v times per call on the fall-through path", n)
	}
}
