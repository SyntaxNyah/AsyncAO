package theme

// Gates for the SECTION RENAME (v1.90.0 W8) — the primitive W7b deferred, and
// the one funnel that is allowed to drive it.

import (
	"errors"
	"go/ast"
	"strings"
	"testing"
)

// renameFixture is a comment-dominated document, because comments are what a
// rename is most likely to lose and what nobody notices until the author opens
// their own file.
const renameFixture = "; the whole theme\n" +
	"[theme]\r\n" +
	"name = Paper\r\n" +
	"\n" +
	"; ---- the speech box -------------------------------------------------\n" +
	"[  element.box  ]   ; the reading band\n" +
	"kind = shape   ; a flat panel\n" +
	"rect = 10, 20, 30, 40\n" +
	"# an unknown key from a newer client\n" +
	"halo_px = 3\n" +
	"\n" +
	"[element.badge]\n" +
	"kind = text\n" +
	"text = hello\n"

// TestRenameSectionMovesTheNameAndNothingElse is the primitive's contract, byte
// for byte: after a rename the file differs in EXACTLY the header's name and in
// nothing else — not the author's spacing, not their trailing comment, not the
// CRLF the [theme] block happens to use, not the position of the section.
func TestRenameSectionMovesTheNameAndNothingElse(t *testing.T) {
	d, err := ParseINIDoc([]byte(renameFixture))
	if err != nil {
		t.Fatalf("ParseINIDoc: %v", err)
	}
	if err := d.RenameSection("element.box", "element.band"); err != nil {
		t.Fatalf("RenameSection: %v", err)
	}
	got := string(d.Bytes())
	want := strings.Replace(renameFixture, "[  element.box  ]", "[  element.band  ]", 1)
	if got != want {
		t.Fatalf("the rename changed more than the name:\n got %q\nwant %q", got, want)
	}
	// The keys moved WITH it — the index has to follow the header or the writer
	// would append a second copy of every key under the new name.
	if v, ok := d.Get("element.band", "rect"); !ok || v != "10, 20, 30, 40" {
		t.Errorf("after the rename element.band/rect = %q, %v — want the original row", v, ok)
	}
	if _, ok := d.Get("element.box", "rect"); ok {
		t.Error("the OLD section still answers Get — the file would read back as two elements")
	}
}

// TestRenameSectionRefusesWhatWouldMergeOrBreak drives every refusal from the
// outside. Each one is a case where the "obvious" lenient behaviour silently
// destroys somebody's data.
func TestRenameSectionRefusesWhatWouldMergeOrBreak(t *testing.T) {
	cases := []struct {
		name, from, to string
		want           error
	}{
		{"onto an existing section", "element.box", "element.badge", ErrINIDocSectionTaken},
		{"a section that is not there", "element.ghost", "element.new", ErrINIDocNoSection},
		{"the root section", "", "element.new", ErrINIDocBadKey},
		{"a name with a bracket", "element.box", "element.b]x", ErrINIDocBadKey},
		{"a name with a comment character", "element.box", "element.b;x", ErrINIDocBadKey},
		{"an empty target", "element.box", "", ErrINIDocBadKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ParseINIDoc([]byte(renameFixture))
			if err != nil {
				t.Fatalf("ParseINIDoc: %v", err)
			}
			if err := d.RenameSection(tc.from, tc.to); !errors.Is(err, tc.want) {
				t.Fatalf("RenameSection(%q, %q) = %v, want %v", tc.from, tc.to, err, tc.want)
			}
			// AND THE DOCUMENT IS UNTOUCHED. A refusal that had already moved half
			// the section would be worse than the merge it refused.
			if got := string(d.Bytes()); got != renameFixture {
				t.Errorf("a refused rename still changed the file:\n%q", got)
			}
		})
	}
}

