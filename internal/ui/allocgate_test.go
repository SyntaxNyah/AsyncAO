package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// The measurement every zero-alloc draw gate runs on.
//
// WHAT WENT WRONG. These gates used to warm up through a settle() helper that
// rendered 20-frame batches until one allocated nothing, and then asserted over a
// 200-frame batch. Two different window sizes, and testing.AllocsPerRun divides
// the GLOBAL malloc delta by the window and TRUNCATES — so twenty quiet frames
// never implied two hundred. Anything that allocated fewer than 20 objects per
// 20-frame batch cleared settle, and the same one-shot event landing inside the
// 200-frame assert reported 1.0/op and failed the gate.
//
// WHAT IT ACTUALLY WAS, measured rather than assumed. Under the full package a
// settled drawCourtroom reads 85–136/op in the FIRST window after settle and 0 in
// every window after it — one burst of 17,000–27,000 objects, not a per-frame
// cost. Run the same fixture alone and 40 consecutive windows read 0 from cold. A
// control that burns the same wall clock per "frame" and cannot allocate reads 0
// in every window of the loaded run, so those mallocs are the draw's own goroutine
// and not a background one leaking into the window. The burst needs the loaded
// package to appear at all and roughly a second of unrelated work absorbs it,
// which is the signature of a shared capacity-bounded cache churning until this
// fixture's working set wins it back — a warm-up. (Which cache is NOT pinned:
// every instrument fine-grained enough to catch the stack — MemProfileRate=1 —
// perturbs it out of existence. 80,000 profiled frames caught nothing.)
//
// The symptom, for the record: CI red on docs-only commits from 2026-08-10 to
// 2026-08-29 (33013b3, 8ca21a6, dfd514d), and TestDrawCourtroomEggZeroAlloc
// flaking at ~5% per arm for far longer.
//
// WHY THIS IS NOT LOOSENING THE GATE. A per-frame allocation is in EVERY window by
// definition, so it can never produce a clean one: allocsPerFrame below still
// fails it on the first reading. Only an event that is over by the next window can
// come back 0, and that event is exactly what the gate was never meant to catch.
// The detection power is identical — truncating division already meant anything
// under one allocation per frame read 0 — and the false failures are gone.

const (
	// allocGateFrames is the window a whole-screen gate measures. Named so the
	// settle loop and the assertion cannot drift to different sizes again; that
	// drift WAS the bug.
	allocGateFrames = 200

	// editorAllocFrames is the shorter window the theme editor's gates use: they
	// assert against a per-frame chrome BUDGET rather than zero, so they do not
	// need the long window to resolve one allocation per frame.
	editorAllocFrames = 30

	// allocGateWindows bounds how many windows may be spent looking for a clean
	// one. A leak fails on the first, so this only costs anything when a warm-up
	// is in flight; the worst run observed needed two windows, and eight leaves
	// room without letting a genuinely dirty gate spin.
	allocGateWindows = 8
)

// allocsPerFrame reports what one settled frame of draw allocates. It measures
// windows of frames frames until one comes in at or under budget, and returns the
// LOWEST reading it saw.
//
// A non-zero return therefore means every one of allocGateWindows windows was
// dirty, which is a per-frame cost and not a warm-up. Callers keep their own
// failure messages: what they assert has not changed.
func allocsPerFrame(frames int, budget float64, draw func()) float64 {
	var lowest float64
	for i := 0; i < allocGateWindows; i++ {
		n := testing.AllocsPerRun(frames, draw)
		if n <= budget {
			return n
		}
		if i == 0 || n < lowest {
			lowest = n
		}
	}
	return lowest
}

// allocGateSink keeps the contract tests' allocations escaping and alive.
var allocGateSink any

// oneShotBurstFrame is the frame a staged warm-up fires on: early enough to land
// inside the first measured window, past AllocsPerRun's single warm-up call.
const oneShotBurstFrame = 5

// stagedOneShotBurst returns a draw that allocates NOTHING per frame and fires a
// single burst — larger than the window, so truncating division still reports it
// — on one early frame, the shape a cache warming up has.
func stagedOneShotBurst() func() {
	frame := 0
	return func() {
		frame++
		if frame == oneShotBurstFrame {
			for i := 0; i < allocGateFrames*3; i++ {
				allocGateSink = new(int)
			}
		}
	}
}

// TestAllocGateStillFailsAPerFrameAllocation is the half that matters: the loop
// must not be a way to make a real leak disappear. One object per frame is in
// every window, so no amount of retrying can find a clean one.
func TestAllocGateStillFailsAPerFrameAllocation(t *testing.T) {
	leak := func() { allocGateSink = new(int) }
	if n := allocsPerFrame(allocGateFrames, 0, leak); n < 1 {
		t.Fatalf("allocsPerFrame reported %.1f/op for a draw that allocates once per frame, want >= 1 — "+
			"the retry loop is masking exactly the bug these gates exist to catch", n)
	}
}

