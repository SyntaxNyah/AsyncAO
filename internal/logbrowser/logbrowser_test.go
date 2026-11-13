package logbrowser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

func TestSessionLabel(t *testing.T) {
	if got := SessionLabel("2026-06-29_08-42-00.log"); got != "2026-06-29 08:42" {
		t.Errorf("SessionLabel timestamp = %q", got)
	}
	if got := SessionLabel("notes.log"); got != "notes" {
		t.Errorf("SessionLabel plain = %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("no-trunc = %q", got)
	}
	if got := truncateRunes("hello", 3); got != "hel…" {
		t.Errorf("trunc = %q", got)
	}
}

func TestFilterLines(t *testing.T) {
	lines := []Line{
		{Who: "Phoenix", Text: "Phoenix: Objection!", Lower: "phoenix: objection!"},
		{Who: "Edgeworth", Text: "Edgeworth: Hold it", Lower: "edgeworth: hold it"},
		{Who: "Phoenix", Text: "Phoenix: Take that", Lower: "phoenix: take that"},
	}
	if got := FilterLines(lines, "", false, ""); len(got) != 3 {
		t.Errorf("empty query matched %d, want 3", len(got))
	}
	if got := FilterLines(lines, "phoenix", false, ""); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("substring filter = %v", got)
	}
	if got := FilterLines(lines, "OBJECTION", false, ""); len(got) != 1 || got[0] != 0 {
		t.Errorf("case-insensitive substring = %v", got)
	}
	if got := FilterLines(lines, "", false, "Edgeworth"); len(got) != 1 || got[0] != 1 {
		t.Errorf("speaker filter = %v", got)
	}
	if got := FilterLines(lines, "objection|hold", true, ""); len(got) != 2 {
		t.Errorf("regex filter = %v", got)
	}
	if got := FilterLines(lines, "(unclosed", true, ""); len(got) != 0 {
		t.Errorf("bad regex should fall back to substring (no match) = %v", got)
	}
}

func TestComputeStats(t *testing.T) {
	lines := []Line{
		{Who: "Phoenix", Text: "[t] Phoenix: hello there", Session: "s1"},
		{Who: "Phoenix", Text: "[t] Phoenix: objection", Session: "s1"},
		{Who: "Maya", Text: "[t] Maya: hi", Session: "s2"},
	}
	stats, tl, tw, sess := ComputeStats(lines)
	if tl != 3 || sess != 2 || tw == 0 {
		t.Errorf("totals: lines=%d words=%d sessions=%d", tl, tw, sess)
	}
	if len(stats) != 2 || stats[0].Name != "Phoenix" || stats[0].Lines != 2 {
		t.Errorf("stats = %+v (want Phoenix with 2 lines first)", stats)
	}
}

func TestLoadScope(t *testing.T) {
	root := t.TempDir()
	mk := func(server, file, body string) {
		dir := filepath.Join(root, server)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("miku.pizza", "2026-06-29_08-00-00.log", "[t] Phoenix: hi\n\n[t] Maya: hello\n")
	mk("other", "2026-06-28_10-00-00.log", "[t] Judge: order\n")

	// One server, all its sessions (the blank line is skipped).
	one := Load(root, "miku.pizza", "", 0)
	if len(one.Lines) != 2 {
		t.Fatalf("server scope lines = %d, want 2", len(one.Lines))
	}
	if one.Lines[0].Server != "miku.pizza" || one.Lines[0].Session != "2026-06-29 08:00" {
		t.Errorf("line tagging = %+v", one.Lines[0])
	}

	// One specific session file.
	if got := Load(root, "other", "2026-06-28_10-00-00.log", 0); len(got.Lines) != 1 || got.Lines[0].Server != "other" {
		t.Errorf("single-session scope = %+v", got)
	}

	// All servers.
	if got := Load(root, "", "", 0); len(got.Lines) != 3 {
		t.Errorf("all-servers scope lines = %d, want 3", len(got.Lines))
	}
}

// TestLoadStripsTheSidechannel is the behavioural form of the log-browser display
// lane: a transcript line carrying a sprite-style marker (the zero-width codec runes
// the writer appends to styled messages) must come back from Load with the marker
// gone. The browser reads HISTORY — logs written by older builds that still carry
// the runes — so the strip has to happen at read time, not just at write time.
func TestLoadStripsTheSidechannel(t *testing.T) {
	marker := courtroom.SpriteStyle{Glow: true}.EncodeMarker()
	root := t.TempDir()
	dir := filepath.Join(root, "miku.pizza")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One marker on the line's tail and one mid-text, so the strip is proven to drop
	// the codec wherever it sits, not just trailing.
	body := "[t] Phoenix: hi" + marker + "\n[t] Phoenix: objection" + marker + "!\n"
	if err := os.WriteFile(filepath.Join(dir, "2026-06-29_08-00-00.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Load(root, "miku.pizza", "", 0)
	if len(res.Lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(res.Lines))
	}
	if got, want := res.Lines[0].Text, "[t] Phoenix: hi"; got != want {
		t.Errorf("line 0 = %q, want %q — the trailing marker was not stripped", got, want)
	}
	if got, want := res.Lines[1].Text, "[t] Phoenix: objection!"; got != want {
		t.Errorf("line 1 = %q, want %q — the mid-text marker was not stripped", got, want)
	}
}
