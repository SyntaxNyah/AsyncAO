package ui

import (
	"testing"
	"unicode"
)

func TestXMLEscapeKeepsToastTextPlainASCII(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain name", "plain name"},
		{`a&<b>"c'`, "a&#38;&#60;b&#62;&#34;c&#39;"},
		{"Jos\u00e9", "Jos&#233;"},
		{"\u30b2\u30fc\u30e0", "&#12466;&#12540;&#12512;"},
		{"\U0001F600", "&#128512;"},
		{"\u2018\u2019\u201a\u201b", "&#8216;&#8217;&#8218;&#8219;"},
		{"line\nbreak\tend", "linebreakend"},
	}
	for _, tc := range cases {
		got := xmlEscape(tc.in)
		if got != tc.want {
			t.Errorf("xmlEscape(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for _, r := range got {
			if r > unicode.MaxASCII {
				t.Errorf("xmlEscape(%q) left %q in the script", tc.in, r)
			}
		}
	}
}
