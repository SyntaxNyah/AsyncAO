package hwid

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var hdidRe = regexp.MustCompile(`^asyncao-[0-9a-f]{64}$`)

const (
	// A server this client might join, and a second one that must never learn
	// anything about the first one's id.
	serverA = "ws://one.example.com:50001"
	serverB = "ws://two.example.com:50001"
)

// For must be stable within a process, correctly shaped, and never empty —
// servers key bans on it, so an unstable or malformed id breaks moderation.
func TestForStableAndShaped(t *testing.T) {
	a := For(serverA)
	if a == "" {
		t.Fatal("empty HDID")
	}
	if !hdidRe.MatchString(a) {
		t.Errorf("HDID %q does not match %v", a, hdidRe)
	}
	if b := For(serverA); a != b {
		t.Errorf("HDID not stable across calls: %q != %q", a, b)
	}
}

// The point of the whole scheme: two servers get two unrelated ids, so an id
// harvested on one is inert on the other. A different port on the same host is
// a different server too (one box commonly runs several).
func TestForDiffersPerServer(t *testing.T) {
	seen := map[string]string{}
	for _, addr := range []string{
		serverA,
		serverB,
		"ws://one.example.com:50002",
		"wss://secure.example.com:2096",
		"", // no address named: its own bucket, not a shared one
	} {
		id := For(addr)
		if !hdidRe.MatchString(id) {
			t.Errorf("For(%q) = %q, malformed", addr, id)
		}
		if prev, dup := seen[id]; dup {
			t.Errorf("For(%q) and For(%q) share the id %q — one server could wear the other's", prev, addr, id)
		}
		seen[id] = addr
	}
}

// A ban has to stick across the ways one address gets spelled: the scheme, the
// case of the hostname and a trailing slash all name the same server, so they
// must all mint the same id. Otherwise reconnecting through wss:// after a ws://
// ban would evade it by accident.
func TestForIgnoresAddressSpelling(t *testing.T) {
	want := For("ws://one.example.com:50001")
	for _, same := range []string{
		"wss://one.example.com:50001",
		"WS://One.Example.COM:50001",
		"ws://one.example.com:50001/",
		"  ws://one.example.com:50001  ",
		"one.example.com:50001",
	} {
		if got := For(same); got != want {
			t.Errorf("For(%q) = %q, want %q — the same server must mint one id", same, got, want)
		}
	}
}

// The device hash is fuel, never a wire value: no id we hand out may contain it
// or equal it, or a server could recover the cross-server identity from what it
// received.
func TestForNeverExposesTheDeviceHash(t *testing.T) {
	root := device()
	if root == "" {
		t.Fatal("empty device hash")
	}
	for _, addr := range []string{serverA, serverB, ""} {
		id := For(addr)
		if strings.Contains(id, root) || id == idPrefix+root {
			t.Errorf("For(%q) = %q leaks the device hash %q", addr, id, root)
		}
	}
}

// device() (the memoised root) and compute() (its un-memoised core) must be
// deterministic: two runs on the same machine produce the same value — that is
// what makes a ban stick.
func TestDeviceDeterministic(t *testing.T) {
	if first, second := compute(), compute(); first != second {
		t.Errorf("compute() is non-deterministic: %q != %q", first, second)
	}
	if first, second := device(), device(); first != second {
		t.Errorf("device() is non-deterministic: %q != %q", first, second)
	}
}

// roots() must not panic and must be deterministic; on a normal machine it reads
// at least one stable root, but a bare environment legitimately has none (then
// compute() uses the hostname), so an empty result is not a failure.
func TestRootsDeterministic(t *testing.T) {
	first, second := roots(), roots()
	if len(first) != len(second) {
		t.Errorf("roots() changed between calls: %d != %d", len(first), len(second))
	}
	for _, r := range first {
		if !strings.Contains(r, "=") {
			t.Errorf("root %q is not label=value", r)
		}
	}
}

// The seam: this package may only hand out ids that are bound to a server. That
// is enforced by what it exports — an exported function taking no server address
// would be a way back to one id for every server, which is exactly the hole the
// per-server scheme closes.
//
// This reads the package's own sources rather than calling anything, because the
// property is about the API SHAPE: a re-added Compute() would compile, pass every
// behavioural test above, and quietly restore the shared id. Adding an export
// here fails loudly and forces the decision to be argued.
func TestOnlyServerBoundIDsAreExported(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob this package's sources: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no sources found — the export gate would pass vacuously")
	}
	exported := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !fd.Name.IsExported() {
				continue
			}
			exported[fd.Name.Name] = true
			if fd.Recv != nil {
				t.Errorf("%s: exported method %s — this package hands out ids through functions only", name, fd.Name.Name)
				continue
			}
			if fd.Type.Params.NumFields() == 0 {
				t.Errorf("%s: exported func %s takes no server address, so it can only return one id for every server — bind it to a server or unexport it", name, fd.Name.Name)
			}
		}
	}
	if !exported["For"] {
		t.Error("For is gone: the sanctioned way to get an HDID must exist, or its callers found another one")
	}
	for name := range exported {
		if name != "For" {
			t.Errorf("unexpected export %s: every id this package hands out goes through For, so callers cannot obtain the cross-server device hash", name)
		}
	}
}