// TestRenameSectionRenamesEveryHeaderForOneSection pins the split-section case.
// A file that opens the same section twice is ONE section to the reader (last
// declaration wins per key), so renaming only the first header would strand the
// second run's keys under the old name.
func TestRenameSectionRenamesEveryHeaderForOneSection(t *testing.T) {
	src := "[element.box]\nkind = box\n\n[other]\nx = 1\n\n[element.box]\nrect = 1, 2, 3, 4\n"
	d, err := ParseINIDoc([]byte(src))
	if err != nil {
		t.Fatalf("ParseINIDoc: %v", err)
	}
	if err := d.RenameSection("element.box", "element.band"); err != nil {
		t.Fatalf("RenameSection: %v", err)
	}
	got := string(d.Bytes())
	if strings.Contains(got, "[element.box]") {
		t.Fatalf("a second header for the same section survived the rename:\n%s", got)
	}
	if n := strings.Count(got, "[element.band]"); n != 2 {
		t.Fatalf("%d headers renamed, want 2:\n%s", n, got)
	}
	if v, ok := d.Get("element.band", "rect"); !ok || v != "1, 2, 3, 4" {
		t.Errorf("the second run's key did not follow the rename: %q, %v", v, ok)
	}
}

// TestRenameSectionWorksOnASectionTheWriterCreated closes the asymmetry that
// would otherwise make exactly one section in a file un-renameable: the one this
// document authored itself. Set() creates a header, and a header created by a
// write has to carry its own name span like every other.
func TestRenameSectionWorksOnASectionTheWriterCreated(t *testing.T) {
	d, err := ParseINIDoc(nil)
	if err != nil {
		t.Fatalf("ParseINIDoc: %v", err)
	}
	if err := d.Set("element.fresh", "kind", "box"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := d.RenameSection("element.fresh", "element.moved"); err != nil {
		t.Fatalf("RenameSection: %v", err)
	}
	got := string(d.Bytes())
	if !strings.Contains(got, "[element.moved]") || strings.Contains(got, "[element.fresh]") {
		t.Fatalf("an authored section did not rename cleanly:\n%s", got)
	}
	// Re-read it: the whole point is that the result is a file, not a shape that
	// only this document understands.
	back, err := ParseINIDoc([]byte(got))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if v, ok := back.Get("element.moved", "kind"); !ok || v != "box" {
		t.Errorf("re-parsed element.moved/kind = %q, %v", v, ok)
	}
}

// ---------------------------------------------------------------------------
// The funnel
// ---------------------------------------------------------------------------

// TestRenameElementMovesTheModelAndTheFileTogether is the seam's whole reason
// for existing: one call, both halves, and a re-read that finds ONE element.
func TestRenameElementMovesTheModelAndTheFileTogether(t *testing.T) {
	sc, err := ParseSidecar([]byte(renameFixture))
	if err != nil {
		t.Fatalf("ParseSidecar: %v", err)
	}
	if err := sc.RenameElement("box", "band"); err != nil {
		t.Fatalf("RenameElement: %v", err)
	}
	b, err := sc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	again, err := ParseSidecar(b)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	ids := elementIDsOf(again)
	if strings.Join(ids, ",") != "band,badge" {
		t.Fatalf("after a rename the file reads back as %v — want exactly [band badge] in the "+
			"author's own order", ids)
	}
	// The rect came along with the name, which is what proves the KEYS moved and
	// not merely the header.
	for i := range again.Elements {
		if again.Elements[i].ID == "band" && again.Elements[i].Rect != (Rect{X: 10, Y: 20, W: 30, H: 40}) {
			t.Errorf("band's rect is %+v — the keys did not follow the header", again.Elements[i].Rect)
		}
	}
	// AND THE AUTHOR'S COMMENT SURVIVED. This is the property a
	// delete-then-append rename cannot have.
	if !strings.Contains(string(b), "; the reading band") {
		t.Errorf("the header's trailing comment did not survive the rename:\n%s", b)
	}
	if !strings.Contains(string(b), "; a flat panel") {
		t.Errorf("a key's trailing comment did not survive the rename:\n%s", b)
	}
}

// TestRenameElementRefusesEveryWayItCouldDuplicate drives the funnel's refusals.
// The MODEL half and the CARRIER half are both represented, because a check on
// only one of them is exactly how the duplication bug comes back.
func TestRenameElementRefusesEveryWayItCouldDuplicate(t *testing.T) {
	// A section this build preserved-and-ignored: a valid id whose KIND is from a
	// newer client. The model does not hold it, so a model-only collision check
	// would happily rename another element on top of it.
	const withUnknown = "[element.box]\nkind = shape\n\n[element.future]\nkind = hologram\nrect = 1, 2, 3, 4\n"
	cases := []struct {
		name, src, from, to string
		want                error
	}{
		{"a taken model id", renameFixture, "box", "badge", ErrRenameTaken},
		{"a taken PRESERVED section", withUnknown, "box", "future", ErrINIDocSectionTaken},
		{"an id outside the grammar", renameFixture, "box", "Band Two", ErrRenameBadID},
		{"an id past the cap", renameFixture, "box", strings.Repeat("x", IDRuneCap+1), ErrRenameBadID},
		{"an element that is not there", renameFixture, "ghost", "band", ErrRenameMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, err := ParseSidecar([]byte(tc.src))
			if err != nil {
				t.Fatalf("ParseSidecar: %v", err)
			}
			before := elementIDsOf(sc)
			// The BASELINE is what this model writes BEFORE the attempt, not the
			// source: a save of an unedited file is already the writer's own
			// business (the corpus gate in repothemes_test.go measures that), and
			// what is under test here is only that a REFUSAL moves nothing.
			baseline, err := sc.Bytes()
			if err != nil {
				t.Fatalf("Bytes before the attempt: %v", err)
			}
			if err := sc.RenameElement(tc.from, tc.to); !errors.Is(err, tc.want) {
				t.Fatalf("RenameElement(%q, %q) = %v, want %v", tc.from, tc.to, err, tc.want)
			}
			if got := elementIDsOf(sc); strings.Join(got, ",") != strings.Join(before, ",") {
				t.Errorf("a refused rename still moved the model: %v -> %v", before, got)
			}
			b, err := sc.Bytes()
			if err != nil {
				t.Fatalf("Bytes after a refusal: %v", err)
			}
			if string(b) != string(baseline) {
				t.Errorf("a refused rename still changed the file:\n%s", b)
			}
		})
	}
}

