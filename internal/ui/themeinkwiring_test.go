package ui

import (
	"os"
	"strings"
	"testing"
)

// The four panel helpers below are drawn from BOTH the theme's design canvas and
// AsyncAO's own chrome, and each caller declares which it is. Getting that
// boolean wrong is invisible to every other test in this package: the ink still
// resolves, the panel still draws, and the only symptom is a theme's black
// track-list colour bleeding onto a floating window that fills its own dark
// panel — or, in the other direction, a theme's colours silently doing nothing.
//
// So this gate reads the call sites as SOURCE. It is the same shape as
// TestEveryHandDrawnKeyHasAPainter, and for the same reason: the invariant lives
// in which file the call is written in, which no runtime assertion can see.

// canvasInkArg is one pinned call site: the helper, the zero-based index of its
// canvas flag, and the literal every call in that file must pass.
type canvasInkArg struct {
	file string
	fn   string
	arg  int
	want string
	why  string
}

func TestCanvasInkCallSitesDeclareTheirPass(t *testing.T) {
	for _, tc := range []canvasInkArg{
		{"theme_layout.go", "drawICLogList", 1, "true", "the themed ic_chatlog element"},
		{"theme_layout.go", "drawOOCLogList", 1, "true", "the themed server_chatlog element"},
		{"theme_layout.go", "drawAreaList", 1, "true", "the themed area_list element"},
		{"theme_layout.go", "drawMusicList", 1, "true", "the themed music_list element"},

		{"screens.go", "drawICLogList", 1, "false", "the classic docked Log tab is chrome"},
		{"screens.go", "drawAreaList", 1, "false", "the classic docked Areas tab is chrome"},
		{"screens.go", "drawMusicList", 1, "false", "the classic docked Music tab is chrome"},

		{"torntabs.go", "drawAreaList", 1, "false", "a torn-off tab is a floating window, drawn AFTER the courtroom pass"},
		{"torntabs.go", "drawMusicList", 1, "false", "a torn-off tab is a floating window, drawn AFTER the courtroom pass"},
	} {
		t.Run(tc.file+":"+tc.fn, func(t *testing.T) {
			src, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			args := callArgs(string(src), "a."+tc.fn+"(")
			if len(args) == 0 {
				t.Fatalf("no %s call found in %s — the gate went blind (renamed helper?)", tc.fn, tc.file)
			}
			for _, got := range args {
				if tc.arg >= len(got) {
					t.Fatalf("%s call has %d args, wanted index %d: %v", tc.fn, len(got), tc.arg, got)
				}
				if got[tc.arg] != tc.want {
					t.Errorf("%s(%v): canvas flag = %q, want %q — %s",
						tc.fn, got, got[tc.arg], tc.want, tc.why)
				}
			}
		})
	}
}

// callArgs returns the top-level argument text of every call to prefix in src.
// Paren- and brace-aware so a composite-literal argument (sdl.Rect{...}) and a
// nested call (insetThemedBody(r)) do not split.
func callArgs(src, prefix string) [][]string {
	var out [][]string
	for i := 0; ; {
		k := strings.Index(src[i:], prefix)
		if k < 0 {
			return out
		}
		start := i + k + len(prefix)
		depth, argStart := 0, start
		var args []string
		j := start
		for ; j < len(src); j++ {
			switch src[j] {
			case '(', '{', '[':
				depth++
			case ')', '}', ']':
				if src[j] == ')' && depth == 0 {
					args = append(args, strings.TrimSpace(src[argStart:j]))
					out = append(out, args)
					j++
					goto next
				}
				depth--
			case ',':
				if depth == 0 {
					args = append(args, strings.TrimSpace(src[argStart:j]))
					argStart = j + 1
				}
			}
		}
	next:
		i = j
	}
}

// TestDrawOOCLogRowKeepsChromeInk pins the OTHER half of the same invariant: the
// shared row painter takes its base colour from the caller, and the classic OOC
// box must hand it ColText. That box fills its own ColPanel background, so a
// theme colour authored against the theme's art would land on the wrong thing.
func TestDrawOOCLogRowKeepsChromeInk(t *testing.T) {
	src, err := os.ReadFile("screens.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := callArgs(string(src), "a.drawOOCLogRow(")
	if len(calls) != 2 {
		t.Fatalf("expected exactly two drawOOCLogRow call sites (themed list + classic box), found %d", len(calls))
	}
	var bases []string
	for _, c := range calls {
		bases = append(bases, c[len(c)-1])
	}
	// One canvas caller passing the resolved server_chatlog ink, one chrome caller
	// passing ColText. Order-independent so a reshuffle of the file does not fail.
	wantOOCInk, wantColText := false, false
	for _, b := range bases {
		switch b {
		case "oocInk":
			wantOOCInk = true
		case "ColText":
			wantColText = true
		}
	}
	if !wantOOCInk || !wantColText {
		t.Errorf("drawOOCLogRow base colours = %v, want one oocInk (themed server_chatlog) and one ColText (classic OOC box)", bases)
	}
}