// TestAllocGateIgnoresAOneShotWarmUp is the other half, and pins the behaviour
// change. The same staged burst is measured twice: once the old way (a single
// window, which is what every gate used to do after settle) and once through the
// helper. The old way fails, the helper passes. If the first assert ever stops
// failing, this fixture no longer reproduces the flake and the second one has
// stopped proving anything.
func TestAllocGateIgnoresAOneShotWarmUp(t *testing.T) {
	if n := testing.AllocsPerRun(allocGateFrames, stagedOneShotBurst()); n < 1 {
		t.Fatalf("a single window over a one-shot burst read %.1f/op, want >= 1 — the fixture no longer "+
			"reproduces what turned CI red, so the gate below proves nothing", n)
	}
	if n := allocsPerFrame(allocGateFrames, 0, stagedOneShotBurst()); n != 0 {
		t.Fatalf("allocsPerFrame reported %.1f/op for a draw whose only allocation was a one-shot "+
			"warm-up, want 0 — the loop is not outlasting the burst", n)
	}
}

// TestAllocGateHonoursANonZeroBudget covers the editor gates' arm: they pass a
// per-frame chrome budget rather than 0, and a window at or under it has to end
// the search just as a clean one does.
func TestAllocGateHonoursANonZeroBudget(t *testing.T) {
	const budget = 2
	twoPerFrame := func() {
		allocGateSink = new(int)
		allocGateSink = new(int)
	}
	if n := allocsPerFrame(editorAllocFrames, budget, twoPerFrame); n > budget {
		t.Fatalf("allocsPerFrame reported %.1f/op against a budget of %d — a draw inside its budget must "+
			"settle, not burn every window", n, budget)
	}
}

// wholeScreenDraws are the draw entry points whose gates must go through
// allocsPerFrame. Measuring one of these in a bare testing.AllocsPerRun call is
// the exact shape that flaked: a single window over a screen big enough for a
// warm-up to hide in.
var wholeScreenDraws = []string{"drawCourtroom", "drawLobby", "driveFrame"}

// TestWholeScreenGatesGoThroughAllocsPerFrame is the encapsulation gate. The
// helper only earns its keep while it is the ONE way a whole-screen draw is
// measured; a new gate written the old way — settle-and-assert, or just a bare
// window — brings the flake straight back with every test here still green,
// because they would be exercising a helper that gate does not call.
//
// It resolves each bare window's SECOND argument, following a local closure
// (draw := func() { a.drawCourtroom(w, h) }) to its body, and only complains when
// a whole-screen draw is what that window actually measures. Judging the enclosing
// function instead would condemn a file for a neighbouring gate that measures one
// panel — themereportpanel_test.go does exactly that, legitimately.
func TestWholeScreenGatesGoThroughAllocsPerFrame(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no test files found — this gate is reading the wrong directory")
	}
	fset := token.NewFileSet()
	var offenders []string
	for _, path := range files {
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			closures := drawClosures(fn.Body)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isAllocsPerRun(call) || len(call.Args) < 2 {
					return true
				}
				if draw := wholeScreenDrawIn(call.Args[1], closures); draw != "" {
					offenders = append(offenders, fset.Position(call.Pos()).String()+" ("+fn.Name.Name+" measures "+draw+")")
				}
				return true
			})
		}
	}
	if len(offenders) != 0 {
		t.Errorf("these gates measure a whole-screen draw with a bare testing.AllocsPerRun window:\n  %s\n"+
			"use allocsPerFrame instead — one window cannot tell a shipped per-frame allocation from a "+
			"cache warming up, and that ambiguity is what kept CI red",
			strings.Join(offenders, "\n  "))
	}
}

// isAllocsPerRun reports whether call is testing.AllocsPerRun(...).
func isAllocsPerRun(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "AllocsPerRun" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing"
}

// drawClosures indexes every `name := func() { ... }` in body, so a window handed
// a bare identifier can still be traced to what it draws.
func drawClosures(body *ast.BlockStmt) map[string]*ast.FuncLit {
	out := map[string]*ast.FuncLit{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || i >= len(as.Rhs) {
				continue
			}
			if lit, ok := as.Rhs[i].(*ast.FuncLit); ok {
				out[id.Name] = lit
			}
		}
		return true
	})
	return out
}

// wholeScreenDrawIn names the whole-screen draw arg reaches, or "" for none. An
// inline literal is read directly; a bare identifier is resolved through closures.
func wholeScreenDrawIn(arg ast.Expr, closures map[string]*ast.FuncLit) string {
	var body ast.Node = arg
	if id, ok := arg.(*ast.Ident); ok {
		lit, ok := closures[id.Name]
		if !ok {
			return ""
		}
		body = lit
	}
	found := ""
	ast.Inspect(body, func(n ast.Node) bool {
		name := ""
		switch v := n.(type) {
		case *ast.SelectorExpr:
			name = v.Sel.Name
		case *ast.Ident:
			name = v.Name
		default:
			return true
		}
		for _, draw := range wholeScreenDraws {
			if name == draw {
				found = draw
			}
		}
		return true
	})
	return found
}
