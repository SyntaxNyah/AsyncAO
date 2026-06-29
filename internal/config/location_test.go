package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigBase(t *testing.T) {
	const (
		exe = "/app"
		os_ = "/home/u/.config"
	)
	portable := filepath.Join(exe, PortableDirName)
	classic := filepath.Join(os_, PrefsDirName)
	portableFile := filepath.Join(portable, PrefsFileName)
	classicFile := filepath.Join(classic, PrefsFileName)

	// set builds an exists() over a fixed set of present paths.
	set := func(present ...string) func(string) bool {
		m := map[string]bool{}
		for _, p := range present {
			m[p] = true
		}
		return func(p string) bool { return m[p] }
	}
	yes := func(string) bool { return true }
	no := func(string) bool { return false }

	cases := []struct {
		name          string
		exeDir, osDir string
		exists        func(string) bool
		writable      func(string) bool
		wantDir       string
		wantPortable  bool
	}{
		{
			name:   "existing portable wins even when classic also exists",
			exeDir: exe, osDir: os_, exists: set(portableFile, classicFile), writable: yes,
			wantDir: portable, wantPortable: true,
		},
		{
			name:   "existing classic keeps existing users in place",
			exeDir: exe, osDir: os_, exists: set(classicFile), writable: yes,
			wantDir: classic, wantPortable: false,
		},
		{
			name:   "fresh + writable exe dir => portable",
			exeDir: exe, osDir: os_, exists: no, writable: yes,
			wantDir: portable, wantPortable: true,
		},
		{
			name:   "fresh + read-only exe dir => classic fallback",
			exeDir: exe, osDir: os_, exists: no, writable: no,
			wantDir: classic, wantPortable: false,
		},
		{
			name: "an empty config/ dir must NOT hijack an existing classic user",
			// portable dir exists but holds no prefs file: still classic.
			exeDir: exe, osDir: os_, exists: set(classicFile, portable), writable: yes,
			wantDir: classic, wantPortable: false,
		},
		{
			name:   "no exe path (go test/install w/o exe) => classic",
			exeDir: "", osDir: os_, exists: no, writable: yes,
			wantDir: classic, wantPortable: false,
		},
		{
			name:   "no OS dir => portable is the only option",
			exeDir: exe, osDir: "", exists: no, writable: yes,
			wantDir: portable, wantPortable: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, portable := resolveConfigBase(tc.exeDir, tc.osDir, tc.exists, tc.writable)
			if dir != tc.wantDir {
				t.Errorf("dir = %q, want %q", dir, tc.wantDir)
			}
			if portable != tc.wantPortable {
				t.Errorf("portable = %v, want %v", portable, tc.wantPortable)
			}
		})
	}
}

func TestCopyTree(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "config") // dst should be created

	write := func(rel, body string) {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(PrefsFileName, `{"a":1}`)
	write(JukeboxFileName, `{"playlists":[]}`)
	write(filepath.Join(NotebookDirName, "miku-pizza-deadbeef.json"), `{"lines":["x"]}`)
	write(writeProbeName, "junk") // must be skipped

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for _, rel := range []string{PrefsFileName, JukeboxFileName, filepath.Join(NotebookDirName, "miku-pizza-deadbeef.json")} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing copied %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, writeProbeName)); err == nil {
		t.Errorf("write-probe %q should not have been copied", writeProbeName)
	}
	// Source is left intact (copy, never move).
	if _, err := os.Stat(filepath.Join(src, PrefsFileName)); err != nil {
		t.Errorf("source prefs vanished after copy: %v", err)
	}
}
