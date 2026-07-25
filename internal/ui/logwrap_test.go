package ui

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// stripSpaces removes ALL whitespace so a wrapped-then-reassembled text can be
// compared against its original regardless of where word-wrap inserted breaks
// (at spaces) or hard-split cut mid-word: both sides collapse to the same
// non-whitespace character sequence. Only sound for single-spaced ASCII input.
func stripSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, s)
}

// TestLogHangingIndent pins the wrap-continuation presentation (playtest: a
// wrapped message read as a run of new paragraphs): rows the WRAP created
// indent by logWrapIndentPx, rows a paragraph's own newline created do not.
// IC rows key off entry adjacency, OOC rows off the cont flags built at wrap
// time. Nil fonts take the 8 px/char fallback measure, so 160 px ≈ 20 chars.
func TestLogHangingIndent(t *testing.T) {
	a := testTabApp(t)
	a.logPct = DefaultScalePct
	a.oocPct = DefaultScalePct

	// IC: one entry long enough to wrap into several rows.
	a.icLog = append(a.icLog, icEntry{text: "this message is long enough to wrap into rows"})
	a.icLogSeq++
	rows := a.icWrapped(160, false)
	if len(rows) < 2 {
		t.Fatalf("IC fixture must wrap, got %d rows", len(rows))
	}
	if got := a.logRowIndent(logSelIC, 0); got != 0 {
		t.Errorf("IC first row indent = %d, want 0", got)
	}
	if got := a.logRowIndent(logSelIC, 1); got != logWrapIndentPx {
		t.Errorf("IC continuation indent = %d, want %d", got, logWrapIndentPx)
	}

	// OOC: a wrapping paragraph THEN an explicit newline — the wrap's rows are
	// continuations, the newline's row starts a fresh unindented paragraph.
	a.oocLog = append(a.oocLog, "a long first paragraph that will wrap across rows\nsecond para")
	a.oocSpeakers = append(a.oocSpeakers, "MOTD")
	a.oocSeq++
	lines := a.oocWrapped(160)
	if len(lines) < 3 {
		t.Fatalf("OOC fixture must wrap, got %d rows", len(lines))
	}
	if len(a.oocWrapCont) != len(lines) {
		t.Fatalf("cont flags = %d, rows = %d — must stay parallel", len(a.oocWrapCont), len(lines))
	}
	if len(a.oocWrapSrc) != len(lines) {
		t.Fatalf("src indices = %d, rows = %d — must stay parallel (both append sites)", len(a.oocWrapSrc), len(lines))
	}
	if a.oocWrapCont[0] {
		t.Error("a paragraph's first row must not be a continuation")
	}
	if !a.oocWrapCont[1] {
		t.Error("a wrapped row must be a continuation")
	}
	if last := len(lines) - 1; a.oocWrapCont[last] {
		t.Error("a newline-started paragraph must not be a continuation")
	}
	if got := a.logRowIndent(logSelOOC, 1); got != logWrapIndentPx {
		t.Errorf("OOC continuation indent = %d, want %d", got, logWrapIndentPx)
	}
}

