package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// Issue #134: links in a server description are clickable where they appear, not repeated under the text.
func TestServerDescriptionLinksAreInline(t *testing.T) {
	const (
		site   = "https://github.com/SyntaxNyah/Nyathena"
		webao  = "webao.miku.pizza/client.html"
		listW  = 1000
		wantLn = 2
	)
	a := testTabApp(t)
	a.servers = []network.ServerEntry{{Name: "Skrapegropen", Description: "Fork: " + site + ". Web: www." + webao}}
	a.selectServerRow(0, listW)

	if len(a.descLinks) != wantLn {
		t.Fatalf("got %d links, want %d: %+v", len(a.descLinks), wantLn, a.descLinks)
	}
	for i, want := range []string{site, "www." + webao} {
		s := a.descLinks[i]
		if s.url != want {
			t.Errorf("link %d opens %q, want %q", i, s.url, want)
		}
		if got := a.descLines[s.row][s.lo:s.hi]; got != want {
			t.Errorf("link %d covers %q, want %q", i, got, want)
		}
	}
	for _, line := range a.descLines {
		if line == site {
			t.Errorf("link repeated as its own row: %q", a.descLines)
		}
	}
}

// A link cut by the wrap's ellipsis still opens the whole address.
func TestWrappedLinkSpansEllipsisOpensFullURL(t *testing.T) {
	const url = "https://example.com/long/path"
	rows := []string{"see https://example.com/lo…"}
	spans, _ := wrappedLinkSpans(nil, rows, 0, "see "+url+" more words", rows[0], 0)
	if len(spans) != 1 || spans[0].url != url {
		t.Fatalf("spans = %+v, want one opening %q", spans, url)
	}
}
