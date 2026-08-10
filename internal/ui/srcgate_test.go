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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

// callsNamed returns every call inside n whose function NAME is fn, in source order.
// containsCall answers "is it called at all"; this is for gates that then have to look
// at the ARGUMENTS of the call they found.
func callsNamed(n ast.Node, fn string) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(n, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && callName(call) == fn {
			out = append(out, call)
		}
		return true
	})
	return out
}

// bindingOf returns the name the single-value assignment `x := fn(...)` binds inside
// n, or "" when fn's result is not assigned to one plain identifier. A gate uses it to
// learn what a helper's result is CALLED here, so the rest of the gate can require
// other expressions to be derived from that name without hard-coding it — rename the
// variable and the gate follows; stop using the helper and the gate fails.
func bindingOf(n ast.Node, fn string) string {
	name := ""
	ast.Inspect(n, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		if call, ok := as.Rhs[0].(*ast.CallExpr); !ok || callName(call) != fn {
			return true
		}
		if id, ok := as.Lhs[0].(*ast.Ident); ok {
			name = id.Name
		}
		return false
	})
	return name
}

// mentionsIdent reports whether the expression e reads the identifier name anywhere
// inside it. Deliberately "mentions" and not "equals": a coordinate that legitimately
// grows to `head.count.X+2` still derives from head, while one rebuilt out of the
// panel rect does not mention it at all — which is the drift these gates catch.
func mentionsIdent(e ast.Expr, name string) bool {
	found := false
	ast.Inspect(e, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && id.Name == name {
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

// readsFieldOf reports whether n contains `<expr containing a call to src>.<field>`
// — a READ of that field off the helper's result, wherever it appears.
//
// It is assignsFieldFrom's missing half, and the omission was load-bearing: that
// one matches an assignment TO a selector field, so a census built on it can only
// ever see writes. The bypass a "one place knows this rule" gate is written to
// catch is a READ — `anim := a.charMetaFor(name).idle` — whose left-hand side is a
// plain identifier, so it went straight through. This walks expressions instead of
// statements, which catches the read in every position it can take (an assignment,
// an if-init, a call argument, a return).
func readsFieldOf(n ast.Node, field, src string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != field {
			return true
		}
		if containsCall(sel.X, src) {
			found = true
		}
		return !found
	})
	return found
}

// packageFuncs visits every function and method declared in this package's
// non-test sources, handing the visitor the file it lives in, its name and its
// body.
//
// WHY WHOLE-PACKAGE AND NOT A NAMED LIST. A census over three remembered function
// names answers "did these three break the rule", which is not the question a
// "only one place may do X" gate is asking — a NEW call site is precisely the
// thing that has not been remembered. Walking the package makes the gate's subject
// the rule rather than the roster, so a fourth preview surface is covered by
// existing at all. It fails loudly on an unparseable or unreadable file rather
// than skipping it, so the census can never silently shrink.
func packageFuncs(t *testing.T, visit func(file, fn string, body *ast.BlockStmt)) {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob this package's sources: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no sources found for the package census — it would pass vacuously")
	}
	sort.Strings(names) // deterministic report order, whatever the filesystem hands back
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		_, f := parsedFile(t, name)
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
				visit(name, fd.Name.Name, fd.Body)
			}
		}
	}
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
