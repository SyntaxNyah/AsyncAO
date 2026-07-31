package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// The issue #21 fixture: the exact theme from the side-by-side screenshot,
// checked in as its three text files. No art and no TTFs — the font tests
// synthesize faces, and 13 MiB of PNGs would buy nothing a rect test can read.
//
// Before this existed, every themed-layout change was verified by screenshot.
// That is how the IC-bar cram shipped as "intended" and stayed that way through
// several releases.

// aceattorney2xRoot is the fixture's CONTENT ROOT (the folder containing
// themes/), which is the shape theme.Load takes.
const aceattorney2xRoot = "testdata"

// aceattorney2xName is the theme directory name inside it.
const aceattorney2xName = "aceattorney2x"

// loadAceAttorney2x opens the fixture the way a real import does.
func loadAceAttorney2x(t *testing.T) *theme.Theme {
	t.Helper()
	th, err := theme.Load(aceattorney2xName, []string{aceattorney2xRoot})
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if _, ok := th.ElementRect("courtroom"); !ok {
		t.Fatalf("fixture did not load — probed %v", th.Dirs())
	}
	return th
}

// readFixtureFile reads one checked-in fixture file by name.
func readFixtureFile(name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(aceattorney2xRoot, theme.ThemesDirName, aceattorney2xName, name))
	return string(b), err
}

// containsSelector reports whether a CSS body declares sel as a rule head. Both
// spacings occur in real theme CSS.
func containsSelector(css, sel string) bool {
	return strings.Contains(css, sel+" {") || strings.Contains(css, sel+"{")
}

// aceattorney2xDesignKeys / …RectKeys are counted from the checked-in file with
// the parser's own rules (';' truncates, keys lowercased, no sections). They are
// pinned so an accidental re-copy of a DIFFERENT version of the theme fails
// loudly instead of silently changing what issue #21 means.
const (
	aceattorney2xDesignKeys = 120
	aceattorney2xRectKeys   = 104
)

// aceattorney2xBoundRects is how many of those rect keys reach a themeSlots row.
// The remainder are 3 char-select keys and 27 written deferrals.
//
// If this fails, do NOT edit the constant to match: diff the two key sets and
// decide about each key. Being able to notice a key nobody decided about is the
// entire purpose of the oracle below.
const aceattorney2xBoundRects = 74

// TestAceAttorney2xFixtureLoads pins the fixture's own shape and the rects the
// whole issue turns on.
func TestAceAttorney2xFixtureLoads(t *testing.T) {
	th := loadAceAttorney2x(t)

	keys := th.DesignKeys()
	if len(keys) != aceattorney2xDesignKeys {
		t.Fatalf("fixture has %d design keys, want %d — the checked-in file changed", len(keys), aceattorney2xDesignKeys)
	}
	rects := 0
	for k := range keys {
		if _, ok := th.ElementRect(k); ok {
			rects++
		}
	}
	if rects != aceattorney2xRectKeys {
		t.Fatalf("fixture has %d rect keys, want %d", rects, aceattorney2xRectKeys)
	}

	// Label 4's rect: the IC input is a FULL-WIDTH bar under the stage, 512 px
	// wide, on a row of its own. AsyncAO crams its colour picker, Immediate, Pre,
	// FX and Flip into that same rect, which is the headline complaint.
	if r, ok := th.ElementRect("ao2_ic_chat_message"); !ok || r != (theme.Rect{X: 216, Y: 384, W: 512, H: 23}) {
		t.Errorf("ao2_ic_chat_message = %+v ok=%v, want {216 384 512 23}", r, ok)
	}
	// This theme's canvas is 944x600, not AO2's stock 714x579 — so anything that
	// assumes the stock canvas is already wrong here.
	if r, _ := th.ElementRect("courtroom"); r != (theme.Rect{X: 0, Y: 0, W: 944, H: 600}) {
		t.Errorf("courtroom = %+v, want {0 0 944 600}", r)
	}
	// ms_chatlog and server_chatlog are declared at the SAME rect, which is why
	// the merged-log heuristic and the debug-log re-point both have to be right.
	ms, _ := th.ElementRect("ms_chatlog")
	sv, _ := th.ElementRect("server_chatlog")
	if ms != sv || ms != (theme.Rect{X: 728, Y: 0, W: 216, H: 346}) {
		t.Errorf("ms_chatlog=%+v server_chatlog=%+v, want both {728 0 216 346}", ms, sv)
	}
}

