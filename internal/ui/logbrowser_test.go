package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionLabel(t *testing.T) {
	if got := sessionLabel("2026-06-29_08-42-00.log"); got != "2026-06-29 08:42" {
		t.Errorf("sessionLabel timestamp = %q", got)
	}
	if got := sessionLabel("notes.log"); got != "notes" {
		t.Errorf("sessionLabel plain = %q", got)
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

func TestFilterLogLines(t *testing.T) {
	lines := []logLine{
		{who: "Phoenix", text: "Phoenix: Objection!", lower: "phoenix: objection!"},
		{who: "Edgeworth", text: "Edgeworth: Hold it", lower: "edgeworth: hold it"},
		{who: "Phoenix", text: "Phoenix: Take that", lower: "phoenix: take that"},
	}
	if got := filterLogLines(lines, "", false, ""); len(got) != 3 {
		t.Errorf("empty query matched %d, want 3", len(got))
	}
	if got := filterLogLines(lines, "phoenix", false, ""); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("substring filter = %v", got)
	}
	if got := filterLogLines(lines, "OBJECTION", false, ""); len(got) != 1 || got[0] != 0 {
		t.Errorf("case-insensitive substring = %v", got)
	}
	if got := filterLogLines(lines, "", false, "Edgeworth"); len(got) != 1 || got[0] != 1 {
		t.Errorf("speaker filter = %v", got)
	}
	if got := filterLogLines(lines, "objection|hold", true, ""); len(got) != 2 {
		t.Errorf("regex filter = %v", got)
	}
	if got := filterLogLines(lines, "(unclosed", true, ""); len(got) != 0 {
		t.Errorf("bad regex should fall back to substring (no match) = %v", got)
	}
}

func TestReadLogScope(t *testing.T) {
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
	one := readLogScope(root, "miku.pizza", "")
	if len(one) != 2 {
		t.Fatalf("server scope lines = %d, want 2", len(one))
	}
	if one[0].server != "miku.pizza" || one[0].session != "2026-06-29 08:00" {
		t.Errorf("line tagging = %+v", one[0])
	}

	// One specific session file.
	if got := readLogScope(root, "other", "2026-06-28_10-00-00.log"); len(got) != 1 || got[0].server != "other" {
		t.Errorf("single-session scope = %+v", got)
	}

	// All servers.
	if got := readLogScope(root, "", ""); len(got) != 3 {
		t.Errorf("all-servers scope lines = %d, want 3", len(got))
	}
}
