package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateClosuresDoNotReachPrefs is the committed closure-safety gate: no Update
// closure body may call into the prefs (Load / Update / a getter / a setter) — the closure
// runs while Update holds p.mu, so such a call deadlocks. This is a lightweight AST scan of
// the module's non-test Go files; the type-resolved transitive call-graph version lives in
// the tooling (closuresafety) and reports zero hits too.
//
// Correction: Load() is lock-free only while the snapshot is fresh; when the snapshot is
// stale it rebuilds under the write lock, so Load is NOT safe inside a closure. Any direct
// prefs use inside a closure is flagged here, so a helper that reaches Load and is invoked
// from a closure is caught transitively by the tooling.
func TestUpdateClosuresDoNotReachPrefs(t *testing.T) {
	var files []string
	_ = filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	fset := token.NewFileSet()
	for _, f := range files {
		src, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			continue
		}
		ast.Inspect(src, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Update" || len(call.Args) != 1 {
				return true
			}
			fl, ok := call.Args[0].(*ast.FuncLit)
			if !ok {
				return true
			}
			ast.Inspect(fl.Body, func(m ast.Node) bool {
				se, ok := m.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				name := se.Sel.Name
				if name == "Prefs" || name == "Load" || name == "Update" || strings.HasPrefix(name, "Set") {
					t.Errorf("%s: Update closure reaches %q (a prefs call deadlocks under the write lock)", fset.Position(se.Pos()), name)
				}
				return true
			})
			return true
		})
	}
}
