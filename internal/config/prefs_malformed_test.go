package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// malformedCases are the load-path malformed / adversarial inputs (Order 1A-a): every
// shape the on-disk file can take — unmarshal errors (empty, truncated, BOM, wrong type)
// and valid-JSON-but-needs-normalize (null/array/string roots, duplicate keys, huge or
// negative numbers, unknown keys). Each is run through BOTH the old load() and
// snapshotLoad and compared on error class and whole-struct dump.
func malformedCases() []struct{ name, data string } {
	return []struct{ name, data string }{
		{"empty", ""},
		{"truncated", `{"smoothScaling": tru`},
		{"bom", "\xEF\xBB\xBF" + `{"smoothScaling": true}`},
		{"null_root", "null"},
		{"array_root", "[1,2,3]"},
		{"string_root", `"hello"`},
		{"wrong_type_string_for_int", `{"nameColorVal": "abc"}`},
		{"wrong_type_int_for_bool", `{"smoothScaling": 123}`},
		{"wrong_type_nested", `{"export": {"heightPx": "tall"}}`},
		{"duplicate_keys", `{"smoothScaling": true, "smoothScaling": false}`},
		{"huge_number_clamp", `{"nameColorVal": 1000000000}`},
		{"negative_number_clamp", `{"nameColorVal": -1000000000}`},
		{"unknown_key_root", `{"zzzUnknown": 1}`},
		{"unknown_key_nested", `{"export": {"zzzUnknown": 1}}`},
	}
}

// malformedErrClass is a struct-name-agnostic render of the unmarshal error: the old
// load() decodes into prefsJSON and snapshotLoad into AssetPreferences, so the
// *json.UnmarshalTypeError's Struct field legitimately differs; the Field, Value and
// base Type must not. The old load() wraps the error (fmt.Errorf ... %w), so it is
// unwrapped first.
func malformedErrClass(err error) string {
	for err != nil {
		switch e := err.(type) {
		case *json.SyntaxError:
			// The kind only: encoding/json's message wording and offsets change
			// between Go releases (Go 1.27 reworded the BOM and truncation errors).
			return "syntax"
		case *json.UnmarshalTypeError:
			if e.Field == "" {
				// top-level "cannot unmarshal <value> into Go value of type <struct>";
				// the struct name (prefsJSON vs AssetPreferences) is the swap's whole point.
				return "type: root=" + e.Value
			}
			return "type: " + e.Field + "=" + e.Value + " -> " + strings.TrimPrefix(e.Type.String(), "*")
		}
		err = errors.Unwrap(err)
	}
	return "<nil>"
}

// malformedAllowlist is the explicit set of KNOWN malformed-input differences the
// snapshot does not yet reproduce (Order 1A-a). Key is "case"; each entry is removed by
// the commit that closes it. A difference not listed, or a listed entry that no longer
// differs, both fail.
var malformedAllowlist = map[string]string{}

// TestMalformedParity runs every malformed case through the old load() (a real file) and
// snapshotLoad (the raw bytes) and compares the error class and the whole-struct dump
// (JSON plus the themeFit one-shot flags). The file-level quarantine side-effect is
// pinned separately by TestMalformedQuarantine.
func TestMalformedParity(t *testing.T) {
	cases := malformedCases()
	seen := map[string]bool{}
	var unlisted, stale []string

	for _, c := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "p.json")
		if err := os.WriteFile(path, []byte(c.data), 0o644); err != nil {
			t.Fatal(err)
		}
		oldp, oldErr := load(path)
		newp, newErr := snapshotLoad([]byte(c.data))

		if oldClass, newClass := malformedErrClass(oldErr), malformedErrClass(newErr); oldClass != newClass {
			key := c.name + ".error"
			if _, ok := malformedAllowlist[key]; ok {
				seen[key] = true
			} else {
				unlisted = append(unlisted, key+": old="+oldClass+" new="+newClass)
			}
		} else {
			seen[c.name+".error"] = true
		}

		// struct dump parity (defaults on both sides when unmarshal errors, the
		// normalize output otherwise).
		for _, f := range diffJSON(dumpPrefs(oldp), dumpPrefs(newp), "") {
			key := c.name + "." + f
			if _, ok := malformedAllowlist[key]; ok {
				seen[key] = true
			} else {
				unlisted = append(unlisted, key)
			}
		}
	}

	for k := range malformedAllowlist {
		if !seen[k] {
			stale = append(stale, k)
		}
	}

	for _, d := range unlisted {
		t.Errorf("snapshotLoad differs from load() (not allowlisted): %s", d)
	}
	for _, s := range stale {
		t.Errorf("stale malformed allowlist entry (no longer differs): %s", s)
	}
	if len(unlisted) > 0 || len(stale) > 0 {
		t.Fatalf("%d unlisted + %d stale malformed differences", len(unlisted), len(stale))
	}
}

