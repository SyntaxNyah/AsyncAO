package ui

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// The #38 suite: "clickable OOC links highlight the entire message rather than
// just the link" / "only 1 link outta this link dump being clickable".
//
// All of it runs headless, where a nil font measures wrapFallbackCharPx per
// BYTE — so every expected pixel offset below is just (byte offset × 8), which
// is also exactly how the wrap chose its break points.

// spanText is the drawn text a span covers — the assertion that matters most,
// since a span's whole job is to point at the characters the user sees.
func spanText(a *App, s oocLinkSpan) string {
	if int(s.row) >= len(a.oocWrap) {
		return ""
	}
	row := a.oocWrap[s.row]
	if s.lo < 0 || s.hi > int32(len(row)) || s.lo > s.hi {
		return ""
	}
	return row[s.lo:s.hi]
}

// TestOOCLinkSpansMultiplePerParagraph pins the core of #38: EVERY link on a
// line gets its own span — with its own url, its own id, and a pixel range that
// covers only its characters — instead of one url for the whole paragraph.
func TestOOCLinkSpansMultiplePerParagraph(t *testing.T) {
	a := testTabApp(t)
	a.oocPct = DefaultScalePct
	const u1 = "https://aaa.example.com/one"
	const u2 = "www.bbb.example.org"
	const u3 = "http://ccc.example.net/three"
	line := "Links: " + u1 + " and " + u2 + " plus " + u3 + "."
	a.pushOOC(line, "")

	rows := a.oocWrapped(int32(len(line)+8) * wrapFallbackCharPx) // wide enough for one row
	if len(rows) != 1 {
		t.Fatalf("fixture must stay on one row, got %d: %v", len(rows), rows)
	}
	spans := a.oocRowLinks(0)
	if len(spans) != 3 {
		t.Fatalf("got %d spans, want 3 (one per link) — %v", len(spans), spans)
	}
	for i, want := range []string{u1, u2, u3} {
		if spans[i].url != want {
			t.Errorf("span %d opens %q, want %q", i, spans[i].url, want)
		}
		// The span must cover exactly the link text — not the "plus " before it
		// and not the sentence '.' after the last one (urlTokenRange trims it).
		if got := spanText(a, spans[i]); got != want {
			t.Errorf("span %d covers %q, want %q", i, got, want)
		}
		// Pixel range agrees with the byte range under the headless metric.
		if wx0, wx1 := spans[i].lo*wrapFallbackCharPx, spans[i].hi*wrapFallbackCharPx; spans[i].x0 != wx0 || spans[i].x1 != wx1 {
			t.Errorf("span %d px = [%d,%d), want [%d,%d)", i, spans[i].x0, spans[i].x1, wx0, wx1)
		}
	}
	// Distinct links, and no overlap between neighbours (so a hit test can only
	// ever resolve to one of them).
	for i := 1; i < len(spans); i++ {
		if spans[i].link == spans[i-1].link {
			t.Errorf("spans %d and %d share link id %d — distinct links must be distinct", i-1, i, spans[i].link)
		}
		if spans[i].x0 < spans[i-1].x1 {
			t.Errorf("span %d starts at %d, inside span %d ending at %d", i, spans[i].x0, i-1, spans[i-1].x1)
		}
	}

	// Hit-testing INSIDE link 2 must return link 2 — the reporter's actual
	// complaint was that only the first link ever answered.
	list := sdl.Rect{X: 0, Y: 0, W: 4000, H: 100}
	const lineH, wrapW = 20, 3990
	c := a.ctx
	for i, want := range []string{u1, u2, u3} {
		c.mouseX, c.mouseY = (spans[i].x0+spans[i].x1)/2, 5
		idx, id := a.oocHoverSpan(list, 0, lineH, wrapW, len(rows))
		if idx < 0 {
			t.Fatalf("mid-link hover on link %d missed entirely", i)
		}
		if got := a.oocWrapLink[idx]; got.url != want || id != spans[i].link {
			t.Errorf("hover inside link %d resolved to %q (id %d), want %q (id %d)",
				i, got.url, id, want, spans[i].link)
		}
	}
	// The plain word between two links resolves to NOTHING (it used to tint and
	// click as part of the message-wide link).
	c.mouseX = spans[0].x1 + (spans[1].x0-spans[0].x1)/2
	if idx, _ := a.oocHoverSpan(list, 0, lineH, wrapW, len(rows)); idx >= 0 {
		t.Errorf("plain text between links resolved to span %d (%q); it must miss",
			idx, a.oocWrapLink[idx].url)
	}
	// So does the trailing sentence period after the last link.
	c.mouseX = spans[2].x1 + wrapFallbackCharPx/2
	if idx, _ := a.oocHoverSpan(list, 0, lineH, wrapW, len(rows)); idx >= 0 {
		t.Errorf("the trailing '.' resolved to span %d; the trim must exclude it", idx)
	}
}

// TestOOCLinkSpansNoURL pins the empty case: an ordinary message contributes no
// spans at all, so the draw keeps its single-pass fast path and nothing tints.
func TestOOCLinkSpansNoURL(t *testing.T) {
	a := testTabApp(t)
	a.oocPct = DefaultScalePct
	a.pushOOC("Nyah: no links here, just a colon: and a dot.", "Nyah")

	rows := a.oocWrapped(2000)
	if len(a.oocWrapLink) != 0 {
		t.Fatalf("a link-free message produced %d spans: %v", len(a.oocWrapLink), a.oocWrapLink)
	}
	if len(a.oocWrapLinkAt) != len(rows)+1 {
		t.Fatalf("link index = %d entries, want rows+1 = %d", len(a.oocWrapLinkAt), len(rows)+1)
	}
	for li := range rows {
		if s := a.oocRowLinks(li); len(s) != 0 {
			t.Errorf("row %d reports spans %v", li, s)
		}
	}
}