// TestOOCHoverSpan pins the link hit test on a hand-built span table (#38):
// the cursor resolves to the ONE link under it — horizontally as well as
// vertically — so plain text beside a link, a DIFFERENT link on the same row and
// the scrollbar gutter all miss. Rows 1..3 hold one hard-split link (id 0, the
// wrapped-across-rows case), row 5 holds two distinct links side by side.
func TestOOCHoverSpan(t *testing.T) {
	a := testTabApp(t)
	a.oocWrapLink = []oocLinkSpan{
		{row: 1, x0: 0, x1: 100, link: 0, url: "u1"},
		{row: 2, x0: 0, x1: 100, link: 0, url: "u1"},
		{row: 3, x0: 0, x1: 40, link: 0, url: "u1"},
		{row: 5, x0: 10, x1: 50, link: 1, url: "u2"},
		{row: 5, x0: 80, x1: 120, link: 2, url: "u3"},
	}
	// linkAt[r] = index of row r's first span; 7 display rows → 8 entries.
	a.oocWrapLinkAt = []int32{0, 0, 1, 2, 3, 3, 5, 5}
	list := sdl.Rect{X: 0, Y: 0, W: 200, H: 140}
	const lineH, wrapW = 20, 190
	c := a.ctx

	c.mouseX, c.mouseY = 10, 2*lineH+5 // row 2, inside the wrapped link
	idx, id := a.oocHoverSpan(list, 0, lineH, wrapW, 7)
	if idx != 1 || id != 0 {
		t.Fatalf("mid-link hover = (%d,%d), want (1,0)", idx, id)
	}

	c.mouseX, c.mouseY = 60, 3*lineH+5 // row 3, PAST the link's last piece
	if idx, id = a.oocHoverSpan(list, 0, lineH, wrapW, 7); idx >= 0 {
		t.Fatalf("plain text after a link must miss, got (%d,%d)", idx, id)
	}

	c.mouseX, c.mouseY = 10, 5 // row 0: no spans at all
	if idx, _ = a.oocHoverSpan(list, 0, lineH, wrapW, 7); idx >= 0 {
		t.Fatalf("an unlinked row must miss, got %d", idx)
	}

	c.mouseX, c.mouseY = wrapW+2, 2*lineH+5 // over the scrollbar gutter
	if idx, _ = a.oocHoverSpan(list, 0, lineH, wrapW, 7); idx >= 0 {
		t.Fatalf("the scrollbar gutter must miss, got %d", idx)
	}

	// Two links on ONE row: each hit-tests to itself, the gap between them to
	// neither. This is the heart of #38 ("only 1 link outta this link dump").
	c.mouseX, c.mouseY = 20, 5*lineH+5
	if idx, id = a.oocHoverSpan(list, 0, lineH, wrapW, 7); idx != 3 || id != 1 {
		t.Fatalf("first link on the row = (%d,%d), want (3,1)", idx, id)
	}
	c.mouseX = 100
	if idx, id = a.oocHoverSpan(list, 0, lineH, wrapW, 7); idx != 4 || id != 2 {
		t.Fatalf("second link on the row = (%d,%d), want (4,2) — not the first link", idx, id)
	}
	c.mouseX = 65 // the plain text between them
	if idx, _ = a.oocHoverSpan(list, 0, lineH, wrapW, 7); idx >= 0 {
		t.Fatalf("the gap between two links must miss, got %d", idx)
	}

	// Scroll offset shifts which row the cursor lands on.
	c.mouseX, c.mouseY = 20, 5 // top row on screen…
	if idx, id = a.oocHoverSpan(list, 5*lineH, lineH, wrapW, 7); idx != 3 || id != 1 {
		t.Fatalf("scrolled hover = (%d,%d), want (3,1)", idx, id)
	}
}

// TestWrapToWidthPerStringFontDrivesBreaks pins the #42 fix: wrapToWidth measures
// each candidate with the font its picker returns FOR THAT STRING, so a wrapped
// line is broken by the SAME face the log draws it in. Under a multi-face chain
// (a custom font + the embedded last resort), the old fixed-font wrap measured a
// whole paragraph in the fallback the custom font couldn't fully cover, then drew
// covered lines in the wider custom face — and they overflowed. Here a wide face
// for the marker word forces earlier breaks than an all-narrow wrap; proof the
// per-string face, not one paragraph face, drives the break.
func TestWrapToWidthPerStringFontDrivesBreaks(t *testing.T) {
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	defer ttf.Quit()
	narrow, err := loadEmbeddedFont(12)
	if err != nil {
		t.Skipf("embedded font: %v", err)
	}
	defer narrow.Close()
	wide, err := loadEmbeddedFont(48)
	if err != nil {
		t.Skipf("embedded font: %v", err)
	}
	defer wide.Close()

	const text, colW, maxLines = "aa aa aa big aa aa aa", 120, 24
	// The marker word (and any candidate containing it) measures in the WIDE face.
	pick := func(s string) *ttf.Font {
		if strings.Contains(s, "big") {
			return wide
		}
		return narrow
	}
	perString := wrapToWidth(pick, text, colW, maxLines)
	allNarrow := wrapToWidth(constFont(narrow), text, colW, maxLines)
	if len(perString) <= len(allNarrow) {
		t.Errorf("per-string wide word must force MORE breaks than an all-narrow wrap: perString=%d allNarrow=%d",
			len(perString), len(allNarrow))
	}
	// No text is lost regardless of the per-string measure.
	if got := stripSpaces(strings.Join(perString, "")); got != stripSpaces(text) {
		t.Errorf("per-string wrap lost text: %q", got)
	}
}

