package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestNotebookRoundTrip pins the notebook contract: add/remove/caps,
// atomic flush, and reload from disk.
func TestNotebookRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nb.json")
	nb := &Notebook{path: path}

	nb.Add("  first pin  ")
	nb.Add(strings.Repeat("x", notebookLineMax+100)) // truncates
	nb.Add("third")
	nb.Remove(2)
	nb.Remove(99) // out of range: no-op

	if got := nb.Lines(); len(got) != 2 || got[0] != "first pin" || len(got[1]) != notebookLineMax {
		t.Fatalf("lines = %d entries (first %q, second len %d)", len(got), got[0], len(got[1]))
	}
	if err := nb.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	again := loadNotebookFile(path)
	if again.Len() != 2 || again.Lines()[0] != "first pin" {
		t.Fatalf("reload = %d lines, want the flushed 2", again.Len())
	}
}

// TestNotebookCap pins the bound: the oldest pins fall off past the cap.
func TestNotebookCap(t *testing.T) {
	nb := &Notebook{path: filepath.Join(t.TempDir(), "nb.json")}
	for i := 0; i < notebookLineCap+50; i++ {
		nb.Add(strings.Repeat("a", 4) + string(rune('0'+i%10)))
	}
	if nb.Len() != notebookLineCap {
		t.Fatalf("len = %d, want cap %d", nb.Len(), notebookLineCap)
	}
}

// TestWardrobePerServer pins the per-server split: lists never leak
// between servers, and the legacy flat list migrates exactly once to the
// first server that claims it.
func TestWardrobePerServer(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.Wardrobe = []string{"Phoenix", "Edgeworth"} // legacy flat list

	p.ClaimLegacyWardrobe("wss://main.example:2096")
	if got := p.WardrobeList("wss://main.example:2096"); len(got) != 2 {
		t.Fatalf("first server must inherit the legacy list, got %v", got)
	}
	if got := p.WardrobeList("wss://other.example:2096"); len(got) != 0 {
		t.Fatalf("other servers start clean, got %v", got)
	}
	p.ClaimLegacyWardrobe("wss://other.example:2096") // no-op: already claimed
	if got := p.WardrobeList("wss://other.example:2096"); len(got) != 0 {
		t.Fatalf("legacy list must migrate exactly once, got %v", got)
	}

	if !p.AddWardrobe("wss://other.example:2096", "Franziska") {
		t.Fatal("add to a fresh server wardrobe must succeed")
	}
	if !p.RemoveWardrobe("wss://main.example:2096", "phoenix") {
		t.Fatal("remove is case-insensitive within one server")
	}
	if got := p.WardrobeList("wss://main.example:2096"); len(got) != 1 || got[0] != "Edgeworth" {
		t.Fatalf("main wardrobe after remove = %v", got)
	}
}

// TestCharKeyBinds pins the per-server keybind table: set/overwrite/
// clear, isolation between servers, and the cap.
func TestCharKeyBinds(t *testing.T) {
	p, _ := newTestPrefs(t)
	const srv = "wss://main.example:2096"
	p.SetCharKeyBind(srv, "A", "Phoenix") // key names normalize lowercase
	p.SetCharKeyBind(srv, "b", "Edgeworth")
	p.SetCharKeyBind(srv, "b", "Franziska") // overwrite
	p.SetCharKeyBind(srv, "a", "")          // clear

	binds := p.CharKeyBinds(srv)
	if len(binds) != 1 || binds["b"] != "Franziska" {
		t.Fatalf("binds = %v, want only b→Franziska", binds)
	}
	if other := p.CharKeyBinds("wss://other.example:2096"); other != nil {
		t.Fatalf("keybinds leaked across servers: %v", other)
	}
	for i := 0; i < charKeyCap+10; i++ {
		p.SetCharKeyBind(srv, string(rune('a'))+string(rune('0'+i%10))+string(rune('a'+i%26)), "X")
	}
	if got := len(p.CharKeyBinds(srv)); got > charKeyCap {
		t.Fatalf("keybind table exceeded cap: %d > %d", got, charKeyCap)
	}
}

// TestNotebookPathDistinct pins the filename scheme: long server keys
// sharing a truncated prefix still map to distinct files (hash suffix).
func TestNotebookPathDistinct(t *testing.T) {
	long := "wss://very-long-server-host-name-that-truncates.example.com:2096/extra"
	a, err := NotebookPath(long + "/a")
	if err != nil {
		t.Skip("no user config dir in this environment")
	}
	b, _ := NotebookPath(long + "/b")
	if a == b {
		t.Fatalf("distinct servers mapped to one notebook file: %s", a)
	}
	if filepath.Ext(a) != ".json" {
		t.Fatalf("notebook files are JSON, got %s", a)
	}
}
