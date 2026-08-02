package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// userTextDrawSites are the functions that paint text AsyncAO did not author —
// player names, saved shownames, server names. Every one of them was reported
// drawing tofu boxes for a name the fixed chrome face could not cover, while the
// SAME string rendered correctly in the chatbox and the log, which had already
// been moved onto the per-glyph path.
//
// A single face cannot draw a string whose glyphs live in two faces, so the font
// census (which fixes which face is PICKED) can never fix these on its own. They
// must go through a covering path: labelEmoji for App-side draws,
// labelCoveringCentered for the widget kit.
var userTextDrawSites = []string{
	"drawPlayerRow",         // the roster: [uid] showname · character, and the OOC/IPID sub-line
	"dropdownEx",            // the closed control — the showname picker's current pick
	"labelCoveringCentered", // the kit's covering path itself (must not fall back silently)
}

// singleFaceLabels are the draw calls that resolve exactly ONE face and blit it.
// They are correct for AsyncAO's own ASCII chrome and wrong for anything a user
// or a server supplies.
var singleFaceLabels = []string{
	"LabelClippedFont",
	"LabelClipped",
}

// TestUserTextSurfacesUseACoveringFace pins the bug CLASS rather than the three
// surfaces that happened to get reported: if someone adds a roster column or a
// dropdown row and reaches for the single-face label, this fails and names the
// covering call to use instead.
//
// blitCenteredLabel is deliberately allowed inside labelCoveringCentered — it IS
// that function's plain-text fast path, reached only after the covering gate has
// already decided the face can draw the whole string.
func TestUserTextSurfacesUseACoveringFace(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse ui package: %v", err)
	}
	want := map[string]bool{}
	for _, fn := range userTextDrawSites {
		want[fn] = false
	}
	for _, p := range pkg {
		for _, file := range p.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok || fn.Name == nil {
					return true
				}
				if _, tracked := want[fn.Name.Name]; !tracked {
					return true
				}
				want[fn.Name.Name] = true
				ast.Inspect(fn.Body, func(in ast.Node) bool {
					call, ok := in.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					for _, banned := range singleFaceLabels {
						if sel.Sel.Name == banned {
							t.Errorf("%s calls %s at %s — that resolves ONE face, so a name needing two "+
								"renders as tofu boxes. Use labelEmoji (App) or labelCoveringCentered (Ctx).",
								fn.Name.Name, banned, fset.Position(call.Pos()))
						}
					}
					return true
				})
				return true
			})
		}
	}
	for fn, found := range want {
		if !found {
			t.Errorf("user-text draw site %q no longer exists — update userTextDrawSites so the gate keeps covering it", fn)
		}
	}
}