// TestWrapEmojiAwareBreaksEmojiNames pins the IC/OOC log wrap fix: a line with wide
// colour emoji (an emoji-laden showname) must break under the emoji-AWARE measure,
// where the plain word-wrap sizes the emoji as narrow tofu and lets the line overflow
// the column (the "text is cut, not wrapping" playtest bug). Using a larger embedded
// face as the stand-in emoji font reproduces it: assignEmoji keys on the codepoint, so
// those runes take the WIDE face's metrics exactly as a real colour-emoji face would.
func TestWrapEmojiAwareBreaksEmojiNames(t *testing.T) {
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	defer ttf.Quit()
	primary, err := loadEmbeddedFont(14)
	if err != nil {
		t.Skipf("embedded font: %v", err)
	}
	defer primary.Close()
	emoji, err := loadEmbeddedFont(56) // a much wider face stands in for a colour-emoji font
	if err != nil {
		t.Skipf("embedded font: %v", err)
	}
	defer emoji.Close()

	const width, maxLines = 200, 12
	text := "💖💙🤍💜🩷 Bwuhpi: hello there, this is a fairly long message to wrap"

	plain := wrapToWidth(constFont(primary), text, width, maxLines)       // emoji sized as narrow tofu
	aware := render.WrapEmojiAware(primary, emoji, text, width, maxLines) // emoji sized at the wide face
	if len(aware) == 0 {
		t.Fatal("emoji-aware wrap returned no lines")
	}
	if len(aware) <= len(plain) {
		t.Errorf("emoji-aware wrap = %d lines, plain = %d — expected MORE (the wide emoji force breaks)", len(aware), len(plain))
	}

	// A nil emoji face degrades to a plain single-font wrap (no panic, still wraps).
	if got := render.WrapEmojiAware(primary, nil, text, width, maxLines); len(got) == 0 {
		t.Error("emoji=nil wrap returned no lines")
	}
	// maxLines caps the output.
	if got := render.WrapEmojiAware(primary, emoji, text, width, 2); len(got) > 2 {
		t.Errorf("maxLines=2 produced %d lines", len(got))
	}
}

// TestOOCWrapLongHelpNoLoss pins the /help fix: a long single-paragraph entry
// (a server's /help dump) at a narrow column wraps to well past the old 24-row
// per-paragraph clamp WITHOUT losing text — the pre-fix code silently dropped
// everything past 24 display rows. Reassembling the wrapped rows and comparing
// whitespace-stripped proves nothing was cut. Nil fonts take the 8 px/char
// fallback, so a 160 px column holds ~20 chars; ~3000 chars → ~150 rows.
func TestOOCWrapLongHelpNoLoss(t *testing.T) {
	a := testTabApp(t)
	a.oocPct = DefaultScalePct

	// A single paragraph (no '\n'): short single-spaced ASCII words so the wrap
	// breaks at spaces, never mid-word, and no word exceeds the column.
	var b strings.Builder
	for b.Len() < 3000 {
		b.WriteString("alpha bravo charlie ") // 20 chars/group, all fit a 160 px column
	}
	help := strings.TrimSpace(b.String())
	a.oocLog = append(a.oocLog, help)
	a.oocSpeakers = append(a.oocSpeakers, "Server")
	a.oocSeq++

	rows := a.oocWrapped(160)
	if len(rows) <= oocWrapMaxLinesPerEntryOld {
		t.Fatalf("fixture must exceed the old %d-row clamp, got %d rows",
			oocWrapMaxLinesPerEntryOld, len(rows))
	}
	// Parallel slices stay in lockstep at the new budget (the link index carries
	// one extra entry by construction — it holds each row's span START).
	if len(a.oocWrapName) != len(rows) || len(a.oocWrapCont) != len(rows) ||
		len(a.oocWrapSrc) != len(rows) || len(a.oocWrapLinkAt) != len(rows)+1 {
		t.Fatalf("parallel slices desynced: out=%d name=%d cont=%d src=%d linkAt=%d",
			len(rows), len(a.oocWrapName), len(a.oocWrapCont), len(a.oocWrapSrc), len(a.oocWrapLinkAt))
	}
	if got, want := stripSpaces(strings.Join(rows, "")), stripSpaces(help); got != want {
		t.Fatalf("text lost in wrap: reassembled %d chars, original %d", len(got), len(want))
	}
	// A no-loss legitimate wrap must NOT emit the "…" marker row.
	for _, ln := range rows {
		if ln == "…" {
			t.Fatal("legitimate long /help must not trip the truncation marker")
		}
	}
}

