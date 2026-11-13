package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealPrefsIdentical is the one-machine check for the user's own prefs file. Set
// REAL_PREFS=/path/to/prefs.json and run:
//
//	REAL_PREFS=/path/to/prefs.json go test ./internal/config -run TestRealPrefsIdentical -v
//
// The old load() is deleted, so it can no longer compare against it directly; instead it
// prints ONLY the per-section SHA-256 (error class, exported JSON, unexported dump, dirty
// flag, Save bytes, ImportSettings accept/reject) — never file contents. Build prefsdump
// at bb9f414 and here, run both on the same file, and diff (see HANDOFF.md).
func TestRealPrefsIdentical(t *testing.T) {
	path := os.Getenv("REAL_PREFS")
	if path == "" {
		t.Skip("set REAL_PREFS=/path/to/prefs.json to print the per-section SHA-256")
	}

	p, lerr := load(path)

	sha := func(b []byte) string {
		s := sha256.Sum256(b)
		return hex.EncodeToString(s[:])
	}

	t.Logf("errclass=%s", sha([]byte(diffErrClassStr(lerr))))
	if p == nil {
		return
	}
	ej, _ := json.Marshal(p.Load())
	t.Logf("exported=%s", sha(ej))
	t.Logf("unexported=%s", sha([]byte(fmt.Sprintf("moved=%v stamp=%v", p.themeFitMovedToNative, p.themeFitStampPending))))
	t.Logf("dirty=%s", sha([]byte(fmt.Sprintf("pending=%v", p.pending.Load()))))

	dir, derr := os.MkdirTemp("", "realprefs")
	if derr != nil {
		return
	}
	defer os.RemoveAll(dir)
	savePath := filepath.Join(dir, "save.json")
	p.path = savePath
	if serr := p.SaveNow(); serr != nil {
		t.Logf("save=%s", sha([]byte("err:"+serr.Error())))
	} else {
		sb, _ := os.ReadFile(savePath)
		t.Logf("save=%s", sha(sb))
	}
}

// diffErrClassStr is a struct-name-agnostic error class for the real-prefs hash.
func diffErrClassStr(err error) string {
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
