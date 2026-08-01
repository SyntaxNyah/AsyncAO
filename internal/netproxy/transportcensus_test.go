package netproxy_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A zero-value http.Transport has Proxy == nil, and a nil Proxy means NEVER
// proxy — it is an opt-out, not "use the default". So every hand-built transport
// in this repo is a place where a user's proxy configuration can be silently
// discarded, and the symptom is not a crash or a failed test: it is traffic
// leaving the machine directly that the user believed was tunnelled.
//
// Three such transports existed and nothing flagged them, which is why this gate
// reads the whole tree. It parses rather than greps, because the prose in
// netproxy.go quotes a transport literal to explain the bug and a regexp census
// duly reported the explanation as an offender.

// directTransports exempts a file whose transport genuinely must never be
// proxied. Keyed by repo-relative path (slash-separated) with the reason, so an
// exemption cannot be added without writing down why.
var directTransports = map[string]string{}

func TestNoTransportIsBuiltWithoutAProxy(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var offenders []string
	seen := map[string]bool{}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "bin", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil // a file that does not parse is someone else's failing test
		}
		rel := filepath.ToSlash(mustRel(t, root, path))
		if _, ok := directTransports[rel]; ok {
			seen[rel] = true
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isHTTPTransport(lit.Type) {
				return true
			}
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Proxy" {
					return true
				}
			}
			offenders = append(offenders, rel+":"+fset.Position(lit.Pos()).String()[len(path)+1:])
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("http.Transport built with no Proxy field at: %s\n"+
			"A nil Proxy means NEVER proxy, so that traffic ignores the user's proxy configuration "+
			"while other paths in the app honour it. Set Proxy: netproxy.Proxy, or build it with "+
			"netproxy.Transport() (which also inherits the stdlib's timeouts), or add the file to "+
			"directTransports with the reason it must be direct.", strings.Join(offenders, ", "))
	}
	for rel := range directTransports {
		if !seen[rel] {
			t.Errorf("directTransports[%q] names a file that no longer exists — drop the exemption", rel)
		}
	}
}

// isHTTPTransport reports whether a composite-literal type is http.Transport
// (qualified) or Transport inside package http itself.
func isHTTPTransport(t ast.Expr) bool {
	switch v := t.(type) {
	case *ast.SelectorExpr:
		pkg, ok := v.X.(*ast.Ident)
		return ok && pkg.Name == "http" && v.Sel.Name == "Transport"
	case *ast.Ident:
		return false // a bare `Transport{}` in some other package is not net/http's
	}
	return false
}

func mustRel(t *testing.T, base, target string) string {
	t.Helper()
	rel, err := filepath.Rel(base, target)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

// repoRoot walks up from this package to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test's working directory")
		}
		dir = parent
	}
}
