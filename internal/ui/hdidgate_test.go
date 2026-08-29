package ui

// The wiring gate for the per-server HDID.
//
// WHY SOURCE AND NOT BEHAVIOUR (the srcgate_test.go rationale, applied here): the
// property is "the id we hand a session is the one derived for the server that
// session is connecting to". A behavioural test can only see the id a session got,
// and every wrong answer is a well-shaped id too — a constant, a cached one from
// the previous tab, the same value for both tabs. What makes it right is that the
// derivation reads the address being dialled, at the call, and that is placement.
//
// These are deletion-catchers, not mirrors: they contain no copy of the hashing
// and would go red if the funnel were bypassed, not if it changed shape.

import (
	"go/ast"
	"testing"
)

// hdidFuncDecl returns the declaration (not just the body) of the named function
// in file, so a gate can read its PARAMETER names as well as its statements.
func hdidFuncDecl(t *testing.T, file, fn string) *ast.FuncDecl {
	t.Helper()
	_, f := parsedFile(t, file)
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Name.Name == fn && fd.Body != nil {
			return fd
		}
	}
	t.Fatalf("%s: no function %s — it was renamed or removed, and its wiring gate now checks nothing", file, fn)
	return nil
}

// firstParamName is the name of a function's first parameter.
func firstParamName(t *testing.T, fd *ast.FuncDecl) string {
	t.Helper()
	if fd.Type.Params.NumFields() == 0 || len(fd.Type.Params.List[0].Names) == 0 {
		t.Fatalf("%s takes no named first parameter", fd.Name.Name)
	}
	return fd.Type.Params.List[0].Names[0].Name
}

// identAssignedToField returns the plain identifier assigned to `<x>.field`
// inside n, or "" when the field is never assigned one. It lets a gate learn what
// the code CALLS the value it treats as a given server's identity, instead of
// hard-coding a variable name.
func identAssignedToField(n ast.Node, field string) string {
	name := ""
	ast.Inspect(n, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		sel, ok := as.Lhs[0].(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != field {
			return true
		}
		if id, ok := as.Rhs[0].(*ast.Ident); ok {
			name = id.Name
		}
		return name == ""
	})
	return name
}

// The session that talks to a server must be built with the id derived FOR that
// server's address — the same address the rest of the per-server state is keyed
// by (a.serverKey). Deriving it from anything else (a bare hdid(), a package
// variable, a remembered value) fails here, and so does a tab whose id comes from
// a different server than its preferences do.
func TestTheSessionGetsTheIDForTheServerItDials(t *testing.T) {
	fd := hdidFuncDecl(t, "app.go", "connectWith")
	url := identAssignedToField(fd.Body, "serverKey")
	if url == "" {
		t.Fatal("connectWith no longer assigns a plain identifier to serverKey — the gate cannot tell which value names this server")
	}
	calls := callsNamed(fd.Body, "NewSession")
	if len(calls) != 1 {
		t.Fatalf("connectWith makes %d NewSession calls, want 1 — the gate below checks only what it can see", len(calls))
	}
	args := calls[0].Args
	if len(args) == 0 {
		t.Fatal("NewSession is called with no arguments")
	}
	last := args[len(args)-1]
	call, ok := last.(*ast.CallExpr)
	if !ok || callName(call) != "hdid" {
		t.Fatalf("NewSession's id argument is not an hdid(...) call — a session must never carry an id from anywhere else")
	}
	if len(call.Args) == 0 {
		t.Fatal("hdid is called with no server address: the id would be the same on every server, which is the replay hole the per-server scheme closes")
	}
	if !mentionsIdent(call.Args[0], url) {
		t.Errorf("hdid's argument does not mention %s — the id must be derived from the address being dialled, not from remembered state", url)
	}
}

// One funnel: hdid is the only place in this package that asks internal/hwid for
// an id, and it passes its own parameter through. A second call site could ask
// with a different address (or a constant) and put a mismatched id on the wire.
func TestOnlyHdidAsksForAnID(t *testing.T) {
	seen := false
	packageFuncs(t, func(file, fn string, body *ast.BlockStmt) {
		calls := callsNamed(body, "For")
		if len(calls) == 0 {
			return
		}
		if fn != "hdid" {
			t.Errorf("%s: %s calls hwid.For — every id goes through hdid, so all of them are derived the same way", file, fn)
			return
		}
		seen = true
		fd := hdidFuncDecl(t, file, fn)
		param := firstParamName(t, fd)
		for _, c := range calls {
			if len(c.Args) == 0 || !mentionsIdent(c.Args[0], param) {
				t.Errorf("%s: hdid does not pass its %s parameter to hwid.For", file, param)
			}
		}
	})
	if !seen {
		t.Error("no function calls hwid.For: the client would be sending an id it did not derive, or none at all")
	}
}