// TestAceAttorney2xEveryRectIsAccountedFor is the totality oracle — the test that
// makes issue #21 hard to reintroduce. A rect key the theme declares must be
// bound to a widget, deliberately deferred WITH A WRITTEN REASON, or a
// char-select key. There is no fourth answer, and "AsyncAO doesn't know about it"
// is not one of the three.
func TestAceAttorney2xEveryRectIsAccountedFor(t *testing.T) {
	th := loadAceAttorney2x(t)

	var bound, charSel, orphan []string
	for k := range th.DesignKeys() {
		if _, ok := th.ElementRect(k); !ok {
			continue // pairs, colours and scalars are not widget rects
		}
		_, isCharSel := charSelectDefaultRects[k]
		switch {
		case themeSlotFor(k) != nil:
			bound = append(bound, k)
		case themeSlotDeferred[k] != "":
			// A written deferral. Counted only by the empty-reason check below.
		case isCharSel:
			charSel = append(charSel, k)
		default:
			orphan = append(orphan, k)
		}
	}
	sort.Strings(orphan)
	if len(orphan) > 0 {
		t.Errorf("%d design rect(s) reach no widget and no written deferral: %v\n"+
			"Add a themeSlots row (themeslots.go) or a themeSlotDeferred entry with a REASON.", len(orphan), orphan)
	}
	if len(bound) != aceattorney2xBoundRects {
		t.Errorf("%d bound rects, want %d — re-derive aceattorney2xBoundRects and say why it moved", len(bound), aceattorney2xBoundRects)
	}
	sort.Strings(charSel)
	if len(charSel) != 3 {
		t.Errorf("char-select rects in the courtroom fixture = %v, want exactly 3", charSel)
	}
}

// TestAceAttorney2xFontDeclarations pins what the typography commits have to
// honour, read off the checked-in courtroom_fonts.ini rather than a memory of it.
// FOUR families, not three: evidence_description_font = Times New Roman is a
// fourth, unreachable today only because theme.FontElements has no evidence
// member.
func TestAceAttorney2xFontDeclarations(t *testing.T) {
	th := loadAceAttorney2x(t)

	for _, tc := range []struct {
		id     string
		size   int
		family string
		sharp  bool
	}{
		{"showname", 12, "Ace Name", true},
		{"message", 24, "Igiari Cyrillic", true},
		{"ic_chatlog", 10, "Arial", false},
		{"ms_chatlog", 8, "Arial", true},
		{"server_chatlog", 8, "Arial", true},
		{"music_list", 8, "Arial", true},
		{"music_name", 8, "Arial", false},
		{"area_list", 8, "Arial", true},
		{"evidence_description", 10, "Times New Roman", false},
	} {
		spec := th.Font(tc.id)
		if !spec.SizeSet || spec.Size != tc.size {
			t.Errorf("%s size = %d (set=%v), want %d", tc.id, spec.Size, spec.SizeSet, tc.size)
		}
		if spec.Font != tc.family {
			t.Errorf("%s family = %q, want %q", tc.id, spec.Font, tc.family)
		}
		if spec.Sharp != tc.sharp {
			t.Errorf("%s sharp = %v, want %v", tc.id, spec.Sharp, tc.sharp)
		}
	}
	// Per-element COLOURS: declared here, and today they reach no draw site.
	// music_list_color is BLACK, which is invisible on AsyncAO's dark panels —
	// the reason the colour commit needs a luma guard rather than a plain apply.
	if c := th.Font("ic_chatlog"); !c.ColorSet || c.Color != (theme.RGB{R: 255, G: 255, B: 255}) {
		t.Errorf("ic_chatlog_color = %+v set=%v, want 255,255,255", c.Color, c.ColorSet)
	}
	if c := th.Font("music_list"); !c.ColorSet || c.Color != (theme.RGB{}) {
		t.Errorf("music_list_color = %+v set=%v, want 0,0,0", c.Color, c.ColorSet)
	}
}

// TestAceAttorney2xStylesheetSelectors pins the CSS selectors the palette commit
// has to resolve by class inheritance. It asserts the FILE, not the resolver, so
// it is green today and becomes that commit's input.
func TestAceAttorney2xStylesheetSelectors(t *testing.T) {
	css, err := readFixtureFile("courtroom_stylesheets.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, sel := range []string{
		"QFrame", "QAbstractItemView", "QListView", "QComboBox", "QScrollBar",
		"QTextBrowser#ui_debug_log", "QLineEdit#ui_ic_chat_name",
	} {
		if !containsSelector(css, sel) {
			t.Errorf("fixture CSS lost selector %q", sel)
		}
	}
}
