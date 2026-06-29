package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestComputeLogStats(t *testing.T) {
	lines := []logLine{
		{who: "Phoenix", text: "[t] Phoenix: hello there", session: "s1"},
		{who: "Phoenix", text: "[t] Phoenix: objection", session: "s1"},
		{who: "Maya", text: "[t] Maya: hi", session: "s2"},
	}
	stats, tl, tw, sess := computeLogStats(lines)
	if tl != 3 || sess != 2 || tw == 0 {
		t.Errorf("totals: lines=%d words=%d sessions=%d", tl, tw, sess)
	}
	if len(stats) != 2 || stats[0].name != "Phoenix" || stats[0].lines != 2 {
		t.Errorf("stats = %+v (want Phoenix with 2 lines first)", stats)
	}
}

func TestBuildModcallClip(t *testing.T) {
	now := time.Date(2026, 6, 29, 14, 32, 0, 0, time.UTC)
	log := []icEntry{
		{text: "Phoenix: one", stamp: "14:30"},
		{text: "Maya: two", stamp: "14:31"},
		{text: "Edgeworth: three", stamp: "14:32"},
	}
	// n caps to the last 2 lines; header is 5 lines.
	got := buildModcallClip("miku.pizza", "Phoenix called a mod: spam", log, 2, now)
	if len(got) != 7 {
		t.Fatalf("clip lines = %d, want 7 (5 header + 2 IC)\n%q", len(got), got)
	}
	if got[0] != "AsyncAO modcall clip" || got[1] != "Server : miku.pizza" {
		t.Errorf("header = %q", got[:2])
	}
	if got[3] != "Notice : Phoenix called a mod: spam" {
		t.Errorf("notice line = %q", got[3])
	}
	// Only the last 2 IC lines, with timestamps, in order.
	if got[5] != "[14:31] Maya: two" || got[6] != "[14:32] Edgeworth: three" {
		t.Errorf("IC tail = %q", got[5:])
	}
	// n larger than the log keeps every line (no panic / negative slice).
	if all := buildModcallClip("s", "x", log, 99, now); len(all) != 5+3 {
		t.Errorf("oversized n: lines = %d, want 8", len(all))
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
