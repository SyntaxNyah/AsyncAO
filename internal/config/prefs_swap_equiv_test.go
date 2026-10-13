package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// diffJSON returns the dotted field paths where two decoded-JSON maps differ. Maps and
// slices are compared structurally; a key present in only one side, or a scalar with a
// different value, is one path.
func diffJSON(a, b map[string]any, prefix string) []string {
	var out []string
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		av, aok := a[k]
		bv, bok := b[k]
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		switch {
		case !aok || !bok:
			out = append(out, path)
		case av == nil || bv == nil:
			if !reflect.DeepEqual(av, bv) {
				out = append(out, path)
			}
		default:
			am, aIsMap := av.(map[string]any)
			bm, bIsMap := bv.(map[string]any)
			if aIsMap && bIsMap {
				out = append(out, diffJSON(am, bm, path)...)
			} else if !reflect.DeepEqual(av, bv) {
				out = append(out, path)
			}
		}
	}
	return out
}

// loadGoldenDump runs the OLD load() on an input and returns the unexported-inclusive
// dump (JSON plus themeFitMovedToNative/themeFitStampPending), so the corpus comparison
// sees the one-shot stamp side-effects too.
func loadGoldenDump(t *testing.T, input map[string]any) map[string]any {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return dumpPrefs(p)
}

// TestSwapCorpusEquivalence drives snapshotLoad against the OLD load() over the whole
// load_golden corpus, field by field, on both the exported JSON and the unexported dump.
// Every difference must be on equivalenceAllowlist (keyed "case.field"); a difference not
// listed, or a listed entry that no longer differs, both fail.
func TestSwapCorpusEquivalence(t *testing.T) {
	cases := loadGoldenCorpus(t)

	seen := map[string]bool{}
	var unlisted []string
	var perCategory = map[string]int{}

	for _, c := range cases {
		oldDump := loadGoldenDump(t, c.input)
		newDump := dumpPrefs(snapshotLoad(mustJSON(t, c.input)))
		for _, f := range diffJSON(oldDump, newDump, "") {
			key := c.name + "." + f
			reason, ok := equivalenceAllowlist[key]
			if !ok {
				unlisted = append(unlisted, key)
				continue
			}
			seen[key] = true
			cat := reason
			if i := strings.IndexByte(reason, ':'); i >= 0 {
				cat = reason[:i]
			}
			perCategory[cat]++
		}
	}

	// stale entries: allowlisted but no longer a difference.
	var stale []string
	for k := range equivalenceAllowlist {
		if !seen[k] {
			stale = append(stale, k)
		}
	}

	var cats []string
	for c := range perCategory {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		t.Logf("allowlist[%s] = %d", c, perCategory[c])
	}

	for _, d := range unlisted {
		t.Errorf("snapshotLoad differs from load() (not allowlisted): %s", d)
	}
	for _, s := range stale {
		t.Errorf("stale allowlist entry (no longer differs): %s", s)
	}
	if len(unlisted) > 0 {
		t.Fatalf("%d corpus differences not on the allowlist", len(unlisted))
	}
	if len(stale) > 0 {
		t.Fatalf("%d stale allowlist entries", len(stale))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
