package ui

// Shared source-structure helpers for the wiring gates in this package.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

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

// parsedPackage is the memoized result of parsing this package's non-test sources.
type parsedPackage struct {
	fset  *token.FileSet
	files map[string]*ast.File
}

// parseKey identifies a memoized parse: the package directory and the parser mode.
type parseKey struct {
	dir  string
	mode parser.Mode
}

var (
	srcParseMu    sync.Mutex
	srcParseCache = map[parseKey]*parsedPackage{}
)

// parseNonTestSources parses every non-test .go file in this package's directory into
// fset, returning a filename→AST map in the same shape the deprecated parser.ParseDir
// used to hand back (its per-package Files map). ParseDir was dropped because it
// ignores build tags when associating files with packages (SA1019); the explicit walk +
// parser.ParseFile keeps the census exact. An empty result is a failure, not a pass: a
// census that read nothing passes everything.
func parseNonTestSources(t testing.TB, mode parser.Mode) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	key := parseKey{dir: wd, mode: mode}
	srcParseMu.Lock()
	defer srcParseMu.Unlock()
	if pp, ok := srcParseCache[key]; ok {
		return pp.fset, cloneFileMap(pp.files)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, mode)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = f
	}
	if len(files) == 0 {
		t.Fatal("no production sources found — the census would be vacuous")
	}
	srcParseCache[key] = &parsedPackage{fset: fset, files: files}
	return fset, cloneFileMap(files)
}

// cloneFileMap returns a shallow copy of files so a caller that mutates the map (the
// icsfx sentinel gate deletes its own file before policing the rest) cannot corrupt the
// shared cache. The *ast.File values are shared but only ever read, never written.
func cloneFileMap(files map[string]*ast.File) map[string]*ast.File {
	out := make(map[string]*ast.File, len(files))
	for k, v := range files {
		out[k] = v
	}
	return out
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

// packageFuncs visits every function and method declared in this package's
// non-test sources, handing the visitor the file it lives in, its name and its
// body.
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
