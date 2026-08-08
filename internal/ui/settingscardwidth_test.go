package ui

// THE SETTINGS TAB BODIES DRAW INTO THE CARD, NOT THE WINDOW.
//
// THE DEFECT THIS CLOSES. drawSettingsTabBody hands every tab body the WINDOW width,
// while the page clips the body to the content card ({cardX, cardW}, left inset by the
// sidebar and capped at settMaxCardW). Every body therefore ignores the argument and
// re-reads a.formW2() — every body except drawSettingsThemeEditor, which shipped in
// W10 using the parameter. Its two right-anchored buttons place themselves at
// `w - themeGetBtnW - scrollBarW`, so with the window width they land outside the clip:
// eight pixels shaved at 1280, and at 1920 the whole button drawn and hit-tested past
// the card's right edge — invisible, and unpressable, because Ctx.hovering returns
// false outside c.clipRect. A right-aligned control that silently leaves the card is
// not a visual nit; it is a dead feature.
//
// WHY A CENSUS AND NOT A FIX. The rule is invisible at the call site — the parameter is
// there, it is an int32, and it is even the right KIND of number — so the next tab
// body is one `w` away from the same bug. The census makes the house form a gate: a
// body that draws in window space fails here rather than three waves later on someone's
// 1920 px screen. It reads PRODUCTION dispatch (the switch in drawSettingsTabBody
// decides the roster; this file does not restate it), so a tab added tomorrow joins the
// gate the moment it is dispatched.

import (
	"go/ast"
	"os"
	"strings"
	"testing"
)

// settingsTabBodyCensusMin is the smallest roster this gate accepts before it calls
// itself vacuous. The dispatch carries fourteen tabs today; the floor is deliberately
// well under that, because the point is to catch a harvester that stopped matching
// (a renamed dispatcher, a switch turned into a table), not to restate the count.
const settingsTabBodyCensusMin = 10

// TestEverySettingsTabBodyRebasesToTheCard is that census.
func TestEverySettingsTabBodyRebasesToTheCard(t *testing.T) {
	dispatch := funcBodySource(t, "settings.go", "drawSettingsTabBody")

	// The roster comes out of the dispatch itself.
	var bodies []string
	seen := map[string]bool{}
	ast.Inspect(dispatch, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callName(call)
		if !strings.HasPrefix(name, "drawSettings") || name == "drawSettingsTabBody" || seen[name] {
			return true
		}
		seen[name] = true
		bodies = append(bodies, name)
		return true
	})
	if len(bodies) < settingsTabBodyCensusMin {
		t.Fatalf("the census harvested %d tab bodies out of drawSettingsTabBody, want at least %d — the "+
			"dispatch was reshaped and this gate is now checking almost nothing",
			len(bodies), settingsTabBodyCensusMin)
	}
	if !seen["drawSettingsThemeEditor"] {
		t.Fatal("the Theme editor tab is no longer dispatched by drawSettingsTabBody — it is the body this " +
			"census was written for, and its absence would make the gate pass without covering it")
	}

	decls := packageFuncDecls(t)
	for _, name := range bodies {
		fd := decls[name]
		if fd == nil {
			t.Errorf("drawSettingsTabBody dispatches %s, which no production file in this package declares", name)
			continue
		}
		// Half one: it asks the card for its width at all, in its OWN statement list.
		if !topLevelCalls(fd.Body, "formW2") {
			t.Errorf("%s never calls a.formW2() among its top-level statements — drawSettingsTabBody hands it "+
				"the WINDOW width, and the card is clipped to {formX, formW}, so any right-anchored control in "+
				"this body draws (and hit-tests) outside the clip: on a wide window it is invisible AND "+
				"unpressable", name)
			continue
		}
		// Half two: it does not then go on using the window width anyway. A body that
		// KEEPS its width parameter must overwrite it, at the top level of the body
		// (drawSettingsTheme's shape — it holds the real window width in a second name
		// for the aspect-true preview); one that reads formW2 into a fresh variable
		// while still laying out against the parameter would pass half one and be just
		// as broken.
		p := widthParamName(fd)
		if p == "" || p == "_" {
			continue
		}
		if !topLevelAssignsFromCall(fd.Body, p, "formW2") {
			t.Errorf("%s keeps its width parameter %q without ever assigning it from a.formW2() at the top "+
				"level of its body — the house form is `(y, _ int32)` plus `w := a.formW2()`, or an explicit "+
				"`%s = a.formW2()`; anything else lays the card's rows out in window space", name, p, p)
		}
	}
}