// TestRenameElementOnAnAuthoredElementIsNotAFailure pins the one "error" that is
// not one: an element the editor created this session has no header in the
// carrier yet, so there is nothing to move and the model rename must still land.
func TestRenameElementOnAnAuthoredElementIsNotAFailure(t *testing.T) {
	sc := NewSidecar()
	sc.Elements = append(sc.Elements, Element{ID: "fresh", Kind: ElemShape})
	if err := sc.RenameElement("fresh", "named"); err != nil {
		t.Fatalf("renaming an element with no lines yet: %v", err)
	}
	if sc.Elements[0].ID != "named" {
		t.Fatalf("the model kept %q", sc.Elements[0].ID)
	}
	b, err := sc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if !strings.Contains(string(b), "[element.named]") {
		t.Fatalf("the written file does not declare the renamed element:\n%s", b)
	}
	if strings.Contains(string(b), "[element.fresh]") {
		t.Fatalf("the written file declares the OLD id too:\n%s", b)
	}
}

// TestNothingButTheFunnelRenamesAnElementSection is the ENCAPSULATION gate.
//
// The carrier is private, so the only way to rename an element's section from
// outside internal/theme is Sidecar.RenameElement. This census fails if the
// package ever grows a second exported path to a section rename — an accessor
// handing out the *INIDoc, or a helper that renames without the model — because
// that is the shape (two owners of one identity) the whole file exists to
// prevent.
func TestNothingButTheFunnelRenamesAnElementSection(t *testing.T) {
	callers := exportedFuncsCalling(t, "RenameSection")
	for _, fn := range callers {
		if fn != "RenameElement" {
			t.Errorf("%s calls INIDoc.RenameSection — the model half would not move with it; "+
				"route it through Sidecar.RenameElement", fn)
		}
	}
	if len(callers) == 0 {
		t.Error("nothing in this package calls RenameSection any more — the primitive is dead, " +
			"and the id row in internal/ui is now writing a duplicate section")
	}
}

// exportedFuncsCalling names every function in this package whose body calls
// method. It reuses parsePackageSources — the one census walk the package's
// other gates are built on — so a file split can never make it vacuous.
func exportedFuncsCalling(t *testing.T, method string) []string {
	t.Helper()
	_, files := parsePackageSources(t)
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			found := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel != nil && sel.Sel.Name == method {
					found = true
				}
				return true
			})
			if found {
				out = append(out, fn.Name.Name)
			}
		}
	}
	return out
}

// elementIDsOf lists the model's ids in order.
func elementIDsOf(s *Sidecar) []string {
	out := make([]string, 0, len(s.Elements))
	for i := range s.Elements {
		out = append(out, s.Elements[i].ID)
	}
	return out
}