// oocWrapMaxLinesPerEntryOld is the pre-fix per-paragraph clamp; the fix must
// wrap well past it without loss (guards against a silent regression back to it).
const oocWrapMaxLinesPerEntryOld = 24

// TestPushOOCStoreCapIntact pins the store-time guard: an entry longer than
// oocLineCap is still "…"-capped at pushOOC (a hostile server can't store an
// unbounded line), now at the larger 16 KiB bound. ASCII fixture so the byte
// slice can't split a multibyte rune, and plain prose so it isn't mistaken for
// an area list (which the /getarea paths would swallow before storing).
func TestPushOOCStoreCapIntact(t *testing.T) {
	a := testTabApp(t)
	huge := strings.Repeat("x", oocLineCap+500) // comfortably over the cap
	a.pushOOC(huge, "")
	if len(a.oocLog) != 1 {
		t.Fatalf("expected one stored entry, got %d", len(a.oocLog))
	}
	stored := a.oocLog[0]
	if !strings.HasSuffix(stored, "…") {
		t.Fatal("over-cap entry must be marked with a trailing …")
	}
	if len(stored) > oocLineCap+len("…") {
		t.Fatalf("stored entry %d bytes exceeds oocLineCap+… (%d)", len(stored), oocLineCap+len("…"))
	}
	// A legitimately long /help UNDER the cap is stored whole (not clipped).
	a2 := testTabApp(t)
	fits := strings.Repeat("y", oocLineCap-1)
	a2.pushOOC(fits, "")
	if len(a2.oocLog) != 1 || a2.oocLog[0] != fits {
		t.Fatal("an under-cap entry must be stored verbatim")
	}
}

// TestOOCWrapPathologicalMarker pins the marker path: only an ABSURD synthetic
// width — one so narrow it forces fewer than oocWrapMinCharsPerRow chars per row
// — can exhaust an entry's whole row budget, and when it does the wrap appends a
// visible "…" row instead of silently dropping text. A single space-free word
// longer than the budget hard-splits into more rows than the budget at width 8
// px (≈ 1 char/row under the fallback metric).
func TestOOCWrapPathologicalMarker(t *testing.T) {
	a := testTabApp(t)
	a.oocPct = DefaultScalePct

	// No spaces → the wrap hard-splits it; longer than the budget so it overflows.
	pathological := strings.Repeat("Z", oocWrapMaxLinesPerEntry+50)
	a.oocLog = append(a.oocLog, pathological)
	a.oocSpeakers = append(a.oocSpeakers, "")
	a.oocSeq++

	rows := a.oocWrapped(8) // 8 px ≈ 1 char/row, well under oocWrapMinCharsPerRow
	if len(rows) == 0 {
		t.Fatal("pathological entry produced no rows")
	}
	if rows[len(rows)-1] != "…" {
		t.Fatalf("budget exhaustion must append a … marker, last row = %q", rows[len(rows)-1])
	}
	// The budget bounds the row count (marker included) — it never balloons.
	if len(rows) > oocWrapMaxLinesPerEntry+1 {
		t.Fatalf("row count %d exceeds budget+marker (%d)", len(rows), oocWrapMaxLinesPerEntry+1)
	}
	// Parallel slices include the marker row.
	if len(a.oocWrapName) != len(rows) || len(a.oocWrapCont) != len(rows) ||
		len(a.oocWrapSrc) != len(rows) || len(a.oocWrapLinkAt) != len(rows)+1 {
		t.Fatalf("marker desynced parallel slices: out=%d name=%d cont=%d src=%d linkAt=%d",
			len(rows), len(a.oocWrapName), len(a.oocWrapCont), len(a.oocWrapSrc), len(a.oocWrapLinkAt))
	}
	// A NORMAL width never trips the marker for the same content.
	a.oocSeq++ // force a cache rebuild at the new width
	wide := a.oocWrapped(4000)
	for _, ln := range wide {
		if ln == "…" {
			t.Fatal("a normal width must not trip the marker")
		}
	}
}