// widthParamName is the name of a tab body's SECOND parameter — the width — or "" when
// it has fewer than two. The signatures are `(y, w int32)` and `(y, w, h int32)`, so
// position is the identity; `_` means the body already refuses the window width.
func widthParamName(fd *ast.FuncDecl) string {
	if fd == nil || fd.Type == nil || fd.Type.Params == nil {
		return ""
	}
	var names []string
	for _, f := range fd.Type.Params.List {
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
	}
	if len(names) < 2 {
		return ""
	}
	return names[1]
}

// THE CENSUS READS A BODY'S OWN STATEMENT LIST, NEVER A NESTED BLOCK, and that
// restriction is the gate rather than an optimisation.
//
// Both halves used to ast.Inspect the whole body, which made the census bypassable by
// the exact edit someone reaches for to silence it: hand the body back its `w`
// parameter and write `if y >= 0 { w := a.formW2(); _ = w }`. The outer w — the window
// width — still laid out every row, and the suite reported ok. A rebase that happens
// only inside a branch is not a rebase; a `:=` inside one is a SHADOW that dies at the
// closing brace. Scanning `fd.Body.List` costs nothing legitimate: all fourteen
// dispatched bodies already use the top-level form, either `w := a.formW2()` or
// drawSettingsTheme's `winW := w` / `w = a.formW2()` pair (settings.go).

// topLevelCalls reports whether one of body's own statements calls fn.
func topLevelCalls(body *ast.BlockStmt, fn string) bool {
	if body == nil {
		return false
	}
	for _, st := range body.List {
		if callsUnnested(st, fn) {
			return true
		}
	}
	return false
}

// topLevelAssignsFromCall reports whether one of body's own statements assigns to the
// identifier target from a right-hand side that calls fn.
func topLevelAssignsFromCall(body *ast.BlockStmt, target, fn string) bool {
	if body == nil {
		return false
	}
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		hit := false
		for _, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == target {
				hit = true
			}
		}
		if !hit {
			continue
		}
		for _, rhs := range as.Rhs {
			if callsUnnested(rhs, fn) {
				return true
			}
		}
	}
	return false
}

// callsUnnested reports whether n contains a call named fn without descending into a
// nested statement body or a function literal. An `if` / `for` / `switch` condition,
// operands and call arguments all still count — only the braces below them are cut, so
// `if c { f() }`, `for { f() }`, `{ f() }` and `x := func() { f() }` all report false.
func callsUnnested(n ast.Node, fn string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if found {
			return false
		}
		switch node.(type) {
		case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause, *ast.FuncLit:
			// A nested body, including the case where n IS a bare block: what runs
			// under those braces does not rebase the statements after them.
			return false
		}
		if call, ok := node.(*ast.CallExpr); ok && callName(call) == fn {
			found = true
			return false
		}
		return true
	})
	return found
}

// packageFuncDecls maps every function and method declared in this package's
// PRODUCTION files to its declaration, parsed once.
//
// Once, because the alternative is re-parsing ~150 files per lookup; production only,
// because a gate that could resolve a name to a test helper would be checking itself.
func packageFuncDecls(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	out := make(map[string]*ast.FuncDecl)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		_, f := parsedFile(t, name)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			// Methods on different receivers can share a name; the settings bodies are
			// all *App and unique, and a collision here would only ever make the gate
			// read the wrong body, so keep the FIRST and let the roster check notice.
			if _, dup := out[fd.Name.Name]; !dup {
				out[fd.Name.Name] = fd
			}
		}
	}
	return out
}
