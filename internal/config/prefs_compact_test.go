package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// prefsDelta returns the subset of m that differs from def, recursing into nested
// maps. Scalars, arrays and pointers are atomic: any difference includes the whole
// value. This is the compact form the frozen golden data files are stored in; the
// expansion tests reconstruct the exact old bytes and pin the OLD SHA-256.
func prefsDelta(def, m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		dv, ok := def[k]
		if !ok {
			out[k] = v
			continue
		}
		if vm, ok := v.(map[string]any); ok {
			if dm, ok2 := dv.(map[string]any); ok2 {
				if sub := prefsDelta(dm, vm); len(sub) > 0 {
					out[k] = sub
				}
				continue
			}
		}
		if !reflect.DeepEqual(v, dv) {
			out[k] = v
		}
	}
	return out
}

// applyPrefsDelta overlays delta onto def (recursively) and returns the full map.
func applyPrefsDelta(def, delta map[string]any) map[string]any {
	out := make(map[string]any, len(def)+len(delta))
	for k, v := range def {
		out[k] = v
	}
	for k, v := range delta {
		if vm, ok := v.(map[string]any); ok {
			if dm, ok := out[k].(map[string]any); ok {
				out[k] = applyPrefsDelta(dm, vm)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func prefsDefaults(t *testing.T) map[string]any {
	t.Helper()
	return decodeJSON(t, filepath.Join("testdata", defaultPrefsName))
}

// TestLoadGoldenExpansion reconstructs the OLD load_golden.json bytes from the compact
// delta form and asserts the frozen SHA-256, so compaction can never silently drop a byte.
func TestLoadGoldenExpansion(t *testing.T) {
	defaults := prefsDefaults(t)
	var compact []loadGoldenCompactCase
	prefsDumpReadJSON(t, filepath.Join("testdata", loadGoldenName), &compact)
	full := make([]loadGoldenFullCase, 0, len(compact))
	for _, c := range compact {
		full = append(full, loadGoldenFullCase{c.Name, c.Input, applyPrefsDelta(defaults, c.OutputDelta)})
	}
	b, err := json.MarshalIndent(full, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256hex(b); got != loadGoldenSHA256 {
		t.Errorf("expanded load_golden.json SHA-256 changed: got %s, want %s — the delta no longer reconstructs the frozen bytes", got, loadGoldenSHA256)
	}
}

// TestMalformedExpansion reconstructs the OLD malformed_golden.json bytes from the
// compact delta form and asserts the frozen SHA-256.
func TestMalformedExpansion(t *testing.T) {
	defaults := prefsDefaults(t)
	var compact []malformedCompactCase
	prefsDumpReadJSON(t, filepath.Join("testdata", malformedGoldenName), &compact)
	full := make([]malformedFullCase, 0, len(compact))
	for _, c := range compact {
		full = append(full, malformedFullCase{c.Name, c.Data, c.Error, c.Quarantined, applyPrefsDelta(defaults, c.OutputDelta)})
	}
	b, err := json.MarshalIndent(full, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256hex(b); got != malformedGoldenSHA {
		t.Errorf("expanded malformed_golden.json SHA-256 changed: got %s, want %s — the delta no longer reconstructs the frozen bytes", got, malformedGoldenSHA)
	}
}

// goldenRunRE matches the compacted single-character run token "<char>*<len>" that
// encodeGoldenRun emits (the "huge" string scenarios' 4096-char values).
var goldenRunRE = regexp.MustCompile(`(.)\*(\d+)`)

// decodeGoldenRuns expands every "<char>*<len>" token back to the full run.
func decodeGoldenRuns(s string) string {
	return goldenRunRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := goldenRunRE.FindStringSubmatch(m)
		n, err := strconv.Atoi(sub[2])
		if err != nil {
			return m
		}
		return strings.Repeat(sub[1], n)
	})
}

// TestSetterGoldenExpansion reconstructs the OLD prefs_setters_golden.txt bytes from the
// compact run-length form and asserts the frozen SHA-256.
func TestSetterGoldenExpansion(t *testing.T) {
	raw, err := os.ReadFile(setterGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	full := decodeGoldenRuns(string(raw))
	if got := sha256hex([]byte(full)); got != settersGoldenSHA {
		t.Errorf("expanded prefs_setters_golden.txt SHA-256 changed: got %s, want %s — the run-length form no longer reconstructs the frozen bytes", got, settersGoldenSHA)
	}
}