// TestOOCLinkSpansWrapContiguous pins that a URL the wrap HARD-SPLIT still
// behaves as ONE link: consecutive rows, one span each, all sharing a link id
// and the whole URL. That contiguity is what the earlier "highlight the whole
// wrapped link" fix bought, and #38 had to keep it while narrowing the tint.
func TestOOCLinkSpansWrapContiguous(t *testing.T) {
	a := testTabApp(t)
	a.oocPct = DefaultScalePct
	const url = "https://example.com/a/very/long/path/that/cannot/fit/one/row/at/all.zip"
	a.pushOOC("see "+url+" ok", "")

	rows := a.oocWrapped(20 * wrapFallbackCharPx) // ~20 chars/row → the URL must split
	if len(rows) < 3 {
		t.Fatalf("fixture must wrap, got %d rows: %v", len(rows), rows)
	}
	spans := a.oocWrapLink
	if len(spans) < 2 {
		t.Fatalf("a hard-split link must leave one span per row, got %d", len(spans))
	}
	var rebuilt strings.Builder
	for i, s := range spans {
		if s.url != url {
			t.Errorf("piece %d opens %q, want the whole %q", i, s.url, url)
		}
		if s.link != spans[0].link {
			t.Errorf("piece %d has link id %d, want %d — the pieces must tint together", i, s.link, spans[0].link)
		}
		if i > 0 && s.row != spans[i-1].row+1 {
			t.Errorf("piece %d sits on row %d, want %d — pieces must be on consecutive rows", i, s.row, spans[i-1].row+1)
		}
		rebuilt.WriteString(spanText(a, s))
	}
	// The pieces reassemble into the exact link: no byte of it is left plain,
	// and no neighbouring text is swept in.
	if rebuilt.String() != url {
		t.Errorf("pieces reassemble to %q, want %q", rebuilt.String(), url)
	}
	// The plain words around it stay untinted: the wrap pushes a word wider than
	// the column onto its own rows, so "see" keeps row 0 to itself — and row 0
	// carries NO span at all.
	if rows[0] != "see" {
		t.Fatalf("row 0 = %q, want the lone word before the link", rows[0])
	}
	if s := a.oocRowLinks(0); len(s) != 0 {
		t.Errorf("the plain leading row carries spans %v; it must carry none", s)
	}
	// The last piece ends before the trailing " ok" that shares its row.
	last := spans[len(spans)-1]
	if tail := a.oocWrap[last.row][last.hi:]; tail != " ok" {
		t.Errorf("text after the final piece = %q, want %q (the link must stop at its own end)", tail, " ok")
	}
}

// TestOOCLinkSpansStreamerMasked pins the subtle one: streamer mode redacts the
// sender prefix ("Nyah: " → "???: ") only on the DISPLAYED text, so a span built
// against the raw entry would sit one byte to the right of the link the user can
// see. Positions must come from the masked text while the URL still opens the
// real thing.
func TestOOCLinkSpansStreamerMasked(t *testing.T) {
	a := testTabApp(t)
	a.oocPct = DefaultScalePct
	const url = "https://example.com/x"
	a.d.Prefs.SetStreamerMode(true)
	a.pushOOC("Nyah: grab "+url+" now", "Nyah")

	rows := a.oocWrapped(2000)
	if len(rows) != 1 {
		t.Fatalf("fixture must stay on one row, got %v", rows)
	}
	if !strings.HasPrefix(rows[0], "???: ") {
		t.Fatalf("streamer mode must mask the sender, row = %q", rows[0])
	}
	spans := a.oocRowLinks(0)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	// Aligned to the DRAWN text (the whole point), and still opening the real link.
	if got := spanText(a, spans[0]); got != url {
		t.Errorf("span covers %q of the masked row, want %q", got, url)
	}
	if spans[0].url != url {
		t.Errorf("span opens %q, want the unmasked %q", spans[0].url, url)
	}
	if want := int32(strings.Index(rows[0], url)) * wrapFallbackCharPx; spans[0].x0 != want {
		t.Errorf("span x0 = %d, want %d (masked offset, not the raw one)", spans[0].x0, want)
	}
}

// TestOOCRowLinksNoAlloc pins the per-frame cost: the draw loop asks every
// visible row for its spans and hit-tests the cursor once, both on the render
// path — they must not touch the heap (hard rule: zero-allocation render loop).
func TestOOCRowLinksNoAlloc(t *testing.T) {
	a := testTabApp(t)
	a.oocPct = DefaultScalePct
	a.pushOOC("Nyah: see https://example.com/one and https://example.com/two", "Nyah")
	rows := a.oocWrapped(2000)
	if len(a.oocWrapLink) != 2 {
		t.Fatalf("fixture must produce 2 spans, got %d", len(a.oocWrapLink))
	}
	list := sdl.Rect{X: 0, Y: 0, W: 2100, H: 100}
	a.ctx.mouseX, a.ctx.mouseY = (a.oocWrapLink[1].x0+a.oocWrapLink[1].x1)/2, 5
	if n := testing.AllocsPerRun(200, func() {
		_ = a.oocRowLinks(0)
		_, _ = a.oocHoverSpan(list, 0, 20, 2090, len(rows))
	}); n != 0 {
		t.Fatalf("row-span lookup + hit test allocate %v/op on the draw path; want 0", n)
	}
}