// TestLogWrapCustomFontRowsFitDrawnWidth reproduces #42 as reported (a theme's
// own .ttf applied to the OOC log, an emoji-laden MOTD) and pins the invariant
// that closes it: EVERY wrapped row of both logs, measured in the face(s) that
// will actually DRAW it, fits the column.
//
// The setup is the real one, not a synthetic pick: installing a font chain makes
// the log set MULTI-face (the chain face + the embedded last resort), which is
// what takes pickSet off its len==1 fast path — without a custom font there is no
// divergence to have. The emoji then split the picks apart: no face covers a
// colour emoji, so the whole PARAGRAPH picks the last-resort face, while its
// plain rows pick the wide chain face. The old emoji wrap measured every row in
// the paragraph's face and the rows overflowed by the ratio between the two
// (OpenDyslexic is ~70% wider than the embedded face at the same size) — exactly
// the screenshot on the issue.
func TestLogWrapCustomFontRowsFitDrawnWidth(t *testing.T) {
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	defer ttf.Quit()
	base, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Skipf("embedded font: %v", err)
	}
	a := testTabApp(t)
	c := &Ctx{font: base, textCache: map[textKey]cachedText{}, widthCache: map[string]int32{}}
	a.ctx = c
	a.oocPct, a.logPct = DefaultScalePct, DefaultScalePct
	c.SetFontChain([]string{dyslexiaFontName}, [][]byte{openDyslexicOTF}) // the "theme font" stand-in
	c.SetEmojiFont(twemojiTTF)                                            // colour emoji, as a real session has

	// Preconditions — assert them, don't assume: a single-face set can't diverge
	// (pickSet's len==1 fast path), and neither can two faces of equal width.
	faces := c.fontsFor(&c.logSet, a.oocPct)
	if len(faces) < 2 {
		t.Fatalf("fixture must build a MULTI-face log set, got %d face(s)", len(faces))
	}
	const motd = "\U0001F49B Welcome to Killing Fever Online, or KFO for short. This server is responsible " +
		"for the KFO-Server codebase, used by Vanilla and everyone else."
	plain := "Welcome to Killing Fever Online, or KFO for short."
	if fontWidth(faces[0], plain) == fontWidth(faces[len(faces)-1], plain) {
		t.Fatal("fixture must use faces of DIFFERENT width, else the desync is invisible")
	}
	if c.EmojiFont(a.oocPct) == nil {
		t.Fatal("fixture must load a colour-emoji face, else the emoji wrap branch never runs")
	}
	if c.LogFontFor(a.oocPct, motd) == c.LogFontFor(a.oocPct, plain) {
		t.Fatal("fixture must make the paragraph pick a DIFFERENT face than a plain row")
	}

	// The column the OOC list wraps against (its own indent already removed).
	const colW = 240
	a.oocLog = append(a.oocLog, motd)
	a.oocSpeakers = append(a.oocSpeakers, "MOTD")
	a.oocSeq++
	rows := a.oocWrapped(colW)
	if len(rows) < 3 {
		t.Fatalf("MOTD must wrap into several rows, got %d", len(rows))
	}
	for i, row := range rows {
		if len([]rune(row)) <= 1 {
			continue // a lone rune wider than the column can't be split any further
		}
		if w := a.logDrawnWidth(elemServerChatlog, a.oocPct, row); w > colW {
			t.Errorf("OOC row %d %q draws %d px wide, column is %d — it will overflow", i, row, w, colW)
		}
	}
	if got, want := stripSpaces(strings.Join(rows, "")), stripSpaces(motd); got != want {
		t.Errorf("OOC wrap lost text:\n got %q\nwant %q", got, want)
	}

	// The IC log wraps through the same measure — pin it too (same entry, so the
	// same paragraph-vs-row pick split).
	a.icLog = append(a.icLog, icEntry{text: motd})
	a.icLogSeq++
	icRows := a.icWrapped(colW, false)
	if len(icRows) < 3 {
		t.Fatalf("IC entry must wrap into several rows, got %d", len(icRows))
	}
	for i, row := range icRows {
		if len([]rune(row.text)) <= 1 {
			continue
		}
		if w := a.logDrawnWidth(elemICChatlog, a.logPct, row.text); w > colW {
			t.Errorf("IC row %d %q draws %d px wide, column is %d — it will overflow", i, row.text, w, colW)
		}
	}
}
