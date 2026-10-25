package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestPrefsDump writes the per-section SHA-256 of what load() and its side effects produce
// for every differential input to the file named by PREFS_DUMP_OUT (test-only; no production
// export). It is self-contained: the corpus is read from the frozen golden data files plus a
// few literal fixtures, so this single file can be dropped into a worktree at any baseline
// commit and the two outputs diffed to prove the load path is byte-identical.
// The per-input sections are: errclass, exported JSON, unexported flags, dirty flag, Save
// bytes, and ImportSettings accept/reject (valid + garbage). Only hashes are ever written,
// never file contents.
func TestPrefsDump(t *testing.T) {
	outPath := os.Getenv("PREFS_DUMP_OUT")
	if outPath == "" {
		t.Skip("set PREFS_DUMP_OUT=<file> to write the per-section SHA-256")
	}

	// The load-golden corpus: each case's decoded input, re-marshalled to raw JSON bytes.
	var loadGolden []struct {
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	}
	prefsDumpReadJSON(t, filepath.Join("testdata", "load_golden.json"), &loadGolden)

	// The malformed corpus: each case's raw data bytes (already a JSON string).
	var malformed []struct {
		Name string `json:"name"`
		Data string `json:"data"`
	}
	prefsDumpReadJSON(t, filepath.Join("testdata", "malformed_golden.json"), &malformed)

	var names []string
	dumps := map[string]string{}
	addLine := func(name, line string) {
		names = append(names, name)
		dumps[name] = line
	}
	dump := func(name string, raw []byte) {
		addLine(name, prefsDumpLine(raw))
	}
	for _, c := range loadGolden {
		raw, err := json.Marshal(c.Input)
		if err != nil {
			t.Fatalf("marshal %s: %v", c.Name, err)
		}
		dump("load_"+c.Name, raw)
	}
	for _, c := range malformed {
		dump("malformed_"+c.Name, []byte(c.Data))
	}
	for _, name := range []string{"prefs_golden", "prefs_default"} {
		raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		dump(name, raw)
	}
	dump("empty_object", []byte("{}"))

	// Setter section: for every setter golden scenario, apply the setter and hash the
	// dirty flag plus the bytes Save would write. The baseline (old inline-field bodies)
	shaHex := func(b []byte) string {
		s := sha256.Sum256(b)
		return hex.EncodeToString(s[:])
	}
	setterApply := func(name, label string, call func(p *AssetPreferences)) {
		base := defaultPrefs("")
		call(base)
		sp := filepath.Join(t.TempDir(), "s.json")
		base.path = sp
		var sb []byte
		if serr := base.SaveNow(); serr != nil {
			sb = []byte("err:" + serr.Error())
		} else {
			sb, _ = os.ReadFile(sp)
		}
		addLine("setter_"+name+"_"+label, fmt.Sprintf("dirty=%v save=%s", base.pending.Load(), shaHex(sb)))
	}
	pt := reflect.TypeOf(&AssetPreferences{})
	var setterNames []string
	for i := 0; i < pt.NumMethod(); i++ {
		if name := pt.Method(i).Name; strings.HasPrefix(name, "Set") {
			setterNames = append(setterNames, name)
		}
	}
	sort.Strings(setterNames)
	for _, name := range setterNames {
		m, _ := pt.MethodByName(name)
		for _, sc := range setterScenarios(m) {
			args := sc.args
			setterApply(name, sc.label, func(p *AssetPreferences) {
				callArgs := append([]reflect.Value{reflect.ValueOf(p)}, args...)
				func() {
					defer func() { _ = recover() }()
					m.Func.Call(callArgs)
				}()
			})
		}
	}
	// Explicit no-op: a setter that changes nothing must not mark dirty.
	setterApply("noop", "unchanged", func(p *AssetPreferences) { p.SetBoldNames(true) })

	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		sb.WriteString(n)
		sb.WriteString("\t")
		sb.WriteString(dumps[n])
		sb.WriteString("\n")
	}
	if err := os.WriteFile(outPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d sections to %s", len(names), outPath)
}

// TestRealPrefsDump prints the same per-section SHA-256 (via prefsDumpLine) for ONE real
// prefs file named by REAL_PREFS. Only hashes are written, never file contents, and the
// file is never written back (prefsDumpLine loads a temp copy). Run it at the baseline and
// at the tip and diff the two lines.
func TestRealPrefsDump(t *testing.T) {
	path := os.Getenv("REAL_PREFS")
	if path == "" {
		t.Skip("set REAL_PREFS=<file> to print the per-section SHA-256 for one real prefs file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	fmt.Printf("path=%s\n", path)
	fmt.Println(prefsDumpLine(raw))
}

func prefsDumpReadJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

// prefsDumpLine runs load() on the raw prefs bytes at a temp path and returns the per-section
// SHA-256, matching the old diffbridge.DebugDiffSections output. The sections are joined by a
// tab so the whole line is a stable, diffable unit.
func prefsDumpLine(raw []byte) string {
	sha := func(b []byte) string {
		s := sha256.Sum256(b)
		return hex.EncodeToString(s[:])
	}

	dir, err := os.MkdirTemp("", "prefsdump")
	if err != nil {
		return "tmpdir-error"
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "p.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "write-error"
	}

	var out []string
	p, lerr := load(path)
	out = append(out, "errclass="+sha([]byte(prefsDumpErrClass(lerr))))
	if p == nil {
		return strings.Join(out, "\t")
	}
	ej, _ := json.Marshal(p.Load())
	out = append(out, "exported="+sha(ej))
	out = append(out, "unexported="+sha([]byte(fmt.Sprintf("moved=%v stamp=%v", p.themeFitMovedToNative, p.themeFitStampPending))))
	out = append(out, "dirty="+sha([]byte(fmt.Sprintf("pending=%v", p.pending.Load()))))

	savePath := filepath.Join(dir, "save.json")
	p.path = savePath
	var sb []byte
	if serr := p.SaveNow(); serr != nil {
		out = append(out, "save="+sha([]byte("err:"+serr.Error())))
	} else {
		sb, _ = os.ReadFile(savePath)
		out = append(out, "save="+sha(sb))
	}

	impSrc := filepath.Join(dir, "imp.json")
	target := filepath.Join(dir, "target.json")
	importCheck := func(name string, contents []byte) {
		_ = os.WriteFile(impSrc, contents, 0o644)
		imp := defaultPrefs("")
		imp.path = target
		ierr := imp.ImportSettings(impSrc)
		var state string
		if ierr == nil {
			tb, _ := os.ReadFile(target)
			state = sha(tb)
		} else {
			state = "rejected"
		}
		out = append(out, name+"="+sha([]byte(fmt.Sprintf("accept=%v state=%s", ierr == nil, state))))
	}
	importCheck("import_valid", sb)
	importCheck("import_garbage", []byte("{ this is not valid json"))

	return strings.Join(out, "\t")
}

// prefsDumpErrClass is a struct-name-agnostic error class (identical to the old scratch
// diffbridge.diffErrClass) so the hash is stable across load()/snapshotLoad implementations.
func prefsDumpErrClass(err error) string {
	if err == nil {
		return "<nil>"
	}
	for err != nil {
		switch e := err.(type) {
		case *json.SyntaxError:
			return "syntax:" + e.Error()
		case *json.UnmarshalTypeError:
			if e.Field == "" {
				return "type:root=" + e.Value
			}
			return "type:" + e.Field + "=" + e.Value + "->" + strings.TrimPrefix(e.Type.String(), "*")
		}
		err = errors.Unwrap(err)
	}
	return "other"
}