// TestMalformedQuarantine pins the file-level side effect per malformed case: a case
// whose unmarshal errors must be renamed aside (quarantined) and leave defaults; a case
// whose JSON parses must leave the file in place. This is the old load()'s behaviour the
// switch must preserve verbatim.
func TestMalformedQuarantine(t *testing.T) {
	for _, c := range malformedCases() {
		dir := t.TempDir()
		path := filepath.Join(dir, "p.json")
		if err := os.WriteFile(path, []byte(c.data), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := load(path)

		_, decodeErr := snapshotLoad([]byte(c.data))
		if decodeErr != nil {
			// unmarshal error ⇒ quarantine (rename aside).
			if err == nil {
				t.Errorf("%s: snapshotLoad errors (%v) but load() returned nil error", c.name, decodeErr)
				continue
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Errorf("%s: original must be renamed aside, stat err=%v", c.name, statErr)
			}
		} else {
			if err != nil {
				t.Errorf("%s: valid JSON must not error, got %v", c.name, err)
				continue
			}
			if _, statErr := os.Stat(path); statErr != nil {
				t.Errorf("%s: valid JSON must be left in place, stat err=%v", c.name, statErr)
			}
		}
	}
}

// malformedGoldenName is the frozen oracle for the malformed-input cases: per case the
// struct-name-agnostic error class, whether the old load() quarantined, and the whole
// struct dump. It freezes the old load()'s malformed behaviour so the switch can delete
// the old load() without deleting the oracle.
const malformedGoldenName = "malformed_golden.json"

// malformedCompactCase is the on-disk (compact) form: Output is stored as a delta
// against the default prefs (prefs_default.json).
type malformedCompactCase struct {
	Name        string         `json:"name"`
	Data        string         `json:"data"`
	Error       string         `json:"error"`
	Quarantined bool           `json:"quarantined"`
	OutputDelta map[string]any `json:"output_delta"`
}

// malformedFullCase is the EXPANDED form the old malformed_golden.json stored.
type malformedFullCase struct {
	Name        string         `json:"name"`
	Data        string         `json:"data"`
	Error       string         `json:"error"`
	Quarantined bool           `json:"quarantined"`
	Output      map[string]any `json:"output"`
}

// TestMalformedGolden pins the OLD load()'s malformed-input behaviour as a frozen golden.
// Regenerate with: GOLDEN_REGEN=1 go test ./internal/config -run TestMalformedGolden
func TestMalformedGolden(t *testing.T) {
	cases := malformedCases()
	dir := filepath.Join("testdata")
	goldenPath := filepath.Join(dir, malformedGoldenName)

	if os.Getenv("GOLDEN_REGEN") == "1" {
		defaults := prefsDefaults(t)
		out := make([]malformedCompactCase, 0, len(cases))
		for _, c := range cases {
			p := filepath.Join(t.TempDir(), "p.json")
			if err := os.WriteFile(p, []byte(c.data), 0o644); err != nil {
				t.Fatal(err)
			}
			lp, lerr := load(p)
			out = append(out, malformedCompactCase{c.name, c.data, malformedErrClass(lerr), lerr != nil, prefsDelta(defaults, dumpPrefs(lp))})
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	defaults := prefsDefaults(t)
	var golden []malformedCompactCase
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (regenerate with GOLDEN_REGEN=1)", err)
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	for _, g := range golden {
		_, newErr := snapshotLoad([]byte(g.Data))
		if got := malformedErrClass(newErr); got != g.Error {
			t.Errorf("%s: snapshotLoad error class = %q, want %q", g.Name, got, g.Error)
		}
		newp, _ := snapshotLoad([]byte(g.Data))
		want := applyPrefsDelta(defaults, g.OutputDelta)
		if diffs := diffJSON(dumpPrefs(newp), want, ""); len(diffs) > 0 {
			t.Errorf("%s: snapshotLoad dump differs from golden: %v", g.Name, diffs)
		}
	}
}
