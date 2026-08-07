package theme

// THE CENSUS'S OWN GATES.
//
// sidecar_validate_test.go censuses this package's API for a path that writes a
// sidecar without the write guard. That census is only worth what its MATCHERS
// are worth, and a matcher cannot be tested against the production package: the
// package (correctly) contains none of the bypasses it is looking for, so every
// matcher passes vacuously there — including a broken one.
//
// These fixtures ARE the bypasses, written down. Each one is a shape that reached
// the guard census and was waved through, or would be.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// parseCensusFixture parses one synthetic source and returns its lone function.
func parseCensusFixture(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package theme\n"+src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			return fn
		}
	}
	t.Fatal("the fixture declared no function")
	return nil
}

// TestCallsAnyOnRequiresASidecarReceiver is the guard-matcher's own gate.
//
// THE HOLE IT CLOSES, verbatim: the matcher used to compare a guard call by
// METHOD NAME only, so `s.doc.Bytes()` — a reach PAST the model into the lossless
// document, which is the very bypass TestNoExportedPathWritesASidecarWithoutTheGuard
// exists to forbid — satisfied the census because the selector happened to be
// spelled Bytes. An emitter could therefore serialise the raw document, skipping
// Validate entirely, and pass the gate that claims every emitter is guarded.
func TestCallsAnyOnRequiresASidecarReceiver(t *testing.T) {
	cases := []struct {
		what string
		src  string
		want bool
		why  string
	}{
		{
			"the real shape: s.Validate() on the Sidecar receiver",
			"func (s *Sidecar) Emit() []byte { if err := s.Validate(); err != nil { return nil }; return nil }",
			true, "Bytes' own spelling must keep passing, or this census is unshippable",
		},
		{
			"delegation: s.Bytes() on the Sidecar receiver",
			"func (s *Sidecar) Emit() []byte { b, _ := s.Bytes(); return b }",
			true, "Write satisfies the census exactly this way",
		},
		{
			"THE HOLE: s.doc.Bytes(), the document straight past the model",
			"func (s *Sidecar) Emit() []byte { return s.doc.Bytes() }",
			false, "this is the bypass the whole file exists to forbid, and the name-only matcher called it a guard",
		},
		{
			"a laundered local document",
			"func (s *Sidecar) Emit() []byte { d := s.doc; return d.Bytes() }",
			false, "one assignment is all it takes to launder the receiver",
		},
		{
			"a Sidecar arriving as a PARAMETER, not a receiver",
			"func Emit(sc *Sidecar) []byte { b, _ := sc.Bytes(); return b }",
			true, "a free function holding a Sidecar is an emitter too, and its guard is just as real",
		},
		{
			"inside a closure",
			"func (s *Sidecar) Emit() []byte { f := func() { _ = s.Validate() }; f(); return nil }",
			true, "a guard reached through a nested call is still reached",
		},
		{
			"a guard-shaped call on something that is not a Sidecar at all",
			"func (s *Sidecar) Emit(d *INIDoc) []byte { return d.Bytes() }",
			false, "INIDoc.Bytes is not the guard; it is what the guard stands in front of",
		},
	}
	for _, c := range cases {
		fn := parseCensusFixture(t, c.src)
		if got := callsAnyOn(fn.Body, sidecarIdents(fn), sidecarGuardCalls); got != c.want {
			t.Errorf("%s: callsAnyOn = %v, want %v — %s", c.what, got, c.want, c.why)
		}
	}
}

// TestExportedINIDocValuesAreRefused pins the third arm of the API census: the
// package-level var/const shapes that hand out a live document with no method and
// no struct field for the other two arms to see.
//
// It is the cheapest possible way to reintroduce the deleted Doc() accessor under
// a name the census had no opinion about, and until this arm existed the census
// had no opinion about any of them.
func TestExportedINIDocValuesAreRefused(t *testing.T) {
	cases := []struct {
		what string
		src  string
		want bool
	}{
		{"a bare exported pointer var", "var Scratch *INIDoc", true},
		{"a slice of them", "var Docs []*INIDoc", true},
		{"a map of them", "var ByName map[string]*INIDoc", true},
		{"an array of them", "var Pair [2]*INIDoc", true},
		{"a struct wrapping one", "var Held struct{ D *INIDoc }", true},
		{"an untyped var built from a literal", "var Scratch = &INIDoc{}", true},
		{"an untyped var built from an allow-listed producer", "var Scratch, _ = ParseINIDoc(nil)", true},
		{"an unrelated exported var", "var ThemeFormatName = \"asyncao_theme.ini\"", false},
		{"a Sidecar, which is the model and is MEANT to be reachable", "var Empty *Sidecar", false},
		{"an unexported one, which no importer can reach", "var scratch *INIDoc", false},
	}
	for _, c := range cases {
		f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package theme\n"+c.src, 0)
		if err != nil {
			t.Fatalf("%s: parsing the fixture: %v", c.what, err)
		}
		if got := fixtureHandsOutADocument(f); got != c.want {
			t.Errorf("%s: the var/const arm reports %v, want %v — %q", c.what, got, c.want, c.src)
		}
	}
}

// fixtureHandsOutADocument runs the census's var/const arm over one parsed file.
// It is deliberately the same two predicates the census itself calls, in the same
// order — a re-implementation here would be a mirror test, which rule 11 does not
// count.
func fixtureHandsOutADocument(f *ast.File) bool {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || (gd.Tok != token.VAR && gd.Tok != token.CONST) {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			exported := false
			for _, n := range vs.Names {
				if n.IsExported() {
					exported = true
				}
			}
			if !exported {
				continue
			}
			if vs.Type != nil && mentionsINIDocPtr(vs.Type) {
				return true
			}
			for _, v := range vs.Values {
				if makesINIDoc(v) {
					return true
				}
			}
		}
	}
	return false
}
