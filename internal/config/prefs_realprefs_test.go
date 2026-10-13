package config

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestRealPrefsIdentical is the one-machine check for the user's own prefs file. Set
// REAL_PREFS=/path/to/prefs.json and run:
//
//	REAL_PREFS=/path/to/prefs.json go test ./internal/config -run TestRealPrefsIdentical -v
//
// It compares load() (the snapshot path) against a direct snapshotLoad of the same bytes
// and prints ONLY "identical" or "not identical" — never the contents of the file.
func TestRealPrefsIdentical(t *testing.T) {
	path := os.Getenv("REAL_PREFS")
	if path == "" {
		t.Skip("set REAL_PREFS=/path/to/prefs.json to compare the real file against the snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	oldp, err := load(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	newp, perr := snapshotLoad(data)
	if perr != nil {
		t.Fatalf("snapshotLoad: %v", perr)
	}

	oldJSON, _ := json.Marshal(oldp)
	newJSON, _ := json.Marshal(newp)
	var oldM, newM map[string]any
	_ = json.Unmarshal(oldJSON, &oldM)
	_ = json.Unmarshal(newJSON, &newM)

	if reflect.DeepEqual(oldM, newM) {
		t.Log("identical")
	} else {
		t.Log("not identical")
	}
}
