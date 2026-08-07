package ui

// Shared source-structure helpers for the wiring gates in this package.
//
// WHY SOURCE AND NOT BEHAVIOUR. Some of what these bugs turned on is ORDER and
// PLACEMENT inside a single-pass draw — which layer paints after which, whether a
// bracket is released with defer — and neither is observable from a function's
// return value. The precedent is TestFrameGatesDriveTheRealFrame in
// frameharness_test.go, which parses this package's own sources for the same reason.
//
// These are deletion-catchers, never mirrors: they assert that PRODUCTION code calls
// production functions in a required relationship. They contain no copy of the logic
// under test, so they cannot go green against a re-implementation.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"testing"
)

// strconvUnquote is strconv.Unquote, wrapped so the harvesters can be read without
// an import that looks unrelated to string literals.
func strconvUnquote(lit string) (string, error) { return strconv.Unquote(lit) }

// parsedFile parses one file of this package, cached per test run is unnecessary —
// these gates run once each and the files are small.
func parsedFile(t *testing.T, name string) (*token.FileSet, *ast.File) {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return fset, f
}

// funcBodySource returns the AST body of the named function (or method) declared in
// file. Fails the test when it is absent — a renamed function must break its gate
// loudly, not silently stop being checked.
func funcBodySource(t *testing.T, file, fn string) *ast.BlockStmt {
	t.Helper()
	_, f := parsedFile(t, file)
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Name.Name == fn && fd.Body != nil {
			return fd.Body
		}
	}
	t.Fatalf("%s: no function %s — it was renamed or removed, and its wiring gate now checks nothing", file, fn)
	return nil
}

// containsCall reports whether n contains a call whose function NAME (the selector's
// last segment, or a bare identifier) is fn.
func containsCall(n ast.Node, fn string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if callName(call) == fn {
			found = true
		}
		return !found
	})
	return found
}

// deferredCall reports whether n contains `defer <...>fn(...)`.
func deferredCall(n ast.Node, fn string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		d, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		if callName(d.Call) == fn {
			found = true
		}
		return !found
	})
	return found
}

// callName is a call's function name: the selector's last segment for x.F(), the
// identifier for F(), "" for anything else (a call through a variable).
func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// callOrder returns the source ORDER of the named calls inside n: for each call
// found, its index into want. Calls not in want are ignored, and a name that appears
// twice contributes twice. Used to pin paint order in a single-pass draw.
func callOrder(n ast.Node, want ...string) []string {
	type hit struct {
		pos  token.Pos
		name string
	}
	var hits []hit
	inWant := make(map[string]bool, len(want))
	for _, w := range want {
		inWant[w] = true
	}
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := callName(call); inWant[name] {
			hits = append(hits, hit{pos: call.Pos(), name: name})
		}
		return true
	})
	// ast.Inspect is already in source order for a single file, but sort defensively
	// on position so a future nesting change cannot reorder the answer silently.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].pos < hits[j-1].pos; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.name)
	}
	return out
}
