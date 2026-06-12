package theme

import "testing"

// A representative slice of a real AO theme stylesheet: comments, multi
// selectors, hover states, Qt #aarrggbb alpha colors, and junk we ignore.
const sampleQSS = `
/* AO2 theme stylesheet */
QWidget {
	background-color: #1b2735;
	color: rgb(220, 230, 240);
	font-family: "Igiari"; /* ignored: not a color */
}
QPushButton, AOButton {
	background-color: #ff2f4156;
	border-color: #5fa8ff;
	border-radius: 4px;
}
QPushButton:hover {
	background-color: #7fb8ff;
}
QListWidget { background: #16202c; }
.unknownClass { color: #ffffff; }
`

func TestParseStylesheetExtractsPalette(t *testing.T) {
	p := ParseStylesheet([]byte(sampleQSS))
	if p.Empty() {
		t.Fatal("parsed palette is empty")
	}
	if p.Panel == nil || *p.Panel != (RGB{R: 0x1b, G: 0x27, B: 0x35}) {
		t.Errorf("Panel = %+v, want #1b2735", p.Panel)
	}
	if p.Text == nil || *p.Text != (RGB{R: 220, G: 230, B: 240}) {
		t.Errorf("Text = %+v, want rgb(220,230,240)", p.Text)
	}
	// #ff2f4156 is Qt ARGB: alpha ff dropped, color 2f4156. The later
	// QListWidget background must NOT clobber PanelHi (different slot).
	if p.PanelHi == nil || *p.PanelHi != (RGB{R: 0x2f, G: 0x41, B: 0x56}) {
		t.Errorf("PanelHi = %+v, want #2f4156 (Qt alpha dropped)", p.PanelHi)
	}
	// :hover wins the accent over border-color — later declarations win.
	if p.Accent == nil || *p.Accent != (RGB{R: 0x7f, G: 0xb8, B: 0xff}) {
		t.Errorf("Accent = %+v, want #7fb8ff (hover state)", p.Accent)
	}
}

func TestParseStylesheetIgnoresGarbage(t *testing.T) {
	cases := []string{
		"",
		"not css at all",
		"QWidget { color: url(image.png); }",   // non-color value
		"QWidget { background-color: ; }",      // empty value
		"QWidget { color: #12 }",               // bad hex length
		"/* unterminated comment QWidget {",    // EOF inside comment
		"QWidget { color: #fff",                // unterminated block
		".mystery { background-color: #fff; }", // unknown selector
	}
	for _, src := range cases {
		if p := ParseStylesheet([]byte(src)); !p.Empty() {
			t.Errorf("ParseStylesheet(%q) = %+v, want empty", src, p)
		}
	}
}

func TestParseCSSColorForms(t *testing.T) {
	cases := []struct {
		in   string
		want RGB
		ok   bool
	}{
		{"#fff", RGB{R: 255, G: 255, B: 255}, true},
		{"#a1b2c3", RGB{R: 0xa1, G: 0xb2, B: 0xc3}, true},
		{"#80a1b2c3", RGB{R: 0xa1, G: 0xb2, B: 0xc3}, true}, // Qt ARGB
		{"rgb(1, 2, 3)", RGB{R: 1, G: 2, B: 3}, true},
		{"rgba(4,5,6,0.5)", RGB{R: 4, G: 5, B: 6}, true},
		{"white", RGB{R: 255, G: 255, B: 255}, true},
		{"WHITE", RGB{R: 255, G: 255, B: 255}, true},
		{"bogus", RGB{}, false},
		{"#gggggg", RGB{}, false},
	}
	for _, tc := range cases {
		got, ok := parseCSSColor(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseCSSColor(%q) = %+v,%v want %+v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
