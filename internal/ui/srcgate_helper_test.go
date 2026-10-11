package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// failTB records a Fatalf call so a helper's failure path can be asserted without
// terminating the test (a census that silently stops checking must be observable).
type failTB struct {
	testing.TB
	fatalfCalled bool
}

func (f *failTB) Fatalf(format string, args ...any) { f.fatalfCalled = true }
func (f *failTB) Helper()                           {}

// TestFuncBodySourceFoundAndAbsent pins the package-wide lookup's contract: a
// present function is found wherever it lives, and an absent one must fail the
// gate loudly rather than silently stop checking.
func TestFuncBodySourceFoundAndAbsent(t *testing.T) {
	if body := funcBodySource(t, "ui.go", "blitLabel"); body == nil {
		t.Fatal("blitLabel should be found package-wide")
	}
	ft := &failTB{}
	funcBodySource(ft, "ui.go", "ThisFunctionDoesNotExist")
	if !ft.fatalfCalled {
		t.Error("funcBodySource did not fail for an absent function — a renamed/removed function would go unchecked")
	}
}

// TestFuncBodySourceAmbiguousFails pins the double-declaration guard: two methods
// with the SAME name on DIFFERENT receivers (legal Go) in two files, looked up
// without a hint that owns either, must fail — the census would otherwise read one
// of two bodies.
func TestFuncBodySourceAmbiguousFails(t *testing.T) {
	fset := token.NewFileSet()
	parse := func(name, src string) *ast.File {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		return f
	}
	files := map[string]*ast.File{
		"a.go": parse("a.go", "package ui\n\ntype ambigA struct{}\n\nfunc (ambigA) probe() {}\n"),
		"b.go": parse("b.go", "package ui\n\ntype ambigB struct{}\n\nfunc (ambigB) probe() {}\n"),
	}
	ft := &failTB{}
	findFuncBodyInFiles(ft, files, "hint.go", "probe")
	if !ft.fatalfCalled {
		t.Error("funcBodySource did not fail on a function declared in two files — it would read one of two bodies")
	}
}
