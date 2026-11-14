package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// mdTestWrapW is a column the test paragraph must wrap in.
const mdTestWrapW = 160

// TestParseInlineMarkdown pins the inline markup: ** toggles bold, backticks vanish, a link keeps its text, and the
// runs cover the cleaned text exactly.
func TestParseInlineMarkdown(t *testing.T) {
	col := sdl.Color{R: 1, G: 2, B: 3, A: 4}
	cases := []struct {
		in, want string
		bold     []bool // per run
	}{
		{"plain text", "plain text", []bool{false}},
		{"**Bold lead.** then plain", "Bold lead. then plain", []bool{true, false}},
		{"use `-debug` here", "use -debug here", []bool{false}},
		{"see [the release page](https://example.com) now", "see the release page now", []bool{false}},
		{"open **to the end", "open to the end", []bool{false, true}},
		{"a [not a link and **bold**", "a [not a link and bold", []bool{false, true}},
		{"emoji 🎥 **stays**", "emoji 🎥 stays", []bool{false, true}},
	}
	for _, tc := range cases {
		got, spans := parseInlineMarkdown(tc.in, col)
		if got != tc.want {
			t.Errorf("%q: text %q, want %q", tc.in, got, tc.want)
			continue
		}
		total := 0
		bold := make([]bool, 0, len(spans))
		for _, sp := range spans {
			total += sp.Len
			bold = append(bold, sp.Bold)
			if sp.Color != col {
				t.Errorf("%q: run colour %v, want %v", tc.in, sp.Color, col)
			}
		}
		if total != utf8.RuneCountInString(got) {
			t.Errorf("%q: runs cover %d runes, text has %d", tc.in, total, utf8.RuneCountInString(got))
		}
		if len(bold) != len(tc.bold) {
			t.Errorf("%q: %d runs %v, want %v", tc.in, len(bold), bold, tc.bold)
			continue
		}
		for i := range bold {
			if bold[i] != tc.bold[i] {
				t.Errorf("%q: run %d bold %v, want %v", tc.in, i, bold[i], tc.bold[i])
			}
		}
	}
}

// TestParseMarkdownBlocks pins the block split: headings by level without markup, bullets, a hard-wrapped bullet and
// paragraph joined into one block each, and the blank line before a block remembered.
func TestParseMarkdownBlocks(t *testing.T) {
	doc := "# Title\n\n## v1 - **date**\n- **Lead.** first line\n  second line\n\nA paragraph\nthat wraps.\n### Sub"
	got := parseMarkdownBlocks(doc)
	want := []markdownBlock{
		{level: markdownTitle, text: "Title"},
		{level: markdownSection, text: "v1 - date", afterBlank: true},
		{text: "**Lead.** first line second line", bullet: true},
		{text: "A paragraph that wraps.", afterBlank: true},
		{level: markdownSub, text: "Sub"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d blocks %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestShippedChangelog pins what the What's New screen shows: v2.1.0 is the first entry, no pre-release entry is left,
// and no heading keeps markdown markup.
func TestShippedChangelog(t *testing.T) {
	blocks := parseMarkdownBlocks(changelogMD)
	first := ""
	for _, b := range blocks {
		if b.level == markdownSection {
			if first == "" {
				first = b.text
			}
			if strings.Contains(b.text, "-rc.") {
				t.Errorf("pre-release entry %q is still in the changelog", b.text)
			}
		}
		if b.level > 0 && strings.ContainsAny(b.text, "*`") {
			t.Errorf("heading %q keeps markup", b.text)
		}
	}
	if !strings.HasPrefix(first, "v2.1.0 ") {
		t.Errorf("first entry %q, want v2.1.0", first)
	}
}

// TestMarkdownRasterDrawsWithoutMarkup pins the drawn text: a block's raster holds the cleaned text, wraps in a narrow
// column, is cached, and its height replaces the estimate.
func TestMarkdownRasterDrawsWithoutMarkup(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	defer c.purgeMarkdownCache()
	line := "**Drawing on the CPU is many times faster.** Sprites and backgrounds are kept at their size on screen."
	m := c.markdownRaster(line, ColText, mdTestWrapW)
	if m == nil {
		t.Fatal("no raster built")
	}
	if strings.Contains(m.Text(), "**") || m.Text() != plainMarkdown(line) {
		t.Errorf("raster text %q, want the line without markup", m.Text())
	}
	if m.Lines() < 2 {
		t.Errorf("raster has %d lines, want it wrapped in a %d px column", m.Lines(), mdTestWrapW)
	}
	if again := c.markdownRaster(line, ColText, mdTestWrapW); again != m {
		t.Error("the same block was rasterized twice; it should come from the cache")
	}
	if h, ok := c.markdownBuiltHeight(line, ColText, mdTestWrapW); !ok || h != m.Height() {
		t.Errorf("built height %d (ok %v), want the raster's %d", h, ok, m.Height())
	}
}
