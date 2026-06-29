package config

import (
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
		name         string
		exeDir, osDir string
		exists       func(string) bool
		writable     func(string) bool
		wantDir      string
		wantPortable bool
	}{
		{
			name: "existing portable wins even when classic also exists",
			exeDir: exe, osDir: os_, exists: set(portableFile, classicFile), writable: yes,
			wantDir: portable, wantPortable: true,
		},
		{
			name: "existing classic keeps existing users in place",
			exeDir: exe, osDir: os_, exists: set(classicFile), writable: yes,
			wantDir: classic, wantPortable: false,
		},
		{
			name: "fresh + writable exe dir => portable",
			exeDir: exe, osDir: os_, exists: no, writable: yes,
			wantDir: portable, wantPortable: true,
		},
		{
			name: "fresh + read-only exe dir => classic fallback",
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
			name: "no exe path (go test/install w/o exe) => classic",
			exeDir: "", osDir: os_, exists: no, writable: yes,
			wantDir: classic, wantPortable: false,
		},
		{
			name: "no OS dir => portable is the only option",
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
