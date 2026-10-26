package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestApplySide pins the position-change fix the playtester asked for: a
// user-initiated pos change sets OUR side AND forwards "/pos <side>" to the server
// (so the move is instant, like typing it in the OOC box), lowercased + trimmed,
// with empty and same-side picks as no-ops that send nothing.
func TestApplySide(t *testing.T) {
	var sent []protocol.Packet
	s := courtroom.NewSession(func(p protocol.Packet) error { sent = append(sent, p); return nil }, "")
	a := &App{}
	a.sess = s

	// A real change lowercases, stores, and fires exactly one /pos over OOC (CT).
	a.applySide("  Def ")
	if a.sidePref != "def" {
		t.Fatalf("sidePref = %q, want %q", a.sidePref, "def")
	}
	if len(sent) != 1 || sent[0].Header != "CT" || len(sent[0].Fields) < 2 || sent[0].Fields[1] != "/pos def" {
		t.Fatalf("sent = %+v, want one CT carrying \"/pos def\"", sent)
	}

	// Re-picking the SAME side is a no-op — no second send.
	a.applySide("def")
	if len(sent) != 1 {
		t.Fatalf("same-side re-pick re-sent: %+v", sent)
	}

	// Empty / whitespace-only is a no-op and never sends.
	a.applySide("   ")
	if a.sidePref != "def" || len(sent) != 1 {
		t.Fatalf("empty pick changed state: sidePref=%q sent=%+v", a.sidePref, sent)
	}

	// A different side sends again.
	a.applySide("pro")
	if a.sidePref != "pro" || len(sent) != 2 || sent[1].Fields[1] != "/pos pro" {
		t.Fatalf("second change wrong: sidePref=%q sent=%+v", a.sidePref, sent)
	}
}

// TestApplySideNilSession ensures a pos change with no live session just sets the
// local side (and never panics) — the offline / lobby path.
func TestApplySideNilSession(t *testing.T) {
	a := &App{}
	a.applySide("wit")
	if a.sidePref != "wit" {
		t.Fatalf("sidePref = %q, want wit", a.sidePref)
	}
}
